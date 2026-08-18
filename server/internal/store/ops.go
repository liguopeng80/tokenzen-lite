package store

import (
	"context"
	"time"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
)

// 经营分析（Ops）月度汇总。复用 RollupRepo.Aggregate 的保留期安全聚合路径，
// 不另起数据源。所有总额按单维度（AggByDay）在全月范围求和——总额与所选维度无关，
// 因为所有维度的行汇总后都是同一组已结算记录的合计。

// OpsSummary 是 GET /api/admin/stats/ops-summary 的响应体。
type OpsSummary struct {
	Month     string         `json:"month"`      // "YYYY-MM"
	ThisMonth OpsMonthTotals `json:"this_month"` // 本月合计
	PrevMonth OpsMonthTotals `json:"prev_month"` // 上月合计
	MoM       OpsMoMDelta    `json:"mom"`        // 环比变化（百分比）
	TopModels []OpsRankRow   `json:"top_models"` // 本月按扣费额排序的模型 Top N
	TopUsers  []OpsRankRow   `json:"top_users"`  // 本月按扣费额排序的用户 Top N
}

// OpsMonthTotals 单月合计。
type OpsMonthTotals struct {
	Requests       int64          `json:"requests"`
	CreditsCharged domain.Credits `json:"credits_charged"`
	CreditsCost    domain.Credits `json:"credits_cost"`
	Margin         domain.Credits `json:"margin"`
	// TopupCredits 当月充值/发放的积分合计（grant + redeem 的正数金额）。
	// 不可用时为 0。
	TopupCredits int64 `json:"topup_credits"`
}

// OpsMoMDelta 本月相对上月的百分比变化。上月分母为 0 时对应字段为 nil，
// 避免除零产生无意义的极大值。
type OpsMoMDelta struct {
	ChargedPct *float64 `json:"charged_pct"`
	CostPct    *float64 `json:"cost_pct"`
	RequestPct *float64 `json:"request_pct"`
	TopupPct   *float64 `json:"topup_pct"`
}

// OpsRankRow 排行榜行。
type OpsRankRow struct {
	GroupKey       string         `json:"group_key"`
	GroupID        int64          `json:"group_id"`
	Requests       int64          `json:"requests"`
	CreditsCharged domain.Credits `json:"credits_charged"`
	CreditsCost    domain.Credits `json:"credits_cost"`
}

// opsTopN 排行榜截取长度。
const opsTopN = 5

// OpsSummary 计算给定自然月（month 取该月内任一时刻即可）的经营分析汇总。
// integrationID 非 nil 时按接入方作用域收窄（托管视角），与费用报表口径一致。
//
// rolledThrough 在入口处读一次（4 路聚合共享同一水位，避免重复 MAX(day) 查询）。
// 总额走专门的标量 SUM（绕开 Aggregate 的 4 表实体 JOIN 与按日 GROUP BY——月度总额
// 不需要维度与实体名）；Top 走 aggregateWithRollup(AggByModel/AggByUser, 全月范围) 取前
// opsTopN 行。充值额（topup）从 credit_ledger 直接求和 grant+redeem。
func (r *RollupRepo) OpsSummary(ctx context.Context, month time.Time, integrationID *int64) (*OpsSummary, error) {
	thisFrom, thisTo := MonthRange(month)
	prevFrom, prevTo := MonthRange(thisFrom.AddDate(0, -1, 0))

	// 4 路聚合共享同一水位，避免每路各自 SELECT MAX(day)。
	rolledThrough, err := r.RolledThrough(ctx)
	if err != nil {
		return nil, err
	}

	thisTotals, err := r.monthTotals(ctx, thisFrom, thisTo, integrationID, rolledThrough)
	if err != nil {
		return nil, err
	}
	prevTotals, err := r.monthTotals(ctx, prevFrom, prevTo, integrationID, rolledThrough)
	if err != nil {
		return nil, err
	}

	thisTop, err := r.monthTop(ctx, thisFrom, thisTo, integrationID, rolledThrough)
	if err != nil {
		return nil, err
	}

	return &OpsSummary{
		Month:     thisFrom.Format("2006-01"),
		ThisMonth: thisTotals,
		PrevMonth: prevTotals,
		MoM: OpsMoMDelta{
			ChargedPct: momPct(thisTotals.CreditsCharged, prevTotals.CreditsCharged),
			CostPct:    momPct(thisTotals.CreditsCost, prevTotals.CreditsCost),
			RequestPct: momPct(thisTotals.Requests, prevTotals.Requests),
			TopupPct:   momPct(thisTotals.TopupCredits, prevTotals.TopupCredits),
		},
		TopModels: thisTop.models,
		TopUsers:  thisTop.users,
	}, nil
}

