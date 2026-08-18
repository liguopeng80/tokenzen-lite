package relay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"gorm.io/datatypes"
	"gorm.io/gorm"

	"github.com/liguopeng80/tokenzen-lite/server/internal/alerting"
	"github.com/liguopeng80/tokenzen-lite/server/internal/billing"
	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/obs"
	"github.com/liguopeng80/tokenzen-lite/server/internal/pricing"
	"github.com/liguopeng80/tokenzen-lite/server/internal/secrets"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
	"github.com/liguopeng80/tokenzen-lite/server/internal/strutil"
)

// Engine 是中继引擎，持有全部依赖。
type Engine struct {
	DB        *gorm.DB
	Channels  *store.ChannelRepo
	Costs     *store.ChannelCostRepo
	Models    *store.ModelRepo
	Billing   *billing.Service
	UsageLogs *store.UsageLogRepo
	Settings  *store.SettingsRepo
	Secrets   *secrets.Box
	Client    *http.Client
	// Spend 每日花费计数，用于用户级花费上限校验；为 nil 时不校验。
	Spend *store.SpendRepo
	// Alerts 主动告警通道；为 nil 时只记日志不投递。
	Alerts alerting.Notifier
	// UpstreamTimeout 上游请求独立超时（含读完响应体；零值用 defaultUpstreamTimeout）。
	// 上游上下文与下游断连解耦后，该超时是上游读取时长的唯一保证，
	// 不依赖 Client.Timeout 是否配置。
	UpstreamTimeout time.Duration
	// Usage 用量日志/录制落库入口（含时钟、录制目录、异步写入器）。
	// nil 时由 usage() 懒初始化，兼容测试以字面量构造 Engine。
	Usage *UsageSink
	// Selector 渠道选择器（provider 过滤后的加权随机 + 亲和绑定）。
	// nil 时由 selector() 懒初始化，兼容测试以字面量构造 Engine。
	Selector *ChannelSelector
	// Health 渠道健康跟踪（连续失败计数 + 自动禁用 + 半开探活）。
	// nil 时由 health() 懒初始化，兼容测试以字面量构造 Engine。
	Health *ChannelHealth
	// usageOnce/selectorOnce/healthOnce 守护懒初始化的并发安全：字面量构造的
	// Engine 在并发首请求时各协作对象只初始化一次。生产路径由 buildDeps 预注入，
	// Once.Do 内的 nil 判空保证预注入实例不被覆盖。
	usageOnce    sync.Once
	selectorOnce sync.Once
	healthOnce   sync.Once
}

// raiseAlert 投递一条告警；未配置告警通道时静默跳过（调用点已另有日志）。
func (e *Engine) raiseAlert(ctx context.Context, alertType domain.AlertType,
	dedupKey, title, message string) {

	if e.Alerts == nil {
		return
	}
	severity := domain.AlertWarning
	if alertType == domain.AlertChannelAutoDisabled || alertType == domain.AlertPolicyMalformed {
		severity = domain.AlertCritical
	}
	e.Alerts.Raise(ctx, alerting.Event{
		Type: alertType, Severity: severity,
		DedupKey: dedupKey, Title: title, Message: message,
	})
}

// usage 返回用量日志/录制落库入口（懒初始化，兼容测试以字面量构造 Engine）。
// 生产路径由 buildDeps 预先注入；本访问器为 &Engine{} 字面量构造且未预置 Usage 的
// 测试保留——按 Engine 的 UsageLogs/Settings 构造默认 sink（不启用录制、用默认时钟）。
// 需要录制/可控时钟的测试应在构造 Engine 时显式传入 Usage sink。
func (e *Engine) usage() *UsageSink {
	e.usageOnce.Do(func() {
		if e.Usage == nil {
			e.Usage = NewUsageSink(e.UsageLogs, e.Settings, "", nil)
		}
	})
	return e.Usage
}

// selector 返回渠道选择器（懒初始化，兼容测试以字面量构造 Engine）。
// 生产路径由 buildDeps 预先注入；本访问器仅为 &Engine{} 字面量构造的测试保留。
func (e *Engine) selector() *ChannelSelector {
	e.selectorOnce.Do(func() {
		if e.Selector == nil {
			e.Selector = NewChannelSelector()
		}
	})
	return e.Selector
}

// health 返回渠道健康跟踪器（懒初始化，兼容测试以字面量构造 Engine）。
// 生产路径由 buildDeps 预先注入；本访问器为 &Engine{} 字面量构造的测试保留。
func (e *Engine) health() *ChannelHealth {
	e.healthOnce.Do(func() {
		if e.Health == nil {
			e.Health = NewChannelHealth(e.Channels, e.Secrets, e.Client, e.Settings, e.Alerts)
		}
	})
	return e.Health
}

