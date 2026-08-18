package api

import (
	"net/http"
	"sort"

	"github.com/liguopeng80/tokenzen-lite/server/internal/api/respond"
	"github.com/liguopeng80/tokenzen-lite/server/internal/auth"
	"github.com/liguopeng80/tokenzen-lite/server/internal/obs"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// /me 侧统计/报表 handler。共用的时间范围与筛选助手在 stats_helpers.go。
// 用户侧一律不暴露网关采购成本（credits_cost）与差额，只返回本人请求的次数、扣费与 token。

func (c *meStatsController) handleMeListUsageLogs(w http.ResponseWriter, r *http.Request) {
	u := auth.CurrentUser(r.Context())
	f := usageLogFilterFromQuery(r, u.ID)
	logs, total, err := c.UsageLogs.List(r.Context(), f)
	if err != nil {
		respond.Fail(w, http.StatusInternalServerError, "查询用量日志失败")
		return
	}
	rate := c.Settings.GetInt64(r.Context(), "exchange_rate_credits_per_cny")
	mc := newMoneyCtx(rate)
	// 投影为用户侧可见行，剥离渠道/成本/差额/价格快照等运营字段。
	respond.OK(w, respond.NewPage(f.Page, f.PageSize, total,
		wrapList(toMeUsageLogRows(logs), func(r meUsageLogRow) meUsageLogRowWithMoney {
			return wrapMeUsageLogRow(r, mc)
		})))
}

// summaryRowWithMoney 包装 store.SummaryRow，旁置扣费积分的货币串。
type summaryRowWithMoney struct {
	store.SummaryRow
	CreditsChargedMoney string `json:"credits_charged_money"`
}

// wrapSummaryRow 把汇总行的扣费积分换算为货币串。
func wrapSummaryRow(s store.SummaryRow, mc moneyCtx) summaryRowWithMoney {
	return summaryRowWithMoney{
		SummaryRow:          s,
		CreditsChargedMoney: mc.money(s.CreditsCharged),
	}
}

// handleMeUsageSummary 当前用户按 day/model/key 维度的用量汇总。
// 走 RollupRepo.Aggregate：已完成汇总的日期读聚合表、未完成读原始日志并合并，
// 因此在原始日志被按保留期清理后结果仍保留（retention-safe）。
// 用户侧不暴露网关采购成本与差额，只返回请求次数、扣费积分与 token 合计。
func (c *meStatsController) handleMeUsageSummary(w http.ResponseWriter, r *http.Request) {
	u := auth.CurrentUser(r.Context())
	groupBy := r.URL.Query().Get("group_by")
	if groupBy != "model" && groupBy != "key" && groupBy != "project" {
		groupBy = "day"
	}
	dim := store.AggByDay
	switch groupBy {
	case "model":
		dim = store.AggByModel
	case "key":
		dim = store.AggByKey
	case "project":
		dim = store.AggByProject
	}
	from, to := meUsageRange(r)
	rows, err := c.Rollup.Aggregate(r.Context(), dim, store.AggFilter{
		UserID: u.ID, From: from, To: to,
	})
	if err != nil {
		respond.Fail(w, http.StatusInternalServerError, "汇总查询失败")
		return
	}
	rate := c.Settings.GetInt64(r.Context(), "exchange_rate_credits_per_cny")
	mc := newMoneyCtx(rate)
	out := make([]summaryRowWithMoney, 0, len(rows))
	for _, row := range rows {
		out = append(out, wrapSummaryRow(store.SummaryRow{
			GroupKey:       row.GroupKey,
			Requests:       row.Requests,
			CreditsCharged: row.CreditsCharged,
			TotalTokens:    row.PromptTokens + row.CompletionTokens,
		}, mc))
	}
	// 按日维度按时间升序输出，便于前端按时间轴作图；其余维度保留扣费额降序。
	if dim == store.AggByDay {
		sort.Slice(out, func(i, j int) bool { return out[i].GroupKey < out[j].GroupKey })
	}
	respond.OK(w, out)
}

// handleMeUsageDaily 当前用户的按日用量。同走 RollupRepo.Aggregate（按日维度），
// 用户侧不暴露 CreditsCost。按日升序输出。
func (c *meStatsController) handleMeUsageDaily(w http.ResponseWriter, r *http.Request) {
	u := auth.CurrentUser(r.Context())
	from, to := meUsageRange(r)
	rows, err := c.Rollup.Aggregate(r.Context(), store.AggByDay, store.AggFilter{
		UserID: u.ID, From: from, To: to,
	})
	if err != nil {
		respond.Fail(w, http.StatusInternalServerError, "统计查询失败")
		return
	}
	rate := c.Settings.GetInt64(r.Context(), "exchange_rate_credits_per_cny")
	mc := newMoneyCtx(rate)
	out := make([]dailyStatWithMoney, 0, len(rows))
	for _, row := range rows {
		// Aggregate 按日维度的 group_key 为 dayKeyLayout（"YYYY-MM-DD"，服务器时区）。
		day, err := store.ParseDayKey(row.GroupKey)
		if err != nil {
			continue
		}
		out = append(out, wrapDailyStat(store.DailyStat{
			Day:            day,
			Requests:       row.Requests,
			CreditsCharged: row.CreditsCharged,
			CreditsCost:    0, // 用户侧不暴露网关采购成本
			TotalTokens:    row.PromptTokens + row.CompletionTokens,
		}, mc))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Day.Before(out[j].Day) })
	respond.OK(w, out)
}

