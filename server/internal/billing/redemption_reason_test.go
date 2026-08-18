package billing

import (
	"context"
	"errors"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/liguopeng80/tokenzen-lite/server/internal/auth"
	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// seedRedemption 种入一个兑换码，返回明文。
func seedRedemption(t *testing.T, db *gorm.DB, code string,
	status domain.RedemptionStatus, expiresAt *time.Time) {

	t.Helper()
	red := &store.Redemption{
		CodeHash: auth.HashKey(code), Credits: 10_000,
		Status: status, ExpiresAt: expiresAt, Name: "原因判定",
	}
	if err := db.Create(red).Error; err != nil {
		t.Fatalf("种入兑换码失败: %v", err)
	}
}

// 核销失败时必须区分出具体原因：员工据此判断是自己抄错了，
// 还是这张码本身已经不能用、只能去找管理员。
func TestRedeemFailureReasons(t *testing.T) {
	db := newTestDB(t)
	svc := NewService(db)
	u := seedUser(t, db, "reason-user", 0)

	past := time.Now().Add(-time.Hour)
	future := time.Now().Add(time.Hour)
	seedRedemption(t, db, "code-expired", domain.RedemptionUnused, &past)
	seedRedemption(t, db, "code-disabled", domain.RedemptionDisabled, &future)
	seedRedemption(t, db, "code-used", domain.RedemptionUsed, nil)

	cases := []struct {
		name string
		code string
		want error
	}{
		{"已过期", "code-expired", ErrRedemptionExpired},
		{"已作废", "code-disabled", ErrRedemptionDisabled},
		{"已核销", "code-used", ErrRedemptionUsed},
		{"不存在", "code-never-issued", ErrRedemptionNotFound},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := svc.Redeem(context.Background(), u.ID, c.code)
			if !errors.Is(err, c.want) {
				t.Fatalf("期望 %v，实际 %v", c.want, err)
			}
			// 四个原因都归入 ErrRedemptionUnavailable，只判「不可用」的调用方不受影响。
			if !errors.Is(err, ErrRedemptionUnavailable) {
				t.Errorf("具体原因应仍可判定为兑换码不可用，实际 %v", err)
			}
		})
	}

	// 失败路径不得改动余额。
	var fresh store.User
	db.First(&fresh, u.ID)
	if fresh.CreditBalance != 0 {
		t.Errorf("核销全部失败后余额应仍为 0，实际 %d", fresh.CreditBalance)
	}
	assertReconcileClean(t, db)
}

// 未过期的码正常核销：过期判定不能误伤仍在有效期内的码。
func TestRedeemWithinExpiry(t *testing.T) {
	db := newTestDB(t)
	svc := NewService(db)
	u := seedUser(t, db, "within-expiry", 0)

	future := time.Now().Add(24 * time.Hour)
	seedRedemption(t, db, "code-valid", domain.RedemptionUnused, &future)

	entry, err := svc.Redeem(context.Background(), u.ID, "code-valid")
	if err != nil {
		t.Fatalf("有效期内的码应核销成功，实际 %v", err)
	}
	if entry.Amount != 10_000 {
		t.Errorf("入账金额应为 10000，实际 %d", entry.Amount)
	}
	// 已核销的码再兑一次，原因必须是「已被使用」而不是「不存在」。
	if _, err := svc.Redeem(context.Background(), u.ID, "code-valid"); !errors.Is(err, ErrRedemptionUsed) {
		t.Errorf("重复核销应判定为已被使用，实际 %v", err)
	}
	assertReconcileClean(t, db)
}

// 展示态推导：已核销与已作废的码不因过期时间改变显示，
// 否则管理员在列表里看不出它当初是被谁用掉的还是被作废的。
func TestEffectiveRedemptionStatus(t *testing.T) {
	now := time.Now()
	past := now.Add(-time.Minute)
	future := now.Add(time.Minute)

	cases := []struct {
		name    string
		stored  domain.RedemptionStatus
		expires *time.Time
		want    domain.RedemptionStatus
	}{
		{"未使用且未设过期时间", domain.RedemptionUnused, nil, domain.RedemptionUnused},
		{"未使用且未到期", domain.RedemptionUnused, &future, domain.RedemptionUnused},
		{"未使用且已到期", domain.RedemptionUnused, &past, domain.RedemptionExpired},
		{"已核销且已到期", domain.RedemptionUsed, &past, domain.RedemptionUsed},
		{"已作废且已到期", domain.RedemptionDisabled, &past, domain.RedemptionDisabled},
	}
	for _, c := range cases {
		if got := domain.EffectiveRedemptionStatus(c.stored, c.expires, now); got != c.want {
			t.Errorf("%s：期望 %s，实际 %s", c.name, c.want, got)
		}
	}
}
