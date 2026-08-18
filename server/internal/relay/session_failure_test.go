package relay

// 计费会话结算失败路径的单元测试（评审整改）：
// 结算写入失败后预扣保留、封锁退款，交由孤儿预扣清理补偿——
// 立即退款会在"写入超时但事务已提交"时与 settle_adjust 双重入账。
// 通过指向不可达地址的懒连接数据库注入写入失败，不依赖测试库。

import (
	"context"
	"testing"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/liguopeng80/tokenzen-lite/server/internal/billing"
)

// newBrokenDB 返回指向不可达地址的懒连接 gorm.DB：任何查询在执行时失败。
func newBrokenDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(
		postgres.Open("postgres://none:none@127.0.0.1:1/none?sslmode=disable&connect_timeout=1"),
		&gorm.Config{DisableAutomaticPing: true, Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("构造懒连接数据库失败: %v", err)
	}
	return db
}

// 【单元】状态机守卫：会话处于非初始态时调用 Precharge 直接报错，
// 不触发任何数据库写入、不改变会话状态（数据库为不可达懒连接，
// 任何写入尝试都会以连接错误暴露，与状态守卫错误可区分）。
func TestBillingSessionPrechargeRejectedWhenNotInit(t *testing.T) {
	db := newBrokenDB(t)
	for _, st := range []sessionState{statePrecharged, stateSettled, stateRefunded, stateSettleFailed} {
		s := &BillingSession{
			db: db, billing: billing.NewService(db),
			requestID: "req-state-guard", userID: 1, keyID: 1,
			state: st, precharged: 3000,
		}
		err := s.Precharge(context.Background(), 100)
		if err == nil {
			t.Fatalf("状态 %d 下 Precharge 应报错", st)
		}
		if s.state != st {
			t.Fatalf("Precharge 被拒后不得改变状态，期望 %d 实际 %d", st, s.state)
		}
		if s.precharged != 3000 {
			t.Fatalf("Precharge 被拒后不得改变已预扣金额，实际 %d", s.precharged)
		}
	}
}

// 【单元】结算写入失败：会话转入结算失败态，兜底与显式退款均被封锁。
// 结算差额为 0 时也必须尝试写零额 settle_adjust 终态标记
// （若代码在零差额时跳过写入，本测试的 Settle 会"成功"而失败）。
func TestBillingSessionSettleFailureBlocksRefund(t *testing.T) {
	db := newBrokenDB(t)
	s := &BillingSession{
		db: db, billing: billing.NewService(db),
		requestID: "req-settle-fail", userID: 1, keyID: 1,
		state: statePrecharged, precharged: 3000,
	}

	// final == precharged：零差额也要写终态标记，写入失败必须报错
	if err := s.Settle(context.Background(), 3000); err == nil {
		t.Fatal("结算写入失败时 Settle 应返回错误")
	}
	if s.state != stateSettleFailed {
		t.Fatalf("结算失败后状态应为 stateSettleFailed，实际 %d", s.state)
	}

	// defer 兜底不得退款：状态保持结算失败态
	s.EnsureFinal(context.Background())
	if s.state != stateSettleFailed {
		t.Fatalf("EnsureFinal 不得在结算失败态触发退款，状态被改为 %d", s.state)
	}

	// 显式退款同样幂等静默、不改状态、不写流水
	if err := s.Refund(context.Background()); err != nil {
		t.Fatalf("结算失败态下 Refund 应幂等静默，实际返回错误: %v", err)
	}
	if s.state != stateSettleFailed {
		t.Fatalf("Refund 不得改变结算失败态，状态被改为 %d", s.state)
	}
}
