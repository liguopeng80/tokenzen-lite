package billing

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"gorm.io/gorm"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// 本文件为 Key 级每日花费上限的 DB 集成测试，需 TZL_TEST_DATABASE_URL。
// 与 daily_limit_test.go（用户级）同范式：权威校验在 applyTx 同事务行锁内，
// 闭合 relay 层 checkKeyDailySpendLimit 预检与扣费之间的 TOCTOU。
// 集成库由主会话统一门禁覆盖，本文件不在本会话内运行。

// seedKeyWithDailyLimit 种入一个属于 user 的 API Key 行，满足 daily_spend_by_key 的外键。
func seedKeyWithDailyLimit(t *testing.T, db *gorm.DB, userID int64, dailyLimit domain.Credits) *store.APIKey {
	t.Helper()
	k := &store.APIKey{
		UserID: userID, Name: "kdl-test", KeyHash: fmt.Sprintf("hash-%d", userID),
		KeyPrefix: "sk-test", Status: domain.KeyEnabled,
		DailySpendLimit: dailyLimit,
	}
	if err := db.Create(k).Error; err != nil {
		t.Fatalf("种入 API Key 失败: %v", err)
	}
	return k
}

// TestKeyDailyLimitSequential 单线程基线：Key 当日累计扣费突破上限时被拒，
// 退款不触发 Key 上限校验。为并发测试提供可独立排查的逻辑基线。
func TestKeyDailyLimitSequential(t *testing.T) {
	db := newTestDB(t)
	svc := NewService(db)
	u := seedUser(t, db, "kdl-seq", 1_000_000)
	k := seedKeyWithDailyLimit(t, db, u.ID, 5_000)
	const precharge = domain.Credits(3_000)

	// 首次：0 + 3000 = 3000 ≤ 5000，通过。
	if _, err := svc.Apply(context.Background(), Adjustment{
		UserID: u.ID, KeyID: k.ID, Amount: -precharge, EntryType: domain.LedgerConsume,
		RequestID: "req-kdl-seq-1", AffectsUsed: true, KeyDailyLimit: k.DailySpendLimit,
	}); err != nil {
		t.Fatalf("首次预扣应成功: %v", err)
	}
	// 二次：3000 + 3000 = 6000 > 5000，被 ErrKeyDailyLimitExceeded 拒绝。
	_, err := svc.Apply(context.Background(), Adjustment{
		UserID: u.ID, KeyID: k.ID, Amount: -precharge, EntryType: domain.LedgerConsume,
		RequestID: "req-kdl-seq-2", AffectsUsed: true, KeyDailyLimit: k.DailySpendLimit,
	})
	if !errors.Is(err, ErrKeyDailyLimitExceeded) {
		t.Fatalf("二次预扣应超 Key 上限被拒，实际: %v", err)
	}
	// 边界：再扣 2000（恰好达上限 5000，不超）应通过。
	if _, err := svc.Apply(context.Background(), Adjustment{
		UserID: u.ID, KeyID: k.ID, Amount: -2000, EntryType: domain.LedgerConsume,
		RequestID: "req-kdl-seq-3", AffectsUsed: true, KeyDailyLimit: k.DailySpendLimit,
	}); err != nil {
		t.Fatalf("恰达 Key 上限（=不超）应通过，实际: %v", err)
	}
	// 退款不带 KeyDailyLimit，不受 Key 上限校验影响，且回减 Key 当日计数。
	if _, err := svc.Apply(context.Background(), Adjustment{
		UserID: u.ID, KeyID: k.ID, Amount: precharge, EntryType: domain.LedgerRefund,
		RequestID: "req-kdl-seq-4", AffectsUsed: true,
	}); err != nil {
		t.Fatalf("退款不应被 Key 日上限拦截: %v", err)
	}
	// daily_spend_by_key：扣 3000 + 2000 = 5000，退款回减 3000 → 2000。
	var keySpend store.DailySpendByKey
	if err := db.Where("api_key_id = ?", k.ID).Take(&keySpend).Error; err != nil {
		t.Fatalf("查询 daily_spend_by_key 失败: %v", err)
	}
	if want := domain.Credits(2_000); keySpend.Credits != want {
		t.Errorf("daily_spend_by_key 应为 %d（扣 5000 - 退 3000），实际 %d", want, keySpend.Credits)
	}
	assertReconcileClean(t, db)
}

