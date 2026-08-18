// Package billing 实现积分账本核心：余额原子调整、流水记账、兑换码核销。
// 不变式：users.credit_balance == SUM(credit_ledger.amount)，
// 任何余额变动必须与流水写入在同一事务内完成。
package billing

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/liguopeng80/tokenzen-lite/server/internal/auth"
	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/obs"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// ErrInsufficientCredits 余额不足。
var ErrInsufficientCredits = errors.New("积分余额不足")

// ErrKeyDepleted API Key 独立额度（credit_limit）耗尽。
// applyTx 在预扣条件 UPDATE 命中上限时同事务把 api_keys.status 置为 depleted，
// Apply 据此吞错提交（持久化 depleted 标记）并把本错误透传给调用方。
// 由 relay 包通过赋值别名复用（relay.ErrKeyDepleted = billing.ErrKeyDepleted），
// 保留包级符号以兼容既有的中继与 api 层错误判定。
var ErrKeyDepleted = errors.New("密钥额度不足")

// ErrDailyLimitExceeded 当日累计扣费将突破用户上限。
// 权威校验在 applyTx 内、持有用户行锁且写 daily_spend 之前完成，闭合 relay 层
// 预检（checkDailySpendLimit）与扣费之间的 TOCTOU：N 个并发请求不能同时过检后串行写库。
var ErrDailyLimitExceeded = errors.New("当日花费上限")

// ErrKeyDailyLimitExceeded 当日累计扣费将突破 Key 上限。
// 与 ErrDailyLimitExceeded 同范式：权威校验在 applyTx 内、用户行锁覆盖的同一事务内、
// 写 daily_spend_by_key 之前完成（同一用户的并发调整被用户行锁串行化，Key 维度亦被串行化）。
var ErrKeyDailyLimitExceeded = errors.New("Key 当日花费上限")

// ProjectedDailySpendExceeds 判定本次扣费后当日累计是否超出每日上限（纯函数）。
// 用户级与 Key 级权威校验共用：limit <= 0 表示不限制（恒放行），projected > limit 才视为超出
// （恰好等于上限放行）。delta 为本次预扣额（正数）。
//
// 负数 limit 视同不限制（防御性）：负值在上游（API 入参校验、迁移 CHECK 约束）已被拒绝，
// 此处仅作兜底，避免计数被误判为超限。
func ProjectedDailySpendExceeds(spent, delta, limit domain.Credits) bool {
	if limit <= 0 {
		return false
	}
	return spent+delta > limit
}

// ErrDuplicateEntry 同一请求同一类型的流水已存在（幂等重放）。
var ErrDuplicateEntry = errors.New("流水已存在")

// ErrRedemptionUnavailable 兑换码不可用。下面四个具体原因都归入它，
// 调用方既可以只判「不可用」，也可以按具体原因给出可操作的提示——
// 只说「无效或已被使用」会让拿到过期码的员工反复重试或去查自己的输入。
var (
	ErrRedemptionUnavailable = errors.New("兑换码不可用")
	ErrRedemptionNotFound    = fmt.Errorf("兑换码不存在: %w", ErrRedemptionUnavailable)
	ErrRedemptionUsed        = fmt.Errorf("兑换码已被使用: %w", ErrRedemptionUnavailable)
	ErrRedemptionDisabled    = fmt.Errorf("兑换码已作废: %w", ErrRedemptionUnavailable)
	ErrRedemptionExpired     = fmt.Errorf("兑换码已过期: %w", ErrRedemptionUnavailable)
)

// Service 积分账本服务。
type Service struct{ db *gorm.DB }

func NewService(db *gorm.DB) *Service { return &Service{db: db} }

