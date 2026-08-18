package billing

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// TestDailyLimitExceededSequential 单线程基线：当日累计扣费突破上限时被拒，
// 退款与不带上限的调整不受影响。为并发测试提供可独立排查的逻辑基线。
func TestDailyLimitExceededSequential(t *testing.T) {
	db := newTestDB(t)
	svc := NewService(db)
	u := seedUser(t, db, "dl-seq", 1_000_000)
	const limit = domain.Credits(5_000)
	const precharge = domain.Credits(3_000)

	// 首次：当日 0 + 3000 = 3000 ≤ 5000，通过。
	if _, err := svc.Apply(context.Background(), Adjustment{
		UserID: u.ID, Amount: -precharge, EntryType: domain.LedgerConsume,
		RequestID: "req-dl-seq-1", AffectsUsed: true, DailyLimit: limit,
	}); err != nil {
		t.Fatalf("首次预扣应成功: %v", err)
	}
	// 二次：3000 + 3000 = 6000 > 5000，被 ErrDailyLimitExceeded 拒绝。
	_, err := svc.Apply(context.Background(), Adjustment{
		UserID: u.ID, Amount: -precharge, EntryType: domain.LedgerConsume,
		RequestID: "req-dl-seq-2", AffectsUsed: true, DailyLimit: limit,
	})
	if !errors.Is(err, ErrDailyLimitExceeded) {
		t.Fatalf("二次预扣应超上限被拒，实际: %v", err)
	}
	// 边界：再扣 2000（恰好达上限 5000，不超）应通过。
	if _, err := svc.Apply(context.Background(), Adjustment{
		UserID: u.ID, Amount: -2000, EntryType: domain.LedgerConsume,
		RequestID: "req-dl-seq-3", AffectsUsed: true, DailyLimit: limit,
	}); err != nil {
		t.Fatalf("恰达上限（=不超）应通过，实际: %v", err)
	}
	// 退款不带 DailyLimit，不受上限校验影响。
	if _, err := svc.Apply(context.Background(), Adjustment{
		UserID: u.ID, Amount: precharge, EntryType: domain.LedgerRefund,
		RequestID: "req-dl-seq-4", AffectsUsed: true,
	}); err != nil {
		t.Fatalf("退款不应被日上限拦截: %v", err)
	}

	// daily_spend：扣费累计 3000 + 2000 = 5000，退款回减 3000 → 2000（被拒的 3000 未入账）。
	// 退款与扣费同源，按 applyTx 内 daily_spend 累加的 -amount 累计，故等量回减。
	var spend store.DailySpend
	if err := db.Where("user_id = ?", u.ID).Take(&spend).Error; err != nil {
		t.Fatalf("查询 daily_spend 失败: %v", err)
	}
	if want := domain.Credits(2_000); spend.Credits != want {
		t.Errorf("daily_spend 应为 %d（扣 5000 - 退 3000），实际 %d", want, spend.Credits)
	}
	assertReconcileClean(t, db)
}

// TestDailyLimitConcurrentPrecharge 验证日花费上限的权威校验闭合 TOCTOU：
// 两个并发预扣合计超过上限时，恰好一个被 ErrDailyLimitExceeded 拒绝，daily_spend 不被突破。
//
// 缺陷背景：relay 层 checkDailySpendLimit 在余额行锁之外读 daily_spend 做预检，
// N 个并发请求可同时通过预检、再串行写入扣费，使日上限被超出 (N-1)×precharge。
// 权威校验移入 billing.applyTx 同事务（持用户行锁、写 daily_spend 之前）后，
// 用户行锁串行化同一用户的并发调整，第二个请求看到第一个的写入后即被拒。
func TestDailyLimitConcurrentPrecharge(t *testing.T) {
	db := newTestDB(t)
	svc := NewService(db)
	u := seedUser(t, db, "dl-conc", 10_000_000)
	const limit = domain.Credits(5_000)
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
			<-started // 同时触发，最大化竞态窗口
			_, err := svc.Apply(context.Background(), Adjustment{
				UserID: u.ID, Amount: -precharge, EntryType: domain.LedgerConsume,
				RequestID: fmt.Sprintf("req-dl-conc-%d", i), AffectsUsed: true,
				DailyLimit: limit,
			})
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				succeeded++
			case errors.Is(err, ErrDailyLimitExceeded):
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
		t.Fatalf("应恰好 1 成功 + 1 ErrDailyLimitExceeded 拒绝，实际 成功=%d 被拒=%d（竞态未闭合）",
			succeeded, dailyLimited)
	}
	// daily_spend 恰为单次预扣 4000，未被突破。这是权威校验生效的直接证据。
	var spend store.DailySpend
	if err := db.Where("user_id = ?", u.ID).Take(&spend).Error; err != nil {
		t.Fatalf("查询 daily_spend 失败: %v", err)
	}
	if spend.Credits != precharge {
		t.Errorf("daily_spend 应恰为单次预扣 %d（上限未被突破），实际 %d",
			precharge, spend.Credits)
	}
	assertReconcileClean(t, db)
}

// TestDailyLimitConcurrentManyWorkers 更高并发压力：上限只够 1 次预扣，8 个并发请求
// 同时抢，恰好 1 个成功、7 个被 ErrDailyLimitExceeded 拒绝，daily_spend 不超过上限。
func TestDailyLimitConcurrentManyWorkers(t *testing.T) {
	db := newTestDB(t)
	svc := NewService(db)
	u := seedUser(t, db, "dl-many", 100_000_000)
	const limit = domain.Credits(3_000)
	const precharge = domain.Credits(3_000)
	const workers = 8 // 合计 24000，上限 3000，至多 1 个成功

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
				UserID: u.ID, Amount: -precharge, EntryType: domain.LedgerConsume,
				RequestID: fmt.Sprintf("req-dl-many-%d", i), AffectsUsed: true,
				DailyLimit: limit,
			})
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				succeeded++
			case errors.Is(err, ErrDailyLimitExceeded):
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
		t.Fatalf("其余 %d 个应被 ErrDailyLimitExceeded 拒绝，实际 %d", workers-1, dailyLimited)
	}
	var spend store.DailySpend
	if err := db.Where("user_id = ?", u.ID).Take(&spend).Error; err != nil {
		t.Fatalf("查询 daily_spend 失败: %v", err)
	}
	if spend.Credits != limit {
		t.Errorf("daily_spend 应恰为上限 %d，实际 %d（被突破）", limit, spend.Credits)
	}
	assertReconcileClean(t, db)
}
