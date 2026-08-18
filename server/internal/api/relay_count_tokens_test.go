package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// countTokens 以 API Key 调用计数端点，返回状态码与解析后的响应体。
func (e *testEnv) countTokens(t *testing.T, apiKey string, body map[string]any) (int, map[string]any) {
	t.Helper()
	payload, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", e.srv.URL+"/v1/messages/count_tokens", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", apiKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("计数请求失败: %v", err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

// 有 anthropic 协议渠道承载时，计数结果取自上游，且模型名按渠道映射改写。
func TestCountTokensForwardsToAnthropicChannel(t *testing.T) {
	e := newTestEnv(t)
	var gotPath, gotVersion string
	var gotBody map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotVersion = r.Header.Get("anthropic-version")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		fmt.Fprint(w, `{"input_tokens":1234}`)
	}))
	defer upstream.Close()

	userID, key := e.seedRelayUser(t, "counter1", 1_000_000, nil)
	e.seedModel(t, "claude-sonnet-4-6")
	e.seedChannelProto(t, "count-ch", upstream.URL, domain.ProtocolAnthropic,
		[]string{"claude-sonnet-4-6"}, map[string]string{"claude-sonnet-4-6": "glm-4.7-anthropic"})

	status, out := e.countTokens(t, key, map[string]any{
		"model":    "claude-sonnet-4-6",
		"messages": []map[string]any{{"role": "user", "content": "你好"}},
	})
	if status != http.StatusOK {
		t.Fatalf("计数应 200，实际 %d：%v", status, out)
	}
	if out["input_tokens"] != float64(1234) {
		t.Errorf("应返回上游计数 1234，实际 %v", out["input_tokens"])
	}
	if gotPath != "/v1/messages/count_tokens" {
		t.Errorf("上游路径错误: %s", gotPath)
	}
	if gotVersion != "2023-06-01" {
		t.Errorf("缺少 anthropic-version 头，实际 %q", gotVersion)
	}
	if gotBody["model"] != "glm-4.7-anthropic" {
		t.Errorf("模型名应按渠道映射改写，实际 %v", gotBody["model"])
	}

	// 计数不消耗上游 token：不扣积分、不写用量日志。
	if bal := e.userBalance(t, userID); bal != 1_000_000 {
		t.Errorf("计数不应扣费，余额应为 1000000，实际 %d", bal)
	}
	var logCount int64
	if err := e.db.Model(&store.UsageLog{}).Where("user_id = ?", userID).
		Count(&logCount).Error; err != nil {
		t.Fatalf("查询用量日志失败: %v", err)
	}
	if logCount != 0 {
		t.Errorf("计数不应写用量日志，实际 %d 条", logCount)
	}
}