// StartRecoveryProbe 启动自动禁用渠道的定时半开探测循环。
// 外部 API（main.go startBackground）；实际编排委托给 ChannelHealth。
func (e *Engine) StartRecoveryProbe(ctx context.Context) {
	e.health().StartRecoveryProbe(ctx)
}

// ProbeAutoDisabledChannels 对全部自动禁用渠道各做一次半开探测。
// 外部 API（api 集成测试触发主动探测）；实际编排委托给 ChannelHealth。
func (e *Engine) ProbeAutoDisabledChannels(ctx context.Context) {
	e.health().ProbeAutoDisabledChannels(ctx)
}

// Close 停机前刷盘用量日志队列与录制队列；ctx 到期则放弃等待。
// 外部 API（main.go 停机、api 测试）经由本方法；实际刷盘委托给 UsageSink。
func (e *Engine) Close(ctx context.Context) {
	e.usage().Close(ctx)
}

// DroppedUsageLogCount 返回用量日志累计丢弃条数（健康接口暴露，运维告警依据）。
// 外部 API（/healthz）；实际计数委托给 UsageSink。
func (e *Engine) DroppedUsageLogCount() int64 {
	return e.usage().DroppedCount()
}

// Identity 是 /v1 请求的认证结果。
type Identity struct {
	User *store.User
	Key  *store.APIKey
	// Department 是用户所属部门，未分配时为 nil。承载部门级模型策略。
	Department *store.Department
	// Provider 锁定 /{provider}/v1/* 前缀路由的候选渠道厂商。零值（空串）表示
	// 无前缀约束——对应 /v1/* 入口，候选集跨 provider 容错（现有行为）。
	// 非零值时：候选渠道按 provider 收窄，且请求体 model 的归属厂商必须与之
	// 一致（prepareModel 内校验），不一致返回 model_provider_mismatch。
	Provider domain.Provider
}

// DepartmentID 返回用户所属部门 ID，未分配时为 0。
// 该值作为记账时点快照写入用量日志，使报表口径不随用户后续转部门而变。
func (i Identity) DepartmentID() int64 {
	if i.User == nil || i.User.DepartmentID == nil {
		return 0
	}
	return *i.User.DepartmentID
}

// IntegrationID 返回用户所属接入方 ID，未托管时为 0（本机直管账号）。
// 与 DepartmentID 同为记账时点快照写入用量日志，使托管视角的用量列表与
// 报表按接入方隔离，不混入他接入方的记录。
func (i Identity) IntegrationID() int64 {
	if i.User == nil || i.User.IntegrationID == nil {
		return 0
	}
	return *i.User.IntegrationID
}

// ProjectID 返回该密钥归属的项目 ID，未归属时为 0。
// 与 DepartmentID 同为记账时点快照写入用量日志，使报表口径不随密钥后续改挂项目而变。
// 取值来自密钥而非用户：项目归属是密钥维度的成本归集，与用户所属部门正交。
func (i Identity) ProjectID() int64 {
	if i.Key == nil || i.Key.ProjectID == nil {
		return 0
	}
	return *i.Key.ProjectID
}

// departmentPolicy 返回部门级模型策略，未分配部门时为空。
func (i Identity) departmentPolicy() datatypes.JSON {
	if i.Department == nil {
		return nil
	}
	return i.Department.AllowedModels
}

// errWriter 下游协议对应的错误输出。
type errWriter func(w http.ResponseWriter, status int, code, message string)

// WriteOpenAIError 以 OpenAI 错误格式输出。
func WriteOpenAIError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{"message": message, "type": code, "code": code},
	})
}

// WriteAnthropicError 以 Anthropic 错误格式输出。
func WriteAnthropicError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"type":  "error",
		"error": map[string]any{"type": code, "message": message},
	})
}

// relayError 面向下游的错误。
type relayError struct {
	status  int
	code    string
	message string
}

// maxRelayBodyBytes 限制中继请求体（图像 base64 需要较大空间）。
const maxRelayBodyBytes = 20 << 20 // 20 MiB

// defaultUpstreamTimeout 上游请求独立超时的默认值（与 TZL_UPSTREAM_TIMEOUT_SEC 默认一致）。
const defaultUpstreamTimeout = 600 * time.Second

