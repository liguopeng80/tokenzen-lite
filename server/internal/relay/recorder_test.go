package relay

// 请求录制（recorder.go）的单元测试与集成测试。
// 单元测试不依赖数据库，覆盖 nil 安全、脱敏、序列化、有界队列；
// 集成测试经完整中继链路（httptest 上游）验证文件落盘内容。

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/liguopeng80/tokenzen-lite/server/internal/billing"
	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/obs"
	"github.com/liguopeng80/tokenzen-lite/server/internal/secrets"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store/migrate"
)

// --- 单元测试（不依赖数据库） ---

// nil recorder 的所有方法必须 no-op，不 panic。
func TestRecordingNilRecorderNoop(t *testing.T) {
	var rec *recorder
	rec.captureResp([]byte("raw"), []byte("out")) // 不 panic
	rec.setResponseMeta(200, http.Header{"Content-Type": []string{"application/json"}})
	rec.appendStreamLine([]byte("data: {}\n"))
	rec.flush(time.Now(), &store.UsageLog{RequestID: "x"}, nil)

	if rec != nil {
		t.Fatal("nil recorder 不应被实例化")
	}
}

// redactHeaders 脱敏请求侧敏感头。
func TestRecordingRedactRequestHeaders(t *testing.T) {
	h := http.Header{}
	h.Set("Authorization", "Bearer sk-secret")
	h.Set("X-API-Key", "key-secret")
	h.Set("Cookie", "session=abc")
	h.Set("Proxy-Authorization", "Basic xyz")
	h.Set("Content-Type", "application/json")
	h.Set("User-Agent", "test/1.0")

	out := redactHeaders(h, redactRequestHeaderNames)
	if out["Authorization"] != redactedValue {
		t.Errorf("Authorization 未脱敏: %q", out["Authorization"])
	}
	if out["X-Api-Key"] != redactedValue { // http.Header.CanonicalHeaderKey 大写化为 X-Api-Key
		t.Errorf("X-API-Key 未脱敏: %q", out["X-Api-Key"])
	}
	if out["Cookie"] != redactedValue {
		t.Errorf("Cookie 未脱敏: %q", out["Cookie"])
	}
	if out["Proxy-Authorization"] != redactedValue {
		t.Errorf("Proxy-Authorization 未脱敏: %q", out["Proxy-Authorization"])
	}
	if out["Content-Type"] != "application/json" {
		t.Errorf("非敏感头被误改: %q", out["Content-Type"])
	}
	if out["User-Agent"] != "test/1.0" {
		t.Errorf("非敏感头被误改: %q", out["User-Agent"])
	}
}

// redactHeaders 脱敏响应侧敏感头。
func TestRecordingRedactResponseHeaders(t *testing.T) {
	h := http.Header{}
	h.Set("Set-Cookie", "token=abc; Path=/")
	h.Set("WWW-Authenticate", `Basic realm="x"`)
	h.Set("Content-Type", "text/event-stream")

	out := redactHeaders(h, redactResponseHeaderNames)
	if out["Set-Cookie"] != redactedValue {
		t.Errorf("Set-Cookie 未脱敏: %q", out["Set-Cookie"])
	}
	if out["Www-Authenticate"] != redactedValue {
		t.Errorf("WWW-Authenticate 未脱敏: %q", out["Www-Authenticate"])
	}
	if out["Content-Type"] != "text/event-stream" {
		t.Errorf("非敏感响应头被误改: %q", out["Content-Type"])
	}
}

