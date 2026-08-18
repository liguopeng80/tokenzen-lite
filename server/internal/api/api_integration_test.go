package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/liguopeng80/tokenzen-lite/server/internal/alerting"
	"github.com/liguopeng80/tokenzen-lite/server/internal/audit"
	"github.com/liguopeng80/tokenzen-lite/server/internal/auth"
	"github.com/liguopeng80/tokenzen-lite/server/internal/billing"
	"github.com/liguopeng80/tokenzen-lite/server/internal/config"
	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/ratelimit"
	"github.com/liguopeng80/tokenzen-lite/server/internal/relay"
	"github.com/liguopeng80/tokenzen-lite/server/internal/secrets"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store/migrate"
)

// 集成测试基座：需要 TZL_TEST_DATABASE_URL，未设置时跳过。

type testEnv struct {
	srv  *httptest.Server
	db   *gorm.DB
	deps Deps
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	url := os.Getenv("TZL_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("未设置 TZL_TEST_DATABASE_URL，跳过 API 集成测试")
	}
	if err := migrate.Up(url); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}
	db, err := store.Open(url)
	if err != nil {
		t.Fatalf("连接测试库失败: %v", err)
	}
	if err := db.Exec(`TRUNCATE users, api_keys, sessions, models, model_prices,
		model_peak_rules, credit_ledger, redemptions, settings,
		channels, channel_costs, usage_logs, departments, projects, audit_logs, alert_events,
		daily_spend, usage_daily_rollup, usage_rollup_state,
		integrations, service_tokens, idempotency_records
		RESTART IDENTITY CASCADE`).Error; err != nil {
		t.Fatalf("清空测试表失败: %v", err)
	}
	sqlDB, _ := db.DB()
	// 每个测试新开一个连接池，测试结束必须关闭；
	// 否则包内数十个测试累计的空闲连接会耗尽 PostgreSQL 连接上限。
	t.Cleanup(func() { _ = sqlDB.Close() })
	cfg := &config.Config{
		Env: config.EnvDev, LogLevel: "error", RegisterEnabled: true,
	}
	settings := store.NewSettingsRepo(db)
	billingSvc := billing.NewService(db)
	channelRepo := store.NewChannelRepo(db)
	costRepo := store.NewChannelCostRepo(db)
	modelRepo := store.NewModelRepo(db)
	usageLogs := store.NewUsageLogRepo(db)
	box := secrets.New("test-encrypt-key")
	alertEvents := store.NewAlertEventRepo(db)
	auditLogs := store.NewAuditLogRepo(db)
	spend := store.NewSpendRepo(db)
	alerts := &alerting.Service{Events: alertEvents, Settings: settings, Secrets: box}
	deps := Deps{
		Cfg:           cfg,
		DB:            db,
		Sessions:      auth.NewSessionManager(sqlDB, false),
		Users:         store.NewUserRepo(db),
		Keys:          store.NewAPIKeyRepo(db),
		Models:        modelRepo,
		Ledger:        store.NewLedgerRepo(db),
		Redemptions:   store.NewRedemptionRepo(db),
		Settings:      settings,
		Billing:       billingSvc,
		Channels:      channelRepo,
		Costs:         costRepo,
		UsageLogs:     usageLogs,
		Secrets:       box,
		Stats:         store.NewStatsRepo(db),
		Limiter:       ratelimit.NewMemoryLimiter(),
		Gate:          ratelimit.NewConcurrencyGate(),
		LoginLock:     ratelimit.NewFailureLocker(),
		Departments:   store.NewDepartmentRepo(db),
		Projects:      store.NewProjectRepo(db),
		AuditLogs:     auditLogs,
		Audit:         audit.NewRecorder(auditLogs),
		AlertEvents:   alertEvents,
		Alerts:        alerts,
		Spend:         spend,
		Rollup:        store.NewRollupRepo(db),
		Integrations:  store.NewIntegrationRepo(db),
		ServiceTokens: store.NewServiceTokenRepo(db),
		Idempotency:   store.NewIdempotencyRepo(db),
		Relay: &relay.Engine{
			DB: db, Channels: channelRepo, Costs: costRepo, Models: modelRepo,
			Billing: billingSvc, UsageLogs: usageLogs, Settings: settings,
			Secrets: box, Client: &http.Client{Timeout: 10 * time.Second},
			Spend: spend, Alerts: alerts,
		},
	}
	srv := httptest.NewServer(NewRouter(deps))
	t.Cleanup(srv.Close)
	return &testEnv{srv: srv, db: db, deps: deps}
}

