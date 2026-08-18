package store

import (
	"time"

	"gorm.io/gorm"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
)

// rawLogStatsQuery 是原始 usage_logs 统计查询（heatmap/health/calltype）的共享构造器，
// 消除三处 Table+Where 过滤条件的重复实现，并把共享 WHERE 子句里的状态过滤由魔法字符串
// 'settled' 改为显式传入 domain.UsageStatus。注意：此处的 status 常量化仅限本构造器的
// WHERE 子句；各查询 SELECT 列里的状态相关表达式（如 health-timeline 的失败计数）由各
// 查询自行用 domain.UsageSettled 常量构造，不经过本构造器。
//
//   - status 非空时叠加状态过滤（heatmap/calltype 用 domain.UsageSettled）；
//     为空时跳过（health 时间线需统计失败，不能限定状态）。
//   - model/channelID/departmentID/scopeUserID 为零值/空、integrationID 为 nil 时跳过。
//   - tablePrefix 用于多表 JOIN 时的列名消歧（如 calltype 的 "usage_logs."）。
type rawLogStatsQuery struct {
	from          time.Time
	to            time.Time
	status        domain.UsageStatus // 空表示不过滤状态
	scopeUserID   int64              // >0 时限定用户（/me 自助视角）
	model         string
	channelID     int64
	departmentID  int64
	integrationID *int64
	tablePrefix   string // 默认 ""；JOIN 时为 "usage_logs."
}

// apply 把过滤条件挂到给定的查询上，返回可继续链式 Select/Group/Scan 的 *gorm.DB。
func (q rawLogStatsQuery) apply(db *gorm.DB) *gorm.DB {
	out := db.Table("usage_logs")
	p := q.tablePrefix
	if q.status != "" {
		out = out.Where(p+"status = ?", string(q.status))
	}
	out = out.Where(p+"created_at >= ? AND "+p+"created_at < ?", q.from, q.to)
	if q.scopeUserID > 0 {
		out = out.Where(p+"user_id = ?", q.scopeUserID)
	}
	if q.model != "" {
		out = out.Where(p+"model_name = ?", q.model)
	}
	if q.channelID > 0 {
		out = out.Where(p+"channel_id = ?", q.channelID)
	}
	if q.departmentID > 0 {
		out = out.Where(p+"department_id = ?", q.departmentID)
	}
	if q.integrationID != nil {
		out = out.Where(p+"integration_id = ?", *q.integrationID)
	}
	return out
}