// buildDocument 非流式：raw_body 与 body 都在，tzl 块从 UsageLog 抄。
func TestRecordingBuildDocumentNonStream(t *testing.T) {
	rec := &recorder{
		requestID:   "req-001",
		method:      "POST",
		path:        "/v1/chat/completions",
		query:       "",
		reqHeaders:  map[string]string{"Content-Type": "application/json"},
		reqBody:     []byte(`{"model":"gpt-4","messages":[{"role":"user","content":"hi"}]}`),
		reqBodyLen:  52,
		clientIP:    "127.0.0.1",
		startedAt:   time.Date(2026, 8, 8, 10, 0, 0, 0, time.UTC),
		finishedAt:  time.Date(2026, 8, 8, 10, 0, 1, 0, time.UTC),
		isStream:    false,
		statusCode:  200,
		respHeaders: map[string]string{"Content-Type": "application/json"},
		rawBody:     []byte(`{"id":"chatcmpl-1","usage":{"prompt_tokens":5}}`),
		outBody:     []byte(`{"id":"chatcmpl-1","choices":[],"usage":{"prompt_tokens":5,"completion_tokens":10}}`),
		usageLog: &store.UsageLog{
			UserID: 1, ChannelID: 2, ModelName: "gpt-4",
			UpstreamModel: "gpt-4-2024", Protocol: domain.ProtocolOpenAICompat,
			Status: domain.UsageSettled, PromptTokens: 5, CompletionTokens: 10,
			CreditsCharged: 100,
		},
	}

	doc := rec.buildDocument()
	if doc.ID != "req-001" {
		t.Errorf("ID mismatch: %s", doc.ID)
	}
	if doc.Meta.DurationMS != 1000 {
		t.Errorf("DurationMS mismatch: %d", doc.Meta.DurationMS)
	}
	if doc.Meta.BodyHash == "" {
		t.Error("BodyHash 不应为空")
	}

	// 请求体
	var reqBody map[string]any
	if err := json.Unmarshal(doc.Request.Body, &reqBody); err != nil {
		t.Fatalf("请求体不是合法 JSON: %v", err)
	}
	if reqBody["model"] != "gpt-4" {
		t.Errorf("请求体 model 字段丢失: %v", reqBody["model"])
	}

	// 响应体（out）
	var respBody map[string]any
	if err := json.Unmarshal(doc.Response.Body, &respBody); err != nil {
		t.Fatalf("响应体不是合法 JSON: %v", err)
	}

	// raw_body（上游原始）
	var rawBody map[string]any
	if err := json.Unmarshal(doc.Response.RawBody, &rawBody); err != nil {
		t.Fatalf("raw_body 不是合法 JSON: %v", err)
	}
	if _, ok := rawBody["id"]; !ok {
		t.Error("raw_body 缺少 id 字段")
	}

	if doc.Response.IsStream {
		t.Error("非流式请求 IsStream 应为 false")
	}
	if doc.Response.Truncated {
		t.Error("未截断时 Truncated 应为 false")
	}

	// tzl 业务块
	if doc.TZL == nil {
		t.Fatal("tzl 块不应为 nil")
	}
	if doc.TZL.UserID != 1 || doc.TZL.ChannelID != 2 {
		t.Errorf("tzl 块 UserID/ChannelID 错误: %+v", doc.TZL)
	}
	if doc.TZL.Model != "gpt-4" {
		t.Errorf("tzl 块 Model 错误: %s", doc.TZL.Model)
	}
}

// buildDocument 流式：response.body 是拼回的 SSE 全流。
func TestRecordingBuildDocumentStream(t *testing.T) {
	sseLines := []byte("data: {\"choices\":[{}]}\n\ndata: [DONE]\n")
	rec := &recorder{
		requestID:   "req-stream-001",
		method:      "POST",
		path:        "/v1/chat/completions",
		reqHeaders:  map[string]string{},
		reqBody:     []byte(`{"model":"gpt-4","stream":true}`),
		reqBodyLen:  30,
		startedAt:   time.Now(),
		finishedAt:  time.Now(),
		isStream:    true,
		streamBuf:   sseLines,
		statusCode:  200,
		respHeaders: map[string]string{"Content-Type": "text/event-stream"},
	}

	doc := rec.buildDocument()
	if !doc.Response.IsStream {
		t.Error("流式请求 IsStream 应为 true")
	}

	// 响应体应是 JSON 字符串，解码后含 [DONE]
	var respBody string
	if err := json.Unmarshal(doc.Response.Body, &respBody); err != nil {
		t.Fatalf("流式响应体解码失败: %v", err)
	}
	if !strings.Contains(respBody, "[DONE]") {
		t.Errorf("流式响应体缺少 [DONE] 标记: %q", respBody)
	}
	if !strings.Contains(respBody, "choices") {
		t.Errorf("流式响应体缺少 choices: %q", respBody)
	}

	// 流式不应有 raw_body
	if doc.Response.RawBody != nil {
		t.Error("流式响应不应有 raw_body")
	}
}

