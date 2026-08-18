package store

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
)

// Integration 对应 integrations 表：一个对接本系统的外部租户（接入方）。
// 接入方名下的用户与密钥由其自助管理，本机只记账与路由。
type Integration struct {
	ID        int64                    `gorm:"primaryKey" json:"id"`
	Name      string                   `json:"name"`
	Slug      string                   `json:"slug"`
	Status    domain.IntegrationStatus `json:"status"`
	CreatedAt time.Time                `json:"created_at"`
	UpdatedAt time.Time                `json:"updated_at"`
}

// ServiceToken 对应 service_tokens 表：接入方调用管理 API 的凭证。
// TokenHash 不参与 JSON 序列化，前端仅见 TokenPrefix。软删除保留记录，
// 事后追查泄漏或异常调用时仍能解析出这个令牌是谁的、何时删除的。
type ServiceToken struct {
	ID            int64                     `gorm:"primaryKey" json:"id"`
	IntegrationID int64                     `json:"integration_id"`
	Name          string                    `json:"name"`
	TokenHash     string                    `json:"-"`
	TokenPrefix   string                    `json:"token_prefix"`
	Status        domain.ServiceTokenStatus `json:"status"`
	LastUsedAt    *time.Time                `json:"last_used_at"`
	CreatedAt     time.Time                 `json:"created_at"`
	UpdatedAt     time.Time                 `json:"updated_at"`
	// DeletedAt 非空表示令牌已被删除。GORM 据此为本模型的全部查询自动追加
	// deleted_at IS NULL，认证与列表都不会再命中已删除的令牌。
	DeletedAt gorm.DeletedAt `gorm:"index" json:"deleted_at"`
}

func (ServiceToken) TableName() string { return "service_tokens" }

// IntegrationRepo 封装 integrations 表访问。
type IntegrationRepo struct{ db *gorm.DB }

func NewIntegrationRepo(db *gorm.DB) *IntegrationRepo { return &IntegrationRepo{db: db} }

func (r *IntegrationRepo) Create(ctx context.Context, it *Integration) error {
	return r.db.WithContext(ctx).Create(it).Error
}

func (r *IntegrationRepo) GetByID(ctx context.Context, id int64) (*Integration, error) {
	var it Integration
	err := r.db.WithContext(ctx).First(&it, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	return &it, err
}

// GetBySlug 按 slug 精确查询；slug 是接入方的稳定标识，创建后不可变。
func (r *IntegrationRepo) GetBySlug(ctx context.Context, slug string) (*Integration, error) {
	var it Integration
	err := r.db.WithContext(ctx).Where("slug = ?", slug).First(&it).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	return &it, err
}

// List 返回接入方列表。status 为空串时不过滤状态。
func (r *IntegrationRepo) List(ctx context.Context, status domain.IntegrationStatus) ([]Integration, error) {
	q := r.db.WithContext(ctx).Model(&Integration{})
	if status != "" {
		q = q.Where("status = ?", status)
	}
	var rows []Integration
	err := q.Order("id").Find(&rows).Error
	return rows, err
}

// integrationMutableFields 是允许通过 UpdateFields 修改的字段白名单。
// slug 不在其中：它是接入方的稳定外部标识，创建后不可变。
var integrationMutableFields = map[string]struct{}{
	"name": {},
}

// UpdateFields 按白名单字段更新接入方；slug 不在白名单内，传入会被忽略。
func (r *IntegrationRepo) UpdateFields(ctx context.Context, id int64, fields map[string]any) error {
	filtered := make(map[string]any, len(fields))
	for k, v := range fields {
		if _, ok := integrationMutableFields[k]; ok {
			filtered[k] = v
		}
	}
	filtered["updated_at"] = time.Now()
	res := r.db.WithContext(ctx).Model(&Integration{}).Where("id = ?", id).Updates(filtered)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// SetStatus 切换接入方状态（启用/停用）。
func (r *IntegrationRepo) SetStatus(ctx context.Context, id int64, status domain.IntegrationStatus) error {
	res := r.db.WithContext(ctx).Model(&Integration{}).Where("id = ?", id).
		Updates(map[string]any{"status": status, "updated_at": time.Now()})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// ServiceTokenRepo 封装 service_tokens 表访问。
type ServiceTokenRepo struct{ db *gorm.DB }

func NewServiceTokenRepo(db *gorm.DB) *ServiceTokenRepo { return &ServiceTokenRepo{db: db} }

func (r *ServiceTokenRepo) Create(ctx context.Context, t *ServiceToken) error {
	return r.db.WithContext(ctx).Create(t).Error
}

// GetByHash 按明文哈希查令牌（认证路径）。GORM 软删除自动追加 deleted_at IS NULL。
func (r *ServiceTokenRepo) GetByHash(ctx context.Context, hash string) (*ServiceToken, error) {
	var t ServiceToken
	err := r.db.WithContext(ctx).Where("token_hash = ?", hash).First(&t).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	return &t, err
}

func (r *ServiceTokenRepo) GetByID(ctx context.Context, id int64) (*ServiceToken, error) {
	var t ServiceToken
	err := r.db.WithContext(ctx).First(&t, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	return &t, err
}

// ListByIntegration 返回指定接入方名下的全部令牌，按 ID 排序。
func (r *ServiceTokenRepo) ListByIntegration(ctx context.Context, integrationID int64) ([]ServiceToken, error) {
	var rows []ServiceToken
	err := r.db.WithContext(ctx).Where("integration_id = ?", integrationID).
		Order("id").Find(&rows).Error
	return rows, err
}

// UpdateStatus 切换令牌状态（启用/停用）。
func (r *ServiceTokenRepo) UpdateStatus(ctx context.Context, id int64, status domain.ServiceTokenStatus) error {
	res := r.db.WithContext(ctx).Model(&ServiceToken{}).Where("id = ?", id).
		Updates(map[string]any{"status": status, "updated_at": time.Now()})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdateLastUsed 记录最近一次认证时间。
func (r *ServiceTokenRepo) UpdateLastUsed(ctx context.Context, id int64) error {
	return r.db.WithContext(ctx).Model(&ServiceToken{}).Where("id = ?", id).
		Updates(map[string]any{"last_used_at": time.Now(), "updated_at": time.Now()}).Error
}

// Delete 软删除令牌，记录保留供事后追溯。
func (r *ServiceTokenRepo) Delete(ctx context.Context, id int64) error {
	res := r.db.WithContext(ctx).Where("id = ?", id).Delete(&ServiceToken{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}
