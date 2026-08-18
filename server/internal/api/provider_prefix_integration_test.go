package api

// provider 前缀路由（/{provider}/v1/*）的 API 层集成测试。
// 经完整 HTTP 链路（httptest Server + 真实路由 + 真实 DB）验证：
//   - /{provider}/v1/models 只返回该 provider 且有同 provider 渠道承载的模型
//   - 未知 provider slug 返回 404 provider_not_found
//   - /{provider}/v1/key/info 挂载且 provider 前缀 no-op（响应与 /v1/key/info 一致）
//   - provider 与 model 不一致返回 400 model_provider_mismatch
//
// 依赖 TZL_TEST_DATABASE_URL；共用测试库，跨包必须 -p 1 串行。
// 本文件由主会话统一串行运行，不在本会话内执行。

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// seedModelWithProvider 建模型并指定 provider（默认 seedModel 不设 provider）。
func (e *testEnv) seedModelWithProvider(t *testing.T, name string, provider domain.Provider) int64 {
	t.Helper()
	id := e.seedModel(t, name)
	if err := e.db.Model(&store.Model{}).Where("id = ?", id).
		Update("provider", string(provider)).Error; err != nil {
		t.Fatalf("改模型 provider 失败: %v", err)
	}
	return id
}

// seedChannelWithProvider 建渠道并指定 provider（默认 seedChannel 固定 zhipu）。
func (e *testEnv) seedChannelWithProvider(t *testing.T, name string, provider domain.Provider,
	models []string) int64 {
	t.Helper()
	id := e.seedChannel(t, name, "http://unused.example", 0, models, nil)
	if err := e.db.Model(&store.Channel{}).Where("id = ?", id).
		Update("provider", string(provider)).Error; err != nil {
		t.Fatalf("改渠道 provider 失败: %v", err)
	}
	return id
}

// v1Get 用 API Key 发起 GET 请求到指定路径。
func (e *testEnv) v1Get(t *testing.T, apiKey, path string) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest("GET", e.srv.URL+path, nil)
	req.Header.Set("Authorization", "Bearer "+apiKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("请求 %s 失败: %v", path, err)
	}
	defer resp.Body.Close()
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	return resp.StatusCode, body
}

// v1Post 用 API Key 发起 POST 请求到指定路径（带 JSON body）。
func (e *testEnv) v1Post(t *testing.T, apiKey, path string, body map[string]any) (int, map[string]any) {
	t.Helper()
	payload, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", e.srv.URL+path, strings.NewReader(string(payload)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("请求 %s 失败: %v", path, err)
	}
	defer resp.Body.Close()
	var raw map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&raw)
	return resp.StatusCode, raw
}

// modelIDsOf 从 /v1/models 响应体提取模型 id 列表。
func modelIDsOf(t *testing.T, body map[string]any) []string {
	t.Helper()
	data, _ := body["data"].([]any)
	ids := make([]string, 0, len(data))
	for _, it := range data {
		if m, ok := it.(map[string]any); ok {
			if id, ok := m["id"].(string); ok {
				ids = append(ids, id)
			}
		}
	}
	return ids
}

// 【集成】/{provider}/v1/models 只返回该 provider 且有同 provider 渠道承载的模型。
// 模型：claude-test（anthropic）、gpt-test（openai）、glm-test（zhipu）。
// 渠道：anthropic 渠道承载 claude-test；openai 渠道承载 gpt-test；glm-test 无渠道承载。
// 请求 /anthropic/v1/models → 仅 claude-test；/openai/v1/models → 仅 gpt-test。
func TestProviderPrefixModelsFiltersByProvider(t *testing.T) {
	e := newTestEnv(t)
	_, key := e.seedRelayUser(t, "pf-models", 1_000_000, nil)
	e.seedModelWithProvider(t, "claude-test", domain.ProviderAnthropic)
	e.seedModelWithProvider(t, "gpt-test", domain.ProviderOpenAI)
	e.seedModelWithProvider(t, "glm-test", domain.ProviderZhipu)
	e.seedChannelWithProvider(t, "ch-anth", domain.ProviderAnthropic, []string{"claude-test"})
	e.seedChannelWithProvider(t, "ch-openai", domain.ProviderOpenAI, []string{"gpt-test"})
	// glm-test 故意不配渠道，验证「有模型无渠道」从清单剔除

	// /anthropic/v1/models 只返回 claude-test
	status, body := e.v1Get(t, key, "/anthropic/v1/models")
	if status != 200 {
		t.Fatalf("/anthropic/v1/models 应 200，实际 %d %v", status, body)
	}
	ids := modelIDsOf(t, body)
	if len(ids) != 1 || ids[0] != "claude-test" {
		t.Fatalf("/anthropic/v1/models 应只含 claude-test，实际 %v", ids)
	}

	// /openai/v1/models 只返回 gpt-test
	status, body = e.v1Get(t, key, "/openai/v1/models")
	if status != 200 {
		t.Fatalf("/openai/v1/models 应 200，实际 %d %v", status, body)
	}
	ids = modelIDsOf(t, body)
	if len(ids) != 1 || ids[0] != "gpt-test" {
		t.Fatalf("/openai/v1/models 应只含 gpt-test，实际 %v", ids)
	}

	// 别名归一：/kimi/v1/models 与 /moonshot/v1/models 等价（无 moonshot 模型时返回空列表，不报错）
	status, body = e.v1Get(t, key, "/kimi/v1/models")
	if status != 200 {
		t.Fatalf("/kimi/v1/models 应 200（无模型时返回空列表），实际 %d %v", status, body)
	}
	ids = modelIDsOf(t, body)
	if len(ids) != 0 {
		t.Fatalf("/kimi/v1/models 应返回空列表（无 moonshot 模型），实际 %v", ids)
	}
}

