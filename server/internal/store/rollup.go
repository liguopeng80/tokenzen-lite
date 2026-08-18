package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"gorm.io/gorm"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
)

// UsageDailyRollup 对应 usage_daily_rollup 表：用量日志按
// (日期, 用户, 部门, 项目, 模型, 渠道, 接入方, 密钥) 预聚合的报表数据源，只汇总已结算记录。
type UsageDailyRollup struct {
	Day          time.Time `gorm:"primaryKey" json:"day"`
	UserID       int64     `gorm:"primaryKey" json:"user_id"`
	DepartmentID int64     `gorm:"primaryKey" json:"department_id"`
	// ProjectID 记账时点该密钥所属项目的快照。0019 迁移前的历史已汇总日期为 0（按项目不可拆）。
	ProjectID int64  `gorm:"primaryKey" json:"project_id"`
	ModelName string `gorm:"primaryKey" json:"model_name"`
	ChannelID int64  `gorm:"primaryKey" json:"channel_id"`
	// IntegrationID 记账时点用户所属接入方的快照，0 表示本机直管账号。
	IntegrationID int64 `gorm:"column:integration_id;default:0;primaryKey" json:"integration_id"`
	// APIKeyID 记账时点的密钥快照。0015 迁移前的历史已汇总日期为 0（按密钥不可拆）。
	APIKeyID         int64 `gorm:"primaryKey" json:"api_key_id"`
	Requests         int64 `json:"requests"`
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
	CacheReadTokens  int64 `json:"cache_read_tokens"`
	CacheWriteTokens int64 `json:"cache_write_tokens"`
	CreditsCharged   int64 `json:"credits_charged"`
	CreditsCost      int64 `json:"credits_cost"`
}

func (UsageDailyRollup) TableName() string { return "usage_daily_rollup" }

// UsageRollupState 记录已完成汇总的日期水位。报表据此判断某日读聚合表
// 还是读原始日志；清理原始日志前也据此校验该日期确已汇总。
type UsageRollupState struct {
	Day         time.Time `gorm:"primaryKey" json:"day"`
	RowsRolled  int64     `json:"rows_rolled"`
	CompletedAt time.Time `json:"completed_at"`
}

func (UsageRollupState) TableName() string { return "usage_rollup_state" }

// RollupRepo 负责用量按日汇总的生成与查询。
type RollupRepo struct{ db *gorm.DB }

func NewRollupRepo(db *gorm.DB) *RollupRepo { return &RollupRepo{db: db} }

// RollDay 汇总指定自然日（服务器时区）的用量日志。
// 先删后插，重复执行同一日得到相同结果，不会累加。
func (r *RollupRepo) RollDay(ctx context.Context, day time.Time) (int64, error) {
	from := SpendDay(day)
	to := from.AddDate(0, 0, 1)
	var rows int64
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(`DELETE FROM usage_daily_rollup WHERE day = ?`, from).Error; err != nil {
			return fmt.Errorf("清理当日聚合失败: %w", err)
		}
		res := tx.Exec(`
			INSERT INTO usage_daily_rollup (day, user_id, department_id, project_id, model_name, channel_id, integration_id, api_key_id,
				requests, prompt_tokens, completion_tokens, cache_read_tokens, cache_write_tokens,
				credits_charged, credits_cost)
			SELECT ?, user_id, department_id, COALESCE(project_id, 0), model_name, channel_id, integration_id, api_key_id,
				COUNT(*), COALESCE(SUM(prompt_tokens),0), COALESCE(SUM(completion_tokens),0),
				COALESCE(SUM(cache_read_tokens),0), COALESCE(SUM(cache_write_tokens),0),
				COALESCE(SUM(credits_charged),0), COALESCE(SUM(credits_cost),0)
			FROM usage_logs
			WHERE status = ? AND created_at >= ? AND created_at < ?
			GROUP BY user_id, department_id, COALESCE(project_id, 0), model_name, channel_id, integration_id, api_key_id`,
			from, string(domain.UsageSettled), from, to)
		if res.Error != nil {
			return fmt.Errorf("写入当日聚合失败: %w", res.Error)
		}
		rows = res.RowsAffected
		return tx.Exec(`
			INSERT INTO usage_rollup_state (day, rows_rolled, completed_at)
			VALUES (?, ?, now())
			ON CONFLICT (day) DO UPDATE
			SET rows_rolled = EXCLUDED.rows_rolled, completed_at = now()`, from, rows).Error
	})
	return rows, err
}

