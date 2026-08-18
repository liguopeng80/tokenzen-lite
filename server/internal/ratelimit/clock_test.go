package ratelimit

import (
	"sync"
	"time"
)

// fakeClock 进程内可控时钟，供 limiter/failure_lock 测试注入 Now 字段，
// 消除 time.Sleep。并发安全（advance 与 now 互斥）。
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{t: time.Now()}
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}
