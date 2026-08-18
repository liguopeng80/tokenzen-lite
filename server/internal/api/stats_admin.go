package api

import (
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/liguopeng80/tokenzen-lite/server/internal/api/respond"
	"github.com/liguopeng80/tokenzen-lite/server/internal/auth"
	"github.com/liguopeng80/tokenzen-lite/server/internal/obs"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// 管理端统计/报表 handler。共用的时间范围与筛选助手在 stats_helpers.go。

func (c *reportsAdminController) handleAdminStatsOverview(w http.ResponseWriter, r *http.Request) {
	o, err := c.Stats.Overview(r.Context())
	if err != nil {
		respond.Fail(w, http.StatusInternalServerError, "统计查询失败")
		return
	}
	respond.OK(w, o)
}

func (c *reportsAdminController) handleAdminUsageDaily(w http.ResponseWriter, r *http.Request) {
	stats, err := c.Stats.UsageDaily(r.Context(), 0, daysParam(r, 30, 365))
	if err != nil {
		respond.Fail(w, http.StatusInternalServerError, "统计查询失败")
		return
	}
	rate := c.Settings.GetInt64(r.Context(), "exchange_rate_credits_per_cny")
	mc := newMoneyCtx(rate)
	respond.OK(w, wrapList(stats, func(s store.DailyStat) dailyStatWithMoney { return wrapDailyStat(s, mc) }))
}

// handleAdminCalendar 全站按日用量，供管理端首页年度日历热力图（M1）使用。
//
// 与 handleAdminUsageDaily 的差异：本端点走 RollupRepo.Aggregate（按日维度），原始日志
// 按保留期清理后结果仍保留（retention-safe），支持 365 天回看；后者读原始 usage_logs，
// 不 retention-safe，是趋势图的既有数据源，保留不动（surgical）。
// 托管视角叠加接入方作用域，写法与 handleAdminCostReport 一致。
//
// 时间窗口走 resolveDayRange（与 /me/usage-daily 同源），按包含的自然日对齐：
// to 取明日 0 点作排他上界，避免 SpendDay(time.Now()) 截断把今日整日排除
// （rollup 查询用 created_at < to，与 stats_helpers.resolveDayRange 注释所指同源缺陷）。
func (c *reportsAdminController) handleAdminCalendar(w http.ResponseWriter, r *http.Request) {
	defaultDays := daysParam(r, 90, 365)
	from, to, ok := resolveDayRange(r, defaultDays, 365)
	if !ok {
		respond.Fail(w, http.StatusBadRequest, "时间范围非法：end_timestamp 必须晚于 start_timestamp")
		return
	}
	f := store.AggFilter{From: from, To: to}
	if iid := auth.ScopeIntegrationID(r.Context()); iid != nil {
		f.IntegrationID = iid
	}
	rows, err := c.Rollup.Aggregate(r.Context(), store.AggByDay, f)
	if err != nil {
		obs.Logger(r.Context()).Error("日历热力图查询失败", "from", from, "to", to, "error", err)
		respond.Fail(w, http.StatusInternalServerError, "日历热力图查询失败")
		return
	}
	rate := c.Settings.GetInt64(r.Context(), "exchange_rate_credits_per_cny")
	mc := newMoneyCtx(rate)
	out := make([]dailyStatWithMoney, 0, len(rows))
	for _, row := range rows {
		day, err := store.ParseDayKey(row.GroupKey)
		if err != nil {
			continue
		}
		out = append(out, wrapDailyStat(store.DailyStat{
			Day:            day,
			Requests:       row.Requests,
			CreditsCharged: row.CreditsCharged,
			CreditsCost:    row.CreditsCost,
			TotalTokens:    row.PromptTokens + row.CompletionTokens,
		}, mc))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Day.Before(out[j].Day) })
	respond.OK(w, out)
}

