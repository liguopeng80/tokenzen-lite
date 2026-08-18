package store

import "testing"

// TestCurrencySymbolSettingDef 固化货币符号设置项的默认值与校验。
// 业务后果：符号为空会让界面金额无符号可显示；过长符号破坏布局。
func TestCurrencySymbolSettingDef(t *testing.T) {
	def := settingDef("currency_symbol")
	if def == nil {
		t.Fatal("缺少设置项 currency_symbol")
	}
	if def.Kind != SettingString {
		t.Errorf("currency_symbol Kind 应为 string，实际 %s", def.Kind)
	}
	if def.Default != "¥" {
		t.Errorf("currency_symbol 默认值应为 ¥，实际 %v", def.Default)
	}
	if def.Validate == nil {
		t.Fatal("currency_symbol 应提供 Validate")
	}
	cases := []struct {
		name string
		v    any
		ok   bool
	}{
		{"默认符号", "¥", true},
		{"美元符号", "$", true},
		{"中文元", "元", true},
		{"空串", "", false},
		{"空白", "  ", false},
		{"过长", "123456789", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := def.Validate(c.v)
			if c.ok && err != nil {
				t.Errorf("Validate(%q) 不应报错，实际 %v", c.v, err)
			}
			if !c.ok && err == nil {
				t.Errorf("Validate(%q) 应报错", c.v)
			}
		})
	}
}
