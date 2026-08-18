package api

import (
	"encoding/json"
	"testing"
)

// channelPayload 覆盖字段的指针语义（无 DB 单元测试）：
// 字段缺席 / 显式 null → 指针为 nil（保持原值）；
// 显式 {} → 指针非 nil 且指向空 map（显式清空）；
// 携带键值 → 指针非 nil 且内容逐键正确。
func TestChannelPayloadOverridePointerSemantics(t *testing.T) {
	base := `"name":"n","provider":"zhipu","protocol":"openai_compat",` +
		`"base_url":"http://u","models":["m"]`

	cases := []struct {
		name string
		body string
		// 期望：nil 表示指针应为 nil；非 nil 表示指针非 nil 且内容逐键相等
		wantParam  *map[string]any
		wantHeader *map[string]string
	}{
		{
			name:       "U1 字段缺席指针为 nil",
			body:       `{` + base + `}`,
			wantParam:  nil,
			wantHeader: nil,
		},
		{
			name:       "U2 显式 null 与缺席同义",
			body:       `{` + base + `,"param_override":null,"header_override":null}`,
			wantParam:  nil,
			wantHeader: nil,
		},
		{
			name:       "U3 显式空对象为非 nil 空 map",
			body:       `{` + base + `,"param_override":{},"header_override":{}}`,
			wantParam:  &map[string]any{},
			wantHeader: &map[string]string{},
		},
		{
			name: "U4 携带键值逐键正确",
			body: `{` + base + `,"param_override":{"temperature":0.5,"max_tokens":100},` +
				`"header_override":{"X-Custom":"v"}}`,
			wantParam:  &map[string]any{"temperature": 0.5, "max_tokens": float64(100)},
			wantHeader: &map[string]string{"X-Custom": "v"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var p channelPayload
			if err := json.Unmarshal([]byte(tc.body), &p); err != nil {
				t.Fatalf("反序列化失败: %v", err)
			}

			if tc.wantParam == nil {
				if p.ParamOverride != nil {
					t.Errorf("param_override 指针应为 nil，实际 %v", *p.ParamOverride)
				}
			} else {
				if p.ParamOverride == nil {
					t.Fatal("param_override 指针不应为 nil")
				}
				got := *p.ParamOverride
				if got == nil {
					t.Fatal("param_override 应指向非 nil map")
				}
				want := *tc.wantParam
				if len(got) != len(want) {
					t.Errorf("param_override 键数不符: 期望 %d 实际 %d (%v)", len(want), len(got), got)
				}
				for k, v := range want {
					if got[k] != v {
						t.Errorf("param_override[%s] 期望 %v 实际 %v", k, v, got[k])
					}
				}
			}

			if tc.wantHeader == nil {
				if p.HeaderOverride != nil {
					t.Errorf("header_override 指针应为 nil，实际 %v", *p.HeaderOverride)
				}
			} else {
				if p.HeaderOverride == nil {
					t.Fatal("header_override 指针不应为 nil")
				}
				got := *p.HeaderOverride
				if got == nil {
					t.Fatal("header_override 应指向非 nil map")
				}
				want := *tc.wantHeader
				if len(got) != len(want) {
					t.Errorf("header_override 键数不符: 期望 %d 实际 %d (%v)", len(want), len(got), got)
				}
				for k, v := range want {
					if got[k] != v {
						t.Errorf("header_override[%s] 期望 %q 实际 %q", k, v, got[k])
					}
				}
			}
		})
	}
}
