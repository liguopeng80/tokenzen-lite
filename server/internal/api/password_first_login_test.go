package api

import (
	"net/http"
	"strconv"
	"testing"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// login 用给定口令登录并返回带会话的客户端；失败时返回状态码。
func (e *testEnv) login(t *testing.T, username, password string) (*http.Client, int) {
	t.Helper()
	c := e.client(t)
	resp, _ := e.do(t, c, "POST", "/api/auth/login",
		map[string]string{"username": username, "password": password})
	return c, resp.StatusCode
}

// mustChangePassword 读取库中该用户的强制改密标志。
func mustChangePassword(t *testing.T, e *testEnv, username string) bool {
	t.Helper()
	var u store.User
	if err := e.db.Where("username = ?", username).First(&u).Error; err != nil {
		t.Fatalf("查询用户 %s 失败: %v", username, err)
	}
	return u.MustChangePassword
}

// 管理员建号后，账号带强制改密标志：本人能登录、能读取自身，但业务接口一律被拒；
// 自己改掉密码后立即恢复正常。这条链路的业务意义是初始密码在本人改掉之前
// 同时存在于管理员与转达渠道上，此时不应允许该账号创建 API Key。
func TestFirstLoginMustChangePassword(t *testing.T) {
	e := newTestEnv(t)
	adminC := e.seedAndLogin(t, "pwadmin", domain.RoleAdmin)

	if _, env := e.do(t, adminC, "POST", "/api/admin/users", map[string]any{
		"username": "pwmember", "password": "initial-password-1", "role": "user",
	}); env["success"] != true {
		t.Fatalf("建号失败：%v", env)
	}
	if !mustChangePassword(t, e, "pwmember") {
		t.Fatal("管理员建号后应带强制改密标志")
	}

	memberC, code := e.login(t, "pwmember", "initial-password-1")
	if code != http.StatusOK {
		t.Fatalf("初始密码应能登录，实际 %d", code)
	}
	// 读取自身放行：前端据此判断要不要跳改密页。
	if resp, env := e.do(t, memberC, "GET", "/api/auth/me", nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("未改密时读取自身应放行，实际 %d：%v", resp.StatusCode, env)
	}
	// 业务接口被拒，且提示指向改密而非「权限不足」。
	for _, path := range []string{"/api/me/balance", "/api/me/keys/"} {
		resp, env := e.do(t, memberC, "GET", path, nil)
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("未改密时 %s 应 403，实际 %d", path, resp.StatusCode)
		}
		if msg, _ := env["message"].(string); msg != "请先修改初始密码" {
			t.Errorf("%s 的拒绝提示应指向改密，实际 %q", path, msg)
		}
	}
	// 创建密钥同样被拒：未改密的账号不应产生可长期使用的凭证。
	if resp, _ := e.do(t, memberC, "POST", "/api/me/keys/",
		map[string]any{"name": "should-not-exist"}); resp.StatusCode != http.StatusForbidden {
		t.Errorf("未改密时创建密钥应 403，实际 %d", resp.StatusCode)
	}

	if resp, env := e.do(t, memberC, "PUT", "/api/auth/password", map[string]any{
		"original_password": "initial-password-1", "password": "chosen-password-1",
	}); resp.StatusCode != http.StatusOK {
		t.Fatalf("改密应成功，实际 %d：%v", resp.StatusCode, env)
	}
	if mustChangePassword(t, e, "pwmember") {
		t.Error("本人改密后应清除强制改密标志")
	}
	if resp, env := e.do(t, memberC, "GET", "/api/me/balance", nil); resp.StatusCode != http.StatusOK {
		t.Errorf("改密后业务接口应放行，实际 %d：%v", resp.StatusCode, env)
	}
}

// 管理员为他人重置密码后重新置位；root 重置自己的密码属于本人操作，不置位——
// 否则管理员改自己的密码会把自己挡在管理端之外。
func TestResetPasswordRequiresChangeExceptForSelf(t *testing.T) {
	e := newTestEnv(t)
	rootC := e.seedAndLogin(t, "pwroot", domain.RoleRoot)
	e.seedAndLogin(t, "pwtarget", domain.RoleUser)

	targetID := userIDOf(t, e, "pwtarget")
	if _, env := e.do(t, rootC, "POST",
		"/api/admin/users/"+strconv.FormatInt(targetID, 10)+"/reset-password",
		map[string]any{"password": "reset-password-1"}); env["success"] != true {
		t.Fatalf("重置他人密码失败：%v", env)
	}
	if !mustChangePassword(t, e, "pwtarget") {
		t.Error("管理员重置他人密码后应要求首次登录改密")
	}

	rootID := userIDOf(t, e, "pwroot")
	if _, env := e.do(t, rootC, "POST",
		"/api/admin/users/"+strconv.FormatInt(rootID, 10)+"/reset-password",
		map[string]any{"password": "root-new-password-1"}); env["success"] != true {
		t.Fatalf("重置自己密码失败：%v", env)
	}
	if mustChangePassword(t, e, "pwroot") {
		t.Error("本人重置自己的密码不应置位强制改密")
	}
}

// 建号与批量导入均可不填密码：系统生成一次性初始密码，明文只在响应中返回一次，
// 且该密码可直接用于登录。这消除了管理员为每人拟定并保管明文密码的人工动作。
func TestGeneratedInitialPasswordReturnedOnce(t *testing.T) {
	e := newTestEnv(t)
	adminC := e.seedAndLogin(t, "genadmin", domain.RoleAdmin)

	_, env := e.do(t, adminC, "POST", "/api/admin/users",
		map[string]any{"username": "genmember", "role": "user"})
	data, _ := env["data"].(map[string]any)
	single, _ := data["initial_password"].(string)
	if single == "" {
		t.Fatalf("未指定密码时应返回系统生成的初始密码：%v", env)
	}
	if _, code := e.login(t, "genmember", single); code != http.StatusOK {
		t.Errorf("系统生成的初始密码应能登录，实际 %d", code)
	}

	_, env = e.do(t, adminC, "POST", "/api/admin/users/import", map[string]any{
		"items": []map[string]any{
			{"username": "genimport1"},
			{"username": "genimport2", "password": "explicit-password-1"},
		},
	})
	data, _ = env["data"].(map[string]any)
	results, _ := data["results"].([]any)
	if len(results) != 2 {
		t.Fatalf("导入应逐条回报结果：%v", data)
	}
	first, _ := results[0].(map[string]any)
	generated, _ := first["initial_password"].(string)
	if generated == "" {
		t.Errorf("密码留空的记录应返回生成的初始密码：%v", first)
	}
	if _, code := e.login(t, "genimport1", generated); code != http.StatusOK {
		t.Errorf("导入生成的初始密码应能登录，实际 %d", code)
	}
	second, _ := results[1].(map[string]any)
	if pwd, _ := second["initial_password"].(string); pwd != "" {
		t.Errorf("管理员自行指定密码时不应回显：%v", second)
	}
	if !mustChangePassword(t, e, "genimport2") {
		t.Error("批量导入的账号应要求首次登录改密")
	}
}
