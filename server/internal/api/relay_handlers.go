package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"gorm.io/datatypes"

	"github.com/liguopeng80/tokenzen-lite/server/internal/alerting"
	"github.com/liguopeng80/tokenzen-lite/server/internal/auth"
	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/obs"
	"github.com/liguopeng80/tokenzen-lite/server/internal/ratelimit"
	"github.com/liguopeng80/tokenzen-lite/server/internal/relay"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// relayController 承载下游 LLM API 端点（/v1/* 与 /{provider}/v1/*）：
// chat/completions、messages、count_tokens、embeddings、images、models、key/info。
// 认证（API Key）→ 限流/并发闸 → 中继引擎路由，三段在同一 controller 内闭合。
// Alerts 用 alerting.Notifier 端口：未配置通道时装配根传 nil，raiseAlert 显式守卫。
type relayController struct {
	Alerts      alerting.Notifier
	Channels    *store.ChannelRepo
	Departments *store.DepartmentRepo
	Gate        *ratelimit.ConcurrencyGate
	Keys        *store.APIKeyRepo
	Limiter     ratelimit.Limiter
	Models      *store.ModelRepo
	Relay       *relay.Engine
	Settings    *store.SettingsRepo
	Users       *store.UserRepo
}

// apiKeyIdentity 从请求提取并校验 API Key，返回认证身份。
// 失败时已写出 OpenAI 格式错误响应。
func (c *relayController) apiKeyIdentity(w http.ResponseWriter, r *http.Request) (relay.Identity, bool) {
	ctx := r.Context()
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if token == "" {
		token = r.Header.Get("x-api-key") // Anthropic 风格
	}
	if !auth.LooksLikeKey(token) {
		relay.WriteOpenAIError(w, http.StatusUnauthorized, "invalid_api_key", "缺少或非法的 API Key")
		return relay.Identity{}, false
	}
	key, err := c.Keys.GetByHash(ctx, auth.HashKey(token))
	if err != nil {
		relay.WriteOpenAIError(w, http.StatusUnauthorized, "invalid_api_key", "API Key 无效")
		return relay.Identity{}, false
	}
	switch key.Status {
	case domain.KeyEnabled:
	case domain.KeyDisabled:
		relay.WriteOpenAIError(w, http.StatusForbidden, "key_disabled", "API Key 已被禁用")
		return relay.Identity{}, false
	default:
		relay.WriteOpenAIError(w, http.StatusForbidden, "key_unavailable", "API Key 不可用")
		return relay.Identity{}, false
	}
	if key.ExpiresAt != nil && key.ExpiresAt.Before(time.Now()) {
		_ = c.Keys.UpdateFields(ctx, key.ID, 0, map[string]any{"status": domain.KeyExpired})
		relay.WriteOpenAIError(w, http.StatusForbidden, "key_expired", "API Key 已过期")
		return relay.Identity{}, false
	}
	allowedIP, err := keyAllowsIP(key, r)
	if err != nil {
		// 白名单写错时拒绝而非放行：放行等于配置静默失效，与设置白名单的意图相反。
		obs.Logger(ctx).Error("API Key 来源 IP 白名单解析失败，已拒绝调用",
			"key_id", key.ID, "error", err)
		c.raiseAlert(ctx, domain.AlertPolicyMalformed,
			fmt.Sprintf("policy_malformed:key:%d", key.ID),
			"来源 IP 白名单配置有误",
			fmt.Sprintf("API Key #%d 的来源 IP 白名单无法解析，其调用已被拒绝：%v", key.ID, err))
		relay.WriteOpenAIError(w, http.StatusForbidden, "ip_not_allowed",
			"来源 IP 白名单配置有误，请联系管理员")
		return relay.Identity{}, false
	}
	if !allowedIP {
		obs.Logger(ctx).Warn("API Key IP 白名单拦截", "key_id", key.ID, "ip", obs.ClientIP(r))
		relay.WriteOpenAIError(w, http.StatusForbidden, "ip_not_allowed", "来源 IP 不在白名单内")
		return relay.Identity{}, false
	}
	user, err := c.Users.GetByID(ctx, key.UserID)
	if err != nil || user.Status != domain.UserEnabled {
		relay.WriteOpenAIError(w, http.StatusForbidden, "user_disabled", "账号不可用")
		return relay.Identity{}, false
	}
	ident := relay.Identity{User: user, Key: key}
	// 部门承载部门级模型策略，是有效模型集合的最外层；查不到时按未分配处理，
	// 该层即不施加限制。
	if user.DepartmentID != nil && c.Departments != nil {
		if dept, err := c.Departments.GetByID(ctx, *user.DepartmentID); err == nil {
			ident.Department = dept
		} else {
			obs.Logger(ctx).Warn("加载用户所属部门失败，本次不施加部门级模型策略",
				"user_id", user.ID, "department_id", *user.DepartmentID, "error", err)
		}
	}
	return ident, true
}

