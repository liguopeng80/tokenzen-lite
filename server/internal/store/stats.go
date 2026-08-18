package store

import (
	"context"
	"time"

	"gorm.io/gorm"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
)

// StatsRepo 聚合统计查询（dashboard 与利润分析）。
type StatsRepo struct{ db *gorm.DB }

func NewStatsRepo(db *gorm.DB) *StatsRepo { return &StatsRepo{db: db} }

// Overview 管理端总览统计。
type Overview struct {
	TotalUsers          int64 `json:"total_users"`
	ActiveUsersToday    int64 `json:"active_users_today"`
	RequestsToday       int64 `json:"requests_today"`
	CreditsChargedToday int64 `json:"credits_charged_today"`
	CreditsCostToday    int64 `json:"credits_cost_today"`
	ChannelsEnabled     int64 `json:"channels_enabled"`
	ChannelsDisabled    int64 `json:"channels_disabled"`
	TotalCreditBalance  int64 `json:"total_credit_balance"`
}

func (r *StatsRepo) Overview(ctx context.Context) (*Overview, error) {
	o := &Overview{}
	db := r.db.WithContext(ctx)
	// 「今天」按服务器本地日界截断，与 SpendDay/SpendRepo 同口径。
	// time.Truncate(24h) 按 UTC epoch 取整，UTC+8 下会把本地 00:00–08:00 算进昨天。
	today := SpendDay(time.Now())
	if err := db.Model(&User{}).Count(&o.TotalUsers).Error; err != nil {
		return nil, err
	}
	// 任一聚合查询失败都应上抛，而不是以零值假象混过——
	// 否则仪表盘会悄无声息地少报某一项。
	if err := db.Model(&UsageLog{}).Where("created_at >= ?", today).
		Distinct("user_id").Count(&o.ActiveUsersToday).Error; err != nil {
		return nil, err
	}
	if err := db.Model(&UsageLog{}).Where("created_at >= ?", today).
		Count(&o.RequestsToday).Error; err != nil {
		return nil, err
	}
	var sums struct{ Charged, Cost int64 }
	if err := db.Model(&UsageLog{}).Where("created_at >= ? AND status = ?", today, string(domain.UsageSettled)).
		Select("COALESCE(SUM(credits_charged),0) AS charged, COALESCE(SUM(credits_cost),0) AS cost").
		Scan(&sums).Error; err != nil {
		return nil, err
	}
	o.CreditsChargedToday, o.CreditsCostToday = sums.Charged, sums.Cost
	if err := db.Model(&Channel{}).Where("status = ?", string(domain.ChannelEnabled)).
		Count(&o.ChannelsEnabled).Error; err != nil {
		return nil, err
	}
	if err := db.Model(&Channel{}).Where("status <> ?", string(domain.ChannelEnabled)).
		Count(&o.ChannelsDisabled).Error; err != nil {
		return nil, err
	}
	if err := db.Model(&User{}).
		Select("COALESCE(SUM(credit_balance),0)").Scan(&o.TotalCreditBalance).Error; err != nil {
		return nil, err
	}
	return o, nil
}

// DailyStat 按日聚合。
type DailyStat struct {
	Day            time.Time `json:"day"`
	Requests       int64     `json:"requests"`
	CreditsCharged int64     `json:"credits_charged"`
	CreditsCost    int64     `json:"credits_cost"`
	TotalTokens    int64     `json:"total_tokens"`
}

// UsageDaily 全站或单用户按日聚合（userID=0 为全站）。
func (r *StatsRepo) UsageDaily(ctx context.Context, userID int64, days int) ([]DailyStat, error) {
	since := time.Now().AddDate(0, 0, -days)
	// 按服务器本地日界分桶，与 SpendDay/rollup 同口径：date_trunc('day', created_at)
	// 依赖 PG 会话时区，生产 PG 常为 UTC、应用为本地时区时会错一天。
	// date 类型经 pgx 读入为「该日 00:00 UTC」的 time.Time，与 usage_daily_rollup.day
	// 的既有扫描语义一致（见 scheduler_test.go 对 DATE 列的说明）。
	q := r.db.WithContext(ctx).Model(&UsageLog{}).
		Select(`(created_at AT TIME ZONE ?)::date AS day,
			COUNT(*) AS requests,
			COALESCE(SUM(credits_charged),0) AS credits_charged,
			COALESCE(SUM(credits_cost),0) AS credits_cost,
			COALESCE(SUM(prompt_tokens + completion_tokens),0) AS total_tokens`,
			LocalZoneName()).
		Where("created_at >= ? AND status = ?", since, string(domain.UsageSettled))
	if userID > 0 {
		q = q.Where("user_id = ?", userID)
	}
	var stats []DailyStat
	err := q.Group("day").Order("day").Scan(&stats).Error
	return stats, err
}

// ProfitRow 利润分析行。
type ProfitRow struct {
	GroupKey       string `json:"group_key"` // 渠道名或模型名
	Requests       int64  `json:"requests"`
	CreditsCharged int64  `json:"credits_charged"`
	CreditsCost    int64  `json:"credits_cost"`
	Margin         int64  `json:"margin"`
}

// Profit 按渠道或模型聚合利润（charged − cost）。
func (r *StatsRepo) Profit(ctx context.Context, groupBy string, from, to time.Time) ([]ProfitRow, error) {
	var groupExpr string
	if groupBy == "channel" {
		groupExpr = "COALESCE(c.name, '未知渠道 #' || usage_logs.channel_id::text)"
	} else {
		groupExpr = "usage_logs.model_name"
	}
	q := r.db.WithContext(ctx).Table("usage_logs").
		Select(groupExpr+` AS group_key,
			COUNT(*) AS requests,
			COALESCE(SUM(credits_charged),0) AS credits_charged,
			COALESCE(SUM(credits_cost),0) AS credits_cost,
			COALESCE(SUM(credits_charged - credits_cost),0) AS margin`).
		Where("usage_logs.status = ? AND usage_logs.created_at >= ? AND usage_logs.created_at < ?",
			string(domain.UsageSettled), from, to)
	if groupBy == "channel" {
		q = q.Joins("LEFT JOIN channels c ON c.id = usage_logs.channel_id")
	}
	var rows []ProfitRow
	err := q.Group("group_key").Order("margin DESC").Scan(&rows).Error
	return rows, err
}

// SummaryRow 用户侧用量汇总行。
type SummaryRow struct {
	GroupKey       string `json:"group_key"`
	Requests       int64  `json:"requests"`
	CreditsCharged int64  `json:"credits_charged"`
	TotalTokens    int64  `json:"total_tokens"`
}
