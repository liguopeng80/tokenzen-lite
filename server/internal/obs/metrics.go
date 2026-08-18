package obs

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// 指标体系的目的是回答「有没有变坏、从什么时候开始、影响哪部分」，
// 这三个问题靠日志逐条翻是答不出来的。输出为 Prometheus 文本格式，
// 不引入客户端库：需要的只是计数器与直方图两种结构，自己实现比多一个依赖划算。

// durationBucketsSec 是请求耗时直方图的分桶边界（秒）。
// 上界取到 120 秒：大模型的长回答与流式请求常以十秒计，桶太密集在这里没有意义，
// 太稀疏又看不出「从 2 秒劣化到 8 秒」这类变化。
var durationBucketsSec = []float64{0.05, 0.1, 0.25, 0.5, 1, 2, 5, 10, 30, 60, 120}

// Recorder 是指标记录的最小行为契约：计数器累加（Inc/Add）、耗时观测（Observe）、
// 仪表登记（SetGaugeFunc）。
//
// 抽成接口的目的是让 obs 包外的调用方（relay/api/billing）在重构后能在自己的测试里
// 注入 fake Recorder，而不必依赖进程级单件 defaultMetrics——后者使跨包测试脆弱、
// 并行用例互相污染。*Metrics 实现此接口；包级 Record* 函数仍委托 defaultMetrics，
// 调用方按需在自己的构造函数里接收 Recorder 即可解耦。
type Recorder interface {
	Inc(name string, labels ...string)
	Add(name string, delta float64, labels ...string)
	Observe(name string, seconds float64, labels ...string)
	SetGaugeFunc(name, help string, f func() float64)
}

// 编译期断言：*Metrics 满足 Recorder 接口。
var _ Recorder = (*Metrics)(nil)

// Metrics 是进程内的指标集合。计数与观测在请求路径上执行，因此全部操作
// 只做一次加锁的 map 写入，不做任何 I/O。
//
// 作用域是单进程内存态：进程重启后计数归零，与限流、渠道失败计数的口径一致。
// 单机部署下这不构成问题；将来若多副本部署，抓取端按实例聚合即可。
type Metrics struct {
	mu         sync.Mutex
	counters   map[string]map[string]float64   // 指标名 → 标签串 → 值
	histograms map[string]map[string]*histLine // 指标名 → 标签串 → 分布
	gauges     map[string]func() float64       // 指标名 → 取值函数（无标签）
	helps      map[string]string
}

type histLine struct {
	counts []uint64 // 各桶的累计计数（与 durationBucketsSec 同长，末位为 +Inf 之外的溢出）
	sum    float64
	total  uint64
}

// NewMetrics 创建指标集合。
func NewMetrics() *Metrics {
	return &Metrics{
		counters:   map[string]map[string]float64{},
		histograms: map[string]map[string]*histLine{},
		gauges:     map[string]func() float64{},
		helps:      map[string]string{},
	}
}

// Describe 登记指标的说明文本，随导出输出为 HELP 行。
func (m *Metrics) Describe(name, help string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.helps[name] = help
	m.mu.Unlock()
}

// Inc 为计数器加一。labels 为「键, 值, 键, 值」的扁平序列。
func (m *Metrics) Inc(name string, labels ...string) {
	m.Add(name, 1, labels...)
}

// Add 为计数器累加指定值。
func (m *Metrics) Add(name string, delta float64, labels ...string) {
	if m == nil {
		return
	}
	key := labelKey(labels)
	m.mu.Lock()
	series, ok := m.counters[name]
	if !ok {
		series = map[string]float64{}
		m.counters[name] = series
	}
	series[key] += delta
	m.mu.Unlock()
}

// Observe 记录一次耗时观测（秒）。
func (m *Metrics) Observe(name string, seconds float64, labels ...string) {
	if m == nil {
		return
	}
	key := labelKey(labels)
	m.mu.Lock()
	series, ok := m.histograms[name]
	if !ok {
		series = map[string]*histLine{}
		m.histograms[name] = series
	}
	line, ok := series[key]
	if !ok {
		line = &histLine{counts: make([]uint64, len(durationBucketsSec))}
		series[key] = line
	}
	for i, bound := range durationBucketsSec {
		if seconds <= bound {
			line.counts[i]++
		}
	}
	line.sum += seconds
	line.total++
	m.mu.Unlock()
}

