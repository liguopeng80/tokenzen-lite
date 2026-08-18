package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// 渠道生命周期：创建（不回显明文密钥）→ 中继成功 → 留空编辑保留密钥 →
// 手工禁用后无可用渠道 → 恢复启用清空禁用原因 → 成本设置回读 → 删除后 404。
func TestAdminChannelLifecycle(t *testing.T) {
	e := newTestEnv(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"x","model":"glm-5","choices":[{"message":{"content":"hi"}}],
			"usage":{"prompt_tokens":100,"completion_tokens":50}}`)
	}))
	defer upstream.Close()

	adminC := e.seedAndLogin(t, "chadmin", domain.RoleAdmin)
	_, key := e.seedRelayUser(t, "chuser", 1_000_000, nil)
	e.seedModel(t, "glm-5")

	const plainSecret = "sk-upstream-plain-secret"
	relayOnce := func() *http.Response {
		resp, _ := e.relayPost(t, key, map[string]any{
			"model": "glm-5", "messages": []map[string]any{{"role": "user", "content": "hi"}},
		})
		return resp
	}

	// 1. 创建渠道：响应不得回显明文密钥
	resp, env := e.do(t, adminC, "POST", "/api/admin/channels/", map[string]any{
		"name": "生命周期渠道", "provider": "zhipu", "protocol": "openai_compat",
		"base_url": upstream.URL, "api_key": plainSecret, "models": []string{"glm-5"},
	})
	if resp.StatusCode != 201 {
		t.Fatalf("创建渠道失败: %d %v", resp.StatusCode, env)
	}
	raw, _ := json.Marshal(env)
	if strings.Contains(string(raw), plainSecret) {
		t.Error("创建响应不应包含明文密钥")
	}
	chID := int64(env["data"].(map[string]any)["id"].(float64))

	// 详情与列表同样不得泄露密钥
	resp, env = e.do(t, adminC, "GET", fmt.Sprintf("/api/admin/channels/%d", chID), nil)
	if resp.StatusCode != 200 {
		t.Fatalf("查询渠道详情失败: %d", resp.StatusCode)
	}
	raw, _ = json.Marshal(env)
	if strings.Contains(string(raw), plainSecret) {
		t.Error("渠道详情不应包含明文密钥")
	}
	resp, env = e.do(t, adminC, "GET", "/api/admin/channels/", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("查询渠道列表失败: %d", resp.StatusCode)
	}
	raw, _ = json.Marshal(env)
	if strings.Contains(string(raw), plainSecret) {
		t.Error("渠道列表不应包含明文密钥")
	}

	// 2. 经该渠道中继成功（证明落库密钥可解密可用）
	if r := relayOnce(); r.StatusCode != 200 {
		t.Fatalf("创建后中继应成功，实际 %d", r.StatusCode)
	}

	// 3. 密钥留空编辑：其余字段更新、密钥保留，中继仍成功
	resp, env = e.do(t, adminC, "PUT", fmt.Sprintf("/api/admin/channels/%d", chID), map[string]any{
		"name": "改名后渠道", "provider": "zhipu", "protocol": "openai_compat",
		"base_url": upstream.URL, "api_key": "", "models": []string{"glm-5"},
	})
	if resp.StatusCode != 200 {
		t.Fatalf("编辑渠道失败: %d %v", resp.StatusCode, env)
	}
	var ch store.Channel
	if err := e.db.First(&ch, chID).Error; err != nil {
		t.Fatalf("查渠道失败: %v", err)
	}
	if ch.Name != "改名后渠道" {
		t.Errorf("渠道名应已更新，实际 %s", ch.Name)
	}
	if got, err := e.deps.Secrets.Decrypt(ch.APIKeyEncrypted); err != nil || got != plainSecret {
		t.Errorf("留空编辑应保留原密钥，实际 %q err=%v", got, err)
	}
	if r := relayOnce(); r.StatusCode != 200 {
		t.Fatalf("留空编辑后中继应成功，实际 %d", r.StatusCode)
	}

	// 4. 手工禁用：中继返回无可用渠道
	resp, _ = e.do(t, adminC, "PUT", fmt.Sprintf("/api/admin/channels/%d/status", chID),
		map[string]any{"status": "manual_disabled"})
	if resp.StatusCode != 200 {
		t.Fatalf("禁用渠道失败: %d", resp.StatusCode)
	}
	if r := relayOnce(); r.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("禁用后中继应 503 无可用渠道，实际 %d", r.StatusCode)
	}

	// 5. 恢复启用：禁用原因被清空，中继恢复
	e.db.Model(&store.Channel{}).Where("id = ?", chID).
		Update("disabled_reason", "上游密钥失效")
	resp, _ = e.do(t, adminC, "PUT", fmt.Sprintf("/api/admin/channels/%d/status", chID),
		map[string]any{"status": "enabled"})
	if resp.StatusCode != 200 {
		t.Fatalf("恢复启用失败: %d", resp.StatusCode)
	}
	if err := e.db.First(&ch, chID).Error; err != nil {
		t.Fatalf("查渠道失败: %v", err)
	}
	if ch.Status != domain.ChannelEnabled || ch.DisabledReason != "" {
		t.Errorf("恢复启用应清空禁用原因，实际 status=%s reason=%q", ch.Status, ch.DisabledReason)
	}
	if r := relayOnce(); r.StatusCode != 200 {
		t.Fatalf("恢复启用后中继应成功，实际 %d", r.StatusCode)
	}

	// 6. 设置成本并回读校验
	resp, _ = e.do(t, adminC, "PUT", fmt.Sprintf("/api/admin/channels/%d/costs", chID),
		map[string]any{"costs": []map[string]any{
			{"model_name": "glm-5", "currency": "usd", "input_cost": 30, "output_cost": 60},
			{"model_name": "glm-5-air", "input_cost": 10, "output_cost": 20}, // currency 缺省应落 credits
		}})
	if resp.StatusCode != 200 {
		t.Fatalf("设置成本失败: %d", resp.StatusCode)
	}
	resp, env = e.do(t, adminC, "GET", fmt.Sprintf("/api/admin/channels/%d/costs", chID), nil)
	if resp.StatusCode != 200 {
		t.Fatalf("查询成本失败: %d", resp.StatusCode)
	}
	items := env["data"].([]any)
	if len(items) != 2 {
		t.Fatalf("应回读 2 条成本，实际 %d", len(items))
	}
	first := items[0].(map[string]any) // 按 model_name 排序，glm-5 在前
	if first["model_name"] != "glm-5" || first["currency"] != "usd" ||
		int64(first["input_cost"].(float64)) != 30 || int64(first["output_cost"].(float64)) != 60 {
		t.Errorf("成本回读不符: %v", first)
	}
	second := items[1].(map[string]any)
	if second["model_name"] != "glm-5-air" || second["currency"] != "credits" {
		t.Errorf("缺省币种应为 credits: %v", second)
	}

	// 7. 删除后详情 404
	resp, _ = e.do(t, adminC, "DELETE", fmt.Sprintf("/api/admin/channels/%d", chID), nil)
	if resp.StatusCode != 200 {
		t.Fatalf("删除渠道失败: %d", resp.StatusCode)
	}
	resp, _ = e.do(t, adminC, "GET", fmt.Sprintf("/api/admin/channels/%d", chID), nil)
	if resp.StatusCode != 404 {
		t.Errorf("删除后查询应 404，实际 %d", resp.StatusCode)
	}
}

// 渠道编辑覆盖配置保持语义：param_override / header_override 字段缺席保持原值，
// 显式传 {} 才清空（与 priority/weight 同款指针语义）。
func TestAdminChannelUpdateKeepsOverrides(t *testing.T) {
	e := newTestEnv(t)
	adminC := e.seedAndLogin(t, "chadmin3", domain.RoleAdmin)

	resp, env := e.do(t, adminC, "POST", "/api/admin/channels/", map[string]any{
		"name": "覆盖渠道", "provider": "zhipu", "protocol": "openai_compat",
		"base_url": "http://127.0.0.1:1", "api_key": "sk-x", "models": []string{"glm-5"},
		"param_override":  map[string]any{"temperature": 0.2},
		"header_override": map[string]string{"X-Custom": "v1"},
	})
	if resp.StatusCode != 201 {
		t.Fatalf("创建渠道失败: %d %v", resp.StatusCode, env)
	}
	chID := int64(env["data"].(map[string]any)["id"].(float64))

	// I1. GET 详情回读：覆盖字段与入参一致
	resp, env = e.do(t, adminC, "GET", fmt.Sprintf("/api/admin/channels/%d", chID), nil)
	if resp.StatusCode != 200 {
		t.Fatalf("查询渠道详情失败: %d", resp.StatusCode)
	}
	detail := env["data"].(map[string]any)
	if po, ok := detail["param_override"].(map[string]any); !ok || po["temperature"] != 0.2 {
		t.Errorf("详情回读参数覆盖不符: %v", detail["param_override"])
	}
	if ho, ok := detail["header_override"].(map[string]any); !ok || ho["X-Custom"] != "v1" {
		t.Errorf("详情回读请求头覆盖不符: %v", detail["header_override"])
	}

	loadChannel := func() store.Channel {
		var ch store.Channel
		if err := e.db.First(&ch, chID).Error; err != nil {
			t.Fatalf("查渠道失败: %v", err)
		}
		return ch
	}

	// 1. 编辑其他字段、不携带覆盖字段：覆盖配置保持原值
	resp, env = e.do(t, adminC, "PUT", fmt.Sprintf("/api/admin/channels/%d", chID), map[string]any{
		"name": "覆盖渠道改名", "provider": "zhipu", "protocol": "openai_compat",
		"base_url": "http://127.0.0.1:1", "api_key": "", "models": []string{"glm-5"},
	})
	if resp.StatusCode != 200 {
		t.Fatalf("编辑渠道失败: %d %v", resp.StatusCode, env)
	}
	ch := loadChannel()
	if ch.Name != "覆盖渠道改名" {
		t.Errorf("渠道名应已更新，实际 %s", ch.Name)
	}
	if got := ch.ParamOverrides(); got["temperature"] != 0.2 {
		t.Errorf("字段缺席时参数覆盖应保持原值，实际 %v", got)
	}
	if got := ch.HeaderOverrides(); got["X-Custom"] != "v1" {
		t.Errorf("字段缺席时请求头覆盖应保持原值，实际 %v", got)
	}

	// 2. 显式传入新值：正常更新
	resp, _ = e.do(t, adminC, "PUT", fmt.Sprintf("/api/admin/channels/%d", chID), map[string]any{
		"name": "覆盖渠道改名", "provider": "zhipu", "protocol": "openai_compat",
		"base_url": "http://127.0.0.1:1", "api_key": "", "models": []string{"glm-5"},
		"header_override": map[string]string{"X-Custom": "v2"},
	})
	if resp.StatusCode != 200 {
		t.Fatalf("编辑渠道失败: %d", resp.StatusCode)
	}
	ch = loadChannel()
	if got := ch.HeaderOverrides(); got["X-Custom"] != "v2" {
		t.Errorf("显式传入请求头覆盖应更新，实际 %v", got)
	}
	if got := ch.ParamOverrides(); got["temperature"] != 0.2 {
		t.Errorf("未携带的参数覆盖应保持原值，实际 %v", got)
	}

	// 2b. 传入新键集合：整体替换旧值而非合并，旧键不残留
	resp, _ = e.do(t, adminC, "PUT", fmt.Sprintf("/api/admin/channels/%d", chID), map[string]any{
		"name": "覆盖渠道改名", "provider": "zhipu", "protocol": "openai_compat",
		"base_url": "http://127.0.0.1:1", "api_key": "", "models": []string{"glm-5"},
		"param_override":  map[string]any{"top_p": 0.9},
		"header_override": map[string]string{"X-Other": "z"},
	})
	if resp.StatusCode != 200 {
		t.Fatalf("编辑渠道失败: %d", resp.StatusCode)
	}
	ch = loadChannel()
	if got := ch.ParamOverrides(); got["top_p"] != 0.9 || len(got) != 1 {
		t.Errorf("参数覆盖应整体替换为新键集合，旧键 temperature 不应残留，实际 %v", got)
	}
	if got := ch.HeaderOverrides(); got["X-Other"] != "z" || len(got) != 1 {
		t.Errorf("请求头覆盖应整体替换为新键集合，旧键 X-Custom 不应残留，实际 %v", got)
	}

	// 2c. 显式传 null：与缺席同义，库中原值保持（A1，端到端断言 DB 终态）
	resp, _ = e.do(t, adminC, "PUT", fmt.Sprintf("/api/admin/channels/%d", chID), map[string]any{
		"name": "覆盖渠道改名", "provider": "zhipu", "protocol": "openai_compat",
		"base_url": "http://127.0.0.1:1", "api_key": "", "models": []string{"glm-5"},
		"param_override": nil, "header_override": nil,
	})
	if resp.StatusCode != 200 {
		t.Fatalf("编辑渠道失败: %d", resp.StatusCode)
	}
	ch = loadChannel()
	if got := ch.ParamOverrides(); got["top_p"] != 0.9 || len(got) != 1 {
		t.Errorf("显式传 null 参数覆盖应保持原值，实际 %v", got)
	}
	if got := ch.HeaderOverrides(); got["X-Other"] != "z" || len(got) != 1 {
		t.Errorf("显式传 null 请求头覆盖应保持原值，实际 %v", got)
	}

	// 3. 显式传 {}：清空覆盖配置
	resp, _ = e.do(t, adminC, "PUT", fmt.Sprintf("/api/admin/channels/%d", chID), map[string]any{
		"name": "覆盖渠道改名", "provider": "zhipu", "protocol": "openai_compat",
		"base_url": "http://127.0.0.1:1", "api_key": "", "models": []string{"glm-5"},
		"param_override":  map[string]any{},
		"header_override": map[string]string{},
	})
	if resp.StatusCode != 200 {
		t.Fatalf("编辑渠道失败: %d", resp.StatusCode)
	}
	ch = loadChannel()
	if got := ch.ParamOverrides(); len(got) != 0 {
		t.Errorf("显式传空对象应清空参数覆盖，实际 %v", got)
	}
	if got := ch.HeaderOverrides(); len(got) != 0 {
		t.Errorf("显式传空对象应清空请求头覆盖，实际 %v", got)
	}
}

// 创建路径缺席覆盖字段：落库为 {} 而非 NULL（满足列非空约束，读取端无需判空）。
func TestAdminChannelCreateDefaultsOverridesToEmptyObject(t *testing.T) {
	e := newTestEnv(t)
	adminC := e.seedAndLogin(t, "chadmin4", domain.RoleAdmin)

	resp, env := e.do(t, adminC, "POST", "/api/admin/channels/", map[string]any{
		"name": "缺省覆盖渠道", "provider": "zhipu", "protocol": "openai_compat",
		"base_url": "http://127.0.0.1:1", "api_key": "sk-x", "models": []string{"glm-5"},
	})
	if resp.StatusCode != 201 {
		t.Fatalf("创建渠道失败: %d %v", resp.StatusCode, env)
	}
	chID := int64(env["data"].(map[string]any)["id"].(float64))

	var ch store.Channel
	if err := e.db.First(&ch, chID).Error; err != nil {
		t.Fatalf("查渠道失败: %v", err)
	}
	if string(ch.ParamOverride) != "{}" {
		t.Errorf("缺席时参数覆盖应落 {}，实际 %q", string(ch.ParamOverride))
	}
	if string(ch.HeaderOverride) != "{}" {
		t.Errorf("缺席时请求头覆盖应落 {}，实际 %q", string(ch.HeaderOverride))
	}
}

// Seam：配置覆盖 → 不含覆盖字段的编辑 → 中继请求仍携带覆盖参数与覆盖请求头。
func TestChannelOverridesSurviveEditInRelay(t *testing.T) {
	e := newTestEnv(t)

	var mu sync.Mutex
	var gotBody map[string]any
	var gotHeader http.Header
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotHeader = r.Header.Clone()
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"x","model":"glm-5","choices":[{"message":{"content":"hi"}}],
			"usage":{"prompt_tokens":100,"completion_tokens":50}}`)
	}))
	defer upstream.Close()

	adminC := e.seedAndLogin(t, "chadmin5", domain.RoleAdmin)
	_, key := e.seedRelayUser(t, "chuser5", 1_000_000, nil)
	e.seedModel(t, "glm-5")

	resp, env := e.do(t, adminC, "POST", "/api/admin/channels/", map[string]any{
		"name": "中继覆盖渠道", "provider": "zhipu", "protocol": "openai_compat",
		"base_url": upstream.URL, "api_key": "sk-x", "models": []string{"glm-5"},
		"param_override":  map[string]any{"temperature": 0.7},
		"header_override": map[string]string{"X-Ov": "keep"},
	})
	if resp.StatusCode != 201 {
		t.Fatalf("创建渠道失败: %d %v", resp.StatusCode, env)
	}
	chID := int64(env["data"].(map[string]any)["id"].(float64))

	// 不含覆盖字段的编辑
	resp, _ = e.do(t, adminC, "PUT", fmt.Sprintf("/api/admin/channels/%d", chID), map[string]any{
		"name": "中继覆盖渠道改名", "provider": "zhipu", "protocol": "openai_compat",
		"base_url": upstream.URL, "api_key": "", "models": []string{"glm-5"},
	})
	if resp.StatusCode != 200 {
		t.Fatalf("编辑渠道失败: %d", resp.StatusCode)
	}

	r, raw := e.relayPost(t, key, map[string]any{
		"model": "glm-5", "messages": []map[string]any{{"role": "user", "content": "hi"}},
	})
	if r.StatusCode != 200 {
		t.Fatalf("中继应成功，实际 %d %s", r.StatusCode, raw)
	}

	mu.Lock()
	defer mu.Unlock()
	if gotBody == nil {
		t.Fatal("上游未收到请求体")
	}
	if gotBody["temperature"] != 0.7 {
		t.Errorf("编辑后中继请求体应仍含参数覆盖 temperature=0.7，实际 %v", gotBody["temperature"])
	}
	if got := gotHeader.Get("X-Ov"); got != "keep" {
		t.Errorf("编辑后中继请求头应仍含覆盖头 X-Ov=keep，实际 %q", got)
	}
}