// raiseAlert 投递一条告警；未配置告警组件时静默跳过（调用点已另有日志）。
func (c *relayController) raiseAlert(ctx context.Context, alertType domain.AlertType,
	dedupKey, title, message string) {

	if c.Alerts == nil {
		return
	}
	c.Alerts.Raise(ctx, alerting.Event{
		Type: alertType, Severity: domain.AlertCritical,
		DedupKey: dedupKey, Title: title, Message: message,
	})
}

// keyAllowsIP 校验来源 IP 是否命中 Key 的白名单（空 = 不限制）。
// 白名单内容无法解析时返回错误，由调用方拒绝请求并告警。
func keyAllowsIP(key *store.APIKey, r *http.Request) (bool, error) {
	if len(key.AllowedIPs) == 0 {
		return true, nil
	}
	var entries []string
	if err := json.Unmarshal(key.AllowedIPs, &entries); err != nil {
		return false, fmt.Errorf("来源 IP 白名单不是字符串数组: %w", err)
	}
	if len(entries) == 0 {
		return true, nil
	}
	// 客户端 IP 以连接远端地址为准；可信代理传递的 X-Real-IP
	// 已由入口的 obs.RealIPMiddleware 改写进 RemoteAddr，此处不再读头。
	ip := net.ParseIP(obs.ClientIP(r))
	if ip == nil {
		return false, nil
	}
	for _, entry := range entries {
		if _, cidr, err := net.ParseCIDR(entry); err == nil {
			if cidr.Contains(ip) {
				return true, nil
			}
		} else if parsed := net.ParseIP(entry); parsed != nil && parsed.Equal(ip) {
			return true, nil
		}
	}
	return false, nil
}

