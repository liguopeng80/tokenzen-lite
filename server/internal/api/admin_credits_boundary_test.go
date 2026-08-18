package api

// 管理端积分调整接口的边界与幂等用例（报告 P3-18）：
// 调整金额为 0 应 400；扣回金额超过用户当前余额应 400 且余额与流水均不变；
// 同一管理动作连续提交两次时固化实际设计——POST /admin/users/{id}/credits
// 未生成幂等键（billing.Service.Grant 不设置流水的 request_id），
// 因此该端点按当前实现非幂等：重复提交会分别记账、余额被重复调整。
// 每条用例结束后调用 assertReconcile 校验不变式：用户余额 == 积分流水金额之和。

import (
	"fmt"
	"testing"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// ledgerCount 返回指定用户的流水条数。
func (e *testEnv) ledgerCount(t *testing.T, userID int64) int64 {
	t.Helper()
	var n int64
	if err := e.db.Model(&store.LedgerEntry{}).Where("user_id = ?", userID).Count(&n).Error; err != nil {
		t.Fatalf("查流水条数失败: %v", err)
	}
	return n
}

// TestAdminCreditsZeroAmountRejected 覆盖 P3-18：调整金额为 0 应 400，且不产生副作用。
func TestAdminCreditsZeroAmountRejected(t *testing.T) {
	e := newTestEnv(t)
	rootC := e.seedAndLogin(t, "root-credit0", domain.RoleRoot)
	targetC := e.seedAndLogin(t, "target-credit0", domain.RoleUser) // id=2
	_ = targetC

	var targetID int64 = 2
	if _, err := e.deps.Billing.Grant(t.Context(), targetID, 500_000, 1, "初始额度", ""); err != nil {
		t.Fatalf("初始分配失败: %v", err)
	}
	balBefore := e.userBalance(t, targetID)
	ledgerBefore := e.ledgerCount(t, targetID)

	resp, env := e.do(t, rootC, "POST", fmt.Sprintf("/api/admin/users/%d/credits", targetID),
		map[string]any{"amount": 0, "note": "零调整"})
	if resp.StatusCode != 400 {
		t.Fatalf("调整金额为 0 应 400，实际 %d %v", resp.StatusCode, env)
	}

	if bal := e.userBalance(t, targetID); bal != balBefore {
		t.Errorf("金额为 0 被拒绝后余额不应变化，之前 %d，之后 %d", balBefore, bal)
	}
	if n := e.ledgerCount(t, targetID); n != ledgerBefore {
		t.Errorf("金额为 0 被拒绝后不应产生新流水，之前 %d 条，之后 %d 条", ledgerBefore, n)
	}
	e.assertReconcile(t)
}

// TestAdminCreditsRevokeExceedsBalanceRejected 覆盖 P3-18：扣回金额超过用户当前余额应 400，
// 且余额与流水均不变（区别于结算补扣场景的截断到 0——该场景走 ClampToBalance，
// 管理员扣回走严格拒绝，见 billing.Service.applyTx）。
func TestAdminCreditsRevokeExceedsBalanceRejected(t *testing.T) {
	e := newTestEnv(t)
	rootC := e.seedAndLogin(t, "root-overdraw", domain.RoleRoot)
	e.seedAndLogin(t, "target-overdraw", domain.RoleUser) // id=2
	var targetID int64 = 2
	if _, err := e.deps.Billing.Grant(t.Context(), targetID, 100_000, 1, "初始额度", ""); err != nil {
		t.Fatalf("初始分配失败: %v", err)
	}
	balBefore := e.userBalance(t, targetID)
	ledgerBefore := e.ledgerCount(t, targetID)

	// 扣回 200000，超过当前余额 100000
	resp, env := e.do(t, rootC, "POST", fmt.Sprintf("/api/admin/users/%d/credits", targetID),
		map[string]any{"amount": -200_000, "note": "超额扣回"})
	if resp.StatusCode != 400 {
		t.Fatalf("扣回超过余额应 400，实际 %d %v", resp.StatusCode, env)
	}
	if msg, _ := env["message"].(string); msg != "扣回金额超过用户当前余额" {
		t.Errorf("提示文案应为 扣回金额超过用户当前余额，实际: %v", env["message"])
	}

	if bal := e.userBalance(t, targetID); bal != balBefore {
		t.Errorf("超额扣回被拒绝后余额不应变化，之前 %d，之后 %d", balBefore, bal)
	}
	if n := e.ledgerCount(t, targetID); n != ledgerBefore {
		t.Errorf("超额扣回被拒绝后不应产生新流水，之前 %d 条，之后 %d 条", ledgerBefore, n)
	}
	e.assertReconcile(t)

	// 对照：扣回恰好等于余额应成功，余额归零
	resp, env = e.do(t, rootC, "POST", fmt.Sprintf("/api/admin/users/%d/credits", targetID),
		map[string]any{"amount": -100_000, "note": "全额扣回"})
	if resp.StatusCode != 200 {
		t.Fatalf("扣回恰好等于余额应成功，实际 %d %v", resp.StatusCode, env)
	}
	if bal := e.userBalance(t, targetID); bal != 0 {
		t.Errorf("全额扣回后余额应为 0，实际 %d", bal)
	}
	e.assertReconcile(t)
}

// TestAdminCreditsGrantNotIdempotent 固化 P3-18 的实际设计：
// POST /admin/users/{id}/credits 未使用幂等键（billing.Service.Grant 不写入流水的
// request_id，credit_ledger_request_entry_uniq 只对非空 request_id 生效），
// 管理员重复提交同一笔发放会各自记账、余额被重复累加——该端点按当前实现非幂等。
// 该行为已在 docs/api-contract.md 的积分调整章节写明，此测试为回归保护：
// 若未来补齐幂等键使其改为幂等，需同步更新契约文档与本测试。
func TestAdminCreditsGrantNotIdempotent(t *testing.T) {
	e := newTestEnv(t)
	rootC := e.seedAndLogin(t, "root-dup", domain.RoleRoot)
	e.seedAndLogin(t, "target-dup", domain.RoleUser) // id=2
	var targetID int64 = 2
	ledgerBefore := e.ledgerCount(t, targetID)

	body := map[string]any{"amount": 300_000, "note": "重复提交测试"}
	resp1, env1 := e.do(t, rootC, "POST", fmt.Sprintf("/api/admin/users/%d/credits", targetID), body)
	if resp1.StatusCode != 200 {
		t.Fatalf("第一次提交应 200，实际 %d %v", resp1.StatusCode, env1)
	}
	resp2, env2 := e.do(t, rootC, "POST", fmt.Sprintf("/api/admin/users/%d/credits", targetID), body)
	if resp2.StatusCode != 200 {
		t.Fatalf("第二次提交（重放同一动作）应 200（当前实现不做幂等拦截），实际 %d %v",
			resp2.StatusCode, env2)
	}

	if bal := e.userBalance(t, targetID); bal != 600_000 {
		t.Errorf("非幂等实现下重复提交应各自生效，期望余额 600000，实际 %d", bal)
	}
	if n := e.ledgerCount(t, targetID); n != ledgerBefore+2 {
		t.Errorf("非幂等实现下应产生 2 条独立流水，之前 %d 条，之后 %d 条", ledgerBefore, n)
	}
	e.assertReconcile(t)
}
