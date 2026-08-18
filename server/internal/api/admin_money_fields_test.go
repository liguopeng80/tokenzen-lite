package api

// 管理端 third-party-reachable 实体端点的 _money 货币串旁置字段测试。
// 覆盖：admin 用户列表、发放积分响应、管理端流水列表、部门列表/详情、项目列表/详情。
// 默认兑换率 1,000,000 积分 = 1 元（6 位小数），故 N 积分 → "0.00000N" 元。

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
)

// moneyOf 从响应 data 中取出 _money 字段值。
func moneyOf(t *testing.T, data map[string]any, field string) string {
	t.Helper()
	v, ok := data[field]
	if !ok {
		t.Fatalf("响应缺少字段 %s：%v", field, data)
	}
	s, ok := v.(string)
	if !ok {
		t.Fatalf("字段 %s 不是字符串：%v", field, v)
	}
	return s
}

// assertMoneyEq 比对 _money 字段是否等于按默认兑换率（1e6，6 位小数）换算的预期值。
// pricing.CreditsToDecimalString 在默认兑换率下产生 6 位小数，如 1_000_000 → "1.000000"。
func assertMoneyEq(t *testing.T, data map[string]any, field string, credits int64) {
	t.Helper()
	got := moneyOf(t, data, field)
	want := formatExpectedMoney(credits)
	if got != want {
		t.Errorf("字段 %s 期望 %q，实际 %q", field, want, got)
	}
}

// formatExpectedMoney 按默认兑换率 1e6（6 位小数）把积分数换算为期望的货币串。
func formatExpectedMoney(credits int64) string {
	neg := credits < 0
	if neg {
		credits = -credits
	}
	whole := credits / 1_000_000
	frac := credits % 1_000_000
	s := fmt.Sprintf("%d.%06d", whole, frac)
	if neg {
		s = "-" + s
	}
	return s
}

// TestAdminUsersListMoneyFields 用户列表的每一行应旁置余额/已用/每日上限的 _money 串。
func TestAdminUsersListMoneyFields(t *testing.T) {
	e := newTestEnv(t)
	rootC := e.seedAndLogin(t, "root-ulm", domain.RoleRoot)
	targetC := e.seedAndLogin(t, "user-ulm", domain.RoleUser) // id=2
	_ = targetC

	// 给目标用户发放 1,500,000 积分，并设置每日上限 300,000。
	if _, err := e.deps.Billing.Grant(t.Context(), 2, 1_500_000, 1, "初始额度", ""); err != nil {
		t.Fatalf("发放积分失败: %v", err)
	}
	if err := e.deps.Users.UpdateFields(t.Context(), 2, map[string]any{"daily_spend_limit": int64(300_000)}); err != nil {
		t.Fatalf("设置每日上限失败: %v", err)
	}

	resp, env := e.do(t, rootC, "GET", "/api/admin/users/?page=1&page_size=10", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("用户列表应 200，实际 %d：%v", resp.StatusCode, env)
	}
	items := env["data"].(map[string]any)["items"].([]any)
	var found bool
	for _, raw := range items {
		row := raw.(map[string]any)
		if int64(row["id"].(float64)) != 2 {
			continue
		}
		found = true
		assertMoneyEq(t, row, "credit_balance_money", 1_500_000)
		// credit_used 默认 0
		assertMoneyEq(t, row, "daily_spend_limit_money", 300_000)
	}
	if !found {
		t.Fatalf("未在列表中找到目标用户 id=2：%v", items)
	}
}

