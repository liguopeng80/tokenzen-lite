package relay

// 计费会话预扣与密钥额度上限的精确边界集成测试（评审整改 P3-6 / 决策 2）：
// 直接驱动 BillingSession.Precharge 以精确控制金额——
// credit_limit 恰好等于预扣金额时预扣成功（<= 判定），状态保持 enabled；
// 额度占满后再预扣 1 积分返回 ErrKeyDepleted，状态写入 depleted。

import (
	"context"
	"errors"
	"os"
	"testing"

	"gorm.io/gorm"

	"github.com/liguopeng80/tokenzen-lite/server/internal/billing"
	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store/migrate"
)

// newSessionTestDB 连接共享测试库（未设置 TZL_TEST_DATABASE_URL 时跳过）。
func newSessionTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	url := os.Getenv("TZL_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("未设置 TZL_TEST_DATABASE_URL，跳过 relay 会话集成测试")
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
	if err := db.Exec("TRUNCATE users, api_keys, sessions, credit_ledger, redemptions, usage_logs RESTART IDENTITY CASCADE").Error; err != nil {
		t.Fatalf("清空测试表失败: %v", err)
	}
	return db
}

// 【集成】精确边界：上限 == 预扣金额 → 成功且保持 enabled；随后 +1 积分 → depleted。
func TestBillingSessionPrechargeExactLimitBoundary(t *testing.T) {
	db := newSessionTestDB(t)
	svc := billing.NewService(db)
	ctx := context.Background()

	u := &store.User{Username: "boundary-user", PasswordHash: "x",
		Role: domain.RoleUser, Status: domain.UserEnabled}
	if err := db.Create(u).Error; err != nil {
		t.Fatalf("种入用户失败: %v", err)
	}
	if _, err := svc.Grant(ctx, u.ID, 10_000, 0, "测试额度", ""); err != nil {
		t.Fatalf("分配积分失败: %v", err)
	}
	limit := domain.Credits(500)
	key := &store.APIKey{UserID: u.ID, Name: "boundary-key",
		KeyHash: "hash-boundary", KeyPrefix: "sk-bd",
		Status: domain.KeyEnabled, CreditLimit: &limit}
	if err := db.Create(key).Error; err != nil {
		t.Fatalf("种入密钥失败: %v", err)
	}

	keyStatus := func() domain.KeyStatus {
		var k store.APIKey
		if err := db.First(&k, key.ID).Error; err != nil {
			t.Fatalf("查密钥失败: %v", err)
		}
		return k.Status
	}

	// 预扣金额恰好等于上限：<= 判定应放行
	s1 := NewBillingSession(db, svc, "req-boundary-1", key, 0)
	if err := s1.Precharge(ctx, 500); err != nil {
		t.Fatalf("预扣金额等于上限应成功，实际 %v", err)
	}
	if got := keyStatus(); got != domain.KeyEnabled {
		t.Fatalf("精确占满上限后状态应保持 enabled，实际 %s", got)
	}

	// 额度已占满：再预扣 1 积分命中上限，状态写入 depleted
	s2 := NewBillingSession(db, svc, "req-boundary-2", key, 0)
	if err := s2.Precharge(ctx, 1); !errors.Is(err, ErrKeyDepleted) {
		t.Fatalf("额度占满后预扣应返回 ErrKeyDepleted，实际 %v", err)
	}
	if got := keyStatus(); got != domain.KeyDepleted {
		t.Fatalf("命中上限后状态应写入 depleted，实际 %s", got)
	}
}

// seedSessionKey 种入用户（含积分）与密钥，limit 为 nil 表示无额度上限。
func seedSessionKey(t *testing.T, db *gorm.DB, svc *billing.Service, name string, limit *domain.Credits) *store.APIKey {
	t.Helper()
	ctx := context.Background()
	u := &store.User{Username: name + "-user", PasswordHash: "x",
		Role: domain.RoleUser, Status: domain.UserEnabled}
	if err := db.Create(u).Error; err != nil {
		t.Fatalf("种入用户失败: %v", err)
	}
	if _, err := svc.Grant(ctx, u.ID, 100_000, 0, "测试额度", ""); err != nil {
		t.Fatalf("分配积分失败: %v", err)
	}
	key := &store.APIKey{UserID: u.ID, Name: name + "-key",
		KeyHash: "hash-" + name, KeyPrefix: "sk-" + name,
		Status: domain.KeyEnabled, CreditLimit: limit}
	if err := db.Create(key).Error; err != nil {
		t.Fatalf("种入密钥失败: %v", err)
	}
	return key
}

