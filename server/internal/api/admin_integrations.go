package api

import (
	"net/http"
	"regexp"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/liguopeng80/tokenzen-lite/server/internal/api/respond"
	"github.com/liguopeng80/tokenzen-lite/server/internal/audit"
	"github.com/liguopeng80/tokenzen-lite/server/internal/auth"
	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/obs"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// admin_integrations.go：root 运营接入方与其服务令牌（批次 F）。
// 接入方托管能力的运营入口——没有它就无法在生产创建接入方、服务账号与服务令牌。
// 全部端点 root 独占：运营 admin 不持接入方，托管 managed 用 service-tokens/* 自助。

// integrationsAdminController 承载 root 桶的接入方与服务令牌运营端点：
// 接入方 CRUD/级联停用、服务令牌签发/列/改状态/删。建接入方事务内同步建服务账号用户，
// 故持 *gorm.DB 而非仅 IntegrationRepo。
type integrationsAdminController struct {
	Audit         *audit.Recorder
	DB            *gorm.DB
	Integrations  *store.IntegrationRepo
	ServiceTokens *store.ServiceTokenRepo
	Users         *store.UserRepo
}

// integrationSlugRe 规定接入方 slug 的合法形态：服务账号用户名 svc:<slug> 由它拼成，
// 故与用户名同样只允许字母、数字、下划线与连字符，长度 1-64。
var integrationSlugRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// serviceAccountUsername 由 slug 拼出服务账号的用户名。
func serviceAccountUsername(slug string) string { return "svc:" + slug }

type createIntegrationRequest struct {
	Name string `json:"name"`
	Slug string `json:"slug"`
}

// handleAdminCreateIntegration 事务内建接入方与其服务账号用户。
// 服务账号无口令（不登录网关），凭据由接入方持有的服务令牌承担。
func (c *integrationsAdminController) handleAdminCreateIntegration(w http.ResponseWriter, r *http.Request) {
	var req createIntegrationRequest
	if !Bind(w, r, &req) {
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	req.Slug = strings.TrimSpace(req.Slug)
	if req.Name == "" || len(req.Name) > 100 {
		respond.Fail(w, http.StatusBadRequest, "接入方名称必填且不超过 100 字符")
		return
	}
	if !integrationSlugRe.MatchString(req.Slug) {
		respond.Fail(w, http.StatusBadRequest, "slug 须为 1-64 位字母、数字、下划线或连字符")
		return
	}
	username := serviceAccountUsername(req.Slug)
	var created *store.Integration
	err := c.DB.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		it := &store.Integration{Name: req.Name, Slug: req.Slug, Status: domain.IntegrationEnabled}
		if err := tx.Create(it).Error; err != nil {
			return err
		}
		svc := &store.User{
			Username:           username,
			DisplayName:        it.Name,
			Role:               domain.RoleManaged,
			Status:             domain.UserEnabled,
			IntegrationID:      &it.ID,
			MustChangePassword: false,
		}
		if err := tx.Create(svc).Error; err != nil {
			return err
		}
		created = it
		return nil
	})
	if err != nil {
		// slug 唯一冲突或 svc:<slug> 用户名唯一冲突均落在此分支，统一 409。
		obs.Logger(r.Context()).Warn("创建接入方失败", "slug", req.Slug, "error", err)
		respond.Fail(w, http.StatusConflict, "接入方标识冲突或服务账号已存在")
		return
	}
	obs.Logger(r.Context()).Info("创建接入方", "integration_id", created.ID, "slug", created.Slug)
	c.Audit.Record(r, audit.Entry{
		Action: domain.AuditIntegrationCreate, TargetType: domain.AuditTargetIntegration,
		TargetID: created.ID, TargetName: created.Name,
		After: integrationSnapshot(created),
	})
	respond.Created(w, created)
}