// appendStreamLine 达上限后置 truncated 且不再累计。
func TestRecordingAppendStreamLineTruncation(t *testing.T) {
	rec := &recorder{
		maxStreamBytes: 10,
	}
	// 前 10 字节正常累计
	rec.appendStreamLine([]byte("0123456789")) // 10 字节 + 1 换行 = 11 > 10
	if !rec.streamTrunc {
		t.Error("达上限后应置 truncated")
	}
	if len(rec.streamBuf) > 0 {
		t.Errorf("超上限后不应累计，实际 %d 字节", len(rec.streamBuf))
	}

	// 再调一次：truncated 已置，直接返回
	rec.appendStreamLine([]byte("more"))
	if len(rec.streamBuf) > 0 {
		t.Error("truncated 后不应再累计")
	}
}

// recordingWriter 有界队列：正常入队 + 落盘 + 关闭刷盘。
func TestRecordingWriterPersistAndClose(t *testing.T) {
	dir := t.TempDir()
	w := newRecordingWriter(dir)
	defer w.close(context.Background())

	rec := &recorder{
		requestID: "req-persist-001",
		method:    "POST",
		path:      "/v1/chat/completions",
		reqBody:   []byte(`{"model":"x"}`),
		startedAt: time.Now(),
	}
	rec.captureResp([]byte(`{"raw":true}`), []byte(`{"out":true}`))
	rec.flush(time.Now(), nil, w)

	// 等待 worker 落盘
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		files := findRecordingFiles(dir, "req-persist-001")
		if len(files) > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	files := findRecordingFiles(dir, "req-persist-001")
	if len(files) == 0 {
		t.Fatal("录制文件未生成")
	}

	// 验证文件权限 0600
	info, err := os.Stat(files[0])
	if err != nil {
		t.Fatalf("stat 失败: %v", err)
	}
	if perm := info.Mode().Perm(); perm != recordingFilePerm {
		t.Errorf("文件权限错误: %o，期望 %o", perm, recordingFilePerm)
	}

	// 验证文件内容可反序列化
	data, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatalf("读取文件失败: %v", err)
	}
	var doc recordingFile
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("反序列化失败: %v", err)
	}
	if doc.ID != "req-persist-001" {
		t.Errorf("文件 ID 错误: %s", doc.ID)
	}
}

// recordingWriter 队列满时丢弃计数。
func TestRecordingWriterDroppedCount(t *testing.T) {
	dir := t.TempDir()
	w := &recordingWriter{
		dir:    dir,
		queue:  make(chan *recorder, 2), // 容量 2
		stopCh: make(chan struct{}),
		done:   make(chan struct{}),
	}
	// 不启动 worker，让队列填满后入队走丢弃分支
	w.queue <- &recorder{requestID: "r1"}
	w.queue <- &recorder{requestID: "r2"}
	w.enqueue(&recorder{requestID: "r3"}) // 应被丢弃
	if w.droppedCount() != 1 {
		t.Errorf("丢弃计数错误: %d，期望 1", w.droppedCount())
	}
	close(w.stopCh) // 清理
}

// concurrentSafeRecorder 并发调用 nil-safe 的方法不 panic。
func TestRecordingConcurrentNilSafe(t *testing.T) {
	var rec *recorder
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rec.captureResp(nil, nil)
			rec.appendStreamLine(nil)
			rec.setResponseMeta(0, nil)
			rec.flush(time.Now(), nil, nil)
		}()
	}
	wg.Wait()
}

// --- 集成测试（依赖 TZL_TEST_DATABASE_URL） ---

// recordingTestDB 连接共享测试库。
func recordingTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	url := os.Getenv("TZL_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("未设置 TZL_TEST_DATABASE_URL，跳过录制集成测试")
	}
	if err := migrate.Up(url); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}
	db, err := store.Open(url)
	if err != nil {
		t.Fatalf("连接测试库失败: %v", err)
	}
	if sqlDB, dbErr := db.DB(); dbErr == nil {
		t.Cleanup(func() { _ = sqlDB.Close() })
	}
	if err := db.Exec("TRUNCATE users, api_keys, sessions, credit_ledger, redemptions, usage_logs, channels, models, model_prices, channel_costs, settings RESTART IDENTITY CASCADE").Error; err != nil {
		t.Fatalf("清空测试表失败: %v", err)
	}
	return db
}