// RolledThrough 返回已完成汇总的最大日期；从未汇总时返回零值。
func (r *RollupRepo) RolledThrough(ctx context.Context) (time.Time, error) {
	// 空表时聚合函数返回 NULL，必须用可空类型接收，否则扫描直接报错。
	var day sql.NullTime
	err := r.db.WithContext(ctx).Model(&UsageRollupState{}).
		Select("MAX(day)").Scan(&day).Error
	if err != nil || !day.Valid {
		return time.Time{}, err
	}
	return day.Time, nil
}

// PendingDays 返回截至 upTo（含）尚未汇总的日期，最多 limit 天。
// 只从最早一条用量日志所在日开始扫描，避免新装系统空转。
func (r *RollupRepo) PendingDays(ctx context.Context, upTo time.Time, limit int) ([]time.Time, error) {
	var earliest sql.NullTime
	if err := r.db.WithContext(ctx).Model(&UsageLog{}).
		Select("MIN(created_at)").Scan(&earliest).Error; err != nil {
		return nil, err
	}
	if !earliest.Valid {
		return nil, nil
	}
	var done []time.Time
	if err := r.db.WithContext(ctx).Model(&UsageRollupState{}).
		Where("day >= ?", SpendDay(earliest.Time)).Pluck("day", &done).Error; err != nil {
		return nil, err
	}
	rolled := make(map[string]bool, len(done))
	for _, d := range done {
		rolled[d.Format("2006-01-02")] = true
	}
	var pending []time.Time
	for d := SpendDay(earliest.Time); !d.After(SpendDay(upTo)) && len(pending) < limit; d = d.AddDate(0, 0, 1) {
		if !rolled[d.Format("2006-01-02")] {
			pending = append(pending, d)
		}
	}
	return pending, nil
}

// PurgeUsageLogsBefore 清理早于 before（自然日起点）的原始用量日志。
// 仅当被清理范围内的每一天都已完成汇总时才执行，否则返回错误——
// 先删后汇总会让那段时间的报表数据永久消失。
func (r *RollupRepo) PurgeUsageLogsBefore(ctx context.Context, before time.Time) (int64, error) {
	cutoff := SpendDay(before)
	var earliest sql.NullTime
	if err := r.db.WithContext(ctx).Model(&UsageLog{}).
		Select("MIN(created_at)").Scan(&earliest).Error; err != nil {
		return 0, err
	}
	if !earliest.Valid {
		return 0, nil
	}
	for d := SpendDay(earliest.Time); d.Before(cutoff); d = d.AddDate(0, 0, 1) {
		var n int64
		if err := r.db.WithContext(ctx).Model(&UsageRollupState{}).
			Where("day = ?", d).Count(&n).Error; err != nil {
			return 0, err
		}
		if n == 0 {
			return 0, fmt.Errorf("%s 尚未完成用量汇总，暂不清理原始日志", d.Format("2006-01-02"))
		}
	}
	res := r.db.WithContext(ctx).Where("created_at < ?", cutoff).Delete(&UsageLog{})
	return res.RowsAffected, res.Error
}

// AggDimension 费用报表的聚合维度。
type AggDimension string

const (
	AggByUser       AggDimension = "user"
	AggByDepartment AggDimension = "department"
	AggByProject    AggDimension = "project"
	AggByModel      AggDimension = "model"
	AggByChannel    AggDimension = "channel"
	AggByDay        AggDimension = "day"
	AggByKey        AggDimension = "key"
)

// Valid 判断聚合维度取值是否合法。
func (d AggDimension) Valid() bool {
	switch d {
	case AggByUser, AggByDepartment, AggByProject, AggByModel, AggByChannel, AggByDay, AggByKey:
		return true
	}
	return false
}

// AggFilter 费用报表的筛选条件。时间范围按自然日对齐（服务器时区）。
// DepartmentID 指向 0 表示只看未分配部门；ProjectID 指向 0 表示只看未归属项目。
type AggFilter struct {
	From          time.Time
	To            time.Time
	UserID        int64
	DepartmentID  *int64
	ProjectID     *int64
	ModelName     string
	ChannelID     int64
	IntegrationID *int64
	// APIKeyID 大于 0 时只统计指定密钥；0 表示不限。
	APIKeyID int64
}