// upstreamContext 构建上游请求上下文（架构决策 2026-08-05 第 3 项）：
// 与下游连接解耦（WithoutCancel，客户端断连不取消上游请求），并附加独立超时，
// 防止解耦后上游读取失去时长上界。保留原上下文中的值（request_id 等日志字段）。
// 调用方必须在读完上游响应体之后才 cancel，提前取消会中断流式读取。
func (e *Engine) upstreamContext(ctx context.Context) (context.Context, context.CancelFunc) {
	timeout := e.UpstreamTimeout
	if timeout <= 0 {
		timeout = defaultUpstreamTimeout
	}
	return context.WithTimeout(context.WithoutCancel(ctx), timeout)
}

// HandleChatCompletions 处理 POST /v1/chat/completions（OpenAI 下游）。
func (e *Engine) HandleChatCompletions(w http.ResponseWriter, r *http.Request, ident Identity) {
	e.handleChat(w, r, ident, dsOpenAI, WriteOpenAIError)
}

// HandleMessages 处理 POST /v1/messages（Anthropic 下游，Claude Code 场景）。
func (e *Engine) HandleMessages(w http.ResponseWriter, r *http.Request, ident Identity) {
	e.handleChat(w, r, ident, dsAnthropic, WriteAnthropicError)
}

// handleChat 两个下游共用的对话中继流程。
func (e *Engine) handleChat(w http.ResponseWriter, r *http.Request, ident Identity,
	ds downstream, writeErr errWriter) {

	ctx := r.Context()
	start := e.now()

	r.Body = http.MaxBytesReader(w, r.Body, maxRelayBodyBytes)
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_request_error", "请求体不是合法 JSON")
		return
	}
	publicModel, _ := body["model"].(string)
	if publicModel == "" {
		writeErr(w, http.StatusBadRequest, "invalid_request_error", "缺少 model 字段")
		return
	}
	// 全局别名解析（docs/glossary.md「模型能力属性」节）：把对外短名（如 opus）
	// 解析为真实模型名，先于渠道 model_mapping 生效。解析失败按原名继续，不构成错误。
	if e.Models != nil {
		if name, err := e.Models.ResolveAlias(ctx, publicModel); err == nil && name != "" {
			publicModel = name
		}
		// 大小写归一：客户端模型名与目录仅大小写不同时，归一到目录规范名，
		// 使白名单/存在性/渠道匹配不受客户端大小写影响（上游对该参数大小写不敏感）。
		if name, err := e.Models.ResolveNameFold(ctx, publicModel); err == nil && name != "" {
			publicModel = name
		}
	}
	stream, _ := body["stream"].(bool)

	model, price, multiplier, ok := e.prepareModel(ctx, w, ident, publicModel,
		domain.ModalityText, domain.BillPerToken, writeErr)
	if !ok {
		return
	}

	// 预扣费
	promptEstimate := estimatePromptTokens(body)
	minTokens := e.Settings.GetInt64(ctx, "precharge_min_tokens")
	if promptEstimate < minTokens {
		promptEstimate = minTokens
	}
	maxTokens := e.Settings.GetInt64(ctx, "precharge_default_max_tokens")
	if mt, ok := body["max_tokens"].(float64); ok && mt > 0 {
		maxTokens = int64(mt)
	}
	precharge := pricing.CalcTokenCredits(domain.NormalizedUsage{
		BaseInput: promptEstimate, Output: maxTokens,
	}, price, multiplier)

	requestID := obs.RequestID(ctx)
	session := NewBillingSession(e.DB, e.Billing, requestID, ident.Key, ident.User.DailySpendLimit)
	if err := e.checkDailySpendLimit(ctx, ident, precharge); err != nil {
		e.rejectPrecharge(w, ctx, ident, publicModel, stream, start, multiplier, err, writeErr)
		return
	}
	if err := session.Precharge(ctx, precharge); err != nil {
		e.rejectPrecharge(w, ctx, ident, publicModel, stream, start, multiplier, err, writeErr)
		return
	}
	defer session.EnsureFinal(ctx) // 退款写入在 Refund 内部脱离取消并施加截止时间

	log := &store.UsageLog{
		RequestID: requestID, UserID: ident.User.ID, APIKeyID: ident.Key.ID,
		DepartmentID:  ident.DepartmentID(),
		ProjectID:     ident.ProjectID(),
		IntegrationID: ident.IntegrationID(),
		ModelName:     publicModel, IsStream: stream,
		PeakMultiplierPercent: multiplier, CreditsPrecharged: precharge,
		ClientIP: clientIP(r),
	}
	rec := e.usage().maybeNewRecorder(ctx, r, body, stream)
	e.relayWithRetry(w, r, session, body, model, ds, ident.Key.ID, price, multiplier, stream, start, log, writeErr, rec, ident.Provider)
}

