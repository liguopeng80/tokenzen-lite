package pricing

import (
	"math"
	"testing"
	"time"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
)

// 表驱动穷举：各 token 类型组合 × 倍率 × 取整边界。
func TestCalcTokenCredits(t *testing.T) {
	// 单价：input 300万积分/1M（≈3 元/1M）、output 1500万积分/1M
	p := Price{
		InputPrice: 3_000_000, OutputPrice: 15_000_000,
		CacheReadPrice: 300_000, CacheWritePrice: 3_750_000,
		AudioInputPrice: 10_000_000, AudioOutputPrice: 20_000_000,
	}
	cases := []struct {
		name string
		u    domain.NormalizedUsage
		mult int
		want int64
	}{
		{"零用量", domain.NormalizedUsage{}, 100, 0},
		{"纯输入 1M token", domain.NormalizedUsage{BaseInput: 1_000_000}, 100, 3_000_000},
		{"输入+输出", domain.NormalizedUsage{BaseInput: 1000, Output: 2000}, 100,
			// (1000×3e6 + 2000×15e6) / 1e6 = 3000 + 30000
			33_000},
		{"缓存读写", domain.NormalizedUsage{CacheRead: 10_000, CacheWrite: 4000}, 100,
			// (10000×3e5 + 4000×3.75e6)/1e6 = 3000 + 15000
			18_000},
		{"音频", domain.NormalizedUsage{AudioInput: 500, AudioOutput: 250}, 100,
			// (500×1e7 + 250×2e7)/1e6 = 5000 + 5000
			10_000},
		{"全字段", domain.NormalizedUsage{
			BaseInput: 1000, CacheRead: 10_000, CacheWrite: 4000,
			Output: 2000, AudioInput: 500, AudioOutput: 250}, 100,
			61_000},
		{"取整向上：1 token 也要扣 1 积分", domain.NormalizedUsage{BaseInput: 1}, 100,
			// 3e6/1e6 = 3 整除 → 3
			3},
		{"取整向上：不足 1 积分进位", domain.NormalizedUsage{CacheRead: 1}, 100,
			// 3e5/1e6 = 0.3 → 1
			1},
		{"1.5 倍时段", domain.NormalizedUsage{BaseInput: 1000, Output: 2000}, 150,
			// 33000 × 1.5 = 49500
			49_500},
		{"3 倍时段", domain.NormalizedUsage{BaseInput: 1_000_000}, 300, 9_000_000},
		{"倍率取整向上", domain.NormalizedUsage{CacheRead: 1}, 150,
			// 3e5×150 / (100×1e6) = 0.45 → 1
			1},
		{"非法倍率低于 100 按 100 处理", domain.NormalizedUsage{BaseInput: 1000}, 50, 3000},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := CalcTokenCredits(tc.u, p, tc.mult)
			if got != tc.want {
				t.Errorf("期望 %d 积分，实际 %d", tc.want, got)
			}
		})
	}
}

func TestCalcPerCallCredits(t *testing.T) {
	p := Price{PerCallPrice: 40_000} // 每次 4 万积分（≈0.04 元）
	cases := []struct {
		name  string
		count int64
		mult  int
		want  int64
	}{
		{"单次", 1, 100, 40_000},
		{"多次", 3, 100, 120_000},
		{"高峰 2 倍", 2, 200, 160_000},
		{"零次", 0, 100, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := CalcPerCallCredits(tc.count, p, tc.mult); got != tc.want {
				t.Errorf("期望 %d，实际 %d", tc.want, got)
			}
		})
	}
}

