package api

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// setOwner 把某用户指派为某部门的负责人，并把该用户划入这个部门。
// 两步缺一不可：负责人的查账权限同时要求「是负责人」与「仍是本部门成员」，
// 只写 owner_user_id 得到的是负责人已转出部门的状态，那时查账应当被拒。
func setOwner(t *testing.T, e *testEnv, deptID, userID int64) {
	t.Helper()
	if err := e.db.Model(&store.Department{}).Where("id = ?", deptID).
		Update("owner_user_id", userID).Error; err != nil {
		t.Fatalf("指派部门负责人失败: %v", err)
	}
	if err := e.db.Model(&store.User{}).Where("id = ?", userID).
		Update("department_id", deptID).Error; err != nil {
		t.Fatalf("把负责人划入部门失败: %v", err)
	}
}

// userIDOf 取用户名对应的用户 ID。
func userIDOf(t *testing.T, e *testEnv, username string) int64 {
	t.Helper()
	var u store.User
	if err := e.db.Where("username = ?", username).First(&u).Error; err != nil {
		t.Fatalf("查询用户 %s 失败: %v", username, err)
	}
	return u.ID
}

// 部门负责人的查账范围由部门归属决定，不由角色决定：普通角色的负责人可以查
// 本部门，非负责人的普通用户查同一部门被拒。
func TestDeptReportScopedByOwnership(t *testing.T) {
	e := newTestEnv(t)
	adminC := e.seedAndLogin(t, "deptrepadmin", domain.RoleAdmin)
	deptID := createDepartment(t, e, adminC, map[string]any{
		"name": "研发部", "code": "RD-REP", "monthly_budget_credits": 1_000_000,
	})

	ownerC := e.seedAndLogin(t, "deptowner", domain.RoleUser)
	setOwner(t, e, deptID, userIDOf(t, e, "deptowner"))
	outsiderC := e.seedAndLogin(t, "deptoutsider", domain.RoleUser)

	// 负责人可查本部门预算。
	resp, env := e.do(t, ownerC, "GET",
		fmt.Sprintf("/api/dept/budget?department_id=%d", deptID), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("负责人查本部门预算应 200，实际 %d：%v", resp.StatusCode, env)
	}
	data, _ := env["data"].(map[string]any)
	if budget, _ := data["monthly_budget_credits"].(float64); int64(budget) != 1_000_000 {
		t.Errorf("预算应为 1000000，实际 %v", data["monthly_budget_credits"])
	}
	// 本月无消费时仍须返回零值行，前端据此区分「未消费」与「查询失败」。
	if _, ok := data["credits_charged"]; !ok {
		t.Errorf("无消费时也应返回 credits_charged 字段：%v", data)
	}

	// 非负责人查同一部门被拒；管理员角色不构成本组端点的通行条件之外的例外，
	// 但管理员另有 /api/admin 下的全站报表，此处只验证归属校验本身。
	for _, ep := range []string{"budget", "cost-report", "members"} {
		resp, env = e.do(t, outsiderC, "GET",
			fmt.Sprintf("/api/dept/%s?department_id=%d", ep, deptID), nil)
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("非负责人访问 /api/dept/%s 应 403，实际 %d：%v", ep, resp.StatusCode, env)
		}
	}
}

// 负责人只能看到自己负责的部门：改 department_id 指向别的部门一律 403，
// 且响应不区分「部门不存在」与「无权查看」，避免借该端点探测部门 ID。
func TestDeptReportRejectsOtherDepartment(t *testing.T) {
	e := newTestEnv(t)
	adminC := e.seedAndLogin(t, "deptrepadmin2", domain.RoleAdmin)
	mine := createDepartment(t, e, adminC, map[string]any{"name": "我的部门", "code": "MINE"})
	theirs := createDepartment(t, e, adminC, map[string]any{"name": "别的部门", "code": "THEIRS"})

	ownerC := e.seedAndLogin(t, "deptowner2", domain.RoleUser)
	setOwner(t, e, mine, userIDOf(t, e, "deptowner2"))

	resp, env := e.do(t, ownerC, "GET",
		fmt.Sprintf("/api/dept/cost-report?department_id=%d", theirs), nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("查别的部门应 403，实际 %d：%v", resp.StatusCode, env)
	}
	notExist, _ := e.do(t, ownerC, "GET", "/api/dept/cost-report?department_id=999999", nil)
	if notExist.StatusCode != resp.StatusCode {
		t.Errorf("不存在的部门与无权的部门应返回同一状态码，实际 %d 与 %d",
			notExist.StatusCode, resp.StatusCode)
	}
}

