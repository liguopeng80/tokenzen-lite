package relay

// 后台写入统一封装（架构评审报告 P3-2）。
// 结算、退款与用量日志的写入刻意脱离请求取消——客户端断连时计费仍须完成；
// 但必须带截止时间：数据库锁等待或磁盘变慢时，无截止时间的后台写入会无限期
// 占用连接池，把计费变慢升级为全站不可用。超时的写入按失败记录日志，
// 由 tzl reconcile 与孤儿预扣清理补偿。

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/liguopeng80/tokenzen-lite/server/internal/obs"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// BackgroundWriteTimeout 后台写入截止时间（评审建议 5-10 秒区间，取 8 秒）。
// 导出作为结算写入窗口的唯一事实源，供启动守卫（cmd/tzl）扣除该窗口后
// 再比较上游超时与孤儿预扣判定阈值，避免「上游恰在阈值前完成、结算仍在
// 写入窗口内时被孤儿清理先退款」的套利配置。
const BackgroundWriteTimeout = 8 * time.Second

// detachedWriteCtx 返回脱离请求取消、但带截止时间的后台写入上下文。
// 保留原上下文中的值（request_id 等日志字段）。调用方必须调用返回的 cancel。
func detachedWriteCtx(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), BackgroundWriteTimeout)
}

// usageLogQueueSize 用量日志有界队列容量。
const usageLogQueueSize = 4096

// dropWarnInterval 队列满丢弃告警的最小间隔：丢弃逐条计数，
// 告警限频汇总输出，避免高负载下日志洪泛。
const dropWarnInterval = time.Minute

// usageLogSink 用量日志写入目标的最小接口（供单元测试注入 fake）。
type usageLogSink interface {
	Create(ctx context.Context, l *store.UsageLog) error
}

// usageLogWriter 用量日志异步写入器：有界队列 + 单工作协程。
// 替代每请求一个裸协程的写法，突发流量下写入并发不再放大；
// 队列满时丢弃并计数告警（用量日志可丢，计费流水不经此路径，
// 计费终态标记也不依赖用量日志——见 billing 包孤儿预扣清理）。
type usageLogWriter struct {
	repo  usageLogSink
	queue chan *store.UsageLog
	// stopCh 关闭时通知工作协程排空队列后退出。
	// 刻意不关闭 queue channel：停机窗口内在途请求仍可能入队，
	// 向已关闭 channel 发送会 panic，改用 closed 标志走丢弃计数分支。
	stopCh       chan struct{}
	done         chan struct{}
	closed       atomic.Bool
	dropped      atomic.Int64
	lastDropWarn atomic.Int64 // 上次丢弃告警时间（UnixNano），限频用
	writeTimeout time.Duration
}

// newUsageLogWriter 创建写入器并启动工作协程（生产参数）。
func newUsageLogWriter(repo *store.UsageLogRepo) *usageLogWriter {
	return newUsageLogWriterSized(repo, usageLogQueueSize, BackgroundWriteTimeout)
}

// newUsageLogWriterSized 按指定容量与单条写入截止时间创建写入器（测试可缩短）。
func newUsageLogWriterSized(repo usageLogSink, capacity int, writeTimeout time.Duration) *usageLogWriter {
	w := &usageLogWriter{
		repo:         repo,
		queue:        make(chan *store.UsageLog, capacity),
		stopCh:       make(chan struct{}),
		done:         make(chan struct{}),
		writeTimeout: writeTimeout,
	}
	go w.run()
	return w
}

// run 单工作协程：逐条写入，每条独立截止时间；收到停止信号后排空队列再退出。
func (w *usageLogWriter) run() {
	defer close(w.done)
	for {
		select {
		case entry := <-w.queue:
			w.write(entry)
		case <-w.stopCh:
			// 停机排空：写完队列中已入队的记录后退出。
			for {
				select {
				case entry := <-w.queue:
					w.write(entry)
				default:
					return
				}
			}
		}
	}
}

// write 写入单条记录，独立截止时间，失败仅记日志（用量日志可丢）。
func (w *usageLogWriter) write(entry *store.UsageLog) {
	ctx, cancel := context.WithTimeout(context.Background(), w.writeTimeout)
	defer cancel()
	if err := w.repo.Create(ctx, entry); err != nil {
		obs.Logger(ctx).Error("用量日志写入失败",
			"error", err, "request_id", entry.RequestID)
	}
}

// enqueue 非阻塞入队；队列满或写入器已关闭时丢弃本条并计数，
// 告警按 dropWarnInterval 限频汇总，不阻塞请求路径。
func (w *usageLogWriter) enqueue(ctx context.Context, entry *store.UsageLog) {
	if w.closed.Load() {
		w.noteDrop(ctx, entry, "写入器已关闭")
		return
	}
	select {
	case w.queue <- entry:
	default:
		w.noteDrop(ctx, entry, "队列已满")
	}
}

// noteDrop 丢弃计数 +1，并按限频输出汇总告警。
func (w *usageLogWriter) noteDrop(ctx context.Context, entry *store.UsageLog, reason string) {
	n := w.dropped.Add(1)
	now := time.Now().UnixNano()
	last := w.lastDropWarn.Load()
	if now-last >= int64(dropWarnInterval) && w.lastDropWarn.CompareAndSwap(last, now) {
		obs.Logger(ctx).Warn("用量日志被丢弃（限频汇总告警）",
			"reason", reason, "request_id", entry.RequestID, "dropped_total", n)
	}
}

// droppedCount 返回累计丢弃条数（健康接口暴露）。
func (w *usageLogWriter) droppedCount() int64 {
	return w.dropped.Load()
}

// close 停止接收并等待队列刷盘；ctx 到期则放弃等待并记录剩余条数。
// 幂等：重复调用只生效一次。关闭后的 enqueue 走丢弃计数分支，不会 panic。
func (w *usageLogWriter) close(ctx context.Context) {
	if !w.closed.CompareAndSwap(false, true) {
		return
	}
	close(w.stopCh)
	select {
	case <-w.done:
	case <-ctx.Done():
		obs.Logger(ctx).Warn("用量日志刷盘超时，剩余记录未写入",
			"remaining", len(w.queue))
	}
}