func (c *reportsAdminController) handleAdminProfit(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	groupBy := q.Get("group_by")
	if groupBy != "channel" && groupBy != "model" {
		groupBy = "channel"
	}
	// 文档契约（docs/api-contract.md）与前端 wrapper 均用 from/to（Unix 秒），
	// 不走 start_timestamp/end_timestamp，也不按自然日对齐：原实现直接把原始
	// Unix 时间戳透传给 Stats.Profit，这里保持该语义，避免破坏既有调用方。
	to := time.Now()
	from := to.AddDate(0, 0, -30)
	if ts, err := strconv.ParseInt(q.Get("from"), 10, 64); err == nil && ts > 0 {
		from = time.Unix(ts, 0)
	}
	if ts, err := strconv.ParseInt(q.Get("to"), 10, 64); err == nil && ts > 0 {
		to = time.Unix(ts, 0)
	}
	rows, err := c.Stats.Profit(r.Context(), groupBy, from, to)
	if err != nil {
		respond.Fail(w, http.StatusInternalServerError, "利润分析查询失败")
		return
	}
	respond.OK(w, rows)
}

func (c *reportsAdminController) handleAdminListUsageLogs(w http.ResponseWriter, r *http.Request) {
	f := usageLogFilterFromQuery(r, 0)
	logs, total, err := c.UsageLogs.ListWithNames(r.Context(), f)
	if err != nil {
		respond.Fail(w, http.StatusInternalServerError, "查询用量日志失败")
		return
	}
	rate := c.Settings.GetInt64(r.Context(), "exchange_rate_credits_per_cny")
	mc := newMoneyCtx(rate)
	wrapped := wrapList(logs, func(l store.UsageLogRow) usageLogRowWithMoney { return wrapUsageLogRow(l, mc) })
	respond.OK(w, respond.NewPage(f.Page, f.PageSize, total, wrapped))
}

// handleAdminGetUsageLog 按 request_id 直达单条日志（含计费明细快照）。
func (c *reportsAdminController) handleAdminGetUsageLog(w http.ResponseWriter, r *http.Request) {
	requestID := r.URL.Query().Get("request_id")
	if requestID == "" {
		respond.Fail(w, http.StatusBadRequest, "缺少 request_id 参数")
		return
	}
	l, err := c.UsageLogs.GetByRequestID(r.Context(), requestID)
	if err != nil {
		respond.Fail(w, http.StatusNotFound, "日志不存在")
		return
	}
	// 托管视角访问他接入方的日志按「不存在」处理，与跨作用域对象访问的既有口径一致，
	// 避免借 request_id 探测归属。
	if iid := auth.ScopeIntegrationID(r.Context()); iid != nil && l.IntegrationID != *iid {
		respond.Fail(w, http.StatusNotFound, "日志不存在")
		return
	}
	rate := c.Settings.GetInt64(r.Context(), "exchange_rate_credits_per_cny")
	mc := newMoneyCtx(rate)
	respond.OK(w, wrapUsageLog(*l, mc))
}

// handleAdminHeatmap 全站（或按 user_id 收窄）的周×时活跃时段热力图。
// 托管视角叠加接入方作用域，与费用报表口径一致。
func (c *reportsAdminController) handleAdminHeatmap(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	// 时间范围默认最近 30 天，最大 90 天，按包含的自然日对齐。
	from, to, ok := resolveDayRange(r, 30, 90)
	if !ok {
		respond.Fail(w, http.StatusBadRequest, "时间范围非法：end_timestamp 必须晚于 start_timestamp")
		return
	}

	var (
		userID       int64
		model        string
		channelID    int64
		departmentID int64
	)
	if id, ok := parseInt64(q.Get("user_id")); ok {
		userID = id
	}
	model = q.Get("model")
	if id, ok := parseInt64(q.Get("channel_id")); ok {
		channelID = id
	}
	if id, ok := parseInt64(q.Get("department_id")); ok {
		departmentID = id
	}
	// 托管视角叠加接入方作用域，与费用报表口径一致。
	cells, err := c.Stats.Heatmap(r.Context(), userID, from, to, model, channelID, departmentID,
		auth.ScopeIntegrationID(r.Context()))
	if err != nil {
		obs.Logger(r.Context()).Error("活跃时段查询失败",
			"from", from, "to", to, "model", model, "error", err)
		respond.Fail(w, http.StatusInternalServerError, "活跃时段查询失败")
		return
	}
	rate := c.Settings.GetInt64(r.Context(), "exchange_rate_credits_per_cny")
	mc := newMoneyCtx(rate)
	respond.OK(w, adminHeatmapResponse{
		From:  from.Unix(),
		To:    to.Unix(),
		Cells: orEmptyHeatmapCells(cells, mc),
	})
}

