package obs

// 本文件名带 _test.go 后缀，仅测试构建可见，Reset 不会进入生产二进制。

// Reset 清空 defaultMetrics 的全部计数器、直方图与仪表登记，使每个用例从零计数开始，
// 互不污染。helps（Describe 登记的 HELP 文本）保留——它是启动期的静态说明，与计数无关。
//
// 用法：在用到包级 Record* / defaultMetrics 的用例里 `t.Cleanup(Reset)` 或在用例开头调用，
// 即可隔离其它用例留下的计数。注意 defaultMetrics 是进程级单件，并行用例（t.Parallel）
// 仍会互相干扰，需要并行的场景应改用本地 NewMetrics() 实例。
func Reset() {
	defaultMetrics.mu.Lock()
	defer defaultMetrics.mu.Unlock()
	defaultMetrics.counters = map[string]map[string]float64{}
	defaultMetrics.histograms = map[string]map[string]*histLine{}
	defaultMetrics.gauges = map[string]func() float64{}
}
