// Package alerting 把系统异常投递给管理员。事件先落库再投递：
// 落库使「没收到告警」时能区分「未触发」与「触发了但发不出去」。
// 通道与格式取值见 docs/glossary.md，设计依据见 docs/design/组织与审计模型.md。
package alerting

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"gorm.io/datatypes"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/obs"
	"github.com/liguopeng80/tokenzen-lite/server/internal/secrets"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
	"github.com/liguopeng80/tokenzen-lite/server/internal/strutil"
)

// deliverTimeout 单次投递尝试的耗时上限。告警投递卡住不能拖住业务路径，
// 且每次重试独立计时——一次卡住不应耗光后续重试的预算。
const deliverTimeout = 15 * time.Second

// retryBackoffs 是后台投递失败后的退避节奏：retryBackoffs[i] 在第 i+1 次
// 失败之后、第 i+2 次尝试之前等待。总尝试次数 = 1 + len(retryBackoffs)。
// 量级 2s/8s/30s，指数递增，覆盖典型瞬时故障（网络抖动、接收端限流）的恢复窗口。
// 同步投递路径（RaiseSync）不重试，不受此约束。
var retryBackoffs = []time.Duration{
	2 * time.Second,
	8 * time.Second,
	30 * time.Second,
}

// nextRetry 返回第 attempt 次失败后的下一次重试退避。返回 ok=false 表示
// attempt 已是允许的最后一次尝试，没有后续重试。
func nextRetry(attempt int) (time.Duration, bool) {
	if attempt < 1 || attempt > len(retryBackoffs) {
		return 0, false
	}
	return retryBackoffs[attempt-1], true
}

// Event 是一次待投递的告警。
type Event struct {
	Type     domain.AlertType
	Severity domain.AlertSeverity
	// DedupKey 为空表示不参与抑制，每次都投递（通道测试）。
	DedupKey string
	Title    string
	Message  string
	Payload  map[string]any
	// SuppressFor 覆盖本条事件的抑制窗口。为 0 时按系统设置的全局窗口。
	// 面向用户本人的通知需要比运维告警长得多的窗口：管理员愿意每小时被提醒一次
	// 「有人余额不足」，员工不愿意每小时收到一封同样内容的邮件。
	SuppressFor time.Duration
	// EmailTo 非空表示这是发给特定收件人的定向通知：只走邮件通道，
	// 收件人取本字段而非系统配置的管理员地址，且不投 Webhook——
	// 群通道的读者是管理员，把某个员工的余额发进群里既无用也多余。
	EmailTo []string
}

// Notifier 是告警投递入口。业务模块依赖该接口而非具体实现，
// 便于在没有配置告警通道的部署与测试中传入 nil。
type Notifier interface {
	// Raise 异步投递一条告警，不阻塞调用方。
	Raise(ctx context.Context, ev Event)
	// EmailReady 报告邮件通道是否具备投递条件。定向通知（EmailTo 非空）
	// 只能走邮件，调用方据此决定要不要产生这类事件——通道未配置时逐个用户
	// 落一条投递失败记录，只会把告警列表刷满而不解决任何问题。
	EmailReady(ctx context.Context) bool
}

// Service 落库并投递告警。
type Service struct {
	Events   *store.AlertEventRepo
	Settings *store.SettingsRepo
	Secrets  *secrets.Box
	// HTTPClient 供测试注入；为 nil 时使用带超时的默认客户端。
	HTTPClient *http.Client
	// SendMail 供测试注入邮件发送；为 nil 时走真实 SMTP。
	SendMail func(cfg SMTPConfig, subject, body string) error
}

// Raise 异步投递告警。调用方所在的请求上下文可能随请求结束被取消，
// 因此投递使用脱离的上下文。后台协程经 obs.RunSafe 包裹，单条告警的
// 投递 panic 不会向上抛；通道失败时按指数退避重试，耗尽转死信。
// 不阻塞调用方。
func (s *Service) Raise(ctx context.Context, ev Event) {
	if s == nil || s.Events == nil {
		return
	}
	requestID := obs.RequestID(ctx)
	go func() {
		obs.RunSafe("alerting.deliver", func() {
			bg := obs.WithRequestID(context.Background(), requestID)
			record, err := s.createEvent(bg, ev)
			if err != nil {
				obs.Logger(bg).Error("告警事件落库失败", "alert_type", ev.Type, "error", err)
				return
			}
			if record.Status == domain.AlertSuppressed {
				return
			}
			deliver := func(attemptCtx context.Context) (sent, failures []string, attempted bool) {
				cfg := s.LoadConfig(attemptCtx)
				return s.attemptChannels(attemptCtx, cfg, record, ev.EmailTo)
			}
			deliverWithRetry(bg, record, deliver, s.persistOutcome, defaultSleeper)
		})
	}()
}

