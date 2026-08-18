package api

import (
	"context"
	"net/http"
	"strconv"
	"testing"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// TestModelCRUDNewFields 模型 CRUD 读写新字段（provider/context_window/max_output/capabilities/alias）。
// 业务后果：厂商归属、上下文窗口与能力标签是选型展示的核心信息，
// 别名是中继全局短名解析的输入；任一字段写入或读出丢失都会使前端展示或中继解析失效。
func TestModelCRUDNewFields(t *testing.T) {
	e := newTestEnv(t)
	c := e.seedAndLogin(t, "model-fields-admin", domain.RoleAdmin)

	// 创建带全部新字段的模型。
	resp, env := e.do(t, c, "POST", "/api/admin/models/", map[string]any{
		"name": "claude-opus-5", "display_name": "Claude Opus 5",
		"modality": "text", "billing_mode": "per_token",
		"provider": "anthropic", "context_window": 1000000, "max_output": 32000,
		"capabilities": []string{"vision"}, "alias": "opus",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("创建模型应 201，实际 %d %v", resp.StatusCode, env)
	}
	data, _ := env["data"].(map[string]any)
	if provider, _ := data["provider"].(string); provider != "anthropic" {
		t.Errorf("创建响应的 provider 应为 anthropic，实际 %q", provider)
	}
	if alias, _ := data["alias"].(string); alias != "opus" {
		t.Errorf("创建响应的 alias 应为 opus，实际 %q", alias)
	}

	// 读回校验落库值。
	var m store.Model
	if err := e.db.Where("name = ?", "claude-opus-5").First(&m).Error; err != nil {
		t.Fatalf("模型未落库: %v", err)
	}
	if m.Provider != "anthropic" {
		t.Errorf("provider 落库不符：期望 anthropic，实际 %s", m.Provider)
	}
	if m.ContextWindow != 1000000 {
		t.Errorf("context_window 落库不符：期望 1000000，实际 %d", m.ContextWindow)
	}
	if m.MaxOutput != 32000 {
		t.Errorf("max_output 落库不符：期望 32000，实际 %d", m.MaxOutput)
	}
	if m.Alias != "opus" {
		t.Errorf("alias 落库不符：期望 opus，实际 %s", m.Alias)
	}

	// 更新字段（alias 冲突场景）。
	resp, env = e.do(t, c, "POST", "/api/admin/models/", map[string]any{
		"name": "other-model", "modality": "text", "billing_mode": "per_token",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("创建第二个模型应 201，实际 %d %v", resp.StatusCode, env)
	}
	otherData, _ := env["data"].(map[string]any)
	otherID, _ := otherData["id"].(float64)
	if otherID == 0 {
		t.Fatalf("第二个模型创建响应缺少 id: %v", otherData)
	}
	otherIDPath := "/api/admin/models/" + strconv.FormatFloat(otherID, 'f', 0, 64)

	resp, _ = e.do(t, c, "PUT", otherIDPath, map[string]any{
		"name": "other-model", "modality": "text", "billing_mode": "per_token",
		"alias": "opus", // 与第一条冲突
	})
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("别名冲突应 409，实际 %d", resp.StatusCode)
	}

	// 更新为合法新值。
	resp, env = e.do(t, c, "PUT", otherIDPath, map[string]any{
		"name": "other-model", "modality": "text", "billing_mode": "per_token",
		"provider": "openai", "alias": "other",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("合法更新应 200，实际 %d %v", resp.StatusCode, env)
	}
	var m2 store.Model
	if err := e.db.Where("name = ?", "other-model").First(&m2).Error; err != nil {
		t.Fatalf("模型未落库: %v", err)
	}
	if m2.Provider != "openai" || m2.Alias != "other" {
		t.Errorf("更新后 provider=%s alias=%s，期望 openai/other", m2.Provider, m2.Alias)
	}
}

// TestValidateModelPayloadNewFields 新字段的校验规则。
func TestValidateModelPayloadNewFields(t *testing.T) {
	cases := []struct {
		label   string
		payload modelPayload
		wantErr bool
		hint    string
	}{
		{"合法 provider", modelPayload{Name: "m", Provider: "moonshot"}, false, ""},
		{"非法 provider", modelPayload{Name: "m", Provider: "unknown"}, true, "厂商"},
		{"空 provider 合法", modelPayload{Name: "m", Provider: ""}, false, ""},
		{"负 context_window", modelPayload{Name: "m", ContextWindow: -1}, true, "负数"},
		{"负 max_output", modelPayload{Name: "m", MaxOutput: -1}, true, "负数"},
		{"合法 capabilities", modelPayload{Name: "m", Capabilities: []string{"vision", "reasoning"}}, false, ""},
		{"非法 capabilities", modelPayload{Name: "m", Capabilities: []string{"flying"}}, true, "能力标签"},
		{"空 capabilities 合法", modelPayload{Name: "m", Capabilities: nil}, false, ""},
	}
	for _, c := range cases {
		t.Run(c.label, func(t *testing.T) {
			msg := validateModelPayload(&c.payload)
			if c.wantErr {
				if msg == "" {
					t.Fatalf("期望返回校验错误")
				}
			} else {
				if msg != "" {
					t.Errorf("期望放行，实际返回错误: %q", msg)
				}
			}
		})
	}
}

// TestResolveAlias ModelRepo.ResolveAlias 的行为：
// 命中返回真实名，未命中返回空串与 nil。
func TestResolveAlias(t *testing.T) {
	e := newTestEnv(t)
	m := &store.Model{Name: "claude-opus-5", Modality: domain.ModalityText,
		BillingMode: domain.BillPerToken, Status: domain.ModelEnabled, Alias: "opus"}
	if err := e.db.Create(m).Error; err != nil {
		t.Fatalf("建模型失败: %v", err)
	}

	ctx := context.Background()
	name, err := e.deps.Models.ResolveAlias(ctx, "opus")
	if err != nil {
		t.Fatalf("命中别名应无错误，实际 %v", err)
	}
	if name != "claude-opus-5" {
		t.Errorf("别名解析结果不符：期望 claude-opus-5，实际 %s", name)
	}

	// 未命中返回空串与 nil。
	name, err = e.deps.Models.ResolveAlias(ctx, "nonexistent")
	if err != nil {
		t.Fatalf("未命中别名应无错误，实际 %v", err)
	}
	if name != "" {
		t.Errorf("未命中的别名应返回空串，实际 %s", name)
	}

	// 空入参直接返回空。
	name, err = e.deps.Models.ResolveAlias(ctx, "")
	if err != nil || name != "" {
		t.Errorf("空入参应返回空串与 nil，实际 %q %v", name, err)
	}
}
