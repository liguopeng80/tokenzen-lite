package relay

// 渠道健康跟踪：致命错误按渠道累计连续失败次数，达到阈值自动禁用；
// 自动禁用的渠道由定时半开探测恢复。计数为进程内存态，重启后清零。
//
// 高层编排（noteChannelSuccess/noteChannelFailure/StartRecoveryProbe/
// ProbeAutoDisabledChannels/probeChannel）已上移到 ChannelHealth 协作对象
// （channel_health.go，C2 设计 step 5）；本文件只保留计数器原语与其常量。

import (
	"sync"
	"time"
)

// channelProbeTimeout 单个渠道半开探测的请求超时。
const channelProbeTimeout = 30 * time.Second

// probeSettingRecheck 探测被设置关闭时，重新读取设置的间隔。
const probeSettingRecheck = time.Minute

// channelHealth 渠道连续失败计数（并发安全，进程内存态）。
type channelHealth struct {
	mu       sync.Mutex
	failures map[int64]int
}

// fail 记一次失败，返回当前连续失败次数。
func (h *channelHealth) fail(id int64) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.failures == nil {
		h.failures = make(map[int64]int)
	}
	h.failures[id]++
	return h.failures[id]
}

// reset 清零连续失败计数。
func (h *channelHealth) reset(id int64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.failures, id)
}