// recordingTestEnv 集成测试所需的全部依赖。
type recordingTestEnv struct {
	db        *gorm.DB
	engine    *Engine
	recordDir string
	upstream  *httptest.Server
}

func newRecordingTestEnv(t *testing.T, upstreamHandler http.HandlerFunc) *recordingTestEnv {
	t.Helper()
	db := recordingTestDB(t)
	recordDir := t.TempDir()
	box := secrets.New("tzl-dev-only-encrypt-key")
	settings := store.NewSettingsRepo(db)
	channels := store.NewChannelRepo(db)
	costs := store.NewChannelCostRepo(db)
	models := store.NewModelRepo(db)
	usageLogs := store.NewUsageLogRepo(db)
	spend := store.NewSpendRepo(db)
	billingSvc := billing.NewService(db)
	upstream := httptest.NewServer(upstreamHandler)
	t.Cleanup(upstream.Close)

	e := &Engine{
		DB: db, Channels: channels, Costs: costs, Models: models,
		Billing: billingSvc, UsageLogs: usageLogs, Settings: settings,
		Secrets: box, Client: upstream.Client(),
		Spend: spend, Usage: NewUsageSink(usageLogs, settings, recordDir, nil),
	}
	t.Cleanup(func() { e.Close(context.Background()) })

	// 种入用户与积分
	u := &store.User{Username: "rec-user", PasswordHash: "x",
		Role: domain.RoleUser, Status: domain.UserEnabled}
	if err := db.Create(u).Error; err != nil {
		t.Fatalf("种入用户失败: %v", err)
	}
	if _, err := billingSvc.Grant(context.Background(), u.ID, 100_000_000, 0, "测试", ""); err != nil {
		t.Fatalf("分配积分失败: %v", err)
	}
	key := &store.APIKey{UserID: u.ID, Name: "k", KeyHash: "h", KeyPrefix: "sk",
		Status: domain.KeyEnabled}
	if err := db.Create(key).Error; err != nil {
		t.Fatalf("种入密钥失败: %v", err)
	}

	// 种入模型与定价
	m := &store.Model{Name: "test-model", Modality: domain.ModalityText,
		BillingMode: domain.BillPerToken, Status: domain.ModelEnabled}
	if err := db.Create(m).Error; err != nil {
		t.Fatalf("种入模型失败: %v", err)
	}
	price := &store.ModelPrice{ModelID: m.ID, InputPrice: 1_000_000, OutputPrice: 2_000_000}
	if err := db.Create(price).Error; err != nil {
		t.Fatalf("种入价格失败: %v", err)
	}

	// 种入渠道（指向 httptest 上游）
	enc, _ := box.Encrypt("upstream-key")
	modelsJSON, _ := json.Marshal([]string{"test-model"})
	ch := &store.Channel{Name: "test-ch", Provider: domain.ProviderOpenAI,
		Protocol: domain.ProtocolOpenAICompat, BaseURL: upstream.URL,
		APIKeyEncrypted: enc, Models: modelsJSON, ModelMapping: []byte("{}"),
		Status: domain.ChannelEnabled, Priority: 1, Weight: 1,
		ParamOverride: []byte("{}"), HeaderOverride: []byte("{}")}
	if err := db.Create(ch).Error; err != nil {
		t.Fatalf("种入渠道失败: %v", err)
	}

	return &recordingTestEnv{
		db: db, engine: e, recordDir: recordDir, upstream: upstream,
	}
}

// setRecordingEnabled 开启录制并设全采样。
func setRecordingEnabled(t *testing.T, db *gorm.DB) {
	t.Helper()
	ctx := context.Background()
	for _, kv := range []struct {
		k string
		v json.RawMessage
	}{
		{"record_enabled", json.RawMessage("true")},
		{"record_sample_rate_permyriad", json.RawMessage("10000")},
	} {
		if err := store.NewSettingsRepo(db).Set(ctx, kv.k, kv.v); err != nil {
			t.Fatalf("设置 %s 失败: %v", kv.k, err)
		}
	}
}

