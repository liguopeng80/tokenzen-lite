package api

import (
	"encoding/csv"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/liguopeng80/tokenzen-lite/server/internal/api/respond"
	"github.com/liguopeng80/tokenzen-lite/server/internal/auth"
	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/obs"
	"github.com/liguopeng80/tokenzen-lite/server/internal/pricing"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// exportPageSize 导出时的分页拉取批量。逐批拉取而非一次性载入，
// 使导出全量数据的内存占用与总记录数无关。
const exportPageSize = 1000

// reportsAdminController 承载管理端只读报表与统计端点（托管桶 /admin/stats/* 与
// /admin/usage-logs、/admin/reports）：成本口径报表、部门/项目预算、用量日志列表/
// 导出、利润/总览、用量日趋势、热力图、健康时间线、运维摘要、调用类型成本、
// 模型-渠道成本明细。跨 admin_reports / stats_admin 两文件共享。
type reportsAdminController struct {
	Departments *store.DepartmentRepo
	Projects    *store.ProjectRepo
	Rollup      *store.RollupRepo
	UsageLogs   *store.UsageLogRepo
	Costs       *store.ChannelCostRepo
	Models      *store.ModelRepo
	Stats       *store.StatsRepo
	Settings    *store.SettingsRepo
}

// maxExportRows 单次导出的行数上限，防止一次导出把数据库与内存拖垮。
const maxExportRows = 200000

// timeRangeParams 解析 start_timestamp / end_timestamp（Unix 秒）。
func timeRangeParams(r *http.Request) (*time.Time, *time.Time) {
	q := r.URL.Query()
	var start, end *time.Time
	if ts, ok := parseInt64(q.Get("start_timestamp")); ok {
		t := time.Unix(ts, 0)
		start = &t
	}
	if ts, ok := parseInt64(q.Get("end_timestamp")); ok {
		t := time.Unix(ts, 0)
		end = &t
	}
	return start, end
}

// aggFilterFromQuery 解析费用报表的筛选条件。时间范围默认最近 31 天，
// 并按自然日对齐——按日聚合表的粒度是自然日，非日界的起止会造成口径歧义。
//
// 语义：start/end 为包含的自然日。to 取 end 次日 0 点（排他上界，含 end 当日全天），
// 修正前端 endOf('day')（23:59:59）被 SpendDay 截断到当日 0 点、导致 end 当日整日被
// created_at < to 排除的缺陷。
func aggFilterFromQuery(r *http.Request) store.AggFilter {
	q := r.URL.Query()
	// 默认基线：to = 明日 0 点（含今日），from = to 倒推 31 天。
	to := store.SpendDay(time.Now()).AddDate(0, 0, 1)
	from := to.AddDate(0, 0, -31)
	start, end := timeRangeParams(r)
	if start != nil {
		from = store.SpendDay(*start)
	}
	if end != nil {
		// end 为包含的自然日：次日 0 点作排他上界。
		to = store.SpendDay(*end).AddDate(0, 0, 1)
	}
	f := store.AggFilter{
		From: from, To: to, ModelName: q.Get("model"),
	}
	if id, ok := parseInt64(q.Get("user_id")); ok {
		f.UserID = id
	}
	if raw := q.Get("department_id"); raw != "" {
		if id, ok := parseInt64(raw); ok {
			f.DepartmentID = &id
		}
	}
	if raw := q.Get("project_id"); raw != "" {
		if id, ok := parseInt64(raw); ok {
			f.ProjectID = &id
		}
	}
	if id, ok := parseInt64(q.Get("channel_id")); ok {
		f.ChannelID = id
	}
	if id, ok := parseInt64(q.Get("api_key_id")); ok {
		f.APIKeyID = id
	}
	return f
}

// handleAdminCostReport 按维度出费用报表：用户、部门、模型、渠道、日期。
func (c *reportsAdminController) handleAdminCostReport(w http.ResponseWriter, r *http.Request) {
	dim := store.AggDimension(r.URL.Query().Get("group_by"))
	if !dim.Valid() {
		dim = store.AggByUser
	}
	f := aggFilterFromQuery(r)
	// 托管视角只看本接入方作用域（批次 E 接入）；运营 admin/root 不限。
	if iid := auth.ScopeIntegrationID(r.Context()); iid != nil {
		f.IntegrationID = iid
	}
	rows, err := c.Rollup.Aggregate(r.Context(), dim, f)
	if err != nil {
		obs.Logger(r.Context()).Error("费用报表查询失败", "group_by", dim, "error", err)
		respond.Fail(w, http.StatusInternalServerError, "费用报表查询失败")
		return
	}
	rate := c.Settings.GetInt64(r.Context(), "exchange_rate_credits_per_cny")
	mc := newMoneyCtx(rate)
	// 托管视角剥除采购成本与差额列，口径与 /api/dept 一致（验收 #3）。
	var rowsField any
	if auth.ScopeIntegrationID(r.Context()) != nil {
		rowsField = wrapList(toDeptAggRows(rows), func(r deptAggRow) deptAggRowWithMoney { return wrapDeptAggRow(r, mc) })
	} else {
		rowsField = wrapList(rows, func(r store.AggRow) aggRowWithMoney { return wrapAggRow(r, mc) })
	}
	respond.OK(w, map[string]any{
		"group_by": dim,
		"from":     f.From.Unix(),
		"to":       f.To.Unix(),
		"rows":     rowsField,
	})
}

// departmentBudgetRow 是部门费用与预算的对比行。
type departmentBudgetRow struct {
	DepartmentID   int64          `json:"department_id"`
	DepartmentName string         `json:"department_name"`
	Requests       int64          `json:"requests"`
	CreditsCharged domain.Credits `json:"credits_charged"`
	CreditsCost    domain.Credits `json:"credits_cost"`
	Margin         domain.Credits `json:"margin"`
	// MonthlyBudgetCredits 为 0 表示该部门未设预算，不做超预算判定。
	MonthlyBudgetCredits domain.Credits `json:"monthly_budget_credits"`
	BudgetUsedPercent    int64          `json:"budget_used_percent"`
	OverBudget           bool           `json:"over_budget"`
}

// departmentBudgetRowScoped 是托管视角的部门预算行，相对 departmentBudgetRow 去掉
// 采购成本与差额（口径与 /api/dept、托管 cost-report 一致）。
type departmentBudgetRowScoped struct {
	DepartmentID         int64          `json:"department_id"`
	DepartmentName       string         `json:"department_name"`
	Requests             int64          `json:"requests"`
	CreditsCharged       domain.Credits `json:"credits_charged"`
	MonthlyBudgetCredits domain.Credits `json:"monthly_budget_credits"`
	BudgetUsedPercent    int64          `json:"budget_used_percent"`
	OverBudget           bool           `json:"over_budget"`
}

func toScopedBudgetRows(rows []departmentBudgetRow) []departmentBudgetRowScoped {
	out := make([]departmentBudgetRowScoped, 0, len(rows))
	for _, r := range rows {
		out = append(out, departmentBudgetRowScoped{
			DepartmentID: r.DepartmentID, DepartmentName: r.DepartmentName,
			Requests: r.Requests, CreditsCharged: r.CreditsCharged,
			MonthlyBudgetCredits: r.MonthlyBudgetCredits,
			BudgetUsedPercent:    r.BudgetUsedPercent, OverBudget: r.OverBudget,
		})
	}
	return out
}

// handleAdminDepartmentBudget 出当月的部门费用与预算对比。
// 预算按自然月核算，与财务的月度分摊口径一致。
func (c *reportsAdminController) handleAdminDepartmentBudget(w http.ResponseWriter, r *http.Request) {
	month, ok := monthFromQuery(w, r)
	if !ok {
		return
	}
	from, to := store.MonthRange(month)
	af := store.AggFilter{From: from, To: to}
	// 托管视角只看本接入方作用域（批次 E 接入）。
	managedIID := auth.ScopeIntegrationID(r.Context())
	if managedIID != nil {
		af.IntegrationID = managedIID
	}
	rows, err := c.Rollup.Aggregate(r.Context(), store.AggByDepartment, af)
	if err != nil {
		obs.Logger(r.Context()).Error("部门预算对比查询失败", "error", err)
		respond.Fail(w, http.StatusInternalServerError, "部门预算对比查询失败")
		return
	}
	budgets, err := c.Departments.ListAll(r.Context())
	if err != nil {
		respond.Fail(w, http.StatusInternalServerError, "查询部门失败")
		return
	}
	// 托管视角只保留本接入方部门（消费行已由 AggFilter 限定，部门清单再按归属收窄）。
	if managedIID != nil {
		filtered := budgets[:0]
		for i := range budgets {
			if budgets[i].IntegrationID != nil && *budgets[i].IntegrationID == *managedIID {
				filtered = append(filtered, budgets[i])
			}
		}
		budgets = filtered
	}
	merged := mergeDepartmentBudget(rows, budgets)
	rate := c.Settings.GetInt64(r.Context(), "exchange_rate_credits_per_cny")
	mc := newMoneyCtx(rate)
	var rowsOut any
	if managedIID != nil {
		rowsOut = wrapList(toScopedBudgetRows(merged), func(r departmentBudgetRowScoped) departmentBudgetRowScopedWithMoney {
			return wrapDepartmentBudgetRowScoped(r, mc)
		})
	} else {
		rowsOut = wrapList(merged, func(r departmentBudgetRow) departmentBudgetRowWithMoney {
			return wrapDepartmentBudgetRow(r, mc)
		})
	}
	respond.OK(w, map[string]any{
		"month": from.Format("2006-01"),
		"rows":  rowsOut,
	})
}

// mergeDepartmentBudget 把消费聚合与部门预算合并成对比行。
// 未产生消费的部门也要出现在报表里，否则「预算全部未用」与「部门不存在」无法区分。
func mergeDepartmentBudget(rows []store.AggRow, departments []store.Department) []departmentBudgetRow {
	spent := make(map[int64]store.AggRow, len(rows))
	names := make(map[int64]string, len(rows))
	for _, row := range rows {
		spent[row.GroupID] = row
		names[row.GroupID] = row.GroupKey
	}
	out := make([]departmentBudgetRow, 0, len(departments)+1)
	appendRow := func(id int64, name string, budget domain.Credits) {
		row := spent[id]
		item := departmentBudgetRow{
			DepartmentID: id, DepartmentName: name,
			Requests: row.Requests, CreditsCharged: row.CreditsCharged,
			CreditsCost: row.CreditsCost, Margin: row.Margin,
			MonthlyBudgetCredits: budget,
		}
		if budget > 0 {
			item.BudgetUsedPercent = row.CreditsCharged * 100 / budget
			item.OverBudget = row.CreditsCharged > budget
		}
		delete(spent, id)
		out = append(out, item)
	}
	for i := range departments {
		appendRow(departments[i].ID, departments[i].Name, departments[i].MonthlyBudgetCredits)
	}
	// 剩下的是未分配部门（ID 0）与已删除部门：其消费仍须计入报表合计。
	for id := range spent {
		name := names[id]
		if name == "" {
			name = fmt.Sprintf("已删除部门 #%d", id)
		}
		appendRow(id, name, 0)
	}
	return out
}

// projectBudgetRow 是项目费用与预算的对比行（与 departmentBudgetRow 同构）。
type projectBudgetRow struct {
	ProjectID      int64          `json:"project_id"`
	ProjectName    string         `json:"project_name"`
	Requests       int64          `json:"requests"`
	CreditsCharged domain.Credits `json:"credits_charged"`
	CreditsCost    domain.Credits `json:"credits_cost"`
	Margin         domain.Credits `json:"margin"`
	// MonthlyBudgetCredits 为 0 表示该项目未设预算，不做超预算判定。
	MonthlyBudgetCredits domain.Credits `json:"monthly_budget_credits"`
	BudgetUsedPercent    int64          `json:"budget_used_percent"`
	OverBudget           bool           `json:"over_budget"`
}

// projectBudgetRowScoped 是托管视角的项目预算行，去掉采购成本与差额（同 departmentBudgetRowScoped）。
type projectBudgetRowScoped struct {
	ProjectID            int64          `json:"project_id"`
	ProjectName          string         `json:"project_name"`
	Requests             int64          `json:"requests"`
	CreditsCharged       domain.Credits `json:"credits_charged"`
	MonthlyBudgetCredits domain.Credits `json:"monthly_budget_credits"`
	BudgetUsedPercent    int64          `json:"budget_used_percent"`
	OverBudget           bool           `json:"over_budget"`
}

func toScopedProjectBudgetRows(rows []projectBudgetRow) []projectBudgetRowScoped {
	out := make([]projectBudgetRowScoped, 0, len(rows))
	for _, r := range rows {
		out = append(out, projectBudgetRowScoped{
			ProjectID: r.ProjectID, ProjectName: r.ProjectName,
			Requests: r.Requests, CreditsCharged: r.CreditsCharged,
			MonthlyBudgetCredits: r.MonthlyBudgetCredits,
			BudgetUsedPercent:    r.BudgetUsedPercent, OverBudget: r.OverBudget,
		})
	}
	return out
}

// handleAdminProjectBudget 出当月的项目费用与预算对比（镜像 handleAdminDepartmentBudget）。
// 预算按自然月核算，仅作对比与告警，不拦截调用（同部门范式）。
func (c *reportsAdminController) handleAdminProjectBudget(w http.ResponseWriter, r *http.Request) {
	month, ok := monthFromQuery(w, r)
	if !ok {
		return
	}
	from, to := store.MonthRange(month)
	af := store.AggFilter{From: from, To: to}
	// 托管视角只看本接入方作用域。
	managedIID := auth.ScopeIntegrationID(r.Context())
	if managedIID != nil {
		af.IntegrationID = managedIID
	}
	rows, err := c.Rollup.Aggregate(r.Context(), store.AggByProject, af)
	if err != nil {
		obs.Logger(r.Context()).Error("项目预算对比查询失败", "error", err)
		respond.Fail(w, http.StatusInternalServerError, "项目预算对比查询失败")
		return
	}
	projects, err := c.Projects.ListAll(r.Context())
	if err != nil {
		respond.Fail(w, http.StatusInternalServerError, "查询项目失败")
		return
	}
	// 托管视角只保留本接入方项目。
	if managedIID != nil {
		filtered := projects[:0]
		for i := range projects {
			if projects[i].IntegrationID != nil && *projects[i].IntegrationID == *managedIID {
				filtered = append(filtered, projects[i])
			}
		}
		projects = filtered
	}
	merged := mergeProjectBudget(rows, projects)
	rate := c.Settings.GetInt64(r.Context(), "exchange_rate_credits_per_cny")
	mc := newMoneyCtx(rate)
	var rowsOut any
	if managedIID != nil {
		rowsOut = wrapList(toScopedProjectBudgetRows(merged), func(r projectBudgetRowScoped) projectBudgetRowScopedWithMoney {
			return wrapProjectBudgetRowScoped(r, mc)
		})
	} else {
		rowsOut = wrapList(merged, func(r projectBudgetRow) projectBudgetRowWithMoney {
			return wrapProjectBudgetRow(r, mc)
		})
	}
	respond.OK(w, map[string]any{
		"month": from.Format("2006-01"),
		"rows":  rowsOut,
	})
}

// mergeProjectBudget 把消费聚合与项目预算合并成对比行（同 mergeDepartmentBudget 范式）。
// project_id=0（未归属）与已删除项目的消费仍须计入报表合计。
func mergeProjectBudget(rows []store.AggRow, projects []store.Project) []projectBudgetRow {
	spent := make(map[int64]store.AggRow, len(rows))
	names := make(map[int64]string, len(rows))
	for _, row := range rows {
		spent[row.GroupID] = row
		names[row.GroupID] = row.GroupKey
	}
	out := make([]projectBudgetRow, 0, len(projects)+1)
	appendRow := func(id int64, name string, budget domain.Credits) {
		row := spent[id]
		item := projectBudgetRow{
			ProjectID: id, ProjectName: name,
			Requests: row.Requests, CreditsCharged: row.CreditsCharged,
			CreditsCost: row.CreditsCost, Margin: row.Margin,
			MonthlyBudgetCredits: budget,
		}
		if budget > 0 {
			item.BudgetUsedPercent = row.CreditsCharged * 100 / budget
			item.OverBudget = row.CreditsCharged > budget
		}
		delete(spent, id)
		out = append(out, item)
	}
	for i := range projects {
		appendRow(projects[i].ID, projects[i].Name, projects[i].MonthlyBudgetCredits)
	}
	// 剩下的是未归属（ID 0）与已删除项目。
	for id := range spent {
		name := names[id]
		if name == "" {
			name = fmt.Sprintf("已删除项目 #%d", id)
		}
		appendRow(id, name, 0)
	}
	return out
}

// handleAdminExportUsageLogs 按当前筛选条件导出全部用量日志。
// 逐页拉取并直接写入响应流，不在内存里累积整份结果。
func (c *reportsAdminController) handleAdminExportUsageLogs(w http.ResponseWriter, r *http.Request) {
	f := usageLogFilterFromQuery(r, 0)
	f.Page, f.PageSize = 1, exportPageSize

	// 兑换率与对应明细精度：默认 1e6 → 6 位小数，逐行金额无损可汇总。
	creditsPerUnit := c.Settings.GetInt64(r.Context(), "exchange_rate_credits_per_cny")
	moneyDecimals := pricing.DetailDecimals(creditsPerUnit)

	filename := "usage-logs-" + time.Now().Format("20060102-150405") + ".csv"
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	// UTF-8 BOM：Excel 默认按本地编码打开无 BOM 的 CSV，中文会乱码。
	_, _ = w.Write([]byte{0xEF, 0xBB, 0xBF})

	cw := csv.NewWriter(w)
	defer cw.Flush()
	header := []string{"时间", "请求标识", "用户 ID", "部门 ID", "项目 ID", "密钥 ID", "模型",
		"渠道 ID", "输入 token", "输出 token", "缓存读 token", "缓存写 token",
		"扣费积分", "扣费金额", "成本积分", "成本金额", "状态", "错误分类", "耗时毫秒"}
	if err := cw.Write(header); err != nil {
		obs.Logger(r.Context()).Error("导出用量日志写表头失败", "error", err)
		return
	}

	written := 0
	for {
		logs, _, err := c.UsageLogs.List(r.Context(), f)
		if err != nil {
			obs.Logger(r.Context()).Error("导出用量日志查询失败", "page", f.Page, "error", err)
			return
		}
		if len(logs) == 0 {
			break
		}
		for i := range logs {
			if err := cw.Write(usageLogCSVRow(&logs[i], creditsPerUnit, moneyDecimals)); err != nil {
				obs.Logger(r.Context()).Error("导出用量日志写入失败", "error", err)
				return
			}
			written++
			if written >= maxExportRows {
				obs.Logger(r.Context()).Warn("导出用量日志达到行数上限，结果已截断",
					"limit", maxExportRows)
				return
			}
		}
		if len(logs) < f.PageSize {
			break
		}
		cw.Flush()
		f.Page++
	}
	obs.Logger(r.Context()).Info("导出用量日志完成", "rows", written)
}

func usageLogCSVRow(l *store.UsageLog, creditsPerUnit int64, decimals int) []string {
	return []string{
		l.CreatedAt.Local().Format(time.RFC3339),
		l.RequestID,
		strconv.FormatInt(l.UserID, 10),
		strconv.FormatInt(l.DepartmentID, 10),
		strconv.FormatInt(l.ProjectID, 10),
		strconv.FormatInt(l.APIKeyID, 10),
		l.ModelName,
		strconv.FormatInt(l.ChannelID, 10),
		strconv.FormatInt(l.PromptTokens, 10),
		strconv.FormatInt(l.CompletionTokens, 10),
		strconv.FormatInt(l.CacheReadTokens, 10),
		strconv.FormatInt(l.CacheWriteTokens, 10),
		strconv.FormatInt(l.CreditsCharged, 10),
		pricing.CreditsToDecimalString(l.CreditsCharged, creditsPerUnit, decimals),
		strconv.FormatInt(l.CreditsCost, 10),
		pricing.CreditsToDecimalString(l.CreditsCost, creditsPerUnit, decimals),
		string(l.Status),
		string(l.ErrorClass),
		strconv.FormatInt(l.LatencyMS, 10),
	}
}

// orEmptySlice 保证空结果序列化为 []，而不是 null。
func orEmptySlice[T any](rows []T) []T {
	if rows == nil {
		return []T{}
	}
	return rows
}

// ---- 费用/预算报表的 _money 旁置包装类型 ----
// 嵌入原结构体使原 JSON 字段全部保留，仅新增 _money 裸小数串。
// 运营视角含成本/差额，托管视角（Scoped）剥除这两列，故 Scoped 包装只对扣费与预算旁置。

// aggRowWithMoney 包装 store.AggRow（运营视角费用报表行）。
type aggRowWithMoney struct {
	store.AggRow
	CreditsChargedMoney string `json:"credits_charged_money"`
	CreditsCostMoney    string `json:"credits_cost_money"`
	MarginMoney         string `json:"margin_money"`
}

func wrapAggRow(r store.AggRow, mc moneyCtx) aggRowWithMoney {
	return aggRowWithMoney{
		AggRow:              r,
		CreditsChargedMoney: mc.money(r.CreditsCharged),
		CreditsCostMoney:    mc.money(r.CreditsCost),
		MarginMoney:         mc.money(r.Margin),
	}
}

// departmentBudgetRowWithMoney 包装 departmentBudgetRow（运营视角部门预算对比行）。
type departmentBudgetRowWithMoney struct {
	departmentBudgetRow
	CreditsChargedMoney       string `json:"credits_charged_money"`
	CreditsCostMoney          string `json:"credits_cost_money"`
	MarginMoney               string `json:"margin_money"`
	MonthlyBudgetCreditsMoney string `json:"monthly_budget_credits_money"`
}

func wrapDepartmentBudgetRow(r departmentBudgetRow, mc moneyCtx) departmentBudgetRowWithMoney {
	return departmentBudgetRowWithMoney{
		departmentBudgetRow:       r,
		CreditsChargedMoney:       mc.money(r.CreditsCharged),
		CreditsCostMoney:          mc.money(r.CreditsCost),
		MarginMoney:               mc.money(r.Margin),
		MonthlyBudgetCreditsMoney: mc.money(r.MonthlyBudgetCredits),
	}
}

// departmentBudgetRowScopedWithMoney 包装 departmentBudgetRowScoped（托管视角部门预算对比行）。
type departmentBudgetRowScopedWithMoney struct {
	departmentBudgetRowScoped
	CreditsChargedMoney       string `json:"credits_charged_money"`
	MonthlyBudgetCreditsMoney string `json:"monthly_budget_credits_money"`
}

func wrapDepartmentBudgetRowScoped(r departmentBudgetRowScoped, mc moneyCtx) departmentBudgetRowScopedWithMoney {
	return departmentBudgetRowScopedWithMoney{
		departmentBudgetRowScoped: r,
		CreditsChargedMoney:       mc.money(r.CreditsCharged),
		MonthlyBudgetCreditsMoney: mc.money(r.MonthlyBudgetCredits),
	}
}

// projectBudgetRowWithMoney 包装 projectBudgetRow（运营视角项目预算对比行）。
type projectBudgetRowWithMoney struct {
	projectBudgetRow
	CreditsChargedMoney       string `json:"credits_charged_money"`
	CreditsCostMoney          string `json:"credits_cost_money"`
	MarginMoney               string `json:"margin_money"`
	MonthlyBudgetCreditsMoney string `json:"monthly_budget_credits_money"`
}

func wrapProjectBudgetRow(r projectBudgetRow, mc moneyCtx) projectBudgetRowWithMoney {
	return projectBudgetRowWithMoney{
		projectBudgetRow:          r,
		CreditsChargedMoney:       mc.money(r.CreditsCharged),
		CreditsCostMoney:          mc.money(r.CreditsCost),
		MarginMoney:               mc.money(r.Margin),
		MonthlyBudgetCreditsMoney: mc.money(r.MonthlyBudgetCredits),
	}
}

// projectBudgetRowScopedWithMoney 包装 projectBudgetRowScoped（托管视角项目预算对比行）。
type projectBudgetRowScopedWithMoney struct {
	projectBudgetRowScoped
	CreditsChargedMoney       string `json:"credits_charged_money"`
	MonthlyBudgetCreditsMoney string `json:"monthly_budget_credits_money"`
}

func wrapProjectBudgetRowScoped(r projectBudgetRowScoped, mc moneyCtx) projectBudgetRowScopedWithMoney {
	return projectBudgetRowScopedWithMoney{
		projectBudgetRowScoped:    r,
		CreditsChargedMoney:       mc.money(r.CreditsCharged),
		MonthlyBudgetCreditsMoney: mc.money(r.MonthlyBudgetCredits),
	}
}
