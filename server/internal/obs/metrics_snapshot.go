package obs

import (
	"strconv"
	"strings"
	"time"
)

// Snapshot 是结构化运行指标快照，供 /admin/stats/runtime JSON 端点消费。
//
// 与 Export() 的 Prometheus 文本格式相对：本结构面向浏览器端展示，直接给出
// 分位估算（histQuantile）与解析好的标签 map，前端无需实现 Prom 文本解析
// 与分位插值。字段顺序按 gauges → counters → histograms 排列，与 Export()
// 的输出顺序一致，便于人工核对。
type Snapshot struct {
	GeneratedAt time.Time        `json:"generated_at"`
	Gauges      []GaugeValue     `json:"gauges"`
	Counters    []CounterValue   `json:"counters"`
	Histograms  []HistogramValue `json:"histograms"`
}

// GaugeValue 是仪表的即时取值（无标签）。
type GaugeValue struct {
	Name  string  `json:"name"`
	Value float64 `json:"value"`
}

// CounterValue 是计数器的一条时间序列：标签 map + 累计值。
type CounterValue struct {
	Name   string            `json:"name"`
	Labels map[string]string `json:"labels"`
	Value  float64           `json:"value"`
}

// HistogramValue 是直方图的一条时间序列：count/sum 为精确值，
// P50/P95/P99 是从累计桶计数线性插值估算的分位（秒）。
type HistogramValue struct {
	Name   string            `json:"name"`
	Labels map[string]string `json:"labels"`
	Count  uint64            `json:"count"`
	Sum    float64           `json:"sum"`
	P50    float64           `json:"p50"`
	P95    float64           `json:"p95"`
	P99    float64           `json:"p99"`
}

// Snapshot 返回结构化运行指标快照，供 /admin/stats/runtime JSON 端点消费。
//
// 锁处理严格镜像 Export()：持锁期间只做 counters/histograms/gauges 的深拷贝
// （histograms 的 counts 切片逐条 copy），释放锁后再调用 gauge 取值函数、
// 计算分位、组装返回。gauge 取值函数可能访问其它组件的锁（例如在途请求计数），
// 在持有本锁时调用会引入锁序依赖，违反已记录的约束。
func (m *Metrics) Snapshot() Snapshot {
	if m == nil {
		return Snapshot{GeneratedAt: time.Now()}
	}
	m.mu.Lock()
	counters := make(map[string]map[string]float64, len(m.counters))
	for name, series := range m.counters {
		cp := make(map[string]float64, len(series))
		for k, v := range series {
			cp[k] = v
		}
		counters[name] = cp
	}
	hists := make(map[string]map[string]histLine, len(m.histograms))
	for name, series := range m.histograms {
		cp := make(map[string]histLine, len(series))
		for k, v := range series {
			counts := make([]uint64, len(v.counts))
			copy(counts, v.counts)
			cp[k] = histLine{counts: counts, sum: v.sum, total: v.total}
		}
		hists[name] = cp
	}
	// gauge 只拷贝取值函数引用，不在此处调用（锁序约束）。
	gauges := make(map[string]func() float64, len(m.gauges))
	for name, f := range m.gauges {
		gauges[name] = f
	}
	m.mu.Unlock()

	out := Snapshot{GeneratedAt: time.Now()}
	for _, name := range sortedGaugeKeys(gauges) {
		out.Gauges = append(out.Gauges, GaugeValue{Name: name, Value: gauges[name]()})
	}
	for _, name := range sortedKeys(counters) {
		for _, key := range sortedKeysFloat(counters[name]) {
			out.Counters = append(out.Counters, CounterValue{
				Name:   name,
				Labels: parseLabelKey(key),
				Value:  counters[name][key],
			})
		}
	}
	for _, name := range sortedKeysHist(hists) {
		for _, key := range sortedKeysHistLine(hists[name]) {
			line := hists[name][key]
			out.Histograms = append(out.Histograms, HistogramValue{
				Name:   name,
				Labels: parseLabelKey(key),
				Count:  line.total,
				Sum:    line.sum,
				P50:    histQuantile(line.counts, durationBucketsSec, line.total, 0.5),
				P95:    histQuantile(line.counts, durationBucketsSec, line.total, 0.95),
				P99:    histQuantile(line.counts, durationBucketsSec, line.total, 0.99),
			})
		}
	}
	return out
}

// parseLabelKey 反解 labelKey() 产出的 {k="v",k2="v2"} 格式，返回键值 map。
//
// 空串（无标签的指标）返回空 map。键由 labelKey 按字典序排好，无需再排。
// 值经 strconv.Unquote 反转 fmt.Sprintf("%q", ...) 的转义；escapeLabel 的
// 换行→空格替换是信息有损的，无法反转，返回的是 escapeLabel 之后的形态。
func parseLabelKey(s string) map[string]string {
	out := map[string]string{}
	if len(s) < 2 || s[0] != '{' || s[len(s)-1] != '}' {
		return out
	}
	inner := s[1 : len(s)-1]
	if inner == "" {
		return out
	}
	for _, pair := range strings.Split(inner, ",") {
		eq := strings.IndexByte(pair, '=')
		if eq < 0 {
			continue
		}
		k := pair[:eq]
		v := pair[eq+1:]
		// v 形如 "..."（带引号），strconv.Unquote 反转 %q 的转义。
		// 反转失败时降级为剥离首尾引号，避免单条坏值拖垮整个快照。
		if unq, err := strconv.Unquote(v); err == nil {
			out[k] = unq
		} else {
			out[k] = strings.Trim(v, `"`)
		}
	}
	return out
}

// histQuantile 从累计桶计数估算分位。
//
// counts[i] = #obs <= bounds[i]（累计计数，与 durationBucketsSec 同长）；
// total = 总观测数。线性插值：找到首个 counts[i] >= q*total 的桶 i，
// 在 (lower, upper) 区间内按观测占比插值。
func histQuantile(counts []uint64, bounds []float64, total uint64, q float64) float64 {
	if total == 0 || len(counts) == 0 {
		return 0
	}
	target := q * float64(total)
	i := 0
	for ; i < len(counts); i++ {
		if float64(counts[i]) >= target {
			break
		}
	}
	// 找不到（q 超过最高累计，理论上不会，因 counts 末位应 == total），
	// 返回最大边界作为保守估计。
	if i == len(counts) {
		return bounds[len(bounds)-1]
	}
	var lower float64
	var prevCount uint64
	if i == 0 {
		lower = 0
	} else {
		lower = bounds[i-1]
		prevCount = counts[i-1]
	}
	upper := bounds[i]
	bucketTotal := counts[i] - prevCount
	if bucketTotal == 0 {
		return upper
	}
	return lower + (upper-lower)*(target-float64(prevCount))/float64(bucketTotal)
}