// prepareModel 加载模型、校验白名单与定价，并校验模型的形态与计费方式和当前端点匹配。
// wantModality/wantBilling 是当前端点期望的模型形态与计费方式；不匹配返回 400，
// 防止按次计费或非对话形态的模型从 token 计费端点进入导致零扣费。失败时已写出响应。
func (e *Engine) prepareModel(ctx context.Context, w http.ResponseWriter, ident Identity,
	publicModel string, wantModality domain.Modality, wantBilling domain.BillingMode,
	writeErr errWriter) (*store.Model, pricing.Price, int, bool) {

	allowed, err := identityAllowsModel(ident, publicModel)
	if err != nil {
		// 策略解析失败按拒绝处理并告警：放行会让写错的策略静默失效。
		obs.Logger(ctx).Error("模型策略解析失败，已拒绝调用",
			"user_id", ident.User.ID, "key_id", ident.Key.ID, "model", publicModel, "error", err)
		e.raiseAlert(ctx, domain.AlertPolicyMalformed,
			fmt.Sprintf("policy_malformed:user:%d", ident.User.ID),
			"模型策略配置有误",
			fmt.Sprintf("用户 %s 的模型策略无法解析，其调用已被拒绝：%v", ident.User.Username, err))
		writeErr(w, http.StatusForbidden, "model_not_allowed",
			"模型访问策略配置有误，请联系管理员")
		return nil, pricing.Price{}, 0, false
	}
	if !allowed {
		writeErr(w, http.StatusForbidden, "model_not_allowed", "该密钥无权访问模型 "+publicModel)
		return nil, pricing.Price{}, 0, false
	}
	model, err := e.Models.GetByName(ctx, publicModel)
	if err != nil || model.Status != domain.ModelEnabled {
		writeErr(w, http.StatusNotFound, "model_not_found", "模型不存在或未上架: "+publicModel)
		return nil, pricing.Price{}, 0, false
	}
	// provider 前缀路由（/{provider}/v1/*）的一致校验：URL 前缀锁定的 provider
	// 必须与请求体 model 的归属厂商（models.provider）一致。判定发生在模型别名
	// 解析之后（publicModel 在调用方已 ResolveAlias）、模型记录加载之后，依据是
	// models.provider 字段（glossary：「模型归属厂商由 models.provider 字段记录」），
	// 权威且唯一，不依赖渠道配置。provider 前缀不放松任何现有约束（白名单等仍生效）。
	if ident.Provider != "" && domain.Provider(model.Provider) != ident.Provider {
		writeErr(w, http.StatusBadRequest, domain.ErrCodeModelProviderMismatch,
			fmt.Sprintf("URL 前缀 provider %q 与模型 %q 的归属厂商 %q 不一致",
				ident.Provider, publicModel, model.Provider))
		return nil, pricing.Price{}, 0, false
	}
	if model.Modality != wantModality {
		writeErr(w, http.StatusBadRequest, "model_endpoint_mismatch",
			fmt.Sprintf("模型 %s 不支持本端点，请使用 %s 调用",
				publicModel, endpointForModality(model.Modality)))
		return nil, pricing.Price{}, 0, false
	}
	if model.BillingMode != wantBilling {
		obs.Logger(ctx).Warn("模型计费方式与端点不匹配，拒绝请求",
			"model", publicModel, "billing_mode", model.BillingMode, "endpoint_billing", wantBilling)
		writeErr(w, http.StatusBadRequest, "model_endpoint_mismatch",
			fmt.Sprintf("模型 %s 的计费方式（%s）与本端点不匹配，请联系管理员核对模型配置",
				publicModel, model.BillingMode))
		return nil, pricing.Price{}, 0, false
	}
	if model.Price == nil {
		obs.Logger(ctx).Error("模型缺少定价，拒绝服务", "model", publicModel)
		writeErr(w, http.StatusServiceUnavailable, "model_not_priced", "模型暂不可用（未配置定价）")
		return nil, pricing.Price{}, 0, false
	}
	return model, model.Price.ToPricing(), e.evaluateMultiplier(model), true
}

// endpointForModality 返回模型形态对应的下游端点（端点不匹配的错误提示用）。
func endpointForModality(m domain.Modality) string {
	switch m {
	case domain.ModalityEmbedding:
		return "/v1/embeddings"
	case domain.ModalityImage:
		return "/v1/images/generations"
	default:
		return "/v1/chat/completions 或 /v1/messages"
	}
}

// ErrDailySpendLimit 当日累计扣费触及用户上限。
var ErrDailySpendLimit = errors.New("已达当日花费上限")

// ErrKeyDailySpendLimit 当日累计扣费触及 Key 上限。
var ErrKeyDailySpendLimit = errors.New("已达该 Key 当日花费上限")