// cacheReportGroup 用户侧缓存分析的分组行。
type cacheReportGroup struct {
	GroupKey         string  `json:"group_key"`
	Requests         int64   `json:"requests"`
	PromptTokens     int64   `json:"prompt_tokens"`
	CacheReadTokens  int64   `json:"cache_read_tokens"`
	CacheWriteTokens int64   `json:"cache_write_tokens"`
	CacheHitRate     float64 `json:"cache_hit_rate"`
	CreditsCharged   int64   `json:"credits_charged"`
}

// cacheReportGroupWithMoney 包装 cacheReportGroup，旁置扣费积分的货币串。
type cacheReportGroupWithMoney struct {
	cacheReportGroup
	CreditsChargedMoney string `json:"credits_charged_money"`
}

// wrapCacheReportGroup 把缓存分析分组行的扣费积分换算为货币串。
func wrapCacheReportGroup(g cacheReportGroup, mc moneyCtx) cacheReportGroupWithMoney {
	return cacheReportGroupWithMoney{
		cacheReportGroup:    g,
		CreditsChargedMoney: mc.money(g.CreditsCharged),
	}
}

// cacheReportOverall 用户侧缓存分析的整体汇总。
type cacheReportOverall struct {
	Requests         int64   `json:"requests"`
	PromptTokens     int64   `json:"prompt_tokens"`
	CacheReadTokens  int64   `json:"cache_read_tokens"`
	CacheWriteTokens int64   `json:"cache_write_tokens"`
	CacheHitRate     float64 `json:"cache_hit_rate"`
}

// cacheReportResponse GET /api/me/cache-report 的响应体。
// 用户侧不暴露网关采购成本与差额，只返回本人请求的缓存命中情况与积分消费。
type cacheReportResponse struct {
	From    int64                       `json:"from"`
	To      int64                       `json:"to"`
	Overall cacheReportOverall          `json:"overall"`
	Groups  []cacheReportGroupWithMoney `json:"groups"`
}

// cacheHitRate 缓存命中率 = cache_read_tokens / max(1, cache_read_tokens + prompt_tokens)。
// 口径含义：输入侧 token 中由缓存直接命中的占比（prompt_tokens 在上游计费口径里
// 不含 cache_read，二者相加构成输入侧总量）。窗口内无输入时返回 0。
func cacheHitRate(cacheRead, prompt int64) float64 {
	denom := cacheRead + prompt
	if denom <= 0 {
		return 0
	}
	return float64(cacheRead) / float64(denom)
}

