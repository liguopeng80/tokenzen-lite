package ratelimit

import (
	"sync"
	"time"
)

// FailureLocker 连续失败锁定器（进程内存态）。
// 同一 key 连续失败达到阈值后进入锁定期，锁定期内即使凭证正确也拒绝；
// 成功一次即清零计数。用于登录接口的账号级防爆破。
type FailureLocker struct {
	mu        sync.Mutex
	entries   map[string]*failureEntry
	lastSweep time.Time
	// Now 可注入的时钟（测试用），零值走 time.Now。
	Now func() time.Time
}

type failureEntry struct {
	failures    int
	lastFailure time.Time
	lockedUntil time.Time
}

func NewFailureLocker() *FailureLocker {
	return &FailureLocker{entries: make(map[string]*failureEntry), lastSweep: time.Now()}
}

// now 取当前时间；注入了 Now 时优先使用，否则走 time.Now。
func (l *FailureLocker) now() time.Time {
	if l.Now != nil {
		return l.Now()
	}
	return time.Now()
}

// Locked 判断 key 当前是否处于锁定期。
func (l *FailureLocker) Locked(key string) bool {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	e, ok := l.entries[key]
	return ok && now.Before(e.lockedUntil)
}

// RecordFailure 记录一次失败。连续失败达到 threshold 时锁定 lockFor 时长并清零计数；
// 距上次失败超过 lockFor 的旧计数视为过期，重新从 1 计起。threshold <= 0 表示不锁定。
func (l *FailureLocker) RecordFailure(key string, threshold int, lockFor time.Duration) {
	if threshold <= 0 {
		return
	}
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()

	if now.Sub(l.lastSweep) > 10*time.Minute {
		l.sweep(now, lockFor)
	}

	e, ok := l.entries[key]
	if !ok || now.Sub(e.lastFailure) > lockFor {
		e = &failureEntry{}
		l.entries[key] = e
	}
	e.failures++
	e.lastFailure = now
	if e.failures >= threshold {
		e.lockedUntil = now.Add(lockFor)
		e.failures = 0
	}
}

// Reset 成功后清除 key 的失败记录（不解除已生效的锁定）。
func (l *FailureLocker) Reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	e, ok := l.entries[key]
	if !ok {
		return
	}
	if l.now().Before(e.lockedUntil) {
		e.failures = 0
		return
	}
	delete(l.entries, key)
}

// sweep 清理长期无活动且不在锁定期的 key（防止冷 key 泄漏内存）。
func (l *FailureLocker) sweep(now time.Time, lockFor time.Duration) {
	cutoff := now.Add(-lockFor * 2)
	for k, e := range l.entries {
		if e.lastFailure.Before(cutoff) && now.After(e.lockedUntil) {
			delete(l.entries, k)
		}
	}
	l.lastSweep = now
}
