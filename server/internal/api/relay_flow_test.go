package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// --- 测试基础设施 ---

// seedRelayUser 创建用户 + 分配积分 + 建 Key，返回 Key 明文。
func (e *testEnv) seedRelayUser(t *testing.T, username string, credits int64, keyLimit *int64) (userID int64, plainKey string) {
	t.Helper()
	c := e.seedAndLogin(t, username, domain.RoleUser)
	var u store.User
	if err := e.db.Where("username = ?", username).First(&u).Error; err != nil {
		t.Fatalf("查用户失败: %v", err)
	}
	if credits > 0 {
		if _, err := e.deps.Billing.Grant(context.Background(), u.ID, credits, 0, "测试额度", ""); err != nil {
			t.Fatalf("分配积分失败: %v", err)
		}
	}
	body := map[string]any{"name": "relay-key"}
	if keyLimit != nil {
		body["credit_limit"] = *keyLimit
	}
	resp, env := e.do(t, c, "POST", "/api/me/keys/", body)
	if resp.StatusCode != 201 {
		t.Fatalf("建 Key 失败: %d %v", resp.StatusCode, env)
	}
	return u.ID, env["data"].(map[string]any)["key"].(string)
}

// seedModel 创建模型与定价：input 1积分/token、output 2积分/token。
func (e *testEnv) seedModel(t *testing.T, name string) int64 {
	t.Helper()
	m := &store.Model{Name: name, Modality: domain.ModalityText,
		BillingMode: domain.BillPerToken, Status: domain.ModelEnabled}
	if err := e.db.Create(m).Error; err != nil {
		t.Fatalf("建模型失败: %v", err)
	}
	price := &store.ModelPrice{ModelID: m.ID,
		InputPrice: 1_000_000, OutputPrice: 2_000_000}
	if err := e.db.Create(price).Error; err != nil {
		t.Fatalf("建价格失败: %v", err)
	}
	return m.ID
}

// seedChannel 直接入库一条渠道。
func (e *testEnv) seedChannel(t *testing.T, name, baseURL string, priority int,
	models []string, mapping map[string]string) int64 {
	t.Helper()
	enc, err := e.deps.Secrets.Encrypt("upstream-secret-key")
	if err != nil {
		t.Fatalf("加密失败: %v", err)
	}
	modelsJSON, _ := json.Marshal(models)
	mappingJSON := []byte("{}")
	if mapping != nil {
		mappingJSON, _ = json.Marshal(mapping)
	}
	ch := &store.Channel{
		Name: name, Provider: domain.ProviderZhipu, Protocol: domain.ProtocolOpenAICompat,
		BaseURL: baseURL, APIKeyEncrypted: enc,
		Models: modelsJSON, ModelMapping: mappingJSON,
		Status: domain.ChannelEnabled, Priority: priority, Weight: 1,
		ParamOverride: []byte("{}"), HeaderOverride: []byte("{}"),
	}
	if err := e.db.Create(ch).Error; err != nil {
		t.Fatalf("建渠道失败: %v", err)
	}
	return ch.ID
}

// relayPost 用 API Key 调 /v1/chat/completions。
func (e *testEnv) relayPost(t *testing.T, apiKey string, body map[string]any) (*http.Response, []byte) {
	t.Helper()
	payload, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", e.srv.URL+"/v1/chat/completions", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("中继请求失败: %v", err)
	}
	defer resp.Body.Close()
	raw := new(bytes.Buffer)
	_, _ = raw.ReadFrom(resp.Body)
	return resp, raw.Bytes()
}

// waitUsageLog 轮询异步写入的用量日志。
func (e *testEnv) waitUsageLog(t *testing.T, userID int64) *store.UsageLog {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		var l store.UsageLog
		if err := e.db.Where("user_id = ?", userID).Order("id DESC").First(&l).Error; err == nil {
			return &l
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("等待用量日志超时")
	return nil
}

func (e *testEnv) userBalance(t *testing.T, userID int64) int64 {
	t.Helper()
	var u store.User
	if err := e.db.First(&u, userID).Error; err != nil {
		t.Fatalf("查余额失败: %v", err)
	}
	return u.CreditBalance
}

func (e *testEnv) assertReconcile(t *testing.T) {
	t.Helper()
	mismatches, err := e.deps.Ledger.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("对账失败: %v", err)
	}
	if len(mismatches) != 0 {
		t.Fatalf("对账不通过: %+v", mismatches)
	}
}