// adminHeatmapResponse 是管理端热力图响应体：Cells 旁置扣费金额串。
// 与 /api/me/heatmap 共用的 heatmapResponse 分道，因后者不暴露金额（用户自助视角口径）。
type adminHeatmapResponse struct {
	From  int64                  `json:"from"`
	To    int64                  `json:"to"`
	Cells []heatmapCellWithMoney `json:"cells"`
}

// orEmptyHeatmapCells 把 store.HeatmapCell 映射为带 _money 旁置的 heatmapCellWithMoney，
// nil 时返回长度为 0 的非 nil 切片（保证 JSON 序列化为 [] 而非 null）。
func orEmptyHeatmapCells(cells []store.HeatmapCell, mc moneyCtx) []heatmapCellWithMoney {
	if cells == nil {
		return []heatmapCellWithMoney{}
	}
	return wrapList(cells, func(c store.HeatmapCell) heatmapCellWithMoney { return wrapHeatmapCell(c, mc) })
}

// healthTimelineResponse GET /api/admin/stats/health-timeline 的响应体。
// points 为空时序列化为 []，而非 null（由 orEmptySlice 兜底）。
type healthTimelineResponse struct {
	From   int64               `json:"from"`   // Unix 秒
	To     int64               `json:"to"`     // Unix 秒
	Bucket string              `json:"bucket"` // "hour" | "day"
	Points []store.HealthPoint `json:"points"`
}

// handleAdminHealthTimeline 管理端运维分析：按小时/日分桶的延迟分位与失败率时间线。
//
// 数据走原始 usage_logs（按日汇总表不含 latency_ms 维度），适合 OPS 近期窗口
// （典型 24–72h）视图。默认窗口 24h，最大 30 天；bucket 未显式指定时按窗口长度
// 自动选择（≤7 天按小时，超过按日）。托管视角叠加接入方作用域，与费用报表口径一致。
func (c *reportsAdminController) handleAdminHealthTimeline(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	to := time.Now()
	from := to.Add(-24 * time.Hour)
	if start, end := timeRangeParams(r); start != nil || end != nil {
		if start != nil {
			from = *start
		}
		if end != nil {
			to = *end
		}
	}
	const maxWindow = 30 * 24 * time.Hour
	if to.Sub(from) > maxWindow {
		from = to.Add(-maxWindow)
	}
	if !to.After(from) {
		respond.Fail(w, http.StatusBadRequest, "时间范围非法：end_timestamp 必须晚于 start_timestamp")
		return
	}

	bucket := q.Get("bucket")
	if bucket != "hour" && bucket != "day" {
		bucket = ""
	}
	if bucket == "" {
		if to.Sub(from) > 7*24*time.Hour {
			bucket = "day"
		} else {
			bucket = "hour"
		}
	}

	model := q.Get("model")
	var channelID int64
	if id, ok := parseInt64(q.Get("channel_id")); ok {
		channelID = id
	}

	points, err := c.Stats.HealthTimeline(r.Context(), from, to, bucket, model, channelID,
		auth.ScopeIntegrationID(r.Context()))
	if err != nil {
		obs.Logger(r.Context()).Error("健康度时间线查询失败",
			"from", from, "to", to, "model", model, "error", err)
		respond.Fail(w, http.StatusInternalServerError, "健康度时间线查询失败")
		return
	}
	respond.OK(w, healthTimelineResponse{
		From:   from.Unix(),
		To:     to.Unix(),
		Bucket: bucket,
		Points: orEmptySlice(points),
	})
}

