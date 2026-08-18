package api

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
)

// --- 分层门禁补充集成测试：槽位释放、拒绝日志标识、密钥上限边界 ---

// TestRelayGuardUserQuotaSlotRelease 用户并发子配额槽位在请求结束后释放：
// 正常完成与上游 5xx 失败两条路径结束后，同用户后续请求均应重新放行。
func TestRelayGuardUserQuotaSlotRelease(t *testing.T) {
	e := newTestEnv(t)
	upstream, entered, release := newBlockingUpstream(t)

	uid, key := e.seedRelayUser(t, "layered-release-a", 2_000_000_000, nil)
	key2 := e.seedExtraKey(t, uid, "layered-release-a-2")
	e.seedModel(t, "glm-5")
	e.seedChannel(t, "ch-layered-release", upstream.URL, 0, []string{"glm-5"}, nil)
	e.setSetting(t, "max_concurrent_requests_per_user", "1")
	defer e.setSetting(t, "max_concurrent_requests_per_user", "10")

	// 占位请求挂起期间子配额占满 → 503。
	stop := e.holdLargeRequest(t, key, entered, release)
	if resp, body := e.guardPost(t, key2); resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("占位期间同用户请求应 503，实际 %d，响应: %v", resp.StatusCode, body)
	}
	// 正常完成分支：占位请求结束后槽位释放，同用户请求重新放行。
	stop()
	if resp, body := e.guardPost(t, key2); resp.StatusCode == http.StatusServiceUnavailable {
		t.Errorf("占位请求正常结束后子配额应释放，实际仍 503，响应: %v", body)
	}

	// 上游 5xx 失败分支：失败请求结束后槽位同样释放。
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"message":"boom"}}`, http.StatusInternalServerError)
	}))
	defer failing.Close()
	uidF, keyF := e.seedRelayUser(t, "layered-release-f", 2_000_000_000, nil)
	keyF2 := e.seedExtraKey(t, uidF, "layered-release-f-2")
	e.seedChannel(t, "ch-layered-fail", failing.URL, 5, []string{"glm-5"}, nil)
	// 上游失败的请求（非 503 拒绝）结束后再次请求，不应被子配额挡住。
	if resp, _ := e.guardPost(t, keyF); resp.StatusCode == http.StatusServiceUnavailable {
		t.Fatal("首个请求不应被并发闸门拒绝")
	}
	if resp, body := e.guardPost(t, keyF2); resp.StatusCode == http.StatusServiceUnavailable {
		t.Errorf("上游失败的请求结束后子配额应释放，实际仍 503，响应: %v", body)
	}
}

// syncBuffer 并发安全的日志捕获缓冲区。
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// TestGuardRejectionLogsCarryIdentity 限流 429 与并发 503 两条拒绝路径的
// WARN 日志均携带 user_id 与 key_id，满足"仅凭日志定位占用者"的可观测性要求。
func TestGuardRejectionLogsCarryIdentity(t *testing.T) {
	e := newTestEnv(t)
	upstream, entered, release := newBlockingUpstream(t)

	// 捕获全局 slog 输出（obs.Logger 基于 slog.Default）。
	capture := &syncBuffer{}
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(capture, nil)))
	defer slog.SetDefault(old)

	// 429 路径：用户级 RPM=1，第二次请求被拒。
	_, key := e.seedRelayUser(t, "layered-log-a", 2_000_000_000, nil)
	e.setSetting(t, "rate_limit_per_user_rpm", "1")
	if resp, _ := e.guardPost(t, key); resp.StatusCode == http.StatusTooManyRequests {
		t.Fatal("首次请求不应被限流")
	}
	if resp, _ := e.guardPost(t, key); resp.StatusCode != http.StatusTooManyRequests {
		t.Fatal("第二次请求应命中用户级限流")
	}
	e.setSetting(t, "rate_limit_per_user_rpm", "240")

	// 503 路径：全局并发上限 1 被占住后第二个用户被拒。
	_, keyB := e.seedRelayUser(t, "layered-log-b", 2_000_000_000, nil)
	e.seedModel(t, "glm-5")
	e.seedChannel(t, "ch-layered-log", upstream.URL, 0, []string{"glm-5"}, nil)
	e.setSetting(t, "max_concurrent_requests", "1")
	stop := e.holdLargeRequest(t, key, entered, release)
	if resp, _ := e.guardPost(t, keyB); resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatal("占位期间第二个用户的请求应 503")
	}
	stop()
	e.setSetting(t, "max_concurrent_requests", "40")

	logs := capture.String()
	var rateLine, gateLine string
	for _, line := range strings.Split(logs, "\n") {
		if strings.Contains(line, "限流拒绝请求") {
			rateLine = line
		}
		if strings.Contains(line, "并发闸门拒绝请求") {
			gateLine = line
		}
	}
	if rateLine == "" {
		t.Fatal("未捕获到限流拒绝的 WARN 日志")
	}
	if gateLine == "" {
		t.Fatal("未捕获到并发闸门拒绝的 WARN 日志")
	}
	for name, line := range map[string]string{"限流拒绝": rateLine, "并发闸门拒绝": gateLine} {
		if !strings.Contains(line, `"level":"WARN"`) {
			t.Errorf("%s 日志级别应为 WARN：%s", name, line)
		}
		if !strings.Contains(line, `"user_id"`) {
			t.Errorf("%s 日志应携带 user_id：%s", name, line)
		}
		if !strings.Contains(line, `"key_id"`) {
			t.Errorf("%s 日志应携带 key_id：%s", name, line)
		}
	}
}

// TestMeKeyCountLimitDeleteThenCreate 密钥数量上限的回收路径：
// 达到上限被拒后，删除一把密钥即可重新创建。
func TestMeKeyCountLimitDeleteThenCreate(t *testing.T) {
	e := newTestEnv(t)
	c := e.seedAndLogin(t, "layered-keycap-del", domain.RoleUser)

	e.setSetting(t, "max_keys_per_user", "2")
	defer e.setSetting(t, "max_keys_per_user", "20")

	var firstID float64
	for i, name := range []string{"del-key-1", "del-key-2"} {
		resp, env := e.do(t, c, "POST", "/api/me/keys/", map[string]any{"name": name})
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("第 %d 把密钥应创建成功，实际 %d，响应: %v", i+1, resp.StatusCode, env)
		}
		if i == 0 {
			firstID = env["data"].(map[string]any)["id"].(float64)
		}
	}
	if resp, env := e.do(t, c, "POST", "/api/me/keys/", map[string]any{"name": "del-key-3"}); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("达到上限后创建应 400，实际 %d，响应: %v", resp.StatusCode, env)
	}
	if resp, env := e.do(t, c, "DELETE",
		"/api/me/keys/"+strconv.Itoa(int(firstID)), nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("删除密钥应成功，实际 %d，响应: %v", resp.StatusCode, env)
	}
	if resp, env := e.do(t, c, "POST", "/api/me/keys/", map[string]any{"name": "del-key-3"}); resp.StatusCode != http.StatusCreated {
		t.Errorf("删除一把后创建应成功，实际 %d，响应: %v", resp.StatusCode, env)
	}
}

// TestMeKeyCountLimitLegacyOverLimit 存量超限用户的兼容性：
// 上限下调后，已持有的密钥仍可认证调用、可列出、可删除，仅新建被拒。
func TestMeKeyCountLimitLegacyOverLimit(t *testing.T) {
	e := newTestEnv(t)

	uid, _ := e.seedRelayUser(t, "layered-legacy", 0, nil)
	extraKey := e.seedExtraKey(t, uid, "legacy-extra-1")
	e.seedExtraKey(t, uid, "legacy-extra-2") // 共 3 把
	e.setSetting(t, "max_keys_per_user", "2")
	defer e.setSetting(t, "max_keys_per_user", "20")

	// 存量密钥仍可通过 /v1 认证与门禁（后续业务错误与门禁无关，只要求不被拒于认证/限流/并发层）。
	resp, body := e.guardPost(t, extraKey)
	switch resp.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden,
		http.StatusTooManyRequests, http.StatusServiceUnavailable:
		t.Errorf("存量超限密钥不应被门禁拒绝，实际 %d，响应: %v", resp.StatusCode, body)
	}

	c := e.client(t)
	if resp, env := e.do(t, c, "POST", "/api/auth/login", map[string]string{
		"username": "layered-legacy", "password": "password-layered-legacy",
	}); resp.StatusCode != http.StatusOK {
		t.Fatalf("登录失败: %d %v", resp.StatusCode, env)
	}
	// 可列出全部 3 把。
	respL, envL := e.do(t, c, "GET", "/api/me/keys/", nil)
	if respL.StatusCode != http.StatusOK {
		t.Fatalf("列出密钥应成功，实际 %d，响应: %v", respL.StatusCode, envL)
	}
	items := envL["data"].(map[string]any)["items"].([]any)
	if len(items) != 3 {
		t.Errorf("应列出 3 把存量密钥，实际 %d", len(items))
	}
	// 新建被拒。
	if respC, envC := e.do(t, c, "POST", "/api/me/keys/", map[string]any{"name": "legacy-new"}); respC.StatusCode != http.StatusBadRequest {
		t.Errorf("超限用户新建密钥应 400，实际 %d，响应: %v", respC.StatusCode, envC)
	}
	// 可删除。
	delID := items[0].(map[string]any)["id"].(float64)
	if respD, envD := e.do(t, c, "DELETE",
		"/api/me/keys/"+strconv.Itoa(int(delID)), nil); respD.StatusCode != http.StatusOK {
		t.Errorf("超限用户删除密钥应成功，实际 %d，响应: %v", respD.StatusCode, envD)
	}
}

// TestSettingsEffectiveAllExposesLayeredKeys EffectiveAll 输出包含三个新增设置项
// 的默认值与描述，供管理端设置页展示。
func TestSettingsEffectiveAllExposesLayeredKeys(t *testing.T) {
	e := newTestEnv(t)
	all := e.deps.Settings.EffectiveAll(context.Background())
	wantDefaults := map[string]int64{
		"rate_limit_per_user_rpm":          240,
		"max_concurrent_requests_per_user": 10,
		"max_keys_per_user":                20,
	}
	found := map[string]bool{}
	for _, item := range all {
		key, _ := item["key"].(string)
		want, cared := wantDefaults[key]
		if !cared {
			continue
		}
		found[key] = true
		if v, ok := item["value"].(int64); !ok || v != want {
			t.Errorf("%s 默认生效值应为 int64(%d)，实际 %v (%T)", key, want, item["value"], item["value"])
		}
		if d, _ := item["describe"].(string); d == "" {
			t.Errorf("%s 的 describe 不应为空", key)
		}
	}
	for key := range wantDefaults {
		if !found[key] {
			t.Errorf("EffectiveAll 未包含 %s", key)
		}
	}
}