// TestCalcTokenCreditsOverflow 锁定 int64 中间积溢出漏洞。
//
// 缺陷：旧 CalcTokenCredits 中 raw*multiplierPercent（6 项 token×price 之和再乘倍率）
// 在大用量下溢出为负，被 ceilDiv 的 a<=0 短路成 0，连锁绕过日上限、密钥额度、余额拒绝
// 三道边界，零余额用户可免费请求。攻击向量：max_tokens 由下游可控且 pipeline 无上界校验，
// 放大 u.Output 即可触发。
//
// 修复后：用 math/big.Int 累计分子永不溢出；最终结果若仍超 int64 上限则饱和到
// math.MaxInt64（预扣自然失败于余额不足），否则返回正确的整数结果。
func TestCalcTokenCreditsOverflow(t *testing.T) {
	// Opus 类模型的预设单价：OutputUSD=$75/1M × 7200 汇率 ÷ 1000 = 540_000_000 积分/1M。
	// 这是评审中实证可触发溢出的真实价目。
	const opusOutputPrice = 540_000_000

	cases := []struct {
		name string
		u    domain.NormalizedUsage
		p    Price
		mult int
		want int64
	}{
		{
			// 中间积溢出但最终结果仍落在 int64 内。
			// raw = 2e8 × 5.4e8 = 1.08e17；scaled = raw × 100 = 1.08e19（溢出 int64 < 9.22e18）；
			// credits = ceil(1.08e19 / 1e8) = 1.08e11（落在 int64 内）。
			// 旧实现：scaled 溢出为负 → ceilDiv 返回 0（白嫖）；
			// 新实现：返回 1.08e11 = 1080 亿积分（远超任何余额 → 预扣拒绝）。
			name: "中间积溢出旧实现归零新实现返回正确大数",
			u:    domain.NormalizedUsage{Output: 200_000_000},
			p:    Price{OutputPrice: opusOutputPrice},
			mult: 100,
			want: 108_000_000_000,
		},
		{
			// 中间积与最终结果双双溢出 int64（极端单价 × 极端用量）。
			// 新实现：饱和到 math.MaxInt64，预扣自然失败于余额不足。
			name: "最终结果溢出int64饱和到上限",
			u:    domain.NormalizedUsage{Output: 1 << 62},
			p:    Price{OutputPrice: 1 << 62},
			mult: 100,
			want: math.MaxInt64,
		},
		{
			// 高峰倍率叠加放大：相同极端用量在 2 倍峰时下仍饱和到上限。
			name: "高峰倍率下最终结果溢出饱和到上限",
			u:    domain.NormalizedUsage{Output: 1 << 62},
			p:    Price{OutputPrice: 1 << 62},
			mult: 200,
			want: math.MaxInt64,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := CalcTokenCredits(tc.u, tc.p, tc.mult)
			if got != tc.want {
				t.Errorf("期望 %d，实际 %d", tc.want, got)
			}
			if got <= 0 {
				t.Fatal("积分结果非正，溢出→0 漏洞未闭合")
			}
		})
	}
}

// TestCalcPerCallCreditsOverflow 锁定按次计费的同类溢出。
// 旧实现 PerCallPrice*count*multiplierPercent 在大次数/高单价下溢出为负 → ceilDiv 归零。
func TestCalcPerCallCreditsOverflow(t *testing.T) {
	cases := []struct {
		name  string
		count int64
		p     Price
		mult  int
		want  int64
	}{
		{
			// 1e10 × 1e10 × 100 = 1e22 远超 int64；最终结果 1e22 / 100 = 1e20 仍超 int64 → 饱和。
			name:  "乘积与最终结果均溢出饱和到上限",
			count: 10_000_000_000,
			p:     Price{PerCallPrice: 10_000_000_000},
			mult:  100,
			want:  math.MaxInt64,
		},
		{
			// 回归用例：正常用量基线（与 TestCalcPerCallCredits 互为印证）。
			name:  "正常用量基线",
			count: 5,
			p:     Price{PerCallPrice: 40_000},
			mult:  100,
			want:  200_000,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := CalcPerCallCredits(tc.count, tc.p, tc.mult)
			if got != tc.want {
				t.Errorf("期望 %d，实际 %d", tc.want, got)
			}
		})
	}
}

// mustTime 构造带时区的时刻。
func mustTime(t *testing.T, value, tz string) time.Time {
	t.Helper()
	loc, err := time.LoadLocation(tz)
	if err != nil {
		t.Fatalf("加载时区失败: %v", err)
	}
	ts, err := time.ParseInLocation("2006-01-02 15:04", value, loc)
	if err != nil {
		t.Fatalf("解析时间失败: %v", err)
	}
	return ts
}

func TestEvaluatePeakMultiplier(t *testing.T) {
	// 参照生产事实来源：GLM 高峰期每日 14:00–18:00（Asia/Shanghai）
	peak := PeakRule{
		Timezone: "Asia/Shanghai", StartMinute: 14 * 60, EndMinute: 18 * 60,
		DaysOfWeek: []int{1, 2, 3, 4, 5, 6, 7}, MultiplierPercent: 300, Enabled: true,
	}
	weekdayOnly := PeakRule{
		Timezone: "Asia/Shanghai", StartMinute: 9 * 60, EndMinute: 12 * 60,
		DaysOfWeek: []int{1, 2, 3, 4, 5}, MultiplierPercent: 150, Enabled: true,
	}

	cases := []struct {
		name  string
		rules []PeakRule
		at    time.Time
		want  int
	}{
		{"无规则", nil, mustTime(t, "2026-08-05 15:00", "Asia/Shanghai"), 100},
		{"命中高峰", []PeakRule{peak}, mustTime(t, "2026-08-05 15:00", "Asia/Shanghai"), 300},
		{"区间左闭", []PeakRule{peak}, mustTime(t, "2026-08-05 14:00", "Asia/Shanghai"), 300},
		{"区间右开", []PeakRule{peak}, mustTime(t, "2026-08-05 18:00", "Asia/Shanghai"), 100},
		{"高峰前", []PeakRule{peak}, mustTime(t, "2026-08-05 13:59", "Asia/Shanghai"), 100},
		{"禁用规则不生效", []PeakRule{{
			Timezone: "Asia/Shanghai", StartMinute: 0, EndMinute: 1440,
			DaysOfWeek: []int{1, 2, 3, 4, 5, 6, 7}, MultiplierPercent: 500, Enabled: false,
		}}, mustTime(t, "2026-08-05 15:00", "Asia/Shanghai"), 100},
		{"跨时区换算：UTC 07:00 = 北京 15:00 命中",
			[]PeakRule{peak}, mustTime(t, "2026-08-05 07:00", "UTC"), 300},
		{"跨时区换算：UTC 15:00 = 北京 23:00 不命中",
			[]PeakRule{peak}, mustTime(t, "2026-08-05 15:00", "UTC"), 100},
		{"星期过滤：周三命中工作日规则",
			[]PeakRule{weekdayOnly}, mustTime(t, "2026-08-05 10:00", "Asia/Shanghai"), 150},
		{"星期过滤：周日不命中工作日规则",
			[]PeakRule{weekdayOnly}, mustTime(t, "2026-08-09 10:00", "Asia/Shanghai"), 100},
		{"多规则取最大", []PeakRule{weekdayOnly, {
			Timezone: "Asia/Shanghai", StartMinute: 9 * 60, EndMinute: 12 * 60,
			DaysOfWeek: []int{3}, MultiplierPercent: 200, Enabled: true,
		}}, mustTime(t, "2026-08-05 10:00", "Asia/Shanghai"), 200},
		{"非法时区跳过", []PeakRule{{
			Timezone: "Not/AZone", StartMinute: 0, EndMinute: 1440,
			DaysOfWeek: []int{1, 2, 3, 4, 5, 6, 7}, MultiplierPercent: 400, Enabled: true,
		}}, mustTime(t, "2026-08-05 10:00", "Asia/Shanghai"), 100},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := EvaluatePeakMultiplier(tc.rules, tc.at); got != tc.want {
				t.Errorf("期望倍率 %d，实际 %d", tc.want, got)
			}
		})
	}
}

