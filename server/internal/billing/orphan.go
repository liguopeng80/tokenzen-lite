// 孤儿预扣清理：进程异常退出（崩溃、被杀）会留下只有预扣（consume）流水、
// 既无结算也无退款的请求。此类请求满足"余额 == 流水之和"的对账不变式，
// 常规对账发现不了，必须按请求维度扫描并补退款。
package billing

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm/clause"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/obs"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// DefaultOrphanPrechargeThreshold 孤儿预扣判定阈值：预扣写入超过该时长
// 仍无终态流水（结算/退款）即视为孤儿。取值需大于上游请求最长耗时，
// 避免把仍在途的长流式请求误判为孤儿。
const DefaultOrphanPrechargeThreshold = 15 * time.Minute

// orphanPrechargeNote 补退款流水与追溯日志的统一备注。
const orphanPrechargeNote = "孤儿预扣清理退款（服务中断未结算）"

// OrphanPrecharge 是一条被判定为孤儿的预扣流水。
type OrphanPrecharge struct {
	UserID    int64
	RequestID string
	Amount    domain.Credits // 预扣金额（正数）
	APIKeyID  int64
	CreatedAt time.Time
}

// OrphanCleanupResult 一次清理的执行结果。
type OrphanCleanupResult struct {
	Scanned         int            // 扫描命中的孤儿预扣条数
	Refunded        int            // 本次成功补写退款的条数
	AlreadyHandled  int            // 幂等命中（他处已退款）的条数
	RefundedCredits domain.Credits // 本次补退的积分合计
}

// ScanOrphanPrecharges 只扫描不退款，供对账巡检报告积压情况。
// 判定口径与 CleanupOrphanPrecharges 完全一致，两者共用本方法，
// 避免巡检报的数与回收实际处理的数出现分叉。
func (s *Service) ScanOrphanPrecharges(ctx context.Context, olderThan time.Duration) ([]OrphanPrecharge, error) {
	cutoff := time.Now().Add(-olderThan)
	var orphans []OrphanPrecharge
	err := s.db.WithContext(ctx).Raw(`
		SELECT c.user_id, c.request_id, -c.amount AS amount,
		       c.ref_id AS api_key_id, c.created_at
		FROM credit_ledger c
		WHERE c.entry_type = ?
		  AND c.request_id <> ''
		  AND c.created_at < ?
		  AND NOT EXISTS (
			SELECT 1 FROM credit_ledger x
			WHERE x.request_id = c.request_id AND x.entry_type IN (?, ?)
		  )
		ORDER BY c.id`,
		domain.LedgerConsume, cutoff, domain.LedgerRefund, domain.LedgerSettleAdjust,
	).Scan(&orphans).Error
	if err != nil {
		return nil, fmt.Errorf("扫描孤儿预扣失败: %w", err)
	}
	return orphans, nil
}

// CleanupOrphanPrecharges 扫描并退款孤儿预扣。
// 孤儿判定只依赖流水：consume 流水写入早于 olderThan 阈值，且同一 request_id
// 下无 refund/settle_adjust 流水。结算差额为 0 时中继也写零额 settle_adjust
// 作为终态标记（见 relay.BillingSession.Settle）；用量日志允许队列满或停机
// 刷盘超时时丢弃，不可作为终态判据，否则丢弃会导致已结算请求被误判退款。
// 退款沿用 (request_id, entry_type) 幂等索引：并发或重复执行不会二次入账。
func (s *Service) CleanupOrphanPrecharges(ctx context.Context, olderThan time.Duration) (OrphanCleanupResult, error) {
	var result OrphanCleanupResult

	orphans, err := s.ScanOrphanPrecharges(ctx, olderThan)
	if err != nil {
		return result, err
	}
	result.Scanned = len(orphans)

	for _, o := range orphans {
		octx := obs.WithRequestID(ctx, o.RequestID)
		if err := s.refundOrphan(octx, o); err != nil {
			if errors.Is(err, ErrDuplicateEntry) {
				result.AlreadyHandled++
				continue
			}
			return result, fmt.Errorf("孤儿预扣退款失败 request_id=%s: %w", o.RequestID, err)
		}
		result.Refunded++
		result.RefundedCredits += o.Amount
		obs.Logger(octx).Info("孤儿预扣已退款",
			"user_id", o.UserID, "amount", o.Amount, "precharged_at", o.CreatedAt)
	}
	return result, nil
}

// refundOrphan 对单条孤儿预扣补写退款、补追溯日志。
// 与 relay.BillingSession.Refund 的账务动作保持一致：api_keys.credit_used 的回退由
// applyKeyCredit（结算/退款路径，KeyID 非 0 触发 GREATEST UPDATE）同事务完成，
// 不再单独执行裸 SQL（C1 / P1-1 收敛）。
func (s *Service) refundOrphan(ctx context.Context, o OrphanPrecharge) error {
	if o.Amount < 0 {
		return fmt.Errorf("预扣金额异常（负数预扣 %d）", o.Amount)
	}
	_, err := s.Apply(ctx, Adjustment{
		UserID: o.UserID, Amount: o.Amount, EntryType: domain.LedgerRefund,
		RefType: "api_key", RefID: o.APIKeyID, RequestID: o.RequestID,
		AffectsUsed: true, Note: orphanPrechargeNote,
		// 与 BillingSession.Refund 一致：KeyID 使 Key 级当日计数与 api_keys.credit_used
		// 随退款同步回减（由 applyKeyCredit 在同事务内完成），避免孤儿退款后 Key 的
		// daily_spend_by_key 偏高、误触发 Key 每日上限。
		KeyID: o.APIKeyID,
	})
	if err != nil {
		return err
	}

	// 补一条已退款状态的用量日志供用户与管理端追溯。
	// request_id 冲突时改写终态字段：结算写入失败的请求已带一条 failed 日志，
	// 补偿退款后报表状态与实际账务对齐（实际扣款归零）。
	traceLog := &store.UsageLog{
		RequestID: o.RequestID, UserID: o.UserID, APIKeyID: o.APIKeyID,
		Status: domain.UsageRefunded, CreditsPrecharged: o.Amount,
		ErrorMessage: orphanPrechargeNote, CreatedAt: o.CreatedAt,
	}
	if err := s.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "request_id"}},
			DoUpdates: clause.Assignments(map[string]any{
				"status":          domain.UsageRefunded,
				"credits_charged": 0,
				"error_message":   orphanPrechargeNote,
			}),
		}).
		Create(traceLog).Error; err != nil {
		obs.Logger(ctx).Warn("孤儿预扣追溯日志写入失败（退款已完成）", "error", err)
	}
	return nil
}