// guardV1 认证 + 双维度限流（按密钥、按用户同时生效取更严者）+
// 分层并发闸门（用户子配额 → 全局总上限 → 大请求配额）；
// 通过后执行 fn，结束释放槽位。
func (c *relayController) guardV1(w http.ResponseWriter, r *http.Request,
	fn func(http.ResponseWriter, *http.Request, relay.Identity)) {

	ident, ok := c.apiKeyIdentity(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	if c.Limiter != nil {
		keyRPM := int(c.Settings.GetInt64(ctx, "rate_limit_per_key_rpm"))
		userRPM := int(c.Settings.GetInt64(ctx, "rate_limit_per_user_rpm"))
		// 两个维度独立计数、同时判定：任一超限即拒绝（取更严者）。
		// 两次 Allow 均执行，被拒绝的请求也计入两个窗口。
		keyOK := c.Limiter.Allow(
			"key:"+strconv.FormatInt(ident.Key.ID, 10), keyRPM, time.Minute)
		userOK := c.Limiter.Allow(
			"user:"+strconv.FormatInt(ident.User.ID, 10), userRPM, time.Minute)
		if !keyOK || !userOK {
			obs.Logger(ctx).Warn("限流拒绝请求",
				"user_id", ident.User.ID, "key_id", ident.Key.ID,
				"key_dim_rejected", !keyOK, "user_dim_rejected", !userOK)
			relay.WriteOpenAIError(w, http.StatusTooManyRequests, "rate_limited", "请求过于频繁，请稍后重试")
			return
		}
	}
	if c.Gate != nil {
		userLimit := int(c.Settings.GetInt64(ctx, "max_concurrent_requests_per_user"))
		limit := int(c.Settings.GetInt64(ctx, "max_concurrent_requests"))
		largeLimit := int(c.Settings.GetInt64(ctx, "max_concurrent_large_requests"))
		large := isLargeRequest(r)
		acquired, reason := c.Gate.Acquire(ident.User.ID, userLimit, limit, largeLimit, large)
		if !acquired {
			obs.Logger(ctx).Warn("并发闸门拒绝请求",
				"reason", reason, "user_id", ident.User.ID, "key_id", ident.Key.ID,
				"large_body", large, "content_length", r.ContentLength)
			relay.WriteOpenAIError(w, http.StatusServiceUnavailable, "overloaded", "服务并发已满，请稍后重试")
			return
		}
		defer c.Gate.Release(ident.User.ID, large)
	}
	fn(w, r, ident)
}

// largeRequestBodyBytes 大请求判定阈值：请求体超过该字节数的请求
// 额外占用独立的小并发配额（max_concurrent_large_requests），
// 防止少量大请求体在 JSON 解码与转发期间耗尽进程内存。
// 与内存预算的换算依据见 docs/deployment.md。
const largeRequestBodyBytes = 1 << 20 // 1 MiB

// isLargeRequest 按 Content-Length 判定大请求；
// 长度未知（chunked 编码）时按大请求保守处理。
func isLargeRequest(r *http.Request) bool {
	return r.ContentLength < 0 || r.ContentLength > largeRequestBodyBytes
}

func (c *relayController) handleV1ChatCompletions(w http.ResponseWriter, r *http.Request) {
	c.guardV1(w, r, c.Relay.HandleChatCompletions)
}

func (c *relayController) handleV1Messages(w http.ResponseWriter, r *http.Request) {
	c.guardV1(w, r, c.Relay.HandleMessages)
}

// handleV1CountTokens 计数端点：不消耗上游 token，因此不计费、不写用量日志，
// 但仍受身份、限流与并发闸门约束——转发到上游时同样占用上游连接。
func (c *relayController) handleV1CountTokens(w http.ResponseWriter, r *http.Request) {
	c.guardV1(w, r, c.Relay.HandleCountTokens)
}

func (c *relayController) handleV1Embeddings(w http.ResponseWriter, r *http.Request) {
	c.guardV1(w, r, c.Relay.HandleEmbeddings)
}

func (c *relayController) handleV1Images(w http.ResponseWriter, r *http.Request) {
	c.guardV1(w, r, c.Relay.HandleImages)
}

// requireProvider 解析 URL 前缀的 provider slug。
// 解析失败时直接写出 404 provider_not_found 并返回 ok=false——必须在 guardV1 之前
// 调用，使未知 slug 不经过认证与限流，不占用限流配额。
// 错误格式为 OpenAI 形态（与认证/限流阶段的约定一致：进入端点业务逻辑之前一律 OpenAI 格式）。
func (c *relayController) requireProvider(w http.ResponseWriter, r *http.Request) (domain.Provider, bool) {
	slug := chi.URLParam(r, "provider")
	provider, ok := relay.SlugToProvider(slug)
	if !ok {
		relay.WriteOpenAIError(w, http.StatusNotFound, domain.ErrCodeProviderNotFound,
			fmt.Sprintf("未知的 provider 前缀 %q", slug))
		return "", false
	}
	return provider, true
}

// withProvider 把解析出的 provider 注入 Identity 后交给 guardV1 继续走认证/限流/业务。
// provider 已由调用方经 requireProvider 校验过。
func (c *relayController) withProvider(provider domain.Provider,
	fn func(http.ResponseWriter, *http.Request, relay.Identity)) func(http.ResponseWriter, *http.Request, relay.Identity) {

	return func(w http.ResponseWriter, r *http.Request, ident relay.Identity) {
		ident.Provider = provider
		fn(w, r, ident)
	}
}

func (c *relayController) handleV1ProviderChatCompletions(w http.ResponseWriter, r *http.Request) {
	provider, ok := c.requireProvider(w, r)
	if !ok {
		return
	}
	c.guardV1(w, r, c.withProvider(provider, c.Relay.HandleChatCompletions))
}

func (c *relayController) handleV1ProviderMessages(w http.ResponseWriter, r *http.Request) {
	provider, ok := c.requireProvider(w, r)
	if !ok {
		return
	}
	c.guardV1(w, r, c.withProvider(provider, c.Relay.HandleMessages))
}

func (c *relayController) handleV1ProviderCountTokens(w http.ResponseWriter, r *http.Request) {
	provider, ok := c.requireProvider(w, r)
	if !ok {
		return
	}
	c.guardV1(w, r, c.withProvider(provider, c.Relay.HandleCountTokens))
}

func (c *relayController) handleV1ProviderEmbeddings(w http.ResponseWriter, r *http.Request) {
	provider, ok := c.requireProvider(w, r)
	if !ok {
		return
	}
	c.guardV1(w, r, c.withProvider(provider, c.Relay.HandleEmbeddings))
}

func (c *relayController) handleV1ProviderImages(w http.ResponseWriter, r *http.Request) {
	provider, ok := c.requireProvider(w, r)
	if !ok {
		return
	}
	c.guardV1(w, r, c.withProvider(provider, c.Relay.HandleImages))
}

// handleV1Models 按有效模型集合过滤的模型清单（OpenAI /v1/models 格式）。
// 仅返回当前存在启用渠道承载的模型，使清单反映实际可用性——
// 无渠道承载的模型即使已上架，调用也必然收到 model_not_found/无可用渠道。
func (c *relayController) handleV1Models(w http.ResponseWriter, r *http.Request) {
	ident, ok := c.apiKeyIdentity(w, r)
	if !ok {
		return
	}
	models, _, err := c.Models.List(r.Context(), store.ModelListFilter{Status: domain.ModelEnabled})
	if err != nil {
		relay.WriteOpenAIError(w, http.StatusInternalServerError, "internal_error", "查询模型失败")
		return
	}
	counts, err := c.Channels.CountEnabledByModel(r.Context())
	if err != nil {
		relay.WriteOpenAIError(w, http.StatusInternalServerError, "internal_error", "查询模型失败")
		return
	}
	data := make([]map[string]any, 0, len(models))
	for _, m := range models {
		// 清单按有效模型集合过滤（部门策略 ∩ 用户策略 ∩ 密钥白名单），
		// 与实际调用时的判定同源，避免清单里列出调用即被拒的模型。
		allowed, err := relay.AllowsModel(departmentPolicyOf(ident), ident.User.AllowedModels,
			ident.Key.AllowedModels, m.Name)
		if err != nil || !allowed {
			continue
		}
		if counts[m.Name] == 0 {
			continue
		}
		data = append(data, map[string]any{
			"id": m.Name, "object": "model", "owned_by": "token-zen",
			"created": m.CreatedAt.Unix(),
		})
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": data})
}

// departmentPolicyOf 取身份上的部门级模型策略，未分配部门时为空。
func departmentPolicyOf(ident relay.Identity) datatypes.JSON {
	if ident.Department == nil {
		return nil
	}
	return ident.Department.AllowedModels
}

// handleV1ProviderModels /{provider}/v1/models：只返回 model.provider == URL provider
// 且存在同 provider 启用渠道承载的模型（现有 carriage 过滤 + provider 过滤叠加）。
// 仍在有效模型集合内过滤（部门策略 ∩ 用户策略 ∩ 密钥白名单），provider 前缀不放松权限。
func (c *relayController) handleV1ProviderModels(w http.ResponseWriter, r *http.Request) {
	provider, ok := c.requireProvider(w, r)
	if !ok {
		return
	}
	ident, ok := c.apiKeyIdentity(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	models, _, err := c.Models.List(ctx, store.ModelListFilter{Status: domain.ModelEnabled})
	if err != nil {
		relay.WriteOpenAIError(w, http.StatusInternalServerError, "internal_error", "查询模型失败")
		return
	}
	// 承载集合：仅计入 ch.Provider == provider 的启用渠道（复用既有 List 查询，不新增 SQL）。
	// 与 /v1/models 的 CountEnabledByModel 口径一致——此处按 provider 收窄后等价于
	// 「该 provider 的启用渠道是否承载该模型」。
	channels, _, err := c.Channels.List(ctx, store.ChannelListFilter{Status: domain.ChannelEnabled})
	if err != nil {
		relay.WriteOpenAIError(w, http.StatusInternalServerError, "internal_error", "查询模型失败")
		return
	}
	carried := make(map[string]bool)
	for _, ch := range channels {
		if ch.Provider != provider {
			continue
		}
		for _, name := range ch.ModelList() {
			carried[name] = true
		}
	}
	data := make([]map[string]any, 0, len(models))
	for _, m := range models {
		allowed, err := relay.AllowsModel(departmentPolicyOf(ident), ident.User.AllowedModels,
			ident.Key.AllowedModels, m.Name)
		if err != nil || !allowed {
			continue
		}
		if domain.Provider(m.Provider) != provider {
			continue
		}
		if !carried[m.Name] {
			continue
		}
		data = append(data, map[string]any{
			"id": m.Name, "object": "model", "owned_by": "token-zen",
			"created": m.CreatedAt.Unix(),
		})
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": data})
}

// handleV1KeyInfo 程序化配额查询：Key 剩余额度 + 用户积分余额。
func (c *relayController) handleV1KeyInfo(w http.ResponseWriter, r *http.Request) {
	ident, ok := c.apiKeyIdentity(w, r)
	if !ok {
		return
	}
	var keyRemaining any // null 表示不限额
	if ident.Key.CreditLimit != nil {
		remaining := *ident.Key.CreditLimit - ident.Key.CreditUsed
		if remaining < 0 {
			remaining = 0
		}
		keyRemaining = remaining
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"key_name":             ident.Key.Name,
		"key_credit_limit":     ident.Key.CreditLimit,
		"key_credit_used":      ident.Key.CreditUsed,
		"key_credit_remaining": keyRemaining,
		"user_credit_balance":  ident.User.CreditBalance,
		"user_credit_used":     ident.User.CreditUsed,
	})
}
