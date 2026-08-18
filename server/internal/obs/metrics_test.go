package obs

import (
	"strings"
	"testing"
)

// 计数器与直方图导出为 Prometheus 文本格式：同一组标签无论传入顺序如何
// 都落在同一条时间序列上，否则抓取端会把同一件事看成两条曲线。
func TestMetricsExportCounterAndHistogram(t *testing.T) {
	m := NewMetrics()
	m.Describe("tzl_test_total", "测试计数器")
	m.Inc("tzl_test_total", "group", "api/me", "status", "2xx")
	m.Inc("tzl_test_total", "status", "2xx", "group", "api/me")
	m.Inc("tzl_test_total", "group", "api/me", "status", "5xx")

	out := m.Export()
	if !strings.Contains(out, `tzl_test_total{group="api/me",status="2xx"} 2`) {
		t.Errorf("标签顺序不同的两次计数应合并到同一序列：\n%s", out)
	}
	if !strings.Contains(out, `tzl_test_total{group="api/me",status="5xx"} 1`) {
		t.Errorf("不同标签应各自成列：\n%s", out)
	}
	if !strings.Contains(out, "# HELP tzl_test_total 测试计数器") {
		t.Errorf("缺少 HELP 行：\n%s", out)
	}
}

// 直方图的分桶是累计计数：落在某桶的观测同时计入其后全部更大的桶，
// 抓取端按这一约定计算分位数。
func TestMetricsHistogramBucketsAreCumulative(t *testing.T) {
	m := NewMetrics()
	m.Observe("tzl_test_seconds", 0.2, "group", "v1")
	m.Observe("tzl_test_seconds", 3, "group", "v1")

	out := m.Export()
	for _, want := range []string{
		`tzl_test_seconds_bucket{group="v1",le="0.1"} 0`,
		`tzl_test_seconds_bucket{group="v1",le="0.25"} 1`,
		`tzl_test_seconds_bucket{group="v1",le="5"} 2`,
		`tzl_test_seconds_bucket{group="v1",le="+Inf"} 2`,
		`tzl_test_seconds_count{group="v1"} 2`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("缺少 %q：\n%s", want, out)
		}
	}
}

// 仪表在导出时取值，反映当下而非历史。
func TestMetricsGaugeReadsCurrentValue(t *testing.T) {
	m := NewMetrics()
	value := 3.0
	m.SetGaugeFunc("tzl_test_in_flight", "在途数", func() float64 { return value })

	if !strings.Contains(m.Export(), "tzl_test_in_flight 3") {
		t.Errorf("仪表初值不符：\n%s", m.Export())
	}
	value = 7
	if !strings.Contains(m.Export(), "tzl_test_in_flight 7") {
		t.Errorf("仪表应在每次导出时重新取值：\n%s", m.Export())
	}
}

// 路径归并为固定分组，资源 ID 不进标签——否则时间序列数量会随用户数与渠道数增长。
func TestRouteGroupCollapsesResourceIDs(t *testing.T) {
	cases := map[string]string{
		"/healthz":                    "healthz",
		"/metrics":                    "metrics",
		"/api/admin/users/12345":      "api/admin",
		"/api/admin/channels/7/costs": "api/admin",
		"/api/me/keys/99":             "api/me",
		"/api/dept/budget":            "api/dept",
		"/api/auth/login":             "api/auth",
		"/api/site/config":            "api/public",
		"/v1/chat/completions":        "/v1/chat/completions",
		"/v1/messages":                "/v1/messages",
		"/unknown":                    "other",
	}
	for path, want := range cases {
		if got := RouteGroup(path); got != want {
			t.Errorf("RouteGroup(%q) = %q，期望 %q", path, got, want)
		}
	}
}
