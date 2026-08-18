package billing

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"

	"gorm.io/gorm"

	"github.com/liguopeng80/tokenzen-lite/server/internal/auth"
	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store/migrate"
)

func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	url := os.Getenv("TZL_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("未设置 TZL_TEST_DATABASE_URL，跳过 billing 集成测试")
	}
	if err := migrate.Up(url); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}
	db, err := store.Open(url)
	if err != nil {
		t.Fatalf("连接测试库失败: %v", err)
	}
	if sqlDB, dbErr := db.DB(); dbErr == nil {
		// 测试结束关闭连接池，防止累计空闲连接耗尽 PostgreSQL 连接上限。
		t.Cleanup(func() { _ = sqlDB.Close() })
	}
	if err := db.Exec("TRUNCATE users, api_keys, sessions, credit_ledger, redemptions, usage_logs RESTART IDENTITY CASCADE").Error; err != nil {
		t.Fatalf("清空测试表失败: %v", err)
	}
	return db
}

func seedUser(t *testing.T, db *gorm.DB, username string, balance domain.Credits) *store.User {
	t.Helper()
	u := &store.User{
		Username: username, PasswordHash: "x",
		Role: domain.RoleUser, Status: domain.UserEnabled,
	}
	if err := db.Create(u).Error; err != nil {
		t.Fatalf("种入用户失败: %v", err)
	}
	if balance > 0 {
		svc := NewService(db)
		if _, err := svc.Grant(context.Background(), u.ID, balance, 0, "测试初始额度", ""); err != nil {
			t.Fatalf("初始分配失败: %v", err)
		}
	}
	return u
}

func assertReconcileClean(t *testing.T, db *gorm.DB) {
	t.Helper()
	mismatches, err := store.NewLedgerRepo(db).Reconcile(context.Background())
	if err != nil {
		t.Fatalf("对账查询失败: %v", err)
	}
	if len(mismatches) != 0 {
		t.Fatalf("对账不通过: %+v", mismatches)
	}
}

// 并发扣款：余额只够 33 次，50 个并发只允许 33 次成功，余额不打穿不为负。
func TestConcurrentDebit(t *testing.T) {
	db := newTestDB(t)
	svc := NewService(db)
	u := seedUser(t, db, "concurrent-user", 100_000)

	const workers = 50
	const debit = 3000
	var wg sync.WaitGroup
	var mu sync.Mutex
	succeeded, insufficient := 0, 0

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := svc.Apply(context.Background(), Adjustment{
				UserID: u.ID, Amount: -debit, EntryType: domain.LedgerConsume,
				RequestID: fmt.Sprintf("req-conc-%d", i), AffectsUsed: true,
			})
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				succeeded++
			case errors.Is(err, ErrInsufficientCredits):
				insufficient++
			default:
				t.Errorf("意外错误: %v", err)
			}
		}(i)
	}
	wg.Wait()

	if succeeded != 33 {
		t.Errorf("100000/3000 应成功 33 次，实际 %d", succeeded)
	}
	if insufficient != workers-33 {
		t.Errorf("其余 %d 次应余额不足，实际 %d", workers-33, insufficient)
	}
	var fresh store.User
	db.First(&fresh, u.ID)
	if fresh.CreditBalance != 100_000-33*debit {
		t.Errorf("期望余额 %d，实际 %d", 100_000-33*debit, fresh.CreditBalance)
	}
	if fresh.CreditUsed != 33*debit {
		t.Errorf("期望累计消费 %d，实际 %d", 33*debit, fresh.CreditUsed)
	}
	assertReconcileClean(t, db)
}

// 并发核销同一兑换码：只允许一次成功。
func TestConcurrentRedeem(t *testing.T) {
	db := newTestDB(t)
	svc := NewService(db)
	u := seedUser(t, db, "redeem-user", 0)

	code := "tzr-test-code-abc123"
	red := &store.Redemption{
		CodeHash: auth.HashKey(code), Credits: 50_000,
		Status: domain.RedemptionUnused,
	}
	if err := db.Create(red).Error; err != nil {
		t.Fatalf("种入兑换码失败: %v", err)
	}

	const workers = 20
	var wg sync.WaitGroup
	var mu sync.Mutex
	succeeded := 0
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := svc.Redeem(context.Background(), u.ID, code)
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				succeeded++
			} else if !errors.Is(err, ErrRedemptionUnavailable) {
				t.Errorf("意外错误: %v", err)
			}
		}()
	}
	wg.Wait()

	if succeeded != 1 {
		t.Errorf("并发核销应只成功 1 次，实际 %d", succeeded)
	}
	var fresh store.User
	db.First(&fresh, u.ID)
	if fresh.CreditBalance != 50_000 {
		t.Errorf("期望余额 50000，实际 %d", fresh.CreditBalance)
	}
	assertReconcileClean(t, db)
}

// 幂等：同一 request_id + entry_type 的第二次写入返回 ErrDuplicateEntry 且不改余额。
func TestIdempotentEntry(t *testing.T) {
	db := newTestDB(t)
	svc := NewService(db)
	u := seedUser(t, db, "idem-user", 10_000)

	adj := Adjustment{
		UserID: u.ID, Amount: -1000, EntryType: domain.LedgerConsume,
		RequestID: "req-idem-1", AffectsUsed: true,
	}
	if _, err := svc.Apply(context.Background(), adj); err != nil {
		t.Fatalf("首次扣款失败: %v", err)
	}
	_, err := svc.Apply(context.Background(), adj)
	if !errors.Is(err, ErrDuplicateEntry) {
		t.Fatalf("重复流水应返回 ErrDuplicateEntry，实际: %v", err)
	}
	var fresh store.User
	db.First(&fresh, u.ID)
	if fresh.CreditBalance != 9000 {
		t.Errorf("重复写入不应二次扣款，余额应 9000，实际 %d", fresh.CreditBalance)
	}
	assertReconcileClean(t, db)
}

