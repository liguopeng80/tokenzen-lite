package maintenance

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"gorm.io/gorm"

	"github.com/liguopeng80/tokenzen-lite/server/internal/alerting"
	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store/migrate"
)

func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	url := os.Getenv("TZL_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("未设置 TZL_TEST_DATABASE_URL，跳过数据维护集成测试")
	}
	if err := migrate.Up(url); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}
	db, err := store.Open(url)
	if err != nil {
		t.Fatalf("连接测试库失败: %v", err)
	}
	if sqlDB, dbErr := db.DB(); dbErr == nil {
		t.Cleanup(func() { _ = sqlDB.Close() })
	}
	if err := db.Exec("TRUNCATE users, settings, usage_logs RESTART IDENTITY CASCADE").Error; err != nil {
		t.Fatalf("清空测试表失败: %v", err)
	}
	return db
}

// captureNotifier 记录被触发的告警，供断言检查。
// emailReady 模拟邮件通道是否已配置：定向通知只在通道就绪时才产生。
type captureNotifier struct {
	events     []alerting.Event
	emailReady bool
}

func (c *captureNotifier) Raise(_ context.Context, ev alerting.Event) {
	c.events = append(c.events, ev)
}

func (c *captureNotifier) EmailReady(_ context.Context) bool { return c.emailReady }

// eventsOfType 取指定类型的事件，便于把管理员告警与用户定向通知分开断言。
func (c *captureNotifier) eventsOfType(t domain.AlertType) []alerting.Event {
	var out []alerting.Event
	for _, ev := range c.events {
		if ev.Type == t {
			out = append(out, ev)
		}
	}
	return out
}

// seedUser 种入一个用户。used 为累计消费，用于区分「已消费到低余额」
// 与「从未获得过积分」两种 0 余额账号。
func seedUser(t *testing.T, db *gorm.DB, username string, balance domain.Credits,
	used domain.Credits, status domain.UserStatus) {

	t.Helper()
	u := &store.User{
		Username: username, PasswordHash: "x",
		Role: domain.RoleUser, Status: status,
		CreditBalance: balance, CreditUsed: used,
	}
	if err := db.Create(u).Error; err != nil {
		t.Fatalf("种入用户 %s 失败: %v", username, err)
	}
}

func newScheduler(db *gorm.DB, alerts alerting.Notifier) *Scheduler {
	return &Scheduler{
		Settings: store.NewSettingsRepo(db),
		Users:    store.NewUserRepo(db),
		Alerts:   alerts,
	}
}

// 余额低于阈值的启用用户应触发一条聚合告警，且已禁用用户与余额充足的用户不计入。
func TestCheckLowBalancesRaisesAggregatedAlert(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	seedUser(t, db, "poor_a", 0, 50_000, domain.UserEnabled)
	seedUser(t, db, "poor_b", 500, 0, domain.UserEnabled)
	seedUser(t, db, "rich", 900_000, 0, domain.UserEnabled)
	seedUser(t, db, "disabled_poor", 0, 10_000, domain.UserDisabled)

	alerts := &captureNotifier{}
	newScheduler(db, alerts).checkLowBalances(ctx)

	if len(alerts.events) != 1 {
		t.Fatalf("期望 1 条低余额告警，实际 %d 条", len(alerts.events))
	}
	ev := alerts.events[0]
	if ev.Type != domain.AlertUserLowBalance {
		t.Errorf("告警类型错误: %s", ev.Type)
	}
	if got := ev.Payload["total"]; got != int64(2) {
		t.Errorf("低余额人数应为 2（禁用用户不计入），实际 %v", got)
	}
	for _, name := range []string{"poor_a", "poor_b"} {
		if !strings.Contains(ev.Message, name) {
			t.Errorf("告警正文缺少用户 %s: %s", name, ev.Message)
		}
	}
	for _, name := range []string{"rich", "disabled_poor"} {
		if strings.Contains(ev.Message, name) {
			t.Errorf("告警正文不应包含用户 %s: %s", name, ev.Message)
		}
	}
	// 名单按余额升序：耗尽的用户排在最前，且耗尽人数口径准确。
	if !strings.Contains(ev.Message, "其中 1 人已耗尽") {
		t.Errorf("耗尽人数口径错误: %s", ev.Message)
	}
}

// 阈值设为 0 表示管理员关闭预警，此时即使有用户余额耗尽也不告警。
func TestCheckLowBalancesDisabledByZeroThreshold(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	seedUser(t, db, "poor", 0, 10_000, domain.UserEnabled)

	settings := store.NewSettingsRepo(db)
	if err := settings.Set(ctx, "low_balance_threshold_credits", json.RawMessage("0")); err != nil {
		t.Fatalf("写入阈值失败: %v", err)
	}

	alerts := &captureNotifier{}
	(&Scheduler{Settings: settings, Users: store.NewUserRepo(db), Alerts: alerts}).
		checkLowBalances(ctx)

	if len(alerts.events) != 0 {
		t.Fatalf("阈值为 0 时不应告警，实际 %d 条", len(alerts.events))
	}
}

// 从未获得过积分也从未消费过的账号不计入：它没有正在进行的调用可被中断。
// 新装系统的初始管理员即属此类，计入会让每台新装机器立刻收到无意义的告警。
func TestCheckLowBalancesIgnoresNeverFundedAccounts(t *testing.T) {
	db := newTestDB(t)
	seedUser(t, db, "freshroot", 0, 0, domain.UserEnabled)

	alerts := &captureNotifier{}
	newScheduler(db, alerts).checkLowBalances(context.Background())

	if len(alerts.events) != 0 {
		t.Fatalf("从未获得过积分的账号不应触发告警，实际 %d 条", len(alerts.events))
	}

	// 一旦获得积分（余额为正但低于阈值），即进入预警范围。
	seedUser(t, db, "justfunded", 500, 0, domain.UserEnabled)
	alerts = &captureNotifier{}
	newScheduler(db, alerts).checkLowBalances(context.Background())

	if len(alerts.events) != 1 {
		t.Fatalf("已获得积分但低于阈值的账号应触发告警，实际 %d 条", len(alerts.events))
	}
	if got := alerts.events[0].Payload["total"]; got != int64(1) {
		t.Errorf("低余额人数应为 1（未发放过积分的账号不计入），实际 %v", got)
	}
}

// 全部用户余额均高于阈值时不应产生告警。
func TestCheckLowBalancesQuietWhenAllFunded(t *testing.T) {
	db := newTestDB(t)
	seedUser(t, db, "rich_a", 500_000, 0, domain.UserEnabled)
	seedUser(t, db, "rich_b", 100_000, 0, domain.UserEnabled)

	alerts := &captureNotifier{}
	newScheduler(db, alerts).checkLowBalances(context.Background())

	if len(alerts.events) != 0 {
		t.Fatalf("无低余额用户时不应告警，实际 %d 条", len(alerts.events))
	}
}
