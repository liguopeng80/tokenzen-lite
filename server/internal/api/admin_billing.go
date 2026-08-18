package api

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"time"

	"github.com/liguopeng80/tokenzen-lite/server/internal/alerting"
	"github.com/liguopeng80/tokenzen-lite/server/internal/api/respond"
	"github.com/liguopeng80/tokenzen-lite/server/internal/audit"
	"github.com/liguopeng80/tokenzen-lite/server/internal/auth"
	"github.com/liguopeng80/tokenzen-lite/server/internal/billing"
	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/obs"
	"github.com/liguopeng80/tokenzen-lite/server/internal/secrets"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// redemptionCodePrefix 兑换码明文前缀，与 API Key 前缀区分。
const redemptionCodePrefix = "tzr-"

// billingAdminController 承载积分运营与系统设置端点：
//   - admin_billing：积分发放（单发，幂等）、流水查询、兑换码批次、root 系统设置读写
//     （密文设置项加密存储）
//   - admin_alerts：告警事件查询与测试投递
//   - admin_setup：新装系统配置完整性检查
//
// 字段含 Users：发放积分需先经 loadManagedUser 校验管理边界。
type billingAdminController struct {
	Audit       *audit.Recorder
	Billing     *billing.Service
	Ledger      *store.LedgerRepo
	Redemptions *store.RedemptionRepo
	Secrets     *secrets.Box
	Settings    *store.SettingsRepo
	Users       *store.UserRepo
	AlertEvents *store.AlertEventRepo
	Alerts      *alerting.Service
	Stats       *store.StatsRepo
}

type grantCreditsRequest struct {
	Amount domain.Credits `json:"amount"`
	Note   string         `json:"note"`
	// IdempotencyKey 可选。携带后重复提交只记一次账，返回首次结果。
	IdempotencyKey string `json:"idempotency_key"`
}

func (c *billingAdminController) handleAdminGrantCredits(w http.ResponseWriter, r *http.Request) {
	target, ok := loadManagedUser(w, r, c.Users)
	if !ok {
		return
	}
	var req grantCreditsRequest
	if !Bind(w, r, &req) {
		return
	}
	if req.Amount == 0 {
		respond.Fail(w, http.StatusBadRequest, "调整金额不能为 0")
		return
	}
	if !validIdempotencyKey(req.IdempotencyKey) {
		respond.Fail(w, http.StatusBadRequest, "幂等键须为 1-64 位字母、数字、下划线或连字符")
		return
	}
	actor := auth.CurrentUser(r.Context())
	entry, replayed, err := grantCredits(r, c.Billing, c.Ledger, target.ID, req.Amount, actor.ID, req.Note, req.IdempotencyKey)
	if err != nil {
		if errors.Is(err, billing.ErrInsufficientCredits) {
			respond.Fail(w, http.StatusBadRequest, "扣回金额超过用户当前余额")
			return
		}
		obs.Logger(r.Context()).Error("积分调整失败", "error", err, "user_id", target.ID)
		c.Audit.Record(r, audit.Entry{
			Action: domain.AuditUserCreditGrant, TargetType: domain.AuditTargetUser,
			TargetID: target.ID, TargetName: target.Username,
			Result: domain.AuditFailure, Message: err.Error(),
			After: map[string]any{"amount": req.Amount, "note": req.Note},
		})
		respond.Fail(w, http.StatusInternalServerError, "积分调整失败")
		return
	}
	if replayed {
		obs.Logger(r.Context()).Info("积分发放命中幂等键，未重复记账",
			"actor_id", actor.ID, "user_id", target.ID, "entry_id", entry.ID)
		rate := c.Settings.GetInt64(r.Context(), "exchange_rate_credits_per_cny")
		respond.OKMessage(w, "该发放已记账，本次未重复调整", wrapLedgerEntry(*entry, newMoneyCtx(rate)))
		return
	}
	obs.Logger(r.Context()).Info("管理员调整积分",
		"actor_id", actor.ID, "user_id", target.ID, "amount", req.Amount)
	c.Audit.Record(r, audit.Entry{
		Action: domain.AuditUserCreditGrant, TargetType: domain.AuditTargetUser,
		TargetID: target.ID, TargetName: target.Username,
		Before: map[string]any{"credit_balance": entry.BalanceAfter - entry.Amount},
		After: map[string]any{
			"credit_balance": entry.BalanceAfter,
			"amount":         entry.Amount,
			"note":           req.Note,
		},
	})
	rate := c.Settings.GetInt64(r.Context(), "exchange_rate_credits_per_cny")
	respond.OK(w, wrapLedgerEntry(*entry, newMoneyCtx(rate)))
}

