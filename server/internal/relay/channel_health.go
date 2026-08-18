package relay

// ChannelHealth 渠道健康跟踪与自动禁用/恢复（C2 设计 step 5）。
//
// 连续失败计数 + 自动禁用判定/写库 + 成功清零 + 半开探活。把原本散落在
// Engine 的 health 状态与 noteChannelSuccess/noteChannelFailure/StartRecoveryProbe/
// ProbeAutoDisabledChannels/probeChannel 五个方法收敛到一处；Engine 不再直接
// 持有 channelHealth 计数状态。
//
// 计数为进程内存态，重启后清零；单实例前提（多副本扩展需 Redis 化，见 affinityTable 注释）。

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/liguopeng80/tokenzen-lite/server/internal/alerting"
	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/obs"
	"github.com/liguopeng80/tokenzen-lite/server/internal/secrets"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// ChannelHealth 渠道健康跟踪器：持连续失败计数与自动禁用/探活所需依赖。
type ChannelHealth struct {
	counters channelHealth
	Channels *store.ChannelRepo
	Secrets  *secrets.Box
	Client   *http.Client
	Settings *store.SettingsRepo
	Alerts   alerting.Notifier
}

// NewChannelHealth 创建渠道健康跟踪器。
func NewChannelHealth(channels *store.ChannelRepo, box *secrets.Box,
	client *http.Client, settings *store.SettingsRepo, alerts alerting.Notifier) *ChannelHealth {
	return &ChannelHealth{
		Channels: channels, Secrets: box, Client: client,
		Settings: settings, Alerts: alerts,
	}
}

// NoteSuccess 渠道成功响应后清零其连续失败计数。
func (c *ChannelHealth) NoteSuccess(id int64) {
	obs.RecordChannelAttempt(id, obs.ChannelOutcomeSuccess)
	c.counters.reset(id)
}

// NoteFailure 记录一次上游错误；致命错误类（auth_fatal/quota_fatal）计入
// 连续失败计数，达到 channel_disable_failure_threshold 阈值时自动禁用渠道。
func (c *ChannelHealth) NoteFailure(ctx context.Context, ch *store.Channel,
	class domain.ErrorClass, status int) {

	// 全部上游错误都计入指标，包括不触发自动禁用的类别：
	// 自动禁用看的是致命错误，而渠道健康度看的是失败比例。
	obs.RecordChannelAttempt(ch.ID, obs.ChannelOutcomeFailure)
	if !CountsTowardAutoDisable(class) {
		return
	}
	count := c.counters.fail(ch.ID)
	threshold := c.Settings.GetInt64(ctx, "channel_disable_failure_threshold")
	if threshold <= 0 || int64(count) < threshold {
		obs.Logger(ctx).Warn("渠道致命错误，累计连续失败",
			"channel_id", ch.ID, "class", class, "status", status,
			"consecutive_failures", count, "threshold", threshold)
		return
	}
	reason := fmt.Sprintf("自动禁用：连续 %d 次致命错误（%s，HTTP %d）", count, class, status)
	// 脱离请求取消但带截止时间（见 background.go）：请求断连时禁用仍须落库，
	// 数据库变慢时不无限期占用连接。
	wctx, cancel := detachedWriteCtx(ctx)
	defer cancel()
	if err := c.Channels.AutoDisable(wctx, ch.ID, reason); err != nil {
		obs.Logger(ctx).Error("渠道自动禁用写库失败", "channel_id", ch.ID, "error", err)
		return
	}
	c.counters.reset(ch.ID)
	obs.Logger(ctx).Warn("渠道已自动禁用", "event", "channel_auto_disabled",
		"channel_id", ch.ID, "channel_name", ch.Name, "reason", reason)
	c.raiseAutoDisabledAlert(ctx, ch, reason)
}