// handleAdminOpsSummary 管理端经营分析：本月与上月对比、本月模型/用户 Top N。
// 默认当前自然月，可用 ?month=YYYY-MM 指定。托管视角叠加接入方作用域，
// 与费用报表口径一致。
func (c *reportsAdminController) handleAdminOpsSummary(w http.ResponseWriter, r *http.Request) {
	month, ok := monthFromQuery(w, r)
	if !ok {
		return
	}
	summary, err := c.Rollup.OpsSummary(r.Context(), month, auth.ScopeIntegrationID(r.Context()))
	if err != nil {
		obs.Logger(r.Context()).Error("经营分析汇总查询失败", "month", month.Format("2006-01"), "error", err)
		respond.Fail(w, http.StatusInternalServerError, "经营分析汇总查询失败")
		return
	}
	if summary.TopModels == nil {
		summary.TopModels = []store.OpsRankRow{}
	}
	if summary.TopUsers == nil {
		summary.TopUsers = []store.OpsRankRow{}
	}
	rate := c.Settings.GetInt64(r.Context(), "exchange_rate_credits_per_cny")
	mc := newMoneyCtx(rate)
	respond.OK(w, wrapOpsSummary(*summary, mc))
}

// costByCallTypeResponse GET /api/admin/stats/cost-by-calltype 的响应体。
// rows 为空时序列化为 []，而非 null（wrapList 对 nil 入参产长度 0 的非 nil 切片）。
type costByCallTypeResponse struct {
	From int64                  `json:"from"` // Unix 秒
	To   int64                  `json:"to"`   // Unix 秒
	Rows []callTypeRowWithMoney `json:"rows"`
}

// handleAdminCostByCallType 管理端运维分析：按派生调用类型（向量嵌入/图像/流式对话/
// 非流式对话/其他）聚合扣费分布。数据走原始 usage_logs（按日汇总表不含 is_stream 与
// modality 维度），适合近期窗口（典型 30 天）视图。默认窗口 30 天，最大 90 天；
// 托管视角叠加接入方作用域，与费用报表口径一致。
func (c *reportsAdminController) handleAdminCostByCallType(w http.ResponseWriter, r *http.Request) {
	// 默认最近 30 天，最大 90 天，按包含的自然日对齐。
	from, to, ok := resolveDayRange(r, 30, 90)
	if !ok {
		respond.Fail(w, http.StatusBadRequest, "时间范围非法：end_timestamp 必须晚于 start_timestamp")
		return
	}

	rows, err := c.Stats.CostByCallType(r.Context(), from, to, auth.ScopeIntegrationID(r.Context()))
	if err != nil {
		obs.Logger(r.Context()).Error("调用类型分布查询失败", "from", from, "to", to, "error", err)
		respond.Fail(w, http.StatusInternalServerError, "调用类型分布查询失败")
		return
	}
	rate := c.Settings.GetInt64(r.Context(), "exchange_rate_credits_per_cny")
	mc := newMoneyCtx(rate)
	respond.OK(w, costByCallTypeResponse{
		From: from.Unix(),
		To:   to.Unix(),
		Rows: wrapList(rows, func(r store.CallTypeRow) callTypeRowWithMoney { return wrapCallTypeRow(r, mc) }),
	})
}

