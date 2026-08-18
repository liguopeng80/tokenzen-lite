package billing

import (
	"context"
	"errors"
	"strings"
	"testing"

	"gorm.io/gorm"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// backdateBy 把指定请求的全部流水时间回拨指定分钟数。
func backdateBy(t *testing.T, db *gorm.DB, requestID string, minutes int) {
	t.Helper()
	if err := db.Exec(`UPDATE credit_ledger
		SET created_at = now() - make_interval(mins => ?)
		WHERE request_id = ?`, minutes, requestID).Error; err != nil {
		t.Fatalf("回拨流水时间失败: %v", err)
	}
}

// 幂等冲突路径：request_id 下已存在 refund 流水（模拟清理与中继退款并发竞争）时，
// refundOrphan 命中 (request_id, entry_type) 幂等索引返回 ErrDuplicateEntry，
// 提前返回，不执行密钥额度回退与追溯日志写入。
func TestOrphanRefundIdempotentConflict(t *testing.T) {
	db := newTestDB(t)
	svc := NewService(db)
	u := seedUser(t, db, "orphan-conflict-user", 10_000)
	key := seedKey(t, db, u.ID, nil)
	ctx := context.Background()

	const reqID = "req-orphan-conflict"
	precharge(t, db, svc, key, reqID, 3000)

	// 模拟中继侧已完成退款：只写流水与用户余额（中继退款路径不改本模拟的密钥额度）。
	if _, err := svc.Apply(ctx, Adjustment{
		UserID: u.ID, Amount: 3000, EntryType: domain.LedgerRefund,
		RefType: "api_key", RefID: key.ID, RequestID: reqID,
		AffectsUsed: true, Note: "请求失败退款",
	}); err != nil {
		t.Fatalf("模拟中继退款失败: %v", err)
	}

	balanceBefore := currentBalance(t, db, u.ID)
	var keyBefore store.APIKey
	db.First(&keyBefore, key.ID)

	err := svc.refundOrphan(ctx, OrphanPrecharge{
		UserID: u.ID, RequestID: reqID, Amount: 3000, APIKeyID: key.ID,
	})
	if !errors.Is(err, ErrDuplicateEntry) {
		t.Fatalf("应返回 ErrDuplicateEntry，实际 %v", err)
	}

	if after := currentBalance(t, db, u.ID); after != balanceBefore {
		t.Errorf("幂等命中不应改变余额: 前 %d 后 %d", balanceBefore, after)
	}
	var keyAfter store.APIKey
	db.First(&keyAfter, key.ID)
	if keyAfter.CreditUsed != keyBefore.CreditUsed {
		t.Errorf("幂等命中不应回退密钥额度: 前 %d 后 %d",
			keyBefore.CreditUsed, keyAfter.CreditUsed)
	}
	var logCount int64
	db.Model(&store.UsageLog{}).Where("request_id = ?", reqID).Count(&logCount)
	if logCount != 0 {
		t.Errorf("幂等命中不应写追溯日志，实际 %d 条", logCount)
	}
	assertReconcileClean(t, db)
}

// 空 request_id 的 consume 流水（无请求标识的扣减）不参与孤儿扫描，
// 与幂等索引的部分索引条件（WHERE request_id <> ”）保持一致。
func TestOrphanCleanupSkipsEmptyRequestID(t *testing.T) {
	db := newTestDB(t)
	svc := NewService(db)
	u := seedUser(t, db, "orphan-emptyreq-user", 10_000)
	ctx := context.Background()

	if _, err := svc.Apply(ctx, Adjustment{
		UserID: u.ID, Amount: -2000, EntryType: domain.LedgerConsume,
		RequestID: "", AffectsUsed: true, Note: "无请求标识扣减",
	}); err != nil {
		t.Fatalf("扣减失败: %v", err)
	}
	if err := db.Exec(`UPDATE credit_ledger SET created_at = now() - interval '1 hour'
		WHERE request_id = '' AND entry_type = ?`, domain.LedgerConsume).Error; err != nil {
		t.Fatalf("回拨流水时间失败: %v", err)
	}

	result, err := svc.CleanupOrphanPrecharges(ctx, DefaultOrphanPrechargeThreshold)
	if err != nil {
		t.Fatalf("清理失败: %v", err)
	}
	if result.Scanned != 0 || result.Refunded != 0 {
		t.Errorf("空 request_id 不应命中孤儿扫描，实际 %+v", result)
	}
	if balance := currentBalance(t, db, u.ID); balance != 8000 {
		t.Errorf("余额不应被清理改变，实际 %d", balance)
	}
	assertReconcileClean(t, db)
}

// 多用户各有孤儿预扣：一次清理各自独立退款，账务互不串扰。
func TestOrphanCleanupMultiUser(t *testing.T) {
	db := newTestDB(t)
	svc := NewService(db)
	u1 := seedUser(t, db, "orphan-mu-user1", 10_000)
	u2 := seedUser(t, db, "orphan-mu-user2", 20_000)
	k1 := seedKey(t, db, u1.ID, nil)
	k2 := &store.APIKey{
		UserID: u2.ID, Name: "orphan-test-key-2", KeyHash: "hash2-" + t.Name(),
		KeyPrefix: "sk-test", Status: domain.KeyEnabled,
	}
	if err := db.Create(k2).Error; err != nil {
		t.Fatalf("种入第二个 API Key 失败: %v", err)
	}

	precharge(t, db, svc, k1, "req-mu-1", 1500)
	precharge(t, db, svc, k2, "req-mu-2", 4000)
	backdate(t, db, "req-mu-1")
	backdate(t, db, "req-mu-2")

	result, err := svc.CleanupOrphanPrecharges(context.Background(), DefaultOrphanPrechargeThreshold)
	if err != nil {
		t.Fatalf("清理失败: %v", err)
	}
	if result.Scanned != 2 || result.Refunded != 2 || result.RefundedCredits != 5500 {
		t.Errorf("应退款 2 条合计 5500，实际 %+v", result)
	}
	if b := currentBalance(t, db, u1.ID); b != 10_000 {
		t.Errorf("用户 1 余额应恢复 10000，实际 %d", b)
	}
	if b := currentBalance(t, db, u2.ID); b != 20_000 {
		t.Errorf("用户 2 余额应恢复 20000，实际 %d", b)
	}
	for _, c := range []struct {
		reqID  string
		userID int64
		amount domain.Credits
	}{{"req-mu-1", u1.ID, 1500}, {"req-mu-2", u2.ID, 4000}} {
		var refund store.LedgerEntry
		if err := db.Where("request_id = ? AND entry_type = ?", c.reqID, domain.LedgerRefund).
			First(&refund).Error; err != nil {
			t.Fatalf("应存在退款流水 %s: %v", c.reqID, err)
		}
		if refund.UserID != c.userID || refund.Amount != c.amount {
			t.Errorf("退款流水 %s 归属或金额不符: user=%d amount=%d",
				c.reqID, refund.UserID, refund.Amount)
		}
		var log store.UsageLog
		if err := db.Where("request_id = ?", c.reqID).First(&log).Error; err != nil {
			t.Fatalf("应存在追溯日志 %s: %v", c.reqID, err)
		}
		if log.Status != domain.UsageRefunded || log.UserID != c.userID {
			t.Errorf("追溯日志 %s 状态或归属不符: status=%s user=%d",
				c.reqID, log.Status, log.UserID)
		}
	}
	assertReconcileClean(t, db)
}

// 密钥额度回退下限截断：credit_used 已被其他路径回收到小于预扣额时，
// 补退款后 credit_used 被截断为 0 而非负数。
func TestOrphanKeyCreditUsedClampedAtZero(t *testing.T) {
	db := newTestDB(t)
	svc := NewService(db)
	u := seedUser(t, db, "orphan-clamp-user", 10_000)
	key := seedKey(t, db, u.ID, nil)

	const reqID = "req-orphan-clamp"
	precharge(t, db, svc, key, reqID, 3000)
	// 模拟额度占用已被其他路径部分回收：credit_used 只剩 1000（< 预扣额 3000）。
	if err := db.Exec(`UPDATE api_keys SET credit_used = 1000 WHERE id = ?`,
		key.ID).Error; err != nil {
		t.Fatalf("改写密钥已用额度失败: %v", err)
	}
	backdate(t, db, reqID)

	result, err := svc.CleanupOrphanPrecharges(context.Background(), DefaultOrphanPrechargeThreshold)
	if err != nil {
		t.Fatalf("清理失败: %v", err)
	}
	if result.Refunded != 1 {
		t.Fatalf("应退款 1 条，实际 %+v", result)
	}
	var freshKey store.APIKey
	db.First(&freshKey, key.ID)
	if freshKey.CreditUsed != 0 {
		t.Errorf("密钥已用额度应截断为 0 而非负数，实际 %d", freshKey.CreditUsed)
	}
	if balance := currentBalance(t, db, u.ID); balance != 10_000 {
		t.Errorf("余额应恢复 10000，实际 %d", balance)
	}
	assertReconcileClean(t, db)
}

// 阈值边界：预扣时间略小于阈值不命中，略大于阈值命中。
func TestOrphanThresholdBoundary(t *testing.T) {
	db := newTestDB(t)
	svc := NewService(db)
	u := seedUser(t, db, "orphan-boundary-user", 10_000)
	key := seedKey(t, db, u.ID, nil)
	ctx := context.Background()

	const reqID = "req-orphan-boundary"
	precharge(t, db, svc, key, reqID, 2000)

	backdateBy(t, db, reqID, 14)
	result, err := svc.CleanupOrphanPrecharges(ctx, DefaultOrphanPrechargeThreshold)
	if err != nil {
		t.Fatalf("清理失败: %v", err)
	}
	if result.Scanned != 0 {
		t.Errorf("14 分钟未超 15 分钟阈值，不应命中，实际 %+v", result)
	}

	backdateBy(t, db, reqID, 16)
	result, err = svc.CleanupOrphanPrecharges(ctx, DefaultOrphanPrechargeThreshold)
	if err != nil {
		t.Fatalf("清理失败: %v", err)
	}
	if result.Scanned != 1 || result.Refunded != 1 || result.RefundedCredits != 2000 {
		t.Errorf("16 分钟已超阈值，应退款 2000，实际 %+v", result)
	}
	if balance := currentBalance(t, db, u.ID); balance != 10_000 {
		t.Errorf("余额应恢复 10000，实际 %d", balance)
	}
	assertReconcileClean(t, db)
}

// 负数预扣防御（纯单元）：Amount<0 在首个数据库调用之前被拒绝，
// db 为 nil 仍不 panic 即证明未发起任何数据库写入。
func TestRefundOrphanRejectsNegativeAmount(t *testing.T) {
	svc := &Service{db: nil}
	err := svc.refundOrphan(context.Background(), OrphanPrecharge{
		UserID: 1, RequestID: "req-negative", Amount: -100, APIKeyID: 1,
	})
	if err == nil {
		t.Fatal("负数预扣应直接报错")
	}
	if !strings.Contains(err.Error(), "负数预扣") {
		t.Errorf("错误信息应说明负数预扣，实际 %q", err.Error())
	}
}
