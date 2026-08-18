package pricing

import (
	"testing"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
)

// TestComputeCostCredits 锁定成本侧组合纯函数 ComputeCostCredits 的行为：
//   - token 类走 CalcTokenCredits（向上取整），按次类走 CalcPerCallCredits；
//   - 不施加时段倍率（BaseMultiplierPercent）；
//   - currency == USD 时叠加 ConvertMicroUSDTToCredits，与收入侧同向取整；
//   - currency != USD 时直接返回积分，不做币种折算。
//
// 本函数是 C3 设计（computeCost 下沉）的纯函数载体，relay.computeCost 仅做 IO。
func TestComputeCostCredits(t *testing.T) {
	const rateMilli = 7200     // 7.200 CNY/USD
	const exchange = 1_000_000 // 1 CNY = 1,000,000 积分

	// token 类单价（积分 / 1M tokens）：input 1 积分/M，output 2 积分/M
	tokenPrice := Price{InputPrice: 1_000_000, OutputPrice: 2_000_000}
	// USD 记账的同等官价（微美元），折算后应与积分单价相同量级
	usdPrice := Price{InputPrice: 1_000_000, OutputPrice: 2_000_000}

	cases := []struct {
		name     string
		usage    baseUsage
		price    Price
		currency string
		want     int64
	}{
		{
			name:     "credits 币种 token 计费，3 input + 1 output",
			usage:    baseUsage{input: 3, output: 1},
			price:    tokenPrice,
			currency: "credits",
			// (3×1e6 + 1×2e6) / 1e6 = 5
			want: 5,
		},
		{
			name:     "USD 币种 token 计费，折算后量纲不变（rate=7200, exchange=1e6）",
			usage:    baseUsage{input: 3, output: 1},
			price:    usdPrice,
			currency: "usd",
			// token 算出 5 积分（注意此处 5 积分实为微美元量纲）→ 折算：
			// 5 × 7200 × 1e6 / 1e9 = 36 积分
			want: 36,
		},
		{
			name:     "按次计费（CallCount > 0），PerCallPrice=1000 → 1000 积分",
			usage:    baseUsage{callCount: 2},
			price:    Price{PerCallPrice: 500},
			currency: "credits",
			// 2 × 500 = 1000
			want: 1000,
		},
		{
			name:     "未知 currency 按 credits 处理（不折算）",
			usage:    baseUsage{input: 1, output: 0},
			price:    Price{InputPrice: 1_000_000},
			currency: "???",
			want:     1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			usage := tc.usage.normalized()
			got := ComputeCostCredits(usage, tc.price, tc.currency, rateMilli, exchange)
			if got != tc.want {
				t.Errorf("ComputeCostCredits(...) = %d, want %d", got, tc.want)
			}
		})
	}
}

// TestComputeCostCreditsUSDConversion 验证 USD 折算与 ConvertMicroUSDTToCredits
// 直接调用等价（取整同向、零/负输入返回 0）。
func TestComputeCostCreditsUSDConversion(t *testing.T) {
	const rateMilli = 7200
	const exchange = 1_000_000
	price := Price{InputPrice: 1_000_000}
	// 1 input token × 1e6 单价 / 1e6 = 1 微美元（积分量纲）
	// → ConvertMicroUSDTToCredits(1, 7200, 1e6) = ceil(1×7200×1e6 / 1e9) = ceil(7.2) = 8
	usage := baseUsage{input: 1}.normalized()
	got := ComputeCostCredits(usage, price, "usd", rateMilli, exchange)
	if got != 8 {
		t.Errorf("USD 小额向上取整：got %d, want 8", got)
	}

	// 零用量：CalcTokenCredits 返回 0，ConvertMicroUSDTToCredits(0, ...) 返回 0
	zero := baseUsage{}.normalized()
	if got := ComputeCostCredits(zero, price, "usd", rateMilli, exchange); got != 0 {
		t.Errorf("零用量应返回 0，got %d", got)
	}

	// 汇率未配置（0）：ConvertMicroUSDTToCredits 短路返回 0，不 panic
	if got := ComputeCostCredits(usage, price, "usd", 0, exchange); got != 0 {
		t.Errorf("汇率未配置应返回 0，got %d", got)
	}
}

// baseUsage 是测试用 Usage 构造助手，避免每个用例重复 NormalizedUsage 字面量。
type baseUsage struct {
	input     int64
	output    int64
	cacheRead int64
	callCount int64
}

func (b baseUsage) normalized() domain.NormalizedUsage {
	return domain.NormalizedUsage{
		BaseInput: b.input, Output: b.output,
		CacheRead: b.cacheRead, CallCount: b.callCount,
	}
}