// Adjustment 描述一次余额调整。
type Adjustment struct {
	UserID    int64
	Amount    domain.Credits // 有符号：正=入账，负=扣减
	EntryType domain.LedgerEntryType
	RefType   string
	RefID     int64
	RequestID string
	Note      string
	// OperatorID 发起该笔调整的管理员，0 表示消费/退款/兑换等非管理动作。
	OperatorID int64
	// AffectsUsed 为 true 时同步累计 credit_used 与当日花费计数
	// （consume/refund/settle_adjust）。两者与流水在同一事务内维护，
	// 因此不会出现「扣了积分但没计数」的偏差。
	AffectsUsed bool
	// ClampToBalance 为 true 时，扣减超出余额则只扣到 0（结算补扣场景，
	// 宁可少扣不可拒绝——请求已经完成，"用了没扣"的损失计入流水备注）。
	ClampToBalance bool
	// DailyLimit 当日花费上限（积分）。>0 时，applyTx 在持有用户行锁且写 daily_spend 之前
	// 做权威校验：本次扣减后当日累计将超限则回滚事务返回 ErrDailyLimitExceeded。
	// 仅 consume 类调整（amount<0）需要设置；refund/settle_adjust 不设，不触发。
	// 0 表示不限制。
	DailyLimit domain.Credits
	// KeyID 该笔调整归属的 API Key。非 0 时，applyTx 在同事务内同步维护
	// daily_spend_by_key 计数；与 KeyDailyLimit 配合做 Key 级每日上限权威校验。
	// refund/settle_adjust 也设置（同源回减计数），但不设 KeyDailyLimit（不做校验）。
	KeyID int64
	// KeyDailyLimit Key 级当日花费上限（积分）。>0 且 KeyID 非 0 时，applyTx 在写
	// daily_spend_by_key 之前做权威校验，超限返回 ErrKeyDailyLimitExceeded。
	// 仅 consume 类调整（amount<0）设置；0 表示该 Key 不限制。
	KeyDailyLimit domain.Credits
	// KeyPrechargeMode 选择 api_keys.credit_used 的维护语义：
	//   true  → 预扣条件 UPDATE（WHERE credit_used+?<=credit_limit，命中返回 ErrKeyDepleted），
	//           置于用户行锁之前以保持 fail-fast；
	//   false → 结算/退款 GREATEST 符号差 UPDATE，置于用户余额更新之后，
	//           不触发上限判定。
	// 默认 false 保持 grant/redeem/orphan 退款等既有调用方行为不变。
	KeyPrechargeMode bool
}

