package relay

// 后台写入封装（background.go）的单元测试：
// 上下文封装、有界队列常规写出、队列满丢弃计数、停机刷盘、Close 后入队、单条写入超时。
// 对应架构评审报告 P3-2 的测试计划。

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/liguopeng80/tokenzen-lite/server/internal/obs"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// fakeSink 可注入行为的用量日志写入目标。
type fakeSink struct {
	mu      sync.Mutex
	entries []*store.UsageLog
	// beforeWrite 非 nil 时在每次 Create 前调用（可用于阻塞工作协程）。
	beforeWrite func(ctx context.Context, l *store.UsageLog) error
}

func (f *fakeSink) Create(ctx context.Context, l *store.UsageLog) error {
	if f.beforeWrite != nil {
		if err := f.beforeWrite(ctx, l); err != nil {
			return err
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.entries = append(f.entries, l)
	return nil
}

func (f *fakeSink) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.entries)
}

func (f *fakeSink) requestIDs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	ids := make([]string, 0, len(f.entries))
	for _, e := range f.entries {
		ids = append(ids, e.RequestID)
	}
	return ids
}

// waitFor 轮询条件成立，超时则失败。
func waitFor(t *testing.T, timeout time.Duration, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("等待条件超时: %s", msg)
}

// 【单元】上下文封装：取消父上下文后，封装上下文不受影响，
// 截止时间落在 5-10 秒区间，request_id 随值传递不丢失。
func TestDetachedWriteCtx(t *testing.T) {
	parent, cancelParent := context.WithCancel(context.Background())
	parent = obs.WithRequestID(parent, "req-detach-1")

	begin := time.Now()
	wctx, cancel := detachedWriteCtx(parent)
	defer cancel()

	cancelParent()
	// 给取消传播留出时间
	time.Sleep(10 * time.Millisecond)

	if err := wctx.Err(); err != nil {
		t.Fatalf("父上下文取消后封装上下文 Err() 应为 nil，实际: %v", err)
	}
	dl, ok := wctx.Deadline()
	if !ok {
		t.Fatal("封装上下文应有截止时间")
	}
	remain := dl.Sub(begin)
	if remain < 5*time.Second || remain > 10*time.Second {
		t.Fatalf("截止时间应在 5-10 秒区间，实际约 %v", remain)
	}
	if got := obs.RequestID(wctx); got != "req-detach-1" {
		t.Fatalf("request_id 应随值传递，期望 req-detach-1，实际 %q", got)
	}
}

// 【单元】日志队列常规写出：入队 N 条全部按序写出，丢弃计数为 0。
func TestUsageLogWriterWritesAllInOrder(t *testing.T) {
	sink := &fakeSink{}
	w := newUsageLogWriterSized(sink, 64, time.Second)

	const n = 20
	ctx := context.Background()
	for i := 0; i < n; i++ {
		w.enqueue(ctx, &store.UsageLog{RequestID: fmt.Sprintf("req-%03d", i)})
	}
	waitFor(t, 3*time.Second, func() bool { return sink.count() == n }, "全部写出")

	ids := sink.requestIDs()
	for i, id := range ids {
		if want := fmt.Sprintf("req-%03d", i); id != want {
			t.Fatalf("第 %d 条乱序：期望 %s，实际 %s", i, want, id)
		}
	}
	if got := w.dropped.Load(); got != 0 {
		t.Fatalf("丢弃计数应为 0，实际 %d", got)
	}
}

