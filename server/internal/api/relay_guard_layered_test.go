package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/liguopeng80/tokenzen-lite/server/internal/auth"
	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// --- 分层门禁集成测试：用户维度限流、用户并发子配额、单用户密钥数量上限 ---

// seedExtraKey 为已有用户直接入库一把额外的 API Key，返回明文。
func (e *testEnv) seedExtraKey(t *testing.T, userID int64, name string) string {
	t.Helper()
	gen, err := auth.GenerateKey()
	if err != nil {
		t.Fatalf("生成密钥失败: %v", err)
	}
	k := &store.APIKey{
		UserID: userID, Name: name,
		KeyHash: gen.Hash, KeyPrefix: gen.Prefix,
		Status: domain.KeyEnabled,
	}
	if err := e.db.Create(k).Error; err != nil {
		t.Fatalf("入库密钥失败: %v", err)
	}
	return gen.Plain
}

// TestRelayGuardUserRPMAcrossKeys 按用户维度限流与按密钥上限同时生效：
// 用户级窗口合并计数全部密钥的请求，换用第二把密钥无法绕过用户级上限；
// 其他用户不受影响。
func TestRelayGuardUserRPMAcrossKeys(t *testing.T) {
	e := newTestEnv(t)

	uid, key1 := e.seedRelayUser(t, "layered-rpm-a", 0, nil)
	key2 := e.seedExtraKey(t, uid, "layered-rpm-a-2")
	_, otherKey := e.seedRelayUser(t, "layered-rpm-b", 0, nil)

	e.setSetting(t, "rate_limit_per_user_rpm", "1")
	defer e.setSetting(t, "rate_limit_per_user_rpm", "240")

	// 第一次请求耗尽用户级窗口配额（按密钥上限 120 远未触及）。
	if resp, _ := e.guardPost(t, key1); resp.StatusCode == http.StatusTooManyRequests {
		t.Fatal("首次请求不应被限流")
	}
	// 换用同一用户的第二把密钥：密钥级窗口全新，但用户级窗口已满 → 429。
	resp, body := e.guardPost(t, key2)
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Errorf("换密钥后仍应命中用户级限流（429），实际 %d，响应: %v", resp.StatusCode, body)
	}
	if errObj, ok := body["error"].(map[string]any); !ok || errObj["code"] != "rate_limited" {
		t.Errorf("期望 error.code=rate_limited，实际: %v", body)
	}
	// 其他用户不受该用户窗口影响。
	if respOther, bodyOther := e.guardPost(t, otherKey); respOther.StatusCode == http.StatusTooManyRequests {
		t.Errorf("其他用户不应被限流，响应: %v", bodyOther)
	}
}

// TestRelayGuardKeyRPMStillEffective 用户级上限放宽时，按密钥上限仍独立生效（取更严者）。
func TestRelayGuardKeyRPMStillEffective(t *testing.T) {
	e := newTestEnv(t)

	_, key := e.seedRelayUser(t, "layered-keyrpm", 0, nil)
	e.setSetting(t, "rate_limit_per_key_rpm", "1")
	defer e.setSetting(t, "rate_limit_per_key_rpm", "120")

	if resp, _ := e.guardPost(t, key); resp.StatusCode == http.StatusTooManyRequests {
		t.Fatal("首次请求不应被限流")
	}
	resp, body := e.guardPost(t, key)
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Errorf("密钥级上限应独立生效（429），实际 %d，响应: %v", resp.StatusCode, body)
	}
}

