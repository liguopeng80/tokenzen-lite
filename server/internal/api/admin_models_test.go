package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
)

// validateModelPayload 名称校验回归：更新契约要求请求必须携带与现值一致的 name，
// 名称为空或超长应在进入名称不可变守卫之前被拒绝。
func TestValidateModelPayloadName(t *testing.T) {
	cases := []struct {
		label   string
		payload modelPayload
		wantErr bool
	}{
		{"名称为空", modelPayload{Name: ""}, true},
		{"名称超 128 字符", modelPayload{Name: strings.Repeat("a", 129)}, true},
		{"名称恰 128 字符", modelPayload{Name: strings.Repeat("a", 128)}, false},
		{"合法名称", modelPayload{Name: "gpt-4o"}, false},
	}
	for _, c := range cases {
		t.Run(c.label, func(t *testing.T) {
			p := c.payload
			msg := validateModelPayload(&p)
			if c.wantErr {
				if msg == "" {
					t.Fatalf("期望返回校验错误，实际放行")
				}
				if !strings.Contains(msg, "模型名称") {
					t.Errorf("错误消息应指明模型名称问题，实际: %q", msg)
				}
				return
			}
			if msg != "" {
				t.Errorf("期望放行，实际返回错误: %q", msg)
			}
		})
	}
}

// TestAdminListModelsFilterByProvider 按厂商过滤：?provider=xxx 只返回该厂商模型。
func TestAdminListModelsFilterByProvider(t *testing.T) {
	e := newTestEnv(t)
	adminC := e.seedAndLogin(t, "pf-admin", domain.RoleAdmin)

	e.seedModelWithProvider(t, "pf-openai-model", domain.ProviderOpenAI)
	e.seedModelWithProvider(t, "pf-zhipu-model", domain.ProviderZhipu)

	resp, env := e.do(t, adminC, "GET", "/api/admin/models/?provider=openai", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("列表应 200，实际 %d %v", resp.StatusCode, env)
	}
	data, _ := env["data"].(map[string]any)
	items, _ := data["items"].([]any)
	foundOpenAI, sawOther := false, false
	for _, it := range items {
		m, _ := it.(map[string]any)
		if m["provider"] != string(domain.ProviderOpenAI) {
			sawOther = true
		}
		switch m["name"] {
		case "pf-openai-model":
			foundOpenAI = true
		case "pf-zhipu-model":
			sawOther = true
		}
	}
	if !foundOpenAI {
		t.Errorf("应返回 openai 模型 pf-openai-model，items=%v", items)
	}
	if sawOther {
		t.Errorf("不应返回非 openai 模型，items=%v", items)
	}
}
