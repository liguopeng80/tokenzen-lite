package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// --- 端点与模型形态/计费方式匹配校验（堵住按次计费模型从对话端点零扣费） ---

// postV1 用 API Key 向任意 /v1 路径发请求，返回状态码与解析后的响应体。
func (e *testEnv) postV1(t *testing.T, apiKey, path string, body map[string]any) (int, map[string]any) {
	t.Helper()
	payload, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", e.srv.URL+path, bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("请求 %s 失败: %v", path, err)
	}
	defer resp.Body.Close()
	var parsed map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&parsed)
	return resp.StatusCode, parsed
}

// assertEndpointMismatch 断言响应为 400 + model_endpoint_mismatch，且消息含期望的端点提示。
// 兼容两种下游错误格式：OpenAI 的 error.code 与 Anthropic 的 error.type。
func assertEndpointMismatch(t *testing.T, status int, body map[string]any, wantHint string) {
	t.Helper()
	if status != http.StatusBadRequest {
		t.Fatalf("期望 400，实际 %d，响应: %v", status, body)
	}
	errObj, ok := body["error"].(map[string]any)
	if !ok {
		t.Fatalf("响应应含 error 对象，实际: %v", body)
	}
	code := errObj["code"]
	if code == nil {
		code = errObj["type"]
	}
	if code != "model_endpoint_mismatch" {
		t.Errorf("期望错误码 model_endpoint_mismatch，实际 %v", code)
	}
	msg, _ := errObj["message"].(string)
	if !strings.Contains(msg, wantHint) {
		t.Errorf("错误消息应包含 %q，实际 %q", wantHint, msg)
	}
}

// 按次计费的图像模型从对话端点调用：拒绝且分文不扣（此前会按 token 单价算出 0 积分放行）。
func TestPerCallModelRejectedOnChatEndpoint(t *testing.T) {
	e := newTestEnv(t)
	userID, key := e.seedRelayUser(t, "epm-image", 1_000_000, nil)
	m := &store.Model{Name: "img-model", Modality: domain.ModalityImage,
		BillingMode: domain.BillPerCall, Status: domain.ModelEnabled}
	e.db.Create(m)
	e.db.Create(&store.ModelPrice{ModelID: m.ID, PerCallPrice: 40_000})

	for _, path := range []string{"/v1/chat/completions", "/v1/messages"} {
		status, body := e.postV1(t, key, path, map[string]any{
			"model": "img-model", "max_tokens": 100,
			"messages": []map[string]any{{"role": "user", "content": "hi"}},
		})
		assertEndpointMismatch(t, status, body, "/v1/images/generations")
	}
	if bal := e.userBalance(t, userID); bal != 1_000_000 {
		t.Errorf("拒绝的请求不应扣积分，余额期望 1000000，实际 %d", bal)
	}
	e.assertReconcile(t)
}

// 向量模型从对话端点调用：拒绝并指向 /v1/embeddings。
func TestEmbeddingModelRejectedOnChatEndpoint(t *testing.T) {
	e := newTestEnv(t)
	_, key := e.seedRelayUser(t, "epm-emb", 100_000, nil)
	m := &store.Model{Name: "emb-model", Modality: domain.ModalityEmbedding,
		BillingMode: domain.BillPerToken, Status: domain.ModelEnabled}
	e.db.Create(m)
	e.db.Create(&store.ModelPrice{ModelID: m.ID, InputPrice: 500_000})

	status, body := e.postV1(t, key, "/v1/chat/completions", map[string]any{
		"model": "emb-model", "messages": []map[string]any{{"role": "user", "content": "hi"}},
	})
	assertEndpointMismatch(t, status, body, "/v1/embeddings")
}

// 文本模型从向量与图像端点调用：拒绝并指向对话端点。
func TestTextModelRejectedOnSimpleEndpoints(t *testing.T) {
	e := newTestEnv(t)
	_, key := e.seedRelayUser(t, "epm-text", 100_000, nil)
	e.seedModel(t, "chat-model")

	status, body := e.postV1(t, key, "/v1/embeddings",
		map[string]any{"model": "chat-model", "input": "文本"})
	assertEndpointMismatch(t, status, body, "/v1/chat/completions")

	status, body = e.postV1(t, key, "/v1/images/generations",
		map[string]any{"model": "chat-model", "prompt": "一只猫"})
	assertEndpointMismatch(t, status, body, "/v1/chat/completions")
}

// 形态匹配但计费方式配置错误（文本模型配成按次计费）：拒绝并提示核对配置。
func TestBillingModeMismatchRejectedOnChatEndpoint(t *testing.T) {
	e := newTestEnv(t)
	userID, key := e.seedRelayUser(t, "epm-bill", 1_000_000, nil)
	m := &store.Model{Name: "text-percall", Modality: domain.ModalityText,
		BillingMode: domain.BillPerCall, Status: domain.ModelEnabled}
	e.db.Create(m)
	e.db.Create(&store.ModelPrice{ModelID: m.ID, PerCallPrice: 40_000})

	status, body := e.postV1(t, key, "/v1/chat/completions", map[string]any{
		"model": "text-percall", "messages": []map[string]any{{"role": "user", "content": "hi"}},
	})
	assertEndpointMismatch(t, status, body, "计费方式")
	if bal := e.userBalance(t, userID); bal != 1_000_000 {
		t.Errorf("拒绝的请求不应扣积分，余额期望 1000000，实际 %d", bal)
	}
}