// 部门范围不可被 group_by 或其它筛选参数绕开：请求 department 维度或携带
// 另一个部门的筛选条件时，结果仍限定在本部门。
func TestDeptReportForcesOwnDepartmentScope(t *testing.T) {
	e := newTestEnv(t)
	adminC := e.seedAndLogin(t, "deptrepadmin3", domain.RoleAdmin)
	mine := createDepartment(t, e, adminC, map[string]any{"name": "本部门", "code": "SCOPE"})
	ownerC := e.seedAndLogin(t, "deptowner3", domain.RoleUser)
	setOwner(t, e, mine, userIDOf(t, e, "deptowner3"))

	// group_by=channel 不在允许清单内，应回落到默认的 user 维度而非按渠道出数。
	resp, env := e.do(t, ownerC, "GET",
		fmt.Sprintf("/api/dept/cost-report?department_id=%d&group_by=channel", mine), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("查本部门报表应 200，实际 %d：%v", resp.StatusCode, env)
	}
	data, _ := env["data"].(map[string]any)
	if got, _ := data["group_by"].(string); got != string(store.AggByUser) {
		t.Errorf("不支持的聚合维度应回落为 user，实际 %q", got)
	}
	if got, _ := data["department_id"].(float64); int64(got) != mine {
		t.Errorf("报表应固定在本部门 %d，实际 %v", mine, data["department_id"])
	}
}

// 负责人视图不暴露网关的采购成本与差额——那是网关运营方的数据，
// 与部门的费用分摊无关。
func TestDeptReportHidesCostAndMargin(t *testing.T) {
	e := newTestEnv(t)
	adminC := e.seedAndLogin(t, "deptrepadmin4", domain.RoleAdmin)
	deptID := createDepartment(t, e, adminC, map[string]any{"name": "成本隔离部", "code": "COST"})
	ownerC := e.seedAndLogin(t, "deptowner4", domain.RoleUser)
	setOwner(t, e, deptID, userIDOf(t, e, "deptowner4"))

	resp, env := e.do(t, ownerC, "GET",
		fmt.Sprintf("/api/dept/cost-report?department_id=%d", deptID), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("查本部门报表应 200，实际 %d：%v", resp.StatusCode, env)
	}
	data, _ := env["data"].(map[string]any)
	rows, _ := data["rows"].([]any)
	for _, raw := range rows {
		row, _ := raw.(map[string]any)
		for _, banned := range []string{"credits_cost", "margin"} {
			if _, ok := row[banned]; ok {
				t.Errorf("部门负责人视图不应含字段 %q：%v", banned, row)
			}
		}
	}
}

// 不是任何部门负责人的账号，负责部门列表为空且不报错——前端据此隐藏入口。
func TestDeptDepartmentsEmptyForNonOwner(t *testing.T) {
	e := newTestEnv(t)
	plainC := e.seedAndLogin(t, "deptplain", domain.RoleUser)

	resp, env := e.do(t, plainC, "GET", "/api/dept/departments", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("非负责人查负责部门列表应 200，实际 %d：%v", resp.StatusCode, env)
	}
	rows, ok := env["data"].([]any)
	if !ok {
		t.Fatalf("负责部门列表应为数组，实际 %v", env["data"])
	}
	if len(rows) != 0 {
		t.Errorf("非负责人不应有负责部门，实际 %v", rows)
	}
}

// 登录态获取接口须附带负责部门 ID，供前端一次性判定是否显示部门费用入口。
func TestMeReturnsManagedDepartments(t *testing.T) {
	e := newTestEnv(t)
	adminC := e.seedAndLogin(t, "deptrepadmin5", domain.RoleAdmin)
	deptID := createDepartment(t, e, adminC, map[string]any{"name": "带负责人的部门", "code": "OWNED"})
	ownerC := e.seedAndLogin(t, "deptowner5", domain.RoleUser)
	setOwner(t, e, deptID, userIDOf(t, e, "deptowner5"))

	resp, env := e.do(t, ownerC, "GET", "/api/auth/me", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("获取登录态应 200，实际 %d：%v", resp.StatusCode, env)
	}
	data, _ := env["data"].(map[string]any)
	// 既有字段必须保持平铺，前端依赖 data.username 等字段。
	if got, _ := data["username"].(string); got != "deptowner5" {
		t.Errorf("响应应保持用户字段平铺，实际 %v", data)
	}
	ids, _ := data["managed_department_ids"].([]any)
	if len(ids) != 1 {
		t.Fatalf("应返回 1 个负责部门，实际 %v", data["managed_department_ids"])
	}
	if int64(ids[0].(float64)) != deptID {
		t.Errorf("负责部门应为 %d，实际 %v", deptID, ids[0])
	}
}
