package api

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
)

// 连续失败锁定：达到阈值后，即使密码正确也在锁定期内被拒绝。
func TestLoginLockoutAfterConsecutiveFailures(t *testing.T) {
	e := newTestEnv(t)
	e.seedAndLogin(t, "locktarget", domain.RoleUser)

	c := e.client(t)
	for i := 0; i < loginFailureThreshold; i++ {
		resp, _ := e.do(t, c, "POST", "/api/auth/login",
			map[string]string{"username": "locktarget", "password": "wrong"})
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("第 %d 次错误密码应 401，实际 %d", i+1, resp.StatusCode)
		}
	}
	resp, _ := e.do(t, c, "POST", "/api/auth/login",
		map[string]string{"username": "locktarget", "password": "password-locktarget"})
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Errorf("锁定期内正确密码也应 429，实际 %d", resp.StatusCode)
	}
}

// 成功登录清零失败计数：失败未达阈值后成功一次，再失败不会因累计而提前锁定。
func TestLoginSuccessResetsFailureCount(t *testing.T) {
	e := newTestEnv(t)
	e.seedAndLogin(t, "resetuser", domain.RoleUser)

	c := e.client(t)
	for i := 0; i < loginFailureThreshold-1; i++ {
		e.do(t, c, "POST", "/api/auth/login",
			map[string]string{"username": "resetuser", "password": "wrong"})
	}
	resp, _ := e.do(t, c, "POST", "/api/auth/login",
		map[string]string{"username": "resetuser", "password": "password-resetuser"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("未达阈值时正确密码应登录成功，实际 %d", resp.StatusCode)
	}
	// 清零后再失败一次，不应触发锁定。
	e.do(t, c, "POST", "/api/auth/login",
		map[string]string{"username": "resetuser", "password": "wrong"})
	resp, _ = e.do(t, c, "POST", "/api/auth/login",
		map[string]string{"username": "resetuser", "password": "password-resetuser"})
	if resp.StatusCode != http.StatusOK {
		t.Errorf("成功登录清零计数后不应被锁定，实际 %d", resp.StatusCode)
	}
}

// 单用户名维度限流：窗口内尝试次数超限后返回 429（不分成败）。
func TestLoginPerUsernameRateLimit(t *testing.T) {
	e := newTestEnv(t)
	e.seedAndLogin(t, "ratelimited", domain.RoleUser) // 第 1 次尝试

	c := e.client(t)
	for i := 1; i < loginUserRatePerMin; i++ {
		resp, _ := e.do(t, c, "POST", "/api/auth/login",
			map[string]string{"username": "ratelimited", "password": "password-ratelimited"})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("第 %d 次登录应成功，实际 %d", i+1, resp.StatusCode)
		}
	}
	resp, _ := e.do(t, c, "POST", "/api/auth/login",
		map[string]string{"username": "ratelimited", "password": "password-ratelimited"})
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Errorf("超出单用户名每分钟上限应 429，实际 %d", resp.StatusCode)
	}
}

// 注册按来源 IP 限流：窗口内尝试次数超限后返回 429。
func TestRegisterIPRateLimit(t *testing.T) {
	e := newTestEnv(t)
	e.setSetting(t, "register_enabled", "true")
	c := e.client(t)
	for i := 0; i < registerIPRatePerHour; i++ {
		resp, _ := e.do(t, c, "POST", "/api/auth/register",
			map[string]string{"username": fmt.Sprintf("reguser%d", i), "password": "password123"})
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("第 %d 次注册应成功，实际 %d", i+1, resp.StatusCode)
		}
	}
	resp, _ := e.do(t, c, "POST", "/api/auth/register",
		map[string]string{"username": "reguserlast", "password": "password123"})
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Errorf("超出单 IP 注册上限应 429，实际 %d", resp.StatusCode)
	}
}