// waitRecordingFile 轮询录制文件出现。
func waitRecordingFile(t *testing.T, dir, requestID string, timeout time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		files := findRecordingFiles(dir, requestID)
		if len(files) > 0 {
			return files[0]
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("录制文件 %s 超时未生成", requestID)
	return ""
}

func findRecordingFiles(dir, requestID string) []string {
	pattern := filepath.Join(dir, "recordings", "*", "*", "*", requestID+".json")
	matches, _ := filepath.Glob(pattern)
	return matches
}

// 【集成】非流式中继录制：文件含 raw_body 与 body，头脱敏命中。
func TestRecordingIntegrationNonStream(t *testing.T) {
	upstreamBody := []byte(`{"id":"chatcmpl-1","object":"chat.completion","choices":[{"message":{"role":"assistant","content":"hi"}}],"usage":{"prompt_tokens":5,"completion_tokens":2,"total_tokens":7}}`)
	env := newRecordingTestEnv(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_, _ = w.Write(upstreamBody)
	})
	setRecordingEnabled(t, env.db)

	// 构造中继请求
	body := map[string]any{"model": "test-model", "messages": []map[string]any{
		{"role": "user", "content": "hello"},
	}}
	bodyJSON, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(string(bodyJSON)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer sk-test-secret-key")
	w := httptest.NewRecorder()

	ctx := obs.WithRequestID(req.Context(), "rec-nonstream-001")
	req = req.WithContext(ctx)
	ident := env.buildTestIdentity(t)
	env.engine.handleChat(w, req, ident, dsOpenAI, WriteOpenAIError)

	if w.Code != 200 {
		t.Fatalf("中继请求失败: %d %s", w.Code, w.Body.String())
	}

	// 等待录制文件
	filePath := waitRecordingFile(t, env.recordDir, "rec-nonstream-001", 5*time.Second)
	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("读取录制文件失败: %v", err)
	}
	var doc recordingFile
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("反序列化录制文件失败: %v", err)
	}

	// 非流式：raw_body 与 body 都在
	if len(doc.Response.RawBody) == 0 {
		t.Error("非流式录制缺少 raw_body")
	}
	if len(doc.Response.Body) == 0 {
		t.Error("非流式录制缺少 body")
	}

	// raw_body 是上游原始体
	var raw map[string]any
	json.Unmarshal(doc.Response.RawBody, &raw)
	if raw["id"] != "chatcmpl-1" {
		t.Errorf("raw_body 内容错误: %v", raw["id"])
	}

	// 头脱敏命中
	if v, ok := doc.Request.Headers["Authorization"]; !ok || v != redactedValue {
		t.Errorf("请求头 Authorization 未脱敏: %q", v)
	}

	// tzl 业务块
	if doc.TZL == nil {
		t.Fatal("缺少 tzl 业务块")
	}
	if doc.TZL.Model != "test-model" {
		t.Errorf("tzl.Model 错误: %s", doc.TZL.Model)
	}
	if doc.TZL.Status != string(domain.UsageSettled) {
		t.Errorf("tzl.Status 错误: %s", doc.TZL.Status)
	}
}

// 【集成】流式中继录制：response.body 拼回完整 SSE 且含 [DONE]。
func TestRecordingIntegrationStream(t *testing.T) {
	sseResponse := strings.Join([]string{
		`data: {"choices":[{"delta":{"content":"H"}}]}`,
		``,
		`data: {"choices":[{"delta":{"content":"i"}}]}`,
		``,
		`data: {"choices":[{"delta":{}},{"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":2}}`,
		``,
		`data: [DONE]`,
		``,
	}, "\n")
	env := newRecordingTestEnv(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(sseResponse))
	})
	setRecordingEnabled(t, env.db)

	body := map[string]any{"model": "test-model", "stream": true, "messages": []map[string]any{
		{"role": "user", "content": "hello"},
	}}
	bodyJSON, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(string(bodyJSON)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer sk-stream-secret")
	w := httptest.NewRecorder()

	ctx := obs.WithRequestID(req.Context(), "rec-stream-001")
	req = req.WithContext(ctx)
	ident := env.buildTestIdentity(t)
	env.engine.handleChat(w, req, ident, dsOpenAI, WriteOpenAIError)

	if w.Code != 200 {
		t.Fatalf("流式中继请求失败: %d %s", w.Code, w.Body.String())
	}

	filePath := waitRecordingFile(t, env.recordDir, "rec-stream-001", 5*time.Second)
	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("读取录制文件失败: %v", err)
	}
	var doc recordingFile
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("反序列化录制文件失败: %v", err)
	}

	if !doc.Response.IsStream {
		t.Error("流式录制 IsStream 应为 true")
	}

	var respBody string
	if err := json.Unmarshal(doc.Response.Body, &respBody); err != nil {
		t.Fatalf("流式响应体解码失败: %v", err)
	}
	if !strings.Contains(respBody, "[DONE]") {
		t.Error("流式录制缺少 [DONE] 标记")
	}
	if !strings.Contains(respBody, "finish_reason") {
		t.Error("流式录制缺少 finish_reason 帧")
	}

	// 头脱敏
	if v, ok := doc.Request.Headers["Authorization"]; !ok || v != redactedValue {
		t.Errorf("请求头 Authorization 未脱敏: %q", v)
	}
}