// 必填校验 400 与不存在渠道 404。
func TestAdminChannelValidationAndNotFound(t *testing.T) {
	e := newTestEnv(t)
	adminC := e.seedAndLogin(t, "chadmin2", domain.RoleAdmin)

	valid := func() map[string]any {
		return map[string]any{
			"name": "校验渠道", "provider": "zhipu", "protocol": "openai_compat",
			"base_url": "http://127.0.0.1:1", "api_key": "sk-x", "models": []string{"glm-5"},
		}
	}
	badCases := []struct {
		name   string
		mutate func(m map[string]any)
	}{
		{"缺渠道名称", func(m map[string]any) { m["name"] = "" }},
		{"厂商不合法", func(m map[string]any) { m["provider"] = "unknown-vendor" }},
		{"协议不合法", func(m map[string]any) { m["protocol"] = "grpc" }},
		{"缺 base_url", func(m map[string]any) { m["base_url"] = "" }},
		{"模型清单为空", func(m map[string]any) { m["models"] = []string{} }},
		{"缺上游密钥", func(m map[string]any) { m["api_key"] = "" }},
	}
	for _, tc := range badCases {
		t.Run("创建/"+tc.name, func(t *testing.T) {
			body := valid()
			tc.mutate(body)
			resp, env := e.do(t, adminC, "POST", "/api/admin/channels/", body)
			if resp.StatusCode != 400 {
				t.Errorf("应 400，实际 %d %v", resp.StatusCode, env)
			}
		})
	}

	// 成本校验：缺模型名 / 币种非法 / 单价为负
	resp, env := e.do(t, adminC, "POST", "/api/admin/channels/", valid())
	if resp.StatusCode != 201 {
		t.Fatalf("创建校验渠道失败: %d %v", resp.StatusCode, env)
	}
	chID := int64(env["data"].(map[string]any)["id"].(float64))
	costCases := []struct {
		name    string
		cost    map[string]any
		wantMsg string
	}{
		{"缺模型名", map[string]any{"model_name": "", "input_cost": 1}, "缺少模型名"},
		{"币种非法", map[string]any{"model_name": "glm-5", "currency": "cny", "input_cost": 1},
			"成本币种只能是 credits 或 usd"},
		{"单价为负", map[string]any{"model_name": "glm-5", "input_cost": -1}, "成本单价不能为负数"},
		{"输出成本为负", map[string]any{"model_name": "glm-5", "output_cost": -1}, "成本单价不能为负数"},
		{"按次成本为负", map[string]any{"model_name": "glm-5", "per_call_cost": -1}, "成本单价不能为负数"},
	}
	for _, tc := range costCases {
		t.Run("成本/"+tc.name, func(t *testing.T) {
			resp, env := e.do(t, adminC, "PUT", fmt.Sprintf("/api/admin/channels/%d/costs", chID),
				map[string]any{"costs": []map[string]any{tc.cost}})
			assertFail400(t, resp.StatusCode, env, tc.wantMsg)
		})
	}

	// 不存在渠道：各端点统一 404
	nfCases := []struct {
		name, method, path string
		body               any
	}{
		{"详情", "GET", "/api/admin/channels/99999", nil},
		{"编辑", "PUT", "/api/admin/channels/99999", valid()},
		{"删除", "DELETE", "/api/admin/channels/99999", nil},
		{"改状态", "PUT", "/api/admin/channels/99999/status", map[string]any{"status": "enabled"}},
		{"测试连通", "POST", "/api/admin/channels/99999/test", nil},
		{"设成本", "PUT", "/api/admin/channels/99999/costs",
			map[string]any{"costs": []map[string]any{{"model_name": "glm-5", "input_cost": 1}}}},
	}
	for _, tc := range nfCases {
		t.Run("不存在/"+tc.name, func(t *testing.T) {
			resp, _ := e.do(t, adminC, tc.method, tc.path, tc.body)
			if resp.StatusCode != 404 {
				t.Errorf("应 404，实际 %d", resp.StatusCode)
			}
		})
	}

	// 状态取值校验：仅允许 enabled / manual_disabled
	resp, _ = e.do(t, adminC, "PUT", fmt.Sprintf("/api/admin/channels/%d/status", chID),
		map[string]any{"status": "auto_disabled"})
	if resp.StatusCode != 400 {
		t.Errorf("非法状态应 400，实际 %d", resp.StatusCode)
	}
}