// handleAdminListIntegrations 列出全部接入方（分页）。
func (c *integrationsAdminController) handleAdminListIntegrations(w http.ResponseWriter, r *http.Request) {
	page, pageSize := PageParams(r)
	rows, err := c.Integrations.List(r.Context(), "")
	if err != nil {
		obs.Logger(r.Context()).Error("查询接入方列表失败", "error", err)
		respond.Fail(w, http.StatusInternalServerError, "查询接入方列表失败")
		return
	}
	start, end := pageBounds(len(rows), page, pageSize)
	items := rows[start:end]
	respond.OK(w, respond.NewPage(page, pageSize, int64(len(rows)), items))
}

// integrationDetail 是接入方详情响应，附服务账号 ID 与令牌计数。
type integrationDetail struct {
	store.Integration
	ServiceAccountUserID *int64 `json:"service_account_user_id"`
	TokenCount           int    `json:"token_count"`
}

// handleAdminGetIntegration 返回接入方详情，附带服务账号与令牌计数。
func (c *integrationsAdminController) handleAdminGetIntegration(w http.ResponseWriter, r *http.Request) {
	it, ok := c.loadIntegration(w, r)
	if !ok {
		return
	}
	detail := integrationDetail{Integration: *it}
	if svc, err := c.Users.GetServiceAccount(r.Context(), it.ID); err == nil {
		detail.ServiceAccountUserID = &svc.ID
	}
	if tokens, err := c.ServiceTokens.ListByIntegration(r.Context(), it.ID); err == nil {
		detail.TokenCount = len(tokens)
	}
	respond.OK(w, detail)
}

type updateIntegrationRequest struct {
	Name string `json:"name"`
}

// handleAdminUpdateIntegration 改接入方名称；slug 不可变（不在 UpdateFields 白名单）。
func (c *integrationsAdminController) handleAdminUpdateIntegration(w http.ResponseWriter, r *http.Request) {
	it, ok := c.loadIntegration(w, r)
	if !ok {
		return
	}
	var req updateIntegrationRequest
	if !Bind(w, r, &req) {
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" || len(req.Name) > 100 {
		respond.Fail(w, http.StatusBadRequest, "接入方名称必填且不超过 100 字符")
		return
	}
	fields := map[string]any{"name": req.Name}
	if err := c.Integrations.UpdateFields(r.Context(), it.ID, fields); err != nil {
		respond.Fail(w, http.StatusNotFound, "接入方不存在")
		return
	}
	c.Audit.Record(r, audit.Entry{
		Action: domain.AuditIntegrationUpdate, TargetType: domain.AuditTargetIntegration,
		TargetID: it.ID, TargetName: req.Name,
		Before: integrationSnapshot(it), After: fields,
	})
	respond.OK(w, nil)
}

type createServiceTokenRequest struct {
	Name string `json:"name"`
}

// createdServiceTokenResponse 是服务令牌创建响应，明文仅本次返回一次。
type createdServiceTokenResponse struct {
	store.ServiceToken
	Token string `json:"token"`
}

// handleAdminCreateServiceToken 为接入方生成 tzs- 服务令牌。明文不落库，仅本次响应返回。
func (c *integrationsAdminController) handleAdminCreateServiceToken(w http.ResponseWriter, r *http.Request) {
	it, ok := c.loadIntegration(w, r)
	if !ok {
		return
	}
	var req createServiceTokenRequest
	if !Bind(w, r, &req) {
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" || len(req.Name) > 64 {
		respond.Fail(w, http.StatusBadRequest, "令牌名称必填且不超过 64 字符")
		return
	}
	if it.Status != domain.IntegrationEnabled {
		respond.Fail(w, http.StatusBadRequest, "接入方已停用，不能签发新令牌")
		return
	}
	gen, err := auth.GenerateServiceToken()
	if err != nil {
		obs.Logger(r.Context()).Error("生成服务令牌失败", "error", err)
		respond.Fail(w, http.StatusInternalServerError, "生成服务令牌失败")
		return
	}
	t := &store.ServiceToken{
		IntegrationID: it.ID,
		Name:          req.Name,
		TokenHash:     gen.Hash,
		TokenPrefix:   gen.Prefix,
		Status:        domain.ServiceTokenEnabled,
	}
	if err := c.ServiceTokens.Create(r.Context(), t); err != nil {
		obs.Logger(r.Context()).Error("创建服务令牌失败", "error", err, "integration_id", it.ID)
		respond.Fail(w, http.StatusInternalServerError, "创建服务令牌失败")
		return
	}
	obs.Logger(r.Context()).Info("签发服务令牌",
		"integration_id", it.ID, "token_id", t.ID)
	c.Audit.Record(r, audit.Entry{
		Action: domain.AuditServiceTokenCreate, TargetType: domain.AuditTargetServiceToken,
		TargetID: t.ID, TargetName: t.Name,
		After: map[string]any{
			"integration_id": t.IntegrationID, "token_prefix": t.TokenPrefix, "status": t.Status,
		},
	})
	respond.Created(w, createdServiceTokenResponse{ServiceToken: *t, Token: gen.Plain})
}

