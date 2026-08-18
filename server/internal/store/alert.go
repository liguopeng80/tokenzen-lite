package store

import (
	"context"
	"time"

	"gorm.io/datatypes"
	"gorm.io/gorm"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
)

// AlertEvent 对应 alert_events 表。该表既是投递队列，也是管理员报告
// 「没收到告警」时区分「未触发」与「触发了但发不出去」的依据。
type AlertEvent struct {
	ID        int64                `gorm:"primaryKey" json:"id"`
	AlertType domain.AlertType     `json:"alert_type"`
	Severity  domain.AlertSeverity `json:"severity"`
	DedupKey  string               `json:"dedup_key"`
	Title     string               `json:"title"`
	Message   string               `json:"message"`
	Payload   datatypes.JSON       `json:"payload"`
	Status    domain.AlertStatus   `json:"status"`
	// ChannelsSent 记录投递成功的通道名，供排查「发出去了但没人看到」。
	ChannelsSent datatypes.JSON `json:"channels_sent"`
	Attempts     int            `json:"attempts"`
	LastError    string         `json:"last_error"`
	SentAt       *time.Time     `json:"sent_at"`
	CreatedAt    time.Time      `json:"created_at"`
}

// AlertEventRepo 封装 alert_events 表访问。
type AlertEventRepo struct{ db *gorm.DB }

func NewAlertEventRepo(db *gorm.DB) *AlertEventRepo { return &AlertEventRepo{db: db} }

func (r *AlertEventRepo) Create(ctx context.Context, e *AlertEvent) error {
	return r.db.WithContext(ctx).Create(e).Error
}

// RecentlyDelivered 判断同一去重键是否已在抑制窗口内投递过。
// 只有实际投递出去的事件（sent）参与抑制：被抑制或投递失败的事件不应
// 让后续同类事件继续静默，否则一次通道故障会永久吞掉该类告警。
func (r *AlertEventRepo) RecentlyDelivered(ctx context.Context, dedupKey string, since time.Time) (bool, error) {
	if dedupKey == "" {
		return false, nil
	}
	var n int64
	err := r.db.WithContext(ctx).Model(&AlertEvent{}).
		Where("dedup_key = ? AND status = ? AND created_at >= ?",
			dedupKey, domain.AlertSent, since).
		Count(&n).Error
	return n > 0, err
}

// alertUpdatableFields 是投递结果回写允许更新的字段白名单。
// GORM 的 Updates(map) 会原样下发，没有白名单时调用方一旦写入误字段
// （如 alert_type、created_at）会绕过业务校验直接落库。新增可写字段必须在此登记。
var alertUpdatableFields = map[string]struct{}{
	"status":        {},
	"attempts":      {},
	"last_error":    {},
	"sent_at":       {},
	"channels_sent": {},
}

// UpdateFields 按白名单字段更新告警事件（投递结果回写）。
// 入参不在白名单内的字段被静默丢弃并记 WARNING，避免误写覆盖落库时的不可变快照
// （alert_type、severity、dedup_key、created_at 等）。
func (r *AlertEventRepo) UpdateFields(ctx context.Context, id int64, fields map[string]any) error {
	return r.db.WithContext(ctx).Model(&AlertEvent{}).Where("id = ?", id).
		Updates(filterUpdatableAlertFields(fields)).Error
}

// filterUpdatableAlertFields 丢弃不在白名单内的字段。抽出为独立函数便于单测。
func filterUpdatableAlertFields(fields map[string]any) map[string]any {
	filtered := make(map[string]any, len(fields))
	for k, v := range fields {
		if _, ok := alertUpdatableFields[k]; ok {
			filtered[k] = v
		}
	}
	return filtered
}

// AlertListFilter 告警事件筛选条件。
type AlertListFilter struct {
	AlertType domain.AlertType
	Severity  domain.AlertSeverity
	Status    domain.AlertStatus
	StartTime *time.Time
	EndTime   *time.Time
	Page      int
	PageSize  int
}

func (r *AlertEventRepo) List(ctx context.Context, f AlertListFilter) ([]AlertEvent, int64, error) {
	q := r.db.WithContext(ctx).Model(&AlertEvent{})
	if f.AlertType != "" {
		q = q.Where("alert_type = ?", f.AlertType)
	}
	if f.Severity != "" {
		q = q.Where("severity = ?", f.Severity)
	}
	if f.Status != "" {
		q = q.Where("status = ?", f.Status)
	}
	if f.StartTime != nil {
		q = q.Where("created_at >= ?", *f.StartTime)
	}
	if f.EndTime != nil {
		q = q.Where("created_at < ?", *f.EndTime)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	page, pageSize := NormalizePagination(f.Page, f.PageSize)
	var rows []AlertEvent
	err := q.Order("id DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&rows).Error
	return rows, total, err
}