// adminCacheReportGroupWithMoney 管理端缓存分析的分组行：在 me 版口径之上
// 叠加网关采购成本（credits_cost）与其货币串。Overall 不含成本，仍复用 cacheReportOverall。
// 在 admin 文件独立定义，避免向 /me 的类型泄漏成本字段。
type adminCacheReportGroupWithMoney struct {
	GroupKey            string  `json:"group_key"`
	Requests            int64   `json:"requests"`
	PromptTokens        int64   `json:"prompt_tokens"`
	CacheReadTokens     int64   `json:"cache_read_tokens"`
	CacheWriteTokens    int64   `json:"cache_write_tokens"`
	CacheHitRate        float64 `json:"cache_hit_rate"`
	CreditsCharged      int64   `json:"credits_charged"`
	CreditsChargedMoney string  `json:"credits_charged_money"`
	CreditsCost         int64   `json:"credits_cost"`
	CreditsCostMoney    string  `json:"credits_cost_money"`
}

// adminCacheReportResponse GET /api/admin/stats/cache-report 的响应体。
// Overall 复用 me 版 cacheReportOverall（命中率口径与 /me/cache-report 一致，不含成本）；
// Groups 暴露 credits_cost 供运营视角的毛利对比。
type adminCacheReportResponse struct {
	From    int64                            `json:"from"`
	To      int64                            `json:"to"`
	Overall cacheReportOverall               `json:"overall"`
	Groups  []adminCacheReportGroupWithMoney `json:"groups"`
}

// handleAdminCacheReport 管理端缓存分析报表，镜像 handleMeCacheReport。
// 差异：去掉 UserID 限定，改用 aggFilterFromQuery（含 from/to/user_id/dept/project/channel/key）
// 叠加 auth.ScopeIntegrationID（与 handleAdminCostReport 写法一致）；维度增 channel；
// 分组行额外暴露 credits_cost 供运营视角。命中率口径与 /me/cache-report 完全一致，
// 因此单用户场景下 admin overall 等于该用户的 me overall。
func (c *reportsAdminController) handleAdminCacheReport(w http.ResponseWriter, r *http.Request) {
	groupBy := r.URL.Query().Get("group_by")
	dim := store.AggByDay
	switch groupBy {
	case "model":
		dim = store.AggByModel
	case "project":
		dim = store.AggByProject
	case "channel":
		dim = store.AggByChannel
	}
	f := aggFilterFromQuery(r)
	// 托管视角只看本接入方作用域（写法与 handleAdminCostReport 一致）。
	if iid := auth.ScopeIntegrationID(r.Context()); iid != nil {
		f.IntegrationID = iid
	}
	rows, err := c.Rollup.Aggregate(r.Context(), dim, f)
	if err != nil {
		obs.Logger(r.Context()).Error("缓存分析查询失败",
			"from", f.From, "to", f.To, "group_by", dim, "error", err)
		respond.Fail(w, http.StatusInternalServerError, "缓存分析查询失败")
		return
	}
	rate := c.Settings.GetInt64(r.Context(), "exchange_rate_credits_per_cny")
	mc := newMoneyCtx(rate)
	groups := make([]adminCacheReportGroupWithMoney, 0, len(rows))
	var overall cacheReportOverall
	for _, row := range rows {
		groups = append(groups, adminCacheReportGroupWithMoney{
			GroupKey:            row.GroupKey,
			Requests:            row.Requests,
			PromptTokens:        row.PromptTokens,
			CacheReadTokens:     row.CacheReadTokens,
			CacheWriteTokens:    row.CacheWriteTokens,
			CacheHitRate:        cacheHitRate(row.CacheReadTokens, row.PromptTokens),
			CreditsCharged:      int64(row.CreditsCharged),
			CreditsChargedMoney: mc.money(row.CreditsCharged),
			CreditsCost:         int64(row.CreditsCost),
			CreditsCostMoney:    mc.money(row.CreditsCost),
		})
		overall.Requests += row.Requests
		overall.PromptTokens += row.PromptTokens
		overall.CacheReadTokens += row.CacheReadTokens
		overall.CacheWriteTokens += row.CacheWriteTokens
	}
	overall.CacheHitRate = cacheHitRate(overall.CacheReadTokens, overall.PromptTokens)
	// 按日维度按时间升序输出（与 me 一致），便于前端按时间轴作图。
	if dim == store.AggByDay {
		sort.Slice(groups, func(i, j int) bool { return groups[i].GroupKey < groups[j].GroupKey })
	}
	respond.OK(w, adminCacheReportResponse{
		From:    f.From.Unix(),
		To:      f.To.Unix(),
		Overall: overall,
		Groups:  groups,
	})
}

