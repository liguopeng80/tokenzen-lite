// Package ratelimit 提供进程内滑动窗口限流。
// 单机部署用内存实现；Limiter 接口保留将来替换为 Redis 实现的扩展点。
package ratelimit

import (
	"sync"
	"time"
)

// Limiter 限流器接口。
type Limiter interface {
	// Allow 判断 key 在窗口内是否还允许一次请求。
	Allow(key string, limit int, window time.Duration) bool
}

// MemoryLimiter 滑动窗口内存限流器。
type MemoryLimiter struct {
	mu      sync.Mutex
	buckets map[string][]time.Time
	// lastSweep 上次全量清扫时间（防止冷 key 泄漏内存）
	lastSweep time.Time
	// Now 可注入的时钟（测试用），零值走 time.Now。
	Now func() time.Time
}

func NewMemoryLimiter() *MemoryLimiter {
	return &MemoryLimiter{buckets: make(map[string][]time.Time), lastSweep: time.Now()}
}

// now 取当前时间；注入了 Now 时优先使用，否则走 time.Now。
func (l *MemoryLimiter) now() time.Time {
	if l.Now != nil {
		return l.Now()
	}
	return time.Now()
}

// Allow 滑动窗口判定；limit <= 0 表示不限流。
func (l *MemoryLimiter) Allow(key string, limit int, window time.Duration) bool {
	if limit <= 0 {
		return true
	}
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()

	if now.Sub(l.lastSweep) > 5*time.Minute {
		l.sweep(now, window)
	}

	cutoff := now.Add(-window)
	times := l.buckets[key]
	// 丢弃窗口外的记录
	idx := 0
	for idx < len(times) && times[idx].Before(cutoff) {
		idx++
	}
	times = times[idx:]
	if len(times) >= limit {
		l.buckets[key] = times
		return false
	}
	l.buckets[key] = append(times, now)
	return true
}

// sweep 清理长期无活动的 key。
func (l *MemoryLimiter) sweep(now time.Time, window time.Duration) {
	cutoff := now.Add(-window * 2)
	for k, times := range l.buckets {
		if len(times) == 0 || times[len(times)-1].Before(cutoff) {
			delete(l.buckets, k)
		}
	}
	l.lastSweep = now
}

// 并发闸门拒绝原因，写入拒绝日志用于定位命中的闸门层。
const (
	// GateRejectUserQuota 单用户并发子配额已满。
	GateRejectUserQuota = "user_quota"
	// GateRejectGlobal 全局并发总上限已满（二级保护）。
	GateRejectGlobal = "global_limit"
	// GateRejectLargeQuota 大请求独立配额已满。
	GateRejectLargeQuota = "large_quota"
)

// ConcurrencyGate /v1 分层并发准入闸门（内存保护）。
// 三层约束：单用户子配额（一级，防单一用户占满全站槽位）→
// 全局总槽位（二级保护，约束全部在途请求）→
// 大请求配额（请求体超过阈值，判定在调用方；额外占用独立小配额，
// 防止少量大请求体在解码与转发期间耗尽进程内存）。
// 各上限与内存预算的换算依据见 docs/deployment.md。
type ConcurrencyGate struct {
	mu         sync.Mutex
	count      int
	largeCount int
	// userCount 各用户的在途请求数；归零即删除，防冷用户泄漏内存。
	userCount map[int64]int
}

func NewConcurrencyGate() *ConcurrencyGate {
	return &ConcurrencyGate{userCount: make(map[int64]int)}
}

// Acquire 尝试占用槽位：全部请求占用用户子配额与总槽位，
// large 请求同时占用大请求配额。userLimit / limit / largeLimit <= 0
// 表示对应维度不限制。拒绝时 reason 为 GateReject* 常量之一。
func (g *ConcurrencyGate) Acquire(userID int64, userLimit, limit, largeLimit int, large bool) (ok bool, reason string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if userLimit > 0 && g.userCount[userID] >= userLimit {
		return false, GateRejectUserQuota
	}
	if limit > 0 && g.count >= limit {
		return false, GateRejectGlobal
	}
	if large && largeLimit > 0 && g.largeCount >= largeLimit {
		return false, GateRejectLargeQuota
	}
	g.count++
	g.userCount[userID]++
	if large {
		g.largeCount++
	}
	return true, ""
}

// InFlight 返回当前在途请求数。指标导出用：全局并发上限是最先撞到的容量约束，
// 撞上时请求直接 503 且不排队，因此「离上限还有多远」必须可观测。
func (g *ConcurrencyGate) InFlight() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.count
}

// Release 释放槽位；userID 与 large 必须与 Acquire 时的取值一致。
func (g *ConcurrencyGate) Release(userID int64, large bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.count > 0 {
		g.count--
	}
	if g.userCount[userID] > 0 {
		g.userCount[userID]--
		if g.userCount[userID] == 0 {
			delete(g.userCount, userID)
		}
	}
	if large && g.largeCount > 0 {
		g.largeCount--
	}
}