// grantCredits 执行一次积分调整，命中幂等键时回查首次记账结果。
// 第二个返回值为 true 表示本次是重放，未产生新的流水。
//
// 跨 userAdmin（批量发放）/ billingAdmin（单发）两组 controller 共用，
// 故为包级自由函数，签名收 billing.Service 与 LedgerRepo 而非绑定某个 controller。
func grantCredits(r *http.Request, billingSvc *billing.Service, ledgerRepo *store.LedgerRepo,
	userID int64, amount domain.Credits, actorID int64, note, idempotencyKey string,
) (*store.LedgerEntry, bool, error) {

	ctx := r.Context()
	entry, err := billingSvc.Grant(ctx, userID, amount, actorID, note, idempotencyKey)
	if err == nil {
		return entry, false, nil
	}
	if !errors.Is(err, billing.ErrDuplicateEntry) {
		return nil, false, err
	}
	entryType := domain.LedgerGrant
	if amount < 0 {
		entryType = domain.LedgerRevoke
	}
	existing, lookupErr := ledgerRepo.GetByRequestEntry(ctx,
		billing.AdminRequestID(idempotencyKey), entryType)
	if lookupErr != nil {
		return nil, false, fmt.Errorf("幂等重放回查首次记账结果失败: %w", lookupErr)
	}
	return existing, true, nil
}

// idempotencyKeyRe 限定幂等键的字符集，避免与流水的 request_id 命名空间冲突
// 或写入难以检索的内容。
var idempotencyKeyRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

func validIdempotencyKey(key string) bool {
	return key == "" || idempotencyKeyRe.MatchString(key)
}

func (c *billingAdminController) handleAdminListLedger(w http.ResponseWriter, r *http.Request) {
	page, pageSize := PageParams(r)
	q := r.URL.Query()
	f := store.LedgerListFilter{
		EntryType: domain.LedgerEntryType(q.Get("entry_type")),
		Page:      page, PageSize: pageSize,
		// 托管视角只看本接入方作用域内的流水；运营 admin/root 无作用域，看得见全部。
		IntegrationID: auth.ScopeIntegrationID(r.Context()),
	}
	if uid, ok := parseInt64(q.Get("user_id")); ok {
		f.UserID = uid
	}
	entries, total, err := c.Ledger.List(r.Context(), f)
	if err != nil {
		respond.Fail(w, http.StatusInternalServerError, "查询流水失败")
		return
	}
	rate := c.Settings.GetInt64(r.Context(), "exchange_rate_credits_per_cny")
	mc := newMoneyCtx(rate)
	respond.OK(w, respond.NewPage(page, pageSize, total,
		wrapList(entries, func(e store.LedgerEntry) ledgerEntryWithMoney { return wrapLedgerEntry(e, mc) })))
}

type redemptionBatchRequest struct {
	Count     int            `json:"count"`
	Credits   domain.Credits `json:"credits"`
	Name      string         `json:"name"`
	ExpiresAt *time.Time     `json:"expires_at"`
}

type redemptionBatchResponse struct {
	BatchID string   `json:"batch_id"`
	Codes   []string `json:"codes"` // 明文仅此一次返回
}

