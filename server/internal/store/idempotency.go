package store

import (
	"context"
	"time"

	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// IdempotencyRecord 对应 idempotency_records 表：接入方按幂等键重放请求时，
// 用首次响应直接回放，避免对同一逻辑请求重复执行写操作。
type IdempotencyRecord struct {
	ID             int64  `gorm:"primaryKey" json:"id"`
	IdempotencyKey string `json:"idempotency_key"`
	Scope          string `json:"scope"`
	// IntegrationID 触发该请求的接入方；本机直管路径产生的幂等记录为 nil。
	IntegrationID  *int64         `gorm:"column:integration_id" json:"integration_id"`
	ResponseStatus int            `json:"response_status"`
	ResponseBody   datatypes.JSON `json:"response_body"`
	CreatedAt      time.Time      `json:"created_at"`
}

func (IdempotencyRecord) TableName() string { return "idempotency_records" }

// IdempotencyRepo 封装幂等记录查询与写入。
type IdempotencyRepo struct{ db *gorm.DB }

func NewIdempotencyRepo(db *gorm.DB) *IdempotencyRepo { return &IdempotencyRepo{db: db} }

// GetByKey 按幂等键与作用域查询首次响应记录。
// integration_id 参与匹配，并按迁移里的 COALESCE 唯一索引口径处理 NULL：
// NULL 与 NULL 视为同一作用域，避免本机直管路径的记录互相串。
// 未命中返回 (nil, nil)。
func (r *IdempotencyRepo) GetByKey(ctx context.Context, key, scope string, integrationID *int64) (*IdempotencyRecord, error) {
	var rec IdempotencyRecord
	err := r.db.WithContext(ctx).
		Where("idempotency_key = ? AND scope = ?", key, scope).
		Where("COALESCE(integration_id, 0) = COALESCE(?, 0)", integrationID).
		First(&rec).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &rec, nil
}

// Record 写入一次幂等响应。调用方负责在写入前确认同键尚未存在。
func (r *IdempotencyRepo) Record(ctx context.Context, key, scope string, integrationID *int64, status int, body []byte) error {
	rec := IdempotencyRecord{
		IdempotencyKey: key,
		Scope:          scope,
		IntegrationID:  integrationID,
		ResponseStatus: status,
		ResponseBody:   datatypes.JSON(body),
	}
	return r.db.WithContext(ctx).Create(&rec).Error
}