// 入口大小写归一：客户端以混合大小写请求，归一到目录规范名后白名单/存在性/渠道匹配
// 全链路放行，上游收到 model_mapping 指定的官方名。对应「问题在我们这边」的修复。
func TestRelayModelNameCaseInsensitive(t *testing.T) {
	e := newTestEnv(t)
	var gotBody map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"x","model":"MiniMax-M3","choices":[{"message":{"content":"hi"}}],
			"usage":{"prompt_tokens":10,"completion_tokens":5}}`)
	}))
	defer upstream.Close()

	userID, key := e.seedRelayUser(t, "relaycase", 1_000_000, nil)
	// 用户级白名单为小写目录名，验证大小写归一后白名单闸门放行（否则 403）。
	if err := e.db.Model(&store.User{}).Where("id = ?", userID).
		Update("allowed_models", []byte(`["minimax-m3"]`)).Error; err != nil {
		t.Fatalf("设白名单失败: %v", err)
	}
	e.seedModel(t, "minimax-m3")
	e.seedChannel(t, "ch1", upstream.URL, 0, []string{"minimax-m3"},
		map[string]string{"minimax-m3": "MiniMax-M3"})

	resp, body := e.relayPost(t, key, map[string]any{
		"model":      "MiniMax-M3",
		"messages":   []map[string]any{{"role": "user", "content": "hi"}},
		"max_tokens": 8,
	})
	if resp.StatusCode != 200 {
		t.Fatalf("混合大小写请求应成功: %d %s", resp.StatusCode, body)
	}
	if gotBody["model"] != "MiniMax-M3" {
		t.Errorf("上游应收到官方名 MiniMax-M3，实际 %v", gotBody["model"])
	}
}

// --- 场景测试 ---

// 非流式：结算金额精确、模型名双向改写、鉴权头注入、ledger 自洽。
func TestRelayNonStreamSettlement(t *testing.T) {
	e := newTestEnv(t)
	var gotBody map[string]any
	var gotAuth string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"x","model":"glm-5-turbo","choices":[{"message":{"content":"hi"}}],
			"usage":{"prompt_tokens":1000,"completion_tokens":500}}`)
	}))
	defer upstream.Close()

	userID, key := e.seedRelayUser(t, "relay1", 1_000_000, nil)
	e.seedModel(t, "claude-sonnet-4-6")
	e.seedChannel(t, "ch1", upstream.URL, 0, []string{"claude-sonnet-4-6"},
		map[string]string{"claude-sonnet-4-6": "glm-5-turbo"})

	resp, body := e.relayPost(t, key, map[string]any{
		"model":      "claude-sonnet-4-6",
		"messages":   []map[string]any{{"role": "user", "content": "你好"}},
		"max_tokens": 100,
	})
	if resp.StatusCode != 200 {
		t.Fatalf("中继应成功: %d %s", resp.StatusCode, body)
	}
	// 上游收到映射后的模型名与渠道密钥
	if gotBody["model"] != "glm-5-turbo" {
		t.Errorf("上游应收到映射模型 glm-5-turbo，实际 %v", gotBody["model"])
	}
	if gotAuth != "Bearer upstream-secret-key" {
		t.Errorf("上游应收到渠道密钥，实际 %q", gotAuth)
	}
	// 响应模型名改回公开名
	var out map[string]any
	_ = json.Unmarshal(body, &out)
	if out["model"] != "claude-sonnet-4-6" {
		t.Errorf("响应 model 应为公开名，实际 %v", out["model"])
	}
	// 结算金额：1000×1 + 500×2 = 2000 积分
	if bal := e.userBalance(t, userID); bal != 1_000_000-2000 {
		t.Errorf("期望余额 %d，实际 %d", 1_000_000-2000, bal)
	}
	log := e.waitUsageLog(t, userID)
	if log.Status != domain.UsageSettled || log.CreditsCharged != 2000 {
		t.Errorf("日志应 settled/2000，实际 %s/%d", log.Status, log.CreditsCharged)
	}
	if log.PromptTokens != 1000 || log.CompletionTokens != 500 {
		t.Errorf("token 记录错误: %d/%d", log.PromptTokens, log.CompletionTokens)
	}
	if log.UpstreamModel != "glm-5-turbo" {
		t.Errorf("应记录上游模型名，实际 %s", log.UpstreamModel)
	}
	e.assertReconcile(t)
}

