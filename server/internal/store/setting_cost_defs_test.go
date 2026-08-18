package store

import "testing"

// TestCostConversionSettingDefaults 固化渠道成本 usd 折算依赖的两个设置项默认值，
// 与 docs/glossary.md（usd_cny_rate_milli 7200、全局兑换率 1 人民币 = 1,000,000 积分）保持一致。
func TestCostConversionSettingDefaults(t *testing.T) {
	rate := settingDef("usd_cny_rate_milli")
	if rate == nil {
		t.Fatal("缺少设置项 usd_cny_rate_milli")
	}
	if rate.Kind != SettingInt64 {
		t.Errorf("usd_cny_rate_milli Kind 应为 int64，实际 %s", rate.Kind)
	}
	if rate.Default != int64(7200) {
		t.Errorf("usd_cny_rate_milli 默认值应为 7200（7.200 CNY/USD，见 docs/glossary.md），实际 %v", rate.Default)
	}

	exchange := settingDef("exchange_rate_credits_per_cny")
	if exchange == nil {
		t.Fatal("缺少设置项 exchange_rate_credits_per_cny")
	}
	if exchange.Kind != SettingInt64 {
		t.Errorf("exchange_rate_credits_per_cny Kind 应为 int64，实际 %s", exchange.Kind)
	}
	if exchange.Default != int64(1_000_000) {
		t.Errorf("exchange_rate_credits_per_cny 默认值应为 1,000,000（1 人民币 = 1,000,000 积分），实际 %v", exchange.Default)
	}
}
