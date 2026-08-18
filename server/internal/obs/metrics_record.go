package obs

import (
	"strconv"
	"strings"
	"time"
)

// 指标名。前缀统一为 tzl_，与抓取端上的其它服务区分。
const (
	MetricHTTPRequests    = "tzl_http_requests_total"
	MetricHTTPDuration    = "tzl_http_request_duration_seconds"
	MetricRelayRequests   = "tzl_relay_requests_total"
	MetricRelayErrors     = "tzl_relay_errors_total"
	MetricRelayDuration   = "tzl_relay_duration_seconds"
	MetricChannelAttempts = "tzl_channel_attempts_total"
	MetricRelayInFlight   = "tzl_relay_in_flight"
	MetricUsageLogDropped = "tzl_usage_log_dropped"
	// 结算截断（ClampToBalance）：余额不足时少扣的次数与累计欠扣积分。
	// 截断后 balance==Σledger 仍成立，reconcile 抓不到这类「已计费的应付额被抹掉」的偏差，
	// 必须单独计数才能在指标层面暴露系统性欠扣。
	MetricBillingClampEvents    = "tzl_billing_clamp_events_total"
	MetricBillingClampShortfall = "tzl_billing_clamp_shortfall_total"
	// 音频 token 零计费：模型未配音频单价（AudioInputPrice/OutputPrice 均 ≤0）却收到音频 token，
	// 这部分按 0 单价放行（CalcTokenCredits 的 addMul 对 price≤0 跳过）。账本仍平，reconcile 抓不到，
	// 必须单独计数才能在指标层面暴露漏配价，供运营为相关模型补单。
	MetricBillingAudioZeroPrice = "tzl_billing_audio_zero_price_total"
	// 渠道亲和（方案 C 节）：同对话绑同渠道以保上游 prompt cache 命中。
	// hit = 既有绑定有效、命中绑定渠道；miss = 有亲和键但首次请求无绑定；
	// drift = 绑定失效（渠道故障/被排除/优先级层变化）回退加权随机。
	// 命中率 = hit / (hit + miss + drift)，漂移率 = drift / 同分母。
	MetricRelayAffinityHit   = "tzl_relay_affinity_hit_total"
	MetricRelayAffinityMiss  = "tzl_relay_affinity_miss_total"
	MetricRelayAffinityDrift = "tzl_relay_affinity_drift_total"
)

// defaultMetrics 是进程内的默认指标集合。与 slog 的默认 logger 同样是全局单件：
// 埋点分布在中继、中间件、后台任务等处，逐层传递一个指标句柄只会让签名变长，
// 而指标本身没有多实例的语义。
var defaultMetrics = newDescribedMetrics()

// DefaultMetrics 返回默认指标集合，供导出端点与仪表登记使用。
func DefaultMetrics() *Metrics { return defaultMetrics }

func newDescribedMetrics() *Metrics {
	m := NewMetrics()
	m.Describe(MetricHTTPRequests, "按接口分组与状态类别统计的 HTTP 请求数")
	m.Describe(MetricHTTPDuration, "HTTP 请求耗时分布（秒）")
	m.Describe(MetricRelayRequests, "按模型与计费终态统计的 /v1 中继请求数")
	m.Describe(MetricRelayErrors, "按模型与错误分类统计的中继失败数")
	m.Describe(MetricRelayDuration, "中继请求耗时分布（秒）")
	m.Describe(MetricChannelAttempts, "按渠道与结果统计的上游尝试次数")
	m.Describe(MetricBillingClampEvents, "结算截断（ClampToBalance）事件次数")
	m.Describe(MetricBillingClampShortfall, "结算截断累计欠扣积分")
	m.Describe(MetricBillingAudioZeroPrice, "音频用量零计费累计 token 数（模型未配音频单价）")
	m.Describe(MetricRelayAffinityHit, "渠道亲和命中次数（绑定渠道有效，绕过加权随机）")
	m.Describe(MetricRelayAffinityMiss, "渠道亲和未命中次数（有亲和键但首次请求无绑定）")
	m.Describe(MetricRelayAffinityDrift, "渠道亲和漂移次数（绑定失效回退加权随机）")
	return m
}

