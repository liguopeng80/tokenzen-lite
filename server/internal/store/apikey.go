package store

import (
	"context"
	"errors"
	"time"

	"gorm.io/datatypes"
	"gorm.io/gorm"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
)

// APIKey 对应 api_keys 表。KeyHash 不参与 JSON 序列化，前端仅见 KeyPrefix。
type APIKey struct {
	ID          int64            `gorm:"primaryKey" json:"id"`
	UserID      int64            `json:"user_id"`
	Name        string           `json:"name"`
	KeyHash     string           `json:"-"`
	KeyPrefix   string           `json:"key_prefix"`
	Status      domain.KeyStatus `json:"status"`
	CreditLimit *domain.Credits  `json:"credit_limit"`
	CreditUsed  domain.Credits   `json:"credit_used"`
	// DailySpendLimit 该 Key 单自然日累计扣费积分上限，0 表示不限制。
	// 与 users.daily_spend_limit 并行：取收紧值，权威校验在 billing.applyTx 内。
	DailySpendLimit domain.Credits `json:"daily_spend_limit"`
	// ProjectID 该密钥归属的项目，nil 表示未归属项目。项目是与部门正交的第二层
	// 成本归属维度。中继写入用量日志时按此字段冻结项目快照。
	ProjectID     *int64         `json:"project_id"`
	ExpiresAt     *time.Time     `json:"expires_at"`
	AllowedModels datatypes.JSON `json:"allowed_models"`
	AllowedIPs    datatypes.JSON `json:"allowed_ips"`
	// IntegrationID 所属接入方，nil 表示本机直接管理的密钥。
	IntegrationID *int64     `json:"integration_id"`
	LastUsedAt    *time.Time `json:"last_used_at"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
	// DeletedAt 非空表示密钥已被删除。删除保留记录而非清除行：用量日志的
	// api_key_id 仍要能解析出这个密钥是谁的、叫什么、何时删除的，否则密钥泄漏
	// 或出现异常调用时事后追查会断线。GORM 据此为本模型的全部查询自动追加
	// deleted_at IS NULL，认证与列表都不会再命中已删除的密钥。
	DeletedAt gorm.DeletedAt `json:"deleted_at"`
}

func (APIKey) TableName() string { return "api_keys" }

// APIKeyRepo 封装 api_keys 表访问。
type APIKeyRepo struct{ db *gorm.DB }

func NewAPIKeyRepo(db *gorm.DB) *APIKeyRepo { return &APIKeyRepo{db: db} }

func (r *APIKeyRepo) Create(ctx context.Context, k *APIKey) error {
	return r.db.WithContext(ctx).Create(k).Error
}

// CountByUser 统计用户持有的 API Key 数量（含全部状态），
// 供单用户密钥数量上限（max_keys_per_user）校验使用。
func (r *APIKeyRepo) CountByUser(ctx context.Context, userID int64) (int64, error) {
	var n int64
	err := r.db.WithContext(ctx).Model(&APIKey{}).
		Where("user_id = ?", userID).Count(&n).Error
	return n, err
}

func (r *APIKeyRepo) GetByID(ctx context.Context, id int64) (*APIKey, error) {
	var k APIKey
	err := r.db.WithContext(ctx).First(&k, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	return &k, err
}

// GetByHash 按明文哈希查 Key（/v1 认证路径）。
func (r *APIKeyRepo) GetByHash(ctx context.Context, hash string) (*APIKey, error) {
	var k APIKey
	err := r.db.WithContext(ctx).Where("key_hash = ?", hash).First(&k).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	return &k, err
}

// APIKeyListFilter API Key 列表筛选。UserID 为 0 表示不限用户（管理端视角）。
type APIKeyListFilter struct {
	UserID        int64
	Keyword       string
	Status        domain.KeyStatus
	IntegrationID *int64
	Page          int
	PageSize      int
}

func (r *APIKeyRepo) List(ctx context.Context, f APIKeyListFilter) ([]APIKey, int64, error) {
	q := r.db.WithContext(ctx).Model(&APIKey{})
	if f.UserID > 0 {
		q = q.Where("user_id = ?", f.UserID)
	}
	if f.Keyword != "" {
		q = q.Where("name ILIKE ?", "%"+f.Keyword+"%")
	}
	if f.Status != "" {
		q = q.Where("status = ?", f.Status)
	}
	if f.IntegrationID != nil {
		q = q.Where("integration_id = ?", *f.IntegrationID)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	page, pageSize := NormalizePagination(f.Page, f.PageSize)
	var keys []APIKey
	err := q.Order("id DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&keys).Error
	return keys, total, err
}

// UpdateFields 按白名单字段更新；ownerID > 0 时限定归属用户，防越权。
func (r *APIKeyRepo) UpdateFields(ctx context.Context, id, ownerID int64, fields map[string]any) error {
	fields["updated_at"] = time.Now()
	q := r.db.WithContext(ctx).Model(&APIKey{}).Where("id = ?", id)
	if ownerID > 0 {
		q = q.Where("user_id = ?", ownerID)
	}
	res := q.Updates(fields)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// RestoreDepleted 将 depleted（额度耗尽）状态的密钥恢复为 enabled。
// 仅当额度上限已清除、或已用额度低于当前上限（即重新有可用额度）时生效；
// 由额度上限调整路径在更新 credit_limit 后调用。ownerID > 0 时限定归属用户。
func (r *APIKeyRepo) RestoreDepleted(ctx context.Context, id, ownerID int64) error {
	q := r.db.WithContext(ctx).Model(&APIKey{}).
		Where("id = ? AND status = ?", id, domain.KeyDepleted).
		Where("credit_limit IS NULL OR credit_used < credit_limit")
	if ownerID > 0 {
		q = q.Where("user_id = ?", ownerID)
	}
	return q.Updates(map[string]any{
		"status":     domain.KeyEnabled,
		"updated_at": time.Now(),
	}).Error
}

// Delete 删除 Key（软删除，记录保留供事后追溯）；ownerID > 0 时限定归属用户。
func (r *APIKeyRepo) Delete(ctx context.Context, id, ownerID int64) error {
	q := r.db.WithContext(ctx).Where("id = ?", id)
	if ownerID > 0 {
		q = q.Where("user_id = ?", ownerID)
	}
	res := q.Delete(&APIKey{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}
