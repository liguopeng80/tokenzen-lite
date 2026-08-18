package billing

import (
	"context"
	"testing"

	"gorm.io/gorm"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// seedKey 种入一个 API Key；limit 非 nil 时带独立额度。
func seedKey(t *testing.T, db *gorm.DB, userID int64, limit *domain.Credits) *store.APIKey {
	t.Helper()
	k := &store.APIKey{
		UserID: userID, Name: "orphan-test-key", KeyHash: "hash-" + t.Name(),
		KeyPrefix: "sk-test", Status: domain.KeyEnabled, CreditLimit: limit,
	}
	if err := db.Create(k).Error; err != nil {
		t.Fatalf("种入 API Key 失败: %v", err)
	}
	return k
}

// precharge 模拟中继预扣：写 consume 流水 + 累计密钥已用额度。
// 与 relay.BillingSession.Precharge 一致：无论密钥有无独立额度上限都累计 credit_used。
func precharge(t *testing.T, db *gorm.DB, svc *Service, key *store.APIKey,
	requestID string, amount domain.Credits) {
	t.Helper()
	if err := db.Exec(`UPDATE api_keys SET credit_used = credit_used + ? WHERE id = ?`,
		amount, key.ID).Error; err != nil {
		t.Fatalf("累计密钥已用额度失败: %v", err)
	}
	_, err := svc.Apply(context.Background(), Adjustment{
		UserID: key.UserID, Amount: -amount, EntryType: domain.LedgerConsume,
		RefType: "api_key", RefID: key.ID, RequestID: requestID,
		AffectsUsed: true, Note: "请求预扣",
	})
	if err != nil {
		t.Fatalf("预扣失败: %v", err)
	}
}

// backdate 把指定请求的全部流水时间回拨 1 小时（超过默认 15 分钟阈值）。
func backdate(t *testing.T, db *gorm.DB, requestID string) {
	t.Helper()
	if err := db.Exec(`UPDATE credit_ledger SET created_at = now() - interval '1 hour'
		WHERE request_id = ?`, requestID).Error; err != nil {
		t.Fatalf("回拨流水时间失败: %v", err)
	}
}

// 孤儿预扣被补退款：余额恢复、密钥额度占用回退、退款流水与追溯日志齐备。
func TestOrphanPrechargeRefunded(t *testing.T) {
	db := newTestDB(t)
	svc := NewService(db)
	u := seedUser(t, db, "orphan-user", 10_000)
	limit := domain.Credits(5000)
	key := seedKey(t, db, u.ID, &limit)

	const reqID = "req-orphan-1"
	precharge(t, db, svc, key, reqID, 3000)
	backdate(t, db, reqID)

	result, err := svc.CleanupOrphanPrecharges(context.Background(), DefaultOrphanPrechargeThreshold)
	if err != nil {
		t.Fatalf("清理失败: %v", err)
	}
	if result.Scanned != 1 || result.Refunded != 1 || result.RefundedCredits != 3000 {
		t.Errorf("应扫描 1 条并退款 3000 积分，实际 %+v", result)
	}

	var fresh store.User
	db.First(&fresh, u.ID)
	if fresh.CreditBalance != 10_000 {
		t.Errorf("退款后余额应恢复 10000，实际 %d", fresh.CreditBalance)
	}
	if fresh.CreditUsed != 0 {
		t.Errorf("退款后累计消费应回到 0，实际 %d", fresh.CreditUsed)
	}

	var refund store.LedgerEntry
	err = db.Where("request_id = ? AND entry_type = ?", reqID, domain.LedgerRefund).
		First(&refund).Error
	if err != nil {
		t.Fatalf("应存在退款流水: %v", err)
	}
	if refund.Amount != 3000 {
		t.Errorf("退款流水金额应 3000，实际 %d", refund.Amount)
	}

	var freshKey store.APIKey
	db.First(&freshKey, key.ID)
	if freshKey.CreditUsed != 0 {
		t.Errorf("密钥额度占用应回退到 0，实际 %d", freshKey.CreditUsed)
	}

	var log store.UsageLog
	if err := db.Where("request_id = ?", reqID).First(&log).Error; err != nil {
		t.Fatalf("应存在追溯用量日志: %v", err)
	}
	if log.Status != domain.UsageRefunded {
		t.Errorf("追溯日志状态应 refunded，实际 %s", log.Status)
	}
	if log.CreditsPrecharged != 3000 || log.UserID != u.ID || log.APIKeyID != key.ID {
		t.Errorf("追溯日志字段不符: precharged=%d user=%d key=%d",
			log.CreditsPrecharged, log.UserID, log.APIKeyID)
	}
	assertReconcileClean(t, db)

	// 幂等：再次执行不产生任何新退款。
	again, err := svc.CleanupOrphanPrecharges(context.Background(), DefaultOrphanPrechargeThreshold)
	if err != nil {
		t.Fatalf("二次清理失败: %v", err)
	}
	if again.Scanned != 0 || again.Refunded != 0 {
		t.Errorf("二次清理应无孤儿命中，实际 %+v", again)
	}
	db.First(&fresh, u.ID)
	if fresh.CreditBalance != 10_000 {
		t.Errorf("二次清理不应改变余额，实际 %d", fresh.CreditBalance)
	}
	assertReconcileClean(t, db)
}

// 无额度上限密钥的孤儿预扣被补退款后，密钥已用额度同步回退，不留虚高用量。
func TestOrphanPrechargeRefundedUnlimitedKey(t *testing.T) {
	db := newTestDB(t)
	svc := NewService(db)
	u := seedUser(t, db, "orphan-unlimited-user", 10_000)
	key := seedKey(t, db, u.ID, nil)

	const reqID = "req-orphan-unlimited"
	precharge(t, db, svc, key, reqID, 3000)

	var midKey store.APIKey
	db.First(&midKey, key.ID)
	if midKey.CreditUsed != 3000 {
		t.Fatalf("预扣后无上限密钥已用额度应为 3000，实际 %d", midKey.CreditUsed)
	}

	backdate(t, db, reqID)
	result, err := svc.CleanupOrphanPrecharges(context.Background(), DefaultOrphanPrechargeThreshold)
	if err != nil {
		t.Fatalf("清理失败: %v", err)
	}
	if result.Scanned != 1 || result.Refunded != 1 || result.RefundedCredits != 3000 {
		t.Errorf("应扫描 1 条并退款 3000 积分，实际 %+v", result)
	}

	var fresh store.User
	db.First(&fresh, u.ID)
	if fresh.CreditBalance != 10_000 {
		t.Errorf("退款后余额应恢复 10000，实际 %d", fresh.CreditBalance)
	}

	var freshKey store.APIKey
	db.First(&freshKey, key.ID)
	if freshKey.CreditUsed != 0 {
		t.Errorf("退款后无上限密钥已用额度应回退到 0，实际 %d", freshKey.CreditUsed)
	}
	assertReconcileClean(t, db)
}

// 已终态或在途的预扣一律不动：结算差额、已退款、零差额结算（零额流水标记）、未超阈值。
func TestOrphanCleanupSkipsFinalizedAndInFlight(t *testing.T) {
	db := newTestDB(t)
	svc := NewService(db)
	u := seedUser(t, db, "orphan-skip-user", 100_000)
	key := seedKey(t, db, u.ID, nil)
	ctx := context.Background()

	// a) 已结算（有 settle_adjust 差额流水）
	precharge(t, db, svc, key, "req-settled", 3000)
	if _, err := svc.Apply(ctx, Adjustment{
		UserID: u.ID, Amount: 1000, EntryType: domain.LedgerSettleAdjust,
		RefType: "api_key", RefID: key.ID, RequestID: "req-settled",
		AffectsUsed: true, Note: "结算差额",
	}); err != nil {
		t.Fatalf("结算差额失败: %v", err)
	}
	backdate(t, db, "req-settled")

	// b) 已退款
	precharge(t, db, svc, key, "req-refunded", 2000)
	if _, err := svc.Apply(ctx, Adjustment{
		UserID: u.ID, Amount: 2000, EntryType: domain.LedgerRefund,
		RefType: "api_key", RefID: key.ID, RequestID: "req-refunded",
		AffectsUsed: true, Note: "请求失败退款",
	}); err != nil {
		t.Fatalf("退款失败: %v", err)
	}
	backdate(t, db, "req-refunded")

	// c) 零差额结算：零额 settle_adjust 流水是终态标记。
	// 刻意不种用量日志——日志允许丢弃（队列满/停机刷盘超时），
	// 仅凭流水即须保住已结算请求，不得误判孤儿退款。
	precharge(t, db, svc, key, "req-exact", 1500)
	if _, err := svc.Apply(ctx, Adjustment{
		UserID: u.ID, Amount: 0, EntryType: domain.LedgerSettleAdjust,
		RefType: "api_key", RefID: key.ID, RequestID: "req-exact",
		AffectsUsed: true, Note: "结算差额",
	}); err != nil {
		t.Fatalf("零额结算差额失败: %v", err)
	}
	backdate(t, db, "req-exact")

	// d) 在途请求（未超阈值）
	precharge(t, db, svc, key, "req-inflight", 800)

	balanceBefore := currentBalance(t, db, u.ID)
	result, err := svc.CleanupOrphanPrecharges(ctx, DefaultOrphanPrechargeThreshold)
	if err != nil {
		t.Fatalf("清理失败: %v", err)
	}
	if result.Scanned != 0 || result.Refunded != 0 {
		t.Errorf("终态与在途请求都不应命中，实际 %+v", result)
	}
	if after := currentBalance(t, db, u.ID); after != balanceBefore {
		t.Errorf("清理不应改变余额: 前 %d 后 %d", balanceBefore, after)
	}
	assertReconcileClean(t, db)
}

// 多条孤儿混合终态请求：只退孤儿，金额逐条准确。
func TestOrphanCleanupMultiple(t *testing.T) {
	db := newTestDB(t)
	svc := NewService(db)
	u := seedUser(t, db, "orphan-multi-user", 50_000)
	key := seedKey(t, db, u.ID, nil)
	ctx := context.Background()

	precharge(t, db, svc, key, "req-multi-1", 1000)
	precharge(t, db, svc, key, "req-multi-2", 2500)
	precharge(t, db, svc, key, "req-multi-ok", 4000)
	if _, err := svc.Apply(ctx, Adjustment{
		UserID: u.ID, Amount: 4000, EntryType: domain.LedgerRefund,
		RefType: "api_key", RefID: key.ID, RequestID: "req-multi-ok",
		AffectsUsed: true, Note: "请求失败退款",
	}); err != nil {
		t.Fatalf("退款失败: %v", err)
	}
	for _, id := range []string{"req-multi-1", "req-multi-2", "req-multi-ok"} {
		backdate(t, db, id)
	}

	result, err := svc.CleanupOrphanPrecharges(ctx, DefaultOrphanPrechargeThreshold)
	if err != nil {
		t.Fatalf("清理失败: %v", err)
	}
	if result.Scanned != 2 || result.Refunded != 2 || result.RefundedCredits != 3500 {
		t.Errorf("应退款 2 条合计 3500，实际 %+v", result)
	}
	if balance := currentBalance(t, db, u.ID); balance != 50_000 {
		t.Errorf("全部退款后余额应恢复 50000，实际 %d", balance)
	}
	var logCount int64
	db.Model(&store.UsageLog{}).
		Where("request_id IN ? AND status = ?", []string{"req-multi-1", "req-multi-2"},
			domain.UsageRefunded).Count(&logCount)
	if logCount != 2 {
		t.Errorf("应补 2 条已退款追溯日志，实际 %d", logCount)
	}
	assertReconcileClean(t, db)
}

// 用量日志不是终态判据：结算写入失败的请求只有 consume 流水加一条 failed 日志，
// 超阈值后必须命中孤儿并补退款，且已有日志被改写为 refunded、实际扣款归零。
func TestOrphanUsageLogAloneDoesNotProtect(t *testing.T) {
	db := newTestDB(t)
	svc := NewService(db)
	u := seedUser(t, db, "orphan-log-only-user", 10_000)
	key := seedKey(t, db, u.ID, nil)

	const reqID = "req-settle-failed"
	precharge(t, db, svc, key, reqID, 2000)
	if err := db.Create(&store.UsageLog{
		RequestID: reqID, UserID: u.ID, APIKeyID: key.ID,
		ModelName: "test-model", Status: domain.UsageFailed,
		CreditsPrecharged: 2000, CreditsCharged: 2000,
		ErrorMessage: "结算写入失败，预扣保留，待孤儿预扣清理补偿",
	}).Error; err != nil {
		t.Fatalf("种入结算失败日志失败: %v", err)
	}
	backdate(t, db, reqID)

	result, err := svc.CleanupOrphanPrecharges(context.Background(), DefaultOrphanPrechargeThreshold)
	if err != nil {
		t.Fatalf("清理失败: %v", err)
	}
	if result.Scanned != 1 || result.Refunded != 1 || result.RefundedCredits != 2000 {
		t.Errorf("仅有用量日志不构成终态，应退款 2000，实际 %+v", result)
	}
	if balance := currentBalance(t, db, u.ID); balance != 10_000 {
		t.Errorf("补退后余额应恢复 10000，实际 %d", balance)
	}

	var log store.UsageLog
	if err := db.Where("request_id = ?", reqID).First(&log).Error; err != nil {
		t.Fatalf("读取用量日志失败: %v", err)
	}
	if log.Status != domain.UsageRefunded {
		t.Errorf("补退后日志状态应改写为 refunded，实际 %s", log.Status)
	}
	if log.CreditsCharged != 0 {
		t.Errorf("补退后实际扣款应归零，实际 %d", log.CreditsCharged)
	}
	assertReconcileClean(t, db)
}

func currentBalance(t *testing.T, db *gorm.DB, userID int64) domain.Credits {
	t.Helper()
	var u store.User
	if err := db.First(&u, userID).Error; err != nil {
		t.Fatalf("读取用户失败: %v", err)
	}
	return u.CreditBalance
}
