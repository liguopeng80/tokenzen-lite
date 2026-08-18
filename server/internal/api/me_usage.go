package api

import (
	"encoding/csv"
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

// /me 侧用量日志的详情与导出。列表 handler（handleMeListUsageLogs）在 stats_me.go，
// 共用的筛选助手在 stats_helpers.go，导出共用的分页批量与行数上限在 admin_reports.go。
//
// 用户侧一律不暴露渠道身份、采购成本（credits_cost）、差额、价格快照（price_snapshot）、
// 上游路由（upstream_model/protocol）、计费中间态（credits_precharged）、接入方、
// 客户端 IP 等运营字段；只返回本人请求的 token 明细、扣费积分、耗时、状态与时间。

// meUsageLogRow 用户侧用量日志行。剥离运营字段，与 /me/usage-logs 列表、
// /me/usage-logs/detail、/me/usage-logs/export 三处保持一致脱敏口径。
type meUsageLogRow struct {
	ID                int64              `json:"id"`
	RequestID         string             `json:"request_id"`
	UserID            int64              `json:"user_id"`
	APIKeyID          int64              `json:"api_key_id"`
	ModelName         string             `json:"model_name"`
	IsStream          bool               `json:"is_stream"`
	PromptTokens      int64              `json:"prompt_tokens"`
	CompletionTokens  int64              `json:"completion_tokens"`
	CacheReadTokens   int64              `json:"cache_read_tokens"`
	CacheWriteTokens  int64              `json:"cache_write_tokens"`
	AudioInputTokens  int64              `json:"audio_input_tokens"`
	AudioOutputTokens int64              `json:"audio_output_tokens"`
	CallCount         int64              `json:"call_count"`
	UsageEstimated    bool               `json:"usage_estimated"`
	CreditsCharged    domain.Credits     `json:"credits_charged"`
	Status            domain.UsageStatus `json:"status"`
	ErrorClass        domain.ErrorClass  `json:"error_class"`
	ErrorMessage      string             `json:"error_message"`
	LatencyMS         int64              `json:"latency_ms"`
	FirstByteMS       int64              `json:"first_byte_ms"`
	CreatedAt         time.Time          `json:"created_at"`
}

// meUsageLogRowWithMoney 包装 meUsageLogRow，旁置扣费积分的货币串。
type meUsageLogRowWithMoney struct {
	meUsageLogRow
	CreditsChargedMoney string `json:"credits_charged_money"`
}

// wrapMeUsageLogRow 把用户侧用量日志行的扣费积分换算为货币串。
func wrapMeUsageLogRow(r meUsageLogRow, mc moneyCtx) meUsageLogRowWithMoney {
	return meUsageLogRowWithMoney{
		meUsageLogRow:       r,
		CreditsChargedMoney: mc.money(r.CreditsCharged),
	}
}

// toMeUsageLogRow 把存储层 UsageLog 投影为用户侧可见行，丢弃一切运营字段。
func toMeUsageLogRow(l *store.UsageLog) meUsageLogRow {
	return meUsageLogRow{
		ID: l.ID, RequestID: l.RequestID, UserID: l.UserID, APIKeyID: l.APIKeyID,
		ModelName: l.ModelName, IsStream: l.IsStream,
		PromptTokens: l.PromptTokens, CompletionTokens: l.CompletionTokens,
		CacheReadTokens: l.CacheReadTokens, CacheWriteTokens: l.CacheWriteTokens,
		AudioInputTokens: l.AudioInputTokens, AudioOutputTokens: l.AudioOutputTokens,
		CallCount: l.CallCount, UsageEstimated: l.UsageEstimated,
		CreditsCharged: l.CreditsCharged, Status: l.Status,
		ErrorClass: l.ErrorClass, ErrorMessage: l.ErrorMessage,
		LatencyMS: l.LatencyMS, FirstByteMS: l.FirstByteMS,
		CreatedAt: l.CreatedAt,
	}
}

// toMeUsageLogRows 批量投影。新建切片而非原地改写，保持不可变风格。
func toMeUsageLogRows(logs []store.UsageLog) []meUsageLogRow {
	rows := make([]meUsageLogRow, len(logs))
	for i := range logs {
		rows[i] = toMeUsageLogRow(&logs[i])
	}
	return rows
}

// handleMeGetUsageLog GET /api/me/usage-logs/detail?request_id=：按 request_id
// 直达单条日志，强制 user_id = 当前登录用户。request_id 不存在或不属于本人一律 404
// （不作区分，避免借 request_id 探测他人请求的归属），缺少 request_id 参数 400。
func (c *meStatsController) handleMeGetUsageLog(w http.ResponseWriter, r *http.Request) {
	requestID := r.URL.Query().Get("request_id")
	if requestID == "" {
		respond.Fail(w, http.StatusBadRequest, "缺少 request_id 参数")
		return
	}
	u := auth.CurrentUser(r.Context())
	l, err := c.UsageLogs.GetByRequestID(r.Context(), requestID)
	if err != nil {
		respond.Fail(w, http.StatusNotFound, "日志不存在")
		return
	}
	// 作用域隔离：他人的记录按「不存在」处理，与跨作用域对象访问的既有口径一致。
	if l.UserID != u.ID {
		respond.Fail(w, http.StatusNotFound, "日志不存在")
		return
	}
	rate := c.Settings.GetInt64(r.Context(), "exchange_rate_credits_per_cny")
	respond.OK(w, wrapMeUsageLogRow(toMeUsageLogRow(l), newMoneyCtx(rate)))
}

// handleMeExportUsageLogs GET /api/me/usage-logs/export：按当前筛选导出 CSV 流。
// 复用 admin 导出的分页拉取策略与 20 万行截断上限，列只含用户可见字段，
// 与 /me/usage-logs 列表一致脱敏。响应直接输出 CSV 流（UTF-8 BOM 开头），
// 不套用统一信封——消费者是浏览器与表格软件。
func (c *meStatsController) handleMeExportUsageLogs(w http.ResponseWriter, r *http.Request) {
	u := auth.CurrentUser(r.Context())
	f := usageLogFilterFromQuery(r, u.ID)
	f.Page, f.PageSize = 1, exportPageSize

	// 兑换率与对应明细精度：默认 1e6 → 6 位小数，逐行金额无损可汇总。
	creditsPerUnit := c.Settings.GetInt64(r.Context(), "exchange_rate_credits_per_cny")
	moneyDecimals := pricing.DetailDecimals(creditsPerUnit)

	filename := "my-usage-logs-" + time.Now().Format("20060102-150405") + ".csv"
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	// UTF-8 BOM：Excel 默认按本地编码打开无 BOM 的 CSV，中文会乱码。
	_, _ = w.Write([]byte{0xEF, 0xBB, 0xBF})

	cw := csv.NewWriter(w)
	defer cw.Flush()
	header := []string{"时间", "请求标识", "密钥 ID", "模型",
		"输入 token", "输出 token", "缓存读 token", "缓存写 token",
		"扣费积分", "扣费金额", "状态", "错误分类", "耗时毫秒", "流式"}
	if err := cw.Write(header); err != nil {
		obs.Logger(r.Context()).Error("导出我的用量日志写表头失败", "error", err)
		return
	}

	written := 0
	for {
		logs, _, err := c.UsageLogs.List(r.Context(), f)
		if err != nil {
			obs.Logger(r.Context()).Error("导出我的用量日志查询失败",
				"page", f.Page, "user_id", u.ID, "error", err)
			return
		}
		if len(logs) == 0 {
			break
		}
		for i := range logs {
			if err := cw.Write(meUsageLogCSVRow(&logs[i], creditsPerUnit, moneyDecimals)); err != nil {
				obs.Logger(r.Context()).Error("导出我的用量日志写入失败", "error", err)
				return
			}
			written++
			if written >= maxExportRows {
				obs.Logger(r.Context()).Warn("导出我的用量日志达到行数上限，结果已截断",
					"limit", maxExportRows, "user_id", u.ID)
				return
			}
		}
		if len(logs) < f.PageSize {
			break
		}
		cw.Flush()
		f.Page++
	}
	obs.Logger(r.Context()).Info("导出我的用量日志完成",
		"rows", written, "user_id", u.ID)
}

// meUsageLogCSVRow 用户侧 CSV 行：只含用户可见列，不含渠道/成本/差额/价格快照。
func meUsageLogCSVRow(l *store.UsageLog, creditsPerUnit int64, decimals int) []string {
	return []string{
		l.CreatedAt.Local().Format(time.RFC3339),
		l.RequestID,
		strconv.FormatInt(l.APIKeyID, 10),
		l.ModelName,
		strconv.FormatInt(l.PromptTokens, 10),
		strconv.FormatInt(l.CompletionTokens, 10),
		strconv.FormatInt(l.CacheReadTokens, 10),
		strconv.FormatInt(l.CacheWriteTokens, 10),
		strconv.FormatInt(l.CreditsCharged, 10),
		pricing.CreditsToDecimalString(l.CreditsCharged, creditsPerUnit, decimals),
		string(l.Status),
		string(l.ErrorClass),
		strconv.FormatInt(l.LatencyMS, 10),
		boolToCN(l.IsStream),
	}
}

// boolToCN 把布尔值转为中文「是」「否」，供 CSV 的流式等列展示。
func boolToCN(v bool) string {
	if v {
		return "是"
	}
	return "否"
}
