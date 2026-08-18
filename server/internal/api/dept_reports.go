package api

import (
	"net/http"
	"time"

	"github.com/liguopeng80/tokenzen-lite/server/internal/api/respond"
	"github.com/liguopeng80/tokenzen-lite/server/internal/auth"
	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/obs"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// 部门负责人视图：让部门负责人自助查看本部门的消费与预算进度，
// 不必为此授予管理员权限（管理员权限连带渠道配置、全员积分发放与全站用量）。
//
// 授权依据是 departments.owner_user_id 与当前登录用户的归属关系，不是用户角色：
// 角色一经授予就脱离部门归属，转岗后仍保留原部门的查账能力，且无法表达
// 「同一人负责两个部门」。因此本组端点全部按请求中的 department_id 逐次校验归属。
//
// deptController 承载 /dept/* 端点：负责人可见部门列表、预算、成本报表、成员。
type deptController struct {
	Departments *store.DepartmentRepo
	Rollup      *store.RollupRepo
	Users       *store.UserRepo
}

// 返回口径只含本部门的消费额与用量，不含网关的采购成本与差额——那是网关运营方的
// 数据，与部门的费用分摊无关。

// deptAggRow 是部门负责人可见的聚合行，相对 store.AggRow 去掉成本与差额字段。
type deptAggRow struct {
	GroupID          int64          `json:"group_id"`
	GroupKey         string         `json:"group_key"`
	Requests         int64          `json:"requests"`
	PromptTokens     int64          `json:"prompt_tokens"`
	CompletionTokens int64          `json:"completion_tokens"`
	CreditsCharged   domain.Credits `json:"credits_charged"`
}

func toDeptAggRows(rows []store.AggRow) []deptAggRow {
	out := make([]deptAggRow, 0, len(rows))
	for _, row := range rows {
		out = append(out, deptAggRow{
			GroupID: row.GroupID, GroupKey: row.GroupKey, Requests: row.Requests,
			PromptTokens: row.PromptTokens, CompletionTokens: row.CompletionTokens,
			CreditsCharged: row.CreditsCharged,
		})
	}
	return out
}

// deptAggRowWithMoney 包装 deptAggRow，旁置扣费的货币串。
// 由管理端托管视角费用报表（handleAdminCostReport 的 managed 分支）消费；
// /api/dept/* 部门负责人视图不暴露金额（非第三方可达的程序化消费路径），沿用 deptAggRow。
type deptAggRowWithMoney struct {
	deptAggRow
	CreditsChargedMoney string `json:"credits_charged_money"`
}

func wrapDeptAggRow(r deptAggRow, mc moneyCtx) deptAggRowWithMoney {
	return deptAggRowWithMoney{deptAggRow: r, CreditsChargedMoney: mc.money(r.CreditsCharged)}
}

// managedDepartments 返回当前登录用户担任负责人的部门。
func (c *deptController) managedDepartments(r *http.Request) ([]store.Department, error) {
	u := auth.CurrentUser(r.Context())
	if u == nil {
		return nil, nil
	}
	return c.Departments.ListByOwner(r.Context(), u.ID)
}

// resolveManagedDepartment 解析 department_id 并校验其归属于当前登录用户。
// 未通过时已写出响应，返回 false。归属校验对非负责人与「负责其它部门的人」
// 返回同一个 403，不透露该部门是否存在。
func (c *deptController) resolveManagedDepartment(w http.ResponseWriter, r *http.Request) (*store.Department, bool) {
	id, ok := parseInt64(r.URL.Query().Get("department_id"))
	if !ok {
		respond.Fail(w, http.StatusBadRequest, "缺少 department_id")
		return nil, false
	}
	owned, err := c.managedDepartments(r)
	if err != nil {
		obs.Logger(r.Context()).Error("查询负责部门失败", "error", err)
		respond.Fail(w, http.StatusInternalServerError, "查询负责部门失败")
		return nil, false
	}
	for i := range owned {
		if owned[i].ID == id {
			return &owned[i], true
		}
	}
	obs.Logger(r.Context()).Warn("越权访问部门报表被拒绝",
		"user_id", auth.CurrentUser(r.Context()).ID, "department_id", id)
	respond.Fail(w, http.StatusForbidden, "无权查看该部门")
	return nil, false
}

// handleDeptListDepartments 返回当前用户负责的部门列表。
// 列表为空说明该账号不是任何部门的负责人，前端据此隐藏部门费用入口。
func (c *deptController) handleDeptListDepartments(w http.ResponseWriter, r *http.Request) {
	owned, err := c.managedDepartments(r)
	if err != nil {
		obs.Logger(r.Context()).Error("查询负责部门失败", "error", err)
		respond.Fail(w, http.StatusInternalServerError, "查询负责部门失败")
		return
	}
	rows := make([]map[string]any, 0, len(owned))
	for i := range owned {
		rows = append(rows, map[string]any{
			"id":                     owned[i].ID,
			"name":                   owned[i].Name,
			"code":                   owned[i].Code,
			"status":                 owned[i].Status,
			"monthly_budget_credits": owned[i].MonthlyBudgetCredits,
		})
	}
	respond.OK(w, rows)
}

// handleDeptBudget 出本部门指定自然月的消费与预算对比。
func (c *deptController) handleDeptBudget(w http.ResponseWriter, r *http.Request) {
	dept, ok := c.resolveManagedDepartment(w, r)
	if !ok {
		return
	}
	month, ok := monthFromQuery(w, r)
	if !ok {
		return
	}
	from, to := store.MonthRange(month)
	rows, err := c.Rollup.Aggregate(r.Context(), store.AggByDepartment,
		store.AggFilter{From: from, To: to, DepartmentID: &dept.ID})
	if err != nil {
		obs.Logger(r.Context()).Error("部门预算对比查询失败",
			"department_id", dept.ID, "error", err)
		respond.Fail(w, http.StatusInternalServerError, "部门预算对比查询失败")
		return
	}
	// 本月无消费时聚合结果为空，仍需返回一行零值：否则前端无法区分
	// 「本月尚未产生消费」与「查询失败」。
	var agg store.AggRow
	if len(rows) > 0 {
		agg = rows[0]
	}
	out := map[string]any{
		"month":                  from.Format("2006-01"),
		"department_id":          dept.ID,
		"department_name":        dept.Name,
		"requests":               agg.Requests,
		"prompt_tokens":          agg.PromptTokens,
		"completion_tokens":      agg.CompletionTokens,
		"credits_charged":        agg.CreditsCharged,
		"monthly_budget_credits": dept.MonthlyBudgetCredits,
		"budget_used_percent":    int64(0),
		"over_budget":            false,
	}
	if dept.MonthlyBudgetCredits > 0 {
		out["budget_used_percent"] = agg.CreditsCharged * 100 / dept.MonthlyBudgetCredits
		out["over_budget"] = agg.CreditsCharged > dept.MonthlyBudgetCredits
	}
	respond.OK(w, out)
}

// deptReportDimensions 是部门负责人可用的聚合维度。
// 不含 department（已固定为本部门）与 channel（渠道是网关运营方的配置，
// 对部门费用分摊无意义且会暴露上游供应商构成）。
// 含 project：让负责人在本部门内按项目下钻（项目与部门正交，同一部门可有多个项目）。
var deptReportDimensions = map[store.AggDimension]bool{
	store.AggByUser: true, store.AggByModel: true, store.AggByDay: true,
	store.AggByProject: true,
}

// handleDeptCostReport 按成员、模型或日期出本部门的费用明细。
func (c *deptController) handleDeptCostReport(w http.ResponseWriter, r *http.Request) {
	dept, ok := c.resolveManagedDepartment(w, r)
	if !ok {
		return
	}
	dim := store.AggDimension(r.URL.Query().Get("group_by"))
	if !deptReportDimensions[dim] {
		dim = store.AggByUser
	}
	f := aggFilterFromQuery(r)
	// 部门范围强制覆盖请求中的任何部门筛选，防止改 department_id 之外的参数越权。
	f.DepartmentID = &dept.ID
	rows, err := c.Rollup.Aggregate(r.Context(), dim, f)
	if err != nil {
		obs.Logger(r.Context()).Error("部门费用报表查询失败",
			"department_id", dept.ID, "group_by", dim, "error", err)
		respond.Fail(w, http.StatusInternalServerError, "部门费用报表查询失败")
		return
	}
	respond.OK(w, map[string]any{
		"group_by":        dim,
		"department_id":   dept.ID,
		"department_name": dept.Name,
		"from":            f.From.Unix(),
		"to":              f.To.Unix(),
		"rows":            toDeptAggRows(rows),
	})
}

// deptMemberRow 是部门成员行。只含负责人管理成本所需的字段：
// 不含邮箱与模型策略，那些属于账号管理范畴，由管理员维护。
type deptMemberRow struct {
	UserID       int64             `json:"user_id"`
	Username     string            `json:"username"`
	DisplayName  string            `json:"display_name"`
	Status       domain.UserStatus `json:"status"`
	Balance      domain.Credits    `json:"credit_balance"`
	MonthCharged domain.Credits    `json:"month_credits_charged"`
	MonthRequest int64             `json:"month_requests"`
}

// handleDeptMembers 列出本部门成员及其余额与当月消费。
// 未产生消费的成员也要列出，否则「本月没用」与「不在本部门」无法区分。
func (c *deptController) handleDeptMembers(w http.ResponseWriter, r *http.Request) {
	dept, ok := c.resolveManagedDepartment(w, r)
	if !ok {
		return
	}
	page, pageSize := PageParams(r)
	users, total, err := c.Users.List(r.Context(), store.UserListFilter{
		DepartmentID: &dept.ID, Page: page, PageSize: pageSize,
	})
	if err != nil {
		obs.Logger(r.Context()).Error("部门成员查询失败",
			"department_id", dept.ID, "error", err)
		respond.Fail(w, http.StatusInternalServerError, "部门成员查询失败")
		return
	}
	from, to := store.MonthRange(time.Now())
	spent, err := c.Rollup.Aggregate(r.Context(), store.AggByUser,
		store.AggFilter{From: from, To: to, DepartmentID: &dept.ID})
	if err != nil {
		obs.Logger(r.Context()).Error("部门成员消费查询失败",
			"department_id", dept.ID, "error", err)
		respond.Fail(w, http.StatusInternalServerError, "部门成员消费查询失败")
		return
	}
	byUser := make(map[int64]store.AggRow, len(spent))
	for _, row := range spent {
		byUser[row.GroupID] = row
	}
	rows := make([]deptMemberRow, 0, len(users))
	for i := range users {
		agg := byUser[users[i].ID]
		rows = append(rows, deptMemberRow{
			UserID: users[i].ID, Username: users[i].Username,
			DisplayName: users[i].DisplayName, Status: users[i].Status,
			Balance:      users[i].CreditBalance,
			MonthCharged: agg.CreditsCharged, MonthRequest: agg.Requests,
		})
	}
	respond.OK(w, respond.NewPage(page, pageSize, total, rows))
}
