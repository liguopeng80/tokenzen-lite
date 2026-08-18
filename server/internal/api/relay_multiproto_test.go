package api

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// seedChannelProto 指定协议的渠道。
func (e *testEnv) seedChannelProto(t *testing.T, name, baseURL string,
	protocol domain.ChannelProtocol, models []string, mapping map[string]string) int64 {
	t.Helper()
	id := e.seedChannel(t, name, baseURL, 0, models, mapping)
	if err := e.db.Model(&store.Channel{}).Where("id = ?", id).
		Update("protocol", protocol).Error; err != nil {
		t.Fatalf("改协议失败: %v", err)
	}
	return id
}

// Anthropic 下游 × Anthropic 上游直通（非流式）。
func TestMessagesAnthropicPassthrough(t *testing.T) {
	e := newTestEnv(t)
	var gotAPIKeyHeader, gotVersion string
	var gotBody map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAPIKeyHeader = r.Header.Get("x-api-key")
		gotVersion = r.Header.Get("anthropic-version")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		fmt.Fprint(w, `{"id":"msg_1","type":"message","role":"assistant",
			"model":"glm-upstream","content":[{"type":"text","text":"回复"}],
			"stop_reason":"end_turn",
			"usage":{"input_tokens":300,"output_tokens":100,"cache_read_input_tokens":50}}`)
	}))
	defer upstream.Close()

	userID, key := e.seedRelayUser(t, "anth1", 1_000_000, nil)
	e.seedModel(t, "claude-sonnet-4-6")
	e.seedChannelProto(t, "anth-ch", upstream.URL, domain.ProtocolAnthropic,
		[]string{"claude-sonnet-4-6"}, map[string]string{"claude-sonnet-4-6": "glm-4.7-anthropic"})

	payload, _ := json.Marshal(map[string]any{
		"model": "claude-sonnet-4-6", "max_tokens": 100,
		"messages": []map[string]any{{"role": "user", "content": "你好"}},
	})
	req, _ := http.NewRequest("POST", e.srv.URL+"/v1/messages", bytes.NewReader(payload))
	req.Header.Set("x-api-key", key) // Anthropic 客户端风格认证
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	body := new(bytes.Buffer)
	_, _ = body.ReadFrom(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("应成功: %d %s", resp.StatusCode, body.String())
	}
	if gotAPIKeyHeader != "upstream-secret-key" || gotVersion != "2023-06-01" {
		t.Errorf("上游鉴权头错误: %q %q", gotAPIKeyHeader, gotVersion)
	}
	if gotBody["model"] != "glm-4.7-anthropic" {
		t.Errorf("上游应收到映射模型，实际 %v", gotBody["model"])
	}
	var out map[string]any
	_ = json.Unmarshal(body.Bytes(), &out)
	if out["model"] != "claude-sonnet-4-6" {
		t.Errorf("响应 model 应为公开名: %v", out["model"])
	}
	// anthropic 语义：base 300 + cache_read 50 → 300×1 + 50×0(未配缓存价) + 100×2 = 500
	if bal := e.userBalance(t, userID); bal != 1_000_000-500 {
		t.Errorf("期望余额 %d，实际 %d", 1_000_000-500, bal)
	}
	log := e.waitUsageLog(t, userID)
	if log.UsageSemantic != domain.SemanticAnthropic || log.CacheReadTokens != 50 {
		t.Errorf("语义/缓存记录错误: %s/%d", log.UsageSemantic, log.CacheReadTokens)
	}
	// 用量日志字段语义：prompt_tokens 为含缓存合计（base 300 + cache_read 50）
	if log.PromptTokens != 350 {
		t.Errorf("PromptTokens 应为含缓存合计 350，实际 %d", log.PromptTokens)
	}
	e.assertReconcile(t)
}