// Apply 在独立事务中执行一次调整。
//
// 预扣命中密钥额度上限（ErrKeyDepleted）时，applyTx 已同事务把 api_keys.status
// 置为 depleted——这里吞错提交以持久化 depleted 标记，并把 ErrKeyDepleted 透传给
// 调用方。其他错误照常回滚事务。
func (s *Service) Apply(ctx context.Context, adj Adjustment) (*store.LedgerEntry, error) {
	var entry *store.LedgerEntry
	var prechargeErr error
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		entry, err = s.applyTx(ctx, tx, adj)
		if errors.Is(err, ErrKeyDepleted) {
			// 命中密钥上限：depleted 标记需提交持久化，吞错由外层透传。
			// 此事务内对 api_keys 的两处 UPDATE（条件累计、depleted 标记）一起提交；
			// 用户余额、流水、当日花费均未写入，无需回滚。
			prechargeErr = err
			return nil
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	return entry, prechargeErr
}

// applyTx 在给定事务内执行调整。行级锁串行化同一用户的并发变更。
//
// 结构（C1 重写）：
//  1. 预扣模式（KeyPrechargeMode）：先于用户行锁做 api_keys 条件 UPDATE，
//     命中 credit_limit 上限时同事务标记 depleted 并返回 ErrKeyDepleted——fail-fast，
//     不浪费用户行锁获取。
//  2. 用户行锁 → 余额校验/截断 → 每日上限权威校验 → 写 users.credit_balance/credit_used
//     → 写 daily_spend / daily_spend_by_key。
//  3. 结算/退款模式（!KeyPrechargeMode）：用户余额更新后做 api_keys GREATEST 符号差 UPDATE。
//  4. 写流水（(request_id, entry_type) 幂等）。
//
// credit_used 与 users/ledger/daily_spend 同事务提交，消除中继侧裸 SQL 维护带来的双轨漂移。
func (s *Service) applyTx(ctx context.Context, tx *gorm.DB, adj Adjustment) (*store.LedgerEntry, error) {
	// 统一锁顺序为 api_keys → users：KeyID 非 0 时先取 api_keys 行锁，使预扣模式
	//（随后 applyKeyCredit）与结算/退款模式（末尾 applyKeyCredit）都在持有 api_keys
	// 锁后再取 users 锁，消除两模式跨相并发的 lock-order 死锁（F8 评审 HIGH）。
	// 预扣命中上限时仍在此之后 fail-fast 返回、不取 users 锁。
	if adj.KeyID != 0 {
		var ignore int
		if err := tx.Raw(`SELECT 1 FROM api_keys WHERE id = ? FOR UPDATE`, adj.KeyID).Scan(&ignore).Error; err != nil {
			return nil, fmt.Errorf("锁定密钥行失败: %w", err)
		}
	}

	// 1. 预扣模式：先于用户行锁做条件 UPDATE，fail-fast 命中上限。
	if adj.KeyPrechargeMode {
		if err := s.applyKeyCredit(ctx, tx, adj, adj.Amount); err != nil {
			return nil, err
		}
	}

	var row struct {
		CreditBalance domain.Credits
		CreditUsed    domain.Credits
		DepartmentID  *int64
		IntegrationID *int64
	}
	// 部门与接入方在锁内读取，作为流水的记账时点快照：报表按快照聚合，
	// 用户转部门或被迁移到其他接入方后，已出账月份的口径保持不变。
	res := tx.Raw(`SELECT credit_balance, credit_used, department_id, integration_id
		FROM users WHERE id = ? FOR UPDATE`, adj.UserID).Scan(&row)
	if res.Error != nil {
		return nil, fmt.Errorf("锁定用户余额失败: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return nil, store.ErrNotFound
	}

	amount := adj.Amount
	newBalance := row.CreditBalance + amount
	if newBalance < 0 {
		if !adj.ClampToBalance {
			return nil, ErrInsufficientCredits
		}
		shortfall := -newBalance
		amount = -row.CreditBalance
		newBalance = 0
		adj.Note = fmt.Sprintf("%s（余额不足，欠扣 %d 积分）", adj.Note, shortfall)
		obs.Logger(ctx).Warn("结算补扣超出余额，已截断",
			"user_id", adj.UserID, "shortfall", shortfall, "request_id", adj.RequestID)
		// 截断后账本仍平（balance==Σledger），reconcile 无法发现这类「已计费的应付额被抹掉」，
		// 必须单独计数才能在指标层面暴露系统性欠扣。
		obs.RecordClampShortfall(shortfall)
	}

	day := store.SpendDay(time.Now())
	if err := validateDailyLimits(tx, adj, amount, day); err != nil {
		return nil, err
	}

	updates := map[string]any{"credit_balance": newBalance, "updated_at": time.Now()}
	if adj.AffectsUsed {
		// 消耗为负数流水，credit_used 累加其绝对值；退款/退差则回减
		updates["credit_used"] = row.CreditUsed - amount
	}
	if err := tx.Model(&store.User{}).Where("id = ?", adj.UserID).Updates(updates).Error; err != nil {
		return nil, fmt.Errorf("更新余额失败: %w", err)
	}
	if adj.AffectsUsed {
		// 扣费为负数流水，当日花费累加其绝对值；退款与结算退差回减。
		// spend 写入收口在 billing 包内（P3-13），store 仅暴露读路径。
		if err := addDailySpend(tx, adj.UserID, day, -amount); err != nil {
			return nil, fmt.Errorf("更新当日花费计数失败: %w", err)
		}
		if adj.KeyID != 0 {
			if err := addKeyDailySpend(tx, adj.KeyID, day, -amount); err != nil {
				return nil, fmt.Errorf("更新 Key 当日花费计数失败: %w", err)
			}
		}
	}

	// 3. 结算/退款模式：用户余额更新后做 GREATEST 符号差 UPDATE。
	if !adj.KeyPrechargeMode {
		if err := s.applyKeyCredit(ctx, tx, adj, amount); err != nil {
			return nil, err
		}
	}

	var departmentID int64
	if row.DepartmentID != nil {
		departmentID = *row.DepartmentID
	}
	var integrationID int64
	if row.IntegrationID != nil {
		integrationID = *row.IntegrationID
	}
	return writeLedgerEntry(tx, adj, amount, newBalance, departmentID, integrationID)
}

// validateDailyLimits 在持有用户行锁的事务内、写 daily_spend 之前，校验用户级与
// Key 级当日花费上限。仅 consume 类（amount<0）触发；refund/settle_adjust 不设上限。
// 用户行锁串行化同一用户的并发调整，闭合 relay 层预检与扣费之间的 TOCTOU。
func validateDailyLimits(tx *gorm.DB, adj Adjustment, amount domain.Credits, day time.Time) error {
	if !adj.AffectsUsed || amount >= 0 {
		return nil
	}
	if adj.DailyLimit > 0 {
		var spendRow struct{ Credits domain.Credits }
		if qerr := tx.Raw(
			`SELECT credits FROM daily_spend WHERE user_id = ? AND day = (? AT TIME ZONE ?)::date`,
			adj.UserID, day, store.LocalZoneName()).Scan(&spendRow).Error; qerr != nil &&
			!errors.Is(qerr, gorm.ErrRecordNotFound) {
			return fmt.Errorf("查询当日花费失败: %w", qerr)
		}
		if ProjectedDailySpendExceeds(spendRow.Credits, -amount, adj.DailyLimit) {
			return fmt.Errorf("%w：当日已扣 %d，本次预扣 %d，合计 %d 超过上限 %d",
				ErrDailyLimitExceeded, spendRow.Credits, -amount,
				spendRow.Credits+(-amount), adj.DailyLimit)
		}
	}
	if adj.KeyID != 0 && adj.KeyDailyLimit > 0 {
		var keySpendRow struct{ Credits domain.Credits }
		if qerr := tx.Raw(
			`SELECT credits FROM daily_spend_by_key WHERE api_key_id = ? AND day = (? AT TIME ZONE ?)::date`,
			adj.KeyID, day, store.LocalZoneName()).Scan(&keySpendRow).Error; qerr != nil &&
			!errors.Is(qerr, gorm.ErrRecordNotFound) {
			return fmt.Errorf("查询 Key 当日花费失败: %w", qerr)
		}
		if ProjectedDailySpendExceeds(keySpendRow.Credits, -amount, adj.KeyDailyLimit) {
			return fmt.Errorf("%w：Key 当日已扣 %d，本次预扣 %d，合计 %d 超过上限 %d",
				ErrKeyDailyLimitExceeded, keySpendRow.Credits, -amount,
				keySpendRow.Credits+(-amount), adj.KeyDailyLimit)
		}
	}
	return nil
}

// applyKeyCredit 维护 api_keys.credit_used，把原本散落在 relay/session.go（4 处）
// 与 billing/orphan.go（1 处）的裸 SQL 收敛到唯一事务入口（C1 / P1-1）。
//
// 短路：KeyID==0（admin 发放/兑换等无密钥场景）或 !AffectsUsed 时直接返回。
//
// 两种语义由 KeyPrechargeMode 切换：
//   - true（预扣）：原子条件 UPDATE（WHERE credit_used+?<=credit_limit，DB 列 credit_limit
//     为 NULL 时恒放行——无独立上限的密钥）。命中上限时
//     同事务把 status 从 enabled 翻为 depleted，返回 ErrKeyDepleted；事务外层 Apply
//     据此吞错提交以持久化 depleted 标记。
//   - false（结算/退款）：GREATEST 符号差 UPDATE（credit_used - amount），amount 为
//     applyTx 内 possibly-clamped 的有效金额。amount==0 跳过（零额 settle 终态标记）。
//
// amount 形参允许调用方传入 clamp 后的有效金额（结算路径），与 users/ledger/daily_spend
// 同口径，消除「settle 超额截断时 credit_used 仍按原始 diff 累计」的旧缺陷。
func (s *Service) applyKeyCredit(ctx context.Context, tx *gorm.DB, adj Adjustment, amount domain.Credits) error {
	if adj.KeyID == 0 || !adj.AffectsUsed {
		return nil
	}
	if adj.KeyPrechargeMode {
		// -amount 为正数累计增量（amount 为consume 类负数流水）。
		// WHERE 子句的 credit_limit IS NULL 分支覆盖无独立上限的密钥。
		res := tx.Exec(`
			UPDATE api_keys SET credit_used = credit_used + ?, updated_at = now()
			WHERE id = ? AND (credit_limit IS NULL OR credit_used + ? <= credit_limit)`,
			-amount, adj.KeyID, -amount)
		if res.Error != nil {
			return fmt.Errorf("占用密钥额度失败: %w", res.Error)
		}
		if res.RowsAffected == 0 {
			// 命中 credit_limit 上限：同事务标记 depleted，仅从 enabled 转换以避免
			// 覆盖手工禁用等状态。credit_limit IS NOT NULL 同时排除「密钥已被删除」。
			mark := tx.Exec(`
				UPDATE api_keys SET status = ?, updated_at = now()
				WHERE id = ? AND status = ? AND credit_limit IS NOT NULL`,
				domain.KeyDepleted, adj.KeyID, domain.KeyEnabled)
			if mark.Error != nil {
				obs.Logger(ctx).Warn("标记密钥额度耗尽状态失败",
					"request_id", adj.RequestID, "api_key_id", adj.KeyID, "error", mark.Error)
			} else if mark.RowsAffected > 0 {
				obs.Logger(ctx).Info("密钥独立额度耗尽，状态置为 depleted",
					"request_id", adj.RequestID, "api_key_id", adj.KeyID)
			}
			return ErrKeyDepleted
		}
		return nil
	}
	// 结算/退款：GREATEST 符号差（credit_used - amount），amount=0 跳过。
	if amount == 0 {
		return nil
	}
	if err := tx.Exec(`
		UPDATE api_keys SET credit_used = GREATEST(credit_used - ?, 0), updated_at = now()
		WHERE id = ?`, amount, adj.KeyID).Error; err != nil {
		return fmt.Errorf("调整密钥已用额度失败: %w", err)
	}
	return nil
}

// writeLedgerEntry 写入流水，命中 (request_id, entry_type) 唯一索引时返回 ErrDuplicateEntry。
func writeLedgerEntry(tx *gorm.DB, adj Adjustment, amount, balance domain.Credits,
	departmentID, integrationID int64) (*store.LedgerEntry, error) {
	entry := &store.LedgerEntry{
		UserID:        adj.UserID,
		EntryType:     adj.EntryType,
		Amount:        amount,
		BalanceAfter:  balance,
		RefType:       adj.RefType,
		RefID:         adj.RefID,
		RequestID:     adj.RequestID,
		Note:          adj.Note,
		OperatorID:    adj.OperatorID,
		DepartmentID:  departmentID,
		IntegrationID: integrationID,
	}
	if err := tx.Create(entry).Error; err != nil {
		if isUniqueViolation(err) {
			return nil, ErrDuplicateEntry
		}
		return nil, fmt.Errorf("写入流水失败: %w", err)
	}
	return entry, nil
}

// addDailySpend 在给定事务内累加用户当日花费计数。delta 为有符号：
// 预扣计正，退款与结算退差计负，与 credit_used 的累计口径一致。
//
// 收口在 billing 包内（P3-13）：store 仅暴露读路径，写 spend 的唯一入口是 billing.applyTx，
// 保证与余额/流水同事务，杜绝「扣了积分但没计数」的偏差。
//
// day 由调用方经 SpendDay 算成服务器本地零点；写入显式按服务器时区换算为 DATE，
// 规避 pgx 把 time.Time 当 timestamptz 发送、再被 PG 按会话时区隐式截断。
func addDailySpend(tx *gorm.DB, userID int64, day time.Time, delta domain.Credits) error {
	if delta == 0 {
		return nil
	}
	return tx.Exec(`
		INSERT INTO daily_spend (user_id, day, credits, updated_at)
		VALUES (?, (? AT TIME ZONE ?)::date, ?, now())
		ON CONFLICT (user_id, day) DO UPDATE
		SET credits = daily_spend.credits + EXCLUDED.credits, updated_at = now()`,
		userID, day, store.LocalZoneName(), delta).Error
}

// addKeyDailySpend 在给定事务内累加某 Key 的当日花费。与 addDailySpend 同口径。
func addKeyDailySpend(tx *gorm.DB, keyID int64, day time.Time, delta domain.Credits) error {
	if delta == 0 || keyID == 0 {
		return nil
	}
	return tx.Exec(`
		INSERT INTO daily_spend_by_key (api_key_id, day, credits, updated_at)
		VALUES (?, (? AT TIME ZONE ?)::date, ?, now())
		ON CONFLICT (api_key_id, day) DO UPDATE
		SET credits = daily_spend_by_key.credits + EXCLUDED.credits, updated_at = now()`,
		keyID, day, store.LocalZoneName(), delta).Error
}

// isUniqueViolation 判断错误是否为唯一约束冲突（幂等索引命中）。
func isUniqueViolation(err error) bool {
	return err != nil && (errors.Is(err, gorm.ErrDuplicatedKey) ||
		// pgx 的 23505 错误文本兜底
		strings.Contains(err.Error(), "23505") ||
		strings.Contains(err.Error(), "duplicate key"))
}

// AdminRequestIDPrefix 隔离管理侧幂等键与中继侧请求标识两个命名空间，
// 防止两者取值碰撞后互相判定为重放。
const AdminRequestIDPrefix = "admin:"

// AdminRequestID 把管理侧幂等键编码为流水的 request_id。
// 空键返回空串，表示不参与幂等判定（各自记账，与既有调用方行为一致）。
func AdminRequestID(idempotencyKey string) string {
	if idempotencyKey == "" {
		return ""
	}
	return AdminRequestIDPrefix + idempotencyKey
}

// Grant 管理员分配（amount>0）或扣回（amount<0）积分。
// idempotencyKey 非空时参与 (request_id, entry_type) 唯一索引，
// 重复提交返回 ErrDuplicateEntry，由调用方回查首次结果。
func (s *Service) Grant(ctx context.Context, userID int64, amount domain.Credits,
	actorID int64, note, idempotencyKey string) (*store.LedgerEntry, error) {

	if amount == 0 {
		return nil, fmt.Errorf("调整金额不能为 0")
	}
	entryType := domain.LedgerGrant
	if amount < 0 {
		entryType = domain.LedgerRevoke
	}
	return s.Apply(ctx, Adjustment{
		UserID: userID, Amount: amount, EntryType: entryType,
		RefType: "admin_user", RefID: actorID, OperatorID: actorID,
		RequestID: AdminRequestID(idempotencyKey), Note: note,
	})
}

// Redeem 核销兑换码：状态原子翻转 + 入账在同一事务，天然防并发重复核销。
func (s *Service) Redeem(ctx context.Context, userID int64, codePlain string) (*store.LedgerEntry, error) {
	codeHash := auth.HashKey(codePlain)
	var entry *store.LedgerEntry
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var red store.Redemption
		res := tx.Raw(`
			UPDATE redemptions
			SET status = 'used', used_by_user_id = ?, redeemed_at = now()
			WHERE code_hash = ? AND status = 'unused'
			  AND (expires_at IS NULL OR expires_at > now())
			RETURNING id, credits, name`, userID, codeHash).Scan(&red)
		if res.Error != nil {
			return fmt.Errorf("核销兑换码失败: %w", res.Error)
		}
		if res.RowsAffected == 0 {
			return redemptionUnavailableReason(tx, codeHash)
		}
		var err error
		entry, err = s.applyTx(ctx, tx, Adjustment{
			UserID: userID, Amount: red.Credits, EntryType: domain.LedgerRedeem,
			RefType: "redemption", RefID: red.ID, Note: red.Name,
		})
		return err
	})
	if err != nil {
		return nil, err
	}
	obs.Logger(ctx).Info("兑换码核销成功", "user_id", userID, "credits", entry.Amount)
	return entry, nil
}

// redemptionUnavailableReason 在核销条件未命中后回查该码，判定具体原因。
// 与核销在同一事务内查询：状态在两次语句之间被改动时，这里读到的是
// 事务快照里的最新值，不会给出与核销结果矛盾的解释。
func redemptionUnavailableReason(tx *gorm.DB, codeHash string) error {
	var row struct {
		Status    domain.RedemptionStatus
		ExpiresAt *time.Time
	}
	res := tx.Raw(`SELECT status, expires_at FROM redemptions WHERE code_hash = ?`,
		codeHash).Scan(&row)
	if res.Error != nil {
		return fmt.Errorf("查询兑换码状态失败: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return ErrRedemptionNotFound
	}
	switch domain.EffectiveRedemptionStatus(row.Status, row.ExpiresAt, time.Now()) {
	case domain.RedemptionUsed:
		return ErrRedemptionUsed
	case domain.RedemptionDisabled:
		return ErrRedemptionDisabled
	case domain.RedemptionExpired:
		return ErrRedemptionExpired
	default:
		// 展示态为「未使用」却没能核销：并发核销刚把它改走，或状态取值超出已知枚举。
		return ErrRedemptionUnavailable
	}
}