// checkDailySpendLimit 校验本次预扣是否会突破用户或 Key 的当日花费上限。
// 上限为 0 表示不限制。计数与积分流水同事务维护，因此不会漏计。
//
// 注意：本预检仅为 UX 早拒——更快给用户反馈、避免无谓的密钥额度占用。
// 权威校验在 billing.applyTx 内、持有用户行锁且写 daily_spend / daily_spend_by_key 之前完成
// （由 Precharge 把 DailyLimit / KeyDailyLimit 透传给 consume Adjustment 触发）。
// 预检与扣费之间的 TOCTOU 由 applyTx 的同事务行锁闭合，不依赖本函数。
// 用户级先于 Key 级：两者其一超限即拒绝，用户级更早拒可避免无谓的 Key 计数查询。
func (e *Engine) checkDailySpendLimit(ctx context.Context, ident Identity,
	precharge domain.Credits) error {

	if err := e.checkUserDailySpendLimit(ctx, ident, precharge); err != nil {
		return err
	}
	return e.checkKeyDailySpendLimit(ctx, ident, precharge)
}

func (e *Engine) checkUserDailySpendLimit(ctx context.Context, ident Identity,
	precharge domain.Credits) error {
	limit := ident.User.DailySpendLimit
	if limit <= 0 || e.Spend == nil {
		return nil
	}
	spent, err := e.Spend.TodaySpend(ctx, ident.User.ID, e.now())
	if err != nil {
		// 读取失败按放行处理：花费上限是成本策略而非安全边界，
		// 让计数查询故障拦下全部调用的代价更大。
		obs.Logger(ctx).Error("查询当日花费失败，本次不施加花费上限",
			"user_id", ident.User.ID, "error", err)
		return nil
	}
	if spent+precharge > limit {
		return fmt.Errorf("%w：当日已消费 %d 积分，上限 %d 积分",
			ErrDailySpendLimit, spent, limit)
	}
	return nil
}

// checkKeyDailySpendLimit 预检 Key 级每日上限；权威校验仍在 applyTx 锁内。
func (e *Engine) checkKeyDailySpendLimit(ctx context.Context, ident Identity,
	precharge domain.Credits) error {
	limit := ident.Key.DailySpendLimit
	if limit <= 0 || e.Spend == nil {
		return nil
	}
	spent, err := e.Spend.TodaySpendByKey(ctx, ident.Key.ID, e.now())
	if err != nil {
		obs.Logger(ctx).Error("查询 Key 当日花费失败，本次不施加 Key 花费上限",
			"api_key_id", ident.Key.ID, "error", err)
		return nil
	}
	if spent+precharge > limit {
		return fmt.Errorf("%w：该 Key 当日已消费 %d 积分，上限 %d 积分",
			ErrKeyDailySpendLimit, spent, limit)
	}
	return nil
}

// rejectPrecharge 输出预扣失败响应并落日志。
func (e *Engine) rejectPrecharge(w http.ResponseWriter, ctx context.Context, ident Identity,
	model string, stream bool, start time.Time, multiplier int, err error, writeErr errWriter) {

	switch {
	case errors.Is(err, ErrKeyDailySpendLimit), errors.Is(err, billing.ErrKeyDailyLimitExceeded):
		writeErr(w, http.StatusTooManyRequests, "key_daily_spend_limit_exceeded",
			"该密钥已达当日花费上限，请明日再试或换用其他密钥")
	case errors.Is(err, ErrDailySpendLimit), errors.Is(err, billing.ErrDailyLimitExceeded):
		writeErr(w, http.StatusTooManyRequests, "daily_spend_limit_exceeded",
			"已达当日花费上限，请明日再试或联系管理员上调")
	// relay.ErrKeyDepleted 是 billing.ErrKeyDepleted 的别名（F8 上移），单判即可。
	case errors.Is(err, billing.ErrKeyDepleted):
		writeErr(w, http.StatusPaymentRequired, "key_quota_exceeded", "密钥额度不足")
	case errors.Is(err, billing.ErrInsufficientCredits):
		writeErr(w, http.StatusPaymentRequired, "insufficient_credits", "积分余额不足")
	default:
		obs.Logger(ctx).Error("预扣费失败", "error", err)
		writeErr(w, http.StatusInternalServerError, "internal_error", "计费系统异常")
	}
	e.writeFailLog(ctx, ident, model, stream, start, domain.UsageFailed,
		domain.ErrClassNone, err.Error(), 0, multiplier)
}

