package store

import (
	"context"
	"time"

	"gorm.io/gorm"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
)

// Redemption 对应 redemptions 表。Code 明文仅在批量生成响应中出现一次。
type Redemption struct {
	ID           int64                   `gorm:"primaryKey" json:"id"`
	BatchID      string                  `json:"batch_id"`
	CodeHash     string                  `json:"-"`
	Name         string                  `json:"name"`
	Credits      domain.Credits          `json:"credits"`
	Status       domain.RedemptionStatus `json:"status"`
	UsedByUserID *int64                  `json:"used_by_user_id"`
	RedeemedAt   *time.Time              `json:"redeemed_at"`
	ExpiresAt    *time.Time              `json:"expires_at"`
	CreatedAt    time.Time               `json:"created_at"`
	// EffectiveStatus 是展示态（含 expired），由 Status 与 ExpiresAt 推导，不入库。
	// 界面按它显示与筛选：只看 Status 会把已过期的码显示成「未使用」，
	// 管理员据此发出去的码在员工手里兑不了。
	EffectiveStatus domain.RedemptionStatus `gorm:"-" json:"effective_status"`
}

// withEffectiveStatus 填充展示态。列表与详情统一走这里，避免各处自行推导。
func withEffectiveStatus(items []Redemption) []Redemption {
	now := time.Now()
	out := make([]Redemption, len(items))
	for i, it := range items {
		it.EffectiveStatus = domain.EffectiveRedemptionStatus(it.Status, it.ExpiresAt, now)
		out[i] = it
	}
	return out
}

// RedemptionRepo 封装兑换码访问（核销走 billing.Service 保证与余额同事务）。
type RedemptionRepo struct{ db *gorm.DB }

func NewRedemptionRepo(db *gorm.DB) *RedemptionRepo { return &RedemptionRepo{db: db} }

func (r *RedemptionRepo) CreateBatch(ctx context.Context, items []Redemption) error {
	return r.db.WithContext(ctx).Create(&items).Error
}

// RedemptionListFilter 兑换码列表筛选。
type RedemptionListFilter struct {
	Keyword  string
	Status   domain.RedemptionStatus
	BatchID  string
	Page     int
	PageSize int
}

func (r *RedemptionRepo) List(ctx context.Context, f RedemptionListFilter) ([]Redemption, int64, error) {
	q := r.db.WithContext(ctx).Model(&Redemption{})
	if f.Keyword != "" {
		q = q.Where("name ILIKE ?", "%"+f.Keyword+"%")
	}
	// 按展示态筛选：expired 与 unused 是「未使用」的两个互斥子集，
	// 两者相加等于库里 status = 'unused' 的全部，界面上不会漏也不会重。
	switch f.Status {
	case "":
	case domain.RedemptionExpired:
		q = q.Where("status = ? AND expires_at IS NOT NULL AND expires_at <= now()",
			domain.RedemptionUnused)
	case domain.RedemptionUnused:
		q = q.Where("status = ? AND (expires_at IS NULL OR expires_at > now())",
			domain.RedemptionUnused)
	default:
		q = q.Where("status = ?", f.Status)
	}
	if f.BatchID != "" {
		q = q.Where("batch_id = ?", f.BatchID)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	page, pageSize := NormalizePagination(f.Page, f.PageSize)
	var items []Redemption
	err := q.Order("id DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&items).Error
	return withEffectiveStatus(items), total, err
}

// SetStatus 手工启用/禁用兑换码（已用的不可改）。
func (r *RedemptionRepo) SetStatus(ctx context.Context, id int64, status domain.RedemptionStatus) error {
	res := r.db.WithContext(ctx).Model(&Redemption{}).
		Where("id = ? AND status <> ?", id, domain.RedemptionUsed).
		Update("status", status)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}