// 渠道协议与模型形态匹配校验：模型目录中的向量/图像模型仅允许配置在
// openai_compat 协议渠道；未进入目录的名称不校验（报告 P3-7）。
func TestAdminChannelProtocolModalityValidation(t *testing.T) {
	e := newTestEnv(t)
	adminC := e.seedAndLogin(t, "chproto", domain.RoleAdmin)

	m := &store.Model{Name: "emb-proto-check", Modality: domain.ModalityEmbedding,
		BillingMode: domain.BillPerToken, Status: domain.ModelEnabled}
	if err := e.db.Create(m).Error; err != nil {
		t.Fatalf("建向量模型失败: %v", err)
	}

	payload := func(name, protocol string, models []string) map[string]any {
		return map[string]any{
			"name": name, "provider": "custom", "protocol": protocol,
			"base_url": "http://127.0.0.1:1", "api_key": "sk-x", "models": models,
		}
	}

	// 创建：anthropic 协议 + 向量模型 → 400，消息指明模型与协议
	resp, env := e.do(t, adminC, "POST", "/api/admin/channels/",
		payload("协议校验-创建", "anthropic", []string{"emb-proto-check"}))
	assertFail400(t, resp.StatusCode, env, "emb-proto-check")

	// 创建：openai_compat 协议 + 向量模型 → 201
	resp, env = e.do(t, adminC, "POST", "/api/admin/channels/",
		payload("协议校验-合法", "openai_compat", []string{"emb-proto-check"}))
	if resp.StatusCode != 201 {
		t.Fatalf("openai_compat 协议应创建成功: %d %v", resp.StatusCode, env)
	}
	chID := int64(env["data"].(map[string]any)["id"].(float64))

	// 编辑：改为 gemini 协议 → 400，渠道协议保持不变
	resp, env = e.do(t, adminC, "PUT", fmt.Sprintf("/api/admin/channels/%d", chID),
		payload("协议校验-合法", "gemini", []string{"emb-proto-check"}))
	assertFail400(t, resp.StatusCode, env, "openai_compat")
	resp, env = e.do(t, adminC, "GET", fmt.Sprintf("/api/admin/channels/%d", chID), nil)
	if resp.StatusCode != 200 {
		t.Fatalf("查询渠道失败: %d %v", resp.StatusCode, env)
	}
	if got := env["data"].(map[string]any)["protocol"]; got != "openai_compat" {
		t.Errorf("编辑被拒后协议应保持 openai_compat，实际 %v", got)
	}

	// 未进入模型目录的名称不校验：anthropic 协议 + 未知模型 → 201
	resp, env = e.do(t, adminC, "POST", "/api/admin/channels/",
		payload("协议校验-未知模型", "anthropic", []string{"not-in-catalog"}))
	if resp.StatusCode != 201 {
		t.Errorf("未进入模型目录的名称不应校验: %d %v", resp.StatusCode, env)
	}
}
