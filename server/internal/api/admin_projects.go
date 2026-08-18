package api

import (
	"net/http"
	"strings"

	"github.com/liguopeng80/tokenzen-lite/server/internal/api/respond"
	"github.com/liguopeng80/tokenzen-lite/server/internal/audit"
	"github.com/liguopeng80/tokenzen-lite/server/internal/auth"
	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/obs"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// admin_projects.go：项目 CRUD。项目与部门同构——扁平单层、不持余额、不参与扣费，
// 仅作与部门正交的第二层成本归属维度。权限归运营桶（requireAdmin，admin/root），
// 托管令牌按接入方作用域访问本接入方项目。鉴权与审计范式全部照 admin_departments.go。

type projectPayload struct {
	Name                 string               `json:"name"`
	Code                 string               `json:"code"`
	OwnerUserID          *int64               `json:"owner_user_id"`
	MonthlyBudgetCredits domain.Credits       `json:"monthly_budget_credits"`
	Status               domain.ProjectStatus `json:"status"`
	Note                 string               `json:"note"`
	// ExternalRef 接入方侧的项目标识，写入后不可变更。
	ExternalRef string `json:"external_ref"`
	// IdempotencyKey 携带后，重复提交只生效一次，第二次返回首次结果并标明重放。
	IdempotencyKey string `json:"idempotency_key"`
}

// validate 校验项目载荷；返回面向管理员的错误信息，空串表示通过。
func (p *projectPayload) validate() string {
	p.Name = strings.TrimSpace(p.Name)
	p.Code = strings.TrimSpace(p.Code)
	if p.Name == "" {
		return "项目名称必填"
	}
	if len(p.Name) > 100 {
		return "项目名称不得超过 100 个字符"
	}
	if len(p.Code) > 50 {
		return "项目编码不得超过 50 个字符"
	}
	if p.MonthlyBudgetCredits < 0 {
		return "月度预算不能为负数"
	}
	if p.Status == "" {
		p.Status = domain.ProjectEnabled
	}
	if !p.Status.Valid() {
		return "项目状态取值不合法"
	}
	return ""
}

// canAccessProject 判断操作者能否访问目标项目。
// 运营 admin/root 不限；托管 managed 要求项目归属本接入方作用域。
func canAccessProject(actor *store.User, p *store.Project) bool {
	if actor == nil || p == nil {
		return false
	}
	if actor.Role == domain.RoleRoot || actor.Role == domain.RoleAdmin {
		return true
	}
	if actor.Role == domain.RoleManaged {
		return p.IntegrationID != nil && actor.IntegrationID != nil &&
			*p.IntegrationID == *actor.IntegrationID
	}
	return false
}

// projectWithMoney 包装 store.Project，旁置月度预算的货币串。
// 用于单对象返回（get / external-ref 检索）。
type projectWithMoney struct {
	store.Project
	MonthlyBudgetCreditsMoney string `json:"monthly_budget_credits_money"`
}

func wrapProject(p store.Project, mc moneyCtx) projectWithMoney {
	return projectWithMoney{
		Project:                   p,
		MonthlyBudgetCreditsMoney: mc.money(p.MonthlyBudgetCredits),
	}
}

// projectWithStatsWithMoney 包装 store.ProjectWithStats（列表行），旁置月度预算的货币串。
type projectWithStatsWithMoney struct {
	store.ProjectWithStats
	MonthlyBudgetCreditsMoney string `json:"monthly_budget_credits_money"`
}

func wrapProjectWithStats(p store.ProjectWithStats, mc moneyCtx) projectWithStatsWithMoney {
	return projectWithStatsWithMoney{
		ProjectWithStats:          p,
		MonthlyBudgetCreditsMoney: mc.money(p.MonthlyBudgetCredits),
	}
}

func (c *catalogAdminController) handleAdminListProjects(w http.ResponseWriter, r *http.Request) {
	page, pageSize := PageParams(r)
	q := r.URL.Query()
	f := store.ProjectListFilter{
		Keyword: q.Get("keyword"),
		Status:  domain.ProjectStatus(q.Get("status")),
		Page:    page, PageSize: pageSize,
	}
	// 托管视角只看本接入方作用域内的项目；运营 admin/root 不受限。
	if iid := auth.ScopeIntegrationID(r.Context()); iid != nil {
		f.IntegrationID = iid
	}
	items, total, err := c.Projects.List(r.Context(), f)
	if err != nil {
		obs.Logger(r.Context()).Error("查询项目列表失败", "error", err)
		respond.Fail(w, http.StatusInternalServerError, "查询项目列表失败")
		return
	}
	rate := c.Settings.GetInt64(r.Context(), "exchange_rate_credits_per_cny")
	mc := newMoneyCtx(rate)
	respond.OK(w, respond.NewPage(page, pageSize, total,
		wrapList(items, func(p store.ProjectWithStats) projectWithStatsWithMoney {
			return wrapProjectWithStats(p, mc)
		})))
}

func (c *catalogAdminController) handleAdminCreateProject(w http.ResponseWriter, r *http.Request) {
	var req projectPayload
	if !Bind(w, r, &req) {
		return
	}
	if msg := req.validate(); msg != "" {
		respond.Fail(w, http.StatusBadRequest, msg)
		return
	}
	req.ExternalRef = strings.TrimSpace(req.ExternalRef)
	if req.ExternalRef != "" && !externalRefRe.MatchString(req.ExternalRef) {
		respond.Fail(w, http.StatusBadRequest, "外部标识须为 1-64 位字母、数字、下划线、连字符、小数点或冒号")
		return
	}
	if req.IdempotencyKey != "" && !idempotencyKeyRe.MatchString(req.IdempotencyKey) {
		respond.Fail(w, http.StatusBadRequest, "幂等键须为 1-64 位字母、数字、下划线或连字符")
		return
	}
	if firstID, ok := idempotencyLookupReplay(r.Context(), c.Idempotency, req.IdempotencyKey, "project.create"); ok {
		if prior, err := c.Projects.GetByID(r.Context(), firstID); err == nil {
			respond.OKMessage(w, "该项目已创建，本次未重复执行（重放）", prior)
			return
		}
	}
	p := &store.Project{
		Name: req.Name, Code: req.Code, OwnerUserID: req.OwnerUserID,
		MonthlyBudgetCredits: req.MonthlyBudgetCredits,
		Status:               req.Status, Note: req.Note, ExternalRef: req.ExternalRef,
	}
	// 托管视角新建的项目自动归入本接入方作用域；运营 admin/root 建的为内部对象（NULL）。
	if iid := auth.ScopeIntegrationID(r.Context()); iid != nil {
		p.IntegrationID = iid
	}
	if err := c.Projects.Create(r.Context(), p); err != nil {
		obs.Logger(r.Context()).Error("创建项目失败", "error", err)
		respond.Fail(w, http.StatusConflict, "项目名称或编码已存在")
		return
	}
	if req.IdempotencyKey != "" {
		idempotencyRemember(r.Context(), c.Idempotency, req.IdempotencyKey, "project.create", p.ID)
	}
	c.Audit.Record(r, audit.Entry{
		Action: domain.AuditProjectCreate, TargetType: domain.AuditTargetProject,
		TargetID: p.ID, TargetName: p.Name, After: projectSnapshot(p),
	})
	respond.Created(w, p)
}

func (c *catalogAdminController) handleAdminGetProject(w http.ResponseWriter, r *http.Request) {
	p, ok := c.loadProject(w, r)
	if !ok {
		return
	}
	rate := c.Settings.GetInt64(r.Context(), "exchange_rate_credits_per_cny")
	respond.OK(w, wrapProject(*p, newMoneyCtx(rate)))
}

func (c *catalogAdminController) loadProject(w http.ResponseWriter, r *http.Request) (*store.Project, bool) {
	id, ok := IDParam(w, r, "id")
	if !ok {
		return nil, false
	}
	p, err := c.Projects.GetByID(r.Context(), id)
	if err != nil {
		respond.Fail(w, http.StatusNotFound, "项目不存在")
		return nil, false
	}
	// 托管视角跨作用域访问按不存在处理，避免借端点探测对象 ID（同 loadDepartment）。
	if !canAccessProject(auth.CurrentUser(r.Context()), p) {
		respond.Fail(w, http.StatusNotFound, "项目不存在")
		return nil, false
	}
	return p, true
}

func (c *catalogAdminController) handleAdminUpdateProject(w http.ResponseWriter, r *http.Request) {
	p, ok := c.loadProject(w, r)
	if !ok {
		return
	}
	var req projectPayload
	if !Bind(w, r, &req) {
		return
	}
	if msg := req.validate(); msg != "" {
		respond.Fail(w, http.StatusBadRequest, msg)
		return
	}
	fields := map[string]any{
		"name": req.Name, "code": req.Code, "owner_user_id": req.OwnerUserID,
		"monthly_budget_credits": req.MonthlyBudgetCredits,
		"status":                 req.Status, "note": req.Note,
	}
	if err := c.Projects.UpdateFields(r.Context(), p.ID, fields); err != nil {
		respond.Fail(w, http.StatusConflict, "项目名称或编码已存在")
		return
	}
	c.Audit.Record(r, audit.Entry{
		Action: domain.AuditProjectUpdate, TargetType: domain.AuditTargetProject,
		TargetID: p.ID, TargetName: req.Name,
		Before: projectSnapshot(p), After: fields,
	})
	respond.OK(w, nil)
}

// handleAdminDeleteProject 删除项目。api_keys.project_id 为 ON DELETE SET NULL，
// 归属该项目的密钥会被自动置为未归属；已产生的用量日志与按日汇总中的项目快照保留原 ID，
// 报表显示为「已删除项目 #N」（同部门删除的快照保留范式）。
func (c *catalogAdminController) handleAdminDeleteProject(w http.ResponseWriter, r *http.Request) {
	p, ok := c.loadProject(w, r)
	if !ok {
		return
	}
	if err := c.Projects.Delete(r.Context(), p.ID); err != nil {
		obs.Logger(r.Context()).Error("删除项目失败", "project_id", p.ID, "error", err)
		respond.Fail(w, http.StatusInternalServerError, "删除项目失败")
		return
	}
	c.Audit.Record(r, audit.Entry{
		Action: domain.AuditProjectDelete, TargetType: domain.AuditTargetProject,
		TargetID: p.ID, TargetName: p.Name, Before: projectSnapshot(p),
	})
	respond.OK(w, nil)
}

// projectSnapshot 把项目压成审计用的字段快照。
func projectSnapshot(p *store.Project) map[string]any {
	if p == nil {
		return nil
	}
	return map[string]any{
		"name": p.Name, "code": p.Code, "owner_user_id": p.OwnerUserID,
		"monthly_budget_credits": p.MonthlyBudgetCredits,
		"status":                 p.Status, "note": p.Note,
		"external_ref": p.ExternalRef,
	}
}