// OpenAI 语义缓存计费：prompt_tokens 含缓存命中，落库 PromptTokens 保持含缓存合计
// （不重复相加），CacheReadTokens 记明细，扣费按 base 与 cache_read 分别计价。
func TestChatOpenAICachedUsageBilling(t *testing.T) {
	e := newTestEnv(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"id":"cmpl-1","object":"chat.completion","model":"glm-up",
			"choices":[{"index":0,"message":{"role":"assistant","content":"缓存命中回复"},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":1200,"completion_tokens":100,
				"prompt_tokens_details":{"cached_tokens":1000}}}`)
	}))
	defer upstream.Close()

	userID, key := e.seedRelayUser(t, "cacheduser", 1_000_000, nil)
	m := &store.Model{Name: "glm-cached", Modality: domain.ModalityText,
		BillingMode: domain.BillPerToken, Status: domain.ModelEnabled}
	if err := e.db.Create(m).Error; err != nil {
		t.Fatalf("建模型失败: %v", err)
	}
	// input 1 积分/token、cache_read 0.3 积分/token、output 2 积分/token
	if err := e.db.Create(&store.ModelPrice{ModelID: m.ID,
		InputPrice: 1_000_000, CacheReadPrice: 300_000, OutputPrice: 2_000_000}).Error; err != nil {
		t.Fatalf("建价格失败: %v", err)
	}
	e.seedChannel(t, "cached-ch", upstream.URL, 0, []string{"glm-cached"}, nil)

	resp, body := e.relayPost(t, key, map[string]any{
		"model":    "glm-cached",
		"messages": []map[string]any{{"role": "user", "content": "hi"}},
	})
	if resp.StatusCode != 200 {
		t.Fatalf("应成功: %d %s", resp.StatusCode, body)
	}
	log := e.waitUsageLog(t, userID)
	// 含缓存合计：base 200 + cache_read 1000 = 1200（与上游 prompt_tokens 一致，不重复相加）
	if log.PromptTokens != 1200 {
		t.Errorf("PromptTokens 应为含缓存合计 1200，实际 %d", log.PromptTokens)
	}
	if log.CacheReadTokens != 1000 {
		t.Errorf("CacheReadTokens 应为 1000，实际 %d", log.CacheReadTokens)
	}
	// 扣费 = base 200×1 + cache_read 1000×0.3 + output 100×2 = 200 + 300 + 200 = 700
	if log.CreditsCharged != 700 {
		t.Errorf("扣费应为 700，实际 %d", log.CreditsCharged)
	}
	if bal := e.userBalance(t, userID); bal != 1_000_000-700 {
		t.Errorf("期望余额 %d，实际 %d", 1_000_000-700, bal)
	}
	e.assertReconcile(t)
}

// Claude Code→GLM 生产场景：Anthropic 下游 × openai_compat 上游，流式跨协议转换。
func TestMessagesCrossProtocolStream(t *testing.T) {
	e := newTestEnv(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		// 收到的应是 OpenAI 格式：messages + system 展开
		if _, hasMessages := req["messages"]; !hasMessages {
			t.Errorf("上游应收到 OpenAI 格式请求: %v", req)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		for _, c := range []string{
			`{"choices":[{"delta":{"content":"思考中"}}]}`,
			`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
			`{"choices":[],"usage":{"prompt_tokens":400,"completion_tokens":80}}`,
		} {
			fmt.Fprintf(w, "data: %s\n\n", c)
			fl.Flush()
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
		fl.Flush()
	}))
	defer upstream.Close()

	userID, key := e.seedRelayUser(t, "ccode", 1_000_000, nil)
	e.seedModel(t, "claude-opus-4-6")
	e.seedChannel(t, "glm-compat", upstream.URL, 0, []string{"claude-opus-4-6"},
		map[string]string{"claude-opus-4-6": "glm-5"})

	payload, _ := json.Marshal(map[string]any{
		"model": "claude-opus-4-6", "max_tokens": 100, "stream": true,
		"system":   "你是编程助手",
		"messages": []map[string]any{{"role": "user", "content": "写个函数"}},
	})
	req, _ := http.NewRequest("POST", e.srv.URL+"/v1/messages", bytes.NewReader(payload))
	req.Header.Set("x-api-key", key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("应成功: %d", resp.StatusCode)
	}
	sc := bufio.NewScanner(resp.Body)
	var all strings.Builder
	for sc.Scan() {
		all.WriteString(sc.Text() + "\n")
	}
	got := all.String()
	// Anthropic 事件序列完整性（Claude Code 对顺序敏感）
	for _, marker := range []string{
		"event: message_start", "event: content_block_start",
		`"text":"思考中"`, "event: content_block_stop",
		"event: message_delta", `"stop_reason":"end_turn"`, `"output_tokens":80`,
		"event: message_stop",
	} {
		if !strings.Contains(got, marker) {
			t.Errorf("缺失事件标记 %q\n输出:\n%s", marker, got)
		}
	}
	if strings.Contains(got, "glm-5") {
		t.Error("不应泄露上游模型名")
	}
	// 结算：400×1 + 80×2 = 560
	if bal := e.userBalance(t, userID); bal != 1_000_000-560 {
		t.Errorf("期望余额 %d，实际 %d", 1_000_000-560, bal)
	}
	log := e.waitUsageLog(t, userID)
	if log.Status != domain.UsageSettled || log.CreditsCharged != 560 {
		t.Errorf("结算错误: %s/%d", log.Status, log.CreditsCharged)
	}
	e.assertReconcile(t)
}

