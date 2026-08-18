package api

// /v1 契约固化测试（报告 P2-16）：认证头等价关系、门禁层/业务层错误格式分界、
// 协议范围、上游错误透传、流式帧格式、/v1/key/info 字段契约。
// docs/api-contract.md 的 /v1 章节以本文件断言的实际行为为准。

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// v1Request 向 /v1 路径发起请求，支持自定义认证头。
func (e *testEnv) v1Request(t *testing.T, method, path string, body map[string]any,
	headers map[string]string) (*http.Response, []byte) {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		payload, _ := json.Marshal(body)
		reader = bytes.NewReader(payload)
	} else {
		reader = bytes.NewReader(nil)
	}
	req, _ := http.NewRequest(method, e.srv.URL+path, reader)
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("请求 %s %s 失败: %v", method, path, err)
	}
	defer resp.Body.Close()
	raw := new(bytes.Buffer)
	_, _ = raw.ReadFrom(resp.Body)
	return resp, raw.Bytes()
}

// newOKChatUpstream 返回一个总是成功的 OpenAI 兼容对话上游桩。
func newOKChatUpstream(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"x","model":"glm-5","choices":[{"message":{"content":"ok"}}],
			"usage":{"prompt_tokens":10,"completion_tokens":10}}`)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// 同一 Key 用 Authorization: Bearer 与 x-api-key 均可认证；
// 两头同时提供时 Authorization 优先（x-api-key 为非法值也不影响）。
func TestAuthHeaderEquivalence(t *testing.T) {
	e := newTestEnv(t)
	upstream := newOKChatUpstream(t)
	_, key := e.seedRelayUser(t, "authhdr", 1_000_000, nil)
	e.seedModel(t, "glm-5")
	e.seedChannel(t, "ch-auth", upstream.URL, 0, []string{"glm-5"}, nil)

	chatBody := map[string]any{
		"model": "glm-5", "messages": []map[string]any{{"role": "user", "content": "hi"}},
	}
	cases := []struct {
		name    string
		headers map[string]string
	}{
		{"Authorization Bearer", map[string]string{"Authorization": "Bearer " + key}},
		{"x-api-key", map[string]string{"x-api-key": key}},
		{"Authorization 优先于非法 x-api-key", map[string]string{
			"Authorization": "Bearer " + key, "x-api-key": "tzl-invalid-value"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, body := e.v1Request(t, "POST", "/v1/chat/completions", chatBody, tc.headers)
			if resp.StatusCode != 200 {
				t.Errorf("认证应成功（200），实际 %d %s", resp.StatusCode, body)
			}
		})
	}
}

// 门禁层错误统一 OpenAI 格式，不随端点切换：无效 Key 调 /v1/messages 仍返回 OpenAI 格式。
func TestGuardErrorFormatOnAnthropicEndpoint(t *testing.T) {
	e := newTestEnv(t)
	resp, raw := e.v1Request(t, "POST", "/v1/messages",
		map[string]any{"model": "any", "max_tokens": 10,
			"messages": []map[string]any{{"role": "user", "content": "hi"}}},
		map[string]string{"x-api-key": "tzl-nonexistent-key-000000"})
	if resp.StatusCode != 401 {
		t.Fatalf("无效 Key 应 401，实际 %d %s", resp.StatusCode, raw)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("响应体解析失败: %v %s", err, raw)
	}
	errObj, _ := body["error"].(map[string]any)
	if errObj == nil || errObj["code"] != "invalid_api_key" {
		t.Errorf("门禁层错误应为 OpenAI 格式（error.code=invalid_api_key），实际: %v", body)
	}
	if topType, has := body["type"]; has && topType == "error" {
		t.Errorf("门禁层错误不应切换为 Anthropic 格式（顶层 type=error）: %v", body)
	}
}

// 业务层错误随端点切换：合法 Key 调 /v1/messages 请求未上架模型 → Anthropic 格式 404。
func TestBusinessErrorFormatOnMessages(t *testing.T) {
	e := newTestEnv(t)
	_, key := e.seedRelayUser(t, "bizfmt", 100_000, nil)

	resp, raw := e.v1Request(t, "POST", "/v1/messages",
		map[string]any{"model": "ghost-model", "max_tokens": 10,
			"messages": []map[string]any{{"role": "user", "content": "hi"}}},
		map[string]string{"x-api-key": key})
	if resp.StatusCode != 404 {
		t.Fatalf("未上架模型应 404，实际 %d %s", resp.StatusCode, raw)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("响应体解析失败: %v %s", err, raw)
	}
	if body["type"] != "error" {
		t.Errorf("/v1/messages 业务错误应为 Anthropic 格式（顶层 type=error），实际: %v", body)
	}
	errObj, _ := body["error"].(map[string]any)
	if errObj == nil || errObj["type"] != "model_not_found" {
		t.Errorf("error.type 应为 model_not_found，实际: %v", body)
	}
	if _, hasCode := errObj["code"]; hasCode {
		t.Errorf("Anthropic 格式不应含 error.code 字段: %v", errObj)
	}
}

// 模型已上架但未配置任何渠道 → 503 no_channel，预扣全额退款。
func TestNoChannelForModel(t *testing.T) {
	e := newTestEnv(t)
	userID, key := e.seedRelayUser(t, "nochan", 100_000, nil)
	e.seedModel(t, "glm-5") // 不建渠道

	resp, raw := e.v1Request(t, "POST", "/v1/chat/completions",
		map[string]any{"model": "glm-5",
			"messages": []map[string]any{{"role": "user", "content": "hi"}}},
		map[string]string{"Authorization": "Bearer " + key})
	if resp.StatusCode != 503 {
		t.Fatalf("无渠道应 503，实际 %d %s", resp.StatusCode, raw)
	}
	if !strings.Contains(string(raw), "no_channel") {
		t.Errorf("业务码应为 no_channel，实际 %s", raw)
	}
	// 预扣已全额退款：余额回到初始值
	if bal := e.userBalance(t, userID); bal != 100_000 {
		t.Errorf("应全额退款，期望余额 100000，实际 %d", bal)
	}
	log := e.waitUsageLog(t, userID)
	if log.Status != domain.UsageRefunded {
		t.Errorf("用量日志应 refunded，实际 %s", log.Status)
	}
	e.assertReconcile(t)
}

// 协议范围：/v1/embeddings 仅走 openai_compat 渠道，向量模型仅配置 anthropic
// 协议渠道时返回 503 channel_protocol_unsupported（区别于无渠道的 no_channel，报告 P3-7）。
func TestEmbeddingsProtocolScope(t *testing.T) {
	e := newTestEnv(t)
	userID, key := e.seedRelayUser(t, "embscope", 100_000, nil)
	m := &store.Model{Name: "text-emb-scope", Modality: domain.ModalityEmbedding,
		BillingMode: domain.BillPerToken, Status: domain.ModelEnabled}
	if err := e.db.Create(m).Error; err != nil {
		t.Fatalf("建模型失败: %v", err)
	}
	if err := e.db.Create(&store.ModelPrice{ModelID: m.ID, InputPrice: 500_000}).Error; err != nil {
		t.Fatalf("建价格失败: %v", err)
	}
	// 唯一渠道是 anthropic 协议
	e.seedChannelProto(t, "emb-anth-ch", "http://127.0.0.1:1",
		domain.ProtocolAnthropic, []string{"text-emb-scope"}, nil)

	resp, raw := e.v1Request(t, "POST", "/v1/embeddings",
		map[string]any{"model": "text-emb-scope", "input": "文本"},
		map[string]string{"Authorization": "Bearer " + key})
	if resp.StatusCode != 503 {
		t.Fatalf("仅 anthropic 渠道时 embeddings 应 503，实际 %d %s", resp.StatusCode, raw)
	}
	if !strings.Contains(string(raw), "channel_protocol_unsupported") {
		t.Errorf("业务码应为 channel_protocol_unsupported，实际 %s", raw)
	}
	if bal := e.userBalance(t, userID); bal != 100_000 {
		t.Errorf("应全额退款，期望余额 100000，实际 %d", bal)
	}
	e.assertReconcile(t)

	// 对照：同模型完全无启用渠道时仍为 no_channel
	if err := e.db.Exec("DELETE FROM channels").Error; err != nil {
		t.Fatalf("清空渠道失败: %v", err)
	}
	resp, raw = e.v1Request(t, "POST", "/v1/embeddings",
		map[string]any{"model": "text-emb-scope", "input": "文本"},
		map[string]string{"Authorization": "Bearer " + key})
	if resp.StatusCode != 503 {
		t.Fatalf("无渠道时 embeddings 应 503，实际 %d %s", resp.StatusCode, raw)
	}
	if !strings.Contains(string(raw), "no_channel") {
		t.Errorf("业务码应为 no_channel，实际 %s", raw)
	}
}

// 上游 400 + {"error":{...}} → 下游收到 400 且错误体逐字段一致、用量日志 refunded。
func TestUpstreamErrorBodyPassthrough(t *testing.T) {
	e := newTestEnv(t)
	upstreamErr := `{"error":{"message":"context length exceeded",` +
		`"type":"invalid_request_error","param":null,"code":"context_length_exceeded"}}`
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(400)
		fmt.Fprint(w, upstreamErr)
	}))
	defer upstream.Close()

	userID, key := e.seedRelayUser(t, "passbody", 100_000, nil)
	e.seedModel(t, "glm-5")
	e.seedChannel(t, "ch-pass", upstream.URL, 0, []string{"glm-5"}, nil)

	resp, raw := e.v1Request(t, "POST", "/v1/chat/completions",
		map[string]any{"model": "glm-5",
			"messages": []map[string]any{{"role": "user", "content": "hi"}}},
		map[string]string{"Authorization": "Bearer " + key})
	if resp.StatusCode != 400 {
		t.Fatalf("应透传上游 400，实际 %d %s", resp.StatusCode, raw)
	}
	var got, want map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("下游响应体解析失败: %v %s", err, raw)
	}
	_ = json.Unmarshal([]byte(upstreamErr), &want)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("上游错误体应原样透传。\n期望: %v\n实际: %v", want, got)
	}
	log := e.waitUsageLog(t, userID)
	if log.Status != domain.UsageRefunded {
		t.Errorf("用量日志应 refunded，实际 %s", log.Status)
	}
	e.assertReconcile(t)
}

// OpenAI 下游流式帧格式：text/event-stream、每帧 data: 前缀、终帧 [DONE]、model 改写为公开名。
func TestStreamResponseFraming(t *testing.T) {
	e := newTestEnv(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		for _, c := range []string{
			`{"id":"c","model":"glm-upstream","choices":[{"delta":{"content":"你好"}}]}`,
			`{"id":"c","model":"glm-upstream","choices":[{"delta":{},"finish_reason":"stop"}]}`,
			`{"id":"c","model":"glm-upstream","choices":[],"usage":{"prompt_tokens":100,"completion_tokens":20}}`,
		} {
			fmt.Fprintf(w, "data: %s\n\n", c)
			fl.Flush()
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
		fl.Flush()
	}))
	defer upstream.Close()

	userID, key := e.seedRelayUser(t, "framing", 1_000_000, nil)
	e.seedModel(t, "pub-stream-model")
	e.seedChannel(t, "ch-frame", upstream.URL, 0, []string{"pub-stream-model"},
		map[string]string{"pub-stream-model": "glm-upstream"})

	payload, _ := json.Marshal(map[string]any{
		"model": "pub-stream-model", "stream": true,
		"messages": []map[string]any{{"role": "user", "content": "hi"}},
	})
	req, _ := http.NewRequest("POST", e.srv.URL+"/v1/chat/completions", bytes.NewReader(payload))
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("流式请求失败: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("应成功: %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Errorf("Content-Type 应为 text/event-stream，实际 %q", ct)
	}
	sc := bufio.NewScanner(resp.Body)
	var dataLines []string
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "data: ") {
			t.Errorf("OpenAI 下游每个非空行应以 data: 开头，实际 %q", line)
			continue
		}
		dataLines = append(dataLines, strings.TrimPrefix(line, "data: "))
	}
	if len(dataLines) == 0 || dataLines[len(dataLines)-1] != "[DONE]" {
		t.Fatalf("终帧应为 data: [DONE]，实际帧序列: %v", dataLines)
	}
	for i, d := range dataLines[:len(dataLines)-1] {
		var chunk map[string]any
		if err := json.Unmarshal([]byte(d), &chunk); err != nil {
			t.Errorf("第 %d 帧不是合法 JSON: %s", i, d)
			continue
		}
		if chunk["model"] != "pub-stream-model" {
			t.Errorf("第 %d 帧 model 应改写为公开名 pub-stream-model，实际 %v", i, chunk["model"])
		}
	}
	if strings.Contains(strings.Join(dataLines, "\n"), "glm-upstream") {
		t.Error("流式响应不应泄露上游模型名")
	}
	// 用量结算完成（确认 usage chunk 被嗅探）
	log := e.waitUsageLog(t, userID)
	if log.Status != domain.UsageSettled || log.PromptTokens != 100 {
		t.Errorf("流式结算错误: %s prompt=%d", log.Status, log.PromptTokens)
	}
	e.assertReconcile(t)
}

// /v1/key/info 六字段契约：无限额时 key_credit_remaining 为 null；remaining 负值截断为 0。
func TestKeyInfoFieldContract(t *testing.T) {
	e := newTestEnv(t)

	keyInfo := func(key string) map[string]any {
		resp, raw := e.v1Request(t, "GET", "/v1/key/info", nil,
			map[string]string{"Authorization": "Bearer " + key})
		if resp.StatusCode != 200 {
			t.Fatalf("key/info 应 200，实际 %d %s", resp.StatusCode, raw)
		}
		var info map[string]any
		if err := json.Unmarshal(raw, &info); err != nil {
			t.Fatalf("key/info 解析失败: %v %s", err, raw)
		}
		return info
	}

	// 分支一：无限额密钥
	_, unlimitedKey := e.seedRelayUser(t, "kinfo-unlimited", 500_000, nil)
	info := keyInfo(unlimitedKey)
	for _, field := range []string{"key_name", "key_credit_limit", "key_credit_used",
		"key_credit_remaining", "user_credit_balance", "user_credit_used"} {
		if _, ok := info[field]; !ok {
			t.Errorf("key/info 应含字段 %s，实际: %v", field, info)
		}
	}
	if info["key_name"] != "relay-key" {
		t.Errorf("key_name 应为 relay-key，实际 %v", info["key_name"])
	}
	if info["key_credit_limit"] != nil {
		t.Errorf("无限额时 key_credit_limit 应为 null，实际 %v", info["key_credit_limit"])
	}
	if info["key_credit_remaining"] != nil {
		t.Errorf("无限额时 key_credit_remaining 应为 null，实际 %v", info["key_credit_remaining"])
	}
	if v, _ := info["user_credit_balance"].(float64); int64(v) != 500_000 {
		t.Errorf("user_credit_balance 应为 500000，实际 %v", info["user_credit_balance"])
	}
	if v, _ := info["user_credit_used"].(float64); int64(v) != 0 {
		t.Errorf("user_credit_used 应为 0，实际 %v", info["user_credit_used"])
	}

	// 分支二：限额 100、已用 150 → remaining 截断为 0
	limit := int64(100)
	userID2, limitedKey := e.seedRelayUser(t, "kinfo-limited", 500_000, &limit)
	if err := e.db.Model(&store.APIKey{}).Where("user_id = ?", userID2).
		Update("credit_used", 150).Error; err != nil {
		t.Fatalf("预置已用额度失败: %v", err)
	}
	info = keyInfo(limitedKey)
	if v, _ := info["key_credit_limit"].(float64); int64(v) != 100 {
		t.Errorf("key_credit_limit 应为 100，实际 %v", info["key_credit_limit"])
	}
	if v, _ := info["key_credit_used"].(float64); int64(v) != 150 {
		t.Errorf("key_credit_used 应为 150，实际 %v", info["key_credit_used"])
	}
	rem, ok := info["key_credit_remaining"].(float64)
	if !ok || int64(rem) != 0 {
		t.Errorf("超用时 key_credit_remaining 应截断为 0，实际 %v", info["key_credit_remaining"])
	}
}

// 客户端中途断连不中止计费：上游请求上下文与下游连接解耦（架构决策 2026-08-05 第 3 项），
// 服务端继续读完上游流，按流末 usage chunk 的真实用量结算，不退化为字节估算。
func TestStreamClientDisconnectStillSettlesRealUsage(t *testing.T) {
	e := newTestEnv(t)

	clientGone := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		fmt.Fprint(w, `data: {"id":"c","model":"glm-up","choices":[{"delta":{"content":"hi"}}]}`+"\n\n")
		fl.Flush()
		// 等测试端确认客户端已断连，并留出取消传播时间，再发剩余帧与 usage
		<-clientGone
		time.Sleep(300 * time.Millisecond)
		for _, c := range []string{
			`{"id":"c","model":"glm-up","choices":[{"delta":{},"finish_reason":"stop"}]}`,
			`{"id":"c","model":"glm-up","choices":[],"usage":{"prompt_tokens":77,"completion_tokens":33}}`,
		} {
			fmt.Fprintf(w, "data: %s\n\n", c)
			fl.Flush()
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
		fl.Flush()
	}))
	defer upstream.Close()

	userID, key := e.seedRelayUser(t, "streamdisc", 1_000_000, nil)
	e.seedModel(t, "pub-disc-model")
	e.seedChannel(t, "ch-disc", upstream.URL, 0, []string{"pub-disc-model"},
		map[string]string{"pub-disc-model": "glm-up"})

	payload, _ := json.Marshal(map[string]any{
		"model": "pub-disc-model", "stream": true,
		"messages": []map[string]any{{"role": "user", "content": "hi"}},
	})
	reqCtx, cancelReq := context.WithCancel(context.Background())
	defer cancelReq()
	req, _ := http.NewRequestWithContext(reqCtx, "POST",
		e.srv.URL+"/v1/chat/completions", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("流式请求失败: %v", err)
	}
	sc := bufio.NewScanner(resp.Body)
	gotFirst := false
	for sc.Scan() {
		if strings.HasPrefix(sc.Text(), "data: ") {
			gotFirst = true
			break
		}
	}
	if !gotFirst {
		t.Fatal("未收到首帧")
	}
	// 模拟客户端断连：取消请求上下文并关闭响应体
	cancelReq()
	resp.Body.Close()
	close(clientGone)

	log := e.waitUsageLog(t, userID)
	if log.Status != domain.UsageSettled {
		t.Fatalf("断连后应按真实用量结算，实际状态 %s（错误: %s）", log.Status, log.ErrorMessage)
	}
	if log.UsageEstimated {
		t.Error("断连后不应退化为估算计费")
	}
	if log.PromptTokens != 77 || log.CompletionTokens != 33 {
		t.Errorf("应采用流末 usage chunk 的真实用量 77/33，实际 %d/%d",
			log.PromptTokens, log.CompletionTokens)
	}
	e.assertReconcile(t)
}

// Key 配置 allowed_models 白名单后请求白名单外模型 → 403 model_not_allowed（OpenAI 格式）。
// 该失败发生在预扣之前：不产生新流水、余额不变。
func TestModelNotAllowed(t *testing.T) {
	e := newTestEnv(t)
	upstream := newOKChatUpstream(t)
	userID, key := e.seedRelayUser(t, "allowlist", 100_000, nil)
	e.seedModel(t, "glm-5")
	e.seedChannel(t, "ch-allow", upstream.URL, 0, []string{"glm-5"}, nil)
	if err := e.db.Model(&store.APIKey{}).Where("user_id = ?", userID).
		Update("allowed_models", `["other-model"]`).Error; err != nil {
		t.Fatalf("设置白名单失败: %v", err)
	}

	var ledgerBefore int64
	e.db.Model(&store.LedgerEntry{}).Where("user_id = ?", userID).Count(&ledgerBefore)

	resp, raw := e.v1Request(t, "POST", "/v1/chat/completions",
		map[string]any{"model": "glm-5",
			"messages": []map[string]any{{"role": "user", "content": "hi"}}},
		map[string]string{"Authorization": "Bearer " + key})
	if resp.StatusCode != 403 {
		t.Fatalf("白名单外模型应 403，实际 %d %s", resp.StatusCode, raw)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("响应体解析失败: %v %s", err, raw)
	}
	errObj, _ := body["error"].(map[string]any)
	if errObj == nil || errObj["code"] != "model_not_allowed" {
		t.Errorf("应为 OpenAI 格式 error.code=model_not_allowed，实际: %v", body)
	}
	if bal := e.userBalance(t, userID); bal != 100_000 {
		t.Errorf("预扣前失败余额应不变（100000），实际 %d", bal)
	}
	var ledgerAfter int64
	e.db.Model(&store.LedgerEntry{}).Where("user_id = ?", userID).Count(&ledgerAfter)
	if ledgerAfter != ledgerBefore {
		t.Errorf("预扣前失败不应产生新流水，之前 %d 条，之后 %d 条", ledgerBefore, ledgerAfter)
	}
}

// 上游 401（渠道级致命错误）不透传：换渠道重试，候选耗尽后下游收到 502 upstream_error，
// 预扣全额退款、用量日志 refunded、对账通过——错误码表"例外：致命 4xx 不透传"行。
func TestFatalUpstreamErrorRetryExhausted(t *testing.T) {
	e := newTestEnv(t)
	upstreamErr := `{"error":{"message":"Incorrect API key provided",` +
		`"type":"invalid_request_error","code":"invalid_api_key"}}`
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(401)
		fmt.Fprint(w, upstreamErr)
	}))
	defer upstream.Close()

	userID, key := e.seedRelayUser(t, "fatal401", 100_000, nil)
	e.seedModel(t, "glm-5")
	e.seedChannel(t, "ch-fatal", upstream.URL, 0, []string{"glm-5"}, nil)

	resp, raw := e.v1Request(t, "POST", "/v1/chat/completions",
		map[string]any{"model": "glm-5",
			"messages": []map[string]any{{"role": "user", "content": "hi"}}},
		map[string]string{"Authorization": "Bearer " + key})
	if resp.StatusCode != 502 {
		t.Fatalf("致命 4xx 候选耗尽应 502，实际 %d %s", resp.StatusCode, raw)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("响应体解析失败: %v %s", err, raw)
	}
	errObj, _ := body["error"].(map[string]any)
	if errObj == nil || errObj["code"] != "upstream_error" {
		t.Errorf("业务码应为 upstream_error，实际: %v", body)
	}
	if msg, _ := errObj["message"].(string); strings.Contains(msg, "invalid_api_key") {
		t.Errorf("致命 4xx 不应透传上游错误体，实际 message: %q", msg)
	}
	if bal := e.userBalance(t, userID); bal != 100_000 {
		t.Errorf("应全额退款，期望余额 100000，实际 %d", bal)
	}
	log := e.waitUsageLog(t, userID)
	if log.Status != domain.UsageRefunded {
		t.Errorf("用量日志应 refunded，实际 %s", log.Status)
	}
	e.assertReconcile(t)
}

// Authorization 头省略 Bearer 前缀（直接放 Key 明文）也可认证成功——文档声明的兼容行为。
func TestAuthorizationWithoutBearerPrefix(t *testing.T) {
	e := newTestEnv(t)
	upstream := newOKChatUpstream(t)
	_, key := e.seedRelayUser(t, "nobearer", 1_000_000, nil)
	e.seedModel(t, "glm-5")
	e.seedChannel(t, "ch-nobearer", upstream.URL, 0, []string{"glm-5"}, nil)

	resp, raw := e.v1Request(t, "POST", "/v1/chat/completions",
		map[string]any{"model": "glm-5",
			"messages": []map[string]any{{"role": "user", "content": "hi"}}},
		map[string]string{"Authorization": key})
	if resp.StatusCode != 200 {
		t.Errorf("省略 Bearer 前缀应认证成功（200），实际 %d %s", resp.StatusCode, raw)
	}
}

// /v1 下未匹配的路由返回 404 站点信封 {success:false, message:"接口不存在"}，
// 不采用 OpenAI 错误格式——文档例外条款。
func TestUnmatchedV1RouteSiteEnvelope(t *testing.T) {
	e := newTestEnv(t)
	resp, raw := e.v1Request(t, "GET", "/v1/nonexistent", nil, nil)
	if resp.StatusCode != 404 {
		t.Fatalf("未匹配路由应 404，实际 %d %s", resp.StatusCode, raw)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("响应体解析失败: %v %s", err, raw)
	}
	if success, ok := body["success"].(bool); !ok || success {
		t.Errorf("应为站点信封 success=false，实际: %v", body)
	}
	if body["message"] != "接口不存在" {
		t.Errorf("message 应为 接口不存在，实际: %v", body["message"])
	}
	if _, hasErr := body["error"]; hasErr {
		t.Errorf("未匹配路由不应输出 OpenAI 错误格式: %v", body)
	}
}
