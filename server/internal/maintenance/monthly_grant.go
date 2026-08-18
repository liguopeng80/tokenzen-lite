package maintenance

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/liguopeng80/tokenzen-lite/server/internal/alerting"
	"github.com/liguopeng80/tokenzen-lite/server/internal/billing"
	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/obs"
)

// monthlyGrantNote 按月自动发放写入积分流水的备注，使这类记账在流水里
// 与管理员手工发放可区分。
const monthlyGrantNote = "按月自动发放"

// monthlyGrantKey 返回某账号在某月的发放幂等键。
// 键含月份：同月重复执行命中唯一索引不再记账，跨月自然放行。
func monthlyGrantKey(month string, userID int64) string {
	return fmt.Sprintf("monthly-grant:%s:%d", month, userID)
}

// computeMonthlyGrantDelta 计算单个账号在某月应发放的积分增量。
// 纯函数：判定依据全部由入参传入，便于在无 DB 环境下测试。
// 返回 skip=true 表示该账号本月不发放（补足口径下余额已达额度）。
func computeMonthlyGrantDelta(mode domain.MonthlyGrantMode,
	amount, balance domain.Credits) (delta domain.Credits, skip bool) {
	if mode == domain.MonthlyGrantTopUp {
		if balance >= amount {
			return 0, true
		}
		return amount - balance, false
	}
	return amount, false
}

// buildMonthlyGrantFailureEvent 构造按月发放部分失败时的告警事件。
// 纯函数：不引用 Scheduler 或设置。
func buildMonthlyGrantFailureEvent(month string, mode domain.MonthlyGrantMode,
	granted, failed int) alerting.Event {
	return alerting.Event{
		Type:     domain.AlertMonthlyGrantFailed,
		Severity: domain.AlertWarning,
		DedupKey: fmt.Sprintf("monthly_grant_failed:%s", month),
		Title:    "按月自动发放积分有账号失败",
		Message: fmt.Sprintf("%s 的自动发放中，%d 个账号发放失败（成功 %d 个）。"+
			"失败账号的余额未变动，可在管理端手工补发；具体账号见服务日志。",
			month, failed, granted),
		Payload: map[string]any{
			"month": month, "granted": granted, "failed": failed, "mode": mode,
		},
	}
}

// grantMonthlyCredits 按月为启用的普通用户自动发放积分。
//
// 由每小时一轮的维护循环驱动，而不是精确到月初零点的定时器：幂等键按「月份 + 用户」
// 唯一，因此每月的第一次执行完成发放，同月后续轮次全部命中重放而不记账；
// 服务在月初停机时，恢复后的第一轮即补上，不会漏发一个月。
//
// 批查询失败时已处理的部分仍照常汇总与告警，再返回 error 给 runTask 统一记录。
func (s *Scheduler) grantMonthlyCredits(ctx context.Context) error {
	if s.Billing == nil || s.Users == nil {
		return nil
	}
	amount := s.Settings.GetInt64(ctx, "monthly_grant_credits")
	if amount <= 0 {
		return nil
	}
	mode := domain.MonthlyGrantMode(s.Settings.GetString(ctx, "monthly_grant_mode"))
	if !mode.Valid() {
		mode = domain.MonthlyGrantTopUp
	}
	month := s.now().In(time.Local).Format("2006-01")

	var granted, skipped, replayed, failed int
	var afterID int64
	var queryErr error
	for batch := 0; batch < maxGrantBatches; batch++ {
		targets, err := s.Users.ListGrantTargets(ctx, afterID, grantBatchSize)
		if err != nil {
			queryErr = err
			break
		}
		if len(targets) == 0 {
			break
		}
		for _, t := range targets {
			afterID = t.ID
			delta, skip := computeMonthlyGrantDelta(mode, amount, t.CreditBalance)
			if skip {
				skipped++
				continue
			}
			// 操作人 ID 传 0 表示系统发起，与管理员手工发放在流水中可区分。
			_, err := s.Billing.Grant(ctx, t.ID, delta, 0,
				fmt.Sprintf("%s（%s）", monthlyGrantNote, month), monthlyGrantKey(month, t.ID))
			switch {
			case err == nil:
				granted++
			case errors.Is(err, billing.ErrDuplicateEntry):
				replayed++
			default:
				failed++
				obs.Logger(ctx).Error("按月自动发放积分失败",
					"user_id", t.ID, "username", t.Username, "month", month, "error", err)
			}
		}
		if len(targets) < grantBatchSize {
			break
		}
	}

	// 本月已发放完毕的常态（或首批查询就失败且没处理任何账号）：不写汇总日志，
	// 避免每小时一条无信息量的记录。
	if granted == 0 && failed == 0 {
		if queryErr != nil {
			return fmt.Errorf("查询按月发放目标账号: %w", queryErr)
		}
		return nil
	}
	obs.Logger(ctx).Info("按月自动发放积分完成", "month", month, "mode", mode,
		"amount", amount, "granted", granted, "skipped", skipped,
		"replayed", replayed, "failed", failed)
	if failed > 0 && s.Alerts != nil {
		s.Alerts.Raise(ctx, buildMonthlyGrantFailureEvent(month, mode, granted, failed))
	}
	if queryErr != nil {
		return fmt.Errorf("查询按月发放目标账号: %w", queryErr)
	}
	return nil
}
