package api

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// latestAudit 取指定动作的最近一条审计记录。
func latestAudit(t *testing.T, e *testEnv, action domain.AuditAction) store.AuditLog {
	t.Helper()
	var log store.AuditLog
	if err := e.db.Where("action = ?", action).Order("id DESC").First(&log).Error; err != nil {
		t.Fatalf("未找到动作 %s 的审计记录: %v", action, err)
	}
	return log
}

// 管理侧写操作留下可追溯的审计记录：操作人、对象、变更前后。
func TestAdminWritesLeaveAuditTrail(t *testing.T) {
	e := newTestEnv(t)
	rootC := e.seedAndLogin(t, "auditroot", domain.RoleRoot)

	resp, env := e.do(t, rootC, "POST", "/api/admin/users/", map[string]any{
		"username": "audittarget", "password": "password123",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("创建用户应 201，实际 %d：%v", resp.StatusCode, env)
	}
	userID := int64(env["data"].(map[string]any)["id"].(float64))

	created := latestAudit(t, e, domain.AuditUserCreate)
	if created.OperatorName != "auditroot" {
		t.Errorf("审计应记录操作人用户名，实际 %q", created.OperatorName)
	}
	if created.OperatorRole != domain.RoleRoot {
		t.Errorf("审计应记录操作时的角色，实际 %q", created.OperatorRole)
	}
	if created.TargetID != userID || created.TargetName != "audittarget" {
		t.Errorf("审计应记录对象 ID 与名称快照：%d %q", created.TargetID, created.TargetName)
	}
	if created.RequestID == "" {
		t.Error("审计应记录请求标识，供与访问日志关联")
	}
	if created.Result != domain.AuditSuccess {
		t.Errorf("成功操作的结果应为 success，实际 %q", created.Result)
	}

	// 禁用账号：变更前后都要留痕。
	resp, env = e.do(t, rootC, "POST", fmt.Sprintf("/api/admin/users/%d/status", userID),
		map[string]any{"status": string(domain.UserDisabled)})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("禁用账号应 200，实际 %d：%v", resp.StatusCode, env)
	}
	statusLog := latestAudit(t, e, domain.AuditUserStatusChange)
	if !strings.Contains(string(statusLog.BeforeState), string(domain.UserEnabled)) {
		t.Errorf("应记录变更前状态：%s", statusLog.BeforeState)
	}
	if !strings.Contains(string(statusLog.AfterState), string(domain.UserDisabled)) {
		t.Errorf("应记录变更后状态：%s", statusLog.AfterState)
	}
}

// 被拒绝的操作同样留痕：审计要能回答「谁尝试过什么」。
func TestRejectedOperationRecordedAsFailure(t *testing.T) {
	e := newTestEnv(t)
	adminC := e.seedAndLogin(t, "failadmin", domain.RoleAdmin)
	deptID := createDepartment(t, e, adminC, map[string]any{"name": "有成员的部门"})
	resp, env := e.do(t, adminC, "POST", "/api/admin/users/", map[string]any{
		"username": "blockmember", "password": "password123", "department_id": deptID,
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("创建成员应 201，实际 %d：%v", resp.StatusCode, env)
	}

	resp, env = e.do(t, adminC, "DELETE", fmt.Sprintf("/api/admin/departments/%d", deptID), nil)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("删除应被拒绝，实际 %d：%v", resp.StatusCode, env)
	}
	log := latestAudit(t, e, domain.AuditDepartmentDelete)
	if log.Result != domain.AuditFailure {
		t.Errorf("被拒绝的操作结果应为 failure，实际 %q", log.Result)
	}
	if log.Message == "" {
		t.Error("被拒绝的操作应记录原因")
	}
}

