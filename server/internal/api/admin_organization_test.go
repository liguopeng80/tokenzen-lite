package api

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// createDepartment 建一个部门并返回其 ID。
func createDepartment(t *testing.T, e *testEnv, c *http.Client, body map[string]any) int64 {
	t.Helper()
	resp, env := e.do(t, c, "POST", "/api/admin/departments/", body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("创建部门应 201，实际 %d：%v", resp.StatusCode, env)
	}
	data, _ := env["data"].(map[string]any)
	id, _ := data["id"].(float64)
	if id == 0 {
		t.Fatalf("创建部门未返回 ID：%v", env)
	}
	return int64(id)
}

// 部门仍有成员时不允许删除：成员归属是外键，且已产生的用量日志仍按该部门归集，
// 直接删除会让报表出现无名分组。
func TestDepartmentDeleteBlockedWhileHasMembers(t *testing.T) {
	e := newTestEnv(t)
	adminC := e.seedAndLogin(t, "deptadmin", domain.RoleAdmin)
	deptID := createDepartment(t, e, adminC, map[string]any{"name": "研发部", "code": "RD"})

	resp, env := e.do(t, adminC, "POST", "/api/admin/users/", map[string]any{
		"username": "deptmember", "password": "password123", "department_id": deptID,
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("创建部门成员应 201，实际 %d：%v", resp.StatusCode, env)
	}

	resp, env = e.do(t, adminC, "DELETE", fmt.Sprintf("/api/admin/departments/%d", deptID), nil)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("有成员的部门删除应 409，实际 %d：%v", resp.StatusCode, env)
	}

	// 转出成员后即可删除。
	resp, env = e.do(t, adminC, "POST", fmt.Sprintf("/api/admin/departments/%d/members", deptID),
		map[string]any{"user_ids": []int64{2}, "remove": true})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("转出成员应 200，实际 %d：%v", resp.StatusCode, env)
	}
	resp, env = e.do(t, adminC, "DELETE", fmt.Sprintf("/api/admin/departments/%d", deptID), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("无成员的部门删除应 200，实际 %d：%v", resp.StatusCode, env)
	}
}

// 已停用的部门不能再接收新成员，但既有成员的归属与调用能力不变。
func TestDisabledDepartmentRejectsNewMembers(t *testing.T) {
	e := newTestEnv(t)
	adminC := e.seedAndLogin(t, "deptadmin2", domain.RoleAdmin)
	deptID := createDepartment(t, e, adminC, map[string]any{"name": "已撤销部门"})

	resp, env := e.do(t, adminC, "PUT", fmt.Sprintf("/api/admin/departments/%d", deptID),
		map[string]any{"name": "已撤销部门", "status": string(domain.DepartmentDisabled)})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("停用部门应 200，实际 %d：%v", resp.StatusCode, env)
	}

	resp, env = e.do(t, adminC, "POST", "/api/admin/users/", map[string]any{
		"username": "latecomer", "password": "password123", "department_id": deptID,
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("向停用部门分配新成员应 400，实际 %d：%v", resp.StatusCode, env)
	}
}