// relayWithRetry 依次尝试渠道直至成功或候选耗尽。
// provider 非零值时（/{provider}/v1/* 入口）候选集按 provider 收窄，仅在同 provider
// 内容错；为零值时（/v1/* 入口）跨 provider 容错，行为不变。
// keyID 为发起请求的 API Key ID，用于亲和退化键（无会话级亲和键时按 Key 绑定）。
func (e *Engine) relayWithRetry(w http.ResponseWriter, r *http.Request,
	session *BillingSession, body map[string]any, model *store.Model, ds downstream,
	keyID int64, price pricing.Price, multiplier int, stream bool, start time.Time,
	log *store.UsageLog, writeErr errWriter, rec *recorder, provider domain.Provider) {

	ctx := r.Context()
	publicModel := model.Name
	channels, err := e.Channels.ListEnabledForModel(ctx, publicModel)
	if err != nil || len(channels) == 0 {
		_ = session.Refund(ctx)
		writeErr(w, http.StatusServiceUnavailable, "no_channel", "没有可用渠道承载该模型")
		log.Status = domain.UsageRefunded
		log.ErrorMessage = "无可用渠道"
		e.usage().finishLog(ctx, log, start, rec)
		return
	}
	// provider 前缀路由：/{provider}/v1/* 把候选集收窄到同 provider 渠道。
	// provider 为零值时 filterByProvider 直返原切片（/v1/* 跨 provider 行为不变）。
	// 过滤后候选为空 = 该 provider 无启用渠道承载该模型，返回 no_channel，不回退其他 provider。
	channels = filterByProvider(channels, provider)
	if len(channels) == 0 {
		_ = session.Refund(ctx)
		writeErr(w, http.StatusServiceUnavailable, "no_channel", "没有可用渠道承载该模型")
		log.Status = domain.UsageRefunded
		log.ErrorMessage = "无可用渠道"
		e.usage().finishLog(ctx, log, start, rec)
		return
	}

	maxRetries := int(e.Settings.GetInt64(ctx, "relay_max_retries"))
	tried := map[int64]bool{}
	var lastErr relayError

	// 渠道亲和（方案 C 节）：从请求体提取会话级亲和键（Anthropic metadata.user_id /
	// OpenAI prompt_cache_key），退化到 API Key ID。命中既有绑定时绑到同渠道以保上游
	// prompt cache；无键时退化为现有加权随机，行为不变。
	affinityKey, affinitySrc := extractAffinityKey(body, ds, keyID)
	sel := e.selector()

	// 上游请求上下文与下游连接解耦并附独立超时（架构决策 2026-08-05 第 3 项）：
	// 客户端断连不取消上游请求，流式场景继续读完上游拿到真实 usage 后结算。
	// 超时覆盖整个上游阶段（含渠道重试）；cancel 由 defer 延迟到响应体读完之后。
	upCtx, cancelUpstream := e.upstreamContext(ctx)
	defer cancelUpstream()

	// 跨渠道重试不变的上下文，打包传给 tryChannel，避免函数签名爆炸（M1 拆分）。
	a := &relayCtx{
		ctx: ctx, upCtx: upCtx, w: w, r: r,
		ds: ds, body: body, publicModel: publicModel, stream: stream,
		session: session, price: price, multiplier: multiplier,
		start: start, log: log, writeErr: writeErr, rec: rec,
		selector: sel, affinityKey: affinityKey, affinitySrc: affinitySrc,
	}

	for attempt := 0; attempt < maxRetries; attempt++ {
		ch, affOutcome := sel.Select(channels, tried, publicModel, affinityKey)
		if ch == nil {
			break
		}
		// 仅在首次选择（attempt==0）时记录亲和指标：重试内的换渠道属容错，不计亲和漂移。
		// 漂移（drift）已在 selectWithAffinity 内判定——绑定渠道本轮被 tried/不在顶层。
		if attempt == 0 && affinitySrc != affinitySourceNone {
			switch affOutcome {
			case affinityHit:
				obs.RecordAffinityHit()
			case affinityMiss:
				obs.RecordAffinityMiss()
			case affinityDrift:
				obs.RecordAffinityDrift()
			}
		}
		tried[ch.ID] = true

		done, err := e.tryChannel(a, ch, affOutcome, attempt)
		lastErr = err
		if done {
			return
		}
	}

	// 全部候选失败
	_ = session.Refund(ctx)
	if lastErr.status == 0 {
		lastErr = relayError{http.StatusServiceUnavailable, "no_channel", "没有可用渠道"}
	}
	writeErr(w, lastErr.status, lastErr.code, lastErr.message)
	log.Status = domain.UsageRefunded
	log.ErrorMessage = lastErr.message
	e.usage().finishLog(ctx, log, start, rec)
}

