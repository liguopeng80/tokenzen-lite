package pricing

import "testing"

// TestDetailDecimals 覆盖"单个积分无损可见"所需小数位的推导。
// 业务后果：CSV 与界面明细精度依赖此值，错一个数量级会让小额扣费被截断为 0。
func TestDetailDecimals(t *testing.T) {
	cases := []struct {
		name string
		rate int64
		want int
	}{
		{"默认兑换率 1e6", 1_000_000, 6},
		{"非十的幂 1.5e6", 1_500_000, 7},
		{"百级兑换率", 100, 2},
		{"单位兑换率", 1, 0},
		{"非正兑换率", 0, 0},
		{"七进制极端值", 7, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := DetailDecimals(c.rate); got != c.want {
				t.Errorf("DetailDecimals(%d) = %d，期望 %d", c.rate, got, c.want)
			}
		})
	}
}

// TestCreditsToDecimalString 覆盖积分到货币定点字符串的换算与四舍五入。
// 业务后果：导出 CSV 的金额列必须可精确汇总，默认兑换率下 6 位为无损。
func TestCreditsToDecimalString(t *testing.T) {
	const rate1M = int64(1_000_000)
	cases := []struct {
		name     string
		credits  int64
		rate     int64
		decimals int
		want     string
	}{
		{"大额余额 6 位", 99_931_375, rate1M, 6, "99.931375"},
		{"单积分 6 位", 1, rate1M, 6, "0.000001"},
		{"500 积分补零", 500, rate1M, 6, "0.000500"},
		{"整数货币", 1_000_000, rate1M, 6, "1.000000"},
		{"总额 2 位", 99_931_375, rate1M, 2, "99.93"},
		{"四舍五入进位", 1_250_000, rate1M, 2, "1.25"},
		{"四舍五入进位 2", 1_255_000, rate1M, 2, "1.26"},
		{"负值", -1_500_000, rate1M, 6, "-1.500000"},
		{"零积分", 0, rate1M, 6, "0.000000"},
		{"兑换率非正返回 0", 100, 0, 6, "0"},
		{"负精度返回 0", 100, rate1M, -1, "0"},
		{"百级兑换率", 1, 100, 2, "0.01"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := CreditsToDecimalString(c.credits, c.rate, c.decimals)
			if got != c.want {
				t.Errorf("CreditsToDecimalString(%d, %d, %d) = %q，期望 %q",
					c.credits, c.rate, c.decimals, got, c.want)
			}
		})
	}
}

// TestCreditsToDecimalStringNoOverflow credits × 10^decimals 超 int64 时仍正确。
func TestCreditsToDecimalStringNoOverflow(t *testing.T) {
	// 9e18 积分 ÷ 1e6 = 9e12 货币单位；× 1e6（精度放大）中间积 9e24 超 int64。
	got := CreditsToDecimalString(9_000_000_000_000_000_000, 1_000_000, 6)
	want := "9000000000000.000000"
	if got != want {
		t.Errorf("大额换算 = %q，期望 %q", got, want)
	}
}