// TestRelayGuardUserConcurrencyQuota 用户并发子配额（一级闸门）：
// 单个用户占满自己的子配额后被 503 拒绝，全局仍有空余槽位，其他用户不受影响。
func TestRelayGuardUserConcurrencyQuota(t *testing.T) {
	e := newTestEnv(t)
	upstream, entered, release := newBlockingUpstream(t)

	uidA, keyA := e.seedRelayUser(t, "layered-conc-a", 2_000_000_000, nil)
	keyA2 := e.seedExtraKey(t, uidA, "layered-conc-a-2")
	_, keyB := e.seedRelayUser(t, "layered-conc-b", 0, nil)
	e.seedModel(t, "glm-5")
	e.seedChannel(t, "ch-layered-conc", upstream.URL, 0, []string{"glm-5"}, nil)
	e.setSetting(t, "max_concurrent_requests_per_user", "1")
	defer e.setSetting(t, "max_concurrent_requests_per_user", "10")

	// 用户 A 的占位请求挂起在阻塞式上游，占满其子配额（1）。
	stop := e.holdLargeRequest(t, keyA, entered, release)
	defer stop()

	// 用户 A 换第二把密钥仍被子配额拒绝：子配额按用户合并计数。
	resp, body := e.guardPost(t, keyA2)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("用户子配额占满应 503，实际 %d，响应: %v", resp.StatusCode, body)
	}
	if errObj, ok := body["error"].(map[string]any); !ok || errObj["code"] != "overloaded" {
		t.Errorf("期望 error.code=overloaded，实际: %v", body)
	}
	// 用户 B 不受用户 A 子配额影响（之后因业务原因失败也不应是 503）。
	if respB, bodyB := e.guardPost(t, keyB); respB.StatusCode == http.StatusServiceUnavailable {
		t.Errorf("其他用户不应被用户 A 的子配额拒绝，响应: %v", bodyB)
	}
}

// TestMeCreateKeyCountLimit 单用户密钥数量上限：
// 达到上限后创建被拒绝并提示上限值；置 0 后恢复不限制。
func TestMeCreateKeyCountLimit(t *testing.T) {
	e := newTestEnv(t)
	c := e.seedAndLogin(t, "layered-keycap", domain.RoleUser)

	e.setSetting(t, "max_keys_per_user", "2")
	defer e.setSetting(t, "max_keys_per_user", "20")

	for i, name := range []string{"cap-key-1", "cap-key-2"} {
		resp, env := e.do(t, c, "POST", "/api/me/keys/", map[string]any{"name": name})
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("第 %d 把密钥应创建成功，实际 %d，响应: %v", i+1, resp.StatusCode, env)
		}
	}
	resp, env := e.do(t, c, "POST", "/api/me/keys/", map[string]any{"name": "cap-key-3"})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("达到上限后创建应 400，实际 %d，响应: %v", resp.StatusCode, env)
	}
	if msg, _ := env["message"].(string); msg == "" {
		t.Error("拒绝响应应携带用户可读的上限提示")
	}

	// 上限置 0 = 不限制。
	e.setSetting(t, "max_keys_per_user", "0")
	resp, env = e.do(t, c, "POST", "/api/me/keys/", map[string]any{"name": "cap-key-3"})
	if resp.StatusCode != http.StatusCreated {
		t.Errorf("上限置 0 后创建应成功，实际 %d，响应: %v", resp.StatusCode, env)
	}
}

// TestLayeredGuardSettingsRegistered 三个新增设置项已登记且默认值正确，
// 非法值被校验拒绝。
func TestLayeredGuardSettingsRegistered(t *testing.T) {
	e := newTestEnv(t)
	ctx := context.Background()

	wantDefaults := map[string]int64{
		"rate_limit_per_user_rpm":          240,
		"max_concurrent_requests_per_user": 10,
		"max_keys_per_user":                20,
	}
	for key, want := range wantDefaults {
		if got := e.deps.Settings.GetInt64(ctx, key); got != want {
			t.Errorf("%s 默认值应为 %d，实际 %d", key, want, got)
		}
	}

	rootC := e.seedAndLogin(t, "layered-set-root", domain.RoleRoot)
	for _, key := range []string{"max_concurrent_requests_per_user", "max_keys_per_user"} {
		for _, bad := range []int{-1, 1001} {
			resp, env := e.do(t, rootC, "PUT", "/api/admin/settings",
				map[string]any{"key": key, "value": bad})
			if resp.StatusCode != http.StatusBadRequest {
				t.Errorf("%s 写入 %d 应 400，实际 %d，响应: %v", key, bad, resp.StatusCode, env)
			}
		}
	}
}