// client 返回带独立 cookie jar 的 HTTP 客户端（一个 client = 一个会话身份）。
func (e *testEnv) client(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("创建 cookie jar 失败: %v", err)
	}
	return &http.Client{Jar: jar}
}

func (e *testEnv) do(t *testing.T, c *http.Client, method, path string, body any) (*http.Response, map[string]any) {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("编码请求体失败: %v", err)
		}
	}
	req, err := http.NewRequest(method, e.srv.URL+path, &buf)
	if err != nil {
		t.Fatalf("构建请求失败: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("请求 %s %s 失败: %v", method, path, err)
	}
	defer resp.Body.Close()
	var env map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&env)
	return resp, env
}

// catalogClient 返回一个可读模型目录的登录客户端。模型目录已收敛为需登录
// （上架清单属于内部信息），用例里凡是读目录的地方都要带会话身份。
// 用户名带序号避免同一用例内重复种入。
func catalogClient(t *testing.T, e *testEnv) *http.Client {
	t.Helper()
	catalogClientSeq++
	return e.seedAndLogin(t, fmt.Sprintf("catalogreader%d", catalogClientSeq), domain.RoleUser)
}

var catalogClientSeq int

// seedUser 直接在库中种入用户并返回登录后的客户端。
func (e *testEnv) seedAndLogin(t *testing.T, username string, role domain.Role) *http.Client {
	t.Helper()
	hash, err := auth.HashPassword("password-" + username)
	if err != nil {
		t.Fatalf("哈希失败: %v", err)
	}
	u := &store.User{
		Username: username, PasswordHash: hash,
		Role: role, Status: domain.UserEnabled,
	}
	if err := e.db.Create(u).Error; err != nil {
		t.Fatalf("种入用户失败: %v", err)
	}
	c := e.client(t)
	resp, env := e.do(t, c, "POST", "/api/auth/login",
		map[string]string{"username": username, "password": "password-" + username})
	if resp.StatusCode != 200 {
		t.Fatalf("种子用户 %s 登录失败: %v %v", username, resp.StatusCode, env)
	}
	return c
}

// 越权矩阵（匿名 401 / 普通用户 403 / 管理员 2xx / root 独占）已扩展为
// 覆盖全部端点的表驱动用例，见 authz_matrix_test.go 的 TestAuthzMatrix
// 与路由清单完整性核对 TestAuthzRouteInventoryComplete。

func TestAdminCannotManagePeerAdmin(t *testing.T) {
	e := newTestEnv(t)
	adminC := e.seedAndLogin(t, "admin1", domain.RoleAdmin)
	e.seedAndLogin(t, "admin2", domain.RoleAdmin) // id=2

	resp, _ := e.do(t, adminC, "POST", "/api/admin/users/2/reset-password",
		map[string]string{"password": "hacked-password"})
	if resp.StatusCode != 403 {
		t.Errorf("admin 重置同级 admin 密码应 403，实际 %d", resp.StatusCode)
	}
	resp, _ = e.do(t, adminC, "DELETE", "/api/admin/users/2", nil)
	if resp.StatusCode != 403 {
		t.Errorf("admin 删除同级 admin 应 403，实际 %d", resp.StatusCode)
	}
}

// TestAdminViewScopedByManagement 覆盖 P2-10：
// 查看用户详情与用户密钥列表必须与重置密码、删除等操作使用同一管理边界——
// 普通管理员对超级管理员应 403，对可管辖的普通用户应 200。
func TestAdminViewScopedByManagement(t *testing.T) {
	e := newTestEnv(t)
	adminC := e.seedAndLogin(t, "viewadmin", domain.RoleAdmin) // id=1
	e.seedAndLogin(t, "viewroot", domain.RoleRoot)             // id=2
	e.seedAndLogin(t, "viewuser", domain.RoleUser)             // id=3

	cases := []struct {
		name string
		path string
		want int
	}{
		{"admin 查看 root 详情应 403", "/api/admin/users/2", 403},
		{"admin 查看 root 密钥列表应 403", "/api/admin/users/2/keys", 403},
		{"admin 查看普通用户详情应 200", "/api/admin/users/3", 200},
		{"admin 查看普通用户密钥列表应 200", "/api/admin/users/3/keys", 200},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, env := e.do(t, adminC, "GET", tc.path, nil)
			if resp.StatusCode != tc.want {
				t.Errorf("期望 %d，实际 %d，响应: %v", tc.want, resp.StatusCode, env)
			}
		})
	}
}