// embeddings：按输入 token 计费。
func TestEmbeddingsBilling(t *testing.T) {
	e := newTestEnv(t)
	var upstreamPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamPath = r.URL.Path
		fmt.Fprint(w, `{"object":"list","model":"emb-upstream",
			"data":[{"object":"embedding","index":0,"embedding":[0.1,0.2]}],
			"usage":{"prompt_tokens":120,"total_tokens":120}}`)
	}))
	defer upstream.Close()

	userID, key := e.seedRelayUser(t, "embuser", 100_000, nil)
	m := &store.Model{Name: "text-emb", Modality: domain.ModalityEmbedding,
		BillingMode: domain.BillPerToken, Status: domain.ModelEnabled}
	e.db.Create(m)
	e.db.Create(&store.ModelPrice{ModelID: m.ID, InputPrice: 500_000}) // 0.5 积分/token
	e.seedChannel(t, "emb-ch", upstream.URL, 0, []string{"text-emb"}, nil)

	payload, _ := json.Marshal(map[string]any{"model": "text-emb", "input": "需要向量化的文本"})
	req, _ := http.NewRequest("POST", e.srv.URL+"/v1/embeddings", bytes.NewReader(payload))
	req.Header.Set("Authorization", "Bearer "+key)
	resp, _ := http.DefaultClient.Do(req)
	raw := new(bytes.Buffer)
	_, _ = raw.ReadFrom(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("应成功: %d %s", resp.StatusCode, raw.String())
	}
	// base_url（upstream.URL 根）已含版本根约定下，codec 只追加 /embeddings，
	// 不应出现 /v1/embeddings（否则 base_url 带 /v1 时会拼成 /v1/v1/embeddings）。
	if upstreamPath != "/embeddings" {
		t.Errorf("上游路径应为 /embeddings，实际 %s", upstreamPath)
	}
	// 120 token × 0.5 = 60 积分
	if bal := e.userBalance(t, userID); bal != 100_000-60 {
		t.Errorf("期望余额 %d，实际 %d", 100_000-60, bal)
	}
	log := e.waitUsageLog(t, userID)
	if log.CreditsCharged != 60 || log.CompletionTokens != 0 {
		t.Errorf("embeddings 计费错误: %d/%d", log.CreditsCharged, log.CompletionTokens)
	}
	e.assertReconcile(t)
}

