package api

// 响应信封契约测试（报告 P3-17）：全部会话认证端点（管理端 + 用户端）的成功响应
// 恒含 success / message / data 三个顶层键；全部分页端点的 data 恒为
// page / page_size / total / items 四字段。/v1 下游端点不套用统一信封——
// 成功与失败响应均为裸协议格式，这条差异是本文件的重点守卫对象。
// 端点清单复用 authzRouteInventory()（authz_matrix_test.go），避免重复维护一份路由表。

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
)

// paginatedRoutes 标记 authzRouteInventory 中 data 字段为分页结构（respond.NewPage）的端点。
// 未列入的只读 GET 端点返回裸数组或裸聚合对象，不经过分页信封
// （stats/overview、stats/usage-daily、stats/profit、me/usage-summary、me/usage-daily、
// usage-logs/detail、admin/models/{id}/channel-costs、admin/channels/{id}/costs、admin/settings 等）。
var paginatedRoutes = map[string]bool{
	"GET /api/me/ledger":             true,
	"GET /api/me/usage-logs":         true,
	"GET /api/me/keys/":              true,
	"GET /api/admin/users/":          true,
	"GET /api/admin/models/":         true,
	"GET /api/admin/redemptions/":    true,
	"GET /api/admin/channels/":       true,
	"GET /api/admin/ledger":          true,
	"GET /api/admin/usage-logs":      true,
	"GET /api/admin/users/{id}/keys": true,
	"GET /api/admin/departments/":    true,
	"GET /api/admin/audit-logs/":     true,
	"GET /api/admin/alerts/":         true,
}

// nonEnvelopeRoutes 标记刻意不返回 JSON 信封的端点。信封的作用是让前端用同一段
// 代码解析全部接口响应；下载类端点的消费者是浏览器与表格软件，不经过这段代码。
var nonEnvelopeRoutes = map[string]bool{
	// 用量日志导出直接输出 CSV 流，供浏览器另存与 Excel 打开。
	"GET /api/admin/usage-logs/export": true,
	"GET /api/me/usage-logs/export":    true,
}

// TestSuccessEnvelopeContract 对全部会话认证端点（user/admin/root 级）的授权成功响应
// 断言信封三键齐全且 success=true；分页端点额外断言 data 为四字段分页结构。
// public 与 api_key（/v1）级端点不走统一信封，由本文件其余测试单独覆盖。
func TestSuccessEnvelopeContract(t *testing.T) {
	e := newTestEnv(t)
	userC := e.seedAndLogin(t, "alice", domain.RoleUser)
	adminC := e.seedAndLogin(t, "bob", domain.RoleAdmin)
	rootC := e.seedAndLogin(t, "carol", domain.RoleRoot)
	seedAuthzFixtures(t, e)

	for _, rt := range authzRouteInventory() {
		var c *http.Client
		switch rt.access {
		case accessUser:
			c = userC
		case accessAdmin:
			c = adminC
		case accessRoot:
			c = rootC
		default:
			continue // public、api_key 级不走统一信封契约
		}
		path := rt.path
		if path == "" {
			path = rt.pattern
		}
		key := rt.method + " " + rt.pattern
		if nonEnvelopeRoutes[key] {
			continue
		}
		// 清单中期望非 2xx 的条目只用于核对权限边界（如未配置告警通道时的
		// 通道测试端点），其响应本就不是成功响应，不适用成功信封契约。
		if rt.wantOK >= 300 {
			continue
		}
		t.Run(key, func(t *testing.T) {
			resp, env := e.do(t, c, rt.method, path, rt.body)
			if resp.StatusCode != rt.wantOK {
				t.Fatalf("期望 %d，实际 %d，响应: %v", rt.wantOK, resp.StatusCode, env)
			}
			for _, key := range []string{"success", "message", "data"} {
				if _, ok := env[key]; !ok {
					t.Errorf("成功响应缺少顶层键 %s: %v", key, env)
				}
			}
			if success, _ := env["success"].(bool); !success {
				t.Errorf("success 应为 true: %v", env)
			}
			if paginatedRoutes[key] {
				page, ok := env["data"].(map[string]any)
				if !ok {
					t.Fatalf("分页端点 data 应为对象: %v", env["data"])
				}
				for _, field := range []string{"page", "page_size", "total", "items"} {
					if _, ok := page[field]; !ok {
						t.Errorf("分页响应缺少字段 %s: %v", field, page)
					}
				}
			}
		})
	}
}

