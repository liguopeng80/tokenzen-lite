package api

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
)

// 用户自助修改资料的字段级全局开关：管理员关闭对应键后，PUT /auth/profile 携带该字段返回 403；
// 键开启或该字段未传时不拦截。管理员侧的 PUT /admin/users/{id} 不受影响。
func TestProfileFieldLock(t *testing.T) {
	e := newTestEnv(t)
	userC := e.seedAndLogin(t, "profilelockuser", domain.RoleUser)

	// 默认两键均 true，正常修改。
	if resp, env := e.do(t, userC, "PUT", "/api/auth/profile",
		map[string]any{"display_name": "新昵称"}); resp.StatusCode != http.StatusOK {
		t.Fatalf("默认允许修改显示名称，应 200，实际 %d：%v", resp.StatusCode, env)
	}

	// 关闭显示名称修改。
	setSetting(t, e, "profile_display_name_editable", false)
	if resp, env := e.do(t, userC, "PUT", "/api/auth/profile",
		map[string]any{"display_name": "再改一次"}); resp.StatusCode != http.StatusForbidden {
		t.Errorf("关闭 display_name 开关后自助修改应 403，实际 %d：%v", resp.StatusCode, env)
	}
	// 关闭但请求不携带该字段（只改邮箱），不应被该开关拦截。
	if resp, env := e.do(t, userC, "PUT", "/api/auth/profile",
		map[string]any{"email": "lock@example.com"}); resp.StatusCode != http.StatusOK {
		t.Errorf("关闭 display_name 开关但不传该字段应放行，实际 %d：%v", resp.StatusCode, env)
	}

	// 关闭邮箱修改。
	setSetting(t, e, "profile_email_editable", false)
	if resp, env := e.do(t, userC, "PUT", "/api/auth/profile",
		map[string]any{"email": "again@example.com"}); resp.StatusCode != http.StatusForbidden {
		t.Errorf("关闭 email 开关后自助修改应 403，实际 %d：%v", resp.StatusCode, env)
	}
	// 两键都关、两字段都不传 → 维持既有的「无可更新字段」400，而非 403。
	if resp, env := e.do(t, userC, "PUT", "/api/auth/profile",
		map[string]any{}); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("两键都关但都不传字段应 400（无可更新字段），实际 %d：%v", resp.StatusCode, env)
	}

	// 重新开启后恢复自助修改。
	setSetting(t, e, "profile_display_name_editable", true)
	setSetting(t, e, "profile_email_editable", true)
	if resp, env := e.do(t, userC, "PUT", "/api/auth/profile",
		map[string]any{"display_name": "恢复", "email": "restored@example.com"}); resp.StatusCode != http.StatusOK {
		t.Errorf("重新开启后应允许自助修改，实际 %d：%v", resp.StatusCode, env)
	}
}

// 用户侧锁定不影响管理员路径：管理员始终可改任意字段。
func TestProfileFieldLockNotAffectAdmin(t *testing.T) {
	e := newTestEnv(t)
	adminC := e.seedAndLogin(t, "profilelockadmin", domain.RoleAdmin)
	resp, env := e.do(t, adminC, "POST", "/api/admin/users/", map[string]any{
		"username": "profilelocktarget", "password": "password123",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("建号失败：%d %v", resp.StatusCode, env)
	}
	targetID := int64(env["data"].(map[string]any)["id"].(float64))

	setSetting(t, e, "profile_display_name_editable", false)
	setSetting(t, e, "profile_email_editable", false)
	if resp, env := e.do(t, adminC, "PUT",
		fmt.Sprintf("/api/admin/users/%d", targetID),
		map[string]any{"display_name": "管理员改的", "email": "admin@example.com"}); resp.StatusCode != http.StatusOK {
		t.Errorf("管理员路径不受字段锁定影响，应 200，实际 %d：%v", resp.StatusCode, env)
	}
}