// TestAdminUserDetailAndKeysBoundary 补全 P2-10 的管理边界与契约用例：
// 详情/密钥列表两端点的响应字段脱敏、root 的管辖范围、不存在用户的 404、
// 中间件层的 401/403、以及 admin 对自身走 /api/me 而非管理端点的决策固化。
func TestAdminUserDetailAndKeysBoundary(t *testing.T) {
	e := newTestEnv(t)
	anon := e.client(t)
	adminC := e.seedAndLogin(t, "bdadmin", domain.RoleAdmin) // id=1
	rootC := e.seedAndLogin(t, "bdroot", domain.RoleRoot)    // id=2
	userC := e.seedAndLogin(t, "bduser", domain.RoleUser)    // id=3

	// 为普通用户预置 2 个 API Key
	for i := 0; i < 2; i++ {
		k := &store.APIKey{
			UserID: 3, Name: fmt.Sprintf("seed-key-%d", i),
			KeyHash:   fmt.Sprintf("hash-%d", i),
			KeyPrefix: fmt.Sprintf("tzl-seed%d", i),
			Status:    domain.KeyEnabled,
		}
		if err := e.deps.Keys.Create(t.Context(), k); err != nil {
			t.Fatalf("预置 API Key 失败: %v", err)
		}
	}

	// I1：admin 查看普通用户详情，字段完整且不泄露 password_hash
	resp, env := e.do(t, adminC, "GET", "/api/admin/users/3", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("admin 查看普通用户详情应 200，实际 %d %v", resp.StatusCode, env)
	}
	detail, _ := env["data"].(map[string]any)
	if detail["username"] != "bduser" || detail["role"] != "user" {
		t.Errorf("详情应含 username/role，实际: %v", detail)
	}
	if _, ok := detail["credit_balance"]; !ok {
		t.Errorf("详情应含 credit_balance，实际: %v", detail)
	}
	if raw, _ := json.Marshal(env); strings.Contains(string(raw), "password_hash") {
		t.Error("用户详情响应不应包含 password_hash")
	}

	// I6：admin 查看普通用户密钥列表，分页信封 + 条目脱敏
	resp, env = e.do(t, adminC, "GET", "/api/admin/users/3/keys", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("admin 查看普通用户密钥列表应 200，实际 %d %v", resp.StatusCode, env)
	}
	page, _ := env["data"].(map[string]any)
	for _, field := range []string{"page", "page_size", "total", "items"} {
		if _, ok := page[field]; !ok {
			t.Errorf("密钥列表应为分页信封，缺少字段 %s: %v", field, page)
		}
	}
	if int(page["total"].(float64)) != 2 {
		t.Errorf("密钥列表 total 应为 2，实际 %v", page["total"])
	}
	raw, _ := json.Marshal(page["items"])
	if !strings.Contains(string(raw), "key_prefix") {
		t.Error("密钥条目应含 key_prefix")
	}
	if strings.Contains(string(raw), "key_hash") || strings.Contains(string(raw), "hash-0") {
		t.Error("密钥条目不应含 key_hash")
	}

	// I4/I5/I8/I9/I11：管辖范围与 404 契约
	statusCases := []struct {
		name   string
		client *http.Client
		path   string
		want   int
	}{
		{"root 查看 admin 详情应 200", rootC, "/api/admin/users/1", 200},
		{"root 查看 admin 密钥列表应 200", rootC, "/api/admin/users/1/keys", 200},
		{"admin 查看不存在用户详情应 404", adminC, "/api/admin/users/9999", 404},
		{"admin 查看不存在用户密钥列表应 404", adminC, "/api/admin/users/9999/keys", 404},
		{"admin 经管理端点查看自己详情应 403", adminC, "/api/admin/users/1", 403},
		{"admin 经管理端点查看自己密钥应 403", adminC, "/api/admin/users/1/keys", 403},
		// I10：中间件层 401/403
		{"匿名查看用户详情应 401", anon, "/api/admin/users/3", 401},
		{"匿名查看用户密钥列表应 401", anon, "/api/admin/users/3/keys", 401},
		{"普通用户查看用户详情应 403", userC, "/api/admin/users/3", 403},
		{"普通用户查看用户密钥列表应 403", userC, "/api/admin/users/3/keys", 403},
	}
	for _, tc := range statusCases {
		t.Run(tc.name, func(t *testing.T) {
			resp, env := e.do(t, tc.client, "GET", tc.path, nil)
			if resp.StatusCode != tc.want {
				t.Errorf("期望 %d，实际 %d，响应: %v", tc.want, resp.StatusCode, env)
			}
		})
	}
}