// 流式：SSE 转发、chunk 内 model 改写、末 chunk usage 结算。
func TestRelayStreamSettlement(t *testing.T) {
	e := newTestEnv(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		if opts, ok := req["stream_options"].(map[string]any); !ok || opts["include_usage"] != true {
			t.Errorf("上游请求应注入 include_usage，实际 %v", req["stream_options"])
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		chunks := []string{
			`{"id":"c","model":"glm-5","choices":[{"delta":{"content":"你"}}]}`,
			`{"id":"c","model":"glm-5","choices":[{"delta":{"content":"好"}}]}`,
			`{"id":"c","model":"glm-5","choices":[],"usage":{"prompt_tokens":200,"completion_tokens":50}}`,
		}
		for _, c := range chunks {
			fmt.Fprintf(w, "data: %s\n\n", c)
			fl.Flush()
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
		fl.Flush()
	}))
	defer upstream.Close()

	userID, key := e.seedRelayUser(t, "relay2", 1_000_000, nil)
	e.seedModel(t, "glm-5")
	e.seedChannel(t, "ch-stream", upstream.URL, 0, []string{"glm-5"}, nil)

	payload, _ := json.Marshal(map[string]any{
		"model": "glm-5", "stream": true,
		"messages": []map[string]any{{"role": "user", "content": "hi"}},
	})
	req, _ := http.NewRequest("POST", e.srv.URL+"/v1/chat/completions", bytes.NewReader(payload))
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("流式请求失败: %v", err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("应为 SSE 响应，实际 %s", ct)
	}
	sc := bufio.NewScanner(resp.Body)
	var dataLines []string
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "data: ") {
			dataLines = append(dataLines, strings.TrimPrefix(line, "data: "))
		}
	}
	if len(dataLines) != 4 || dataLines[len(dataLines)-1] != "[DONE]" {
		t.Fatalf("应转发 4 个事件且以 [DONE] 结束，实际 %d: %v", len(dataLines), dataLines)
	}
	var first map[string]any
	_ = json.Unmarshal([]byte(dataLines[0]), &first)
	if first["model"] != "glm-5" {
		t.Errorf("chunk model 应为公开名 glm-5，实际 %v", first["model"])
	}
	// 结算：200×1 + 50×2 = 300
	if bal := e.userBalance(t, userID); bal != 1_000_000-300 {
		t.Errorf("期望余额 %d，实际 %d", 1_000_000-300, bal)
	}
	log := e.waitUsageLog(t, userID)
	if log.Status != domain.UsageSettled || log.CreditsCharged != 300 || !log.IsStream {
		t.Errorf("流式日志错误: %s/%d/stream=%v", log.Status, log.CreditsCharged, log.IsStream)
	}
	if log.UsageEstimated {
		t.Error("有 usage 时不应标记估算")
	}
	e.assertReconcile(t)
}

// 上游 500 → 换渠道重试成功；瞬时错误不禁用渠道。
func TestRelayRetryOnTransient(t *testing.T) {
	e := newTestEnv(t)
	var failCalls, okCalls int32
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&failCalls, 1)
		http.Error(w, "internal error", 500)
	}))
	defer failing.Close()
	healthy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&okCalls, 1)
		fmt.Fprint(w, `{"id":"x","model":"m","choices":[],"usage":{"prompt_tokens":10,"completion_tokens":10}}`)
	}))
	defer healthy.Close()

	userID, key := e.seedRelayUser(t, "relay3", 100_000, nil)
	e.seedModel(t, "glm-5")
	failID := e.seedChannel(t, "ch-fail", failing.URL, 10, []string{"glm-5"}, nil) // 高优先级
	e.seedChannel(t, "ch-ok", healthy.URL, 0, []string{"glm-5"}, nil)

	resp, body := e.relayPost(t, key, map[string]any{
		"model": "glm-5", "messages": []map[string]any{{"role": "user", "content": "hi"}},
	})
	if resp.StatusCode != 200 {
		t.Fatalf("重试后应成功: %d %s", resp.StatusCode, body)
	}
	if atomic.LoadInt32(&failCalls) != 1 || atomic.LoadInt32(&okCalls) != 1 {
		t.Errorf("失败渠道与健康渠道应各调用 1 次，实际 %d/%d", failCalls, okCalls)
	}
	var ch store.Channel
	e.db.First(&ch, failID)
	if ch.Status != domain.ChannelEnabled {
		t.Errorf("瞬时错误不应禁用渠道，实际 %s", ch.Status)
	}
	log := e.waitUsageLog(t, userID)
	if log.Status != domain.UsageSettled {
		t.Errorf("最终应结算，实际 %s", log.Status)
	}
	e.assertReconcile(t)
}