func newRedemptionCode() (string, error) {
	raw := make([]byte, 18)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return redemptionCodePrefix + base64.RawURLEncoding.EncodeToString(raw), nil
}

func (c *billingAdminController) handleAdminCreateRedemptions(w http.ResponseWriter, r *http.Request) {
	var req redemptionBatchRequest
	if !Bind(w, r, &req) {
		return
	}
	if req.Count < 1 || req.Count > 1000 {
		respond.Fail(w, http.StatusBadRequest, "单批数量须在 1-1000 之间")
		return
	}
	if req.Credits <= 0 {
		respond.Fail(w, http.StatusBadRequest, "兑换积分必须为正数")
		return
	}
	batchID := time.Now().Format("20060102-150405")
	codes := make([]string, 0, req.Count)
	items := make([]store.Redemption, 0, req.Count)
	for i := 0; i < req.Count; i++ {
		code, err := newRedemptionCode()
		if err != nil {
			respond.Fail(w, http.StatusInternalServerError, "生成兑换码失败")
			return
		}
		codes = append(codes, code)
		items = append(items, store.Redemption{
			BatchID: batchID, CodeHash: auth.HashKey(code), Name: req.Name,
			Credits: req.Credits, Status: domain.RedemptionUnused, ExpiresAt: req.ExpiresAt,
		})
	}
	if err := c.Redemptions.CreateBatch(r.Context(), items); err != nil {
		obs.Logger(r.Context()).Error("批量生成兑换码失败", "error", err)
		respond.Fail(w, http.StatusInternalServerError, "批量生成兑换码失败")
		return
	}
	obs.Logger(r.Context()).Info("批量生成兑换码",
		"actor_id", auth.CurrentUser(r.Context()).ID, "batch_id", batchID,
		"count", req.Count, "credits", req.Credits)
	// 审计只记批次规模与面额，兑换码明文不入审计。
	c.Audit.Record(r, audit.Entry{
		Action: domain.AuditRedemptionBatch, TargetType: domain.AuditTargetRedemption,
		TargetName: batchID,
		After: map[string]any{
			"batch_id": batchID, "count": req.Count,
			"credits": req.Credits, "name": req.Name, "expires_at": req.ExpiresAt,
		},
	})
	respond.Created(w, redemptionBatchResponse{BatchID: batchID, Codes: codes})
}

func (c *billingAdminController) handleAdminListRedemptions(w http.ResponseWriter, r *http.Request) {
	page, pageSize := PageParams(r)
	q := r.URL.Query()
	items, total, err := c.Redemptions.List(r.Context(), store.RedemptionListFilter{
		Keyword: q.Get("keyword"),
		Status:  domain.RedemptionStatus(q.Get("status")),
		BatchID: q.Get("batch_id"),
		Page:    page, PageSize: pageSize,
	})
	if err != nil {
		respond.Fail(w, http.StatusInternalServerError, "查询兑换码失败")
		return
	}
	respond.OK(w, respond.NewPage(page, pageSize, total, items))
}

type redemptionStatusRequest struct {
	Status domain.RedemptionStatus `json:"status"`
}

func (c *billingAdminController) handleAdminSetRedemptionStatus(w http.ResponseWriter, r *http.Request) {
	id, ok := IDParam(w, r, "id")
	if !ok {
		return
	}
	var req redemptionStatusRequest
	if !Bind(w, r, &req) {
		return
	}
	if req.Status != domain.RedemptionUnused && req.Status != domain.RedemptionDisabled {
		respond.Fail(w, http.StatusBadRequest, "状态只能设置为 unused 或 disabled")
		return
	}
	if err := c.Redemptions.SetStatus(r.Context(), id, req.Status); err != nil {
		respond.Fail(w, http.StatusNotFound, "兑换码不存在或已被使用")
		return
	}
	c.Audit.Record(r, audit.Entry{
		Action: domain.AuditRedemptionStatus, TargetType: domain.AuditTargetRedemption,
		TargetID: id, After: map[string]any{"status": req.Status},
	})
	respond.OK(w, nil)
}

