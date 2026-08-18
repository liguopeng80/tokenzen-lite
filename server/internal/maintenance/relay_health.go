package maintenance

import (
	"context"
	"fmt"
	"time"

	"github.com/liguopeng80/tokenzen-lite/server/internal/alerting"
	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// healthWindow 连续量告警的统计窗口，与维护循环的执行间隔一致：
// 窗口短于间隔会漏掉两轮之间发生的劣化，长于间隔则同一段异常被反复判定。
const healthWindow = time.Hour

// relayHealthAssessment 携带判定中继连续量告警所需的全部入参。
// 纯数据结构，不引用 Scheduler 或设置，便于在无 DB 环境下测试。
type relayHealthAssessment struct {
	RateThresholdPercent int64 // 0 表示关闭失败率判定
	LatencyThresholdMS   int64 // 0 表示关闭耗时判定
	MinRequests          int64
	Health               store.RelayHealth
	Now                  time.Time // 注入而非 time.Now()，使函数可测
}

// evaluateRelayHealth 判定窗口内的中继失败率与耗时是否越过阈值，返回应触发的告警事件。
//
// 纯函数：不访问 DB 或设置，所有判定依据由调用方传入。
// 样本不足（Total < MinRequests）时不产生事件：夜间几次调用全失败会得出 100%
// 的失败率，据此告警只会训练管理员忽略这类告警。
func evaluateRelayHealth(in relayHealthAssessment) []alerting.Event {
	if in.Health.Total < in.MinRequests {
		return nil
	}
	// 去重键含小时：同一段持续劣化每小时最多再报一次，恢复后自然停止。
	hour := in.Now.In(time.Local).Format("2006-01-02T15")
	var events []alerting.Event

	if in.RateThresholdPercent > 0 {
		if actual := in.Health.FailureRatePercent(); actual > in.RateThresholdPercent {
			events = append(events, alerting.Event{
				Type:     domain.AlertErrorRateHigh,
				Severity: domain.AlertCritical,
				DedupKey: fmt.Sprintf("error_rate_high:%s", hour),
				Title:    fmt.Sprintf("中继失败率 %d%%，超过阈值", actual),
				Message: fmt.Sprintf("最近一小时的 %d 次调用中有 %d 次失败（%d%%），"+
					"超过告警阈值 %d%%。用户侧表现为调用报错；"+
					"请检查上游渠道可用性与「告警记录」中的渠道自动禁用事件。",
					in.Health.Total, in.Health.Failed, actual, in.RateThresholdPercent),
				Payload: map[string]any{
					"window_minutes": int64(healthWindow / time.Minute),
					"total":          in.Health.Total, "failed": in.Health.Failed,
					"failure_rate_percent": actual, "threshold_percent": in.RateThresholdPercent,
				},
			})
		}
	}
	if in.LatencyThresholdMS > 0 && in.Health.P95LatencyMS > in.LatencyThresholdMS {
		events = append(events, alerting.Event{
			Type:     domain.AlertLatencyDegraded,
			Severity: domain.AlertWarning,
			DedupKey: fmt.Sprintf("latency_degraded:%s", hour),
			Title:    fmt.Sprintf("中继耗时 95 分位 %d 毫秒，超过阈值", in.Health.P95LatencyMS),
			Message: fmt.Sprintf("最近一小时 %d 次调用的总耗时 95 分位为 %d 毫秒，"+
				"超过告警阈值 %d 毫秒。用户侧表现为客户端响应变慢或超时。",
				in.Health.Total, in.Health.P95LatencyMS, in.LatencyThresholdMS),
			Payload: map[string]any{
				"window_minutes": int64(healthWindow / time.Minute),
				"total":          in.Health.Total,
				"p95_latency_ms": in.Health.P95LatencyMS, "threshold_ms": in.LatencyThresholdMS,
			},
		})
	}
	return events
}

// checkRelayHealth 判定最近一个窗口内的中继失败率与耗时是否越过阈值。
//
// 与既有告警的分工：渠道自动禁用针对单个渠道的连续致命错误，用量日志丢弃针对
// 写入积压，两者都是离散事件。本函数补的是连续量——上游劣化时失败率与耗时是
// 逐步爬升的，任何单条事件都不越界，但用户已经在受影响。
func (s *Scheduler) checkRelayHealth(ctx context.Context) error {
	if s.UsageLogs == nil || s.Alerts == nil {
		return nil
	}
	ratePercent := s.Settings.GetInt64(ctx, "alert_error_rate_percent")
	latencyMS := s.Settings.GetInt64(ctx, "alert_latency_p95_ms")
	if ratePercent <= 0 && latencyMS <= 0 {
		return nil
	}
	since := s.now().Add(-healthWindow)
	health, err := s.UsageLogs.WindowHealth(ctx, since)
	if err != nil {
		return fmt.Errorf("查询中继健康度: %w", err)
	}
	minRequests := s.Settings.GetInt64(ctx, "alert_error_rate_min_requests")
	for _, ev := range evaluateRelayHealth(relayHealthAssessment{
		RateThresholdPercent: ratePercent,
		LatencyThresholdMS:   latencyMS,
		MinRequests:          minRequests,
		Health:               health,
		Now:                  s.now(),
	}) {
		s.Alerts.Raise(ctx, ev)
	}
	return nil
}
