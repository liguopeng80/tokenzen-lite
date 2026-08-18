package store

import (
	"context"
	"time"

	"gorm.io/datatypes"
	"gorm.io/gorm"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
)

// AuditLog 对应 audit_logs 表：管理侧写操作与认证事件的只追加记录。
// 仓储刻意不提供更新与删除单条记录的方法，只有按保留期的批量清理。
type AuditLog struct {
	ID           int64                  `gorm:"primaryKey" json:"id"`
	OperatorID   int64                  `json:"operator_id"`
	OperatorName string                 `json:"operator_name"`
	OperatorRole domain.Role            `json:"operator_role"`
	Action       domain.AuditAction     `json:"action"`
	TargetType   domain.AuditTargetType `json:"target_type"`
	TargetID     int64                  `json:"target_id"`
	TargetName   string                 `json:"target_name"`
	Result       domain.AuditResult     `json:"result"`
	BeforeState  datatypes.JSON         `json:"before_state"`
	AfterState   datatypes.JSON         `json:"after_state"`
	ClientIP     string                 `json:"client_ip"`
	RequestID    string                 `json:"request_id"`
	Message      string                 `json:"message"`
	// IntegrationID 触发该操作的接入方；系统自动操作为 nil。
	IntegrationID *int64    `gorm:"column:integration_id" json:"integration_id"`
	CreatedAt     time.Time `json:"created_at"`
}

// AuditLogRepo 封装 audit_logs 表访问。
type AuditLogRepo struct{ db *gorm.DB }

func NewAuditLogRepo(db *gorm.DB) *AuditLogRepo { return &AuditLogRepo{db: db} }

func (r *AuditLogRepo) Create(ctx context.Context, l *AuditLog) error {
	return r.db.WithContext(ctx).Create(l).Error
}

// AuditListFilter 审计记录筛选条件。
type AuditListFilter struct {
	OperatorID    int64
	Action        domain.AuditAction
	TargetType    domain.AuditTargetType
	TargetID      int64
	Result        domain.AuditResult
	Keyword       string
	StartTime     *time.Time
	EndTime       *time.Time
	IntegrationID *int64
	Page          int
	PageSize      int
}

func (r *AuditLogRepo) List(ctx context.Context, f AuditListFilter) ([]AuditLog, int64, error) {
	q := r.db.WithContext(ctx).Model(&AuditLog{})
	if f.OperatorID > 0 {
		q = q.Where("operator_id = ?", f.OperatorID)
	}
	if f.Action != "" {
		q = q.Where("action = ?", f.Action)
	}
	if f.TargetType != "" {
		q = q.Where("target_type = ?", f.TargetType)
	}
	if f.TargetID > 0 {
		q = q.Where("target_id = ?", f.TargetID)
	}
	if f.Result != "" {
		q = q.Where("result = ?", f.Result)
	}
	if f.Keyword != "" {
		kw := "%" + f.Keyword + "%"
		q = q.Where("operator_name ILIKE ? OR target_name ILIKE ? OR message ILIKE ?", kw, kw, kw)
	}
	if f.StartTime != nil {
		q = q.Where("created_at >= ?", *f.StartTime)
	}
	if f.EndTime != nil {
		q = q.Where("created_at < ?", *f.EndTime)
	}
	if f.IntegrationID != nil {
		q = q.Where("integration_id = ?", *f.IntegrationID)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	page, pageSize := NormalizePagination(f.Page, f.PageSize)
	var rows []AuditLog
	err := q.Order("id DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&rows).Error
	return rows, total, err
}

// PurgeOlderThan 删除早于 before 的审计记录，返回删除条数。
// 调用方负责为清理动作本身补记一条 audit.purge，使清理可追溯。
func (r *AuditLogRepo) PurgeOlderThan(ctx context.Context, before time.Time) (int64, error) {
	res := r.db.WithContext(ctx).Where("created_at < ?", before).Delete(&AuditLog{})
	return res.RowsAffected, res.Error
}