// TestV1SuccessResponseNoEnvelope 断言 /v1 成功响应不套用统一信封：
// chat/completions、key/info、models 三个代表性端点顶层均不含 success/message 键。
func TestV1SuccessResponseNoEnvelope(t *testing.T) {
	e := newTestEnv(t)
	upstream := newOKChatUpstream(t)
	_, key := e.seedRelayUser(t, "envchat", 1_000_000, nil)
	e.seedModel(t, "glm-5")
	e.seedChannel(t, "ch-env", upstream.URL, 0, []string{"glm-5"}, nil)

	assertNoEnvelopeKeys := func(t *testing.T, label string, body map[string]any) {
		t.Helper()
		for _, k := range []string{"success", "message"} {
			if _, ok := body[k]; ok {
				t.Errorf("%s 不应套用统一信封，出现顶层键 %s: %v", label, k, body)
			}
		}
	}

	resp, raw := e.v1Request(t, "POST", "/v1/chat/completions",
		map[string]any{"model": "glm-5", "messages": []map[string]any{{"role": "user", "content": "hi"}}},
		map[string]string{"Authorization": "Bearer " + key})
	if resp.StatusCode != 200 {
		t.Fatalf("应成功: %d %s", resp.StatusCode, raw)
	}
	var chatBody map[string]any
	if err := json.Unmarshal(raw, &chatBody); err != nil {
		t.Fatalf("响应体解析失败: %v %s", err, raw)
	}
	assertNoEnvelopeKeys(t, "/v1/chat/completions", chatBody)
	if _, ok := chatBody["choices"]; !ok {
		t.Errorf("应为裸 OpenAI 格式，含 choices: %v", chatBody)
	}

	resp2, raw2 := e.v1Request(t, "GET", "/v1/key/info", nil,
		map[string]string{"Authorization": "Bearer " + key})
	if resp2.StatusCode != 200 {
		t.Fatalf("key/info 应 200: %d %s", resp2.StatusCode, raw2)
	}
	var infoBody map[string]any
	if err := json.Unmarshal(raw2, &infoBody); err != nil {
		t.Fatalf("响应体解析失败: %v %s", err, raw2)
	}
	assertNoEnvelopeKeys(t, "/v1/key/info", infoBody)
	if _, ok := infoBody["data"]; ok {
		t.Errorf("/v1/key/info 不应含 data 字段（非分页信封）: %v", infoBody)
	}

	resp3, raw3 := e.v1Request(t, "GET", "/v1/models", nil,
		map[string]string{"Authorization": "Bearer " + key})
	if resp3.StatusCode != 200 {
		t.Fatalf("models 应 200: %d %s", resp3.StatusCode, raw3)
	}
	var modelsBody map[string]any
	if err := json.Unmarshal(raw3, &modelsBody); err != nil {
		t.Fatalf("响应体解析失败: %v %s", err, raw3)
	}
	assertNoEnvelopeKeys(t, "/v1/models", modelsBody)
	if modelsBody["object"] != "list" {
		t.Errorf("/v1/models 应为 OpenAI list 格式（object=list）: %v", modelsBody)
	}
}

// TestV1ErrorResponseNoEnvelope 断言 /v1 失败响应（门禁层与业务层）同样不套用统一信封，
// 顶层不含 success/message 键，且错误结构含 error 字段——契约测试已在
// relay_contract_test.go 逐条验证具体协议格式，本测试专门守卫"不误套信封"这一条。
func TestV1ErrorResponseNoEnvelope(t *testing.T) {
	e := newTestEnv(t)

	// 门禁层：无效 Key
	resp, raw := e.v1Request(t, "POST", "/v1/chat/completions",
		map[string]any{"model": "glm-5", "messages": []map[string]any{{"role": "user", "content": "hi"}}},
		map[string]string{"Authorization": "Bearer tzl-invalid-000000"})
	if resp.StatusCode != 401 {
		t.Fatalf("无效 Key 应 401: %d %s", resp.StatusCode, raw)
	}
	var gateBody map[string]any
	if err := json.Unmarshal(raw, &gateBody); err != nil {
		t.Fatalf("响应体解析失败: %v %s", err, raw)
	}
	for _, k := range []string{"success", "message"} {
		if _, ok := gateBody[k]; ok {
			t.Errorf("门禁层错误不应套用信封，出现顶层键 %s: %v", k, gateBody)
		}
	}
	if _, ok := gateBody["error"]; !ok {
		t.Errorf("应为 OpenAI 格式含 error: %v", gateBody)
	}

	// 业务层：未上架模型
	_, key := e.seedRelayUser(t, "envbiz", 100_000, nil)
	resp2, raw2 := e.v1Request(t, "POST", "/v1/chat/completions",
		map[string]any{"model": "ghost-model", "messages": []map[string]any{{"role": "user", "content": "hi"}}},
		map[string]string{"Authorization": "Bearer " + key})
	if resp2.StatusCode != 404 {
		t.Fatalf("未上架模型应 404: %d %s", resp2.StatusCode, raw2)
	}
	var bizBody map[string]any
	if err := json.Unmarshal(raw2, &bizBody); err != nil {
		t.Fatalf("响应体解析失败: %v %s", err, raw2)
	}
	for _, k := range []string{"success", "message"} {
		if _, ok := bizBody[k]; ok {
			t.Errorf("业务层错误不应套用信封，出现顶层键 %s: %v", k, bizBody)
		}
	}
	if _, ok := bizBody["error"]; !ok {
		t.Errorf("应含 error 字段: %v", bizBody)
	}
}
