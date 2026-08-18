package maintenance

import (
	"context"
	"strings"
	"testing"

	"gorm.io/gorm"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// setEmail 为已种入的用户补上邮箱。
func setEmail(t *testing.T, db *gorm.DB, username, email string) {
	t.Helper()
	if err := db.Model(&store.User{}).Where("username = ?", username).
		Update("email", email).Error; err != nil {
		t.Fatalf("设置用户 %s 邮箱失败: %v", username, err)
	}
}

// 余额不足时，除管理员侧的聚合告警外，还向填了邮箱的用户本人投递定向提醒。
// 定向提醒只走邮件（EmailTo 非空），不进 Webhook 群通道。
func TestLowBalanceNotifiesUserDirectly(t *testing.T) {
	db := newTestDB(t)
	seedUser(t, db, "hasmail", 500, 0, domain.UserEnabled)
	seedUser(t, db, "nomail", 300, 0, domain.UserEnabled)
	setEmail(t, db, "hasmail", "hasmail@example.com")

	alerts := &captureNotifier{emailReady: true}
	newScheduler(db, alerts).checkLowBalances(context.Background())

	if n := len(alerts.eventsOfType(domain.AlertUserLowBalance)); n != 1 {
		t.Fatalf("管理员侧应有 1 条聚合告警，实际 %d 条", n)
	}
	notices := alerts.eventsOfType(domain.AlertUserBalanceNotice)
	if len(notices) != 1 {
		t.Fatalf("只有填了邮箱的用户应收到定向提醒，实际 %d 条", len(notices))
	}
	ev := notices[0]
	if len(ev.EmailTo) != 1 || ev.EmailTo[0] != "hasmail@example.com" {
		t.Errorf("定向提醒的收件人应为用户本人邮箱，实际 %v", ev.EmailTo)
	}
	if ev.SuppressFor <= 0 {
		t.Error("定向提醒应有独立的抑制窗口，否则用户会按维护轮次收到重复邮件")
	}
	if !strings.Contains(ev.Message, "hasmail") {
		t.Errorf("提醒正文应指明是哪个账号：%s", ev.Message)
	}
}

// 邮件通道未配置时不产生定向提醒：这类事件只能走邮件，落库只会得到一批
// 注定失败的记录，把告警列表刷满而不解决问题。管理员侧的聚合告警照常。
func TestNoUserNoticeWithoutEmailChannel(t *testing.T) {
	db := newTestDB(t)
	seedUser(t, db, "hasmail2", 500, 0, domain.UserEnabled)
	setEmail(t, db, "hasmail2", "hasmail2@example.com")

	alerts := &captureNotifier{emailReady: false}
	newScheduler(db, alerts).checkLowBalances(context.Background())

	if n := len(alerts.eventsOfType(domain.AlertUserBalanceNotice)); n != 0 {
		t.Errorf("邮件通道未配置时不应产生定向提醒，实际 %d 条", n)
	}
	if n := len(alerts.eventsOfType(domain.AlertUserLowBalance)); n != 1 {
		t.Errorf("管理员侧的聚合告警不受邮件通道影响，实际 %d 条", n)
	}
}

// 管理员关闭该项后不再向用户本人投递，管理员侧告警不受影响。
func TestUserNoticeCanBeDisabled(t *testing.T) {
	db := newTestDB(t)
	seedUser(t, db, "hasmail3", 500, 0, domain.UserEnabled)
	setEmail(t, db, "hasmail3", "hasmail3@example.com")
	setSetting(t, db, "user_balance_notice_enabled", "false")

	alerts := &captureNotifier{emailReady: true}
	newScheduler(db, alerts).checkLowBalances(context.Background())

	if n := len(alerts.eventsOfType(domain.AlertUserBalanceNotice)); n != 0 {
		t.Errorf("该项关闭后不应产生定向提醒，实际 %d 条", n)
	}
	if n := len(alerts.eventsOfType(domain.AlertUserLowBalance)); n != 1 {
		t.Errorf("管理员侧的聚合告警不受该项影响，实际 %d 条", n)
	}
}