// 上游 401 → 连续致命失败达到阈值（默认 3 次）后渠道自动禁用；无备用渠道 → 全额退款。
func TestRelayAuthFatalDisablesChannel(t *testing.T) {
	e := newTestEnv(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"message":"invalid api key"}}`, 401)
	}))
	defer upstream.Close()

	userID, key := e.seedRelayUser(t, "relay4", 100_000, nil)
	e.seedModel(t, "glm-5")
	chID := e.seedChannel(t, "ch-bad-key", upstream.URL, 0, []string{"glm-5"}, nil)

	body := map[string]any{
		"model": "glm-5", "messages": []map[string]any{{"role": "user", "content": "hi"}},
	}
	// 前两次失败：计数未达阈值，渠道保持启用
	for i := 0; i < 2; i++ {
		resp, _ := e.relayPost(t, key, body)
		if resp.StatusCode != 502 && resp.StatusCode != 503 {
			t.Fatalf("渠道失败应 502/503，实际 %d", resp.StatusCode)
		}
	}
	var ch store.Channel
	e.db.First(&ch, chID)
	if ch.Status != domain.ChannelEnabled {
		t.Fatalf("未达连续失败阈值不应禁用渠道，实际 %s", ch.Status)
	}
	// 第三次失败：达到阈值，自动禁用
	resp, _ := e.relayPost(t, key, body)
	if resp.StatusCode != 502 && resp.StatusCode != 503 {
		t.Fatalf("渠道全部失败应 502/503，实际 %d", resp.StatusCode)
	}
	e.db.First(&ch, chID)
	if ch.Status != domain.ChannelAutoDisabled {
		t.Errorf("连续 3 次 401 应自动禁用渠道，实际 %s", ch.Status)
	}
	if ch.DisabledReason == "" {
		t.Error("自动禁用应记录原因")
	}
	// 全额退款
	if bal := e.userBalance(t, userID); bal != 100_000 {
		t.Errorf("失败应全额退款，期望 100000，实际 %d", bal)
	}
	log := e.waitUsageLog(t, userID)
	if log.Status != domain.UsageRefunded {
		t.Errorf("日志应 refunded，实际 %s", log.Status)
	}
	// 渠道全部禁用后再请求：503 no_channel（测试计划 I10 尾部断言）
	resp, respBody := e.relayPost(t, key, body)
	if resp.StatusCode != 503 || !strings.Contains(string(respBody), "no_channel") {
		t.Errorf("无可用渠道应返回 503 no_channel，实际 %d %s", resp.StatusCode, respBody)
	}
	if bal := e.userBalance(t, userID); bal != 100_000 {
		t.Errorf("no_channel 请求应全额退款，实际余额 %d", bal)
	}
	e.assertReconcile(t)
}

// 上游 402（欠费）→ quota_fatal 计入连续失败，达到阈值后渠道自动禁用；
// usage_logs.error_class 落库为 quota_fatal，全额退款（测试计划 I5）。
func TestRelayQuotaFatalDisablesChannel(t *testing.T) {
	e := newTestEnv(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"message":"payment required"}}`, 402)
	}))
	defer upstream.Close()

	userID, key := e.seedRelayUser(t, "relay402", 100_000, nil)
	e.seedModel(t, "glm-5")
	chID := e.seedChannel(t, "ch-no-money", upstream.URL, 0, []string{"glm-5"}, nil)

	body := map[string]any{
		"model": "glm-5", "messages": []map[string]any{{"role": "user", "content": "hi"}},
	}
	for i := 0; i < 3; i++ {
		resp, _ := e.relayPost(t, key, body)
		if resp.StatusCode != 502 && resp.StatusCode != 503 {
			t.Fatalf("渠道失败应 502/503，实际 %d", resp.StatusCode)
		}
	}
	var ch store.Channel
	e.db.First(&ch, chID)
	if ch.Status != domain.ChannelAutoDisabled {
		t.Errorf("连续 3 次 402 应自动禁用渠道，实际 %s", ch.Status)
	}
	if ch.DisabledReason == "" {
		t.Error("自动禁用应记录原因")
	}
	if bal := e.userBalance(t, userID); bal != 100_000 {
		t.Errorf("失败应全额退款，期望 100000，实际 %d", bal)
	}
	log := e.waitUsageLog(t, userID)
	if log.Status != domain.UsageRefunded {
		t.Errorf("日志应 refunded，实际 %s", log.Status)
	}
	if log.ErrorClass != domain.ErrClassQuotaFatal {
		t.Errorf("error_class 应落库为 quota_fatal，实际 %s", log.ErrorClass)
	}
	e.assertReconcile(t)
}

