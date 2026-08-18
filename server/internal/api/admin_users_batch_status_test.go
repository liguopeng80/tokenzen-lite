package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// createManagedUser 建一个普通用户并返回其 ID。
func createManagedUser(t *testing.T, e *testEnv, c *http.Client, username string,
	extra map[string]any) int64 {

	t.Helper()
	body := map[string]any{"username": username, "password": "password123"}
	for k, v := range extra {
		body[k] = v
	}
	resp, env := e.do(t, c, "POST", "/api/admin/users/", body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("创建用户 %s 应 201，实际 %d：%v", username, resp.StatusCode, env)
	}
	data, _ := env["data"].(map[string]any)
	id, _ := data["id"].(float64)
	if id == 0 {
		t.Fatalf("创建用户 %s 未返回 ID：%v", username, env)
	}
	return int64(id)
}

func userStatus(t *testing.T, e *testEnv, id int64) domain.UserStatus {
	t.Helper()
	var u store.User
	if err := e.db.First(&u, id).Error; err != nil {
		t.Fatalf("查询用户 %d 失败: %v", id, err)
	}
	return u.Status
}

// 批量禁用：目标用户全部被禁用，不存在的用户只让该条失败，其余照常处理。
func TestBatchSetUserStatusPerItemIsolation(t *testing.T) {
	e := newTestEnv(t)
	adminC := e.seedAndLogin(t, "batchstatusadmin", domain.RoleAdmin)
	leaverA := createManagedUser(t, e, adminC, "leavera", nil)
	leaverB := createManagedUser(t, e, adminC, "leaverb", nil)
	stayer := createManagedUser(t, e, adminC, "stayer", nil)

	resp, env := e.do(t, adminC, "POST", "/api/admin/users/batch-status", map[string]any{
		"user_ids": []int64{leaverA, leaverB, 99999}, "status": "disabled",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("批量禁用应 200，实际 %d：%v", resp.StatusCode, env)
	}
	summary := env["data"].(map[string]any)
	if summary["succeeded"].(float64) != 2 {
		t.Errorf("应禁用 2 个账号：%v", summary)
	}
	if summary["failed"].(float64) != 1 {
		t.Errorf("不存在的用户应单条失败：%v", summary)
	}
	for _, id := range []int64{leaverA, leaverB} {
		if got := userStatus(t, e, id); got != domain.UserDisabled {
			t.Errorf("用户 %d 应被禁用，实际 %s", id, got)
		}
	}
	if got := userStatus(t, e, stayer); got != domain.UserEnabled {
		t.Errorf("未列入批次的用户不应受影响，实际 %s", got)
	}

	// 重复提交：已是目标状态的记为未改动，不计入成功数。
	resp, env = e.do(t, adminC, "POST", "/api/admin/users/batch-status", map[string]any{
		"user_ids": []int64{leaverA, leaverB}, "status": "disabled",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("重复提交应 200，实际 %d：%v", resp.StatusCode, env)
	}
	summary = env["data"].(map[string]any)
	if summary["unchanged"].(float64) != 2 || summary["succeeded"].(float64) != 0 {
		t.Errorf("重复提交应全部记为未改动：%v", summary)
	}

	// 批量启用可把账号恢复回来。
	resp, env = e.do(t, adminC, "POST", "/api/admin/users/batch-status", map[string]any{
		"user_ids": []int64{leaverA}, "status": "enabled",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("批量启用应 200，实际 %d：%v", resp.StatusCode, env)
	}
	if got := userStatus(t, e, leaverA); got != domain.UserEnabled {
		t.Errorf("用户应被重新启用，实际 %s", got)
	}
}

// 按部门批量禁用：目标集合展开为部门全部成员，部门外的用户不受影响。
func TestBatchSetUserStatusByDepartment(t *testing.T) {
	e := newTestEnv(t)
	adminC := e.seedAndLogin(t, "deptstatusadmin", domain.RoleAdmin)
	deptID := createDepartment(t, e, adminC, map[string]any{"name": "外包组", "code": "OUT"})
	memberA := createManagedUser(t, e, adminC, "outsourcea", map[string]any{"department_id": deptID})
	memberB := createManagedUser(t, e, adminC, "outsourceb", map[string]any{"department_id": deptID})
	outsider := createManagedUser(t, e, adminC, "insider", nil)

	resp, env := e.do(t, adminC, "POST", "/api/admin/users/batch-status", map[string]any{
		"department_id": deptID, "status": "disabled",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("按部门批量禁用应 200，实际 %d：%v", resp.StatusCode, env)
	}
	summary := env["data"].(map[string]any)
	if summary["succeeded"].(float64) != 2 {
		t.Errorf("部门两名成员应全部被禁用：%v", summary)
	}
	for _, id := range []int64{memberA, memberB} {
		if got := userStatus(t, e, id); got != domain.UserDisabled {
			t.Errorf("部门成员 %d 应被禁用，实际 %s", id, got)
		}
	}
	if got := userStatus(t, e, outsider); got != domain.UserEnabled {
		t.Errorf("部门外用户不应受影响，实际 %s", got)
	}
}

// 越权与自禁：管理员不能通过批量接口禁用同级管理员，也不能禁用自己；
// 这两条只让对应记录失败，同批次的普通用户仍被正常处置。
func TestBatchSetUserStatusRejectsPeerAdminAndSelf(t *testing.T) {
	e := newTestEnv(t)
	adminC := e.seedAndLogin(t, "actoradmin", domain.RoleAdmin)
	var actor store.User
	if err := e.db.Where("username = ?", "actoradmin").First(&actor).Error; err != nil {
		t.Fatalf("查询操作者失败: %v", err)
	}
	peer := &store.User{
		Username: "peeradmin", PasswordHash: "x",
		Role: domain.RoleAdmin, Status: domain.UserEnabled,
	}
	if err := e.db.Create(peer).Error; err != nil {
		t.Fatalf("种入同级管理员失败: %v", err)
	}
	normal := createManagedUser(t, e, adminC, "normaluser", nil)

	resp, env := e.do(t, adminC, "POST", "/api/admin/users/batch-status", map[string]any{
		"user_ids": []int64{peer.ID, actor.ID, normal}, "status": "disabled",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("批量禁用应 200，实际 %d：%v", resp.StatusCode, env)
	}
	summary := env["data"].(map[string]any)
	if summary["succeeded"].(float64) != 1 {
		t.Errorf("只有普通用户应被禁用：%v", summary)
	}
	if summary["failed"].(float64) != 2 {
		t.Errorf("同级管理员与自己应各失败一条：%v", summary)
	}
	if got := userStatus(t, e, peer.ID); got != domain.UserEnabled {
		t.Errorf("同级管理员不应被禁用，实际 %s", got)
	}
	if got := userStatus(t, e, actor.ID); got != domain.UserEnabled {
		t.Errorf("操作者本人不应被禁用，实际 %s", got)
	}
	if got := userStatus(t, e, normal); got != domain.UserDisabled {
		t.Errorf("普通用户应被禁用，实际 %s", got)
	}
}

// 被禁用的账号立即失去 /v1 调用能力：批量禁用的业务目的正是切断离职人员的调用。
func TestBatchDisabledUserLosesRelayAccess(t *testing.T) {
	e := newTestEnv(t)
	adminC := e.seedAndLogin(t, "relaystatusadmin", domain.RoleAdmin)
	userID, key := e.seedRelayUser(t, "relayleaver", 100_000, nil)

	resp, _ := e.relayPost(t, key, map[string]any{"model": "gpt-4o-mini"})
	if resp.StatusCode == http.StatusUnauthorized {
		t.Fatalf("禁用前不应因身份被拒，实际 %d", resp.StatusCode)
	}

	_, env := e.do(t, adminC, "POST", "/api/admin/users/batch-status", map[string]any{
		"user_ids": []int64{userID}, "status": "disabled",
	})
	summary, ok := env["data"].(map[string]any)
	if !ok || summary["succeeded"].(float64) != 1 {
		t.Fatalf("批量禁用未生效：%v", env)
	}

	resp, raw := e.relayPost(t, key, map[string]any{"model": "gpt-4o-mini"})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("禁用后调用应 403，实际 %d", resp.StatusCode)
	}
	if !strings.Contains(string(raw), "user_disabled") {
		t.Errorf("拒绝原因应为账号不可用，实际响应 %s", raw)
	}
}

// 状态取值非法时整批拒绝：部分执行会让管理员误以为操作已生效。
func TestBatchSetUserStatusRejectsInvalidStatus(t *testing.T) {
	e := newTestEnv(t)
	adminC := e.seedAndLogin(t, "invalidstatusadmin", domain.RoleAdmin)
	userID := createManagedUser(t, e, adminC, "untouched", nil)

	resp, env := e.do(t, adminC, "POST", "/api/admin/users/batch-status", map[string]any{
		"user_ids": []int64{userID}, "status": "suspended",
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("非法状态应 400，实际 %d：%v", resp.StatusCode, env)
	}
	if got := userStatus(t, e, userID); got != domain.UserEnabled {
		t.Errorf("整批拒绝时不应改动任何账号，实际 %s", got)
	}

	// 未指定用户列表也未指定部门时同样整批拒绝。
	resp, env = e.do(t, adminC, "POST", "/api/admin/users/batch-status",
		map[string]any{"status": "disabled"})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("未指定目标应 400，实际 %d：%v", resp.StatusCode, env)
	}
}
