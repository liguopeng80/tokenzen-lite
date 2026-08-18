package pricing

import "testing"

// TestConvertUSDToCredits 覆盖美元官价到积分单价的折算。
// 业务后果：折算错一个数量级，全站模型的扣费就整体偏离一个数量级。
func TestConvertUSDToCredits(t *testing.T) {
	const rate7200 = 7200           // 1 美元 = 7.2 人民币
	const credits1M = 1_000_000     // 1 人民币 = 1,000,000 积分
	const usd3 = 3 * microUSDPerUSD // $3.00 / 1M tokens

	cases := []struct {
		name          string
		microUSD      int64
		rateMilli     int64
		creditsPerCNY int64
		markupPercent int
		want          int64
	}{
		// $3 × 7.2 = 21.6 元 = 21,600,000 积分
		{"平价折算", usd3, rate7200, credits1M, 100, 21_600_000},
		// 加价 30%：21.6 × 1.3 = 28.08 元
		{"加价三成", usd3, rate7200, credits1M, 130, 28_080_000},
		// $0.075（Statuspage 常见的三位小数价）× 7.2 = 0.54 元
		{"三位小数官价", 75_000, rate7200, credits1M, 100, 540_000},
		// 除不尽时向上取整：1 微美元 × 7.2 × 1e6 / 1e6 = 7.2 积分 → 8
		{"除不尽向上取整", 1, rate7200, credits1M, 100, 8},
		{"官价为零返回零", 0, rate7200, credits1M, 100, 0},
		{"负官价返回零", -1, rate7200, credits1M, 100, 0},
		{"加价百分数为零返回零", usd3, rate7200, credits1M, 0, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ConvertUSDToCredits(c.microUSD, c.rateMilli, c.creditsPerCNY, c.markupPercent)
			if got != c.want {
				t.Errorf("ConvertUSDToCredits(%d, %d, %d, %d) = %d，期望 %d",
					c.microUSD, c.rateMilli, c.creditsPerCNY, c.markupPercent, got, c.want)
			}
		})
	}
}

// TestConvertUSDToCreditsNoInt64Overflow 中间乘积远超 int64 时仍给出正确结果。
// 缺陷成因：若用 int64 直接连乘，$100 官价的中间积约 1.4e20 会溢出成负数，
// 导入后模型单价变成负值或极小值，调用近乎免费。
func TestConvertUSDToCreditsNoInt64Overflow(t *testing.T) {
	got := ConvertUSDToCredits(100*microUSDPerUSD, 7200, 1_000_000, 200)
	// $100 × 7.2 × 2 = 1440 元 = 1,440,000,000 积分
	if want := int64(1_440_000_000); got != want {
		t.Errorf("大额官价折算 = %d，期望 %d", got, want)
	}
	if got <= 0 {
		t.Fatal("折算结果为非正数，说明中间乘积溢出")
	}
}