// relayCtx 与 tryChannel（单次渠道尝试）定义在 retry.go，拆自 relayWithRetry
// 以满足 M1（函数 ≤50 行、单文件 ≤800 行）。

// jsonResponse 非流式：转换响应 → 结算 → 输出。
func (e *Engine) jsonResponse(w http.ResponseWriter, r *http.Request, resp *http.Response,
	cd conduit, session *BillingSession, price pricing.Price, multiplier int,
	start time.Time, log *store.UsageLog, writeErr errWriter, rec *recorder) {

	ctx := r.Context()
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxRelayBodyBytes))
	if err != nil {
		_ = session.Refund(ctx)
		writeErr(w, http.StatusBadGateway, "upstream_error", "读取上游响应失败")
		log.Status = domain.UsageRefunded
		log.ErrorMessage = "读取上游响应失败"
		e.usage().finishLog(ctx, log, start, rec)
		return
	}
	out, usage, usageFound, err := cd.TransformResponse(raw)
	if err != nil {
		_ = session.Refund(ctx)
		writeErr(w, http.StatusBadGateway, "upstream_error", "上游响应格式异常")
		log.Status = domain.UsageRefunded
		log.ErrorMessage = err.Error()
		e.usage().finishLog(ctx, log, start, rec)
		return
	}
	if !usageFound {
		usage.BaseInput = estimateTokensFromText(contentLengthOfBody(r))
		usage.Output = estimateTokensFromText(len(out))
		usage.Estimated = true
	}

	rec.setResponseMeta(http.StatusOK, resp.Header)
	rec.captureResp(raw, out)
	final := pricing.CalcTokenCredits(usage, price, multiplier)
	e.settleAndLog(ctx, session, log, usage, final, price, multiplier, start, rec)

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(out)
}

// settleAndLog 结算并落日志（流式与非流式共用）。
func (e *Engine) settleAndLog(ctx context.Context, session *BillingSession, log *store.UsageLog,
	usage domain.NormalizedUsage, final domain.Credits, price pricing.Price, multiplier int,
	start time.Time, rec *recorder) {

	// Settle 内部使用脱离取消、带截止时间的写入上下文；失败时会话转入
	// 结算失败态（预扣保留、不退款），由孤儿预扣清理补偿，日志状态如实记失败。
	if err := session.Settle(ctx, final); err != nil {
		obs.Logger(ctx).Error("结算失败（预扣保留，交由孤儿预扣清理补偿）", "error", err)
		log.Status = domain.UsageFailed
		log.ErrorMessage = "结算写入失败，预扣保留，待孤儿预扣清理补偿"
	} else {
		log.Status = domain.UsageSettled
	}
	log.PromptTokens = usage.BaseInput + usage.CacheRead + usage.CacheWrite
	log.CompletionTokens = usage.Output
	log.CacheReadTokens = usage.CacheRead
	log.CacheWriteTokens = usage.CacheWrite
	log.AudioInputTokens = usage.AudioInput
	log.AudioOutputTokens = usage.AudioOutput
	log.CallCount = usage.CallCount
	log.UsageSemantic = usage.Semantic
	log.UsageEstimated = usage.Estimated
	if log.Status == domain.UsageSettled {
		log.CreditsCharged = final
	} else {
		// 结算写入失败：实际扣款停留在预扣金额，报表不得显示已按 final 扣费。
		log.CreditsCharged = session.Precharged()
	}
	// 音频零计费兜底：模型未配音频单价（AudioInput/OutputPrice 均 ≤0）却收到音频 token 时，
	// 这部分被 CalcTokenCredits 的 addMul（price≤0 跳过）按 0 单价放行。模态枚举无 audio 子类型，
	// 无法在请求入口预防式拒绝；此处计数 + 告警，暴露系统性漏配价供运营为模型补单。
	// （reconcile 抓不到——账本仍平，漏的是收入而非账本一致性。）
	if (usage.AudioInput > 0 || usage.AudioOutput > 0) &&
		price.AudioInputPrice <= 0 && price.AudioOutputPrice <= 0 {
		obs.RecordAudioZeroPrice(usage.AudioInput + usage.AudioOutput)
		obs.Logger(ctx).Warn("音频用量零计费：模型未配置音频单价，请联系管理员补单",
			"model", log.ModelName,
			"audio_input", usage.AudioInput, "audio_output", usage.AudioOutput)
	}
	costCtx, cancel := detachedWriteCtx(ctx)
	log.CreditsCost = e.computeCost(costCtx, log.ChannelID, log.ModelName, usage)
	cancel()
	snapshot, _ := json.Marshal(map[string]any{
		"price": price, "multiplier_percent": multiplier,
	})
	log.PriceSnapshot = snapshot
	e.usage().finishLog(ctx, log, start, rec)
}

