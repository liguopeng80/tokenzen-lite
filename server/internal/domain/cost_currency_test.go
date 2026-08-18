package domain

import "testing"

// TestCostCurrencyValues 固化渠道成本币种枚举取值，
// 与 docs/glossary.md 的 CostCurrency 条目（credits / usd，usd 计量单位为微美元）保持一致，
// 防止文档与代码脱钩的无意回改。
func TestCostCurrencyValues(t *testing.T) {
	if CostCurrencyCredits != "credits" {
		t.Errorf("CostCurrencyCredits 应为 \"credits\"（见 docs/glossary.md），实际 %q", CostCurrencyCredits)
	}
	if CostCurrencyUSD != "usd" {
		t.Errorf("CostCurrencyUSD 应为 \"usd\"（见 docs/glossary.md），实际 %q", CostCurrencyUSD)
	}
}

// TestCostCurrencyValid 覆盖 CostCurrency.Valid 与注册表 CostCurrencies：
// 已注册取值合法、空串与未注册取值非法、注册表与具名常量同源。
func TestCostCurrencyValid(t *testing.T) {
	for _, c := range CostCurrencies {
		if !c.Valid() {
			t.Errorf("注册表内的 %q 应判定为合法", c)
		}
	}
	want := map[CostCurrency]bool{CostCurrencyCredits: true, CostCurrencyUSD: true}
	if len(CostCurrencies) != len(want) {
		t.Fatalf("CostCurrencies 长度应为 %d，实际 %d（%v）", len(want), len(CostCurrencies), CostCurrencies)
	}
	for _, c := range CostCurrencies {
		if !want[c] {
			t.Errorf("CostCurrencies 出现未声明的币种 %q", c)
		}
	}
	for _, bad := range []CostCurrency{"", "CNY", "Credits", "USD", "jpy"} {
		if bad.Valid() {
			t.Errorf("非法币种 %q 不应判定为合法", bad)
		}
	}
}