func TestLoginFailures(t *testing.T) {
	e := newTestEnv(t)
	e.seedAndLogin(t, "dave", domain.RoleUser)

	c := e.client(t)
	resp, _ := e.do(t, c, "POST", "/api/auth/login",
		map[string]string{"username": "dave", "password": "wrong"})
	if resp.StatusCode != 401 {
		t.Errorf("错误密码应 401，实际 %d", resp.StatusCode)
	}

	e.db.Model(&store.User{}).Where("username = ?", "dave").Update("status", "disabled")
	resp, _ = e.do(t, c, "POST", "/api/auth/login",
		map[string]string{"username": "dave", "password": "password-dave"})
	if resp.StatusCode != 403 {
		t.Errorf("禁用账号登录应 403，实际 %d", resp.StatusCode)
	}
}

// TestUserKeyFlow 覆盖 E2E 流程：注册 → 登录 → 建 Key → 列表脱敏 → 更新 → 删除。
func TestUserKeyFlow(t *testing.T) {
	e := newTestEnv(t)
	// 自助注册默认关闭（内部部署由管理员建号），本流程测的是开放注册后的行为。
	e.setSetting(t, "register_enabled", "true")
	c := e.client(t)

	resp, _ := e.do(t, c, "POST", "/api/auth/register",
		map[string]string{"username": "eve", "password": "password123", "display_name": "Eve"})
	if resp.StatusCode != 201 {
		t.Fatalf("注册失败: %d", resp.StatusCode)
	}
	resp, _ = e.do(t, c, "POST", "/api/auth/login",
		map[string]string{"username": "eve", "password": "password123"})
	if resp.StatusCode != 200 {
		t.Fatalf("登录失败: %d", resp.StatusCode)
	}

	resp, env := e.do(t, c, "POST", "/api/me/keys/", map[string]any{"name": "开发用"})
	if resp.StatusCode != 201 {
		t.Fatalf("创建密钥失败: %d %v", resp.StatusCode, env)
	}
	data := env["data"].(map[string]any)
	plain, _ := data["key"].(string)
	if !strings.HasPrefix(plain, "tzl-") {
		t.Fatalf("创建响应应含 tzl- 明文，实际: %v", data)
	}
	keyID := int64(data["id"].(float64))

	// 列表不应出现明文
	resp, env = e.do(t, c, "GET", "/api/me/keys/", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("列表失败: %d", resp.StatusCode)
	}
	raw, _ := json.Marshal(env)
	if strings.Contains(string(raw), plain) {
		t.Error("密钥列表不应包含完整明文")
	}
	if !strings.Contains(string(raw), plain[:12]) {
		t.Error("密钥列表应包含前缀便于识别")
	}

	resp, _ = e.do(t, c, "PUT", fmt.Sprintf("/api/me/keys/%d", keyID),
		map[string]any{"name": "改名", "status": "disabled"})
	if resp.StatusCode != 200 {
		t.Fatalf("更新密钥失败: %d", resp.StatusCode)
	}

	// 他人的 Key 不可见不可删（IDOR 防护）
	c2 := e.seedAndLogin(t, "mallory", domain.RoleUser)
	resp, _ = e.do(t, c2, "GET", fmt.Sprintf("/api/me/keys/%d", keyID), nil)
	if resp.StatusCode != 404 {
		t.Errorf("他人查看密钥应 404，实际 %d", resp.StatusCode)
	}
	resp, _ = e.do(t, c2, "DELETE", fmt.Sprintf("/api/me/keys/%d", keyID), nil)
	if resp.StatusCode != 404 {
		t.Errorf("他人删除密钥应 404，实际 %d", resp.StatusCode)
	}

	resp, _ = e.do(t, c, "DELETE", fmt.Sprintf("/api/me/keys/%d", keyID), nil)
	if resp.StatusCode != 200 {
		t.Fatalf("删除密钥失败: %d", resp.StatusCode)
	}
	_, env = e.do(t, c, "GET", "/api/me/keys/", nil)
	page := env["data"].(map[string]any)
	if int(page["total"].(float64)) != 0 {
		t.Errorf("删除后密钥列表应为空，实际 total=%v", page["total"])
	}
}