// keyRow 重新读取密钥行。
func keyRow(t *testing.T, db *gorm.DB, id int64) store.APIKey {
	t.Helper()
	var k store.APIKey
	if err := db.First(&k, id).Error; err != nil {
		t.Fatalf("查密钥失败: %v", err)
	}
	return k
}

// 【集成】无限额密钥：预扣仅累计已用额度、不误标 depleted；连续两次预扣累计叠加。
func TestBillingSessionUnlimitedPrechargeAccumulates(t *testing.T) {
	db := newSessionTestDB(t)
	svc := billing.NewService(db)
	ctx := context.Background()
	key := seedSessionKey(t, db, svc, "unlimited-acc", nil)

	s1 := NewBillingSession(db, svc, "req-unlimited-acc-1", key, 0)
	if err := s1.Precharge(ctx, 3000); err != nil {
		t.Fatalf("无限额密钥预扣应成功，实际 %v", err)
	}
	k := keyRow(t, db, key.ID)
	if k.CreditUsed != 3000 {
		t.Fatalf("预扣后已用额度应为 3000，实际 %d", k.CreditUsed)
	}
	if k.Status != domain.KeyEnabled {
		t.Fatalf("无限额密钥不应被标记 depleted，实际 %s", k.Status)
	}

	// 第二次预扣（新会话、新请求）：已用额度叠加
	s2 := NewBillingSession(db, svc, "req-unlimited-acc-2", key, 0)
	if err := s2.Precharge(ctx, 2000); err != nil {
		t.Fatalf("第二次预扣应成功，实际 %v", err)
	}
	k = keyRow(t, db, key.ID)
	if k.CreditUsed != 5000 {
		t.Fatalf("两次预扣后已用额度应累计为 5000，实际 %d", k.CreditUsed)
	}
	if k.Status != domain.KeyEnabled {
		t.Fatalf("累计后状态应保持 enabled，实际 %s", k.Status)
	}
}

// 【集成】无限额密钥结算补扣方向：Precharge(1000) + Settle(1500) → 已用额度 1500。
func TestBillingSessionUnlimitedSettleUpwardAdjust(t *testing.T) {
	db := newSessionTestDB(t)
	svc := billing.NewService(db)
	ctx := context.Background()
	key := seedSessionKey(t, db, svc, "unlimited-up", nil)

	s := NewBillingSession(db, svc, "req-unlimited-up-1", key, 0)
	if err := s.Precharge(ctx, 1000); err != nil {
		t.Fatalf("预扣应成功，实际 %v", err)
	}
	if err := s.Settle(ctx, 1500); err != nil {
		t.Fatalf("结算补扣应成功，实际 %v", err)
	}
	k := keyRow(t, db, key.ID)
	if k.CreditUsed != 1500 {
		t.Fatalf("结算补扣后已用额度应为 1500，实际 %d", k.CreditUsed)
	}
}

// 【集成】设限密钥限内正常消耗：limit=5000，Precharge(2000)+Settle(2000) →
// 已用额度 2000、状态保持 enabled（校验+累计合并路径限内不影响累计正确性）。
func TestBillingSessionLimitedWithinLimitConsume(t *testing.T) {
	db := newSessionTestDB(t)
	svc := billing.NewService(db)
	ctx := context.Background()
	limit := domain.Credits(5000)
	key := seedSessionKey(t, db, svc, "limited-ok", &limit)

	s := NewBillingSession(db, svc, "req-limited-ok-1", key, 0)
	if err := s.Precharge(ctx, 2000); err != nil {
		t.Fatalf("限内预扣应成功，实际 %v", err)
	}
	if err := s.Settle(ctx, 2000); err != nil {
		t.Fatalf("结算应成功，实际 %v", err)
	}
	k := keyRow(t, db, key.ID)
	if k.CreditUsed != 2000 {
		t.Fatalf("限内消耗后已用额度应为 2000，实际 %d", k.CreditUsed)
	}
	if k.Status != domain.KeyEnabled {
		t.Fatalf("限内消耗后状态应保持 enabled，实际 %s", k.Status)
	}
}