// raiseAutoDisabledAlert 投递渠道自动禁用告警（AlertChannelAutoDisabled 恒为 Critical）。
func (c *ChannelHealth) raiseAutoDisabledAlert(ctx context.Context, ch *store.Channel, reason string) {
	if c.Alerts == nil {
		return
	}
	c.Alerts.Raise(ctx, alerting.Event{
		Type: domain.AlertChannelAutoDisabled, Severity: domain.AlertCritical,
		DedupKey: fmt.Sprintf("channel_auto_disabled:%d", ch.ID),
		Title:    "渠道已自动禁用：" + ch.Name,
		Message: fmt.Sprintf("渠道「%s」（ID %d）%s。该渠道已退出路由，落在其上的模型将由其余渠道承接；"+
			"自动禁用的渠道会按探测间隔半开探活，恢复后自动重新参与路由。",
			ch.Name, ch.ID, reason),
	})
}

// StartRecoveryProbe 启动自动禁用渠道的定时半开探测循环，ctx 取消时退出。
// 探测间隔由 channel_probe_interval_sec 控制（0 = 关闭，定期复查设置）。
// 每一轮用 obs.runSafe 包裹：探测 panic 只记日志，不会杀死探测循环。
func (c *ChannelHealth) StartRecoveryProbe(ctx context.Context) {
	go func() {
		for {
			interval := time.Duration(c.Settings.GetInt64(ctx, "channel_probe_interval_sec")) * time.Second
			wait := interval
			if wait <= 0 {
				wait = probeSettingRecheck
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(wait):
			}
			if interval > 0 {
				obs.RunSafe("channel_recovery_probe", func() { c.ProbeAutoDisabledChannels(ctx) })
			}
		}
	}()
}

// ProbeAutoDisabledChannels 对全部自动禁用渠道各做一次半开探测。
func (c *ChannelHealth) ProbeAutoDisabledChannels(ctx context.Context) {
	channels, _, err := c.Channels.List(ctx, store.ChannelListFilter{
		Status: domain.ChannelAutoDisabled,
	})
	if err != nil {
		obs.Logger(ctx).Error("查询自动禁用渠道失败，跳过本轮探测", "error", err)
		return
	}
	for i := range channels {
		select {
		case <-ctx.Done():
			return
		default:
		}
		c.probeChannel(ctx, &channels[i])
	}
}

// probeChannel 探测单个自动禁用渠道；探活成功则恢复为 enabled 并记录事件日志。
func (c *ChannelHealth) probeChannel(ctx context.Context, ch *store.Channel) {
	logger := obs.Logger(ctx)
	apiKey, err := c.Secrets.Decrypt(ch.APIKeyEncrypted)
	if err != nil {
		logger.Error("半开探测：渠道密钥解密失败", "channel_id", ch.ID, "error", err)
		return
	}
	probeCtx, cancel := context.WithTimeout(ctx, channelProbeTimeout)
	defer cancel()
	start := time.Now()
	latency, testErr := TestChannel(probeCtx, c.Client, ch, apiKey, "")
	logger.Info("半开探测完成", "channel_id", ch.ID,
		"duration_ms", time.Since(start).Milliseconds(), "ok", testErr == nil)

	fields := map[string]any{"last_test_at": time.Now(), "last_test_latency_ms": latency}
	if testErr != nil {
		_ = c.Channels.UpdateFields(context.WithoutCancel(ctx), ch.ID, fields)
		logger.Info("渠道半开探测未通过，维持禁用",
			"channel_id", ch.ID, "channel_name", ch.Name, "error", testErr)
		return
	}
	fields["status"] = domain.ChannelEnabled
	fields["disabled_reason"] = ""
	if err := c.Channels.UpdateFields(context.WithoutCancel(ctx), ch.ID, fields); err != nil {
		logger.Error("渠道探活恢复写库失败", "channel_id", ch.ID, "error", err)
		return
	}
	c.counters.reset(ch.ID)
	logger.Warn("渠道探活成功，自动恢复启用", "event", "channel_auto_recovered",
		"channel_id", ch.ID, "channel_name", ch.Name,
		"latency_ms", latency, "previous_reason", ch.DisabledReason)
}
