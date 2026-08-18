package store

import (
	"context"
	"time"

	"gorm.io/gorm"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
)

// HeatmapCell 是 7×24 周×时热力图的一个格子。
// DayOfWeek 按 PostgreSQL EXTRACT(DOW ...) 取值：0=周日 .. 6=周六。
// Hour 为本地时区的小时（0..23），由 (created_at AT TIME ZONE LocalZoneName()) 换算。
type HeatmapCell struct {
	DayOfWeek int   `json:"day_of_week"` // 0=Sun .. 6=Sat
	Hour      int   `json:"hour"`        // 0..23
	Requests  int64 `json:"requests"`
	// gorm column tag 显式映射 SQL 别名 credits_charged；JSON 字段名同样保持 credits_charged。
	Credits int64 `gorm:"column:credits_charged" json:"credits_charged"`
}

// Heatmap 返回 weekday×hour 的请求与扣费聚合。
//
// 数据来源是原始 usage_logs（按日汇总表不含小时维度，无法满足），因此受用量日志
// 保留期约束——热力图只展示近期 ~30 天，落在保留窗口内。
//
//   - scopeUserID > 0 时限定到单个用户（门户自助视角）；
//   - integrationID 非 nil 时按接入方作用域收窄（托管视角）；
//   - model / channelID / departmentID 为可选筛选，非零/非空时叠加；
//   - 只统计 status='settled' 的日志，与其他统计口径一致；
//   - 时间按服务器本地时区换算后再 EXTRACT，使「周一 09:00」与本地直觉一致，
//     不依赖 PostgreSQL 会话时区（生产 PG 常为 UTC）。
//
// 返回值只含产生了数据的格子，前端负责把空格补零。
func (r *StatsRepo) Heatmap(
	ctx context.Context,
	scopeUserID int64,
	from, to time.Time,
	model string,
	channelID, departmentID int64,
	integrationID *int64,
) ([]HeatmapCell, error) {
	zone := LocalZoneName()
	q := rawLogStatsQuery{
		from: from, to: to, status: domain.UsageSettled,
		scopeUserID: scopeUserID, model: model,
		channelID: channelID, departmentID: departmentID,
		integrationID: integrationID,
	}.apply(r.db.WithContext(ctx)).
		Select(`EXTRACT(DOW FROM (created_at AT TIME ZONE ?))::int AS day_of_week,
			EXTRACT(HOUR FROM (created_at AT TIME ZONE ?))::int AS hour,
			COUNT(*) AS requests,
			COALESCE(SUM(credits_charged),0) AS credits_charged`,
			zone, zone)
	var cells []HeatmapCell
	err := q.Group("day_of_week, hour").
		Order("day_of_week, hour").
		Scan(&cells).Error
	if err == gorm.ErrRecordNotFound {
		return []HeatmapCell{}, nil
	}
	return cells, err
}
