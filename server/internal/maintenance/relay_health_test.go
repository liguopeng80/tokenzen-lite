package maintenance

import (
	"context"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// healthScheduler 构造只做连续量判定的调度器。
func healthScheduler(db *gorm.DB, alerts *captureNotifier) *Scheduler {
	return &Scheduler{
		Settings:  store.NewSettingsRepo(db),
		UsageLogs: store.NewUsageLogRepo(db),
		Alerts:    alerts,
	}
}

// seedUsageLogs 种入 n 条指定终态的用量日志。
func seedUsageLogs(t *testing.T, db *gorm.DB, n int, status domain.UsageStatus, latencyMS int64) {
	t.Helper()
	for i := 0; i < n; i++ {
		l := &store.UsageLog{
			RequestID: string(status) + "-" + time.Now().Format("150405.000000000") + "-" + itoa(i),
			ModelName: "health-model", Status: status, LatencyMS: latencyMS,
		}
		if err := db.Create(l).Error; err != nil {
			t.Fatalf("种入用量日志失败: %v", err)
		}
	}
}

func itoa(i int) string {
	const digits = "0123456789"
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{digits[i%10]}, b...)
		i /= 10
	}
	return string(b)
}

// 失败率超过阈值时告警，正文给出窗口内的实际次数——「20% 失败」若不附带
// 分母，管理员无法判断是 1/5 还是 200/1000。
func TestRelayHealthAlertsOnHighFailureRate(t *testing.T) {
	db := newTestDB(t)
	seedUsageLogs(t, db, 30, domain.UsageFailed, 100)
	seedUsageLogs(t, db, 70, domain.UsageSettled, 100)

	alerts := &captureNotifier{}
	healthScheduler(db, alerts).checkRelayHealth(context.Background())

	events := alerts.eventsOfType(domain.AlertErrorRateHigh)
	if len(events) != 1 {
		t.Fatalf("失败率 30%% 超过默认阈值 20%%，应告警 1 条，实际 %d 条", len(events))
	}
	if got := events[0].Payload["failure_rate_percent"]; got != int64(30) {
		t.Errorf("失败率口径错误：%v", got)
	}
	if got := events[0].Payload["total"]; got != int64(100) {
		t.Errorf("窗口内请求总数错误：%v", got)
	}
}

// 样本不足时不判定：夜间几次调用全失败会得出 100% 的失败率，
// 据此告警只会训练管理员忽略这类告警。
func TestRelayHealthQuietBelowMinimumRequests(t *testing.T) {
	db := newTestDB(t)
	seedUsageLogs(t, db, 5, domain.UsageFailed, 100)

	alerts := &captureNotifier{}
	healthScheduler(db, alerts).checkRelayHealth(context.Background())

	if n := len(alerts.events); n != 0 {
		t.Errorf("窗口内请求数不足最小样本时不应告警，实际 %d 条", n)
	}
}

// 阈值设为 0 表示关闭该项判定。
func TestRelayHealthDisabledByZeroThreshold(t *testing.T) {
	db := newTestDB(t)
	seedUsageLogs(t, db, 100, domain.UsageFailed, 100)
	setSetting(t, db, "alert_error_rate_percent", "0")

	alerts := &captureNotifier{}
	healthScheduler(db, alerts).checkRelayHealth(context.Background())

	if n := len(alerts.events); n != 0 {
		t.Errorf("阈值为 0 时不应告警，实际 %d 条", n)
	}
}

// 耗时判定默认关闭；显式设定阈值后，95 分位越线才告警。
func TestRelayHealthAlertsOnLatencyOnlyWhenConfigured(t *testing.T) {
	db := newTestDB(t)
	seedUsageLogs(t, db, 100, domain.UsageSettled, 9_000)

	alerts := &captureNotifier{}
	healthScheduler(db, alerts).checkRelayHealth(context.Background())
	if n := len(alerts.eventsOfType(domain.AlertLatencyDegraded)); n != 0 {
		t.Fatalf("耗时判定默认关闭，不应告警，实际 %d 条", n)
	}

	setSetting(t, db, "alert_latency_p95_ms", "5000")
	alerts = &captureNotifier{}
	healthScheduler(db, alerts).checkRelayHealth(context.Background())

	events := alerts.eventsOfType(domain.AlertLatencyDegraded)
	if len(events) != 1 {
		t.Fatalf("95 分位 9000 毫秒超过阈值 5000，应告警 1 条，实际 %d 条", len(events))
	}
	if got := events[0].Payload["p95_latency_ms"]; got != int64(9_000) {
		t.Errorf("95 分位口径错误：%v", got)
	}
}
