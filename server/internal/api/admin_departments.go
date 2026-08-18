package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/liguopeng80/tokenzen-lite/server/internal/api/respond"
	"github.com/liguopeng80/tokenzen-lite/server/internal/audit"
	"github.com/liguopeng80/tokenzen-lite/server/internal/auth"
	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/obs"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// maxBatchMembers 单次成员划转的规模上限，与批量发放保持一致。
const maxBatchMembers = 500

// orgAdminController 承载托管桶的组织实体端点：部门 CRUD/成员划转（admin_departments）
// 与审计日志查询（admin_audit）。部门操作走幂等键（department.create），审计只读。
type orgAdminController struct {
	Audit       *audit.Recorder
	Departments *store.DepartmentRepo
	Users       *store.UserRepo
	AuditLogs   *store.AuditLogRepo
	Idempotency *store.IdempotencyRepo
	Settings    *store.SettingsRepo
}

// departmentWithMoney 包装 store.Department，旁置月度预算的货币串。
// 用于单对象返回（get / external-ref 检索）。
type departmentWithMoney struct {
	store.Department
	MonthlyBudgetCreditsMoney string `json:"monthly_budget_credits_money"`
}

func wrapDepartment(d store.Department, mc moneyCtx) departmentWithMoney {
	return departmentWithMoney{
		Department:                d,
		MonthlyBudgetCreditsMoney: mc.money(d.MonthlyBudgetCredits),
	}
}

// departmentWithStatsWithMoney 包装 store.DepartmentWithStats（列表行），旁置月度预算的货币串。
type departmentWithStatsWithMoney struct {
	store.DepartmentWithStats
	MonthlyBudgetCreditsMoney string `json:"monthly_budget_credits_money"`
}

func wrapDepartmentWithStats(d store.DepartmentWithStats, mc moneyCtx) departmentWithStatsWithMoney {
	return departmentWithStatsWithMoney{
		DepartmentWithStats:       d,
		MonthlyBudgetCreditsMoney: mc.money(d.MonthlyBudgetCredits),
	}
}

type departmentPayload struct {
	Name                 string                  `json:"name"`
	Code                 string                  `json:"code"`
	OwnerUserID          *int64                  `json:"owner_user_id"`
	MonthlyBudgetCredits domain.Credits          `json:"monthly_budget_credits"`
	AllowedModels        []string                `json:"allowed_models"`
	Status               domain.DepartmentStatus `json:"status"`
	Note                 string                  `json:"note"`
	// ExternalRef 接入方侧的部门标识，写入后不可变更。
	ExternalRef string `json:"external_ref"`
	// IdempotencyKey 携带后，重复提交只生效一次，第二次返回首次结果并标明重放（R4）。
	IdempotencyKey string `json:"idempotency_key"`
}

// validate 校验部门载荷；返回面向管理员的错误信息，空串表示通过。
func (p *departmentPayload) validate() string {
	p.Name = strings.TrimSpace(p.Name)
	p.Code = strings.TrimSpace(p.Code)
	if p.Name == "" {
		return "部门名称必填"
	}
	if len(p.Name) > 100 {
		return "部门名称不得超过 100 个字符"
	}
	if len(p.Code) > 50 {
		return "成本中心编码不得超过 50 个字符"
	}
	if p.MonthlyBudgetCredits < 0 {
		return "月度预算不能为负数"
	}
	if p.Status == "" {
		p.Status = domain.DepartmentEnabled
	}
	if !p.Status.Valid() {
		return "部门状态取值不合法"
	}
	return ""
}

// checkOwnerIsMember 校验部门负责人是本部门成员。负责人能看到全部门的消费明细与
// 成员余额，若允许指定部门外的人，等于绕开部门归属把成本数据开放出去。
// deptID 为 0 表示部门尚未创建，此时不存在任何成员，只能先建部门再指定负责人。
func (c *orgAdminController) checkOwnerIsMember(r *http.Request, ownerID *int64, deptID int64) string {
	if ownerID == nil {
		return ""
	}
	if deptID == 0 {
		return "新建部门时还没有成员，请先创建部门并划入成员，再指定负责人"
	}
	owner, err := c.Users.GetByID(r.Context(), *ownerID)
	if err != nil {
		return "指定的负责人不存在"
	}
	if owner.DepartmentID == nil || *owner.DepartmentID != deptID {
		return "负责人必须是本部门成员，请先把该用户划入本部门"
	}
	return ""
}

// canAccessDepartment 判断操作者能否访问目标部门。
// 运营 admin/root 不限；托管 managed 要求部门归属本接入方作用域。
func canAccessDepartment(actor *store.User, dept *store.Department) bool {
	if actor == nil || dept == nil {
		return false
	}
	if actor.Role == domain.RoleRoot || actor.Role == domain.RoleAdmin {
		return true
	}
	if actor.Role == domain.RoleManaged {
		return dept.IntegrationID != nil && actor.IntegrationID != nil &&
			*dept.IntegrationID == *actor.IntegrationID
	}
	return false
}

