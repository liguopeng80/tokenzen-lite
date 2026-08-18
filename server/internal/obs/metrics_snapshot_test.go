package obs

import (
	"testing"
)

// TestSnapshotGaugesCountersHistograms 覆盖 Snapshot() 的三类指标：
// gauge 即时取值、counter 标签解析与累计值、histogram 的 count/sum 与分位单调性。
// 不依赖 DB。
func TestSnapshotGaugesCountersHistograms(t *testing.T) {
	m := NewMetrics()
	// counter：单标签 + 多标签 + 累加
	m.Inc("tzl_test_total", "status", "2xx")
	m.Inc("tzl_test_total", "group", "api/me", "status", "5xx")
	m.Add("tzl_test_total", 3, "group", "api/me", "status", "5xx")
	// gauge
	val := 5.0
	m.SetGaugeFunc("tzl_test_in_flight", "在途", func() float64 { return val })
	// histogram：已知耗时分布，便于核对分位
	// observations = {0.05, 0.3, 2, 10} → counts = [1,1,1,2,2,3,3,4,4,4,4], total=4, sum=12.35
	m.Observe("tzl_test_seconds", 0.05, "model", "a")
	m.Observe("tzl_test_seconds", 0.3, "model", "a")
	m.Observe("tzl_test_seconds", 2, "model", "a")
	m.Observe("tzl_test_seconds", 10, "model", "a")

	snap := m.Snapshot()

	// gauge：取值在导出时调用，反映当下。
	if len(snap.Gauges) != 1 || snap.Gauges[0].Name != "tzl_test_in_flight" || snap.Gauges[0].Value != 5 {
		t.Errorf("gauge 不符: %+v", snap.Gauges)
	}
	val = 9
	snap2 := m.Snapshot()
	if snap2.Gauges[0].Value != 9 {
		t.Errorf("gauge 应在每次快照时重新取值，期望 9，实际 %v", snap2.Gauges[0].Value)
	}

	// counter：期望 3 条序列（status=2xx、status=2xx+group=api/me 错序合并到一条、group=api/me+status=5xx）
	if len(snap.Counters) != 2 {
		t.Fatalf("counter 序列数不符，期望 2，实际 %d: %+v", len(snap.Counters), snap.Counters)
	}
	var foundMulti bool
	for _, c := range snap.Counters {
		if c.Name == "tzl_test_total" && c.Labels["group"] == "api/me" && c.Labels["status"] == "5xx" {
			if c.Value != 4 {
				t.Errorf("counter value 期望 4（1+3），实际 %v", c.Value)
			}
			foundMulti = true
		}
	}
	if !foundMulti {
		t.Errorf("未找到预期的多标签 counter 序列: %+v", snap.Counters)
	}

	// histogram：count/sum 精确，分位单调
	if len(snap.Histograms) != 1 {
		t.Fatalf("histogram 序列数不符，期望 1，实际 %d: %+v", len(snap.Histograms), snap.Histograms)
	}
	h := snap.Histograms[0]
	if h.Count != 4 {
		t.Errorf("histogram count 期望 4，实际 %d", h.Count)
	}
	if h.Sum != 12.35 {
		t.Errorf("histogram sum 期望 12.35，实际 %v", h.Sum)
	}
	if h.P50 > h.P95 || h.P95 > h.P99 {
		t.Errorf("分位应单调 P50<=P95<=P99，实际 P50=%v P95=%v P99=%v", h.P50, h.P95, h.P99)
	}
	// P50 应落在 [0, 1] 区间（target=2 命中 bounds=0.5 桶，插值结果 0.5）
	if h.P50 < 0 || h.P50 > 1 {
		t.Errorf("P50 应在合理区间 [0,1]，实际 %v", h.P50)
	}
}

// TestSnapshotNilReceiver nil 接收者安全降级，不 panic。
func TestSnapshotNilReceiver(t *testing.T) {
	var m *Metrics
	snap := m.Snapshot()
	if snap.GeneratedAt.IsZero() {
		t.Errorf("nil 接收者的快照仍应填 GeneratedAt")
	}
	if len(snap.Gauges) != 0 || len(snap.Counters) != 0 || len(snap.Histograms) != 0 {
		t.Errorf("nil 接收者的快照应为空，实际 %+v", snap)
	}
}