// 【集成】未知 provider slug 返回 404 provider_not_found。
// 该判定在认证与限流之前——即使带合法 Key，未知 slug 仍 404。
func TestProviderPrefixUnknownSlugReturns404(t *testing.T) {
	e := newTestEnv(t)
	_, key := e.seedRelayUser(t, "pf-unknown", 1_000_000, nil)

	status, body := e.v1Get(t, key, "/unknown/v1/models")
	if status != 404 {
		t.Fatalf("未知 slug 应 404，实际 %d %v", status, body)
	}
	if code := errCodeOf(body); code != domain.ErrCodeProviderNotFound {
		t.Fatalf("期望错误码 %s，实际 %q；body=%v",
			domain.ErrCodeProviderNotFound, code, body)
	}
}

// 【集成】/{provider}/v1/key/info 挂载且 provider 前缀 no-op：
// 响应与 /v1/key/info 完全一致（key 信息是账号级，与 provider 无关）。
func TestProviderPrefixKeyInfoIsNoOp(t *testing.T) {
	e := newTestEnv(t)
	_, key := e.seedRelayUser(t, "pf-keyinfo", 1_000_000, nil)

	status1, body1 := e.v1Get(t, key, "/v1/key/info")
	status2, body2 := e.v1Get(t, key, "/anthropic/v1/key/info")
	if status1 != 200 || status2 != 200 {
		t.Fatalf("key/info 应 200（%d / %d）", status1, status2)
	}
	// key_name 与余额等关键字段一致
	if body1["key_name"] != body2["key_name"] {
		t.Errorf("key_name 不一致: %v vs %v", body1["key_name"], body2["key_name"])
	}
	if body1["user_credit_balance"] != body2["user_credit_balance"] {
		t.Errorf("user_credit_balance 不一致: %v vs %v",
			body1["user_credit_balance"], body2["user_credit_balance"])
	}
}

// 【集成】provider 与 model 不一致返回 400 model_provider_mismatch：
// 模型 claude-test 归属 anthropic；请求 /openai/v1/chat/completions body {model: claude-test}。
func TestProviderPrefixModelMismatchHTTP(t *testing.T) {
	e := newTestEnv(t)
	_, key := e.seedRelayUser(t, "pf-mismatch", 1_000_000, nil)
	e.seedModelWithProvider(t, "claude-test", domain.ProviderAnthropic)
	e.seedChannelWithProvider(t, "ch-anth", domain.ProviderAnthropic, []string{"claude-test"})

	status, body := e.v1Post(t, key, "/openai/v1/chat/completions",
		map[string]any{"model": "claude-test",
			"messages": []map[string]any{{"role": "user", "content": "hi"}}})
	if status != 400 {
		t.Fatalf("期望 400，实际 %d %v", status, body)
	}
	if code := errCodeOf(body); code != domain.ErrCodeModelProviderMismatch {
		t.Fatalf("期望错误码 %s，实际 %q；body=%v",
			domain.ErrCodeModelProviderMismatch, code, body)
	}
}

// errCodeOf 从 OpenAI 错误响应体提取 error.code。
func errCodeOf(body map[string]any) string {
	if errObj, ok := body["error"].(map[string]any); ok {
		if c, ok := errObj["code"].(string); ok {
			return c
		}
	}
	return ""
}
