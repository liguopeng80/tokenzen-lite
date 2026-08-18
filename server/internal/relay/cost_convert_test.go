package relay

import (
	"testing"

	"github.com/liguopeng80/tokenzen-lite/server/internal/pricing"
)

// TestConvertMicroUSDTToCredits 验证微美元 → 积分的折算口径（docs/glossary.md CostCurrency 节）：
// 微美元 × usd_cny_rate_milli / 1000 = 微人民币，微人民币 × exchange_rate_credits_per_cny / 1e6 = 积分。
// 取整方向与计费（CalcTokenCredits、ConvertUSDToCredits）一致：向上取整，运营方不因取整倒亏。
//
// 本函数是成本侧折算（中继 computeCost），与收入侧 pricing.ConvertUSDToCredits 共用
// pricing.ceilMicroUSDRatio，保证成本/收入报表同向取整，利润不失真。
func TestConvertMicroUSDTToCredits(t *testing.T) {
	const rateMilli = 7200     // 7.200 CNY/USD
	const exchange = 1_000_000 // 1 CNY = 1,000,000 积分

	cases := []struct {
		name     string
		usdMicro int64
		want     int64
	}{
		{"1 美元（1,000,000 微美元）→ 7,200,000 积分", 1_000_000, 7_200_000},
		{"零成本", 0, 0},
		// 1 微美元 × 7200 × 1e6 / 1e9 = 7.2 积分 → 向上取整为 8。
		// 旧实现（int64 截断除法）取 7，与计费方向相反，已弃用。
		{"小额向上取整：1 微美元 → 8 积分", 1, 8},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := pricing.ConvertMicroUSDTToCredits(tc.usdMicro, rateMilli, exchange)
			if got != tc.want {
				t.Errorf("ConvertMicroUSDTToCredits(%d, %d, %d) = %d，期望 %d",
					tc.usdMicro, rateMilli, exchange, got, tc.want)
			}
		})
	}
}

// TestConvertMicroUSDTToCreditsBoundary 补充边界用例：零输入、汇率未配置（0）、
// 非整除向上取整方向、大额输入中间值接近 int64 上界但不溢出。
func TestConvertMicroUSDTToCreditsBoundary(t *testing.T) {
	cases := []struct {
		name      string
		usdMicro  int64
		rateMilli int64
		exchange  int64
		want      int64
	}{
		{"零微美元返回 0", 0, 7200, 1_000_000, 0},
		{"usd_cny_rate_milli 为 0（未配置）返回 0 不 panic", 1_000_000, 0, 1_000_000, 0},
		{"exchange_rate_credits_per_cny 为 0（未配置）返回 0 不 panic", 1_000_000, 7200, 0, 0},
		// 1234 微美元 × 7200 / 1000 = 8884.8 微人民币，× 1e6 / 1e6 = 8884.8 积分
		// → 向上取整为 8885。与收入侧 ConvertUSDToCredits 同向。
		{"非整除输入向上取整", 1234, 7200, 1_000_000, 8885},
		// 1e12 微美元（100 万美元）：结果 7.2e12 积分，中间乘积 7.2e18 接近 int64 上界但不溢出。
		{"大额输入不溢出", 1_000_000_000_000, 7200, 1_000_000, 7_200_000_000_000},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := pricing.ConvertMicroUSDTToCredits(tc.usdMicro, tc.rateMilli, tc.exchange)
			if got != tc.want {
				t.Errorf("ConvertMicroUSDTToCredits(%d, %d, %d) = %d，期望 %d",
					tc.usdMicro, tc.rateMilli, tc.exchange, got, tc.want)
			}
		})
	}
}