// 管理端定价校验：按 token 计费至少一项 token 单价非零，按次计费必须配置按次单价；
// 已配置定价的模型变更计费方式时须与定价一致。
func TestAdminModelPriceBillingModeValidation(t *testing.T) {
	e := newTestEnv(t)
	rootC := e.seedAndLogin(t, "epm-root", domain.RoleRoot)

	createModel := func(name string, billing domain.BillingMode) int64 {
		t.Helper()
		resp, env := e.do(t, rootC, "POST", "/api/admin/models/", map[string]any{
			"name": name, "modality": "text", "billing_mode": string(billing),
		})
		if resp.StatusCode != 201 {
			t.Fatalf("创建模型失败: %d %v", resp.StatusCode, env)
		}
		return int64(env["data"].(map[string]any)["id"].(float64))
	}

	// 按 token 计费：全零 token 单价（仅配了按次单价）应拒绝
	tokenID := createModel("val-token", domain.BillPerToken)
	resp, env := e.do(t, rootC, "PUT", fmt.Sprintf("/api/admin/models/%d/price", tokenID),
		map[string]any{"per_call_price": 40_000})
	if resp.StatusCode != 400 {
		t.Errorf("按 token 计费全零 token 单价应 400，实际 %d %v", resp.StatusCode, env)
	}
	resp, _ = e.do(t, rootC, "PUT", fmt.Sprintf("/api/admin/models/%d/price", tokenID),
		map[string]any{"input_price": 720_000, "output_price": 2_300_000})
	if resp.StatusCode != 200 {
		t.Fatalf("合法 token 定价应 200，实际 %d", resp.StatusCode)
	}

	// 按次计费：按次单价为零应拒绝
	callID := createModel("val-call", domain.BillPerCall)
	resp, _ = e.do(t, rootC, "PUT", fmt.Sprintf("/api/admin/models/%d/price", callID),
		map[string]any{"input_price": 720_000})
	if resp.StatusCode != 400 {
		t.Errorf("按次计费零按次单价应 400，实际 %d", resp.StatusCode)
	}
	resp, _ = e.do(t, rootC, "PUT", fmt.Sprintf("/api/admin/models/%d/price", callID),
		map[string]any{"per_call_price": 40_000})
	if resp.StatusCode != 200 {
		t.Fatalf("合法按次定价应 200，实际 %d", resp.StatusCode)
	}

	// 变更计费方式与既有定价冲突：token 定价的模型切成按次计费应拒绝
	resp, _ = e.do(t, rootC, "PUT", fmt.Sprintf("/api/admin/models/%d", tokenID), map[string]any{
		"name": "val-token", "modality": "text", "billing_mode": "per_call",
	})
	if resp.StatusCode != 400 {
		t.Errorf("token 定价切按次计费应 400，实际 %d", resp.StatusCode)
	}
	// 保持一致的更新应通过
	resp, _ = e.do(t, rootC, "PUT", fmt.Sprintf("/api/admin/models/%d", tokenID), map[string]any{
		"name": "val-token", "modality": "text", "billing_mode": "per_token",
		"display_name": "改个名",
	})
	if resp.StatusCode != 200 {
		t.Errorf("与定价一致的更新应 200，实际 %d", resp.StatusCode)
	}
}

