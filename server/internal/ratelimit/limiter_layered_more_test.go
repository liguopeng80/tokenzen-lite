package ratelimit

import "testing"

// TestConcurrencyGateGlobalUnlimited 全局上限 <= 0 表示总槽位维度不限制。
func TestConcurrencyGateGlobalUnlimited(t *testing.T) {
	g := NewConcurrencyGate()
	for i := 0; i < 50; i++ {
		mustAcquire(t, g, 1, 0, 0, 0, false, "limit=0 时不应受全局上限限制")
	}
	// 负值同样按不限制处理。
	g2 := NewConcurrencyGate()
	for i := 0; i < 5; i++ {
		mustAcquire(t, g2, 1, 0, -1, 0, false, "limit<0 时不应受全局上限限制")
	}
}

// TestConcurrencyGateLargeCountsIntoUserQuota 大请求同时计入所属用户的子配额：
// 用户子配额被一个大请求占满后，同一用户的一般请求也被一级闸门拒绝。
func TestConcurrencyGateLargeCountsIntoUserQuota(t *testing.T) {
	g := NewConcurrencyGate()
	mustAcquire(t, g, 1, 1, 10, 5, true, "首个大请求应可占用")
	mustReject(t, g, 1, 1, 10, 5, false, GateRejectUserQuota,
		"大请求应计入用户子配额，同用户后续一般请求应被拒绝")
	mustAcquire(t, g, 2, 1, 10, 5, false, "其他用户不受影响")
	// 释放大请求后用户子配额恢复。
	g.Release(1, true)
	mustAcquire(t, g, 1, 1, 10, 5, false, "释放大请求后用户子配额应恢复")
}

// TestConcurrencyGateReleaseRestoresAllLayers 释放同时回收全局、用户、大请求三层计数。
func TestConcurrencyGateReleaseRestoresAllLayers(t *testing.T) {
	g := NewConcurrencyGate()
	mustAcquire(t, g, 9, 1, 1, 1, true, "占用应成功")
	mustReject(t, g, 9, 1, 1, 1, true, GateRejectUserQuota, "三层均满时应拒绝")
	g.Release(9, true)
	// 三层计数均已回收：同样参数可再次占用。
	mustAcquire(t, g, 9, 1, 1, 1, true, "释放后三层配额应全部恢复")
	g.Release(9, true)
	g.mu.Lock()
	count, largeCount, userEntries := g.count, g.largeCount, len(g.userCount)
	g.mu.Unlock()
	if count != 0 || largeCount != 0 || userEntries != 0 {
		t.Errorf("全部释放后计数应归零：count=%d largeCount=%d userCount条目=%d",
			count, largeCount, userEntries)
	}
}
