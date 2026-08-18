package api

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// 负责人必须是本部门成员：负责人能看到全部门的消费明细与成员余额，
// 允许指定部门外的人等于绕开部门归属把成本数据开放出去。
func TestDepartmentOwnerMustBeMember(t *testing.T) {
	e := newTestEnv(t)
	adminC := e.seedAndLogin(t, "ownerchkadmin", domain.RoleAdmin)
	e.seedAndLogin(t, "outsider", domain.RoleUser)
	outsiderID := userIDOf(t, e, "outsider")

	// 新建部门时还没有成员，任何负责人取值都应被拒绝。
	resp, env := e.do(t, adminC, "POST", "/api/admin/departments/", map[string]any{
		"name": "指定外人部", "owner_user_id": outsiderID,
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("新建部门时指定负责人应 400，实际 %d：%v", resp.StatusCode, env)
	}

	deptID := createDepartment(t, e, adminC, map[string]any{"name": "负责人校验部"})

	// 部门已存在，但该用户不是成员，仍应被拒绝。
	resp, env = e.do(t, adminC, "PUT", fmt.Sprintf("/api/admin/departments/%d", deptID), map[string]any{
		"name": "负责人校验部", "owner_user_id": outsiderID, "status": domain.DepartmentEnabled,
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("指定非本部门成员为负责人应 400，实际 %d：%v", resp.StatusCode, env)
	}
	if msg, _ := env["message"].(string); msg == "" {
		t.Error("拒绝时应给出可读原因")
	}

	// 划入本部门后即可指定。
	resp, env = e.do(t, adminC, "POST", fmt.Sprintf("/api/admin/departments/%d/members", deptID),
		map[string]any{"user_ids": []int64{outsiderID}})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("划入成员失败：%d %v", resp.StatusCode, env)
	}
	resp, env = e.do(t, adminC, "PUT", fmt.Sprintf("/api/admin/departments/%d", deptID), map[string]any{
		"name": "负责人校验部", "owner_user_id": outsiderID, "status": domain.DepartmentEnabled,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("指定本部门成员为负责人应 200，实际 %d：%v", resp.StatusCode, env)
	}
}

// 编辑部门时不再清空已设的负责人：管理员改预算、改备注都会走这个接口，
// 每次提交都把负责人置空会让该负责人静默失去查账入口。
func TestDepartmentUpdateKeepsOwnerWhenSubmitted(t *testing.T) {
	e := newTestEnv(t)
	adminC := e.seedAndLogin(t, "ownerkeepadmin", domain.RoleAdmin)
	e.seedAndLogin(t, "ownerkeep", domain.RoleUser)
	ownerID := userIDOf(t, e, "ownerkeep")

	deptID := createDepartment(t, e, adminC, map[string]any{"name": "保留负责人部"})
	if resp, env := e.do(t, adminC, "POST", fmt.Sprintf("/api/admin/departments/%d/members", deptID),
		map[string]any{"user_ids": []int64{ownerID}}); resp.StatusCode != http.StatusOK {
		t.Fatalf("划入成员失败：%d %v", resp.StatusCode, env)
	}
	if resp, env := e.do(t, adminC, "PUT", fmt.Sprintf("/api/admin/departments/%d", deptID), map[string]any{
		"name": "保留负责人部", "owner_user_id": ownerID, "status": domain.DepartmentEnabled,
	}); resp.StatusCode != http.StatusOK {
		t.Fatalf("指定负责人失败：%d %v", resp.StatusCode, env)
	}

	// 只改预算，负责人一同提交，应保持不变。
	if resp, env := e.do(t, adminC, "PUT", fmt.Sprintf("/api/admin/departments/%d", deptID), map[string]any{
		"name": "保留负责人部", "owner_user_id": ownerID,
		"monthly_budget_credits": 500_000, "status": domain.DepartmentEnabled,
	}); resp.StatusCode != http.StatusOK {
		t.Fatalf("更新部门失败：%d %v", resp.StatusCode, env)
	}
	var dept store.Department
	if err := e.db.First(&dept, deptID).Error; err != nil {
		t.Fatalf("查部门失败：%v", err)
	}
	if dept.OwnerUserID == nil || *dept.OwnerUserID != ownerID {
		t.Fatalf("负责人应保持为 %d，实际 %v", ownerID, dept.OwnerUserID)
	}
}

// 负责人被转出部门后立即失去查账入口：仅凭 owner_user_id 判定会让已转岗的人
// 继续看到原部门的消费明细与成员余额。
func TestDepartmentOwnerLosesAccessAfterTransferOut(t *testing.T) {
	e := newTestEnv(t)
	adminC := e.seedAndLogin(t, "transferadmin", domain.RoleAdmin)
	ownerC := e.seedAndLogin(t, "transferowner", domain.RoleUser)
	ownerID := userIDOf(t, e, "transferowner")

	deptID := createDepartment(t, e, adminC, map[string]any{"name": "转出验证部"})
	setOwner(t, e, deptID, ownerID)

	path := fmt.Sprintf("/api/dept/budget?department_id=%d", deptID)
	if resp, env := e.do(t, ownerC, "GET", path, nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("负责人查账应 200，实际 %d：%v", resp.StatusCode, env)
	}

	// 转出部门（管理员把该成员转为未分配）。
	if resp, env := e.do(t, adminC, "POST", fmt.Sprintf("/api/admin/departments/%d/members", deptID),
		map[string]any{"user_ids": []int64{ownerID}, "remove": true}); resp.StatusCode != http.StatusOK {
		t.Fatalf("转出成员失败：%d %v", resp.StatusCode, env)
	}

	if resp, env := e.do(t, ownerC, "GET", path, nil); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("转出后查账应 403，实际 %d：%v", resp.StatusCode, env)
	}
	// 负责人列表同步收敛，前端据此不再显示部门费用入口。
	_, env := e.do(t, ownerC, "GET", "/api/auth/me", nil)
	managed, _ := env["data"].(map[string]any)["managed_department_ids"].([]any)
	if len(managed) != 0 {
		t.Fatalf("转出后不应再有负责部门，实际 %v", managed)
	}
}

// 部门列表的 owner_username 反映生效口径：负责人已不是成员时显示为空。
func TestDepartmentListOwnerUsernameReflectsMembership(t *testing.T) {
	e := newTestEnv(t)
	adminC := e.seedAndLogin(t, "ownernameadmin", domain.RoleAdmin)
	e.seedAndLogin(t, "ownername", domain.RoleUser)
	ownerID := userIDOf(t, e, "ownername")
	deptID := createDepartment(t, e, adminC, map[string]any{"name": "负责人名称部"})
	setOwner(t, e, deptID, ownerID)

	if got := deptOwnerUsername(t, e, adminC, deptID); got != "ownername" {
		t.Fatalf("应显示负责人用户名，实际 %q", got)
	}

	if err := e.db.Model(&store.User{}).Where("id = ?", ownerID).
		Update("department_id", nil).Error; err != nil {
		t.Fatalf("转出成员失败：%v", err)
	}
	if got := deptOwnerUsername(t, e, adminC, deptID); got != "" {
		t.Fatalf("负责人已不是成员时应显示为空，实际 %q", got)
	}
}

// deptOwnerUsername 从部门列表里取指定部门的 owner_username。
func deptOwnerUsername(t *testing.T, e *testEnv, c *http.Client, deptID int64) string {
	t.Helper()
	resp, env := e.do(t, c, "GET", "/api/admin/departments/", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("查部门列表失败：%d %v", resp.StatusCode, env)
	}
	items, _ := env["data"].(map[string]any)["items"].([]any)
	for _, it := range items {
		row := it.(map[string]any)
		if int64(row["id"].(float64)) == deptID {
			name, _ := row["owner_username"].(string)
			return name
		}
	}
	t.Fatalf("部门列表中未找到部门 %d", deptID)
	return ""
}
