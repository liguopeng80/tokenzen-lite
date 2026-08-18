package relay

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"gorm.io/gorm"

	"github.com/liguopeng80/tokenzen-lite/server/internal/billing"
	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/obs"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// ErrKeyDepleted API Key 独立额度不足。C1 后规范定义上移到 billing 包；
// 这里保留为 billing.ErrKeyDepleted 的赋值别名，沿用既有的包级符号引用
// （中继与 api 层的错误判定无需改动），不引入新的错误对象。
var ErrKeyDepleted = billing.ErrKeyDepleted

// sessionState 计费会话状态（单向推进）。
type sessionState int

const (
	stateInit sessionState = iota
	statePrecharged
	stateSettled
	stateRefunded
	// stateSettleFailed 结算写入失败：预扣保留，交由孤儿预扣清理补偿。
	// 不允许再退款——结算写入可能"超时但事务已提交"，此时立即退款会造成
	// settle_adjust 与 refund 双重入账（entry_type 不同，幂等索引不去重）。
	stateSettleFailed
)

// BillingSession 实现一次请求的预扣→结算→退款状态机。
// 并发安全：mutex 保证结算与退款只发生其一、只发生一次。
//
// C1 后所有 api_keys.credit_used 写入内聚到 billing.applyTx 同事务，
// 会话不再持有任何裸 SQL 维护 credit_used：预扣条件 UPDATE、结算/退款 GREATEST
// UPDATE、命中上限时 depleted 标记，均由 billing.Service.Apply 在事务内完成。
type BillingSession struct {
	mu    sync.Mutex
	state sessionState

	db        *gorm.DB
	billing   *billing.Service
	requestID string
	userID    int64
	keyID     int64

	precharged    domain.Credits
	dailyLimit    domain.Credits // 用户当日花费上限，透传给 billing.Adjustment 做权威校验；0 不限制
	keyDailyLimit domain.Credits // Key 当日花费上限，透传给 consume Adjustment；0 不限制
}

// NewBillingSession 创建计费会话。dailyLimit 为用户当日花费上限，0 表示不限制；
// Key 级上限取自 key.DailySpendLimit（与用户级同口径，0 不限制）。两者均透传给
// Precharge 的 consume Adjustment，在 billing.applyTx 同事务内做权威校验与条件
// UPDATE（用户行锁串行化同一用户的并发调整，Key 维度亦被串行化），闭合
// checkDailySpendLimit 预检与扣费之间的 TOCTOU。密钥独立额度上限（api_keys.
// credit_limit）由 applyKeyCredit 直接读 DB 列判定，无需内存副本。
func NewBillingSession(db *gorm.DB, svc *billing.Service, requestID string, key *store.APIKey,
	dailyLimit domain.Credits) *BillingSession {
	return &BillingSession{
		db: db, billing: svc, requestID: requestID,
		userID: key.UserID, keyID: key.ID,
		dailyLimit: dailyLimit, keyDailyLimit: key.DailySpendLimit,
	}
}

// Precharged 返回已预扣金额。
func (s *BillingSession) Precharged() domain.Credits { return s.precharged }

// Precharge 预扣：在同一事务内累计 Key 已用额度（设有 credit_limit 上限时原子校验）、
// 命中上限时同事务标记 depleted、扣用户余额、写当日计数与流水。
//
// C1 重写：原本的 4 处裸 SQL（条件 UPDATE、depleted 标记、billing.Apply、失败手动
// 回滚）收敛为单次 billing.Service.Apply。事务原子性取代手动回滚——预扣后用户余额
// 不足或日上限命中等任一失败，整个事务（含 api_keys.credit_used 累计）一并回滚。
// 命中密钥额度上限时 applyTx 同事务标记 depleted，Apply 据此吞错提交以持久化标记，
// 并把 ErrKeyDepleted 透传给本方法。
func (s *BillingSession) Precharge(ctx context.Context, amount domain.Credits) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state != stateInit {
		return fmt.Errorf("计费会话状态错误：重复预扣")
	}
	if amount < 0 {
		return fmt.Errorf("预扣金额不能为负")
	}
	_, err := s.billing.Apply(ctx, billing.Adjustment{
		UserID: s.userID, Amount: -amount, EntryType: domain.LedgerConsume,
		RefType: "api_key", RefID: s.keyID, RequestID: s.requestID,
		AffectsUsed: true, Note: "请求预扣",
		DailyLimit: s.dailyLimit, KeyID: s.keyID, KeyDailyLimit: s.keyDailyLimit,
		// 预扣条件 UPDATE 语义：applyTx 在用户行锁前先做
		//   UPDATE api_keys SET credit_used = credit_used + ?
		//   WHERE id=? AND (credit_limit IS NULL OR credit_used + ? <= credit_limit)
		// 命中上限时同事务标记 depleted 并返回 ErrKeyDepleted（fail-fast，不取用户锁）。
		// credit_limit 由 applyKeyCredit 直接读 DB 列判定（无内存副本）。
		KeyPrechargeMode: true,
	})
	if err != nil {
		return err
	}
	s.precharged = amount
	s.state = statePrecharged
	return nil
}

