package relay

// provider 前缀路由（/{provider}/v1/*）的 DB 集成测试。
// 经完整中继链路（httptest 上游 + 真实 DB 候选过滤）验证 F.8 的断言：
//   - provider 全部渠道失败时不回退其他 provider
//   - 同 provider 多渠道容错正常
//   - provider 与 model 不一致被拒绝（model_provider_mismatch）
//
// 依赖 TZL_TEST_DATABASE_URL；共用测试库，跨包必须 -p 1 串行。
// 本文件由主会话统一串行运行，不在本会话内执行。

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"gorm.io/gorm"

	"github.com/liguopeng80/tokenzen-lite/server/internal/billing"
	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/obs"
	"github.com/liguopeng80/tokenzen-lite/server/internal/secrets"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store/migrate"
)

// providerTestDB 连接共享测试库（未设置 TZL_TEST_DATABASE_URL 时跳过）。
func providerTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	url := os.Getenv("TZL_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("未设置 TZL_TEST_DATABASE_URL，跳过 provider 前缀路由集成测试")
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
	if err := db.Exec(`TRUNCATE users, api_keys, sessions, credit_ledger, redemptions,
		usage_logs, channels, models, model_prices, channel_costs, settings
		RESTART IDENTITY CASCADE`).Error; err != nil {
		t.Fatalf("清空测试表失败: %v", err)
	}
	return db
}

// channelSpec 描述一条测试渠道：名称、provider、上游密钥明文、是否注入失败。
type channelSpec struct {
	name     string
	provider domain.Provider
	apiKey   string // 上游密钥明文（mock 据此识别来源渠道）
	fail     bool   // true 时 mock 上游返回 500
	priority int    // 渠道优先级，0 视为默认 1；提高可确定性控制加权随机的首试顺序
}

// providerTestEnv provider 前缀路由集成测试所需依赖。
type providerTestEnv struct {
	db     *gorm.DB
	engine *Engine
	box    *secrets.Box
	// hits 由 mock 上游记录：上游密钥明文 → 命中次数。断言「某 provider 渠道是否被尝试」用。
	hits map[string]int
	mu   sync.Mutex
}

// newProviderTestEnv 按渠道规格种入 fixture，返回测试环境。
// 模型 modelName 归属厂商 modelProvider；渠道切片承载该模型。
// mock 上游按 apiKey 决定成败（fail=true 的返回 500 触发换渠道重试）。
func newProviderTestEnv(t *testing.T, modelName string, modelProvider domain.Provider,
	specs []channelSpec) *providerTestEnv {

	t.Helper()
	db := providerTestDB(t)
	box := secrets.New("tzl-provider-route-test-key")
	settings := store.NewSettingsRepo(db)
	channels := store.NewChannelRepo(db)
	costs := store.NewChannelCostRepo(db)
	models := store.NewModelRepo(db)
	usageLogs := store.NewUsageLogRepo(db)
	spend := store.NewSpendRepo(db)
	billingSvc := billing.NewService(db)

	env := &providerTestEnv{
		db: db, box: box, hits: map[string]int{},
	}

	// mock 上游：按 Authorization Bearer 识别来源渠道，命中计入 hits，按 spec.fail 决定成败。
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiKey := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		env.mu.Lock()
		env.hits[apiKey]++
		env.mu.Unlock()
		// 失败注入：fail 渠道返回 500（transient → 换渠道重试，不触发自动禁用计数）
		for _, s := range specs {
			if s.apiKey == apiKey && s.fail {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"error":{"message":"injected failure"}}`))
				return
			}
		}
		// 成功：返回最小合法 OpenAI chat completion 响应（含 usage，避免估算）
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"chatcmpl-x","object":"chat.completion","choices":[{"message":{"role":"assistant","content":"ok"}}],"usage":{"prompt_tokens":3,"completion_tokens":1,"total_tokens":4}}`))
	}))
	t.Cleanup(upstream.Close)

	e := &Engine{
		DB: db, Channels: channels, Costs: costs, Models: models,
		Billing: billingSvc, UsageLogs: usageLogs, Settings: settings,
		Secrets: box, Client: upstream.Client(), Spend: spend,
	}
	t.Cleanup(func() { e.Close(context.Background()) })
	env.engine = e

	// 种入用户、积分、密钥
	u := &store.User{Username: "provider-route-user", PasswordHash: "x",
		Role: domain.RoleUser, Status: domain.UserEnabled}
	if err := db.Create(u).Error; err != nil {
		t.Fatalf("种入用户失败: %v", err)
	}
	if _, err := billingSvc.Grant(context.Background(), u.ID, 1_000_000_000, 0, "测试", ""); err != nil {
		t.Fatalf("分配积分失败: %v", err)
	}
	key := &store.APIKey{UserID: u.ID, Name: "k", KeyHash: "h", KeyPrefix: "sk",
		Status: domain.KeyEnabled}
	if err := db.Create(key).Error; err != nil {
		t.Fatalf("种入密钥失败: %v", err)
	}

	// 种入模型与定价（provider 字段决定归属厂商，是一致校验的依据）
	m := &store.Model{Name: modelName, Provider: string(modelProvider),
		Modality: domain.ModalityText, BillingMode: domain.BillPerToken,
		Status: domain.ModelEnabled}
	if err := db.Create(m).Error; err != nil {
		t.Fatalf("种入模型失败: %v", err)
	}
	if err := db.Create(&store.ModelPrice{ModelID: m.ID,
		InputPrice: 1_000_000, OutputPrice: 2_000_000}).Error; err != nil {
		t.Fatalf("种入价格失败: %v", err)
	}

	// 种入渠道：全部承载该模型，provider 与协议按 spec
	modelsJSON, _ := json.Marshal([]string{modelName})
	for _, s := range specs {
		enc, err := box.Encrypt(s.apiKey)
		if err != nil {
			t.Fatalf("加密上游密钥失败: %v", err)
		}
		prio := s.priority
		if prio == 0 {
			prio = 1
		}
		ch := &store.Channel{
			Name: s.name, Provider: s.provider, Protocol: domain.ProtocolOpenAICompat,
			BaseURL: upstream.URL, APIKeyEncrypted: enc,
			Models: modelsJSON, ModelMapping: []byte("{}"),
			Status: domain.ChannelEnabled, Priority: prio, Weight: 1,
			ParamOverride: []byte("{}"), HeaderOverride: []byte("{}"),
		}
		if err := db.Create(ch).Error; err != nil {
			t.Fatalf("种入渠道 %s 失败: %v", s.name, err)
		}
	}
	return env
}