// RecordHTTPRequest 记录一次 HTTP 请求。状态码归并为类别（2xx/4xx/5xx）：
// 逐个状态码会让时间序列数量随错误种类增长，而排障需要的是「错误率有没有上升」。
func RecordHTTPRequest(path, method string, status int, elapsed time.Duration) {
	group := RouteGroup(path)
	defaultMetrics.Inc(MetricHTTPRequests,
		"group", group, "method", method, "status", statusClass(status))
	defaultMetrics.Observe(MetricHTTPDuration, elapsed.Seconds(), "group", group)
}

// RecordRelayRequest 记录一次中继请求的终态。errorClass 为空表示成功。
func RecordRelayRequest(model, status, errorClass string, latencyMS int64) {
	if model == "" {
		model = "unknown"
	}
	defaultMetrics.Inc(MetricRelayRequests, "model", model, "status", status)
	defaultMetrics.Observe(MetricRelayDuration, float64(latencyMS)/1000, "model", model)
	if errorClass != "" {
		defaultMetrics.Inc(MetricRelayErrors, "model", model, "error_class", errorClass)
	}
}

// RecordChannelAttempt 记录一次上游渠道尝试的结果。
// outcome 取 success / failure，用于观察单个渠道的健康度。
func RecordChannelAttempt(channelID int64, outcome string) {
	defaultMetrics.Inc(MetricChannelAttempts,
		"channel", strconv.FormatInt(channelID, 10), "outcome", outcome)
}

// RecordClampShortfall 记录一次结算截断（ClampToBalance）事件：
// 余额不足以补扣时，金额被截断到 0，欠扣部分从此计数器暴露，
// 使系统性欠扣可被聚合发现（reconcile 抓不到——截断后账本仍平）。
func RecordClampShortfall(shortfall int64) {
	defaultMetrics.Inc(MetricBillingClampEvents)
	defaultMetrics.Add(MetricBillingClampShortfall, float64(shortfall))
}

// RecordAudioZeroPrice 记录零计费的音频 token 数（模型未配音频单价却收到音频用量）。
// 计费路径对这类用量按 0 单价放行（CalcTokenCredits 对 price≤0 跳过），账本仍平，
// reconcile 抓不到——必须单独计数才能在指标层面暴露漏配价，供运营为相关模型补单。
func RecordAudioZeroPrice(tokens int64) {
	if tokens <= 0 {
		return
	}
	defaultMetrics.Add(MetricBillingAudioZeroPrice, float64(tokens))
}

// RecordAffinityHit 记录一次渠道亲和命中（绑定渠道有效，绕过加权随机）。
func RecordAffinityHit() { defaultMetrics.Inc(MetricRelayAffinityHit) }

// RecordAffinityMiss 记录一次渠道亲和未命中（有亲和键但首次请求无既有绑定）。
func RecordAffinityMiss() { defaultMetrics.Inc(MetricRelayAffinityMiss) }

// RecordAffinityDrift 记录一次渠道亲和漂移（绑定失效回退加权随机）。
func RecordAffinityDrift() { defaultMetrics.Inc(MetricRelayAffinityDrift) }

// statusClass 把状态码归并为类别。
func statusClass(status int) string {
	switch {
	case status >= 500:
		return "5xx"
	case status >= 400:
		return "4xx"
	case status >= 300:
		return "3xx"
	default:
		return "2xx"
	}
}

// RouteGroup 把请求路径归并为固定的接口分组。
//
// 直接用原始路径会把资源 ID 带进标签，时间序列数量随用户数与渠道数增长；
// 归并到分组后标签集合是有限且稳定的，同时保留了「是管理端慢还是中继慢」这一区分。
func RouteGroup(path string) string {
	switch {
	case path == "/healthz":
		return "healthz"
	case path == "/metrics":
		return "metrics"
	case strings.HasPrefix(path, "/v1/"):
		// 中继端点按具体端点区分：它们的耗时分布差异巨大，混在一起看不出问题。
		return strings.TrimSuffix(path, "/")
	case strings.HasPrefix(path, "/api/auth"):
		return "api/auth"
	case strings.HasPrefix(path, "/api/admin"):
		return "api/admin"
	case strings.HasPrefix(path, "/api/dept"):
		return "api/dept"
	case strings.HasPrefix(path, "/api/me"):
		return "api/me"
	case strings.HasPrefix(path, "/api/"):
		return "api/public"
	default:
		return "other"
	}
}

// 渠道尝试的结果取值。
const (
	ChannelOutcomeSuccess = "success"
	ChannelOutcomeFailure = "failure"
)
