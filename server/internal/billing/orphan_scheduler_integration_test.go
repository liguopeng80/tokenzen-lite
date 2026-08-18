package billing

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// 定时回收（P1-3）：孤儿预扣满足"余额 == 流水之和"的对账不变式，常规对账发现不了。
// 在定时回收之前，回收只发生在服务启动时——长期不重启的进程等于不回收，
// 结算写库失败的请求会把用户积分一直扣住。

// TestScanOrphanMatchesCleanup 只扫描与实际回收的判定口径一致：
// 扫描报几条，回收就退几条；回收后再扫描应为空。
func TestScanOrphanMatchesCleanup(t *testing.T) {
	db := newTestDB(t)
	svc := NewService(db)
	ctx := context.Background()
	u := seedUser(t, db, "scan-orphan", 10_000)
	key := seedKey(t, db, u.ID, nil)

	precharge(t, db, svc, key, "scan-req-1", 700)
	precharge(t, db, svc, key, "scan-req-2", 300)
	backdate(t, db, "scan-req-1")
	backdate(t, db, "scan-req-2")

	orphans, err := svc.ScanOrphanPrecharges(ctx, DefaultOrphanPrechargeThreshold)
	if err != nil {
		t.Fatalf("扫描失败: %v", err)
	}
	if len(orphans) != 2 {
		t.Fatalf("应扫描出 2 条孤儿预扣，实际 %d", len(orphans))
	}
	var total domain.Credits
	for _, o := range orphans {
		total += o.Amount
	}
	if total != 1000 {
		t.Errorf("孤儿预扣合计应为 1000 积分，实际 %d", total)
	}

	result, err := svc.CleanupOrphanPrecharges(ctx, DefaultOrphanPrechargeThreshold)
	if err != nil {
		t.Fatalf("回收失败: %v", err)
	}
	if result.Scanned != 2 || result.Refunded != 2 {
		t.Errorf("回收结果应为扫描 2 退款 2，实际 scanned=%d refunded=%d",
			result.Scanned, result.Refunded)
	}

	orphans, err = svc.ScanOrphanPrecharges(ctx, DefaultOrphanPrechargeThreshold)
	if err != nil {
		t.Fatalf("回收后扫描失败: %v", err)
	}
	if len(orphans) != 0 {
		t.Errorf("回收后不应再有孤儿预扣，实际 %d 条", len(orphans))
	}
}

// TestOrphanSchedulerRefundsOnTick 调度器在设置的间隔到达后自动补退孤儿预扣，
// 全程无需重启服务；ctx 取消后循环退出。
func TestOrphanSchedulerRefundsOnTick(t *testing.T) {
	db := newTestDB(t)
	svc := NewService(db)
	settings := store.NewSettingsRepo(db)
	ctx := context.Background()

	u := seedUser(t, db, "sched-orphan", 10_000)
	key := seedKey(t, db, u.ID, nil)
	precharge(t, db, svc, key, "sched-req-1", 800)
	backdate(t, db, "sched-req-1")

	raw, _ := json.Marshal(int64(1))
	if err := settings.Set(ctx, "orphan_cleanup_interval_sec", raw); err != nil {
		t.Fatalf("设置回收间隔失败: %v", err)
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	(&OrphanCleanupScheduler{Service: svc, Settings: settings}).Start(runCtx)

	// 间隔 1 秒，留出设置缓存与调度抖动的余量后断言终态。
	deadline := time.Now().Add(10 * time.Second)
	var balance domain.Credits
	for time.Now().Before(deadline) {
		time.Sleep(200 * time.Millisecond)
		balance = currentBalance(t, db, u.ID)
		if balance == 10_000 {
			break
		}
	}
	if balance != 10_000 {
		t.Fatalf("定时回收应把预扣的 800 积分退回，余额应为 10000，实际 %d", balance)
	}

	orphans, err := svc.ScanOrphanPrecharges(ctx, DefaultOrphanPrechargeThreshold)
	if err != nil {
		t.Fatalf("扫描失败: %v", err)
	}
	if len(orphans) != 0 {
		t.Errorf("定时回收后不应再有孤儿预扣，实际 %d 条", len(orphans))
	}
}

// TestOrphanSchedulerDisabledByZeroInterval 间隔设为 0 时不执行回收，
// 但循环仍在运行（用于在线打开回收），孤儿预扣保持原样。
func TestOrphanSchedulerDisabledByZeroInterval(t *testing.T) {
	db := newTestDB(t)
	svc := NewService(db)
	settings := store.NewSettingsRepo(db)
	ctx := context.Background()

	u := seedUser(t, db, "sched-off", 10_000)
	key := seedKey(t, db, u.ID, nil)
	precharge(t, db, svc, key, "sched-off-req", 500)
	backdate(t, db, "sched-off-req")

	raw, _ := json.Marshal(int64(0))
	if err := settings.Set(ctx, "orphan_cleanup_interval_sec", raw); err != nil {
		t.Fatalf("设置回收间隔失败: %v", err)
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	(&OrphanCleanupScheduler{Service: svc, Settings: settings}).Start(runCtx)
	time.Sleep(1500 * time.Millisecond)

	if balance := currentBalance(t, db, u.ID); balance != 9_500 {
		t.Errorf("回收关闭时不应发生退款，余额应为 9500，实际 %d", balance)
	}
}
