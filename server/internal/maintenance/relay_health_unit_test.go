package maintenance

import (
	"testing"
	"time"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// evaluateRelayHealth 是连续量告警的纯判定逻辑——阈值比较、样本门限、
// 事件构造——全部由入参决定，不依赖 DB 或设置。

func TestEvaluateRelayHealthSampleTooSmall(t *testing.T) {
	events := evaluateRelayHealth(relayHealthAssessment{
		RateThresholdPercent: 20,
		MinRequests:          10,
		Health:               store.RelayHealth{Total: 5, Failed: 5},
		Now:                  time.Date(2026, 8, 9, 10, 0, 0, 0, time.Local),
	})
	if len(events) != 0 {
		t.Errorf("样本不足时不应产生事件，实际 %d 条", len(events))
	}
}

func TestEvaluateRelayHealthFailureRateExceeded(t *testing.T) {
	events := evaluateRelayHealth(relayHealthAssessment{
		RateThresholdPercent: 20,
		MinRequests:          10,
		Health:               store.RelayHealth{Total: 100, Failed: 30},
		Now:                  time.Date(2026, 8, 9, 10, 0, 0, 0, time.Local),
	})
	if len(events) != 1 {
		t.Fatalf("失败率 30%% 超阈值 20%% 应告警 1 条，实际 %d 条", len(events))
	}
	if events[0].Type != domain.AlertErrorRateHigh {
		t.Errorf("事件类型应为 error_rate_high，实际 %s", events[0].Type)
	}
	if got := events[0].Payload["failure_rate_percent"]; got != int64(30) {
		t.Errorf("失败率口径应为 30，实际 %v", got)
	}
}

func TestEvaluateRelayHealthLatencyExceeded(t *testing.T) {
	events := evaluateRelayHealth(relayHealthAssessment{
		LatencyThresholdMS: 5000,
		MinRequests:        10,
		Health:             store.RelayHealth{Total: 100, P95LatencyMS: 9000},
		Now:                time.Date(2026, 8, 9, 10, 0, 0, 0, time.Local),
	})
	if len(events) != 1 {
		t.Fatalf("95 分位 9000 超阈值 5000 应告警 1 条，实际 %d 条", len(events))
	}
	if events[0].Type != domain.AlertLatencyDegraded {
		t.Errorf("事件类型应为 latency_degraded，实际 %s", events[0].Type)
	}
}

// 失败率与耗时同时越线时产生两条事件。
func TestEvaluateRelayHealthBothThresholdsExceeded(t *testing.T) {
	events := evaluateRelayHealth(relayHealthAssessment{
		RateThresholdPercent: 20,
		LatencyThresholdMS:   5000,
		MinRequests:          10,
		Health:               store.RelayHealth{Total: 100, Failed: 50, P95LatencyMS: 9000},
		Now:                  time.Date(2026, 8, 9, 10, 0, 0, 0, time.Local),
	})
	if len(events) != 2 {
		t.Errorf("失败率与耗时同时越线应产生 2 条事件，实际 %d 条", len(events))
	}
}

// 阈值为 0 时关闭对应判定，不产生事件。
func TestEvaluateRelayHealthDisabledThresholds(t *testing.T) {
	events := evaluateRelayHealth(relayHealthAssessment{
		RateThresholdPercent: 0,
		LatencyThresholdMS:   0,
		MinRequests:          1,
		Health:               store.RelayHealth{Total: 100, Failed: 100, P95LatencyMS: 999_999},
		Now:                  time.Date(2026, 8, 9, 10, 0, 0, 0, time.Local),
	})
	if len(events) != 0 {
		t.Errorf("全部阈值关闭时不应产生事件，实际 %d 条", len(events))
	}
}

// 失败率刚好等于阈值时不告警（用 > 而非 >=）。
func TestEvaluateRelayHealthRateAtThresholdNotAlerted(t *testing.T) {
	events := evaluateRelayHealth(relayHealthAssessment{
		RateThresholdPercent: 20,
		MinRequests:          1,
		Health:               store.RelayHealth{Total: 100, Failed: 20}, // 恰好 20%
		Now:                  time.Date(2026, 8, 9, 10, 0, 0, 0, time.Local),
	})
	if len(events) != 0 {
		t.Errorf("失败率等于阈值时不应告警，实际 %d 条", len(events))
	}
}