// AggRow 费用报表的一行。GroupID 是维度对应的实体 ID（按日与按模型维度为 0）。
// CacheReadTokens/CacheWriteTokens 来自聚合表与原始日志两段合并后的求和，
// 既有 cost-report/dept-report 处理函数不消费这两个字段，新增属向后兼容。
type AggRow struct {
	GroupID          int64          `json:"group_id"`
	GroupKey         string         `json:"group_key"`
	Requests         int64          `json:"requests"`
	PromptTokens     int64          `json:"prompt_tokens"`
	CompletionTokens int64          `json:"completion_tokens"`
	CacheReadTokens  int64          `json:"cache_read_tokens"`
	CacheWriteTokens int64          `json:"cache_write_tokens"`
	CreditsCharged   domain.Credits `json:"credits_charged"`
	CreditsCost      domain.Credits `json:"credits_cost"`
	Margin           domain.Credits `json:"margin"`
}

// rollupSplit 计算 [from, to) 内聚合表与原始日志的覆盖分界：聚合表覆盖
// [from, rollupTo)，原始日志覆盖 [rollupTo, to)，两段不重叠。rolledThrough 为零值时
// 表示无已完成汇总，全部读原始日志（rollupTo == from）。
func rollupSplit(from, to, rolledThrough time.Time) time.Time {
	rollupTo := from
	if !rolledThrough.IsZero() {
		if next := SpendDay(rolledThrough).AddDate(0, 0, 1); next.After(from) {
			rollupTo = next
		}
		if rollupTo.After(to) {
			rollupTo = to
		}
	}
	return rollupTo
}

// Aggregate 按维度聚合用量。已完成汇总的日期读聚合表，其余日期读原始日志，
// 两段合并后再聚合，因此开启汇总前后的报表结果一致。
func (r *RollupRepo) Aggregate(ctx context.Context, dim AggDimension, f AggFilter) ([]AggRow, error) {
	from, to := SpendDay(f.From), SpendDay(f.To)
	if !to.After(from) {
		return nil, nil
	}
	rolledThrough, err := r.RolledThrough(ctx)
	if err != nil {
		return nil, err
	}
	return r.aggregateWithRollup(ctx, dim, f, from, to, rolledThrough)
}

// aggregateWithRollup 同 Aggregate，但接受调用方预计算的 rolledThrough 水位与
// 已对齐的 from/to，避免多次聚合（如 OpsSummary 的 4 路查询）重复读 MAX(day)。
func (r *RollupRepo) aggregateWithRollup(ctx context.Context, dim AggDimension, f AggFilter,
	from, to, rolledThrough time.Time) ([]AggRow, error) {
	// 聚合表覆盖 [from, rollupTo)，原始日志覆盖 [rawFrom, to)，两段不重叠。
	rollupTo := rollupSplit(from, to, rolledThrough)
	rawFrom := rollupTo

	groupExpr, keyExpr := aggExprs(dim)
	where, args := aggConditions(f)

	// 局部变量避免与 database/sql 包名冲突。
	// cache_read_tokens/cache_write_tokens 在两段 UNION 中都投影（聚合表与原始日志均有此列），
	// 外层再 SUM，使缓存分析报表与费用报表共用同一条保留期安全的聚合路径。
	query := fmt.Sprintf(`
		SELECT %s AS group_id, %s AS group_key,
			COALESCE(SUM(requests),0) AS requests,
			COALESCE(SUM(prompt_tokens),0) AS prompt_tokens,
			COALESCE(SUM(completion_tokens),0) AS completion_tokens,
			COALESCE(SUM(cache_read_tokens),0) AS cache_read_tokens,
			COALESCE(SUM(cache_write_tokens),0) AS cache_write_tokens,
			COALESCE(SUM(credits_charged),0) AS credits_charged,
			COALESCE(SUM(credits_cost),0) AS credits_cost,
			COALESCE(SUM(credits_charged - credits_cost),0) AS margin
		FROM (
			SELECT day, user_id, department_id, project_id, model_name, channel_id, integration_id, api_key_id,
				requests, prompt_tokens, completion_tokens,
				cache_read_tokens, cache_write_tokens,
				credits_charged, credits_cost
			FROM usage_daily_rollup
			WHERE day >= ? AND day < ?
			UNION ALL
			SELECT (created_at AT TIME ZONE ?)::date, user_id, department_id, COALESCE(project_id, 0), model_name, channel_id, integration_id, api_key_id,
				1, prompt_tokens, completion_tokens,
				cache_read_tokens, cache_write_tokens,
				credits_charged, credits_cost
			FROM usage_logs
			WHERE status = ? AND created_at >= ? AND created_at < ?
		) src
		LEFT JOIN users u ON u.id = src.user_id
		LEFT JOIN departments d ON d.id = src.department_id
		LEFT JOIN projects p ON p.id = src.project_id
		LEFT JOIN channels c ON c.id = src.channel_id
		LEFT JOIN api_keys k ON k.id = src.api_key_id
		WHERE %s
		GROUP BY group_id, group_key
		ORDER BY credits_charged DESC`, groupExpr, keyExpr, where)

	// 原始日志段的日期按服务器时区换算，与聚合表 day 列（Go 侧按同一时区
	// 计算后写入）口径一致；不依赖数据库会话时区。
	// usage_logs 段用 domain.UsageSettled 限定已结算记录，与聚合表的预过滤口径一致。
	callArgs := append([]any{from, rollupTo, LocalZoneName(), string(domain.UsageSettled), rawFrom, to}, args...)
	var rows []AggRow
	err := r.db.WithContext(ctx).Raw(query, callArgs...).Scan(&rows).Error
	return rows, err
}