// Settle 按真实消耗结算：差额多退少补；补扣超出余额时截断（宁少扣不拒绝）。
// 结算差额为 0 时也写一条零额 settle_adjust 流水作为终态标记——孤儿预扣清理
// 只依赖流水判定终态（用量日志允许丢弃，不可作为终态依据）。
// 数据库写入使用脱离请求取消、带截止时间的上下文（见 background.go）；
// 超时按失败返回，会话转入结算失败态：预扣保留、不再退款，由孤儿预扣清理补偿。
//
// C1 后 api_keys.credit_used 的 GREATEST 符号差 UPDATE 由 applyKeyCredit 在
// billing.applyTx 同事务内完成（KeyPrechargeMode=false 走结算/退款路径），不再
// 在本方法裸 SQL 维护。
func (s *BillingSession) Settle(ctx context.Context, final domain.Credits) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state != statePrecharged {
		return nil // 已结算/已退款/未预扣：幂等静默
	}
	wctx, cancel := detachedWriteCtx(ctx)
	defer cancel()
	diff := final - s.precharged
	_, err := s.billing.Apply(wctx, billing.Adjustment{
		UserID: s.userID, Amount: -diff, EntryType: domain.LedgerSettleAdjust,
		RefType: "api_key", RefID: s.keyID, RequestID: s.requestID,
		AffectsUsed: true, ClampToBalance: true, Note: "结算差额",
		KeyID: s.keyID,
		// 结算/退款路径（KeyPrechargeMode 默认 false）：applyKeyCredit 在用户余额
		// 更新后做 GREATEST(credit_used - amount, 0) UPDATE，amount 为 clamp 后的
		// 有效金额，与 users/ledger/daily_spend 同口径。
	})
	if err != nil && !errors.Is(err, billing.ErrDuplicateEntry) {
		// 写入可能"超时但事务已提交"，立即退款会与已入账的 settle_adjust
		// 双重入账，因此转入结算失败态封锁退款，交由孤儿预扣清理补偿：
		// settle_adjust 已入账则不判孤儿；未入账则超阈值后全额补退。
		s.state = stateSettleFailed
		obs.Logger(ctx).Error("结算差额写入失败（预扣保留，交由孤儿预扣清理补偿）",
			"error", err, "request_id", s.requestID, "diff", diff)
		return err
	}
	// request_count 与 last_used_at 是非计费维度的统计/展示字段，保留在 relay 侧
	// 直接更新（C1 设计点 5：不计入 billing.applyTx 的事务边界）。
	if res := s.db.WithContext(wctx).Exec(
		`UPDATE users SET request_count = request_count + 1 WHERE id = ?`, s.userID); res.Error != nil {
		obs.Logger(ctx).Warn("累计用户请求数失败",
			"request_id", s.requestID, "user_id", s.userID, "error", res.Error)
	}
	if res := s.db.WithContext(wctx).Exec(
		`UPDATE api_keys SET last_used_at = now() WHERE id = ?`, s.keyID); res.Error != nil {
		obs.Logger(ctx).Warn("更新密钥最近使用时间失败",
			"request_id", s.requestID, "api_key_id", s.keyID, "error", res.Error)
	}
	s.state = stateSettled
	obs.Logger(ctx).Info("计费结算完成",
		"request_id", s.requestID, "precharged", s.precharged, "charged", final)
	return nil
}

// Refund 全额退款（请求失败、未产生任何消耗）。
// 数据库写入使用脱离请求取消、带截止时间的上下文（见 background.go）；
// 超时按失败返回，预扣保留在会话内，由孤儿预扣清理补退。
// 退款重试安全：refund 流水受 (request_id, entry_type) 幂等索引保护，
// "超时但事务已提交"后的重试命中 ErrDuplicateEntry，按成功处理。
//
// C1 后 api_keys.credit_used 的 GREATEST 回减 UPDATE 由 applyKeyCredit 在同事务内
// 完成，不再在本方法裸 SQL 维护。
func (s *BillingSession) Refund(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state != statePrecharged {
		return nil // 幂等静默
	}
	if s.precharged > 0 {
		wctx, cancel := detachedWriteCtx(ctx)
		defer cancel()
		_, err := s.billing.Apply(wctx, billing.Adjustment{
			UserID: s.userID, Amount: s.precharged, EntryType: domain.LedgerRefund,
			RefType: "api_key", RefID: s.keyID, RequestID: s.requestID,
			AffectsUsed: true, Note: "请求失败退款",
			KeyID: s.keyID,
			// KeyPrechargeMode 默认 false：applyKeyCredit 走 GREATEST 回减路径，
			// 把 credit_used 同事务减回（KeyID 非 0 触发）。
		})
		if err != nil && !errors.Is(err, billing.ErrDuplicateEntry) {
			obs.Logger(ctx).Error("退款失败（交由对账补偿）", "error", err,
				"request_id", s.requestID, "amount", s.precharged)
			return err
		}
	}
	s.state = stateRefunded
	obs.Logger(ctx).Info("预扣已退款", "request_id", s.requestID, "amount", s.precharged)
	return nil
}

// EnsureFinal 兜底：会话结束时仍处于预扣态则强制退款（defer 调用）。
// 结算失败态不退款——预扣保留，由孤儿预扣清理按流水终态判定后补偿。
func (s *BillingSession) EnsureFinal(ctx context.Context) {
	s.mu.Lock()
	pending := s.state == statePrecharged
	s.mu.Unlock()
	if pending {
		obs.Logger(ctx).Warn("会话未终结，触发兜底退款", "request_id", s.requestID)
		_ = s.Refund(ctx)
	}
}