// buildIdentity 构造认证身份（不带 provider 约束，调用方按场景设置 ident.Provider）。
func (env *providerTestEnv) buildIdentity(t *testing.T) Identity {
	t.Helper()
	var u store.User
	if err := env.db.Where("username = ?", "provider-route-user").First(&u).Error; err != nil {
		t.Fatalf("查测试用户失败: %v", err)
	}
	var key store.APIKey
	if err := env.db.Where("user_id = ?", u.ID).First(&key).Error; err != nil {
		t.Fatalf("查测试密钥失败: %v", err)
	}
	return Identity{User: &u, Key: &key}
}

// chatResp 驱动一次 OpenAI 下游对话中继，返回响应码、响应体与错误码。
func (env *providerTestEnv) chat(t *testing.T, ident Identity, modelName string) (int, string, string) {
	t.Helper()
	body := map[string]any{"model": modelName, "messages": []map[string]any{
		{"role": "user", "content": "hi"},
	}}
	bodyJSON, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(string(bodyJSON)))
	req.Header.Set("Content-Type", "application/json")
	ctx := obs.WithRequestID(req.Context(), "provider-route-req")
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()
	env.engine.handleChat(w, req, ident, dsOpenAI, WriteOpenAIError)

	respBody := w.Body.String()
	var parsed map[string]any
	_ = json.Unmarshal([]byte(respBody), &parsed)
	code := ""
	if errObj, ok := parsed["error"].(map[string]any); ok {
		if c, ok := errObj["code"].(string); ok {
			code = c
		}
	}
	return w.Code, respBody, code
}

// hitCount 返回某上游密钥被 mock 上游命中的次数（用于断言某渠道是否被尝试）。
func (env *providerTestEnv) hitCount(apiKey string) int {
	env.mu.Lock()
	defer env.mu.Unlock()
	return env.hits[apiKey]
}

