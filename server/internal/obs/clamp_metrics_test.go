package obs

import (
	"strconv"
	"strings"
	"testing"
)

// 结算截断（ClampToBalance）把欠扣的积分抹掉后，账本仍平（balance==Σledger），
// reconcile 抓不到这类「已计费的应付额被截断」的偏差，必须在指标层单独暴露。
// 这里固定 RecordClampShortfall 同时递增事件计数与累计欠扣量，且两者都经 /metrics 导出。
func TestRecordClampShortfall(t *testing.T) {
	// 经 Reset 把 defaultMetrics 清零：用例从绝对值断言，不再依赖与其它用例的执行顺序，
	// 也不必再用 before/after 差量。t.Cleanup 还原给后续用例一个干净基线。
	Reset()
	t.Cleanup(Reset)

	RecordClampShortfall(500)
	RecordClampShortfall(250)

	if got := counterValue(t, defaultMetrics, MetricBillingClampEvents); got != 2 {
		t.Errorf("事件计数应为 2，实际 %v", got)
	}
	if got := counterValue(t, defaultMetrics, MetricBillingClampShortfall); got != 750 {
		t.Errorf("累计欠扣应为 750，实际 %v", got)
	}

	out := defaultMetrics.Export()
	if !strings.Contains(out, MetricBillingClampEvents) {
		t.Errorf("导出文本应含 %s\n%s", MetricBillingClampEvents, out)
	}
	if !strings.Contains(out, MetricBillingClampShortfall) {
		t.Errorf("导出文本应含 %s\n%s", MetricBillingClampShortfall, out)
	}
}

// counterValue 从 Metrics 导出文本中解析指定无标签计数器的当前值。
// 行格式为「<name> <value>」。
func counterValue(t *testing.T, m *Metrics, name string) float64 {
	t.Helper()
	for _, line := range strings.Split(m.Export(), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") || line == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) >= 2 && parts[0] == name {
			v, err := strconv.ParseFloat(parts[1], 64)
			if err != nil {
				t.Fatalf("解析计数器 %s 的值 %q 失败: %v", name, parts[1], err)
			}
			return v
		}
	}
	return 0
}
