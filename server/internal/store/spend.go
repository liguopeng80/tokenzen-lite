package store

import (
	"context"
	"time"

	"gorm.io/gorm"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
)

// DailySpend 对应 daily_spend 表：单个用户单个自然日的累计净扣费。
// 计数在余额调整的同一事务内维护，与积分流水同源，因此不会出现
// 「扣了积分但没计数」的偏差。
//
// 写入收口在 billing.applyTx（P3-13）：本包仅暴露读取与保留期清理路径，
// billing 经包内私有助手 addDailySpend 在同事务内维护计数。
type DailySpend struct {
	UserID    int64          `gorm:"primaryKey" json:"user_id"`
	Day       time.Time      `gorm:"primaryKey" json:"day"`
	Credits   domain.Credits `json:"credits"`
	UpdatedAt time.Time      `json:"updated_at"`
}

func (DailySpend) TableName() string { return "daily_spend" }

// SpendRepo 提供每日花费的读取与保留期清理。写入统一走 billing.Service，
// 保证与余额调整同事务（P3-13：store 不再暴露 spend 写路径）。
type SpendRepo struct{ db *gorm.DB }

func NewSpendRepo(db *gorm.DB) *SpendRepo { return &SpendRepo{db: db} }

// TodaySpend 返回用户在给定时刻所属自然日的已扣费积分。
func (r *SpendRepo) TodaySpend(ctx context.Context, userID int64, now time.Time) (domain.Credits, error) {
	var row DailySpend
	err := r.db.WithContext(ctx).
		Where("user_id = ? AND day = (? AT TIME ZONE ?)::date",
			userID, SpendDay(now), LocalZoneName()).
		Take(&row).Error
	if err == gorm.ErrRecordNotFound {
		return 0, nil
	}
	return row.Credits, err
}

// PurgeOlderThan 清理早于 before 的每日花费计数，返回删除条数。
// 计数只服务于当日限额判定，历史值的事实源是积分流水，无需长期保留。
func (r *SpendRepo) PurgeOlderThan(ctx context.Context, before time.Time) (int64, error) {
	res := r.db.WithContext(ctx).
		Where("day < (? AT TIME ZONE ?)::date", SpendDay(before), LocalZoneName()).
		Delete(&DailySpend{})
	return res.RowsAffected, res.Error
}

// DailySpendByKey 对应 daily_spend_by_key 表：单个 API Key 单个自然日的累计净扣费。
// 与 DailySpend 同源同口径：在余额调整的同一事务内维护，写入与积分流水同事务，
// 因此不会出现「扣了积分但没计数」的偏差。Key 级每日上限的权威校验读取本表。
//
// 写入同样收口在 billing.applyTx（P3-13），本包仅暴露读取与清理。
type DailySpendByKey struct {
	APIKeyID  int64          `gorm:"primaryKey" json:"api_key_id"`
	Day       time.Time      `gorm:"primaryKey" json:"day"`
	Credits   domain.Credits `json:"credits"`
	UpdatedAt time.Time      `json:"updated_at"`
}

func (DailySpendByKey) TableName() string { return "daily_spend_by_key" }

// TodaySpendByKey 返回给定 Key 在指定时刻所属自然日的已扣费积分。
func (r *SpendRepo) TodaySpendByKey(ctx context.Context, keyID int64, now time.Time) (domain.Credits, error) {
	var row DailySpendByKey
	err := r.db.WithContext(ctx).
		Where("api_key_id = ? AND day = (? AT TIME ZONE ?)::date",
			keyID, SpendDay(now), LocalZoneName()).
		Take(&row).Error
	if err == gorm.ErrRecordNotFound {
		return 0, nil
	}
	return row.Credits, err
}

// PurgeKeySpendOlderThan 清理早于 before 的 Key 级每日花费计数，返回删除条数。
// 与 PurgeOlderThan 同保留期策略，由 maintenance 同轮调用。
func (r *SpendRepo) PurgeKeySpendOlderThan(ctx context.Context, before time.Time) (int64, error) {
	res := r.db.WithContext(ctx).
		Where("day < (? AT TIME ZONE ?)::date", SpendDay(before), LocalZoneName()).
		Delete(&DailySpendByKey{})
	return res.RowsAffected, res.Error
}