// 跨午夜时段的契约替代路径：拆成 [1380,1440) 与 [0,60) 两条规则后，
// 23:30 与 00:30 均命中、01:00（右开边界）不命中——固化文档「跨午夜须拆两条」语义。
func TestEvaluatePeakMultiplierMidnightSplit(t *testing.T) {
	allWeek := []int{1, 2, 3, 4, 5, 6, 7}
	split := []PeakRule{
		{Timezone: "Asia/Shanghai", StartMinute: 1380, EndMinute: 1440,
			DaysOfWeek: allWeek, MultiplierPercent: 300, Enabled: true},
		{Timezone: "Asia/Shanghai", StartMinute: 0, EndMinute: 60,
			DaysOfWeek: allWeek, MultiplierPercent: 300, Enabled: true},
	}
	cases := []struct {
		name string
		at   time.Time
		want int
	}{
		{"午夜前 23:30 命中前半条", mustTime(t, "2026-08-05 23:30", "Asia/Shanghai"), 300},
		{"午夜后 00:30 命中后半条", mustTime(t, "2026-08-05 00:30", "Asia/Shanghai"), 300},
		{"01:00 恰在后半条右开边界外", mustTime(t, "2026-08-05 01:00", "Asia/Shanghai"), 100},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := EvaluatePeakMultiplier(split, tc.at); got != tc.want {
				t.Errorf("期望倍率 %d，实际 %d", tc.want, got)
			}
		})
	}
}

// 空 DaysOfWeek 规则在求值层永不命中：「空数组表示全周」由写入层（管理 API）
// 归一化为 1-7，求值层不做兜底。绕过 API 写入的空星期规则不得被误认为生效。
func TestEvaluatePeakMultiplierEmptyDaysNeverMatch(t *testing.T) {
	rule := PeakRule{
		Timezone: "Asia/Shanghai", StartMinute: 0, EndMinute: 1440,
		DaysOfWeek: nil, MultiplierPercent: 300, Enabled: true,
	}
	for _, at := range []time.Time{
		mustTime(t, "2026-08-05 10:00", "Asia/Shanghai"), // 周三
		mustTime(t, "2026-08-09 10:00", "Asia/Shanghai"), // 周日
	} {
		if got := EvaluatePeakMultiplier([]PeakRule{rule}, at); got != 100 {
			t.Errorf("空 DaysOfWeek 规则不应命中，%s 实际倍率 %d", at, got)
		}
	}
	rule.DaysOfWeek = []int{}
	if got := EvaluatePeakMultiplier([]PeakRule{rule},
		mustTime(t, "2026-08-05 10:00", "Asia/Shanghai")); got != 100 {
		t.Errorf("空切片 DaysOfWeek 规则不应命中，实际倍率 %d", got)
	}
}

// 2026-08-05 是周三、2026-08-09 是周日——校验测试自身的星期假设。
func TestWeekdayAssumptions(t *testing.T) {
	if d := mustTime(t, "2026-08-05 10:00", "Asia/Shanghai").Weekday(); d != time.Wednesday {
		t.Fatalf("2026-08-05 应为周三，实际 %v", d)
	}
	if d := mustTime(t, "2026-08-09 10:00", "Asia/Shanghai").Weekday(); d != time.Sunday {
		t.Fatalf("2026-08-09 应为周日，实际 %v", d)
	}
}