// 成功响应清零连续失败计数：失败 2 次 → 成功 1 次 → 再失败 2 次，
// 渠道始终保持启用（测试计划 I6）。
func TestRelaySuccessResetsFailureCount(t *testing.T) {
	e := newTestEnv(t)
	var failMode atomic.Bool
	failMode.Store(true)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if failMode.Load() {
			http.Error(w, `{"error":{"message":"invalid api key"}}`, 401)
			return
		}
		fmt.Fprint(w, `{"id":"x","model":"m","choices":[],"usage":{"prompt_tokens":10,"completion_tokens":10}}`)
	}))
	defer upstream.Close()

	userID, key := e.seedRelayUser(t, "relay-reset", 100_000, nil)
	e.seedModel(t, "glm-5")
	chID := e.seedChannel(t, "ch-flappy", upstream.URL, 0, []string{"glm-5"}, nil)

	body := map[string]any{
		"model": "glm-5", "messages": []map[string]any{{"role": "user", "content": "hi"}},
	}
	// 失败 2 次（未达阈值 3）
	for i := 0; i < 2; i++ {
		if resp, _ := e.relayPost(t, key, body); resp.StatusCode == 200 {
			t.Fatal("失败模式下不应成功")
		}
	}
	// 成功 1 次：应清零连续失败计数
	failMode.Store(false)
	if resp, respBody := e.relayPost(t, key, body); resp.StatusCode != 200 {
		t.Fatalf("成功模式下应 200，实际 %d %s", resp.StatusCode, respBody)
	}
	// 再失败 2 次：计数从 0 重新累计，仍未达阈值
	failMode.Store(true)
	for i := 0; i < 2; i++ {
		if resp, _ := e.relayPost(t, key, body); resp.StatusCode == 200 {
			t.Fatal("失败模式下不应成功")
		}
	}
	var ch store.Channel
	e.db.First(&ch, chID)
	if ch.Status != domain.ChannelEnabled {
		t.Errorf("成功响应应清零连续失败计数，渠道应保持启用，实际 %s", ch.Status)
	}
	// 4 次失败全额退款，1 次成功结算 10×1+10×2=30 积分
	if bal := e.userBalance(t, userID); bal != 100_000-30 {
		t.Errorf("期望余额 %d，实际 %d", 100_000-30, bal)
	}
	e.assertReconcile(t)
}

