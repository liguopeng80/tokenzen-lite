package api

import (
	"net/http"
	"net/url"
	"strconv"
	"testing"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// sessionToken 从客户端 cookie jar 中读取当前会话 token。
func sessionToken(t *testing.T, e *testEnv, c *http.Client) string {
	t.Helper()
	u, err := url.Parse(e.srv.URL)
	if err != nil {
		t.Fatalf("解析测试服务地址失败: %v", err)
	}
	for _, ck := range c.Jar.Cookies(u) {
		if ck.Name == "tzl_session" {
			return ck.Value
		}
	}
	return ""
}

// TestChangePasswordReissuesCurrentSessionToken 覆盖 P2-4：
// 自助修改密码成功后，当前会话被重新签发——响应带 Set-Cookie，新 token 与改密前不同，后续请求仍 200。
func TestChangePasswordReissuesCurrentSessionToken(t *testing.T) {
	e := newTestEnv(t)
	c := e.seedAndLogin(t, "grace", domain.RoleUser)
	oldToken := sessionToken(t, e, c)
	if oldToken == "" {
		t.Fatal("登录后 cookie jar 中应存在会话 token")
	}

	resp, env := e.do(t, c, "PUT", "/api/auth/password",
		map[string]string{"original_password": "password-grace", "password": "new-password-2"})
	if resp.StatusCode != 200 {
		t.Fatalf("修改密码失败: %d %v", resp.StatusCode, env)
	}
	if len(resp.Header.Values("Set-Cookie")) == 0 {
		t.Error("改密响应应携带 Set-Cookie 重新签发会话")
	}
	newToken := sessionToken(t, e, c)
	if newToken == "" || newToken == oldToken {
		t.Errorf("改密后应签发新 token，改密前 %q，改密后 %q", oldToken, newToken)
	}
	resp, _ = e.do(t, c, "GET", "/api/auth/me", nil)
	if resp.StatusCode != 200 {
		t.Errorf("重新签发后的当前会话应保持有效，实际 %d", resp.StatusCode)
	}
}

// TestChangePasswordRevokesOtherSessions 覆盖 P2-4（测试计划 I2）：
// 自助修改密码成功后，该用户改密前建立的另一会话访问 GET /api/auth/me 返回 401。
func TestChangePasswordRevokesOtherSessions(t *testing.T) {
	e := newTestEnv(t)
	current := e.seedAndLogin(t, "judy", domain.RoleUser)

	// 改密前建立的第二个会话
	other := e.client(t)
	resp, _ := e.do(t, other, "POST", "/api/auth/login",
		map[string]string{"username": "judy", "password": "password-judy"})
	if resp.StatusCode != 200 {
		t.Fatalf("第二会话登录失败: %d", resp.StatusCode)
	}

	resp, env := e.do(t, current, "PUT", "/api/auth/password",
		map[string]string{"original_password": "password-judy", "password": "new-password-judy"})
	if resp.StatusCode != 200 {
		t.Fatalf("修改密码失败: %d %v", resp.StatusCode, env)
	}

	resp, _ = e.do(t, other, "GET", "/api/auth/me", nil)
	if resp.StatusCode != 401 {
		t.Errorf("改密后旧会话应 401，实际 %d", resp.StatusCode)
	}
	resp, _ = e.do(t, current, "GET", "/api/auth/me", nil)
	if resp.StatusCode != 200 {
		t.Errorf("改密后当前会话（重新签发）应仍 200，实际 %d", resp.StatusCode)
	}
}

// TestChangePasswordOldPasswordRejected 覆盖 P2-4：
// 自助修改密码后，旧密码登录失败（401），新密码登录成功（200）。
func TestChangePasswordOldPasswordRejected(t *testing.T) {
	e := newTestEnv(t)
	c := e.seedAndLogin(t, "henry", domain.RoleUser)

	resp, env := e.do(t, c, "PUT", "/api/auth/password",
		map[string]string{"original_password": "password-henry", "password": "new-password-3"})
	if resp.StatusCode != 200 {
		t.Fatalf("修改密码失败: %d %v", resp.StatusCode, env)
	}

	resp, _ = e.do(t, e.client(t), "POST", "/api/auth/login",
		map[string]string{"username": "henry", "password": "password-henry"})
	if resp.StatusCode != 401 {
		t.Errorf("改密后旧密码登录应 401，实际 %d", resp.StatusCode)
	}
	resp, _ = e.do(t, e.client(t), "POST", "/api/auth/login",
		map[string]string{"username": "henry", "password": "new-password-3"})
	if resp.StatusCode != 200 {
		t.Errorf("改密后新密码登录应 200，实际 %d", resp.StatusCode)
	}
}

// TestChangePasswordWrongOriginalKeepsSessions 覆盖 P2-4：
// 原密码错误时改密返回 400，且不作废任何会话——该用户另一会话仍可访问。
func TestChangePasswordWrongOriginalKeepsSessions(t *testing.T) {
	e := newTestEnv(t)
	current := e.seedAndLogin(t, "iris", domain.RoleUser)

	other := e.client(t)
	resp, _ := e.do(t, other, "POST", "/api/auth/login",
		map[string]string{"username": "iris", "password": "password-iris"})
	if resp.StatusCode != 200 {
		t.Fatalf("第二会话登录失败: %d", resp.StatusCode)
	}

	resp, _ = e.do(t, current, "PUT", "/api/auth/password",
		map[string]string{"original_password": "wrong-password", "password": "new-password-4"})
	if resp.StatusCode != 400 {
		t.Fatalf("原密码错误应 400，实际 %d", resp.StatusCode)
	}

	resp, _ = e.do(t, other, "GET", "/api/auth/me", nil)
	if resp.StatusCode != 200 {
		t.Errorf("改密失败不应作废其它会话，实际 %d", resp.StatusCode)
	}
	resp, _ = e.do(t, current, "GET", "/api/auth/me", nil)
	if resp.StatusCode != 200 {
		t.Errorf("改密失败不应作废当前会话，实际 %d", resp.StatusCode)
	}
}

// TestAdminResetPasswordRevokesAllTargetSessions 覆盖 P2-4：
// 管理员重置密码后目标用户全部（多个）会话 401，管理员自身与无关第三方用户的会话不受影响。
func TestAdminResetPasswordRevokesAllTargetSessions(t *testing.T) {
	e := newTestEnv(t)
	adminC := e.seedAndLogin(t, "opadmin2", domain.RoleAdmin) // id=1
	target1 := e.seedAndLogin(t, "victim2", domain.RoleUser)  // id=2
	bystander := e.seedAndLogin(t, "carl", domain.RoleUser)   // id=3

	// 目标用户的第二个会话
	target2 := e.client(t)
	resp, _ := e.do(t, target2, "POST", "/api/auth/login",
		map[string]string{"username": "victim2", "password": "password-victim2"})
	if resp.StatusCode != 200 {
		t.Fatalf("目标用户第二会话登录失败: %d", resp.StatusCode)
	}

	resp, env := e.do(t, adminC, "POST", "/api/admin/users/2/reset-password",
		map[string]string{"password": "reset-password-2"})
	if resp.StatusCode != 200 {
		t.Fatalf("重置密码失败: %d %v", resp.StatusCode, env)
	}
	// 测试计划 I6：管理端提示语的 API 契约
	if msg, _ := env["message"].(string); msg != "密码已重置，该用户全部登录已失效" {
		t.Errorf("重置密码响应 message 应为固定提示语，实际: %q", msg)
	}

	resp, _ = e.do(t, target1, "GET", "/api/auth/me", nil)
	if resp.StatusCode != 401 {
		t.Errorf("目标用户会话 1 应 401，实际 %d", resp.StatusCode)
	}
	resp, _ = e.do(t, target2, "GET", "/api/auth/me", nil)
	if resp.StatusCode != 401 {
		t.Errorf("目标用户会话 2 应 401，实际 %d", resp.StatusCode)
	}
	resp, _ = e.do(t, adminC, "GET", "/api/admin/users/", nil)
	if resp.StatusCode != 200 {
		t.Errorf("管理员自身会话不应受影响，实际 %d", resp.StatusCode)
	}
	resp, _ = e.do(t, bystander, "GET", "/api/auth/me", nil)
	if resp.StatusCode != 200 {
		t.Errorf("无关第三方用户会话不应受影响，实际 %d", resp.StatusCode)
	}
	resp, _ = e.do(t, e.client(t), "POST", "/api/auth/login",
		map[string]string{"username": "victim2", "password": "reset-password-2"})
	if resp.StatusCode != 200 {
		t.Errorf("目标用户新密码登录应 200，实际 %d", resp.StatusCode)
	}
}

// TestRootResetOwnPasswordReissuesCurrentSession 覆盖 P2-4：
// root 通过管理端重置自己的密码——当前会话重新签发后续请求仍 200，root 的其他会话 401。
func TestRootResetOwnPasswordReissuesCurrentSession(t *testing.T) {
	e := newTestEnv(t)
	rootC := e.seedAndLogin(t, "rootself", domain.RoleRoot) // id=1

	// root 的第二个会话
	rootOther := e.client(t)
	resp, _ := e.do(t, rootOther, "POST", "/api/auth/login",
		map[string]string{"username": "rootself", "password": "password-rootself"})
	if resp.StatusCode != 200 {
		t.Fatalf("root 第二会话登录失败: %d", resp.StatusCode)
	}

	var rootUser store.User
	if err := e.db.Where("username = ?", "rootself").First(&rootUser).Error; err != nil {
		t.Fatalf("查询 root 用户失败: %v", err)
	}

	oldToken := sessionToken(t, e, rootC)
	resp, env := e.do(t, rootC, "POST",
		"/api/admin/users/"+strconv.FormatInt(rootUser.ID, 10)+"/reset-password",
		map[string]string{"password": "root-new-password"})
	if resp.StatusCode != 200 {
		t.Fatalf("root 重置自身密码失败: %d %v", resp.StatusCode, env)
	}
	newToken := sessionToken(t, e, rootC)
	if newToken == "" || newToken == oldToken {
		t.Errorf("root 重置自身密码后当前会话应重新签发，改密前 %q，改密后 %q", oldToken, newToken)
	}

	resp, _ = e.do(t, rootC, "GET", "/api/admin/users/", nil)
	if resp.StatusCode != 200 {
		t.Errorf("root 当前会话重新签发后应仍可访问管理端，实际 %d", resp.StatusCode)
	}
	resp, _ = e.do(t, rootOther, "GET", "/api/auth/me", nil)
	if resp.StatusCode != 401 {
		t.Errorf("root 的其他会话应 401，实际 %d", resp.StatusCode)
	}
	resp, _ = e.do(t, e.client(t), "POST", "/api/auth/login",
		map[string]string{"username": "rootself", "password": "root-new-password"})
	if resp.StatusCode != 200 {
		t.Errorf("root 新密码登录应 200，实际 %d", resp.StatusCode)
	}
}