// SetGaugeFunc 登记一个即时取值的仪表。取值函数在导出时调用，
// 因此适合并发数、队列长度这类「当下是多少」的量。
func (m *Metrics) SetGaugeFunc(name, help string, f func() float64) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.gauges[name] = f
	m.helps[name] = help
	m.mu.Unlock()
}

// Export 输出 Prometheus 文本格式。
func (m *Metrics) Export() string {
	if m == nil {
		return ""
	}
	m.mu.Lock()
	// 先复制出快照再释放锁：gauge 的取值函数可能访问其它组件的锁，
	// 在持有本锁时调用会引入锁序依赖。
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
	gauges := make(map[string]func() float64, len(m.gauges))
	for name, f := range m.gauges {
		gauges[name] = f
	}
	helps := make(map[string]string, len(m.helps))
	for name, h := range m.helps {
		helps[name] = h
	}
	m.mu.Unlock()

	var b strings.Builder
	for _, name := range sortedKeys(counters) {
		writeHelp(&b, name, helps[name], "counter")
		for _, key := range sortedKeysFloat(counters[name]) {
			fmt.Fprintf(&b, "%s%s %s\n", name, key, formatValue(counters[name][key]))
		}
	}
	for _, name := range sortedKeysHist(hists) {
		writeHelp(&b, name, helps[name], "histogram")
		for _, key := range sortedKeysHistLine(hists[name]) {
			line := hists[name][key]
			for i, bound := range durationBucketsSec {
				fmt.Fprintf(&b, "%s_bucket%s %d\n",
					name, withLabel(key, "le", strconv.FormatFloat(bound, 'g', -1, 64)), line.counts[i])
			}
			fmt.Fprintf(&b, "%s_bucket%s %d\n", name, withLabel(key, "le", "+Inf"), line.total)
			fmt.Fprintf(&b, "%s_sum%s %s\n", name, key, formatValue(line.sum))
			fmt.Fprintf(&b, "%s_count%s %d\n", name, key, line.total)
		}
	}
	for _, name := range sortedGaugeKeys(gauges) {
		writeHelp(&b, name, helps[name], "gauge")
		fmt.Fprintf(&b, "%s %s\n", name, formatValue(gauges[name]()))
	}
	return b.String()
}

func writeHelp(b *strings.Builder, name, help, kind string) {
	if help != "" {
		fmt.Fprintf(b, "# HELP %s %s\n", name, help)
	}
	fmt.Fprintf(b, "# TYPE %s %s\n", name, kind)
}

func formatValue(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// labelKey 把扁平的标签序列拼成 Prometheus 的标签串，键按字典序排列，
// 保证同一组标签无论传入顺序如何都落在同一条时间序列上。
func labelKey(labels []string) string {
	if len(labels) < 2 {
		return ""
	}
	pairs := make([]string, 0, len(labels)/2)
	for i := 0; i+1 < len(labels); i += 2 {
		pairs = append(pairs, fmt.Sprintf("%s=%q", labels[i], escapeLabel(labels[i+1])))
	}
	sort.Strings(pairs)
	return "{" + strings.Join(pairs, ",") + "}"
}

// withLabel 在既有标签串上追加一个标签（直方图的 le）。
func withLabel(key, name, value string) string {
	pair := fmt.Sprintf("%s=%q", name, value)
	if key == "" {
		return "{" + pair + "}"
	}
	return key[:len(key)-1] + "," + pair + "}"
}

func escapeLabel(v string) string {
	v = strings.ReplaceAll(v, "\\", "\\\\")
	v = strings.ReplaceAll(v, "\n", " ")
	return v
}

func sortedKeys(m map[string]map[string]float64) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedKeysFloat(m map[string]float64) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedKeysHist(m map[string]map[string]histLine) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedKeysHistLine(m map[string]histLine) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedGaugeKeys(m map[string]func() float64) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