// 阈值 0 = 关闭自动禁用：连续 5 次 401 渠道仍保持启用，每次全额退款（测试计划 I7）。
func TestRelayThresholdZeroDisablesAutoDisable(t *testing.T) {
	e := newTestEnv(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"message":"invalid api key"}}`, 401)
	}))
	defer upstream.Close()

	if err := e.deps.Settings.Set(context.Background(),
		"channel_disable_failure_threshold", json.RawMessage("0")); err != nil {
		t.Fatalf("设置阈值失败: %v", err)
	}

	userID, key := e.seedRelayUser(t, "relay-thr0", 100_000, nil)
	e.seedModel(t, "glm-5")
	chID := e.seedChannel(t, "ch-thr0", upstream.URL, 0, []string{"glm-5"}, nil)

	body := map[string]any{
		"model": "glm-5", "messages": []map[string]any{{"role": "user", "content": "hi"}},
	}
	for i := 0; i < 5; i++ {
		resp, _ := e.relayPost(t, key, body)
		if resp.StatusCode != 502 && resp.StatusCode != 503 {
			t.Fatalf("渠道失败应 502/503，实际 %d", resp.StatusCode)
		}
		if bal := e.userBalance(t, userID); bal != 100_000 {
			t.Fatalf("第 %d 次失败后应全额退款，实际余额 %d", i+1, bal)
		}
	}
	var ch store.Channel
	e.db.First(&ch, chID)
	if ch.Status != domain.ChannelEnabled {
		t.Errorf("阈值 0 应关闭自动禁用，渠道应保持启用，实际 %s", ch.Status)
	}
	e.assertReconcile(t)
}

// 上游 429（即使报文含余额字样）→ 归为限流：不禁用渠道，换备用渠道成功。
func TestRelay429NeverDisablesChannel(t *testing.T) {
	e := newTestEnv(t)
	limited := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"message":"insufficient balance / quota exceeded"}}`, 429)
	}))
	defer limited.Close()
	healthy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"id":"x","model":"m","choices":[],"usage":{"prompt_tokens":10,"completion_tokens":10}}`)
	}))
	defer healthy.Close()

	userID, key := e.seedRelayUser(t, "relay429", 100_000, nil)
	e.seedModel(t, "glm-5")
	limitedID := e.seedChannel(t, "ch-limited", limited.URL, 10, []string{"glm-5"}, nil) // 高优先级
	e.seedChannel(t, "ch-backup", healthy.URL, 0, []string{"glm-5"}, nil)

	body := map[string]any{
		"model": "glm-5", "messages": []map[string]any{{"role": "user", "content": "hi"}},
	}
	// 多次触发 429，超过连续失败阈值也不应禁用渠道
	for i := 0; i < 4; i++ {
		resp, respBody := e.relayPost(t, key, body)
		if resp.StatusCode != 200 {
			t.Fatalf("换渠道重试后应成功: %d %s", resp.StatusCode, respBody)
		}
	}
	var ch store.Channel
	e.db.First(&ch, limitedID)
	if ch.Status != domain.ChannelEnabled {
		t.Errorf("429 一律归为限流，不应禁用渠道，实际 %s", ch.Status)
	}
	log := e.waitUsageLog(t, userID)
	if log.Status != domain.UsageSettled {
		t.Errorf("最终应结算，实际 %s", log.Status)
	}
	e.assertReconcile(t)
}

// 400 类错误：不重试、直接透传、退款。
func TestRelayBadRequestNoRetry(t *testing.T) {
	e := newTestEnv(t)
	var calls int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(400)
		fmt.Fprint(w, `{"error":{"message":"context length exceeded","type":"invalid_request_error"}}`)
	}))
	defer upstream.Close()

	userID, key := e.seedRelayUser(t, "relay5", 100_000, nil)
	e.seedModel(t, "glm-5")
	chAID := e.seedChannel(t, "ch-a", upstream.URL, 10, []string{"glm-5"}, nil)
	chBID := e.seedChannel(t, "ch-b", upstream.URL, 0, []string{"glm-5"}, nil)

	resp, body := e.relayPost(t, key, map[string]any{
		"model": "glm-5", "messages": []map[string]any{{"role": "user", "content": "hi"}},
	})
	if resp.StatusCode != 400 {
		t.Fatalf("应透传 400，实际 %d", resp.StatusCode)
	}
	if !strings.Contains(string(body), "context length exceeded") {
		t.Errorf("应透传上游错误信息，实际 %s", body)
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Errorf("bad_request 不应重试，备用渠道应零调用，调用次数 %d", calls)
	}
	if bal := e.userBalance(t, userID); bal != 100_000 {
		t.Errorf("应全额退款，实际余额 %d", bal)
	}
	// bad_request 不计入禁用计数：两条渠道均保持启用（测试计划 I8）
	for _, id := range []int64{chAID, chBID} {
		var ch store.Channel
		e.db.First(&ch, id)
		if ch.Status != domain.ChannelEnabled {
			t.Errorf("bad_request 不应禁用渠道 %d，实际 %s", id, ch.Status)
		}
	}
	log := e.waitUsageLog(t, userID)
	if log.ErrorClass != domain.ErrClassBadRequest {
		t.Errorf("error_class 应落库为 bad_request，实际 %s", log.ErrorClass)
	}
	e.assertReconcile(t)
}

// 余额不足 → 402，不触达上游。
func TestRelayInsufficientCredits(t *testing.T) {
	e := newTestEnv(t)
	var calls int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
	}))
	defer upstream.Close()

	_, key := e.seedRelayUser(t, "poor-user", 10, nil) // 仅 10 积分
	e.seedModel(t, "glm-5")
	e.seedChannel(t, "ch", upstream.URL, 0, []string{"glm-5"}, nil)

	resp, body := e.relayPost(t, key, map[string]any{
		"model": "glm-5", "messages": []map[string]any{{"role": "user", "content": "hi"}},
	})
	if resp.StatusCode != 402 {
		t.Fatalf("余额不足应 402，实际 %d %s", resp.StatusCode, body)
	}
	if atomic.LoadInt32(&calls) != 0 {
		t.Error("余额不足不应触达上游")
	}
	e.assertReconcile(t)
}

// Key 独立额度不足 → 402。
func TestRelayKeyLimitDepleted(t *testing.T) {
	e := newTestEnv(t)
	limit := int64(100) // 远低于预扣需求
	_, key := e.seedRelayUser(t, "limited-user", 1_000_000, &limit)
	e.seedModel(t, "glm-5")

	resp, body := e.relayPost(t, key, map[string]any{
		"model": "glm-5", "messages": []map[string]any{{"role": "user", "content": "hi"}},
	})
	if resp.StatusCode != 402 || !strings.Contains(string(body), "key_quota_exceeded") {
		t.Fatalf("Key 额度不足应 402 key_quota_exceeded，实际 %d %s", resp.StatusCode, body)
	}
	e.assertReconcile(t)
}

// 无限额密钥：调用一次后已用额度应累计为实际结算金额，而非恒为 0。
func TestRelayUnlimitedKeyUsageAccumulates(t *testing.T) {
	e := newTestEnv(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"x","model":"glm-5","choices":[{"message":{"content":"hi"}}],
			"usage":{"prompt_tokens":1000,"completion_tokens":500}}`)
	}))
	defer upstream.Close()

	userID, key := e.seedRelayUser(t, "unlimited-usage", 1_000_000, nil) // 不设额度上限
	e.seedModel(t, "glm-5")
	e.seedChannel(t, "ch-unlimited", upstream.URL, 0, []string{"glm-5"}, nil)

	resp, body := e.relayPost(t, key, map[string]any{
		"model": "glm-5", "messages": []map[string]any{{"role": "user", "content": "hi"}},
	})
	if resp.StatusCode != 200 {
		t.Fatalf("中继应成功: %d %s", resp.StatusCode, body)
	}
	log := e.waitUsageLog(t, userID)
	if log.Status != domain.UsageSettled {
		t.Fatalf("日志应 settled，实际 %s", log.Status)
	}

	var k store.APIKey
	if err := e.db.Where("user_id = ?", userID).First(&k).Error; err != nil {
		t.Fatalf("查密钥失败: %v", err)
	}
	if k.CreditLimit != nil {
		t.Fatalf("前置条件失败：密钥不应设额度上限，实际 %v", *k.CreditLimit)
	}
	if k.CreditUsed <= 0 {
		t.Fatalf("无限额密钥调用一次后已用额度应大于 0，实际 %d", k.CreditUsed)
	}
	// 结算终值应等于实际扣费：1000×1 + 500×2 = 2000 积分
	if k.CreditUsed != 2000 {
		t.Errorf("已用额度应等于结算金额 2000，实际 %d", k.CreditUsed)
	}
	e.assertReconcile(t)
}