// handleAdminRuntime 返回进程级运行指标的结构化 JSON 快照，供管理端运维大屏消费。
//
// 与 /metrics 的差异：/metrics 输出 Prometheus 文本且鉴权为 root+token，浏览器消费不便；
// 本端点走 admin 桶鉴权（managed 及以上），直接给出 gauge 取值与直方图分位估算，
// 前端无需实现 Prom 文本解析与分位插值。快照语义与 Export() 一致（进程内存态，
// 重启归零），锁处理亦镜像 Export()——不在持锁时调用 gauge 取值函数。
func (c *reportsAdminController) handleAdminRuntime(w http.ResponseWriter, r *http.Request) {
	respond.OK(w, obs.DefaultMetrics().Snapshot())
}

// handleAdminModelChannelCosts 跨渠道比价：某模型在各渠道的成本清单。
func (c *reportsAdminController) handleAdminModelChannelCosts(w http.ResponseWriter, r *http.Request) {
	id, ok := IDParam(w, r, "id")
	if !ok {
		return
	}
	m, err := c.Models.GetByID(r.Context(), id)
	if err != nil {
		respond.Fail(w, http.StatusNotFound, "模型不存在")
		return
	}
	costs, err := c.Costs.ListByModel(r.Context(), m.Name)
	if err != nil {
		respond.Fail(w, http.StatusInternalServerError, "查询成本失败")
		return
	}
	respond.OK(w, costs)
}

// ---- 管理端统计/报表的 _money 旁置包装类型 ----
// 这些类型仅 admin 报表路径使用（/me 与 /dept 的对应包装分别归属各自处理器文件）。
// 嵌入原结构体使原 JSON 字段全部保留，仅新增 _money 裸小数串。

// usageLogWithMoney 包装 store.UsageLog，旁置预扣/扣费/成本的货币串。
type usageLogWithMoney struct {
	store.UsageLog
	CreditsPrechargedMoney string `json:"credits_precharged_money"`
	CreditsChargedMoney    string `json:"credits_charged_money"`
	CreditsCostMoney       string `json:"credits_cost_money"`
}

func wrapUsageLog(l store.UsageLog, mc moneyCtx) usageLogWithMoney {
	return usageLogWithMoney{
		UsageLog:               l,
		CreditsPrechargedMoney: mc.money(l.CreditsPrecharged),
		CreditsChargedMoney:    mc.money(l.CreditsCharged),
		CreditsCostMoney:       mc.money(l.CreditsCost),
	}
}

// usageLogRowWithMoney 包装 store.UsageLogRow（嵌入 UsageLog），旁置预扣/扣费/成本的货币串。
type usageLogRowWithMoney struct {
	store.UsageLogRow
	CreditsPrechargedMoney string `json:"credits_precharged_money"`
	CreditsChargedMoney    string `json:"credits_charged_money"`
	CreditsCostMoney       string `json:"credits_cost_money"`
}

func wrapUsageLogRow(l store.UsageLogRow, mc moneyCtx) usageLogRowWithMoney {
	return usageLogRowWithMoney{
		UsageLogRow:            l,
		CreditsPrechargedMoney: mc.money(l.CreditsPrecharged),
		CreditsChargedMoney:    mc.money(l.CreditsCharged),
		CreditsCostMoney:       mc.money(l.CreditsCost),
	}
}