// TestAdminGrantCreditsMoneyFields 发放积分的响应应旁置 amount/balance_after 的 _money 串。
func TestAdminGrantCreditsMoneyFields(t *testing.T) {
	e := newTestEnv(t)
	rootC := e.seedAndLogin(t, "root-gcm", domain.RoleRoot)
	_ = e.seedAndLogin(t, "user-gcm", domain.RoleUser) // id=2

	resp, env := e.do(t, rootC, "POST", "/api/admin/users/2/credits",
		map[string]any{"amount": 2_500_000, "note": "开通额度"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("发放积分应 200，实际 %d：%v", resp.StatusCode, env)
	}
	data := env["data"].(map[string]any)
	assertMoneyEq(t, data, "amount_money", 2_500_000)
	assertMoneyEq(t, data, "balance_after_money", 2_500_000)
}

// TestAdminLedgerMoneyFields 管理端流水列表的每一行应旁置 amount/balance_after 的 _money 串。
func TestAdminLedgerMoneyFields(t *testing.T) {
	e := newTestEnv(t)
	rootC := e.seedAndLogin(t, "root-ledger-m", domain.RoleRoot)
	_ = e.seedAndLogin(t, "user-ledger-m", domain.RoleUser) // id=2

	if _, err := e.deps.Billing.Grant(t.Context(), 2, 800_000, 1, "初始额度", ""); err != nil {
		t.Fatalf("发放积分失败: %v", err)
	}

	resp, env := e.do(t, rootC, "GET", "/api/admin/ledger?user_id=2", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("流水列表应 200，实际 %d：%v", resp.StatusCode, env)
	}
	items := env["data"].(map[string]any)["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("流水应有 1 条，实际 %d 条", len(items))
	}
	row := items[0].(map[string]any)
	assertMoneyEq(t, row, "amount_money", 800_000)
	assertMoneyEq(t, row, "balance_after_money", 800_000)
}

// TestAdminDepartmentsMoneyFields 部门列表与详情的月度预算应旁置 _money 串。
func TestAdminDepartmentsMoneyFields(t *testing.T) {
	e := newTestEnv(t)
	rootC := e.seedAndLogin(t, "root-dept-m", domain.RoleRoot)
	deptID := createDepartment(t, e, rootC, map[string]any{
		"name": "预算部门", "code": "BUDGET", "monthly_budget_credits": 5_000_000,
	})

	// 列表
	resp, env := e.do(t, rootC, "GET", "/api/admin/departments/?page=1&page_size=10", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("部门列表应 200，实际 %d：%v", resp.StatusCode, env)
	}
	items := env["data"].(map[string]any)["items"].([]any)
	var found bool
	for _, raw := range items {
		row := raw.(map[string]any)
		if int64(row["id"].(float64)) != deptID {
			continue
		}
		found = true
		assertMoneyEq(t, row, "monthly_budget_credits_money", 5_000_000)
	}
	if !found {
		t.Fatalf("未在部门列表中找到目标部门 id=%d", deptID)
	}

	// 详情
	resp, env = e.do(t, rootC, "GET", fmt.Sprintf("/api/admin/departments/%d", deptID), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("部门详情应 200，实际 %d：%v", resp.StatusCode, env)
	}
	data := env["data"].(map[string]any)
	assertMoneyEq(t, data, "monthly_budget_credits_money", 5_000_000)
}

// TestAdminProjectsMoneyFields 项目列表与详情的月度预算应旁置 _money 串。
func TestAdminProjectsMoneyFields(t *testing.T) {
	e := newTestEnv(t)
	rootC := e.seedAndLogin(t, "root-proj-m", domain.RoleRoot)
	resp, env := e.do(t, rootC, "POST", "/api/admin/projects/", map[string]any{
		"name": "预算项目", "code": "PBUDGET", "monthly_budget_credits": 3_200_000,
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("创建项目应 201，实际 %d：%v", resp.StatusCode, env)
	}
	projectID := int64(env["data"].(map[string]any)["id"].(float64))

	// 列表
	resp, env = e.do(t, rootC, "GET", "/api/admin/projects/?page=1&page_size=10", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("项目列表应 200，实际 %d：%v", resp.StatusCode, env)
	}
	items := env["data"].(map[string]any)["items"].([]any)
	var found bool
	for _, raw := range items {
		row := raw.(map[string]any)
		if int64(row["id"].(float64)) != projectID {
			continue
		}
		found = true
		assertMoneyEq(t, row, "monthly_budget_credits_money", 3_200_000)
	}
	if !found {
		t.Fatalf("未在项目列表中找到目标项目 id=%d", projectID)
	}

	// 详情
	resp, env = e.do(t, rootC, "GET", fmt.Sprintf("/api/admin/projects/%d", projectID), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("项目详情应 200，实际 %d：%v", resp.StatusCode, env)
	}
	data := env["data"].(map[string]any)
	assertMoneyEq(t, data, "monthly_budget_credits_money", 3_200_000)
}