// 无限额密钥：请求失败全额退款后，已用额度应回落为 0。
func TestRelayUnlimitedKeyUsageRefund(t *testing.T) {
	e := newTestEnv(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(400)
		fmt.Fprint(w, `{"error":{"message":"bad request","type":"invalid_request_error"}}`)
	}))
	defer upstream.Close()

	userID, key := e.seedRelayUser(t, "unlimited-refund", 1_000_000, nil)
	e.seedModel(t, "glm-5")
	e.seedChannel(t, "ch-refund", upstream.URL, 0, []string{"glm-5"}, nil)

	resp, _ := e.relayPost(t, key, map[string]any{
		"model": "glm-5", "messages": []map[string]any{{"role": "user", "content": "hi"}},
	})
	if resp.StatusCode != 400 {
		t.Fatalf("应透传 400，实际 %d", resp.StatusCode)
	}
	log := e.waitUsageLog(t, userID)
	if log.Status != domain.UsageRefunded {
		t.Fatalf("日志应 refunded，实际 %s", log.Status)
	}
	var k store.APIKey
	if err := e.db.Where("user_id = ?", userID).First(&k).Error; err != nil {
		t.Fatalf("查密钥失败: %v", err)
	}
	if k.CreditUsed != 0 {
		t.Errorf("退款后已用额度应回落为 0，实际 %d", k.CreditUsed)
	}
	e.assertReconcile(t)
}