// 积分流水与用量日志上的部门是记账时点快照：用户转部门后，历史记录的归属不变。
func TestDepartmentSnapshotSurvivesTransfer(t *testing.T) {
	e := newTestEnv(t)
	adminC := e.seedAndLogin(t, "snapadmin", domain.RoleAdmin)
	oldDept := createDepartment(t, e, adminC, map[string]any{"name": "原部门"})
	newDept := createDepartment(t, e, adminC, map[string]any{"name": "新部门"})

	resp, env := e.do(t, adminC, "POST", "/api/admin/users/", map[string]any{
		"username": "transferee", "password": "password123", "department_id": oldDept,
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("创建用户应 201，实际 %d：%v", resp.StatusCode, env)
	}
	data, _ := env["data"].(map[string]any)
	userID := int64(data["id"].(float64))

	resp, env = e.do(t, adminC, "POST", fmt.Sprintf("/api/admin/users/%d/credits", userID),
		map[string]any{"amount": 5000, "note": "原部门期间发放"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("发放积分应 200，实际 %d：%v", resp.StatusCode, env)
	}

	// 转到新部门后再发一笔。
	resp, env = e.do(t, adminC, "PUT", fmt.Sprintf("/api/admin/users/%d", userID),
		map[string]any{"department_id": newDept})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("转部门应 200，实际 %d：%v", resp.StatusCode, env)
	}
	resp, env = e.do(t, adminC, "POST", fmt.Sprintf("/api/admin/users/%d/credits", userID),
		map[string]any{"amount": 3000, "note": "新部门期间发放"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("发放积分应 200，实际 %d：%v", resp.StatusCode, env)
	}

	var entries []store.LedgerEntry
	if err := e.db.Where("user_id = ?", userID).Order("id").Find(&entries).Error; err != nil {
		t.Fatalf("查询流水失败: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("应有两条流水，实际 %d 条", len(entries))
	}
	if entries[0].DepartmentID != oldDept {
		t.Errorf("转部门前的流水应保留原部门快照 %d，实际 %d", oldDept, entries[0].DepartmentID)
	}
	if entries[1].DepartmentID != newDept {
		t.Errorf("转部门后的流水应记新部门 %d，实际 %d", newDept, entries[1].DepartmentID)
	}
	if entries[0].OperatorID == 0 {
		t.Error("管理员发放的流水应记录操作人")
	}
}

// 携带幂等键的重复发放只记一次账，返回首次结果。
func TestGrantCreditsIdempotency(t *testing.T) {
	e := newTestEnv(t)
	adminC := e.seedAndLogin(t, "idemadmin", domain.RoleAdmin)
	resp, env := e.do(t, adminC, "POST", "/api/admin/users/", map[string]any{
		"username": "idemtarget", "password": "password123",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("创建用户应 201，实际 %d：%v", resp.StatusCode, env)
	}
	userID := int64(env["data"].(map[string]any)["id"].(float64))

	body := map[string]any{"amount": 10000, "note": "季度额度", "idempotency_key": "q3-2026-alloc"}
	for i := 0; i < 3; i++ {
		resp, env = e.do(t, adminC, "POST", fmt.Sprintf("/api/admin/users/%d/credits", userID), body)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("第 %d 次发放应 200，实际 %d：%v", i+1, resp.StatusCode, env)
		}
	}

	var u store.User
	if err := e.db.First(&u, userID).Error; err != nil {
		t.Fatalf("查询用户失败: %v", err)
	}
	if u.CreditBalance != 10000 {
		t.Errorf("重复提交只应记一次账，余额期望 10000，实际 %d", u.CreditBalance)
	}
	var count int64
	if err := e.db.Model(&store.LedgerEntry{}).
		Where("user_id = ? AND entry_type = ?", userID, domain.LedgerGrant).
		Count(&count).Error; err != nil {
		t.Fatalf("统计流水失败: %v", err)
	}
	if count != 1 {
		t.Errorf("应只有一条发放流水，实际 %d 条", count)
	}
}

// 不带幂等键时保持既有行为：各自记账。
func TestGrantCreditsWithoutKeyStillAccumulates(t *testing.T) {
	e := newTestEnv(t)
	adminC := e.seedAndLogin(t, "noidemadmin", domain.RoleAdmin)
	resp, env := e.do(t, adminC, "POST", "/api/admin/users/", map[string]any{
		"username": "noidemtarget", "password": "password123",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("创建用户应 201，实际 %d：%v", resp.StatusCode, env)
	}
	userID := int64(env["data"].(map[string]any)["id"].(float64))

	for i := 0; i < 2; i++ {
		resp, env = e.do(t, adminC, "POST", fmt.Sprintf("/api/admin/users/%d/credits", userID),
			map[string]any{"amount": 1000, "note": "补发"})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("发放应 200，实际 %d：%v", resp.StatusCode, env)
		}
	}
	var u store.User
	if err := e.db.First(&u, userID).Error; err != nil {
		t.Fatalf("查询用户失败: %v", err)
	}
	if u.CreditBalance != 2000 {
		t.Errorf("无幂等键时应各自记账，余额期望 2000，实际 %d", u.CreditBalance)
	}
}

// 批量发放对每个用户各记一条流水，且幂等键在用户之间不互相命中。
func TestBatchGrantPerUserLedgerAndIdempotency(t *testing.T) {
	e := newTestEnv(t)
	adminC := e.seedAndLogin(t, "batchadmin", domain.RoleAdmin)
	var userIDs []int64
	for _, name := range []string{"batchu1", "batchu2", "batchu3"} {
		resp, env := e.do(t, adminC, "POST", "/api/admin/users/", map[string]any{
			"username": name, "password": "password123",
		})
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("创建 %s 应 201，实际 %d：%v", name, resp.StatusCode, env)
		}
		userIDs = append(userIDs, int64(env["data"].(map[string]any)["id"].(float64)))
	}

	body := map[string]any{
		"user_ids": userIDs, "amount": 2000, "note": "季度统一发放",
		"idempotency_key": "batch-q3",
	}
	resp, env := e.do(t, adminC, "POST", "/api/admin/credits/batch-grant", body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("批量发放应 200，实际 %d：%v", resp.StatusCode, env)
	}
	summary := env["data"].(map[string]any)
	if summary["succeeded"].(float64) != 3 {
		t.Fatalf("三个用户应全部成功：%v", summary)
	}

	// 重放：全部命中幂等，余额不变。
	resp, env = e.do(t, adminC, "POST", "/api/admin/credits/batch-grant", body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("重放应 200，实际 %d：%v", resp.StatusCode, env)
	}
	summary = env["data"].(map[string]any)
	if summary["replayed"].(float64) != 3 {
		t.Errorf("重放应全部判定为已记账：%v", summary)
	}

	for _, id := range userIDs {
		var u store.User
		if err := e.db.First(&u, id).Error; err != nil {
			t.Fatalf("查询用户 %d 失败: %v", id, err)
		}
		if u.CreditBalance != 2000 {
			t.Errorf("用户 %d 余额期望 2000，实际 %d", id, u.CreditBalance)
		}
	}
}

// 批量导入逐条独立处理：已存在的用户名跳过而不覆盖，非法用户名不影响同批其余记录。
func TestImportUsersPerItemIsolation(t *testing.T) {
	e := newTestEnv(t)
	adminC := e.seedAndLogin(t, "importadmin", domain.RoleAdmin)
	resp, env := e.do(t, adminC, "POST", "/api/admin/users/", map[string]any{
		"username": "existinguser", "password": "password123", "display_name": "原有账号",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("预置用户应 201，实际 %d：%v", resp.StatusCode, env)
	}

	resp, env = e.do(t, adminC, "POST", "/api/admin/users/import", map[string]any{
		"items": []map[string]any{
			{"username": "importok", "password": "password123", "initial_credits": 5000},
			{"username": "existinguser", "password": "password123", "display_name": "覆盖尝试"},
			{"username": "x", "password": "password123"},
			{"username": "importok2", "password": "password123"},
		},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("批量导入应 200，实际 %d：%v", resp.StatusCode, env)
	}
	summary := env["data"].(map[string]any)
	if summary["created"].(float64) != 2 {
		t.Errorf("应新建 2 个账号：%v", summary)
	}
	if summary["skipped"].(float64) != 1 {
		t.Errorf("已存在的账号应跳过：%v", summary)
	}
	if summary["failed"].(float64) != 1 {
		t.Errorf("非法用户名应失败：%v", summary)
	}

	// 既有账号未被覆盖。
	var existing store.User
	if err := e.db.Where("username = ?", "existinguser").First(&existing).Error; err != nil {
		t.Fatalf("查询既有账号失败: %v", err)
	}
	if existing.DisplayName != "原有账号" {
		t.Errorf("批量导入不应改动既有账号，实际显示名 %q", existing.DisplayName)
	}
	// 初始积分随建号发放。
	var imported store.User
	if err := e.db.Where("username = ?", "importok").First(&imported).Error; err != nil {
		t.Fatalf("查询导入账号失败: %v", err)
	}
	if imported.CreditBalance != 5000 {
		t.Errorf("初始额度应随建号发放，实际余额 %d", imported.CreditBalance)
	}
}
