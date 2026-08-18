package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"time"

	"gorm.io/datatypes"

	"github.com/liguopeng80/tokenzen-lite/server/internal/api/respond"
	"github.com/liguopeng80/tokenzen-lite/server/internal/audit"
	"github.com/liguopeng80/tokenzen-lite/server/internal/auth"
	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/obs"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// meKeysController 承载员工自助 API Key 管理端点（/me/keys）：列出/签发/查询/改/删。
// 仅持本 feature 所需依赖。
type meKeysController struct {
	Audit    *audit.Recorder
	Keys     *store.APIKeyRepo
	Projects *store.ProjectRepo
	Settings *store.SettingsRepo
}

func (c *meKeysController) handleMeListKeys(w http.ResponseWriter, r *http.Request) {
	u := auth.CurrentUser(r.Context())
	page, pageSize := PageParams(r)
	q := r.URL.Query()
	keys, total, err := c.Keys.List(r.Context(), store.APIKeyListFilter{
		UserID:   u.ID,
		Keyword:  q.Get("keyword"),
		Status:   domain.KeyStatus(q.Get("status")),
		Page:     page,
		PageSize: pageSize,
	})
	if err != nil {
		obs.Logger(r.Context()).Error("查询密钥列表失败", "error", err, "user_id", u.ID)
		respond.Fail(w, http.StatusInternalServerError, "查询密钥列表失败")
		return
	}
	rate := c.Settings.GetInt64(r.Context(), "exchange_rate_credits_per_cny")
	mc := newMoneyCtx(rate)
	respond.OK(w, respond.NewPage(page, pageSize, total,
		wrapList(keys, func(k store.APIKey) apiKeyWithMoney {
			return wrapAPIKey(k, mc)
		})))
}

type keyPayload struct {
	Name          string          `json:"name"`
	CreditLimit   *domain.Credits `json:"credit_limit"`
	ExpiresAt     *time.Time      `json:"expires_at"`
	AllowedModels []string        `json:"allowed_models"`
	AllowedIPs    []string        `json:"allowed_ips"`
	// DailySpendLimit 该 Key 单自然日累计扣费上限（积分），0 表示不限制。
	DailySpendLimit *domain.Credits `json:"daily_spend_limit"`
	// ProjectID 归属项目 ID，nil 表示未归属项目。
	ProjectID *int64 `json:"project_id"`
	// IdempotencyKey 仅管理端代签发使用（R4）；员工自助签发忽略此字段。
	IdempotencyKey string `json:"idempotency_key"`
}

// validateKeyPayload 校验公共字段；错误时返回用户可读信息。
func validateKeyPayload(p *keyPayload) string {
	if p.Name == "" || len(p.Name) > 64 {
		return "密钥名称必填且不超过 64 字符"
	}
	if p.CreditLimit != nil && *p.CreditLimit < 0 {
		return "额度不能为负数"
	}
	if p.DailySpendLimit != nil && *p.DailySpendLimit < 0 {
		return "每日花费上限不能为负数"
	}
	for _, ipStr := range p.AllowedIPs {
		if _, _, err := net.ParseCIDR(ipStr); err != nil {
			if net.ParseIP(ipStr) == nil {
				return "IP 白名单包含非法条目: " + ipStr
			}
		}
	}
	return ""
}