// 模型名称不可修改：渠道路由、成本、密钥白名单与用量日志均按名称字符串关联，
// 改名会静默断链，更新端点必须拒绝名称变更并给出明确提示。
func TestAdminModelNameImmutable(t *testing.T) {
	e := newTestEnv(t)
	rootC := e.seedAndLogin(t, "epm-rename-root", domain.RoleRoot)

	resp, env := e.do(t, rootC, "POST", "/api/admin/models/", map[string]any{
		"name": "rename-src", "modality": "text", "billing_mode": "per_token",
	})
	if resp.StatusCode != 201 {
		t.Fatalf("创建模型失败: %d %v", resp.StatusCode, env)
	}
	id := int64(env["data"].(map[string]any)["id"].(float64))

	// 名称变更应被拒绝，且提示说明原因、给出新建模型的引导
	resp, env = e.do(t, rootC, "PUT", fmt.Sprintf("/api/admin/models/%d", id), map[string]any{
		"name": "rename-dst", "modality": "text", "billing_mode": "per_token",
		"display_name": "不应落库",
	})
	if resp.StatusCode != 400 {
		t.Fatalf("修改模型名称应 400，实际 %d %v", resp.StatusCode, env)
	}
	if msg, _ := env["message"].(string); !strings.Contains(msg, "模型名称不可修改") {
		t.Errorf("拒绝提示应包含「模型名称不可修改」，实际: %q", msg)
	} else if !strings.Contains(msg, "新建模型") {
		t.Errorf("拒绝提示应引导「新建模型」，实际: %q", msg)
	}

	// 拒绝必须是原子的：同请求携带的其他字段不得部分落库
	resp, env = e.do(t, rootC, "GET", fmt.Sprintf("/api/admin/models/%d", id), nil)
	if resp.StatusCode != 200 {
		t.Fatalf("查询模型失败: %d", resp.StatusCode)
	}
	if dn := env["data"].(map[string]any)["display_name"]; dn == "不应落库" {
		t.Errorf("拒绝改名的请求不应产生部分更新，display_name 已落库: %v", dn)
	}

	// 仅大小写差异的名称同样视为改名并拒绝：渠道 models 列表、用量日志等
	// 均按大小写敏感的字符串匹配关联，大小写改名同样断链。
	resp, env = e.do(t, rootC, "PUT", fmt.Sprintf("/api/admin/models/%d", id), map[string]any{
		"name": "rename-SRC", "modality": "text", "billing_mode": "per_token",
	})
	if resp.StatusCode != 400 {
		t.Errorf("仅大小写差异的改名应 400，实际 %d", resp.StatusCode)
	} else if msg, _ := env["message"].(string); !strings.Contains(msg, "模型名称不可修改") {
		t.Errorf("大小写改名拒绝提示应包含「模型名称不可修改」，实际: %q", msg)
	}

	// 空名称走名称必填校验，同样拒绝（更新契约为必须携带与现值一致的 name）
	resp, _ = e.do(t, rootC, "PUT", fmt.Sprintf("/api/admin/models/%d", id), map[string]any{
		"name": "", "modality": "text", "billing_mode": "per_token",
	})
	if resp.StatusCode != 400 {
		t.Errorf("空名称更新应 400，实际 %d", resp.StatusCode)
	}

	// 不存在的模型 id 应 404
	resp, env = e.do(t, rootC, "PUT", "/api/admin/models/999999", map[string]any{
		"name": "rename-src", "modality": "text", "billing_mode": "per_token",
	})
	if resp.StatusCode != 404 {
		t.Errorf("更新不存在的模型应 404，实际 %d", resp.StatusCode)
	} else if msg, _ := env["message"].(string); !strings.Contains(msg, "模型不存在") {
		t.Errorf("404 消息应为「模型不存在」，实际: %q", msg)
	}

	// 权限矩阵回归：普通用户不能调用管理端模型更新
	userC := e.seedAndLogin(t, "epm-rename-user", domain.RoleUser)
	resp, _ = e.do(t, userC, "PUT", fmt.Sprintf("/api/admin/models/%d", id), map[string]any{
		"name": "rename-src", "modality": "text", "billing_mode": "per_token",
	})
	if resp.StatusCode != 403 {
		t.Errorf("普通用户更新模型应 403，实际 %d", resp.StatusCode)
	}

	// 名称保持不变时其余字段应正常更新
	resp, _ = e.do(t, rootC, "PUT", fmt.Sprintf("/api/admin/models/%d", id), map[string]any{
		"name": "rename-src", "modality": "text", "billing_mode": "per_token",
		"display_name": "新展示名",
	})
	if resp.StatusCode != 200 {
		t.Fatalf("名称不变的更新应 200，实际 %d", resp.StatusCode)
	}

	// 数据库中名称未被篡改，其余更新已生效
	resp, env = e.do(t, rootC, "GET", fmt.Sprintf("/api/admin/models/%d", id), nil)
	if resp.StatusCode != 200 {
		t.Fatalf("查询模型失败: %d", resp.StatusCode)
	}
	data := env["data"].(map[string]any)
	if data["name"] != "rename-src" {
		t.Errorf("模型名称期望保持 rename-src，实际 %v", data["name"])
	}
	if data["display_name"] != "新展示名" {
		t.Errorf("展示名称期望已更新为「新展示名」，实际 %v", data["display_name"])
	}

	// 引导路径可用性：携带原 name 下架旧模型（status=disabled）不被名称守卫阻塞
	resp, _ = e.do(t, rootC, "PUT", fmt.Sprintf("/api/admin/models/%d", id), map[string]any{
		"name": "rename-src", "modality": "text", "billing_mode": "per_token",
		"display_name": "新展示名", "status": "disabled",
	})
	if resp.StatusCode != 200 {
		t.Fatalf("携带原名称下架模型应 200，实际 %d", resp.StatusCode)
	}
	resp, env = e.do(t, rootC, "GET", fmt.Sprintf("/api/admin/models/%d", id), nil)
	if resp.StatusCode != 200 {
		t.Fatalf("查询模型失败: %d", resp.StatusCode)
	}
	if st := env["data"].(map[string]any)["status"]; st != "disabled" {
		t.Errorf("模型状态期望 disabled，实际 %v", st)
	}
}