// monthTotals 聚合单月的总额与充值额。
//
// 总额用直接 SUM 查询，复用 Aggregate 的 rollup+raw UNION 但去掉 4 表实体 JOIN
// （users/departments/channels/api_keys）与按日 GROUP BY——月度合计只需标量求和，
// 实体名与维度分组是冗余开销。rolledThrough 由调用方传入，避免重复读水位。
func (r *RollupRepo) monthTotals(ctx context.Context, from, to time.Time, iid *int64,
	rolledThrough time.Time) (OpsMonthTotals, error) {
	fromD, toD := SpendDay(from), SpendDay(to)
	var t OpsMonthTotals
	if toD.After(fromD) {
		rollupTo := rollupSplit(fromD, toD, rolledThrough)
		query := `
			SELECT COALESCE(SUM(requests),0), COALESCE(SUM(credits_charged),0),
			       COALESCE(SUM(credits_cost),0), COALESCE(SUM(credits_charged - credits_cost),0)
			FROM (
				SELECT integration_id, requests, credits_charged, credits_cost
				FROM usage_daily_rollup
				WHERE day >= ? AND day < ?
				UNION ALL
				SELECT integration_id, 1, credits_charged, credits_cost
				FROM usage_logs
				WHERE status = ? AND created_at >= ? AND created_at < ?
			) src`
		args := []any{fromD, rollupTo, string(domain.UsageSettled), rollupTo, toD}
		if iid != nil {
			query += ` WHERE src.integration_id = ?`
			args = append(args, *iid)
		}
		row := r.db.WithContext(ctx).Raw(query, args...).Row()
		if err := row.Scan(&t.Requests, &t.CreditsCharged, &t.CreditsCost, &t.Margin); err != nil {
			return OpsMonthTotals{}, err
		}
	}
	var err error
	t.TopupCredits, err = r.monthTopup(ctx, from, to, iid)
	if err != nil {
		return OpsMonthTotals{}, err
	}
	return t, nil
}

// monthTop 取本月模型与用户的 Top N（按扣费额降序）。
type opsTopResult struct {
	models []OpsRankRow
	users  []OpsRankRow
}

func (r *RollupRepo) monthTop(ctx context.Context, from, to time.Time, iid *int64,
	rolledThrough time.Time) (opsTopResult, error) {
	fromD, toD := SpendDay(from), SpendDay(to)
	af := AggFilter{From: from, To: to, IntegrationID: iid}
	modelRows, err := r.aggregateWithRollup(ctx, AggByModel, af, fromD, toD, rolledThrough)
	if err != nil {
		return opsTopResult{}, err
	}
	userRows, err := r.aggregateWithRollup(ctx, AggByUser, af, fromD, toD, rolledThrough)
	if err != nil {
		return opsTopResult{}, err
	}
	return opsTopResult{
		models: toOpsRankRows(modelRows, opsTopN),
		users:  toOpsRankRows(userRows, opsTopN),
	}, nil
}

func toOpsRankRows(rows []AggRow, limit int) []OpsRankRow {
	if len(rows) > limit {
		rows = rows[:limit]
	}
	out := make([]OpsRankRow, 0, len(rows))
	for _, row := range rows {
		out = append(out, OpsRankRow{
			GroupKey: row.GroupKey, GroupID: row.GroupID,
			Requests:       row.Requests,
			CreditsCharged: row.CreditsCharged, CreditsCost: row.CreditsCost,
		})
	}
	return out
}

// monthTopup 汇总区间内 grant+redeem 的正数金额。
// grant（管理员分配）与 redeem（兑换码充值）是仅有的两种「充值类」正向流水；
// refund/settle_adjust 的正数属调用计费的内部对冲，不计入充值。
func (r *RollupRepo) monthTopup(ctx context.Context, from, to time.Time, iid *int64) (int64, error) {
	q := r.db.WithContext(ctx).Model(&LedgerEntry{}).
		Where("entry_type IN ?", []domain.LedgerEntryType{domain.LedgerGrant, domain.LedgerRedeem}).
		Where("created_at >= ? AND created_at < ?", from, to)
	if iid != nil {
		q = q.Where("integration_id = ?", *iid)
	}
	var sum int64
	if err := q.Select("COALESCE(SUM(amount),0)").Scan(&sum).Error; err != nil {
		return 0, err
	}
	return sum, nil
}

// momPct 计算 new 相对 old 的百分比变化。old 为 0 时返回 nil，避免除零。
func momPct(newVal, oldVal int64) *float64 {
	if oldVal == 0 {
		return nil
	}
	pct := (float64(newVal) - float64(oldVal)) / float64(oldVal) * 100
	return &pct
}