func toJSONField(v any) datatypes.JSON {
	if v == nil {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return datatypes.JSON(b)
}

type createdKeyResponse struct {
	store.APIKey
	// Key 明文仅在创建响应中出现一次
	Key string `json:"key"`
}

func (c *meKeysController) handleMeCreateKey(w http.ResponseWriter, r *http.Request) {
	u := auth.CurrentUser(r.Context())
	var req keyPayload
	if !Bind(w, r, &req) {
		return
	}
	if msg := validateKeyPayload(&req); msg != "" {
		respond.Fail(w, http.StatusBadRequest, msg)
		return
	}
	// 单用户密钥数量上限（0 = 不限制）：防止通过批量建 Key 放大按密钥限流配额。
	if maxKeys := c.Settings.GetInt64(r.Context(), "max_keys_per_user"); maxKeys > 0 {
		count, err := c.Keys.CountByUser(r.Context(), u.ID)
		if err != nil {
			obs.Logger(r.Context()).Error("统计密钥数量失败", "error", err, "user_id", u.ID)
			respond.Fail(w, http.StatusInternalServerError, "创建密钥失败")
			return
		}
		if count >= maxKeys {
			respond.Fail(w, http.StatusBadRequest,
				fmt.Sprintf("密钥数量已达上限（%d 个），请删除不再使用的密钥后重试", maxKeys))
			return
		}
	}
	gen, err := auth.GenerateKey()
	if err != nil {
		obs.Logger(r.Context()).Error("生成密钥失败", "error", err)
		respond.Fail(w, http.StatusInternalServerError, "生成密钥失败")
		return
	}
	k := &store.APIKey{
		UserID:      u.ID,
		Name:        req.Name,
		KeyHash:     gen.Hash,
		KeyPrefix:   gen.Prefix,
		Status:      domain.KeyEnabled,
		CreditLimit: req.CreditLimit,
		ExpiresAt:   req.ExpiresAt,
	}
	if req.DailySpendLimit != nil {
		k.DailySpendLimit = *req.DailySpendLimit
	}
	// 项目归属校验：跨作用域或不存在一律拒绝（不暴露存在性）。
	pid, msg := resolveKeyProject(r.Context(), c.Projects, req.ProjectID, u)
	if msg != "" {
		respond.Fail(w, http.StatusBadRequest, msg)
		return
	}
	k.ProjectID = pid
	if len(req.AllowedModels) > 0 {
		k.AllowedModels = toJSONField(req.AllowedModels)
	}
	if len(req.AllowedIPs) > 0 {
		k.AllowedIPs = toJSONField(req.AllowedIPs)
	}
	if err := c.Keys.Create(r.Context(), k); err != nil {
		obs.Logger(r.Context()).Error("创建密钥失败", "error", err, "user_id", u.ID)
		respond.Fail(w, http.StatusInternalServerError, "创建密钥失败")
		return
	}
	obs.Logger(r.Context()).Info("创建 API Key", "user_id", u.ID, "key_id", k.ID)
	// 员工自助的密钥操作同样进审计：密钥泄漏后要能查出它何时由谁创建。
	// 快照不含密钥明文与哈希，只记前缀与限制条件。
	c.Audit.Record(r, audit.Entry{
		Action: domain.AuditAPIKeyCreate, TargetType: domain.AuditTargetAPIKey,
		TargetID: k.ID, TargetName: k.Name, After: apiKeySnapshot(k),
	})
	respond.Created(w, createdKeyResponse{APIKey: *k, Key: gen.Plain})
}

func (c *meKeysController) handleMeGetKey(w http.ResponseWriter, r *http.Request) {
	u := auth.CurrentUser(r.Context())
	id, ok := IDParam(w, r, "id")
	if !ok {
		return
	}
	k, err := c.Keys.GetByID(r.Context(), id)
	if err != nil || k.UserID != u.ID {
		respond.Fail(w, http.StatusNotFound, "密钥不存在")
		return
	}
	rate := c.Settings.GetInt64(r.Context(), "exchange_rate_credits_per_cny")
	respond.OK(w, wrapAPIKey(*k, newMoneyCtx(rate)))
}

type updateKeyRequest struct {
	Name            *string           `json:"name"`
	Status          *domain.KeyStatus `json:"status"`
	CreditLimit     *domain.Credits   `json:"credit_limit"`
	ClearLimit      bool              `json:"clear_limit"`
	ExpiresAt       *time.Time        `json:"expires_at"`
	ClearExpires    bool              `json:"clear_expires"`
	AllowedModels   []string          `json:"allowed_models"`
	AllowedIPs      []string          `json:"allowed_ips"`
	DailySpendLimit *domain.Credits   `json:"daily_spend_limit"`
	ClearDailyLimit bool              `json:"clear_daily_limit"`
	// ProjectID 非 nil 时把密钥改挂到指定项目；ClearProject 为 true 时置为未归属。
	ProjectID    *int64 `json:"project_id"`
	ClearProject bool   `json:"clear_project"`
}

func (c *meKeysController) handleMeUpdateKey(w http.ResponseWriter, r *http.Request) {
	u := auth.CurrentUser(r.Context())
	id, ok := IDParam(w, r, "id")
	if !ok {
		return
	}
	var req updateKeyRequest
	if !Bind(w, r, &req) {
		return
	}
	fields := map[string]any{}
	if req.Name != nil {
		if *req.Name == "" || len(*req.Name) > 64 {
			respond.Fail(w, http.StatusBadRequest, "密钥名称必填且不超过 64 字符")
			return
		}
		fields["name"] = *req.Name
	}
	if req.Status != nil {
		// 用户只能在启用/禁用之间切换，expired/depleted 由系统判定
		if *req.Status != domain.KeyEnabled && *req.Status != domain.KeyDisabled {
			respond.Fail(w, http.StatusBadRequest, "状态只能设置为 enabled 或 disabled")
			return
		}
		fields["status"] = *req.Status
	}
	if req.ClearLimit {
		fields["credit_limit"] = nil
	} else if req.CreditLimit != nil {
		if *req.CreditLimit < 0 {
			respond.Fail(w, http.StatusBadRequest, "额度不能为负数")
			return
		}
		fields["credit_limit"] = *req.CreditLimit
	}
	if req.ClearDailyLimit {
		fields["daily_spend_limit"] = int64(0)
	} else if req.DailySpendLimit != nil {
		if *req.DailySpendLimit < 0 {
			respond.Fail(w, http.StatusBadRequest, "每日花费上限不能为负数")
			return
		}
		fields["daily_spend_limit"] = int64(*req.DailySpendLimit)
	}
	if req.ClearExpires {
		fields["expires_at"] = nil
	} else if req.ExpiresAt != nil {
		fields["expires_at"] = *req.ExpiresAt
	}
	if req.AllowedModels != nil {
		fields["allowed_models"] = toJSONField(req.AllowedModels)
	}
	if req.AllowedIPs != nil {
		for _, ipStr := range req.AllowedIPs {
			if _, _, err := net.ParseCIDR(ipStr); err != nil && net.ParseIP(ipStr) == nil {
				respond.Fail(w, http.StatusBadRequest, "IP 白名单包含非法条目: "+ipStr)
				return
			}
		}
		fields["allowed_ips"] = toJSONField(req.AllowedIPs)
	}
	if req.ClearProject {
		fields["project_id"] = nil
	} else if req.ProjectID != nil {
		pid, msg := resolveKeyProject(r.Context(), c.Projects, req.ProjectID, u)
		if msg != "" {
			respond.Fail(w, http.StatusBadRequest, msg)
			return
		}
		fields["project_id"] = pid
	}
	if len(fields) == 0 {
		respond.Fail(w, http.StatusBadRequest, "没有可更新的字段")
		return
	}
	// 先读改动前的快照，审计才能表达「改成了什么」而不只是「改过」。
	before, err := c.Keys.GetByID(r.Context(), id)
	if err != nil || before.UserID != u.ID {
		respond.Fail(w, http.StatusNotFound, "密钥不存在")
		return
	}
	// 先算差异：UpdateFields 会往 fields 里塞 updated_at，算完再调用可避免它进审计。
	changedBefore, changedAfter := audit.Diff(apiKeySnapshot(before), fields)
	if err := c.Keys.UpdateFields(r.Context(), id, u.ID, fields); err != nil {
		respond.Fail(w, http.StatusNotFound, "密钥不存在")
		return
	}
	c.Audit.Record(r, audit.Entry{
		Action: domain.AuditAPIKeyUpdate, TargetType: domain.AuditTargetAPIKey,
		TargetID: id, TargetName: before.Name,
		Before: changedBefore, After: changedAfter,
	})
	// 额度上限被上调或清除后，depleted（额度耗尽）状态的密钥自动恢复为 enabled；
	// 新上限仍不高于已用额度时保持 depleted。本次请求同时显式改了状态时，
	// 状态已非 depleted，恢复语句自然不生效。
	if req.ClearLimit || req.CreditLimit != nil {
		if err := c.Keys.RestoreDepleted(r.Context(), id, u.ID); err != nil {
			obs.Logger(r.Context()).Warn("额度调整后恢复密钥状态失败",
				"error", err, "user_id", u.ID, "key_id", id)
		}
	}
	respond.OK(w, nil)
}

func (c *meKeysController) handleMeDeleteKey(w http.ResponseWriter, r *http.Request) {
	u := auth.CurrentUser(r.Context())
	id, ok := IDParam(w, r, "id")
	if !ok {
		return
	}
	k, err := c.Keys.GetByID(r.Context(), id)
	if err != nil || k.UserID != u.ID {
		respond.Fail(w, http.StatusNotFound, "密钥不存在")
		return
	}
	if err := c.Keys.Delete(r.Context(), id, u.ID); err != nil {
		respond.Fail(w, http.StatusNotFound, "密钥不存在")
		return
	}
	obs.Logger(r.Context()).Info("删除 API Key", "user_id", u.ID, "key_id", id)
	c.Audit.Record(r, audit.Entry{
		Action: domain.AuditAPIKeyDelete, TargetType: domain.AuditTargetAPIKey,
		TargetID: id, TargetName: k.Name, Before: apiKeySnapshot(k),
	})
	respond.OK(w, nil)
}

// apiKeySnapshot 把密钥压成审计用的字段快照。不含密钥明文与哈希：
// 审计表的可读范围比密钥本身更宽，快照只记前缀与限制条件。
func apiKeySnapshot(k *store.APIKey) map[string]any {
	if k == nil {
		return nil
	}
	return map[string]any{
		"name": k.Name, "key_prefix": k.KeyPrefix, "status": k.Status,
		"credit_limit": k.CreditLimit, "expires_at": k.ExpiresAt,
		"daily_spend_limit": int64(k.DailySpendLimit),
		"project_id":        k.ProjectID,
		"allowed_models":    json.RawMessage(nullIfEmptyJSON(k.AllowedModels)),
		"allowed_ips":       json.RawMessage(nullIfEmptyJSON(k.AllowedIPs)),
	}
}

// resolveKeyProject 校验密钥归属项目的合法性：nil 表示未归属（合法，直接放行）；
// 非 nil 时要求项目存在且与密钥属主（owner）同接入方作用域——密钥与其归属项目
// 须在同一作用域，避免跨接入方的成本归集串台。跨作用域按「不存在」处理，
// 不暴露项目存在性（与 loadProject/loadManagedUser 的一致性原则）。
// 返回值：经校验的 projectID（可直接写入 APIKey.ProjectID）与错误信息（空串表示通过）。
// resolveKeyProject 解析密钥归属项目并校验其与属主同处一个接入方作用域。
// 跨 meKeys / userAdmin 两组 controller 共用：员工自助与管理员代签发走同一校验。
func resolveKeyProject(ctx context.Context, projects *store.ProjectRepo, projectID *int64, owner *store.User) (*int64, string) {
	if projectID == nil {
		return nil, ""
	}
	p, err := projects.GetByID(ctx, *projectID)
	if err != nil {
		return nil, "项目不存在"
	}
	if !projectMatchesOwnerScope(p, owner) {
		return nil, "项目不存在"
	}
	return projectID, ""
}

// projectMatchesOwnerScope 判断项目是否与密钥属主同处一个接入方作用域。
// 内部对象（integration_id 均为 NULL）相互匹配；托管对象要求 integration_id 相等。
func projectMatchesOwnerScope(p *store.Project, owner *store.User) bool {
	if owner == nil {
		return false
	}
	if p.IntegrationID == nil && owner.IntegrationID == nil {
		return true
	}
	if p.IntegrationID != nil && owner.IntegrationID != nil {
		return *p.IntegrationID == *owner.IntegrationID
	}
	return false
}