// opsMonthTotalsWithMoney 包装 store.OpsMonthTotals，旁置扣费/成本/差额/充值的货币串。
type opsMonthTotalsWithMoney struct {
	store.OpsMonthTotals
	CreditsChargedMoney string `json:"credits_charged_money"`
	CreditsCostMoney    string `json:"credits_cost_money"`
	MarginMoney         string `json:"margin_money"`
	TopupCreditsMoney   string `json:"topup_credits_money"`
}

func wrapOpsMonthTotals(t store.OpsMonthTotals, mc moneyCtx) opsMonthTotalsWithMoney {
	return opsMonthTotalsWithMoney{
		OpsMonthTotals:      t,
		CreditsChargedMoney: mc.money(t.CreditsCharged),
		CreditsCostMoney:    mc.money(t.CreditsCost),
		MarginMoney:         mc.money(t.Margin),
		TopupCreditsMoney:   mc.money(t.TopupCredits),
	}
}

// opsRankRowWithMoney 包装 store.OpsRankRow，旁置扣费/成本的货币串。
type opsRankRowWithMoney struct {
	store.OpsRankRow
	CreditsChargedMoney string `json:"credits_charged_money"`
	CreditsCostMoney    string `json:"credits_cost_money"`
}

func wrapOpsRankRow(r store.OpsRankRow, mc moneyCtx) opsRankRowWithMoney {
	return opsRankRowWithMoney{
		OpsRankRow:          r,
		CreditsChargedMoney: mc.money(r.CreditsCharged),
		CreditsCostMoney:    mc.money(r.CreditsCost),
	}
}

// opsSummaryWithMoney 包装 store.OpsSummary。嵌套的 this_month/prev_month 与
// top_models/top_users 同步旁置货币串：内层字段经外层同名（同 JSON tag）字段覆盖，
// 嵌入 OpsSummary 提升的对应字段被遮蔽，不发生重复序列化。
type opsSummaryWithMoney struct {
	store.OpsSummary
	ThisMonth opsMonthTotalsWithMoney `json:"this_month"`
	PrevMonth opsMonthTotalsWithMoney `json:"prev_month"`
	TopModels []opsRankRowWithMoney   `json:"top_models"`
	TopUsers  []opsRankRowWithMoney   `json:"top_users"`
}

func wrapOpsSummary(s store.OpsSummary, mc moneyCtx) opsSummaryWithMoney {
	return opsSummaryWithMoney{
		OpsSummary: s,
		ThisMonth:  wrapOpsMonthTotals(s.ThisMonth, mc),
		PrevMonth:  wrapOpsMonthTotals(s.PrevMonth, mc),
		TopModels:  wrapList(s.TopModels, func(r store.OpsRankRow) opsRankRowWithMoney { return wrapOpsRankRow(r, mc) }),
		TopUsers:   wrapList(s.TopUsers, func(r store.OpsRankRow) opsRankRowWithMoney { return wrapOpsRankRow(r, mc) }),
	}
}

// callTypeRowWithMoney 包装 store.CallTypeRow，旁置扣费/成本/差额的货币串。
type callTypeRowWithMoney struct {
	store.CallTypeRow
	CreditsChargedMoney string `json:"credits_charged_money"`
	CreditsCostMoney    string `json:"credits_cost_money"`
	MarginMoney         string `json:"margin_money"`
}

func wrapCallTypeRow(r store.CallTypeRow, mc moneyCtx) callTypeRowWithMoney {
	return callTypeRowWithMoney{
		CallTypeRow:         r,
		CreditsChargedMoney: mc.money(r.CreditsCharged),
		CreditsCostMoney:    mc.money(r.CreditsCost),
		MarginMoney:         mc.money(r.Margin),
	}
}
