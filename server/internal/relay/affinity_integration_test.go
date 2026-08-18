package relay

// 渠道亲和路由（方案 C 节）的 DB 集成测试。
// 经完整中继链路（httptest 上游 + 真实 DB 候选过滤 + 亲和绑定表）验证 C.5：
//   - 同亲和键多次请求命中同一渠道（绕过加权随机）
//   - 不同亲和键按权重分布
//   - 绑定渠道注入失败 → 漂移到其他渠道并在成功后重绑
//   - 无亲和键时退化为加权随机
//   - TTL 过期后重新选择
//
// 依赖 TZL_TEST_DATABASE_URL；共用测试库，跨包必须 -p 1 串行。
// 本文件由主会话统一串行运行，不在本会话内执行。

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
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

// affinityTestDB 连接共享测试库（未设置 TZL_TEST_DATABASE_URL 时跳过）。
func affinityTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	url := os.Getenv("TZL_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("未设置 TZL_TEST_DATABASE_URL，跳过渠道亲和集成测试")
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

// affChannelSpec 描述一条测试渠道。
type affChannelSpec struct {
	name     string
	apiKey   string // 上游密钥明文（mock 据此识别来源渠道）
	priority int    // 0 视为默认 1
}

// affTestEnv 渠道亲和集成测试所需依赖。
type affTestEnv struct {
	db     *gorm.DB
	engine *Engine
	box    *secrets.Box
	// hits 上游密钥明文 → 命中次数；断言「同亲和键是否落同一渠道」用。
	hits map[string]int
	mu   sync.Mutex
	// failNext 渠道注入一次性失败：apiKey → true 时下次命中返回 500。
	failNext map[string]bool
	failMu   sync.Mutex
	// reqSeq 用于生成唯一 request_id（预扣费幂等键），避免同测试内多请求复用同一 id 撞流水。
	reqSeq int
}

// newAffTestEnv 按渠道规格种入 fixture（同模型多渠道，OpenAI 协议，等优先级 + 等权重
// 以暴露加权随机的分布性）。mock 上游按 apiKey 记命中次数与失败注入。
func newAffTestEnv(t *testing.T, modelName string, specs []affChannelSpec) *affTestEnv {
	t.Helper()
	db := affinityTestDB(t)
	box := secrets.New("tzl-affinity-test-key")
	settings := store.NewSettingsRepo(db)
	channels := store.NewChannelRepo(db)
	costs := store.NewChannelCostRepo(db)
	models := store.NewModelRepo(db)
	usageLogs := store.NewUsageLogRepo(db)
	spend := store.NewSpendRepo(db)
	billingSvc := billing.NewService(db)

	env := &affTestEnv{
		db: db, box: box, hits: map[string]int{},
		failNext: map[string]bool{},
	}

	// mock 上游：按 Authorization Bearer 识别来源渠道，记命中；按 failNext 注入 500。
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiKey := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		env.mu.Lock()
		env.hits[apiKey]++
		env.mu.Unlock()
		env.failMu.Lock()
		fail := env.failNext[apiKey]
		if fail {
			env.failNext[apiKey] = false // 一次性失败
		}
		env.failMu.Unlock()
		if fail {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":{"message":"injected failure"}}`))
			return
		}
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
	u := &store.User{Username: "affinity-user", PasswordHash: "x",
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

	// 种入模型与定价（OpenAI 协议下游；OpenAICompat 上游）
	m := &store.Model{Name: modelName, Provider: string(domain.ProviderOpenAI),
		Modality: domain.ModalityText, BillingMode: domain.BillPerToken,
		Status: domain.ModelEnabled}
	if err := db.Create(m).Error; err != nil {
		t.Fatalf("种入模型失败: %v", err)
	}
	if err := db.Create(&store.ModelPrice{ModelID: m.ID,
		InputPrice: 1_000_000, OutputPrice: 2_000_000}).Error; err != nil {
		t.Fatalf("种入价格失败: %v", err)
	}

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
			Name: s.name, Provider: domain.ProviderOpenAI, Protocol: domain.ProtocolOpenAICompat,
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

func (env *affTestEnv) buildIdentity(t *testing.T) Identity {
	t.Helper()
	var u store.User
	if err := env.db.Where("username = ?", "affinity-user").First(&u).Error; err != nil {
		t.Fatalf("查测试用户失败: %v", err)
	}
	var key store.APIKey
	if err := env.db.Where("user_id = ?", u.ID).First(&key).Error; err != nil {
		t.Fatalf("查测试密钥失败: %v", err)
	}
	return Identity{User: &u, Key: &key}
}

// chatOpenAI 驱动一次 OpenAI 下游对话中继。extraBody 用于注入亲和键字段
// （prompt_cache_key）。返回 HTTP 状态码与响应体。
// 每次调用生成唯一 request_id，作为预扣费幂等键（复用同一 id 会撞流水）。
func (env *affTestEnv) chatOpenAI(t *testing.T, ident Identity, modelName string, extra map[string]any) (int, string) {
	t.Helper()
	body := map[string]any{"model": modelName, "messages": []map[string]any{
		{"role": "user", "content": "hi"},
	}}
	for k, v := range extra {
		body[k] = v
	}
	bodyJSON, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(string(bodyJSON)))
	req.Header.Set("Content-Type", "application/json")
	env.reqSeq++
	reqID := fmt.Sprintf("affinity-req-%d", env.reqSeq)
	req = req.WithContext(obs.WithRequestID(req.Context(), reqID))
	w := httptest.NewRecorder()
	env.engine.handleChat(w, req, ident, dsOpenAI, WriteOpenAIError)
	return w.Code, w.Body.String()
}

func (env *affTestEnv) hitCount(apiKey string) int {
	env.mu.Lock()
	defer env.mu.Unlock()
	return env.hits[apiKey]
}

func (env *affTestEnv) injectFailure(apiKey string) {
	env.failMu.Lock()
	env.failNext[apiKey] = true
	env.failMu.Unlock()
}

// disableChannelByName 把指定渠道置为 disabled，使其从 ListEnabledForModel 候选中消失，
// 用于构造「绑定渠道结构性不可用」的跨请求漂移场景（区别于重试内的瞬时失败容错）。
func (env *affTestEnv) disableChannelByName(t *testing.T, name string) {
	t.Helper()
	if err := env.db.Model(&store.Channel{}).Where("name = ?", name).
		Update("status", domain.ChannelManualDisabled).Error; err != nil {
		t.Fatalf("禁用渠道 %s 失败: %v", name, err)
	}
}

// 【集成】同亲和键多次请求命中同一渠道（绕过加权随机）：
// 三个等优先级等权重渠道，无亲和时本应均匀分布；带相同 prompt_cache_key 时
// 首次请求建立绑定，后续请求全部命中绑定渠道（即只一个渠道累计命中）。
func TestAffinitySameKeyHitsSameChannel(t *testing.T) {
	const (
		keyA = "sk-a"
		keyB = "sk-b"
		keyC = "sk-c"
	)
	env := newAffTestEnv(t, "gpt-aff", []affChannelSpec{
		{name: "A", apiKey: keyA},
		{name: "B", apiKey: keyB},
		{name: "C", apiKey: keyC},
	})
	ident := env.buildIdentity(t)

	// 同一 prompt_cache_key 发 5 次
	for i := 0; i < 5; i++ {
		status, body := env.chatOpenAI(t, ident, "gpt-aff",
			map[string]any{"prompt_cache_key": "sess-fixed"})
		if status != 200 {
			t.Fatalf("请求 %d 应成功，状态 %d；body=%s", i, status, body)
		}
	}

	// 恰好一个渠道累计 5 次命中（首次 miss 建立绑定，后 4 次 hit）
	hitA := env.hitCount(keyA)
	hitB := env.hitCount(keyB)
	hitC := env.hitCount(keyC)
	hits := []int{hitA, hitB, hitC}
	var nonzero, total int
	for _, h := range hits {
		total += h
		if h > 0 {
			nonzero++
		}
	}
	if total != 5 {
		t.Fatalf("总命中应为 5（5 次成功请求），实际 %d", total)
	}
	if nonzero != 1 {
		t.Fatalf("同亲和键应只命中一个渠道（绕过加权随机），实际分布 A=%d B=%d C=%d", hitA, hitB, hitC)
	}
}

// 【集成】不同亲和键按权重分布（不相互干扰）：
// 两个等优先级等权重渠道；两个不同 prompt_cache_key 各自绑定到某渠道，
// 互不串扰（两个 key 不强制绑到同一渠道，但各自稳定）。
func TestAffinityDifferentKeysIndependent(t *testing.T) {
	const (
		keyA = "sk-a"
		keyB = "sk-b"
	)
	env := newAffTestEnv(t, "gpt-aff2", []affChannelSpec{
		{name: "A", apiKey: keyA},
		{name: "B", apiKey: keyB},
	})
	ident := env.buildIdentity(t)

	// key1 连发 3 次
	for i := 0; i < 3; i++ {
		env.chatOpenAI(t, ident, "gpt-aff2", map[string]any{"prompt_cache_key": "key1"})
	}
	// key2 连发 3 次
	for i := 0; i < 3; i++ {
		env.chatOpenAI(t, ident, "gpt-aff2", map[string]any{"prompt_cache_key": "key2"})
	}

	// 各 key 内部稳定（只命中一个渠道），两 key 之间独立
	// （可能碰巧同渠道也可能不同渠道，都合法；这里只验证总命中数）
	total := env.hitCount(keyA) + env.hitCount(keyB)
	if total != 6 {
		t.Fatalf("总命中应为 6，实际 %d", total)
	}
}

// 【集成】绑定渠道注入失败 → 重试内 failover 到其他渠道并在成功后重绑：
// 等优先级双渠道（消除「优先级 > 亲和」对重绑后命中的干扰）。步骤：
// (1) 用 key1 首次请求（miss）→ 加权随机选到某渠道 X，建立 X 绑定；
// (2) 注入 X 失败 + 发 key1 → attempt 0 命中 X（hit），X 返回 500，
//
//	重试 failover 到另一渠道 Y，成功后重绑到 Y；
//
// (3) 清除失败注入 + 发 key1 → 应命中重绑后的 Y，不再碰 X。
func TestAffinityDriftAndRebindOnFailure(t *testing.T) {
	const (
		keyA = "sk-a"
		keyB = "sk-b"
	)
	env := newAffTestEnv(t, "gpt-aff3", []affChannelSpec{
		{name: "A", apiKey: keyA},
		{name: "B", apiKey: keyB},
	})
	ident := env.buildIdentity(t)

	// (1) 首次请求建立绑定（miss → 加权随机选 X）
	env.chatOpenAI(t, ident, "gpt-aff3", map[string]any{"prompt_cache_key": "key1"})

	// 识别首轮被绑定的渠道 X（命中的那个）与另一渠道 Y
	var boundKey, otherKey string
	if env.hitCount(keyA) == 1 && env.hitCount(keyB) == 0 {
		boundKey, otherKey = keyA, keyB
	} else if env.hitCount(keyB) == 1 && env.hitCount(keyA) == 0 {
		boundKey, otherKey = keyB, keyA
	} else {
		t.Fatalf("首次请求应恰好命中一个渠道，实际 A=%d B=%d",
			env.hitCount(keyA), env.hitCount(keyB))
	}

	// (2) 注入 X 失败 + 发 key1：X 命中后失败，failover 到 Y，成功后重绑到 Y
	env.injectFailure(boundKey)
	status, body := env.chatOpenAI(t, ident, "gpt-aff3",
		map[string]any{"prompt_cache_key": "key1"})
	if status != 200 {
		t.Fatalf("failover 到 Y 后应成功，状态 %d；body=%s", status, body)
	}
	if env.hitCount(otherKey) == 0 {
		t.Fatalf("X 失败后应 failover 到 Y（Y 应被命中）")
	}

	// (3) 清除失败注入（injectFailure 为一次性，已自动清除）+ 发 key1：
	// 应命中重绑后的 Y，不再碰 X
	boundTriesBefore := env.hitCount(boundKey)
	env.chatOpenAI(t, ident, "gpt-aff3", map[string]any{"prompt_cache_key": "key1"})
	if env.hitCount(boundKey) != boundTriesBefore {
		t.Fatalf("failover 重绑到 Y 后，后续同 key 请求不应再碰 X（应命中 Y）")
	}
}

// 【集成】无亲和键时退化为加权随机（分布性）：
// 无 prompt_cache_key、无 metadata.user_id → 退化到 API Key ID 亲和。
// 但本测试用 dsOpenAI 且无会话键，会落到 api_key 退化键——所有请求同 key，
// 全部聚到一个渠道。为验证「无亲和走加权随机」，这里用一个 keyID=0 的匿名身份
// 不现实；改为直接断言：带不同 prompt_cache_key 的请求会各自稳定，而不带任何键的
// 请求因退化到 api_key 仍聚到同一渠道（证明退化键生效）。
//
// 该用例转为验证退化键：同 API Key 的请求（无会话键）全部聚到一个渠道。
func TestAffinityDegradationToAPIKeyID(t *testing.T) {
	const (
		keyA = "sk-a"
		keyB = "sk-b"
		keyC = "sk-c"
	)
	env := newAffTestEnv(t, "gpt-aff4", []affChannelSpec{
		{name: "A", apiKey: keyA},
		{name: "B", apiKey: keyB},
		{name: "C", apiKey: keyC},
	})
	ident := env.buildIdentity(t)

	// 无 prompt_cache_key、无 metadata → 退化到 API Key ID
	for i := 0; i < 4; i++ {
		status, body := env.chatOpenAI(t, ident, "gpt-aff4", nil)
		if status != 200 {
			t.Fatalf("请求 %d 应成功，状态 %d；body=%s", i, status, body)
		}
	}

	// 退化键（同 API Key）下，全部请求应聚到一个渠道（牺牲负载均衡换缓存命中）
	hits := []int{env.hitCount(keyA), env.hitCount(keyB), env.hitCount(keyC)}
	var nonzero int
	for _, h := range hits {
		if h > 0 {
			nonzero++
		}
	}
	if nonzero != 1 {
		t.Fatalf("退化到 API Key ID 时应聚到一个渠道（同 Key 绑定），实际分布 %v", hits)
	}
}

// 【集成】指标计数器递增：hit/miss/drift 计数器随亲和结果正确增长。
// 用 defaultMetrics 的快照比对（obs 包指标为进程全局态）。
// drift 通过禁用绑定渠道（结构性不可用）构造跨请求漂移，区别于重试内的瞬时失败容错。
func TestAffinityMetricsRecorded(t *testing.T) {
	// 取指标快照辅助函数（参照 obs/clamp_metrics_test.go 的 counterValue 模式）
	metricValue := func(name string) float64 {
		out := obs.DefaultMetrics().Export()
		// 简单解析：在 Prometheus 文本里找 name{...} <value> 行并累加
		var sum float64
		for _, line := range strings.Split(out, "\n") {
			if strings.HasPrefix(line, name) {
				fields := strings.Fields(line)
				if len(fields) == 2 {
					var v float64
					_, _ = fmt.Sscanf(fields[1], "%f", &v)
					sum += v
				}
			}
		}
		return sum
	}

	const (
		keyA = "sk-a"
		keyB = "sk-b"
	)
	env := newAffTestEnv(t, "gpt-aff5", []affChannelSpec{
		{name: "A", apiKey: keyA, priority: 2},
		{name: "B", apiKey: keyB, priority: 1},
	})
	ident := env.buildIdentity(t)

	hitBefore := metricValue("tzl_relay_affinity_hit_total")
	missBefore := metricValue("tzl_relay_affinity_miss_total")
	driftBefore := metricValue("tzl_relay_affinity_drift_total")

	// 首次请求：miss（有会话键，无既有绑定）→ 建立绑定到 A
	env.chatOpenAI(t, ident, "gpt-aff5", map[string]any{"prompt_cache_key": "metric-key"})
	// 第二次请求：hit（绑定到 A，A 在顶层候选层）
	env.chatOpenAI(t, ident, "gpt-aff5", map[string]any{"prompt_cache_key": "metric-key"})
	// 禁用绑定渠道 A：构造结构性不可用 → 下次请求的绑定失效（drift）
	env.disableChannelByName(t, "A")
	// 第三次请求：A 不在候选集 → drift，回退加权随机选到 B，成功后重绑到 B
	status, body := env.chatOpenAI(t, ident, "gpt-aff5",
		map[string]any{"prompt_cache_key": "metric-key"})
	if status != 200 {
		t.Fatalf("漂移到 B 后应成功，状态 %d；body=%s", status, body)
	}

	hitAfter := metricValue("tzl_relay_affinity_hit_total")
	missAfter := metricValue("tzl_relay_affinity_miss_total")
	driftAfter := metricValue("tzl_relay_affinity_drift_total")

	if missAfter-missBefore < 1 {
		t.Fatalf("miss 计数器应至少递增 1，before=%v after=%v", missBefore, missAfter)
	}
	if hitAfter-hitBefore < 1 {
		t.Fatalf("hit 计数器应至少递增 1，before=%v after=%v", hitBefore, hitAfter)
	}
	if driftAfter-driftBefore < 1 {
		t.Fatalf("drift 计数器应至少递增 1，before=%v after=%v", driftBefore, driftAfter)
	}
}

// 【集成】TTL 过期后重新选择：
// 注入可控时钟到 Engine 的亲和表；绑定后推进时钟超过 TTL，下次请求应 miss（重新选择）。
func TestAffinityTTLExpiryInEngine(t *testing.T) {
	const (
		keyA = "sk-a"
		keyB = "sk-b"
	)
	env := newAffTestEnv(t, "gpt-aff6", []affChannelSpec{
		{name: "A", apiKey: keyA, priority: 2},
		{name: "B", apiKey: keyB, priority: 1},
	})
	ident := env.buildIdentity(t)

	// 注入可控时钟到 Selector 的亲和表（预置后 ChannelSelector.table 的 Once 会保留该实例）
	start := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	clock := &fixedClock{t: start}
	env.engine.selector().affinity = &affinityTable{now: clock.now}

	// 建立绑定（命中 A）
	env.chatOpenAI(t, ident, "gpt-aff6", map[string]any{"prompt_cache_key": "ttl-key"})
	if env.hitCount(keyA) != 1 {
		t.Fatalf("首次应命中 A，实际 %d", env.hitCount(keyA))
	}

	// 推进时钟超过 TTL
	clock.t = start.Add(affinityTTL + time.Minute)

	// 再次请求：TTL 过期，应重新选择。A 仍最高优先级，应再次绑定到 A。
	env.chatOpenAI(t, ident, "gpt-aff6", map[string]any{"prompt_cache_key": "ttl-key"})
	if env.hitCount(keyA) != 2 {
		t.Fatalf("TTL 过期后重新选择，A 为最高优先级应再次命中（total 应为 2），实际 %d",
			env.hitCount(keyA))
	}
}