// TestKeyDailyLimitConcurrentPrecharge 验证 Key 日花费上限的权威校验闭合 TOCTOU：
// 两个并发预扣合计超过 Key 上限时，恰好一个被 ErrKeyDailyLimitExceeded 拒绝，
// daily_spend_by_key 不被突破。用户行锁串行化同一用户的并发调整，Key 维度亦被串行化。
func TestKeyDailyLimitConcurrentPrecharge(t *testing.T) {
	db := newTestDB(t)
	svc := NewService(db)
	u := seedUser(t, db, "kdl-conc", 10_000_000)
	k := seedKeyWithDailyLimit(t, db, u.ID, 5_000)
	const precharge = domain.Credits(4_000) // 单次 < 上限，两次合计 8000 > 上限 5000

	const workers = 2
	var wg sync.WaitGroup
	started := make(chan struct{})
	var mu sync.Mutex
	succeeded, dailyLimited := 0, 0
	var otherErr error

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-started
			_, err := svc.Apply(context.Background(), Adjustment{
				UserID: u.ID, KeyID: k.ID, Amount: -precharge, EntryType: domain.LedgerConsume,
				RequestID: fmt.Sprintf("req-kdl-conc-%d", i), AffectsUsed: true,
				KeyDailyLimit: k.DailySpendLimit,
			})
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				succeeded++
			case errors.Is(err, ErrKeyDailyLimitExceeded):
				dailyLimited++
			default:
				otherErr = err
			}
		}(i)
	}
	close(started)
	wg.Wait()

	if otherErr != nil {
		t.Fatalf("不应有其他错误，收到: %v", otherErr)
	}
	if succeeded != 1 || dailyLimited != 1 {
		t.Fatalf("应恰好 1 成功 + 1 ErrKeyDailyLimitExceeded 拒绝，实际 成功=%d 被拒=%d（竞态未闭合）",
			succeeded, dailyLimited)
	}
	var keySpend store.DailySpendByKey
	if err := db.Where("api_key_id = ?", k.ID).Take(&keySpend).Error; err != nil {
		t.Fatalf("查询 daily_spend_by_key 失败: %v", err)
	}
	if keySpend.Credits != precharge {
		t.Errorf("daily_spend_by_key 应恰为单次预扣 %d（上限未被突破），实际 %d",
			precharge, keySpend.Credits)
	}
	assertReconcileClean(t, db)
}

// TestKeyDailyLimitMoreWorkers 更高并发：上限只够 1 次预扣，8 个并发请求同时抢，
// 恰好 1 个成功、其余被 ErrKeyDailyLimitExceeded 拒绝，daily_spend_by_key 不超过上限。
func TestKeyDailyLimitMoreWorkers(t *testing.T) {
	db := newTestDB(t)
	svc := NewService(db)
	u := seedUser(t, db, "kdl-many", 100_000_000)
	k := seedKeyWithDailyLimit(t, db, u.ID, 3_000)
	const precharge = domain.Credits(3_000)
	const workers = 8

	var wg sync.WaitGroup
	started := make(chan struct{})
	var mu sync.Mutex
	succeeded, dailyLimited := 0, 0
	var otherErr error

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-started
			_, err := svc.Apply(context.Background(), Adjustment{
				UserID: u.ID, KeyID: k.ID, Amount: -precharge, EntryType: domain.LedgerConsume,
				RequestID: fmt.Sprintf("req-kdl-many-%d", i), AffectsUsed: true,
				KeyDailyLimit: k.DailySpendLimit,
			})
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				succeeded++
			case errors.Is(err, ErrKeyDailyLimitExceeded):
				dailyLimited++
			default:
				otherErr = err
			}
		}(i)
	}
	close(started)
	wg.Wait()

	if otherErr != nil {
		t.Fatalf("不应有其他错误，收到: %v", otherErr)
	}
	if succeeded != 1 {
		t.Fatalf("恰好 1 个应成功，实际 %d", succeeded)
	}
	if dailyLimited != workers-1 {
		t.Fatalf("其余 %d 个应被 ErrKeyDailyLimitExceeded 拒绝，实际 %d", workers-1, dailyLimited)
	}
	var keySpend store.DailySpendByKey
	if err := db.Where("api_key_id = ?", k.ID).Take(&keySpend).Error; err != nil {
		t.Fatalf("查询 daily_spend_by_key 失败: %v", err)
	}
	if keySpend.Credits != k.DailySpendLimit {
		t.Errorf("daily_spend_by_key 应恰为上限 %d，实际 %d（被突破）",
			k.DailySpendLimit, keySpend.Credits)
	}
	assertReconcileClean(t, db)
}

