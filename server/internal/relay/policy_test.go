package relay

import "testing"

// 有效模型集合 = 部门策略 ∩ 用户策略 ∩ 密钥白名单，各层只能收窄不能放宽。
func TestAllowsModelIntersection(t *testing.T) {
	cases := []struct {
		name                  string
		department, user, key string
		model                 string
		want                  bool
	}{
		{name: "三层均未配置则全部放行", model: "gpt-5", want: true},
		{name: "仅密钥收窄且命中", key: `["gpt-5"]`, model: "gpt-5", want: true},
		{name: "仅密钥收窄未命中", key: `["gpt-5"]`, model: "claude-5", want: false},
		{name: "部门收窄未命中则拒绝", department: `["gpt-5"]`, model: "claude-5", want: false},
		{name: "用户收窄未命中则拒绝", user: `["gpt-5"]`, model: "claude-5", want: false},
		{
			name:       "密钥列出但部门未列出仍拒绝——下层不能放宽上层",
			department: `["gpt-5"]`, key: `["claude-5"]`, model: "claude-5", want: false,
		},
		{
			name:       "三层同时命中才放行",
			department: `["gpt-5","claude-5"]`, user: `["gpt-5"]`, key: `["gpt-5"]`,
			model: "gpt-5", want: true,
		},
		{
			name: "用户层收窄到密钥未覆盖的模型则拒绝",
			user: `["gpt-5"]`, key: `["claude-5"]`, model: "gpt-5", want: false,
		},
		{name: "空数组表示该层不施加限制", department: `[]`, key: `[]`, model: "gpt-5", want: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := AllowsModel([]byte(c.department), []byte(c.user), []byte(c.key), c.model)
			if err != nil {
				t.Fatalf("非预期错误: %v", err)
			}
			if got != c.want {
				t.Fatalf("期望 %v，实际 %v", c.want, got)
			}
		})
	}
}

// 策略内容无法解析时必须返回错误由调用方拒绝，而不是静默放行——
// 放行等于配置写错就失效，与设置白名单的意图相反。
func TestAllowsModelRejectsMalformedPolicy(t *testing.T) {
	cases := map[string][3]string{
		"部门层写成对象":   {`{"models":["gpt-5"]}`, "", ""},
		"用户层写成字符串":  {"", `"gpt-5"`, ""},
		"密钥层写成数字数组": {"", "", `[1,2]`},
	}
	for name, layers := range cases {
		t.Run(name, func(t *testing.T) {
			allowed, err := AllowsModel([]byte(layers[0]), []byte(layers[1]), []byte(layers[2]), "gpt-5")
			if err == nil {
				t.Fatal("期望返回解析错误，实际无错误")
			}
			if allowed {
				t.Fatal("解析失败时不得放行")
			}
		})
	}
}