// serviceTokenView 是令牌列表项，不含哈希。
type serviceTokenView struct {
	ID          int64                     `json:"id"`
	Name        string                    `json:"name"`
	TokenPrefix string                    `json:"token_prefix"`
	Status      domain.ServiceTokenStatus `json:"status"`
	LastUsedAt  *string                   `json:"last_used_at"`
	CreatedAt   string                    `json:"created_at"`
}

// handleAdminListServiceTokens 返回接入方名下的令牌列表，哈希不返回。
func (c *integrationsAdminController) handleAdminListServiceTokens(w http.ResponseWriter, r *http.Request) {
	it, ok := c.loadIntegration(w, r)
	if !ok {
		return
	}
	tokens, err := c.ServiceTokens.ListByIntegration(r.Context(), it.ID)
	if err != nil {
		obs.Logger(r.Context()).Error("查询服务令牌列表失败", "error", err, "integration_id", it.ID)
		respond.Fail(w, http.StatusInternalServerError, "查询服务令牌列表失败")
		return
	}
	items := make([]serviceTokenView, 0, len(tokens))
	for i := range tokens {
		t := tokens[i]
		var last string
		if t.LastUsedAt != nil {
			last = t.LastUsedAt.Format("2006-01-02T15:04:05Z07:00")
		}
		items = append(items, serviceTokenView{
			ID: t.ID, Name: t.Name, TokenPrefix: t.TokenPrefix, Status: t.Status,
			LastUsedAt: nullableStr(last), CreatedAt: t.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		})
	}
	respond.OK(w, items)
}

func nullableStr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

type updateServiceTokenStatusRequest struct {
	Status domain.ServiceTokenStatus `json:"status"`
}

// handleAdminSetServiceTokenStatus 停用或启用单个服务令牌。
func (c *integrationsAdminController) handleAdminSetServiceTokenStatus(w http.ResponseWriter, r *http.Request) {
	it, ok := c.loadIntegration(w, r)
	if !ok {
		return
	}
	tokenID, ok := IDParam(w, r, "token_id")
	if !ok {
		return
	}
	var req updateServiceTokenStatusRequest
	if !Bind(w, r, &req) {
		return
	}
	if req.Status != domain.ServiceTokenEnabled && req.Status != domain.ServiceTokenDisabled {
		respond.Fail(w, http.StatusBadRequest, "状态只能设置为 enabled 或 disabled")
		return
	}
	before, err := c.ServiceTokens.GetByID(r.Context(), tokenID)
	if err != nil || before.IntegrationID != it.ID {
		respond.Fail(w, http.StatusNotFound, "令牌不存在")
		return
	}
	if err := c.ServiceTokens.UpdateStatus(r.Context(), tokenID, req.Status); err != nil {
		respond.Fail(w, http.StatusNotFound, "令牌不存在")
		return
	}
	c.Audit.Record(r, audit.Entry{
		Action: domain.AuditServiceTokenStatus, TargetType: domain.AuditTargetServiceToken,
		TargetID: tokenID, TargetName: before.Name,
		Before: map[string]any{"status": before.Status},
		After:  map[string]any{"status": req.Status},
	})
	respond.OK(w, nil)
}