// settingSecretMask 是密文设置项在读取接口中的占位值：只表达「已配置」，
// 不回显明文。空串表示尚未配置。
const settingSecretMask = "********"

func (c *billingAdminController) handleAdminGetSettings(w http.ResponseWriter, r *http.Request) {
	items := c.Settings.EffectiveAll(r.Context())
	for _, item := range items {
		key, _ := item["key"].(string)
		if !alerting.SecretSettingKeys[key] {
			continue
		}
		if stored, _ := item["value"].(string); stored != "" {
			item["value"] = settingSecretMask
		}
		item["secret"] = true
	}
	respond.OK(w, items)
}

type settingUpdateRequest struct {
	Key   string       `json:"key"`
	Value jsonRawValue `json:"value"`
}

func (c *billingAdminController) handleAdminUpdateSetting(w http.ResponseWriter, r *http.Request) {
	var req settingUpdateRequest
	if !Bind(w, r, &req) {
		return
	}
	// 先读改动前的生效值：设置项被误改后，只有审计里同时留下新旧值才查得回原值。
	// 密文设置项只表达「被改过」，不记两侧取值。
	before := map[string]any{}
	if prev, registered := c.Settings.Effective(r.Context(), req.Key); registered {
		if alerting.SecretSettingKeys[req.Key] {
			before[req.Key] = domain.AuditRedacted
		} else {
			before[req.Key] = prev
		}
	}
	value := json.RawMessage(req.Value)
	if alerting.SecretSettingKeys[req.Key] {
		encrypted, ok := c.encryptSettingValue(w, req.Key, value)
		if !ok {
			return
		}
		value = encrypted
	}
	if err := c.Settings.Set(r.Context(), req.Key, value); err != nil {
		respond.Fail(w, http.StatusBadRequest, err.Error())
		return
	}
	obs.Logger(r.Context()).Info("系统设置更新",
		"actor_id", auth.CurrentUser(r.Context()).ID, "key", req.Key)
	// 审计只记录被改的键；密文设置项的取值由 audit 包按敏感字段名脱敏。
	after := map[string]any{req.Key: json.RawMessage(req.Value)}
	if alerting.SecretSettingKeys[req.Key] {
		after = map[string]any{req.Key: domain.AuditRedacted}
	}
	c.Audit.Record(r, audit.Entry{
		Action: domain.AuditSettingUpdate, TargetType: domain.AuditTargetSetting,
		TargetName: req.Key, Before: before, After: after,
	})
	respond.OK(w, nil)
}

// encryptSettingValue 把密文设置项的明文取值加密后重新编码为 JSON 字符串。
// 前端回传掩码占位值表示「不修改」，此时保持库中原值不变。
func (c *billingAdminController) encryptSettingValue(w http.ResponseWriter, key string,
	raw json.RawMessage) (json.RawMessage, bool) {

	var plain string
	if err := json.Unmarshal(raw, &plain); err != nil {
		respond.Fail(w, http.StatusBadRequest, "设置项 "+key+" 的取值应为字符串")
		return nil, false
	}
	if plain == settingSecretMask {
		respond.Fail(w, http.StatusBadRequest, "请输入新的取值，或留空以清除")
		return nil, false
	}
	if plain == "" {
		return json.RawMessage(`""`), true
	}
	if c.Secrets == nil {
		respond.Fail(w, http.StatusInternalServerError, "加密组件未就绪，无法保存该设置项")
		return nil, false
	}
	encrypted, err := c.Secrets.Encrypt(plain)
	if err != nil {
		respond.Fail(w, http.StatusInternalServerError, "加密设置项失败")
		return nil, false
	}
	encoded, err := json.Marshal(encrypted)
	if err != nil {
		respond.Fail(w, http.StatusInternalServerError, "编码设置项失败")
		return nil, false
	}
	return encoded, true
}
