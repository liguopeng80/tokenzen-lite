package api

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
)

// TestPasswordlessAccount 覆盖 R3（验收 #6 的建号与登录侧）：
// 无口令托管账号建号不生成口令、不置「首次改密」，登录被拒，重置密码被拒。
// 凭该账号的 API Key 调 /v1 的正向用例随 R2 代签发 Key（批次 D）一并补齐。
func TestPasswordlessAccount(t *testing.T) {
	e := newTestEnv(t)
	adminC := e.seedAndLogin(t, "r3admin", domain.RoleAdmin) // id=1

	// 1. 建无口令账号：不回显 initial_password、不置 must_change_password、库中哈希为空。
	resp, env := e.do(t, adminC, "POST", "/api/admin/users/", map[string]any{
		"username":     "hosted1",
		"display_name": "托管账号一",
		"passwordless": true,
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("建无口令账号应 201，实际 %d %v", resp.StatusCode, env)
	}
	data, _ := env["data"].(map[string]any)
	if data == nil {
		t.Fatalf("响应缺少 data: %v", env)
	}
	if _, has := data["initial_password"]; has {
		t.Errorf("无口令账号不应回显 initial_password，实际 %v", data["initial_password"])
	}
	if mc, _ := data["must_change_password"].(bool); mc {
		t.Errorf("无口令账号不应置 must_change_password，实际 %v", data["must_change_password"])
	}
	var hash string
	if err := e.db.Raw("SELECT password_hash FROM users WHERE username = 'hosted1'").Row().Scan(&hash); err != nil {
		t.Fatalf("查询无口令账号哈希失败: %v", err)
	}
	if hash != "" {
		t.Errorf("无口令账号哈希应为空，实际 %q", hash)
	}
	hostedID := int64(data["id"].(float64))

	// 2. 无口令账号不能登录（与密码错误同话术，不泄露账号存在性）。
	loginResp, _ := e.do(t, e.client(t), "POST", "/api/auth/login",
		map[string]string{"username": "hosted1", "password": "anything123"})
	if loginResp.StatusCode != http.StatusUnauthorized {
		t.Errorf("无口令账号登录应 401，实际 %d", loginResp.StatusCode)
	}

	// 3. 重置密码对无口令账号被拒：给托管账号设密码会破坏「不用于登录」的不变式。
	resetResp, _ := e.do(t, adminC, "POST",
		fmt.Sprintf("/api/admin/users/%d/reset-password", hostedID),
		map[string]string{"password": "newpassword123"})
	if resetResp.StatusCode != http.StatusBadRequest {
		t.Errorf("重置无口令账号密码应 400，实际 %d", resetResp.StatusCode)
	}

	// 4. passwordless 与 password 同时给应 400。
	conflictResp, _ := e.do(t, adminC, "POST", "/api/admin/users/", map[string]any{
		"username":     "conflict1",
		"password":     "somepass123",
		"passwordless": true,
	})
	if conflictResp.StatusCode != http.StatusBadRequest {
		t.Errorf("passwordless 与 password 同时给应 400，实际 %d", conflictResp.StatusCode)
	}

	// 5. 对照：普通账号仍回显 initial_password 并置 must_change_password。
	resp2, env2 := e.do(t, adminC, "POST", "/api/admin/users/", map[string]any{"username": "normal1"})
	if resp2.StatusCode != http.StatusCreated {
		t.Fatalf("建普通账号应 201，实际 %d %v", resp2.StatusCode, env2)
	}
	data2, _ := env2["data"].(map[string]any)
	if data2["initial_password"] == nil || data2["initial_password"] == "" {
		t.Errorf("普通账号应回显 initial_password，实际 %v", data2["initial_password"])
	}
	if mc, _ := data2["must_change_password"].(bool); !mc {
		t.Errorf("普通账号应置 must_change_password=true")
	}
}
