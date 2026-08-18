package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/liguopeng80/tokenzen-lite/server/internal/api/respond"
	"github.com/liguopeng80/tokenzen-lite/server/internal/auth"
	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// 本文件收口统计/报表端点共用的查询参数与时间范围解析助手，供 stats_admin.go /
// stats_me.go 的 handler 复用。不包含 handler 本身。

func daysParam(r *http.Request, fallback, max int) int {
	d, _ := strconv.Atoi(r.URL.Query().Get("days"))
	if d < 1 {
		return fallback
	}
	if d > max {
		return max
	}
	return d
}

// usageLogFilterFromQuery 从查询串构造日志筛选（userID>0 时强制限定）。
func usageLogFilterFromQuery(r *http.Request, userID int64) store.UsageLogFilter {
	q := r.URL.Query()
	page, pageSize := PageParams(r)
	f := store.UsageLogFilter{
		UserID:    userID,
		ModelName: q.Get("model"),
		Status:    domain.UsageStatus(q.Get("status")),
		RequestID: q.Get("request_id"),
		Page:      page, PageSize: pageSize,
		// 托管视角叠加本接入方作用域：只看本接入方账号产生的用量，与列表/导出口径一致。
		// 运营 admin/root 无作用域（nil），不过滤。员工自助路径（userID>0）受本人 ID 限定，
		// 这里再叠加作用域不影响其结果（内部账号作用域为 nil）。
		IntegrationID: auth.ScopeIntegrationID(r.Context()),
	}
	if userID == 0 {
		if uid, ok := parseInt64(q.Get("user_id")); ok {
			f.UserID = uid
		}
		// 按用户名筛选只在管理端视角生效：员工侧的 UserID 已被强制限定为本人，
		// 这里再放开一个用户维度的条件没有意义，也避免多一条越权的入口。
		f.Username = q.Get("username")
	}
	if kid, ok := parseInt64(q.Get("api_key_id")); ok {
		f.APIKeyID = kid
	}
	if cid, ok := parseInt64(q.Get("channel_id")); ok {
		f.ChannelID = cid
	}
	if ts, ok := parseInt64(q.Get("start_timestamp")); ok {
		t := time.Unix(ts, 0)
		f.StartTime = &t
	}
	if ts, ok := parseInt64(q.Get("end_timestamp")); ok {
		t := time.Unix(ts, 0)
		f.EndTime = &t
	}
	return f
}

// resolveDayRange 解析按日粒度统计端点的时间范围，统一处理三处历史缺陷：
//
//   - SpendDay(end) 把前端 endOf('day')（23:59:59）截断到当日 0 点，
//     配合 store 的 created_at < to（排他上界）会把 end 当日整日排除。
//     修正语义：start/end 为包含的自然日，to 取 end 次日 0 点。
//   - 显式范围分支缺少最大窗口收窄（siblings health=30d、calltype=90d 各自实现）。
//   - 各 handler 重复同一段 parse→default→SpendDay 逻辑。
//
// 返回值已按 SpendDay 对齐：from 为起日 0 点，to 为止日次日 0 点（排他，含止日全天）。
// 缺省时 to = 明日 0 点、from = to 倒推 defaultDays 天；窗口超过 maxDays 天时收窄 from
// （maxDays ≤ 0 时不收窄）。to 不晚于 from 时 ok=false，调用方应返回 400 或空结果。
func resolveDayRange(r *http.Request, defaultDays, maxDays int) (from, to time.Time, ok bool) {
	// 缺省基线：to = 明日 0 点（含今日），from = to 倒推 defaultDays 天。
	to = store.SpendDay(time.Now()).AddDate(0, 0, 1)
	from = to.AddDate(0, 0, -defaultDays)
	start, end := timeRangeParams(r)
	if start != nil {
		from = store.SpendDay(*start)
	}
	if end != nil {
		// end 为包含的自然日：次日 0 点作排他上界，含 end 当日全天。
		to = store.SpendDay(*end).AddDate(0, 0, 1)
	}
	if !to.After(from) {
		return time.Time{}, time.Time{}, false
	}
	if maxDays > 0 {
		if maxWindow := time.Duration(maxDays) * 24 * time.Hour; to.Sub(from) > maxWindow {
			from = to.Add(-maxWindow)
		}
	}
	return from, to, true
}

// meUsageRange 解析 /me 用量端点的时间范围。优先用 start_timestamp/end_timestamp
// （Unix 秒，按自然日对齐，与按日聚合表粒度一致），缺省时回退到 days 天回看
// （默认 30，上限 365），保留既有调用方的兼容入口。
//
// /me 端点对非法范围不返回 400：交由下游查询自然得到空结果（from=to 时排他上界
// 排除所有记录）。显式范围与缺省范围都受 365 天上限收窄。
func meUsageRange(r *http.Request) (from, to time.Time) {
	// /me 端点保留 days 回退入口：无 start/end 时按 days 天回看（默认 30，上限 365）。
	defaultDays := daysParam(r, 30, 365)
	f, t, ok := resolveDayRange(r, defaultDays, 365)
	if !ok {
		// 非法范围：返回零宽区间，下游查询自然返回空。
		now := store.SpendDay(time.Now()).AddDate(0, 0, 1)
		return now, now
	}
	return f, t
}

// monthFromQuery 解析 ?month=YYYY-MM 月份参数，返回该月锚点（已归一为本地时区 1 日 0 点）。
// 缺省返回当前自然月锚点。格式非法时写 400 并返回 ok=false，调用方应直接 return。
// 收口 admin_reports / dept_reports / stats 三处重复的 parse + 400 片段。
func monthFromQuery(w http.ResponseWriter, r *http.Request) (month time.Time, ok bool) {
	raw := r.URL.Query().Get("month")
	if raw == "" {
		thisMonth, _ := store.MonthRange(time.Now())
		return thisMonth, true
	}
	parsed, err := time.ParseInLocation("2006-01", raw, time.Local)
	if err != nil {
		respond.Fail(w, http.StatusBadRequest, "月份格式应为 YYYY-MM")
		return time.Time{}, false
	}
	from, _ := store.MonthRange(parsed)
	return from, true
}

// heatmapResponse GET /api/me/heatmap 与 GET /api/admin/stats/heatmap 的共同响应体。
// cells 只含产生了数据的格子，前端把空格补零。
type heatmapResponse struct {
	From  int64               `json:"from"`
	To    int64               `json:"to"`
	Cells []store.HeatmapCell `json:"cells"`
}