// 上游不报 usage → 字节估算 + estimated 标记。
func TestRelayEstimatedUsage(t *testing.T) {
	e := newTestEnv(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"id":"x","model":"m","choices":[{"message":{"content":"reply"}}]}`)
	}))
	defer upstream.Close()

	userID, key := e.seedRelayUser(t, "est-user", 1_000_000, nil)
	e.seedModel(t, "glm-5")
	e.seedChannel(t, "ch", upstream.URL, 0, []string{"glm-5"}, nil)

	resp, _ := e.relayPost(t, key, map[string]any{
		"model": "glm-5", "messages": []map[string]any{{"role": "user", "content": "hi"}},
	})
	if resp.StatusCode != 200 {
		t.Fatalf("应成功: %d", resp.StatusCode)
	}
	log := e.waitUsageLog(t, userID)
	if !log.UsageEstimated {
		t.Error("无 usage 应标记估算")
	}
	if log.CreditsCharged <= 0 {
		t.Error("估算计费应大于 0")
	}
	e.assertReconcile(t)
}

// Key 模型白名单拦截 + /v1/models 过滤 + /v1/key/info。
func TestRelayKeyScopes(t *testing.T) {
	e := newTestEnv(t)
	userID, key := e.seedRelayUser(t, "scoped-user", 100_000, nil)
	_ = userID
	e.seedModel(t, "glm-5")
	e.seedModel(t, "gpt-5.4")
	// /v1/models 只返回存在启用渠道承载的模型，先为两个模型配置渠道
	e.seedChannel(t, "ch-scope", "http://unused.example", 0, []string{"glm-5", "gpt-5.4"}, nil)
	// 限定只能用 gpt-5.4
	e.db.Model(&store.APIKey{}).Where("user_id = ?", userID).
		Update("allowed_models", []byte(`["gpt-5.4"]`))

	resp, body := e.relayPost(t, key, map[string]any{
		"model": "glm-5", "messages": []map[string]any{{"role": "user", "content": "hi"}},
	})
	if resp.StatusCode != 403 {
		t.Fatalf("白名单外模型应 403，实际 %d %s", resp.StatusCode, body)
	}

	req, _ := http.NewRequest("GET", e.srv.URL+"/v1/models", nil)
	req.Header.Set("Authorization", "Bearer "+key)
	mResp, _ := http.DefaultClient.Do(req)
	var models map[string]any
	_ = json.NewDecoder(mResp.Body).Decode(&models)
	mResp.Body.Close()
	data := models["data"].([]any)
	if len(data) != 1 || data[0].(map[string]any)["id"] != "gpt-5.4" {
		t.Errorf("/v1/models 应只含白名单模型，实际 %v", data)
	}

	req, _ = http.NewRequest("GET", e.srv.URL+"/v1/key/info", nil)
	req.Header.Set("Authorization", "Bearer "+key)
	kResp, _ := http.DefaultClient.Do(req)
	var info map[string]any
	_ = json.NewDecoder(kResp.Body).Decode(&info)
	kResp.Body.Close()
	if int64(info["user_credit_balance"].(float64)) != 100_000 {
		t.Errorf("key/info 余额错误: %v", info)
	}
}

// 自动禁用渠道的半开探测：探活成功恢复启用并清除禁用原因，失败维持禁用。
func TestChannelProbeRecovery(t *testing.T) {
	e := newTestEnv(t)
	healthy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"id":"x","model":"m","choices":[],"usage":{"prompt_tokens":1,"completion_tokens":1}}`)
	}))
	defer healthy.Close()
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"message":"invalid api key"}}`, 401)
	}))
	defer failing.Close()

	recoverableID := e.seedChannel(t, "ch-recoverable", healthy.URL, 0, []string{"glm-5"}, nil)
	stillBadID := e.seedChannel(t, "ch-still-bad", failing.URL, 0, []string{"glm-5"}, nil)
	e.db.Model(&store.Channel{}).Where("id IN ?", []int64{recoverableID, stillBadID}).
		Updates(map[string]any{"status": domain.ChannelAutoDisabled, "disabled_reason": "自动禁用：测试"})

	e.deps.Relay.ProbeAutoDisabledChannels(context.Background())

	var recovered store.Channel
	e.db.First(&recovered, recoverableID)
	if recovered.Status != domain.ChannelEnabled {
		t.Errorf("探活成功应恢复启用，实际 %s", recovered.Status)
	}
	if recovered.DisabledReason != "" {
		t.Errorf("恢复后应清除禁用原因，实际 %q", recovered.DisabledReason)
	}
	if recovered.LastTestAt == nil {
		t.Error("探测应写入 last_test_at")
	}

	var stillBad store.Channel
	e.db.First(&stillBad, stillBadID)
	if stillBad.Status != domain.ChannelAutoDisabled {
		t.Errorf("探活失败应维持禁用，实际 %s", stillBad.Status)
	}
	if stillBad.LastTestAt == nil {
		t.Error("探测失败也应写入 last_test_at")
	}

	// 恢复后的渠道重新参与选路：向该模型发一次请求应 200 并正常结算（测试计划 I9）
	userID, key := e.seedRelayUser(t, "probe-user", 100_000, nil)
	e.seedModel(t, "glm-5")
	resp, respBody := e.relayPost(t, key, map[string]any{
		"model": "glm-5", "messages": []map[string]any{{"role": "user", "content": "hi"}},
	})
	if resp.StatusCode != 200 {
		t.Fatalf("恢复后的渠道应承载请求，实际 %d %s", resp.StatusCode, respBody)
	}
	log := e.waitUsageLog(t, userID)
	if log.Status != domain.UsageSettled {
		t.Errorf("恢复后请求应正常结算，实际 %s", log.Status)
	}
	if log.ChannelID != recoverableID {
		t.Errorf("请求应路由到恢复的渠道 %d，实际 %d", recoverableID, log.ChannelID)
	}
	e.assertReconcile(t)
}