// TestParseLabelKey 覆盖 labelKey 反解析：空串、单标签、多标签、含反斜杠。
func TestParseLabelKey(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want map[string]string
	}{
		{"空串", "", map[string]string{}},
		{"无内容大括号", "{}", map[string]string{}},
		{"单标签", `{status="2xx"}`, map[string]string{"status": "2xx"}},
		{"多标签", `{group="api/me",status="5xx"}`, map[string]string{"group": "api/me", "status": "5xx"}},
		// 含反斜杠：值经 %q 转义为 "a\\b"，Unquote 反转为 a\b。
		{"含反斜杠", `{path="a\\b"}`, map[string]string{"path": `a\b`}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseLabelKey(tc.in)
			if len(got) != len(tc.want) {
				t.Fatalf("长度不符，期望 %d，实际 %d: got=%+v", len(tc.want), len(got), got)
			}
			for k, v := range tc.want {
				if got[k] != v {
					t.Errorf("key %q 期望 %q，实际 %q", k, v, got[k])
				}
			}
		})
	}
}

// TestParseLabelKeyRoundTrip 验证 parseLabelKey(labelKey(labels)) 的互逆性：
// 返回值是 escapeLabel 之后的形态（escapeLabel 的换行→空格是信息有损的，
// 反斜杠翻倍经 Unquote 后还原为翻倍形态——即 escapeLabel 之后的形态）。
func TestParseLabelKeyRoundTrip(t *testing.T) {
	cases := [][]string{
		{"status", "2xx"},
		{"group", "api/me", "status", "5xx"},
		{"channel", "123"},
		{"model", "gpt-4"},
		{"path", `a\b`}, // 含反斜杠
	}
	for _, labels := range cases {
		key := labelKey(labels)
		got := parseLabelKey(key)
		want := map[string]string{}
		for i := 0; i+1 < len(labels); i += 2 {
			want[labels[i]] = escapeLabel(labels[i+1])
		}
		if len(got) != len(want) {
			t.Errorf("round-trip 长度不符 labels=%v key=%q got=%+v want=%+v", labels, key, got, want)
			continue
		}
		for k, v := range want {
			if got[k] != v {
				t.Errorf("round-trip key=%q 期望 %q 实际 %q（labels=%v labelKey=%q）", k, v, got[k], labels, key)
			}
		}
	}
}

// TestHistQuantile 用已知分布核对分位插值：
// observations = {0.05, 0.3, 2, 10} → counts = [1,1,1,2,2,3,3,4,4,4,4]。
// P50=0.5（target=2 命中 0.5 桶，插值 0.25+0.25）；
// P95=9（target=3.8 命中 10 桶，插值 5+4）；
// P99=9.8（target=3.96 命中 10 桶，插值 5+4.8）。
func TestHistQuantile(t *testing.T) {
	bounds := []float64{0.05, 0.1, 0.25, 0.5, 1, 2, 5, 10, 30, 60, 120}
	counts := []uint64{1, 1, 1, 2, 2, 3, 3, 4, 4, 4, 4}
	const total = uint64(4)

	if got := histQuantile(counts, bounds, 0, 0.5); got != 0 {
		t.Errorf("total=0 应返回 0，实际 %v", got)
	}
	if got := histQuantile(counts, bounds, total, 0.5); got != 0.5 {
		t.Errorf("P50 期望 0.5，实际 %v", got)
	}
	if got := histQuantile(counts, bounds, total, 0.95); got != 9 {
		t.Errorf("P95 期望 9，实际 %v", got)
	}
	if got := histQuantile(counts, bounds, total, 0.99); got != 9.8 {
		t.Errorf("P99 期望 9.8，实际 %v", got)
	}
	// 空 counts 安全降级
	if got := histQuantile(nil, bounds, 0, 0.5); got != 0 {
		t.Errorf("空 counts 应返回 0，实际 %v", got)
	}
}