func (c *orgAdminController) handleAdminListDepartments(w http.ResponseWriter, r *http.Request) {
	page, pageSize := PageParams(r)
	q := r.URL.Query()
	f := store.DepartmentListFilter{
		Keyword: q.Get("keyword"),
		Status:  domain.DepartmentStatus(q.Get("status")),
		Page:    page, PageSize: pageSize,
	}
	// 托管视角只看本接入方作用域内的部门；运营 admin/root 不受限。
	if iid := auth.ScopeIntegrationID(r.Context()); iid != nil {
		f.IntegrationID = iid
	}
	items, total, err := c.Departments.List(r.Context(), f)
	if err != nil {
		obs.Logger(r.Context()).Error("查询部门列表失败", "error", err)
		respond.Fail(w, http.StatusInternalServerError, "查询部门列表失败")
		return
	}
	rate := c.Settings.GetInt64(r.Context(), "exchange_rate_credits_per_cny")
	mc := newMoneyCtx(rate)
	respond.OK(w, respond.NewPage(page, pageSize, total,
		wrapList(items, func(d store.DepartmentWithStats) departmentWithStatsWithMoney {
			return wrapDepartmentWithStats(d, mc)
		})))
}

func (c *orgAdminController) handleAdminCreateDepartment(w http.ResponseWriter, r *http.Request) {
	var req departmentPayload
	if !Bind(w, r, &req) {
		return
	}
	if msg := req.validate(); msg != "" {
		respond.Fail(w, http.StatusBadRequest, msg)
		return
	}
	if msg := c.checkOwnerIsMember(r, req.OwnerUserID, 0); msg != "" {
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
	if firstID, ok := idempotencyLookupReplay(r.Context(), c.Idempotency, req.IdempotencyKey, "department.create"); ok {
		if prior, err := c.Departments.GetByID(r.Context(), firstID); err == nil {
			respond.OKMessage(w, "该部门已创建，本次未重复执行（重放）", prior)
			return
		}
	}
	dept := &store.Department{
		Name: req.Name, Code: req.Code, OwnerUserID: req.OwnerUserID,
		MonthlyBudgetCredits: req.MonthlyBudgetCredits,
		AllowedModels:        toJSONField(req.AllowedModels),
		Status:               req.Status, Note: req.Note, ExternalRef: req.ExternalRef,
	}
	// 托管视角新建的部门自动归入本接入方作用域；运营 admin/root 建的部门为内部对象（NULL）。
	if iid := auth.ScopeIntegrationID(r.Context()); iid != nil {
		dept.IntegrationID = iid
	}
	if err := c.Departments.Create(r.Context(), dept); err != nil {
		obs.Logger(r.Context()).Error("创建部门失败", "error", err)
		respond.Fail(w, http.StatusConflict, "部门名称或成本中心编码已存在")
		return
	}
	if req.IdempotencyKey != "" {
		idempotencyRemember(r.Context(), c.Idempotency, req.IdempotencyKey, "department.create", dept.ID)
	}
	c.Audit.Record(r, audit.Entry{
		Action: domain.AuditDepartmentCreate, TargetType: domain.AuditTargetDepartment,
		TargetID: dept.ID, TargetName: dept.Name, After: departmentSnapshot(dept),
	})
	respond.Created(w, dept)
}

func (c *orgAdminController) handleAdminGetDepartment(w http.ResponseWriter, r *http.Request) {
	dept, ok := c.loadDepartment(w, r)
	if !ok {
		return
	}
	rate := c.Settings.GetInt64(r.Context(), "exchange_rate_credits_per_cny")
	respond.OK(w, wrapDepartment(*dept, newMoneyCtx(rate)))
}

func (c *orgAdminController) loadDepartment(w http.ResponseWriter, r *http.Request) (*store.Department, bool) {
	id, ok := IDParam(w, r, "id")
	if !ok {
		return nil, false
	}
	dept, err := c.Departments.GetByID(r.Context(), id)
	if err != nil {
		respond.Fail(w, http.StatusNotFound, "部门不存在")
		return nil, false
	}
	// 托管视角跨作用域访问按不存在处理，避免借端点探测对象 ID（与 loadManagedUser 一致）。
	if !canAccessDepartment(auth.CurrentUser(r.Context()), dept) {
		respond.Fail(w, http.StatusNotFound, "部门不存在")
		return nil, false
	}
	return dept, true
}

func (c *orgAdminController) handleAdminUpdateDepartment(w http.ResponseWriter, r *http.Request) {
	dept, ok := c.loadDepartment(w, r)
	if !ok {
		return
	}
	var req departmentPayload
	if !Bind(w, r, &req) {
		return
	}
	if msg := req.validate(); msg != "" {
		respond.Fail(w, http.StatusBadRequest, msg)
		return
	}
	if msg := c.checkOwnerIsMember(r, req.OwnerUserID, dept.ID); msg != "" {
		respond.Fail(w, http.StatusBadRequest, msg)
		return
	}
	fields := map[string]any{
		"name": req.Name, "code": req.Code, "owner_user_id": req.OwnerUserID,
		"monthly_budget_credits": req.MonthlyBudgetCredits,
		"allowed_models":         toJSONField(req.AllowedModels),
		"status":                 req.Status, "note": req.Note,
	}
	if err := c.Departments.UpdateFields(r.Context(), dept.ID, fields); err != nil {
		respond.Fail(w, http.StatusConflict, "部门名称或成本中心编码已存在")
		return
	}
	c.Audit.Record(r, audit.Entry{
		Action: domain.AuditDepartmentUpdate, TargetType: domain.AuditTargetDepartment,
		TargetID: dept.ID, TargetName: req.Name,
		Before: departmentSnapshot(dept), After: fields,
	})
	respond.OK(w, nil)
}

// handleAdminDeleteDepartment 删除部门。仍有成员时拒绝：成员的部门归属是外键，
// 且已产生的用量日志与流水仍按该部门 ID 归集，直接删除会让报表出现无名分组。
func (c *orgAdminController) handleAdminDeleteDepartment(w http.ResponseWriter, r *http.Request) {
	dept, ok := c.loadDepartment(w, r)
	if !ok {
		return
	}
	if err := c.Departments.Delete(r.Context(), dept.ID); err != nil {
		if errors.Is(err, store.ErrDepartmentHasMembers) {
			msg := "该部门仍有成员，请先把成员转出或改分到其他部门"
			c.Audit.Record(r, audit.Entry{
				Action: domain.AuditDepartmentDelete, TargetType: domain.AuditTargetDepartment,
				TargetID: dept.ID, TargetName: dept.Name,
				Result: domain.AuditFailure, Message: msg,
			})
			respond.Fail(w, http.StatusConflict, msg)
			return
		}
		obs.Logger(r.Context()).Error("删除部门失败", "department_id", dept.ID, "error", err)
		respond.Fail(w, http.StatusInternalServerError, "删除部门失败")
		return
	}
	c.Audit.Record(r, audit.Entry{
		Action: domain.AuditDepartmentDelete, TargetType: domain.AuditTargetDepartment,
		TargetID: dept.ID, TargetName: dept.Name, Before: departmentSnapshot(dept),
	})
	respond.OK(w, nil)
}

type departmentMembersRequest struct {
	UserIDs []int64 `json:"user_ids"`
	// Remove 为 true 时把这些用户转为未分配部门。
	Remove bool `json:"remove"`
}

// handleAdminSetDepartmentMembers 批量划入或转出部门成员。
func (c *orgAdminController) handleAdminSetDepartmentMembers(w http.ResponseWriter, r *http.Request) {
	dept, ok := c.loadDepartment(w, r)
	if !ok {
		return
	}
	var req departmentMembersRequest
	if !Bind(w, r, &req) {
		return
	}
	if len(req.UserIDs) == 0 {
		respond.Fail(w, http.StatusBadRequest, "请至少选择一个用户")
		return
	}
	if len(req.UserIDs) > maxBatchMembers {
		respond.Fail(w, http.StatusBadRequest, "单次最多划转 500 个用户，请分批提交")
		return
	}
	var target *int64
	if !req.Remove {
		if dept.Status != domain.DepartmentEnabled {
			respond.Fail(w, http.StatusBadRequest, "该部门已停用，不能再分配新成员")
			return
		}
		id := dept.ID
		target = &id
	}
	affected, err := c.Departments.AssignMembers(r.Context(), req.UserIDs, target)
	if err != nil {
		obs.Logger(r.Context()).Error("划转部门成员失败", "department_id", dept.ID, "error", err)
		respond.Fail(w, http.StatusInternalServerError, "划转部门成员失败")
		return
	}
	c.Audit.Record(r, audit.Entry{
		Action: domain.AuditDepartmentMembers, TargetType: domain.AuditTargetDepartment,
		TargetID: dept.ID, TargetName: dept.Name,
		After: map[string]any{
			"user_ids": req.UserIDs, "remove": req.Remove, "affected": affected,
		},
	})
	respond.OKMessage(w, "已更新部门成员", map[string]any{"affected": affected})
}

// departmentSnapshot 把部门压成审计用的字段快照。
func departmentSnapshot(dept *store.Department) map[string]any {
	if dept == nil {
		return nil
	}
	return map[string]any{
		"name": dept.Name, "code": dept.Code, "owner_user_id": dept.OwnerUserID,
		"monthly_budget_credits": dept.MonthlyBudgetCredits,
		"allowed_models":         json.RawMessage(nullIfEmptyJSON(dept.AllowedModels)),
		"status":                 dept.Status, "note": dept.Note,
	}
}
