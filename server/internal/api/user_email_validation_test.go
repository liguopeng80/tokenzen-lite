package api

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// 邮箱是余额不足提醒的收件地址，写错的直接后果是提醒发不出去且没人察觉。
// 建号、改资料、员工自助改资料三条路径都要拦住形态不合格的地址。
func TestEmailFormatRejectedOnAllWritePaths(t *testing.T) {
	e := newTestEnv(t)
	adminC := e.seedAndLogin(t, "emailadmin", domain.RoleAdmin)

	bad := []string{"not-an-email", "no@domain", "a b@example.com", "@example.com", "user@"}
	for _, addr := range bad {
		resp, env := e.do(t, adminC, "POST", "/api/admin/users/", map[string]any{
			"username": "emailbad", "password": "password123", "email": addr,
		})
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("建号时邮箱 %q 应被拒绝，实际 %d：%v", addr, resp.StatusCode, env)
		}
	}

	resp, env := e.do(t, adminC, "POST", "/api/admin/users/", map[string]any{
		"username": "emailok", "password": "password123", "email": "  ops@example.com  ",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("合法邮箱应建号成功，实际 %d：%v", resp.StatusCode, env)
	}
	targetID := int64(env["data"].(map[string]any)["id"].(float64))
	var u store.User
	if err := e.db.First(&u, targetID).Error; err != nil {
		t.Fatalf("查用户失败：%v", err)
	}
	if u.Email != "ops@example.com" {
		t.Errorf("邮箱应去掉首尾空白后存储，实际 %q", u.Email)
	}

	// 管理员改资料。
	if resp, env := e.do(t, adminC, "PUT", fmt.Sprintf("/api/admin/users/%d", targetID),
		map[string]any{"email": "still-not-an-email"}); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("改资料时非法邮箱应 400，实际 %d：%v", resp.StatusCode, env)
	}

	// 员工自助改资料。
	userC := e.seedAndLogin(t, "emailself", domain.RoleUser)
	if resp, env := e.do(t, userC, "PUT", "/api/auth/profile",
		map[string]any{"email": "bad@@example.com"}); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("自助改资料时非法邮箱应 400，实际 %d：%v", resp.StatusCode, env)
	}
	if resp, env := e.do(t, userC, "PUT", "/api/auth/profile",
		map[string]any{"email": ""}); resp.StatusCode != http.StatusOK {
		t.Errorf("留空表示不填写，应放行，实际 %d：%v", resp.StatusCode, env)
	}
}

// 建号时可一并发放初始积分：余额为零的账号即使密钥正确也会被拒绝调用。
func TestCreateUserGrantsInitialCredits(t *testing.T) {
	e := newTestEnv(t)
	adminC := e.seedAndLogin(t, "initcreditadmin", domain.RoleAdmin)

	resp, env := e.do(t, adminC, "POST", "/api/admin/users/", map[string]any{
		"username": "initcredit", "password": "password123", "initial_credits": 500_000,
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("建号失败：%d %v", resp.StatusCode, env)
	}
	data := env["data"].(map[string]any)
	if balance := int64(data["credit_balance"].(float64)); balance != 500_000 {
		t.Errorf("响应中的余额应含初始发放，实际 %d", balance)
	}

	userID := int64(data["id"].(float64))
	var u store.User
	if err := e.db.First(&u, userID).Error; err != nil {
		t.Fatalf("查用户失败：%v", err)
	}
	if u.CreditBalance != 500_000 {
		t.Errorf("库中余额应为 500000，实际 %d", u.CreditBalance)
	}
	// 发放走账务唯一入口，流水与余额同事务写入。
	if n := e.ledgerCount(t, userID); n != 1 {
		t.Errorf("初始发放应留下 1 条流水，实际 %d", n)
	}
	e.assertReconcile(t)

	// 负数被拒。
	if resp, env := e.do(t, adminC, "POST", "/api/admin/users/", map[string]any{
		"username": "initcreditneg", "password": "password123", "initial_credits": -1,
	}); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("负数初始积分应 400，实际 %d：%v", resp.StatusCode, env)
	}
}