// handleMeCacheReport 当前用户的缓存分析报表。走 RollupRepo.Aggregate（按日或按模型），
// 与 usage-summary 同一条保留期安全的聚合路径：已完成汇总的日期读聚合表、未完成读原始日志，
// 原始日志按保留期清理后结果仍保留。返回整体命中率/缓存 token 量、按维度的分组明细。
// 用户侧不暴露网关采购成本与差额。
func (c *meStatsController) handleMeCacheReport(w http.ResponseWriter, r *http.Request) {
	u := auth.CurrentUser(r.Context())
	groupBy := r.URL.Query().Get("group_by")
	dim := store.AggByDay
	if groupBy == "model" {
		dim = store.AggByModel
	}
	if groupBy == "project" {
		dim = store.AggByProject
	}
	from, to := meUsageRange(r)
	rows, err := c.Rollup.Aggregate(r.Context(), dim, store.AggFilter{
		UserID: u.ID, From: from, To: to,
	})
	if err != nil {
		obs.Logger(r.Context()).Error("缓存分析查询失败",
			"from", from, "to", to, "user_id", u.ID, "error", err)
		respond.Fail(w, http.StatusInternalServerError, "缓存分析查询失败")
		return
	}
	rate := c.Settings.GetInt64(r.Context(), "exchange_rate_credits_per_cny")
	mc := newMoneyCtx(rate)
	groups := make([]cacheReportGroup, 0, len(rows))
	var overall cacheReportOverall
	for _, row := range rows {
		groups = append(groups, cacheReportGroup{
			GroupKey:         row.GroupKey,
			Requests:         row.Requests,
			PromptTokens:     row.PromptTokens,
			CacheReadTokens:  row.CacheReadTokens,
			CacheWriteTokens: row.CacheWriteTokens,
			CacheHitRate:     cacheHitRate(row.CacheReadTokens, row.PromptTokens),
			CreditsCharged:   int64(row.CreditsCharged),
		})
		overall.Requests += row.Requests
		overall.PromptTokens += row.PromptTokens
		overall.CacheReadTokens += row.CacheReadTokens
		overall.CacheWriteTokens += row.CacheWriteTokens
	}
	overall.CacheHitRate = cacheHitRate(overall.CacheReadTokens, overall.PromptTokens)
	// 按日维度按时间升序输出，便于前端按时间轴作图；其余维度保留扣费额降序。
	if dim == store.AggByDay {
		sort.Slice(groups, func(i, j int) bool { return groups[i].GroupKey < groups[j].GroupKey })
	}
	respond.OK(w, cacheReportResponse{
		From:    from.Unix(),
		To:      to.Unix(),
		Overall: overall,
		Groups: wrapList(groups, func(g cacheReportGroup) cacheReportGroupWithMoney {
			return wrapCacheReportGroup(g, mc)
		}),
	})
}

// tokenReportOverall 用户侧 token 结构的整体合计。
// 四类 billed token 的占比构成消费的结构画像：输入（不含缓存）/ 缓存命中读 / 缓存写入 / 输出。
type tokenReportOverall struct {
	PromptTokens     int64 `json:"prompt_tokens"`      // 输入（不含缓存读）
	CacheReadTokens  int64 `json:"cache_read_tokens"`  // 缓存命中读
	CacheWriteTokens int64 `json:"cache_write_tokens"` // 缓存写入
	CompletionTokens int64 `json:"completion_tokens"`  // 输出
	TotalTokens      int64 `json:"total_tokens"`       // 四类合计
}

// tokenReportGroup 用户侧 token 结构的分组行。
type tokenReportGroup struct {
	GroupKey         string `json:"group_key"`
	Requests         int64  `json:"requests"`
	PromptTokens     int64  `json:"prompt_tokens"`
	CacheReadTokens  int64  `json:"cache_read_tokens"`
	CacheWriteTokens int64  `json:"cache_write_tokens"`
	CompletionTokens int64  `json:"completion_tokens"`
	TotalTokens      int64  `json:"total_tokens"`
	CreditsCharged   int64  `json:"credits_charged"`
}

// tokenReportGroupWithMoney 包装 tokenReportGroup，旁置扣费积分的货币串。
type tokenReportGroupWithMoney struct {
	tokenReportGroup
	CreditsChargedMoney string `json:"credits_charged_money"`
}

// wrapTokenReportGroup 把 token 结构分组行的扣费积分换算为货币串。
func wrapTokenReportGroup(g tokenReportGroup, mc moneyCtx) tokenReportGroupWithMoney {
	return tokenReportGroupWithMoney{
		tokenReportGroup:    g,
		CreditsChargedMoney: mc.money(g.CreditsCharged),
	}
}

// tokenReportResponse GET /api/me/token-report 的响应体。
// 用户侧不暴露网关采购成本与差额，只返回本人 token 结构与积分消费。
type tokenReportResponse struct {
	From    int64                       `json:"from"`
	To      int64                       `json:"to"`
	Overall tokenReportOverall          `json:"overall"`
	Groups  []tokenReportGroupWithMoney `json:"groups"`
}