// 上游渠道密钥不得以任何形式进入审计记录。
func TestChannelSecretNeverEntersAudit(t *testing.T) {
	e := newTestEnv(t)
	adminC := e.seedAndLogin(t, "chauditadmin", domain.RoleAdmin)
	const upstreamSecret = "sk-super-secret-upstream-key"

	resp, env := e.do(t, adminC, "POST", "/api/admin/channels/", map[string]any{
		"name": "审计渠道", "provider": "openai", "protocol": "openai_compat",
		"base_url": "http://127.0.0.1:1", "api_key": upstreamSecret,
		"models": []string{"glm-5"},
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("创建渠道应 201，实际 %d：%v", resp.StatusCode, env)
	}

	log := latestAudit(t, e, domain.AuditChannelCreate)
	snapshot := string(log.BeforeState) + string(log.AfterState)
	if strings.Contains(snapshot, upstreamSecret) {
		t.Fatalf("审计快照泄漏上游密钥明文：%s", snapshot)
	}
	if !strings.Contains(string(log.AfterState), domain.AuditRedacted) {
		t.Errorf("审计应以占位符表达密钥已设置：%s", log.AfterState)
	}
}

// 登录成功与失败都留痕，且失败记录不泄漏密码。
func TestAuthEventsAudited(t *testing.T) {
	e := newTestEnv(t)
	e.seedAndLogin(t, "loginuser", domain.RoleUser)

	anon := e.client(t)
	resp, _ := e.do(t, anon, "POST", "/api/auth/login",
		map[string]any{"username": "loginuser", "password": "wrong-password"})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("错误密码应 401，实际 %d", resp.StatusCode)
	}

	var logs []store.AuditLog
	if err := e.db.Where("action = ?", domain.AuditAuthLogin).Order("id").Find(&logs).Error; err != nil {
		t.Fatalf("查询登录审计失败: %v", err)
	}
	if len(logs) < 2 {
		t.Fatalf("应同时记录登录成功与失败，实际 %d 条", len(logs))
	}
	var sawSuccess, sawFailure bool
	for _, l := range logs {
		switch l.Result {
		case domain.AuditSuccess:
			sawSuccess = true
		case domain.AuditFailure:
			sawFailure = true
			if l.TargetName != "loginuser" {
				t.Errorf("失败记录应保留尝试的用户名，实际 %q", l.TargetName)
			}
			if strings.Contains(string(l.AfterState), "wrong-password") {
				t.Errorf("审计不得记录密码：%s", l.AfterState)
			}
		}
	}
	if !sawSuccess || !sawFailure {
		t.Errorf("成功与失败记录应各至少一条：成功=%v 失败=%v", sawSuccess, sawFailure)
	}
}

// 审计查询端点支持按动作与对象筛选，且只提供读取。
func TestAuditQueryEndpoint(t *testing.T) {
	e := newTestEnv(t)
	adminC := e.seedAndLogin(t, "auditqadmin", domain.RoleAdmin)
	resp, env := e.do(t, adminC, "POST", "/api/admin/users/", map[string]any{
		"username": "auditqtarget", "password": "password123",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("创建用户应 201，实际 %d：%v", resp.StatusCode, env)
	}
	userID := int64(env["data"].(map[string]any)["id"].(float64))

	resp, env = e.do(t, adminC, "GET",
		fmt.Sprintf("/api/admin/audit-logs/?action=%s&target_id=%d",
			domain.AuditUserCreate, userID), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("查询审计应 200，实际 %d：%v", resp.StatusCode, env)
	}
	page := env["data"].(map[string]any)
	items := page["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("按动作与对象筛选应命中 1 条，实际 %d 条：%v", len(items), items)
	}
	row := items[0].(map[string]any)
	if row["target_name"] != "auditqtarget" {
		t.Errorf("返回记录的对象名称不符：%v", row["target_name"])
	}

	// 动作枚举下发给前端筛选，避免前端硬编码一份会漂移的清单。
	resp, env = e.do(t, adminC, "GET", "/api/admin/audit-logs/actions", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("查询动作枚举应 200，实际 %d：%v", resp.StatusCode, env)
	}
	actions := env["data"].([]any)
	if len(actions) != len(domain.AuditActions) {
		t.Errorf("下发的动作数应与领域枚举一致：%d vs %d", len(actions), len(domain.AuditActions))
	}
}
