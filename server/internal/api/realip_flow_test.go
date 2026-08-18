package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/obs"
)

// --- 来源 IP 采信策略集成测试（报告 P3-1）---
//
// 覆盖：未配置可信代理时伪造 X-Real-IP 不生效（I2/I3/I7）；
// 显式配置可信代理时头值被采信并参与 Key IP 白名单判定与用量日志（I4/I5/I6）。

// proxiedServer 以指定可信代理列表构建第二个路由实例（共享同一测试库与依赖）。
func (e *testEnv) proxiedServer(t *testing.T, trustedProxies []string) *httptest.Server {
	t.Helper()
	deps := e.deps
	cfg := *e.deps.Cfg
	cfg.TrustedProxies = trustedProxies
	deps.Cfg = &cfg
	srv := httptest.NewServer(NewRouter(deps))
	t.Cleanup(srv.Close)
	return srv
}

// relayPostRealIP 携带 X-Real-IP 头调 /v1/chat/completions。
func relayPostRealIP(t *testing.T, baseURL, apiKey, realIP string) (*http.Response, map[string]any) {
	t.Helper()
	payload, _ := json.Marshal(map[string]any{
		"model": "glm-5", "messages": []map[string]any{{"role": "user", "content": "hi"}},
	})
	req, err := http.NewRequest("POST", baseURL+"/v1/chat/completions", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("构建请求失败: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	if realIP != "" {
		req.Header.Set(obs.HeaderRealIP, realIP)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("中继请求失败: %v", err)
	}
	defer resp.Body.Close()
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	return resp, body
}

func errorCode(body map[string]any) any {
	if errObj, ok := body["error"].(map[string]any); ok {
		return errObj["code"]
	}
	return nil
}

// newUsageUpstream 返回一个报告固定 usage 的 mock 上游。
func newUsageUpstream(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"x","model":"glm-5","choices":[{"message":{"content":"hi"}}],
			"usage":{"prompt_tokens":10,"completion_tokens":10}}`)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// I2：未配置可信代理时，伪造 X-Real-IP 无法绕过 API Key IP 白名单。
func TestRealIPForgedHeaderCannotBypassKeyWhitelist(t *testing.T) {
	e := newTestEnv(t)
	uid, key := e.seedRelayUser(t, "xff-forge", 1_000_000, nil)
	e.db.Exec(`UPDATE api_keys SET allowed_ips = '["10.255.255.1"]' WHERE user_id = ?`, uid)

	resp, body := relayPostRealIP(t, e.srv.URL, key, "10.255.255.1")
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("伪造 X-Real-IP 应仍被 IP 白名单拒绝（403），实际 %d，响应: %v", resp.StatusCode, body)
	}
	if code := errorCode(body); code != "ip_not_allowed" {
		t.Errorf("期望 error.code=ip_not_allowed，实际 %v", code)
	}
}

// I3：未配置可信代理时，白名单含真实连接来源（127.0.0.1），
// 携带白名单外的伪造 X-Real-IP 也不误伤合法来源（与 I2 构成成功/失败配对）。
func TestRealIPForgedHeaderDoesNotAffectLegitSource(t *testing.T) {
	e := newTestEnv(t)
	upstream := newUsageUpstream(t)
	uid, key := e.seedRelayUser(t, "xff-legit", 1_000_000, nil)
	e.seedModel(t, "glm-5")
	e.seedChannel(t, "ch-realip", upstream.URL, 0, []string{"glm-5"}, nil)
	e.db.Exec(`UPDATE api_keys SET allowed_ips = '["127.0.0.1"]' WHERE user_id = ?`, uid)

	resp, body := relayPostRealIP(t, e.srv.URL, key, "10.255.255.1")
	if resp.StatusCode != http.StatusOK {
		t.Errorf("合法来源携带伪造头不应被误伤，期望 200，实际 %d，响应: %v", resp.StatusCode, body)
	}
}

// I4/I5/I6：配置可信代理后，X-Real-IP 被采信——通过白名单（I4）、
// 白名单外被拒绝（I5）、用量日志记录头中解析出的真实 IP（I6）。
func TestRealIPTrustedProxyHeaderHonored(t *testing.T) {
	e := newTestEnv(t)
	proxied := e.proxiedServer(t, []string{"127.0.0.1"})
	upstream := newUsageUpstream(t)
	uid, key := e.seedRelayUser(t, "xff-trusted", 1_000_000, nil)
	e.seedModel(t, "glm-5")
	e.seedChannel(t, "ch-trusted", upstream.URL, 0, []string{"glm-5"}, nil)
	e.db.Exec(`UPDATE api_keys SET allowed_ips = '["198.51.100.9"]' WHERE user_id = ?`, uid)

	// I4：可信代理传递的真实 IP 命中白名单 → 通过门禁并完成中继
	resp, body := relayPostRealIP(t, proxied.URL, key, "198.51.100.9")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("可信代理传递的白名单 IP 应通过，期望 200，实际 %d，响应: %v", resp.StatusCode, body)
	}

	// I6：用量日志的 client_ip 应为 X-Real-IP 解析值，而非连接地址 127.0.0.1
	log := e.waitUsageLog(t, uid)
	if log.ClientIP != "198.51.100.9" {
		t.Errorf("usage_logs.client_ip 应为 X-Real-IP 解析值 198.51.100.9，实际 %q", log.ClientIP)
	}

	// I5：采信后的头值参与白名单判定——白名单外地址被拒绝
	resp, body = relayPostRealIP(t, proxied.URL, key, "203.0.113.5")
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("可信代理传递白名单外 IP 应 403，实际 %d，响应: %v", resp.StatusCode, body)
	}
	if code := errorCode(body); code != "ip_not_allowed" {
		t.Errorf("期望 error.code=ip_not_allowed，实际 %v", code)
	}
}

// I7：未配置可信代理时，登录连续失败触发锁定后，
// 变换 X-Real-IP 头再以正确密码登录仍被锁定（伪造头无法绕过登录失败锁）。
func TestRealIPForgedHeaderCannotBypassLoginLock(t *testing.T) {
	e := newTestEnv(t)
	e.seedAndLogin(t, "locktarget", domain.RoleUser)

	for i := 0; i < 5; i++ {
		resp, _ := e.do(t, e.client(t), "POST", "/api/auth/login",
			map[string]string{"username": "locktarget", "password": "wrong-password"})
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("第 %d 次错误密码应 401，实际 %d", i+1, resp.StatusCode)
		}
	}

	payload, _ := json.Marshal(map[string]string{
		"username": "locktarget", "password": "password-locktarget",
	})
	req, err := http.NewRequest("POST", e.srv.URL+"/api/auth/login", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("构建请求失败: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(obs.HeaderRealIP, "203.0.113.99")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("登录请求失败: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Errorf("锁定期内伪造 X-Real-IP 以正确密码登录应 429，实际 %d", resp.StatusCode)
	}
}