// images：按次计费，含时段倍率。
func TestImagesPerCallBilling(t *testing.T) {
	e := newTestEnv(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"created":1,"data":[{"b64_json":"aW1n"},{"b64_json":"aW1n"}]}`)
	}))
	defer upstream.Close()

	userID, key := e.seedRelayUser(t, "imguser", 1_000_000, nil)
	m := &store.Model{Name: "gpt-image-1", Modality: domain.ModalityImage,
		BillingMode: domain.BillPerCall, Status: domain.ModelEnabled}
	e.db.Create(m)
	e.db.Create(&store.ModelPrice{ModelID: m.ID, PerCallPrice: 40_000}) // 4 万积分/张
	e.seedChannel(t, "img-ch", upstream.URL, 0, []string{"gpt-image-1"}, nil)

	payload, _ := json.Marshal(map[string]any{
		"model": "gpt-image-1", "prompt": "一只猫", "n": 2, "size": "1024x1024",
	})
	req, _ := http.NewRequest("POST", e.srv.URL+"/v1/images/generations", bytes.NewReader(payload))
	req.Header.Set("Authorization", "Bearer "+key)
	resp, _ := http.DefaultClient.Do(req)
	raw := new(bytes.Buffer)
	_, _ = raw.ReadFrom(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("应成功: %d %s", resp.StatusCode, raw.String())
	}
	// 2 张 × 40000 = 80000
	if bal := e.userBalance(t, userID); bal != 1_000_000-80_000 {
		t.Errorf("期望余额 %d，实际 %d", 1_000_000-80_000, bal)
	}
	log := e.waitUsageLog(t, userID)
	if log.CallCount != 2 || log.CreditsCharged != 80_000 {
		t.Errorf("按次计费错误: count=%d charged=%d", log.CallCount, log.CreditsCharged)
	}
	// 零差额结算也必须写零额 settle_adjust 终态标记：孤儿预扣清理只依赖流水
	// 判定终态，用量日志允许丢弃，缺失该标记会把已结算请求误判孤儿退款。
	var marker store.LedgerEntry
	if err := e.db.Where("request_id = ? AND entry_type = ?",
		log.RequestID, domain.LedgerSettleAdjust).First(&marker).Error; err != nil {
		t.Fatalf("零差额结算应写零额 settle_adjust 终态标记: %v", err)
	}
	if marker.Amount != 0 {
		t.Errorf("足额预扣的按次结算差额应为 0，实际 %d", marker.Amount)
	}
	e.assertReconcile(t)
}

// images 少返图：按实际返回张数结算（取请求张数与响应图片数的较小值），
// 差异写入用量日志备注，多扣部分退款。
func TestImagesPerCallBillingShortReturn(t *testing.T) {
	e := newTestEnv(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 请求 3 张，上游只返回 1 张（模拟内容过滤或单次上限）
		fmt.Fprint(w, `{"created":1,"data":[{"b64_json":"aW1n"}]}`)
	}))
	defer upstream.Close()

	userID, key := e.seedRelayUser(t, "imgshort", 1_000_000, nil)
	m := &store.Model{Name: "gpt-image-1", Modality: domain.ModalityImage,
		BillingMode: domain.BillPerCall, Status: domain.ModelEnabled}
	e.db.Create(m)
	e.db.Create(&store.ModelPrice{ModelID: m.ID, PerCallPrice: 40_000})
	e.seedChannel(t, "img-short-ch", upstream.URL, 0, []string{"gpt-image-1"}, nil)

	payload, _ := json.Marshal(map[string]any{
		"model": "gpt-image-1", "prompt": "一只猫", "n": 3, "size": "1024x1024",
	})
	req, _ := http.NewRequest("POST", e.srv.URL+"/v1/images/generations", bytes.NewReader(payload))
	req.Header.Set("Authorization", "Bearer "+key)
	resp, _ := http.DefaultClient.Do(req)
	raw := new(bytes.Buffer)
	_, _ = raw.ReadFrom(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("应成功: %d %s", resp.StatusCode, raw.String())
	}
	// 预扣 3×40000=120000，实际返回 1 张 → 结算 40000，退回 80000
	if bal := e.userBalance(t, userID); bal != 1_000_000-40_000 {
		t.Errorf("期望余额 %d，实际 %d", 1_000_000-40_000, bal)
	}
	log := e.waitUsageLog(t, userID)
	if log.CallCount != 1 || log.CreditsCharged != 40_000 {
		t.Errorf("应按实际返回张数计费: count=%d charged=%d", log.CallCount, log.CreditsCharged)
	}
	if log.CreditsPrecharged != 120_000 {
		t.Errorf("预扣应按请求张数: %d", log.CreditsPrecharged)
	}
	if !strings.Contains(log.ErrorMessage, "1") || !strings.Contains(log.ErrorMessage, "3") {
		t.Errorf("用量日志备注应记录返回张数与请求张数的差异，实际: %q", log.ErrorMessage)
	}
	if log.Status != domain.UsageSettled {
		t.Errorf("少返图仍属成功结算，状态应为 settled，实际: %s", log.Status)
	}
	e.assertReconcile(t)
}

// GET /api/me/usage-logs 返回体不变式：prompt_tokens ≥ cache_read_tokens + cache_write_tokens
// ——接口层字段语义与文档「输入 token 为含缓存合计、缓存读写为其中明细」一致。
func TestMeUsageLogsPromptTokensInvariant(t *testing.T) {
	e := newTestEnv(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"id":"msg_inv","type":"message","role":"assistant",
			"model":"up","content":[{"type":"text","text":"回复"}],
			"stop_reason":"end_turn",
			"usage":{"input_tokens":300,"output_tokens":100,
				"cache_read_input_tokens":50,"cache_creation_input_tokens":20}}`)
	}))
	defer upstream.Close()

	userID, key := e.seedRelayUser(t, "invuser", 1_000_000, nil)
	e.seedModel(t, "claude-inv")
	e.seedChannelProto(t, "inv-ch", upstream.URL, domain.ProtocolAnthropic,
		[]string{"claude-inv"}, nil)

	payload, _ := json.Marshal(map[string]any{
		"model": "claude-inv", "max_tokens": 100,
		"messages": []map[string]any{{"role": "user", "content": "你好"}},
	})
	req, _ := http.NewRequest("POST", e.srv.URL+"/v1/messages", bytes.NewReader(payload))
	req.Header.Set("x-api-key", key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("中继应成功: %d", resp.StatusCode)
	}
	e.waitUsageLog(t, userID)

	// 用会话身份查询用量日志接口，断言返回体字段不变式
	c := e.client(t)
	loginResp, loginEnv := e.do(t, c, "POST", "/api/auth/login",
		map[string]string{"username": "invuser", "password": "password-invuser"})
	if loginResp.StatusCode != 200 {
		t.Fatalf("登录失败: %d %v", loginResp.StatusCode, loginEnv)
	}
	listResp, env := e.do(t, c, "GET", "/api/me/usage-logs", nil)
	if listResp.StatusCode != 200 {
		t.Fatalf("查询用量日志应 200，实际 %d %v", listResp.StatusCode, env)
	}
	items := pageItems(t, env)
	if len(items) == 0 {
		t.Fatal("应返回至少 1 条用量日志")
	}
	for _, it := range items {
		row := it.(map[string]any)
		prompt := int64(row["prompt_tokens"].(float64))
		cacheRead := int64(row["cache_read_tokens"].(float64))
		cacheWrite := int64(row["cache_write_tokens"].(float64))
		if prompt < cacheRead+cacheWrite {
			t.Errorf("不变式违反：prompt_tokens=%d < cache_read=%d + cache_write=%d",
				prompt, cacheRead, cacheWrite)
		}
	}
	// 本用例数据下应精确等于 base 300 + read 50 + write 20 = 370
	first := items[0].(map[string]any)
	if got := int64(first["prompt_tokens"].(float64)); got != 370 {
		t.Errorf("prompt_tokens 应为含缓存合计 370，实际 %d", got)
	}
}