// handleMeTokenReport 当前用户的 token 结构报告：输入/缓存命中读/缓存写入/输出四类
// billed token 的占比，按日或按模型分组。与 cache-report 同走 RollupRepo.Aggregate
// （保留期安全：已完成汇总的日期读聚合表、未完成读原始日志，清理后结果仍保留）。
// 用户侧不暴露网关采购成本与差额。
func (c *meStatsController) handleMeTokenReport(w http.ResponseWriter, r *http.Request) {
	u := auth.CurrentUser(r.Context())
	groupBy := r.URL.Query().Get("group_by")
	// 默认按模型：token 结构按模型分布是最常用的消费画像视角。
	dim := store.AggByModel
	if groupBy == "day" {
		dim = store.AggByDay
	}
	if groupBy == "project" {
		dim = store.AggByProject
	}
	from, to := meUsageRange(r)
	rows, err := c.Rollup.Aggregate(r.Context(), dim, store.AggFilter{
		UserID: u.ID, From: from, To: to,
	})
	if err != nil {
		obs.Logger(r.Context()).Error("token 结构查询失败",
			"from", from, "to", to, "user_id", u.ID, "error", err)
		respond.Fail(w, http.StatusInternalServerError, "token 结构查询失败")
		return
	}
	rate := c.Settings.GetInt64(r.Context(), "exchange_rate_credits_per_cny")
	mc := newMoneyCtx(rate)
	groups := make([]tokenReportGroup, 0, len(rows))
	var overall tokenReportOverall
	for _, row := range rows {
		total := row.PromptTokens + row.CacheReadTokens + row.CacheWriteTokens + row.CompletionTokens
		groups = append(groups, tokenReportGroup{
			GroupKey:         row.GroupKey,
			Requests:         row.Requests,
			PromptTokens:     row.PromptTokens,
			CacheReadTokens:  row.CacheReadTokens,
			CacheWriteTokens: row.CacheWriteTokens,
			CompletionTokens: row.CompletionTokens,
			TotalTokens:      total,
			CreditsCharged:   int64(row.CreditsCharged),
		})
		overall.PromptTokens += row.PromptTokens
		overall.CacheReadTokens += row.CacheReadTokens
		overall.CacheWriteTokens += row.CacheWriteTokens
		overall.CompletionTokens += row.CompletionTokens
	}
	overall.TotalTokens = overall.PromptTokens + overall.CacheReadTokens +
		overall.CacheWriteTokens + overall.CompletionTokens
	// 按日维度按时间升序输出，便于前端按时间轴作图；其余维度保留 token 量降序。
	if dim == store.AggByDay {
		sort.Slice(groups, func(i, j int) bool { return groups[i].GroupKey < groups[j].GroupKey })
	} else {
		sort.Slice(groups, func(i, j int) bool { return groups[i].TotalTokens > groups[j].TotalTokens })
	}
	respond.OK(w, tokenReportResponse{
		From:    from.Unix(),
		To:      to.Unix(),
		Overall: overall,
		Groups: wrapList(groups, func(g tokenReportGroup) tokenReportGroupWithMoney {
			return wrapTokenReportGroup(g, mc)
		}),
	})
}

// meHeatmapResponse 是 /api/me/heatmap 的响应体。与全站共用的 heatmapResponse
// 同形（from/to/cells），但 cells 元素旁置了货币串——本结构在此独立定义，
// 不改动 stats_helpers.go 里被管理端共用的 heatmapResponse。
type meHeatmapResponse struct {
	From  int64                  `json:"from"`
	To    int64                  `json:"to"`
	Cells []heatmapCellWithMoney `json:"cells"`
}

// handleMeHeatmap 当前用户的周×时活跃时段热力图。
// 走原始 usage_logs（按日汇总表不含小时维度），受保留期约束——热力图只看近期 ~30 天。
// 用户侧只返回本人的请求次数与扣费，不涉及网关采购成本。
func (c *meStatsController) handleMeHeatmap(w http.ResponseWriter, r *http.Request) {
	u := auth.CurrentUser(r.Context())
	from, to := meUsageRange(r)
	model := r.URL.Query().Get("model")
	cells, err := c.Stats.Heatmap(r.Context(), u.ID, from, to, model, 0, 0, nil)
	if err != nil {
		obs.Logger(r.Context()).Error("活跃时段查询失败",
			"from", from, "to", to, "user_id", u.ID, "model", model, "error", err)
		respond.Fail(w, http.StatusInternalServerError, "活跃时段查询失败")
		return
	}
	rate := c.Settings.GetInt64(r.Context(), "exchange_rate_credits_per_cny")
	mc := newMoneyCtx(rate)
	respond.OK(w, meHeatmapResponse{
		From: from.Unix(),
		To:   to.Unix(),
		Cells: wrapList(orEmptySlice(cells), func(c store.HeatmapCell) heatmapCellWithMoney {
			return wrapHeatmapCell(c, mc)
		}),
	})
}