// 结算补扣截断：超出余额时只扣到 0，欠扣计入备注。
func TestClampToBalance(t *testing.T) {
	db := newTestDB(t)
	svc := NewService(db)
	u := seedUser(t, db, "clamp-user", 500)

	entry, err := svc.Apply(context.Background(), Adjustment{
		UserID: u.ID, Amount: -2000, EntryType: domain.LedgerSettleAdjust,
		RequestID: "req-clamp-1", AffectsUsed: true, ClampToBalance: true,
		Note: "结算补扣",
	})
	if err != nil {
		t.Fatalf("截断补扣不应报错: %v", err)
	}
	if entry.Amount != -500 || entry.BalanceAfter != 0 {
		t.Errorf("应截断为 -500/余额 0，实际 %d/%d", entry.Amount, entry.BalanceAfter)
	}
	if !strings.Contains(entry.Note, "欠扣 1500") {
		t.Errorf("备注应记录欠扣金额，实际: %s", entry.Note)
	}
	assertReconcileClean(t, db)
}

// TestClampToBalanceWithKey 锁定 C1 的 credit_used clamp 同口径修复（F8 评审 MEDIUM）：
// 结算补扣被 ClampToBalance 截断时，api_keys.credit_used 按 clamp 后金额累计，与
// users/ledger/daily_spend 同口径。旧实现按原始 diff 累计，api_keys 会偏高。
func TestClampToBalanceWithKey(t *testing.T) {
	db := newTestDB(t)
	svc := NewService(db)
	u := seedUser(t, db, "clamp-key-user", 130)
	k := seedKeyWithDailyLimit(t, db, u.ID, 0) // 无 Key 日上限、credit_limit=NULL（无独立额度上限）

	// 预扣 100：余额 130→30，api_keys.credit_used=100。
	if _, err := svc.Apply(context.Background(), Adjustment{
		UserID: u.ID, KeyID: k.ID, Amount: -100, EntryType: domain.LedgerConsume,
		RefType: "api_key", RefID: k.ID,
		RequestID: "req-clamp-key-1", AffectsUsed: true, KeyPrechargeMode: true,
	}); err != nil {
		t.Fatalf("预扣应成功: %v", err)
	}
	// 结算 200（diff=100）：余额 30 不够补扣 100，ClampToBalance 截断到实际可扣 30。
	// 旧实现 api_keys.credit_used 会按 diff=100 累计得 200（偏高 70）；C1 后按 clamp
	// 后的 30 累计得 130，与 users/ledger/daily_spend 同口径。
	if _, err := svc.Apply(context.Background(), Adjustment{
		UserID: u.ID, KeyID: k.ID, Amount: -100, EntryType: domain.LedgerSettleAdjust,
		RefType: "api_key", RefID: k.ID,
		RequestID: "req-clamp-key-2", AffectsUsed: true, ClampToBalance: true,
	}); err != nil {
		t.Fatalf("结算截断应成功: %v", err)
	}

	// api_keys.credit_used：预扣 100 + 结算 clamp 30 = 130（F8 修复的核心断言）。
	var key store.APIKey
	if err := db.Where("id = ?", k.ID).Take(&key).Error; err != nil {
		t.Fatalf("查询密钥失败: %v", err)
	}
	if key.CreditUsed != 130 {
		t.Errorf("api_keys.credit_used 应为 130（clamp 同口径），实际 %d", key.CreditUsed)
	}

	var usr store.User
	if err := db.Where("id = ?", u.ID).Take(&usr).Error; err != nil {
		t.Fatalf("查询用户失败: %v", err)
	}
	if usr.CreditBalance != 0 {
		t.Errorf("余额应被截断到 0，实际 %d", usr.CreditBalance)
	}
	if usr.CreditUsed != 130 {
		t.Errorf("users.credit_used 应为 130，实际 %d", usr.CreditUsed)
	}

	var sumLedger int64
	if err := db.Raw("SELECT COALESCE(SUM(amount), 0) FROM credit_ledger WHERE user_id = ? AND ref_type = 'api_key'", u.ID).Scan(&sumLedger).Error; err != nil {
		t.Fatalf("查询流水失败: %v", err)
	}
	if sumLedger != -130 {
		t.Errorf("Σledger.amount 应为 -130，实际 %d", sumLedger)
	}

	// 用户余额对账与密钥额度对账均零偏差（密钥侧即 C1 后降为防御巡检的 ReconcileAPIKeys）。
	assertReconcileClean(t, db)
	keyMismatches, err := store.NewLedgerRepo(db).ReconcileAPIKeys(context.Background())
	if err != nil {
		t.Fatalf("密钥对账查询失败: %v", err)
	}
	if len(keyMismatches) != 0 {
		t.Fatalf("密钥对账不通过: %+v", keyMismatches)
	}
}

// 管理员扣回超过余额直接拒绝（不截断）。
func TestRevokeInsufficient(t *testing.T) {
	db := newTestDB(t)
	svc := NewService(db)
	u := seedUser(t, db, "revoke-user", 1000)

	_, err := svc.Grant(context.Background(), u.ID, -5000, 1, "扣回", "")
	if !errors.Is(err, ErrInsufficientCredits) {
		t.Fatalf("扣回超额应报余额不足，实际: %v", err)
	}
	assertReconcileClean(t, db)
}