// 【单元】队列满丢弃与计数告警：工作协程被阻塞时，超出容量的入队立即返回并计数。
func TestUsageLogWriterDropsWhenFull(t *testing.T) {
	gate := make(chan struct{})
	started := make(chan struct{}, 1)
	sink := &fakeSink{beforeWrite: func(ctx context.Context, l *store.UsageLog) error {
		select {
		case started <- struct{}{}:
		default:
		}
		<-gate // 阻塞工作协程（不理会 ctx，模拟卡死的写入）
		return nil
	}}

	const capacity = 4
	const extra = 3
	w := newUsageLogWriterSized(sink, capacity, time.Second)
	ctx := context.Background()

	// 第一条被工作协程取走并卡在写入中
	w.enqueue(ctx, &store.UsageLog{RequestID: "inflight"})
	<-started
	// 填满队列
	for i := 0; i < capacity; i++ {
		w.enqueue(ctx, &store.UsageLog{RequestID: fmt.Sprintf("fill-%d", i)})
	}
	// 超出容量的入队必须立即返回并被丢弃
	begin := time.Now()
	for i := 0; i < extra; i++ {
		w.enqueue(ctx, &store.UsageLog{RequestID: fmt.Sprintf("over-%d", i)})
	}
	if elapsed := time.Since(begin); elapsed > 500*time.Millisecond {
		t.Fatalf("队列满时入队应立即返回，实际耗时 %v", elapsed)
	}
	if got := w.dropped.Load(); got != extra {
		t.Fatalf("丢弃计数应为 %d，实际 %d", extra, got)
	}

	// 放行后，未被丢弃的记录全部写出，丢弃计数不再增长
	close(gate)
	waitFor(t, 3*time.Second, func() bool { return sink.count() == capacity+1 }, "放行后写出全部未丢弃记录")
	if got := w.dropped.Load(); got != extra {
		t.Fatalf("放行后丢弃计数应保持 %d，实际 %d", extra, got)
	}
}

// 【单元】停机刷盘：Close 返回前队列中的记录全部写出。
func TestUsageLogWriterCloseFlushesAll(t *testing.T) {
	sink := &fakeSink{}
	w := newUsageLogWriterSized(sink, 64, time.Second)
	ctx := context.Background()
	const n = 10
	for i := 0; i < n; i++ {
		w.enqueue(ctx, &store.UsageLog{RequestID: fmt.Sprintf("flush-%d", i)})
	}
	closeCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	w.close(closeCtx)
	if got := sink.count(); got != n {
		t.Fatalf("Close 返回前应写出全部 %d 条，实际 %d", n, got)
	}
}

// 【单元】停机刷盘超时：写入永久阻塞时 Close 在截止时间内返回，不永久悬挂。
func TestUsageLogWriterCloseReturnsOnDeadline(t *testing.T) {
	gate := make(chan struct{})
	defer close(gate)
	sink := &fakeSink{beforeWrite: func(ctx context.Context, l *store.UsageLog) error {
		<-gate
		return nil
	}}
	w := newUsageLogWriterSized(sink, 8, time.Minute)
	w.enqueue(context.Background(), &store.UsageLog{RequestID: "stuck"})

	closeCtx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	doneCh := make(chan struct{})
	go func() {
		w.close(closeCtx)
		close(doneCh)
	}()
	select {
	case <-doneCh:
	case <-time.After(2 * time.Second):
		t.Fatal("写入永久阻塞时 Close 应在截止时间内返回，实际悬挂")
	}
}

// 【单元】Close 后入队：关闭后继续入队不得 panic，按丢弃计数处理
// （覆盖停机与在途请求的竞态）。
func TestUsageLogWriterEnqueueAfterClose(t *testing.T) {
	sink := &fakeSink{}
	w := newUsageLogWriterSized(sink, 8, time.Second)
	closeCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	w.close(closeCtx)

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Close 后入队不得 panic，实际 panic: %v", r)
		}
	}()
	w.enqueue(context.Background(), &store.UsageLog{RequestID: "after-close"})
	if got := w.dropped.Load(); got != 1 {
		t.Fatalf("Close 后入队应按丢弃计数处理，期望丢弃 1，实际 %d", got)
	}
}

// 【单元】单条写入超时不阻塞队列：某条写入超过截止时间按失败记录，
// 工作协程继续处理后续记录。
func TestUsageLogWriterSlowEntryDoesNotBlockQueue(t *testing.T) {
	sink := &fakeSink{beforeWrite: func(ctx context.Context, l *store.UsageLog) error {
		if l.RequestID == "slow" {
			<-ctx.Done() // 模拟慢写入：等到单条截止时间到期
			return ctx.Err()
		}
		return nil
	}}
	w := newUsageLogWriterSized(sink, 8, 50*time.Millisecond)
	ctx := context.Background()
	w.enqueue(ctx, &store.UsageLog{RequestID: "slow"})
	w.enqueue(ctx, &store.UsageLog{RequestID: "next"})

	waitFor(t, 3*time.Second, func() bool {
		for _, id := range sink.requestIDs() {
			if id == "next" {
				return true
			}
		}
		return false
	}, "慢记录超时后工作协程继续处理下一条")
	if got := w.dropped.Load(); got != 0 {
		t.Fatalf("慢写入不应计入丢弃，丢弃计数应为 0，实际 %d", got)
	}
}