// RaiseSync 同步落库并投递一次，返回落库后的事件。投递失败时事件仍已落库，
// 错误一并返回供调用方记录。不重试——供管理端通道测试与运维 CLI 使用：
// 这些场景需要即时拿到单次投递结果，退避等待会让请求长时间挂起。
func (s *Service) RaiseSync(ctx context.Context, ev Event) (*store.AlertEvent, error) {
	if s == nil || s.Events == nil {
		return nil, nil
	}
	record, err := s.createEvent(ctx, ev)
	if err != nil {
		return nil, err
	}
	if record.Status == domain.AlertSuppressed {
		return record, nil
	}
	cfg := s.LoadConfig(ctx)
	sent, failures, _ := s.attemptChannels(ctx, cfg, record, ev.EmailTo)
	return record, s.finishDelivery(ctx, record, sent, failures)
}

// createEvent 按抑制策略落库一条待投递事件，返回该记录。命中抑制窗口时
// 落库为 suppressed 并返回（不投递）。落库失败时返回 (nil, error)。
func (s *Service) createEvent(ctx context.Context, ev Event) (*store.AlertEvent, error) {
	if ev.Severity == "" {
		ev.Severity = domain.AlertWarning
	}
	record := &store.AlertEvent{
		AlertType: ev.Type,
		Severity:  ev.Severity,
		DedupKey:  ev.DedupKey,
		Title:     ev.Title,
		Message:   ev.Message,
		Payload:   encodePayload(ev.Payload),
		Status:    domain.AlertPending,
	}

	window := s.dedupWindow(ctx)
	if ev.SuppressFor > 0 {
		window = ev.SuppressFor
	}
	if window > 0 && ev.DedupKey != "" {
		recent, err := s.Events.RecentlyDelivered(ctx, ev.DedupKey, time.Now().Add(-window))
		if err != nil {
			obs.Logger(ctx).Error("查询告警抑制窗口失败", "dedup_key", ev.DedupKey, "error", err)
		} else if recent {
			record.Status = domain.AlertSuppressed
			return record, s.Events.Create(ctx, record)
		}
	}

	if err := s.Events.Create(ctx, record); err != nil {
		return nil, fmt.Errorf("告警事件落库失败: %w", err)
	}
	return record, nil
}

// EmailReady 报告邮件通道是否具备投递条件。
func (s *Service) EmailReady(ctx context.Context) bool {
	if s == nil || s.Settings == nil {
		return false
	}
	return s.LoadConfig(ctx).SMTP.Configured()
}

// attemptChannels 向全部已配置通道做一次投递尝试，任一成功即视为本次送达。
// 返回成功通道名、失败原因，以及 attempted（是否真的尝试过任一通道——
// 未配置任何通道时为 false，调用方据此跳过重试：配置不会自行修复，重试无意义）。
// emailTo 非空时按定向通知处理：只发邮件，收件人改为指定地址。
// 本方法不写库，状态回写交给调用方。
func (s *Service) attemptChannels(ctx context.Context, cfg Config, record *store.AlertEvent,
	emailTo []string) (sent, failures []string, attempted bool) {

	if len(emailTo) > 0 {
		cfg.SMTP.To = emailTo
		if !cfg.SMTP.Configured() {
			return nil, []string{"email: 邮件通道未配置"}, false
		}
		if err := s.sendEmail(ctx, cfg, record); err != nil {
			return nil, []string{"email: " + err.Error()}, true
		}
		return []string{"email"}, nil, true
	}

	if cfg.WebhookURL != "" {
		attempted = true
		if err := s.sendWebhook(ctx, cfg, record); err != nil {
			failures = append(failures, "webhook: "+err.Error())
		} else {
			sent = append(sent, "webhook")
		}
	}
	if cfg.SMTP.Configured() {
		attempted = true
		if err := s.sendEmail(ctx, cfg, record); err != nil {
			failures = append(failures, "email: "+err.Error())
		} else {
			sent = append(sent, "email")
		}
	}
	return sent, failures, attempted
}