// TestChangePasswordInvalidatesOtherSessions 覆盖 P2-4：
// 用户修改密码后，其它设备的旧会话立即失效（401），当前会话重新签发后仍可用。
func TestChangePasswordInvalidatesOtherSessions(t *testing.T) {
	e := newTestEnv(t)
	current := e.seedAndLogin(t, "frank", domain.RoleUser)

	// 同一账号的第二个会话，模拟另一台设备（或攻击者）的登录
	other := e.client(t)
	resp, _ := e.do(t, other, "POST", "/api/auth/login",
		map[string]string{"username": "frank", "password": "password-frank"})
	if resp.StatusCode != 200 {
		t.Fatalf("第二会话登录失败: %d", resp.StatusCode)
	}

	resp, env := e.do(t, current, "PUT", "/api/auth/password",
		map[string]string{"original_password": "password-frank", "password": "new-password-1"})
	if resp.StatusCode != 200 {
		t.Fatalf("修改密码失败: %d %v", resp.StatusCode, env)
	}

	// 旧会话访问受保护端点应 401
	resp, _ = e.do(t, other, "GET", "/api/auth/me", nil)
	if resp.StatusCode != 401 {
		t.Errorf("改密后旧会话应 401，实际 %d", resp.StatusCode)
	}
	// 当前会话（重新签发）仍可用
	resp, _ = e.do(t, current, "GET", "/api/auth/me", nil)
	if resp.StatusCode != 200 {
		t.Errorf("改密后当前会话应保持有效，实际 %d", resp.StatusCode)
	}
	// 新密码可登录
	resp, _ = e.do(t, e.client(t), "POST", "/api/auth/login",
		map[string]string{"username": "frank", "password": "new-password-1"})
	if resp.StatusCode != 200 {
		t.Errorf("新密码登录应 200，实际 %d", resp.StatusCode)
	}
}

// TestAdminResetPasswordInvalidatesSessions 覆盖 P2-4：
// 管理员重置密码后，目标用户全部会话失效，响应提示全部登录已失效；管理员会话不受影响。
func TestAdminResetPasswordInvalidatesSessions(t *testing.T) {
	e := newTestEnv(t)
	adminC := e.seedAndLogin(t, "opadmin", domain.RoleAdmin) // id=1
	targetC := e.seedAndLogin(t, "victim", domain.RoleUser)  // id=2

	resp, env := e.do(t, adminC, "POST", "/api/admin/users/2/reset-password",
		map[string]string{"password": "reset-password-1"})
	if resp.StatusCode != 200 {
		t.Fatalf("重置密码失败: %d %v", resp.StatusCode, env)
	}
	if msg, _ := env["message"].(string); !strings.Contains(msg, "全部登录已失效") {
		t.Errorf("重置密码响应应提示全部登录已失效，实际: %v", env["message"])
	}

	// 目标用户旧会话应 401
	resp, _ = e.do(t, targetC, "GET", "/api/auth/me", nil)
	if resp.StatusCode != 401 {
		t.Errorf("重置密码后目标用户旧会话应 401，实际 %d", resp.StatusCode)
	}
	// 管理员自己的会话不受影响
	resp, _ = e.do(t, adminC, "GET", "/api/auth/me", nil)
	if resp.StatusCode != 200 {
		t.Errorf("重置他人密码不应影响管理员会话，实际 %d", resp.StatusCode)
	}
	// 目标用户可用新密码重新登录
	resp, _ = e.do(t, e.client(t), "POST", "/api/auth/login",
		map[string]string{"username": "victim", "password": "reset-password-1"})
	if resp.StatusCode != 200 {
		t.Errorf("重置后的新密码登录应 200，实际 %d", resp.StatusCode)
	}
}

func TestRegisterDisabled(t *testing.T) {
	e := newTestEnv(t)
	// 关闭注册后再请求
	e2 := &testEnv{db: e.db}
	sqlDB, _ := e.db.DB()
	deps := Deps{
		Cfg:      &config.Config{Env: config.EnvDev, LogLevel: "error", RegisterEnabled: false},
		DB:       e.db,
		Sessions: auth.NewSessionManager(sqlDB, false),
		Users:    store.NewUserRepo(e.db),
		Keys:     store.NewAPIKeyRepo(e.db),
	}
	e2.srv = httptest.NewServer(NewRouter(deps))
	t.Cleanup(e2.srv.Close)

	resp, _ := e2.do(t, e2.client(t), "POST", "/api/auth/register",
		map[string]string{"username": "walter", "password": "password123"})
	if resp.StatusCode != 403 {
		t.Errorf("注册关闭时应 403，实际 %d", resp.StatusCode)
	}
}