// computeCost 计算渠道成本（积分）。
// 仅做两次 IO（Costs.Get + Settings 读汇率/兑换率）+ 一次纯函数调用，
// 跨实体成本编排（单价选择 + 币种折算 + 取整方向）下沉到 pricing.ComputeCostCredits。
func (e *Engine) computeCost(ctx context.Context, channelID int64, modelName string,
	usage domain.NormalizedUsage) domain.Credits {

	if channelID == 0 {
		return 0
	}
	cost, err := e.Costs.Get(ctx, channelID, modelName)
	if err != nil || cost == nil {
		return 0
	}
	costPrice := pricing.Price{
		InputPrice: cost.InputCost, OutputPrice: cost.OutputCost,
		CacheReadPrice: cost.CacheReadCost, CacheWritePrice: cost.CacheWriteCost,
		PerCallPrice: cost.PerCallCost,
	}
	rateMilli := e.Settings.GetInt64(ctx, "usd_cny_rate_milli")
	exchange := e.Settings.GetInt64(ctx, "exchange_rate_credits_per_cny")
	return pricing.ComputeCostCredits(usage, costPrice, cost.Currency, rateMilli, exchange)
}

// writeFailLog 预扣前失败（余额不足等）的日志。
func (e *Engine) writeFailLog(ctx context.Context, ident Identity, model string, stream bool,
	start time.Time, status domain.UsageStatus, class domain.ErrorClass, msg string,
	precharged domain.Credits, multiplier int) {

	log := &store.UsageLog{
		RequestID: obs.RequestID(ctx), UserID: ident.User.ID, APIKeyID: ident.Key.ID,
		DepartmentID:  ident.DepartmentID(),
		ProjectID:     ident.ProjectID(),
		IntegrationID: ident.IntegrationID(),
		ModelName:     model, IsStream: stream, Status: status, ErrorClass: class,
		ErrorMessage: strutil.Truncate(msg, 500), CreditsPrecharged: precharged,
		PeakMultiplierPercent: multiplier,
	}
	e.usage().finishLog(ctx, log, start, nil)
}

// evaluateMultiplier 求当前时刻的时段倍率。
func (e *Engine) evaluateMultiplier(model *store.Model) int {
	rules := make([]pricing.PeakRule, 0, len(model.PeakRules))
	for i := range model.PeakRules {
		rules = append(rules, model.PeakRules[i].ToPricing())
	}
	return pricing.EvaluatePeakMultiplier(rules, e.now())
}

// now 返回当前时刻。时钟字段（Now）已下沉到 UsageSink；本方法保留为 Engine 内部
// 各调用点（时段倍率、日花费、日志耗时）的便捷访问，委托给 sink。
func (e *Engine) now() time.Time {
	return e.usage().Now()
}

// identityAllowsModel 判断模型是否落在有效模型集合内
// （部门策略 ∩ 用户策略 ∩ 密钥白名单）。
func identityAllowsModel(ident Identity, model string) (bool, error) {
	var userPolicy datatypes.JSON
	if ident.User != nil {
		userPolicy = ident.User.AllowedModels
	}
	var keyPolicy datatypes.JSON
	if ident.Key != nil {
		keyPolicy = ident.Key.AllowedModels
	}
	return AllowsModel(ident.departmentPolicy(), userPolicy, keyPolicy, model)
}

// passUpstreamError 把上游错误透传给下游（保持下游协议的错误格式）。
func passUpstreamError(w http.ResponseWriter, status int, body []byte, ds downstream) {
	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err == nil {
		if _, ok := parsed["error"]; ok {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(parsed)
			return
		}
	}
	if ds == dsAnthropic {
		WriteAnthropicError(w, status, "upstream_error", strutil.Truncate(string(body), 300))
		return
	}
	WriteOpenAIError(w, status, "upstream_error", strutil.Truncate(string(body), 300))
}

func copyBody(src map[string]any) map[string]any {
	dst := make(map[string]any, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

// clientIP 取连接远端地址作为客户端 IP；
// 可信代理场景由入口的 obs.RealIPMiddleware 预先改写 RemoteAddr。
func clientIP(r *http.Request) string {
	return obs.ClientIP(r)
}

// contentLengthOfBody 请求体长度的兜底估算来源。
func contentLengthOfBody(r *http.Request) int {
	if r.ContentLength > 0 {
		return int(r.ContentLength)
	}
	return 0
}