// deliverWithRetry 是后台投递的指数退避重试骨架，与具体通道和存储解耦：
// deliver 执行一次尝试、recordOutcome 回写本次结果、sleep 实现退避等待。
// 这样抽取是为了在不依赖数据库、不真睡眠的前提下单测重试与死信决策。
//
// 行为：
//   - 任一次尝试有通道成功 → 记 sent 后返回。
//   - 本次未尝试任何通道（未配置）→ 记 failed 后返回，不重试。
//   - 本次尝试了但全部失败 → 记 failed（中间态），记 WARNING，按 nextRetry 退避后重试；
//     退避期间 ctx 取消则停止；没有下一次重试时记 dead_letter 并记 ERROR。
func deliverWithRetry(
	ctx context.Context,
	record *store.AlertEvent,
	deliver func(context.Context) (sent, failures []string, attempted bool),
	recordOutcome func(ctx context.Context, rec *store.AlertEvent, attempt int, sent, failures []string, status domain.AlertStatus),
	sleep func(ctx context.Context, d time.Duration) bool,
) {
	for attempt := 1; ; attempt++ {
		attemptCtx, cancel := context.WithTimeout(ctx, deliverTimeout)
		sent, failures, attempted := deliver(attemptCtx)
		cancel()

		switch {
		case len(sent) > 0:
			recordOutcome(ctx, record, attempt, sent, failures, domain.AlertSent)
			return
		case !attempted:
			// 未配置任何通道：事件已落库，但管理员不会收到通知；重试无意义。
			recordOutcome(ctx, record, attempt, nil, failures, domain.AlertFailed)
			obs.Logger(ctx).Warn("告警事件已记录但未配置告警通道，管理员不会收到通知",
				"alert_id", record.ID, "alert_type", record.AlertType, "title", record.Title)
			return
		}

		// 通道全部失败：记 WARNING，含告警 ID、尝试次数与失败原因，便于定位。
		obs.Logger(ctx).Warn("告警投递失败，准备重试或转死信",
			"alert_id", record.ID, "attempt", attempt,
			"channels_error", strings.Join(failures, "; "))

		backoff, retry := nextRetry(attempt)
		if !retry {
			recordOutcome(ctx, record, attempt, nil, failures, domain.AlertDeadLetter)
			obs.Logger(ctx).Error("告警投递重试耗尽，转入死信，需管理员介入",
				"alert_id", record.ID, "attempts", attempt,
				"last_error", strutil.Truncate(strings.Join(failures, "; "), 500))
			return
		}
		// 中间态失败先回写 failed + 当前尝试次数，便于管理员在重试期间即看到失败状态。
		recordOutcome(ctx, record, attempt, nil, failures, domain.AlertFailed)
		if !sleep(ctx, backoff) {
			// 退避期间上下文取消（如进程停机）：保持最近一次 failed 状态，不再重试。
			return
		}
	}
}

// persistOutcome 把一次投递尝试的结果回写到 alert_events，并同步内存记录。
// 供后台重试路径使用；同步投递路径仍走 finishDelivery（保留「全部失败」错误返回语义）。
func (s *Service) persistOutcome(ctx context.Context, record *store.AlertEvent,
	attempt int, sent, failures []string, status domain.AlertStatus) {

	lastErr := strutil.Truncate(strings.Join(failures, "; "), 500)
	fields := map[string]any{
		"attempts":   attempt,
		"status":     status,
		"last_error": lastErr,
	}
	record.Attempts = attempt
	record.Status = status
	record.LastError = lastErr
	if status == domain.AlertSent {
		now := time.Now()
		fields["sent_at"] = now
		fields["channels_sent"] = encodePayload(map[string]any{"channels": sent})
		record.SentAt = &now
	}
	if err := s.Events.UpdateFields(ctx, record.ID, fields); err != nil {
		obs.Logger(ctx).Error("回写告警投递结果失败", "alert_id", record.ID, "error", err)
	}
}

// defaultSleeper 在 ctx 取消时提前返回 false，否则等待 d 后返回 true。
func defaultSleeper(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// finishDelivery 按各通道的投递结果回写事件状态。供同步投递路径（RaiseSync）使用。
func (s *Service) finishDelivery(ctx context.Context, record *store.AlertEvent,
	sent, failures []string) error {

	fields := map[string]any{"attempts": record.Attempts + 1}
	var result error
	switch {
	case len(sent) > 0:
		now := time.Now()
		fields["status"] = domain.AlertSent
		fields["sent_at"] = now
		fields["channels_sent"] = encodePayload(map[string]any{"channels": sent})
		fields["last_error"] = strings.Join(failures, "; ")
		record.Status = domain.AlertSent
	case len(failures) > 0:
		fields["status"] = domain.AlertFailed
		fields["last_error"] = strutil.Truncate(strings.Join(failures, "; "), 500)
		record.Status = domain.AlertFailed
		result = fmt.Errorf("全部告警通道投递失败: %s", strings.Join(failures, "; "))
	default:
		// 未配置任何通道：事件仍已落库，但管理员不会收到通知。
		fields["status"] = domain.AlertFailed
		fields["last_error"] = "未配置任何告警通道"
		record.Status = domain.AlertFailed
		obs.Logger(ctx).Warn("告警事件已记录但未配置告警通道，管理员不会收到通知",
			"alert_type", record.AlertType, "title", record.Title)
	}
	if err := s.Events.UpdateFields(ctx, record.ID, fields); err != nil {
		obs.Logger(ctx).Error("回写告警投递结果失败", "alert_id", record.ID, "error", err)
	}
	return result
}

func (s *Service) dedupWindow(ctx context.Context) time.Duration {
	if s.Settings == nil {
		return 0
	}
	return time.Duration(s.Settings.GetInt64(ctx, "alert_dedup_window_sec")) * time.Second
}

func encodePayload(payload map[string]any) datatypes.JSON {
	if len(payload) == 0 {
		return nil
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil
	}
	return datatypes.JSON(raw)
}
