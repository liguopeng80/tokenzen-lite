package relay

// 密钥已用额度对账（ReconcileAPIKeys）的集成校验。
//
// credit_used 由本包 BillingSession 的 Precharge/Settle/Refund 三处裸 SQL 维护
// （外加 billing/orphan.go 的 refundOrphan），脱离 billing.Service 事务、失败仅 warn。
// 单校用户余额对账发现不了密钥账漂移，ReconcileAPIKeys 补这一道。
//
// 这里直接驱动 BillingSession 跑完整 Precharge→Settle 流程以产生真实的
// ref_type='api_key' 流水与 credit_used 更新，再断言对账结果。

import (
	"context"
	"testing"

	"github.com/liguopeng80/tokenzen-lite/server/internal/billing"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// 【集成】完整 Precharge→Settle 流程后，密钥已用额度与流水对账通过。
func TestReconcileAPIKeysCleanAfterSettle(t *testing.T) {
	db := newSessionTestDB(t)
	svc := billing.NewService(db)
	ctx := context.Background()
	key := seedSessionKey(t, db, svc, "reconcile-clean", nil)

	// 预扣 3000、结算 2000：多扣的 1000 退回，credit_used 终值 2000。
	// 该用例不涉及日上限，dailyLimit 传 0（不限制）。
	s := NewBillingSession(db, svc, "req-reconcile-clean", key, 0)
	if err := s.Precharge(ctx, 3000); err != nil {
		t.Fatalf("预扣失败: %v", err)
	}
	if err := s.Settle(ctx, 2000); err != nil {
		t.Fatalf("结算失败: %v", err)
	}

	if k := keyRow(t, db, key.ID); k.CreditUsed != 2000 {
		t.Fatalf("结算后 credit_used 应为 2000，实际 %d", k.CreditUsed)
	}
	mismatches, err := store.NewLedgerRepo(db).ReconcileAPIKeys(ctx)
	if err != nil {
		t.Fatalf("ReconcileAPIKeys 查询失败: %v", err)
	}
	if len(mismatches) != 0 {
		t.Fatalf("完整流程后应无不一致密钥，实际 %+v", mismatches)
	}
}

// 【集成】人为漂移 credit_used（+100）后，ReconcileAPIKeys 精确报出该密钥与差额。
// 模拟 relay/orphan 的裸 SQL 维护失败：warn 后账面与流水各走各的。
func TestReconcileAPIKeysDetectsDrift(t *testing.T) {
	db := newSessionTestDB(t)
	svc := billing.NewService(db)
	ctx := context.Background()
	key := seedSessionKey(t, db, svc, "reconcile-drift", nil)

	// 预扣 1000、结算 1000：diff 为 0，credit_used 终值 1000，流水合计 -1000。
	s := NewBillingSession(db, svc, "req-reconcile-drift", key, 0)
	if err := s.Precharge(ctx, 1000); err != nil {
		t.Fatalf("预扣失败: %v", err)
	}
	if err := s.Settle(ctx, 1000); err != nil {
		t.Fatalf("结算失败: %v", err)
	}

	// 模拟裸 SQL 失败导致的漂移：credit_used 多记 100，流水不变。
	if err := db.Exec(
		`UPDATE api_keys SET credit_used = credit_used + 100 WHERE id = ?`, key.ID).
		Error; err != nil {
		t.Fatalf("人为破坏 credit_used 失败: %v", err)
	}

	mismatches, err := store.NewLedgerRepo(db).ReconcileAPIKeys(ctx)
	if err != nil {
		t.Fatalf("ReconcileAPIKeys 查询失败: %v", err)
	}
	if len(mismatches) != 1 {
		t.Fatalf("应精确报出 1 条漂移密钥，实际 %d 条: %+v", len(mismatches), mismatches)
	}
	m := mismatches[0]
	if m.KeyID != key.ID {
		t.Fatalf("应报出漂移密钥 id=%d，实际 key_id=%d", key.ID, m.KeyID)
	}
	// credit_used=1100、ledger_sum=-1000、difference=1100+(-1000)=100。
	if m.CreditUsed != 1100 || m.LedgerSum != -1000 || m.Difference != 100 {
		t.Fatalf("差额不符：credit_used=%d ledger_sum=%d difference=%d（期望 1100/-1000/100）",
			m.CreditUsed, m.LedgerSum, m.Difference)
	}
}