// 【集成】采样率 0 不生成文件。
func TestRecordingIntegrationSampleRateZero(t *testing.T) {
	env := newRecordingTestEnv(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"id":"x","usage":{"prompt_tokens":1}}`))
	})
	// record_enabled=true 但 sample_rate=0
	ctx := context.Background()
	settings := store.NewSettingsRepo(env.db)
	settings.Set(ctx, "record_enabled", json.RawMessage("true"))
	// sample_rate 默认 0，不设置

	body := map[string]any{"model": "test-model", "messages": []map[string]any{
		{"role": "user", "content": "hi"},
	}}
	bodyJSON, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(string(bodyJSON)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	reqCtx := obs.WithRequestID(req.Context(), "rec-skipped-001")
	req = req.WithContext(reqCtx)
	ident := env.buildTestIdentity(t)
	env.engine.handleChat(w, req, ident, dsOpenAI, WriteOpenAIError)

	// 等一小段时间确认文件不生成
	time.Sleep(500 * time.Millisecond)
	files := findRecordingFiles(env.recordDir, "rec-skipped-001")
	if len(files) > 0 {
		t.Errorf("采样率 0 不应生成录制文件，但找到了 %d 个", len(files))
	}
}

// 【集成】默认关（record_enabled=false）时无文件、无副作用。
func TestRecordingIntegrationDefaultOff(t *testing.T) {
	env := newRecordingTestEnv(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"id":"x","usage":{"prompt_tokens":1}}`))
	})
	// 不设置 record_enabled（默认 false）

	body := map[string]any{"model": "test-model", "messages": []map[string]any{
		{"role": "user", "content": "hi"},
	}}
	bodyJSON, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(string(bodyJSON)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	reqCtx := obs.WithRequestID(req.Context(), "rec-off-001")
	req = req.WithContext(reqCtx)
	ident := env.buildTestIdentity(t)
	env.engine.handleChat(w, req, ident, dsOpenAI, WriteOpenAIError)

	if w.Code != 200 {
		t.Fatalf("中继请求失败: %d", w.Code)
	}
	time.Sleep(300 * time.Millisecond)
	files := findRecordingFiles(env.recordDir, "rec-off-001")
	if len(files) > 0 {
		t.Errorf("默认关时不应生成录制文件")
	}
}

