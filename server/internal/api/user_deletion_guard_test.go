package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// 账号删除的护栏（P1-1）：积分流水是对账唯一事实源，一旦账号产生了流水或调用记录，
// 物理删除会连带销毁这些历史，使已发生的消费无法复算。删除因此只允许用于
// 尚未产生任何记录的误建账号，其余场景一律走禁用。
// 应用层在 handleAdminDeleteUser 中拦截，数据库层由 credit_ledger 的 RESTRICT 外键兜底。

// seedPlainUser 种入一个不登录的普通用户，返回其 id。
func seedPlainUser(t *testing.T, e *testEnv, username string) int64 {
	t.Helper()
	u := &store.User{
		Username: username, PasswordHash: "x",
		Role: domain.RoleUser, Status: domain.UserEnabled,
	}
	if err := e.db.Create(u).Error; err != nil {
		t.Fatalf("种入用户 %s 失败: %v", username, err)
	}
	return u.ID
}

// TestDeleteUserWithLedgerRejected 有积分流水的账号删除被拒 409，且账号与流水都还在。
func TestDeleteUserWithLedgerRejected(t *testing.T) {
	e := newTestEnv(t)
	adminC := e.seedAndLogin(t, "delguard-admin", domain.RoleAdmin)
	targetID := seedPlainUser(t, e, "delguard-funded")

	if _, err := e.deps.Billing.Grant(t.Context(), targetID, 5000, 1, "护栏用例充值", ""); err != nil {
		t.Fatalf("发放积分失败: %v", err)
	}

	resp, env := e.do(t, adminC, "DELETE", "/api/admin/users/2", nil)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("有流水的用户删除应 409，实际 %d %v", resp.StatusCode, env)
	}
	msg, _ := env["message"].(string)
	if !strings.Contains(msg, "禁用") {
		t.Errorf("拒绝原因应提示改用禁用账号，实际: %q", msg)
	}

	if _, err := e.deps.Users.GetByID(t.Context(), targetID); err != nil {
		t.Errorf("被拒绝的删除不应影响账号，实际查不到: %v", err)
	}
	count, err := e.deps.Ledger.CountByUser(t.Context(), targetID)
	if err != nil || count != 1 {
		t.Errorf("流水应完整保留 1 条，实际 count=%d err=%v", count, err)
	}
}

// TestDeleteUserWithUsageLogRejected 无流水但有调用记录的账号同样不可删除。
func TestDeleteUserWithUsageLogRejected(t *testing.T) {
	e := newTestEnv(t)
	adminC := e.seedAndLogin(t, "delguard-admin2", domain.RoleAdmin)
	targetID := seedPlainUser(t, e, "delguard-used")

	log := &store.UsageLog{
		RequestID: "delguard-req-1", UserID: targetID, ModelName: "m",
		Status: domain.UsageFailed,
	}
	if err := e.deps.UsageLogs.Create(t.Context(), log); err != nil {
		t.Fatalf("种入用量日志失败: %v", err)
	}

	resp, env := e.do(t, adminC, "DELETE", "/api/admin/users/2", nil)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("有调用记录的用户删除应 409，实际 %d %v", resp.StatusCode, env)
	}
	if _, err := e.deps.Users.GetByID(t.Context(), targetID); err != nil {
		t.Errorf("被拒绝的删除不应影响账号，实际查不到: %v", err)
	}
}

// TestDeleteCleanUserSucceeds 未产生任何记录的误建账号仍可删除。
func TestDeleteCleanUserSucceeds(t *testing.T) {
	e := newTestEnv(t)
	adminC := e.seedAndLogin(t, "delguard-admin3", domain.RoleAdmin)
	targetID := seedPlainUser(t, e, "delguard-typo")

	resp, env := e.do(t, adminC, "DELETE", "/api/admin/users/2", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("无记录的账号应可删除，实际 %d %v", resp.StatusCode, env)
	}
	if _, err := e.deps.Users.GetByID(t.Context(), targetID); err == nil {
		t.Error("账号删除后仍能查到")
	}
}

// TestLedgerForeignKeyRestrictsUserDelete 绕过应用层直接删库时，
// credit_ledger 的 RESTRICT 外键仍然阻止流水被级联销毁。
func TestLedgerForeignKeyRestrictsUserDelete(t *testing.T) {
	e := newTestEnv(t)
	targetID := seedPlainUser(t, e, "delguard-fk")
	if _, err := e.deps.Billing.Grant(t.Context(), targetID, 1000, 1, "外键用例充值", ""); err != nil {
		t.Fatalf("发放积分失败: %v", err)
	}

	err := e.db.Exec("DELETE FROM users WHERE id = ?", targetID).Error
	if err == nil {
		t.Fatal("有流水的用户被直接删库删掉了，RESTRICT 外键未生效")
	}
	count, cErr := e.deps.Ledger.CountByUser(t.Context(), targetID)
	if cErr != nil || count != 1 {
		t.Errorf("流水应完整保留 1 条，实际 count=%d err=%v", count, cErr)
	}
}

// TestDeleteUserRevokesSessions 删除账号后其登录会话立即失效。
func TestDeleteUserRevokesSessions(t *testing.T) {
	e := newTestEnv(t)
	adminC := e.seedAndLogin(t, "delguard-admin4", domain.RoleAdmin)
	victimC := e.seedAndLogin(t, "delguard-victim", domain.RoleUser)

	resp, _ := e.do(t, victimC, "GET", "/api/auth/me", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("删除前目标用户会话应有效，实际 %d", resp.StatusCode)
	}

	resp, env := e.do(t, adminC, "DELETE", "/api/admin/users/2", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("删除无记录账号应成功，实际 %d %v", resp.StatusCode, env)
	}

	resp, _ = e.do(t, victimC, "GET", "/api/auth/me", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("账号删除后其会话应 401，实际 %d", resp.StatusCode)
	}
}