// 没有 anthropic 协议渠道时回落到本地估算：跨协议渠道上的 Anthropic 客户端
// 仍能拿到可用数值，而不是收到错误。
func TestCountTokensFallsBackToLocalEstimate(t *testing.T) {
	e := newTestEnv(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("没有 anthropic 渠道时不应向上游发起计数请求")
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer upstream.Close()

	_, key := e.seedRelayUser(t, "counter2", 1_000_000, nil)
	e.seedModel(t, "gpt-4o-mini")
	e.seedChannelProto(t, "openai-ch", upstream.URL, domain.ProtocolOpenAICompat,
		[]string{"gpt-4o-mini"}, nil)

	status, out := e.countTokens(t, key, map[string]any{
		"model":    "gpt-4o-mini",
		"messages": []map[string]any{{"role": "user", "content": "0123456789ABCDEF"}},
	})
	if status != http.StatusOK {
		t.Fatalf("计数应 200，实际 %d：%v", status, out)
	}
	tokens, ok := out["input_tokens"].(float64)
	if !ok || tokens <= 0 {
		t.Fatalf("回落估算应返回正整数，实际 %v", out["input_tokens"])
	}
}

// 上游未实现计数端点时同样回落到本地估算，且不因此禁用渠道——
// 据此禁用会连带切断该渠道正常的对话流量。
func TestCountTokensUpstreamFailureDoesNotDisableChannel(t *testing.T) {
	e := newTestEnv(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"type":"error","error":{"type":"not_found_error","message":"unknown path"}}`)
	}))
	defer upstream.Close()

	_, key := e.seedRelayUser(t, "counter3", 1_000_000, nil)
	e.seedModel(t, "claude-sonnet-4-6")
	chID := e.seedChannelProto(t, "nocount-ch", upstream.URL, domain.ProtocolAnthropic,
		[]string{"claude-sonnet-4-6"}, nil)

	// 连续多次失败也不应触发渠道自动禁用。
	for i := 0; i < 5; i++ {
		status, out := e.countTokens(t, key, map[string]any{
			"model":    "claude-sonnet-4-6",
			"messages": []map[string]any{{"role": "user", "content": "你好世界"}},
		})
		if status != http.StatusOK {
			t.Fatalf("第 %d 次计数应回落为 200，实际 %d：%v", i+1, status, out)
		}
		if tokens, ok := out["input_tokens"].(float64); !ok || tokens <= 0 {
			t.Fatalf("第 %d 次回落估算应返回正整数，实际 %v", i+1, out["input_tokens"])
		}
	}

	var ch store.Channel
	if err := e.db.First(&ch, chID).Error; err != nil {
		t.Fatalf("查询渠道失败: %v", err)
	}
	if ch.Status != domain.ChannelEnabled {
		t.Errorf("计数失败不应禁用渠道，实际状态 %s", ch.Status)
	}
}

// 计数端点与对话端点使用同一套模型策略：密钥无权访问的模型一律 403，
// 否则计数端点会变成探测「系统里有哪些模型」的旁路。
func TestCountTokensHonorsKeyModelPolicy(t *testing.T) {
	e := newTestEnv(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"input_tokens":10}`)
	}))
	defer upstream.Close()

	userC := e.seedAndLogin(t, "policyuser", domain.RoleUser)
	e.seedModel(t, "claude-sonnet-4-6")
	e.seedModel(t, "claude-opus-4-6")
	e.seedChannelProto(t, "policy-ch", upstream.URL, domain.ProtocolAnthropic,
		[]string{"claude-sonnet-4-6", "claude-opus-4-6"}, nil)

	resp, env := e.do(t, userC, "POST", "/api/me/keys/", map[string]any{
		"name": "limited", "allowed_models": []string{"claude-sonnet-4-6"},
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("建 Key 应 201，实际 %d：%v", resp.StatusCode, env)
	}
	key := env["data"].(map[string]any)["key"].(string)

	status, _ := e.countTokens(t, key, map[string]any{
		"model":    "claude-sonnet-4-6",
		"messages": []map[string]any{{"role": "user", "content": "你好"}},
	})
	if status != http.StatusOK {
		t.Errorf("白名单内模型计数应 200，实际 %d", status)
	}

	status, out := e.countTokens(t, key, map[string]any{
		"model":    "claude-opus-4-6",
		"messages": []map[string]any{{"role": "user", "content": "你好"}},
	})
	if status != http.StatusForbidden {
		t.Errorf("白名单外模型计数应 403，实际 %d：%v", status, out)
	}
}

// 缺少 model 字段返回 Anthropic 格式的 400，而非 OpenAI 格式：
// 客户端按下游协议解析错误体，格式不符会让报错内容丢失。
func TestCountTokensRejectsMissingModel(t *testing.T) {
	e := newTestEnv(t)
	_, key := e.seedRelayUser(t, "counter4", 1_000_000, nil)

	status, out := e.countTokens(t, key, map[string]any{
		"messages": []map[string]any{{"role": "user", "content": "你好"}},
	})
	if status != http.StatusBadRequest {
		t.Fatalf("缺少 model 应 400，实际 %d：%v", status, out)
	}
	if out["type"] != "error" {
		t.Errorf("错误体应为 Anthropic 格式，实际 %v", out)
	}
}
