package api

import (
	"fmt"
	"net"
	"net/http"

	"github.com/liguopeng80/tokenzen-lite/server/internal/api/respond"
	"github.com/liguopeng80/tokenzen-lite/server/internal/audit"
	"github.com/liguopeng80/tokenzen-lite/server/internal/auth"
	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/obs"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// admin_keys.go：管理端（含托管服务令牌）代用户签发、停用、吊销 API Key（R2）。
// 接入方用户不登录网关，凭据由接入方持有，故 Key 的归属取自路径中的用户 id，
// 而非当前会话用户；作用域校验由 loadManagedUser 承载（托管视角跨作用域一律 404）。

// adminCreatedKeyResponse 与 /me 下的 createdKeyResponse 同构，但旁置密钥额度的货币串。
// 明文 Key 仅在首次创建时返回，重放时不回显。
type adminCreatedKeyResponse struct {
	apiKeyWithMoney
	Key string `json:"key"`
}

// handleAdminCreateUserKey 代目标用户签发 API Key，明文仅返回一次。
func (c *userAdminController) handleAdminCreateUserKey(w http.ResponseWriter, r *http.Request) {
	target, ok := loadManagedUser(w, r, c.Users)
	if !ok {
		return
	}
	var req keyPayload
	if !Bind(w, r, &req) {
		return
	}
	if msg := validateKeyPayload(&req); msg != "" {
		respond.Fail(w, http.StatusBadRequest, msg)
		return
	}
	// 幂等（R4）：重放时无法再返回明文（明文不落库），故只回首次创建的密钥对象并标明重放。
	if req.IdempotencyKey != "" && !idempotencyKeyRe.MatchString(req.IdempotencyKey) {
		respond.Fail(w, http.StatusBadRequest, "幂等键须为 1-64 位字母、数字、下划线或连字符")
		return
	}
	rate := c.Settings.GetInt64(r.Context(), "exchange_rate_credits_per_cny")
	mc := newMoneyCtx(rate)
	if firstID, ok := idempotencyLookupReplay(r.Context(), c.Idempotency, req.IdempotencyKey, "api_key.issue"); ok {
		if prior, err := c.Keys.GetByID(r.Context(), firstID); err == nil && prior.UserID == target.ID {
			respond.OKMessage(w, "该密钥已签发，明文仅首次创建时返回（重放）", wrapAPIKey(*prior, mc))
			return
		}
	}
	if maxKeys := c.Settings.GetInt64(r.Context(), "max_keys_per_user"); maxKeys > 0 {
		count, err := c.Keys.CountByUser(r.Context(), target.ID)
		if err != nil {
			obs.Logger(r.Context()).Error("统计密钥数量失败", "error", err, "user_id", target.ID)
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
		UserID:        target.ID,
		Name:          req.Name,
		KeyHash:       gen.Hash,
		KeyPrefix:     gen.Prefix,
		Status:        domain.KeyEnabled,
		CreditLimit:   req.CreditLimit,
		ExpiresAt:     req.ExpiresAt,
		IntegrationID: target.IntegrationID, // 密钥继承用户的作用域归属
	}
	if req.DailySpendLimit != nil {
		k.DailySpendLimit = *req.DailySpendLimit
	}
	// 项目归属校验：以目标用户（密钥属主）的作用域为准，跨作用域或不存在一律拒绝。
	pid, msg := resolveKeyProject(r.Context(), c.Projects, req.ProjectID, target)
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
		obs.Logger(r.Context()).Error("代签发密钥失败", "error", err, "user_id", target.ID)
		respond.Fail(w, http.StatusInternalServerError, "创建密钥失败")
		return
	}
	if req.IdempotencyKey != "" {
		idempotencyRemember(r.Context(), c.Idempotency, req.IdempotencyKey, "api_key.issue", k.ID)
	}
	obs.Logger(r.Context()).Info("管理端代签发 API Key",
		"actor_id", auth.CurrentUser(r.Context()).ID, "user_id", target.ID, "key_id", k.ID)
	c.Audit.Record(r, audit.Entry{
		Action: domain.AuditAPIKeyCreate, TargetType: domain.AuditTargetAPIKey,
		TargetID: k.ID, TargetName: k.Name, After: apiKeySnapshot(k),
	})
	respond.Created(w, adminCreatedKeyResponse{apiKeyWithMoney: wrapAPIKey(*k, mc), Key: gen.Plain})
}

// handleAdminUpdateUserKey 代目标用户更新指定 Key（停用、调额、改白名单等）。
func (c *userAdminController) handleAdminUpdateUserKey(w http.ResponseWriter, r *http.Request) {
	target, ok := loadManagedUser(w, r, c.Users)
	if !ok {
		return
	}
	keyID, ok := IDParam(w, r, "key_id")
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
		pid, msg := resolveKeyProject(r.Context(), c.Projects, req.ProjectID, target)
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
	before, err := c.Keys.GetByID(r.Context(), keyID)
	if err != nil || before.UserID != target.ID {
		respond.Fail(w, http.StatusNotFound, "密钥不存在")
		return
	}
	changedBefore, changedAfter := audit.Diff(apiKeySnapshot(before), fields)
	if err := c.Keys.UpdateFields(r.Context(), keyID, target.ID, fields); err != nil {
		respond.Fail(w, http.StatusNotFound, "密钥不存在")
		return
	}
	c.Audit.Record(r, audit.Entry{
		Action: domain.AuditAPIKeyUpdate, TargetType: domain.AuditTargetAPIKey,
		TargetID: keyID, TargetName: before.Name,
		Before: changedBefore, After: changedAfter,
	})
	if req.ClearLimit || req.CreditLimit != nil {
		if err := c.Keys.RestoreDepleted(r.Context(), keyID, target.ID); err != nil {
			obs.Logger(r.Context()).Warn("额度调整后恢复密钥状态失败",
				"error", err, "key_id", keyID)
		}
	}
	respond.OK(w, nil)
}

// handleAdminDeleteUserKey 代目标用户吊销指定 Key（用户注销时回收凭据）。
func (c *userAdminController) handleAdminDeleteUserKey(w http.ResponseWriter, r *http.Request) {
	target, ok := loadManagedUser(w, r, c.Users)
	if !ok {
		return
	}
	keyID, ok := IDParam(w, r, "key_id")
	if !ok {
		return
	}
	k, err := c.Keys.GetByID(r.Context(), keyID)
	if err != nil || k.UserID != target.ID {
		respond.Fail(w, http.StatusNotFound, "密钥不存在")
		return
	}
	if err := c.Keys.Delete(r.Context(), keyID, target.ID); err != nil {
		respond.Fail(w, http.StatusNotFound, "密钥不存在")
		return
	}
	obs.Logger(r.Context()).Info("管理端吊销 API Key",
		"actor_id", auth.CurrentUser(r.Context()).ID, "user_id", target.ID, "key_id", keyID)
	c.Audit.Record(r, audit.Entry{
		Action: domain.AuditAPIKeyDelete, TargetType: domain.AuditTargetAPIKey,
		TargetID: keyID, TargetName: k.Name, Before: apiKeySnapshot(k),
	})
	respond.OK(w, nil)
}