// OpenAI 下游 × Gemini 上游转换（非流式）。
func TestChatViaGeminiUpstream(t *testing.T) {
	e := newTestEnv(t)
	var gotPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		fmt.Fprint(w, `{"candidates":[{"finishReason":"STOP",
			"content":{"parts":[{"text":"Gemini 回复"}]}}],
			"usageMetadata":{"promptTokenCount":100,"candidatesTokenCount":20}}`)
	}))
	defer upstream.Close()

	userID, key := e.seedRelayUser(t, "gemuser", 100_000, nil)
	e.seedModel(t, "gemini-3-flash")
	e.seedChannelProto(t, "gem-ch", upstream.URL, domain.ProtocolGemini,
		[]string{"gemini-3-flash"}, nil)

	resp, body := e.relayPost(t, key, map[string]any{
		"model":    "gemini-3-flash",
		"messages": []map[string]any{{"role": "user", "content": "hi"}},
	})
	if resp.StatusCode != 200 {
		t.Fatalf("应成功: %d %s", resp.StatusCode, body)
	}
	if !strings.Contains(gotPath, "models/gemini-3-flash:generateContent") {
		t.Errorf("Gemini 上游路径错误: %s", gotPath)
	}
	var out map[string]any
	_ = json.Unmarshal(body, &out)
	choices := out["choices"].([]any)
	msg := choices[0].(map[string]any)["message"].(map[string]any)
	if msg["content"] != "Gemini 回复" {
		t.Errorf("应转换为 OpenAI 响应格式: %v", out)
	}
	// 100×1 + 20×2 = 140
	if bal := e.userBalance(t, userID); bal != 100_000-140 {
		t.Errorf("期望余额 %d，实际 %d", 100_000-140, bal)
	}
	e.assertReconcile(t)
}
