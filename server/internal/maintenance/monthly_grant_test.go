package maintenance

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/liguopeng80/tokenzen-lite/server/internal/billing"
	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// grantScheduler 构造一个具备发放能力的调度器，时钟固定在给定时刻。
func grantScheduler(db *gorm.DB, alerts *captureNotifier, now time.Time) *Scheduler {
	return &Scheduler{
		Settings: store.NewSettingsRepo(db),
		Users:    store.NewUserRepo(db),
		Billing:  billing.NewService(db),
		Alerts:   alerts,
		Now:      func() time.Time { return now },
	}
}

// setSetting 写入一个系统设置项。
func setSetting(t *testing.T, db *gorm.DB, key string, raw string) {
	t.Helper()
	if err := store.NewSettingsRepo(db).Set(context.Background(), key,
		json.RawMessage(raw)); err != nil {
		t.Fatalf("写入设置 %s 失败: %v", key, err)
	}
}

// balanceOf 读取用户当前余额。
func balanceOf(t *testing.T, db *gorm.DB, username string) domain.Credits {
	t.Helper()
	var u store.User
	if err := db.Where("username = ?", username).First(&u).Error; err != nil {
		t.Fatalf("查询用户 %s 失败: %v", username, err)
	}
	return u.CreditBalance
}

// 额度为 0 表示关闭自动发放，任何账号的余额都不应变动。
func TestMonthlyGrantDisabledByZeroAmount(t *testing.T) {
	db := newTestDB(t)
	seedUser(t, db, "nogrant", 100, 0, domain.UserEnabled)

	grantScheduler(db, &captureNotifier{}, time.Now()).
		grantMonthlyCredits(context.Background())

	if got := balanceOf(t, db, "nogrant"); got != 100 {
		t.Errorf("关闭自动发放时余额不应变动，实际 %d", got)
	}
}

// 补足到额度：余额低于额度的账号补到额度，已达额度的账号不发放，
// 且同月重复执行不重复记账（幂等键按「月份 + 用户」唯一）。
func TestMonthlyGrantTopUpIsIdempotentWithinMonth(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	seedUser(t, db, "low", 20_000, 0, domain.UserEnabled)
	seedUser(t, db, "full", 500_000, 0, domain.UserEnabled)
	seedUser(t, db, "off", 0, 0, domain.UserDisabled)

	setSetting(t, db, "monthly_grant_credits", "100000")
	setSetting(t, db, "monthly_grant_mode", `"topup"`)

	now := time.Date(2026, 3, 1, 2, 0, 0, 0, time.Local)
	s := grantScheduler(db, &captureNotifier{}, now)
	s.grantMonthlyCredits(ctx)

	if got := balanceOf(t, db, "low"); got != 100_000 {
		t.Errorf("补足口径下余额应补到额度，实际 %d", got)
	}
	if got := balanceOf(t, db, "full"); got != 500_000 {
		t.Errorf("余额已达额度的账号不应发放，实际 %d", got)
	}
	if got := balanceOf(t, db, "off"); got != 0 {
		t.Errorf("已禁用账号不应发放，实际 %d", got)
	}

	// 同月再执行一次：命中幂等键，余额不再变动。
	// 这条覆盖的是维护循环每小时一轮的现实——同月会执行数百次。
	s.grantMonthlyCredits(ctx)
	if got := balanceOf(t, db, "low"); got != 100_000 {
		t.Errorf("同月重复执行不应重复发放，实际 %d", got)
	}

	// 该账号消费掉一部分后，同月仍不补发；进入下月才再次补足。
	if err := db.Model(&store.User{}).Where("username = ?", "low").
		Update("credit_balance", 5_000).Error; err != nil {
		t.Fatalf("模拟消费失败: %v", err)
	}
	s.grantMonthlyCredits(ctx)
	if got := balanceOf(t, db, "low"); got != 5_000 {
		t.Errorf("同月消费后不应补发，实际 %d", got)
	}

	next := time.Date(2026, 4, 1, 2, 0, 0, 0, time.Local)
	grantScheduler(db, &captureNotifier{}, next).grantMonthlyCredits(ctx)
	if got := balanceOf(t, db, "low"); got != 100_000 {
		t.Errorf("跨月应重新补足到额度，实际 %d", got)
	}
}

// 增发口径：不看当前余额，每月固定增发，未用完的部分累积。
func TestMonthlyGrantAddAccumulates(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	seedUser(t, db, "adder", 80_000, 0, domain.UserEnabled)

	setSetting(t, db, "monthly_grant_credits", "50000")
	setSetting(t, db, "monthly_grant_mode", `"add"`)

	march := time.Date(2026, 3, 1, 2, 0, 0, 0, time.Local)
	grantScheduler(db, &captureNotifier{}, march).grantMonthlyCredits(ctx)
	if got := balanceOf(t, db, "adder"); got != 130_000 {
		t.Errorf("增发口径应在原余额上叠加，实际 %d", got)
	}

	april := time.Date(2026, 4, 1, 2, 0, 0, 0, time.Local)
	grantScheduler(db, &captureNotifier{}, april).grantMonthlyCredits(ctx)
	if got := balanceOf(t, db, "adder"); got != 180_000 {
		t.Errorf("跨月应再次增发，实际 %d", got)
	}
}