// aggExprs 返回聚合维度对应的 (分组 ID 表达式, 分组显示名表达式)。
func aggExprs(dim AggDimension) (string, string) {
	switch dim {
	case AggByUser:
		return "src.user_id",
			"COALESCE(u.username, '已删除用户 #' || src.user_id::text)"
	case AggByDepartment:
		return "src.department_id",
			`CASE WHEN src.department_id = 0 THEN '未分配'
			      ELSE COALESCE(d.name, '已删除部门 #' || src.department_id::text) END`
	case AggByProject:
		// project_id=0 是合法值（密钥未挂项目，与 department_id=0「未分配」同口径），
		// 故沿用部门范式标记为「未归属」，而不像 api_key_id=0 那样仅代表历史汇总。
		// 0019 迁移前的历史已汇总日期 project_id=0，亦合并进同一「未归属」桶，不再单独区分。
		return "src.project_id",
			`CASE WHEN src.project_id = 0 THEN '未归属'
			      ELSE COALESCE(p.name, '已删除项目 #' || src.project_id::text) END`
	case AggByChannel:
		return "src.channel_id",
			"COALESCE(c.name, '未知渠道 #' || src.channel_id::text)"
	case AggByKey:
		// api_key_id=0 是 0015 迁移前的历史已汇总数据，按密钥维度不可再拆分，
		// 单独标记为「历史汇总」，避免与真实存在的 #0 密钥混淆。
		return "src.api_key_id",
			`CASE WHEN src.api_key_id = 0 THEN '历史汇总（按密钥不可拆）'
			      ELSE COALESCE(k.name, '密钥 #' || src.api_key_id::text) END`
	case AggByDay:
		return "0", "to_char(src.day, 'YYYY-MM-DD')"
	default:
		return "0", "src.model_name"
	}
}

// aggConditions 把筛选条件编译为 WHERE 片段。
func aggConditions(f AggFilter) (string, []any) {
	where, args := "1 = 1", []any{}
	if f.UserID > 0 {
		where += " AND src.user_id = ?"
		args = append(args, f.UserID)
	}
	if f.DepartmentID != nil {
		where += " AND src.department_id = ?"
		args = append(args, *f.DepartmentID)
	}
	if f.ProjectID != nil {
		where += " AND src.project_id = ?"
		args = append(args, *f.ProjectID)
	}
	if f.ModelName != "" {
		where += " AND src.model_name = ?"
		args = append(args, f.ModelName)
	}
	if f.ChannelID > 0 {
		where += " AND src.channel_id = ?"
		args = append(args, f.ChannelID)
	}
	if f.APIKeyID > 0 {
		where += " AND src.api_key_id = ?"
		args = append(args, f.APIKeyID)
	}
	if f.IntegrationID != nil {
		where += " AND src.integration_id = ?"
		args = append(args, *f.IntegrationID)
	}
	return where, args
}