// TestKeyDailyLimitIndependentFromUser Key 上限与用户上限独立：用户上限宽松、Key 更紧时，
// 按 Key 上限拒绝（ErrKeyDailyLimitExceeded）；用户上限更紧时按用户上限拒绝。
func TestKeyDailyLimitIndependentFromUser(t *testing.T) {
	db := newTestDB(t)
	svc := NewService(db)
	u := seedUser(t, db, "kdl-indep", 10_000_000)
	k := seedKeyWithDailyLimit(t, db, u.ID, 3_000) // Key 上限 3000，比用户上限 5000 更紧

	// 用户上限 5000，Key 上限 3000，预扣 4000：Key 更紧 → ErrKeyDailyLimitExceeded。
	_, err := svc.Apply(context.Background(), Adjustment{
		UserID: u.ID, KeyID: k.ID, Amount: -4_000, EntryType: domain.LedgerConsume,
		RequestID: "req-kdl-indep-1", AffectsUsed: true,
		DailyLimit: 5_000, KeyDailyLimit: k.DailySpendLimit,
	})
	if !errors.Is(err, ErrKeyDailyLimitExceeded) {
		t.Fatalf("Key 更紧时应按 Key 上限拒绝，实际: %v", err)
	}

	// 用户上限更紧（2_000），Key 上限 3_000，预扣 2_500：按用户上限拒绝。
	_, err = svc.Apply(context.Background(), Adjustment{
		UserID: u.ID, KeyID: k.ID, Amount: -2_500, EntryType: domain.LedgerConsume,
		RequestID: "req-kdl-indep-2", AffectsUsed: true,
		DailyLimit: 2_000, KeyDailyLimit: k.DailySpendLimit,
	})
	if !errors.Is(err, ErrDailyLimitExceeded) {
		t.Fatalf("用户上限更紧时应按用户上限拒绝，实际: %v", err)
	}
	assertReconcileClean(t, db)
}

// TestKeyDailyLimitUnlimited Key 上限为 0（不限）时不拒绝，即使预扣很大。
func TestKeyDailyLimitUnlimited(t *testing.T) {
	db := newTestDB(t)
	svc := NewService(db)
	u := seedUser(t, db, "kdl-unlimited", 100_000_000)
	k := seedKeyWithDailyLimit(t, db, u.ID, 0) // 0 = 不限

	if _, err := svc.Apply(context.Background(), Adjustment{
		UserID: u.ID, KeyID: k.ID, Amount: -50_000, EntryType: domain.LedgerConsume,
		RequestID: "req-kdl-unlimited-1", AffectsUsed: true, KeyDailyLimit: 0,
	}); err != nil {
		t.Fatalf("Key 上限为 0（不限）时不应拒绝，实际: %v", err)
	}
	// KeyDailyLimit 未设（0）即便 Key 行有非零上限也视为不施加 Key 校验：
	// 是否校验由 Adjustment.KeyDailyLimit 决定（relay 侧从 key.DailySpendLimit 透传）。
	assertReconcileClean(t, db)
}
