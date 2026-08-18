package ratelimit

import (
	"sync"
	"testing"
	"time"
)

func TestMemoryLimiterWindow(t *testing.T) {
	clock := newFakeClock()
	l := NewMemoryLimiter()
	l.Now = clock.now
	window := 100 * time.Millisecond
	for i := 0; i < 3; i++ {
		if !l.Allow("k1", 3, window) {
			t.Fatalf("第 %d 次应放行", i+1)
		}
	}
	if l.Allow("k1", 3, window) {
		t.Error("超出限额应拒绝")
	}
	if !l.Allow("k2", 3, window) {
		t.Error("不同 key 互不影响")
	}
	clock.advance(window + time.Millisecond)
	if !l.Allow("k1", 3, window) {
		t.Error("窗口滑动后应恢复放行")
	}
}

func TestLimiterDisabled(t *testing.T) {
	l := NewMemoryLimiter()
	for i := 0; i < 100; i++ {
		if !l.Allow("k", 0, time.Minute) {
			t.Fatal("limit=0 应不限流")
		}
	}
}

// mustAcquire 断言占用成功。
func mustAcquire(t *testing.T, g *ConcurrencyGate, userID int64,
	userLimit, limit, largeLimit int, large bool, msg string) {
	t.Helper()
	if ok, reason := g.Acquire(userID, userLimit, limit, largeLimit, large); !ok {
		t.Fatalf("%s（拒绝原因 %s）", msg, reason)
	}
}

// mustReject 断言占用被拒绝且拒绝原因正确。
func mustReject(t *testing.T, g *ConcurrencyGate, userID int64,
	userLimit, limit, largeLimit int, large bool, wantReason, msg string) {
	t.Helper()
	ok, reason := g.Acquire(userID, userLimit, limit, largeLimit, large)
	if ok {
		t.Fatalf("%s：应拒绝但被放行", msg)
	}
	if reason != wantReason {
		t.Errorf("%s：期望拒绝原因 %s，实际 %s", msg, wantReason, reason)
	}
}

func TestConcurrencyGate(t *testing.T) {
	g := NewConcurrencyGate()
	mustAcquire(t, g, 1, 0, 2, 0, false, "第 1 个槽位应可占用")
	mustAcquire(t, g, 1, 0, 2, 0, false, "第 2 个槽位应可占用")
	mustReject(t, g, 1, 0, 2, 0, false, GateRejectGlobal, "超出并发总上限")
	g.Release(1, false)
	mustAcquire(t, g, 1, 0, 2, 0, false, "释放后应可再次占用")
}

func TestConcurrencyGateLargeQuota(t *testing.T) {
	g := NewConcurrencyGate()
	// 大请求同时占用总槽位与大请求配额。
	mustAcquire(t, g, 1, 0, 10, 1, true, "首个大请求应可占用")
	mustReject(t, g, 1, 0, 10, 1, true, GateRejectLargeQuota,
		"大请求配额占满后，即使总槽位空闲也应拒绝大请求")
	mustAcquire(t, g, 1, 0, 10, 1, false, "大请求配额占满不应影响一般请求")
	// 大请求计入总槽位：总上限 2 已被 1 大 + 1 小占满。
	mustReject(t, g, 1, 0, 2, 1, false, GateRejectGlobal, "总槽位占满应拒绝一般请求")
	g.Release(1, true)
	mustAcquire(t, g, 1, 0, 10, 1, true, "释放大请求后配额应恢复")
	// largeLimit <= 0 表示大请求维度不限制。
	g2 := NewConcurrencyGate()
	mustAcquire(t, g2, 1, 0, 10, 0, true, "largeLimit=0 时第 1 个大请求不应受配额限制")
	mustAcquire(t, g2, 1, 0, 10, 0, true, "largeLimit=0 时第 2 个大请求不应受配额限制")
}

func TestConcurrencyGateUserQuota(t *testing.T) {
	g := NewConcurrencyGate()
	// 用户 1 占满自己的子配额（2），全局上限（10）仍有空余。
	mustAcquire(t, g, 1, 2, 10, 0, false, "用户 1 第 1 个槽位应可占用")
	mustAcquire(t, g, 1, 2, 10, 0, false, "用户 1 第 2 个槽位应可占用")
	mustReject(t, g, 1, 2, 10, 0, false, GateRejectUserQuota,
		"用户 1 子配额占满应被一级闸门拒绝")
	// 其他用户不受用户 1 子配额影响。
	mustAcquire(t, g, 2, 2, 10, 0, false, "用户 2 不受用户 1 子配额影响")
	// 用户 1 释放一个槽位后子配额恢复。
	g.Release(1, false)
	mustAcquire(t, g, 1, 2, 10, 0, false, "用户 1 释放后子配额应恢复")
	// userLimit <= 0 表示用户维度不限制。
	g2 := NewConcurrencyGate()
	for i := 0; i < 5; i++ {
		mustAcquire(t, g2, 1, 0, 10, 0, false, "userLimit=0 时不应受子配额限制")
	}
	// 子配额未满但全局占满时由二级保护拒绝。
	g3 := NewConcurrencyGate()
	mustAcquire(t, g3, 1, 5, 1, 0, false, "全局上限内应可占用")
	mustReject(t, g3, 2, 5, 1, 0, false, GateRejectGlobal,
		"子配额未满但全局占满应被二级保护拒绝")
}

// TestConcurrencyGateUserCountCleanup 用户计数归零后条目删除，防冷用户泄漏内存。
func TestConcurrencyGateUserCountCleanup(t *testing.T) {
	g := NewConcurrencyGate()
	mustAcquire(t, g, 7, 2, 10, 0, false, "占用应成功")
	g.Release(7, false)
	g.mu.Lock()
	_, exists := g.userCount[7]
	g.mu.Unlock()
	if exists {
		t.Error("计数归零后 userCount 条目应删除")
	}
}

// TestConcurrencyGateConcurrentSafety 并发安全：多个 goroutine 并发 Acquire/Release，
// 断言在途计数任意时刻不超过各层上限，全部结束后三层计数归零（配合 -race 运行）。
func TestConcurrencyGateConcurrentSafety(t *testing.T) {
	g := NewConcurrencyGate()
	const (
		userLimit  = 4
		limit      = 8
		largeLimit = 3
		workers    = 40
		iterations = 200
	)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			userID := int64(worker % 5)
			large := worker%3 == 0
			for j := 0; j < iterations; j++ {
				ok, _ := g.Acquire(userID, userLimit, limit, largeLimit, large)
				// 同包访问内部计数，持锁读取，校验任意时刻不越界。
				g.mu.Lock()
				if g.count > limit {
					t.Errorf("在途总数 %d 超过全局上限 %d", g.count, limit)
				}
				if g.largeCount > largeLimit {
					t.Errorf("大请求在途数 %d 超过大配额 %d", g.largeCount, largeLimit)
				}
				for uid, c := range g.userCount {
					if c > userLimit {
						t.Errorf("用户 %d 在途数 %d 超过用户子配额 %d", uid, c, userLimit)
					}
				}
				g.mu.Unlock()
				if ok {
					g.Release(userID, large)
				}
			}
		}(i)
	}
	wg.Wait()

	g.mu.Lock()
	defer g.mu.Unlock()
	if g.count != 0 {
		t.Errorf("全部释放后总计数应为 0，实际 %d", g.count)
	}
	if g.largeCount != 0 {
		t.Errorf("全部释放后大请求计数应为 0，实际 %d", g.largeCount)
	}
	if len(g.userCount) != 0 {
		t.Errorf("全部释放后 userCount 应为空，实际 %v", g.userCount)
	}
}