// handleAdminDeleteServiceToken 软删除服务令牌，记录保留供事后追溯。
func (c *integrationsAdminController) handleAdminDeleteServiceToken(w http.ResponseWriter, r *http.Request) {
	it, ok := c.loadIntegration(w, r)
	if !ok {
		return
	}
	tokenID, ok := IDParam(w, r, "token_id")
	if !ok {
		return
	}
	before, err := c.ServiceTokens.GetByID(r.Context(), tokenID)
	if err != nil || before.IntegrationID != it.ID {
		respond.Fail(w, http.StatusNotFound, "令牌不存在")
		return
	}
	if err := c.ServiceTokens.Delete(r.Context(), tokenID); err != nil {
		respond.Fail(w, http.StatusNotFound, "令牌不存在")
		return
	}
	obs.Logger(r.Context()).Info("软删服务令牌", "integration_id", it.ID, "token_id", tokenID)
	c.Audit.Record(r, audit.Entry{
		Action: domain.AuditServiceTokenDelete, TargetType: domain.AuditTargetServiceToken,
		TargetID: tokenID, TargetName: before.Name,
		Before: map[string]any{"token_prefix": before.TokenPrefix, "status": before.Status},
	})
	respond.OK(w, nil)
}

// handleAdminDisableIntegration 停用整个接入方：级联停用其全部服务令牌与用户（含服务账号）。
// 不删数据，账务历史完整保留；停用后其用户 Key 调 /v1 由既有鉴权按 user.Status 返回 403。
func (c *integrationsAdminController) handleAdminDisableIntegration(w http.ResponseWriter, r *http.Request) {
	it, ok := c.loadIntegration(w, r)
	if !ok {
		return
	}
	if it.Status == domain.IntegrationDisabled {
		respond.OK(w, nil)
		return
	}
	err := c.DB.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&store.Integration{}).Where("id = ?", it.ID).
			Updates(map[string]any{"status": domain.IntegrationDisabled, "updated_at": time.Now()}).Error; err != nil {
			return err
		}
		// 全部服务令牌置停用。
		if err := tx.Model(&store.ServiceToken{}).Where("integration_id = ?", it.ID).
			Updates(map[string]any{"status": domain.ServiceTokenDisabled, "updated_at": time.Now()}).Error; err != nil {
			return err
		}
		// 全部用户（含服务账号）置禁用。
		if err := tx.Model(&store.User{}).Where("integration_id = ?", it.ID).
			Updates(map[string]any{"status": domain.UserDisabled, "updated_at": time.Now()}).Error; err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		obs.Logger(r.Context()).Error("停用接入方失败", "integration_id", it.ID, "error", err)
		respond.Fail(w, http.StatusInternalServerError, "停用接入方失败")
		return
	}
	obs.Logger(r.Context()).Info("停用接入方（级联停用令牌与用户）", "integration_id", it.ID)
	c.Audit.Record(r, audit.Entry{
		Action: domain.AuditIntegrationDisable, TargetType: domain.AuditTargetIntegration,
		TargetID: it.ID, TargetName: it.Name,
		Before: integrationSnapshot(it),
		After:  map[string]any{"status": domain.IntegrationDisabled},
	})
	respond.OK(w, nil)
}

// loadIntegration 取路径中的接入方；不存在返回 404。
func (c *integrationsAdminController) loadIntegration(w http.ResponseWriter, r *http.Request) (*store.Integration, bool) {
	id, ok := IDParam(w, r, "id")
	if !ok {
		return nil, false
	}
	it, err := c.Integrations.GetByID(r.Context(), id)
	if err != nil {
		respond.Fail(w, http.StatusNotFound, "接入方不存在")
		return nil, false
	}
	return it, true
}

// integrationSnapshot 把接入方压成审计用的字段快照。
func integrationSnapshot(it *store.Integration) map[string]any {
	if it == nil {
		return nil
	}
	return map[string]any{
		"name": it.Name, "slug": it.Slug, "status": it.Status,
	}
}

// pageBounds 计算切片分页的 [start, end) 区间。
func pageBounds(total, page, pageSize int) (int, int) {
	start := (page - 1) * pageSize
	if start > total {
		start = total
	}
	end := start + pageSize
	if end > total {
		end = total
	}
	return start, end
}