// 【集成】body 超 max_body_bytes 时 truncated=true。
func TestRecordingIntegrationBodyTruncated(t *testing.T) {
	env := newRecordingTestEnv(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"id":"x","usage":{"prompt_tokens":1}}`))
	})
	ctx := context.Background()
	settings := store.NewSettingsRepo(env.db)
	settings.Set(ctx, "record_enabled", json.RawMessage("true"))
	settings.Set(ctx, "record_sample_rate_permyriad", json.RawMessage("10000"))
	settings.Set(ctx, "record_max_body_bytes", json.RawMessage("20")) // 极小上限

	// 构造超过 20 字节的请求体
	body := map[string]any{"model": "test-model", "messages": []map[string]any{
		{"role": "user", "content": "this is a long message that exceeds 20 bytes"},
	}}
	bodyJSON, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(string(bodyJSON)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	reqCtx := obs.WithRequestID(req.Context(), "rec-trunc-001")
	req = req.WithContext(reqCtx)
	ident := env.buildTestIdentity(t)
	env.engine.handleChat(w, req, ident, dsOpenAI, WriteOpenAIError)

	filePath := waitRecordingFile(t, env.recordDir, "rec-trunc-001", 5*time.Second)
	data, _ := os.ReadFile(filePath)
	var doc recordingFile
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("反序列化失败: %v", err)
	}
	if !doc.Response.Truncated {
		t.Error("body 超上限时 Truncated 应为 true")
	}
}

// --- 测试辅助 ---

// buildTestIdentity 从测试库构造认证身份。
func (env *recordingTestEnv) buildTestIdentity(t *testing.T) Identity {
	t.Helper()
	var u store.User
	if err := env.db.Where("username = ?", "rec-user").First(&u).Error; err != nil {
		t.Fatalf("查测试用户失败: %v", err)
	}
	var key store.APIKey
	if err := env.db.Where("user_id = ?", u.ID).First(&key).Error; err != nil {
		t.Fatalf("查测试密钥失败: %v", err)
	}
	return Identity{User: &u, Key: &key}
}

// redactBody=true 时请求体整体替换为占位，原始内容不外泄。
func TestRecordingBuildDocumentRedactBody(t *testing.T) {
	rec := &recorder{
		requestID:  "req-redact",
		method:     "POST",
		path:       "/v1/chat/completions",
		reqBody:    []byte(`{"model":"gpt-4","messages":[{"role":"user","content":"secret-content"}]}`),
		reqBodyLen: 70,
		startedAt:  time.Now(),
		finishedAt: time.Now(),
		redactBody: true,
	}
	doc := rec.buildDocument()
	if strings.Contains(string(doc.Request.Body), "secret-content") {
		t.Errorf("redactBody 未脱敏，请求体含原始内容: %s", doc.Request.Body)
	}
	var placeholder string
	if err := json.Unmarshal(doc.Request.Body, &placeholder); err != nil {
		t.Fatalf("脱敏请求体应为 JSON 字符串，解码失败: %v", err)
	}
	if placeholder != "<redacted>" {
		t.Errorf("脱敏占位错误: %q 期望 %q", placeholder, "<redacted>")
	}
	// body_hash 仍基于原始 body 计算（去重用途），不因脱敏丢失
	if doc.Meta.BodyHash == "" {
		t.Error("BodyHash 不应因 redactBody 变空")
	}
}

// recordingWriter 的 close 幂等，且 closed 后 enqueue 走丢弃计数。
func TestRecordingWriterCloseIdempotentAndClosedDrop(t *testing.T) {
	dir := t.TempDir()
	w := newRecordingWriter(dir)
	w.close(context.Background())
	w.close(context.Background()) // 幂等，不 panic

	// closed 后入队应丢弃计数，不入 queue
	w.enqueue(&recorder{requestID: "after-close"})
	if w.droppedCount() != 1 {
		t.Errorf("closed 后 enqueue 丢弃计数错误: %d 期望 1", w.droppedCount())
	}
}

// 【集成】RecordDir 空时即使 record_enabled=true 也不录制（env 双保险）。
func TestRecordingIntegrationRecordDirEmpty(t *testing.T) {
	env := newRecordingTestEnv(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"id":"x","usage":{"prompt_tokens":1}}`))
	})
	setRecordingEnabled(t, env.db)  // record_enabled=true, sample=10000
	env.engine.Usage.recordDir = "" // 但 RecordDir 空 → 双保险禁用

	body := map[string]any{"model": "test-model", "messages": []map[string]any{
		{"role": "user", "content": "hi"},
	}}
	bodyJSON, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(string(bodyJSON)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	reqCtx := obs.WithRequestID(req.Context(), "rec-nodir-001")
	req = req.WithContext(reqCtx)
	ident := env.buildTestIdentity(t)
	env.engine.handleChat(w, req, ident, dsOpenAI, WriteOpenAIError)

	if w.Code != 200 {
		t.Fatalf("中继请求失败: %d", w.Code)
	}
	time.Sleep(300 * time.Millisecond)
	if len(findRecordingFiles(env.recordDir, "rec-nodir-001")) > 0 {
		t.Errorf("RecordDir 空时不应生成录制文件")
	}
}