// 【集成】provider 全部渠道失败时不回退其他 provider：
// 模型归属 anthropic；anthropic 渠道 A1 注入失败；openai 渠道 B1 正常。
// 以前缀 /anthropic/v1/* 入口（ident.Provider=anthropic）请求时：
//   - 只在 anthropic 候选内重试（A1 被试），候选耗尽返回错误；
//   - B1（openai）绝不参与，其上游密钥命中次数为 0。
func TestProviderPrefixNoFallbackAcrossProviders(t *testing.T) {
	const (
		keyAnthropicFail = "sk-anthropic-fail"
		keyOpenAIOk      = "sk-openai-ok"
	)
	env := newProviderTestEnv(t, "claude-test", domain.ProviderAnthropic, []channelSpec{
		{name: "A1", provider: domain.ProviderAnthropic, apiKey: keyAnthropicFail, fail: true},
		{name: "B1", provider: domain.ProviderOpenAI, apiKey: keyOpenAIOk, fail: false},
	})

	ident := env.buildIdentity(t)
	ident.Provider = domain.ProviderAnthropic // 模拟 /anthropic/v1/* 前缀

	status, body, code := env.chat(t, ident, "claude-test")
	if status == 200 {
		t.Fatalf("anthropic 渠道全部失败时不应成功，但返回 200；body=%s", body)
	}
	// 候选耗尽：503 no_channel 或 502 upstream_error（取决于最后一条错误归类）
	if status != http.StatusServiceUnavailable && status != http.StatusBadGateway {
		t.Fatalf("期望 503/502，实际 %d；body=%s", status, body)
	}

	// 关键断言：openai 渠道 B1 绝不被尝试（不回退其他 provider）
	if n := env.hitCount(keyOpenAIOk); n != 0 {
		t.Fatalf("openai 渠道不应被尝试，但上游命中 %d 次（provider 前缀应阻止跨 provider 回退）", n)
	}
	// anthropic 渠道 A1 被尝试过（至少 1 次）
	if n := env.hitCount(keyAnthropicFail); n == 0 {
		t.Fatalf("anthropic 渠道 A1 应被尝试，但上游命中 0 次")
	}
	// 错误码归属
	if code != "no_channel" && code != "upstream_error" {
		t.Fatalf("期望错误码 no_channel 或 upstream_error，实际 %q；body=%s", code, body)
	}
}

// 【集成】同 provider 多渠道容错正常：
// provider=anthropic 有 A1（失败注入）与 A2（正常）；前缀 /anthropic/v1/* 入口。
// 期望：A1 失败后 failover 到 A2 成功（200），行为与现有 /v1/* 跨 provider 容错一致，仅范围收窄。
func TestProviderPrefixSameProviderFailover(t *testing.T) {
	const (
		keyA1Fail = "sk-a1-fail"
		keyA2Ok   = "sk-a2-ok"
	)
	// A1 priority 高于 A2（priority DESC 大优先），确保 A1 必被首试、失败后 failover 到 A2，
	// 避免等优先级加权随机下首试渠道不定导致的 flake。
	env := newProviderTestEnv(t, "claude-test", domain.ProviderAnthropic, []channelSpec{
		{name: "A1", provider: domain.ProviderAnthropic, apiKey: keyA1Fail, fail: true, priority: 2},
		{name: "A2", provider: domain.ProviderAnthropic, apiKey: keyA2Ok, fail: false},
	})

	ident := env.buildIdentity(t)
	ident.Provider = domain.ProviderAnthropic

	status, body, _ := env.chat(t, ident, "claude-test")
	if status != 200 {
		t.Fatalf("同 provider 内应 failover 成功，期望 200，实际 %d；body=%s", status, body)
	}
	if n := env.hitCount(keyA1Fail); n == 0 {
		t.Fatalf("A1 应被尝试（首试失败），上游命中 0 次")
	}
	if n := env.hitCount(keyA2Ok); n == 0 {
		t.Fatalf("A2 应作为 failover 目标被尝试，上游命中 0 次")
	}
}

// 【集成】provider 与 model 不一致被拒绝（model_provider_mismatch）：
// 模型归属 anthropic；前缀 /openai/v1/* 入口（ident.Provider=openai）。
// 期望：400 model_provider_mismatch，文案含两侧 provider；不计费、上游不被调用。
func TestProviderPrefixModelMismatchRejected(t *testing.T) {
	const keyAnthropicOk = "sk-anthropic-ok"
	env := newProviderTestEnv(t, "claude-test", domain.ProviderAnthropic, []channelSpec{
		{name: "A1", provider: domain.ProviderAnthropic, apiKey: keyAnthropicOk, fail: false},
	})

	ident := env.buildIdentity(t)
	ident.Provider = domain.ProviderOpenAI // 与 model.Provider=anthropic 不一致

	status, body, code := env.chat(t, ident, "claude-test")
	if status != http.StatusBadRequest {
		t.Fatalf("期望 400，实际 %d；body=%s", status, body)
	}
	if code != domain.ErrCodeModelProviderMismatch {
		t.Fatalf("期望错误码 %s，实际 %q；body=%s",
			domain.ErrCodeModelProviderMismatch, code, body)
	}
	// 文案需指明两侧 provider，便于客户端定位
	if !strings.Contains(body, "openai") || !strings.Contains(body, "anthropic") {
		t.Fatalf("错误文案应指明两侧 provider，实际：%s", body)
	}
	// 不计费、上游不被调用
	if n := env.hitCount(keyAnthropicOk); n != 0 {
		t.Fatalf("不一致被拒时不应调用上游，实际命中 %d 次", n)
	}
}
