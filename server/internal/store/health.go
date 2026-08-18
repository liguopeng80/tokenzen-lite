package store

import (
	"context"
	"math"
	"time"

	"gorm.io/gorm"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
)

// HealthPoint 是健康度时间线的一个时间桶：请求量、失败率与延迟分位。
// 单位与口径：requests/failed 为条数；fail_rate 在 [0,1]；延迟字段单位为毫秒。
type HealthPoint struct {
	// BucketStart 桶起始时刻（服务器本地时区），由 (created_at AT TIME ZONE ?) 换算。
	// JSON 序列化为 RFC3339 时间戳，前端按 bucket 维度格式化横轴标签。
	BucketStart time.Time `json:"bucket_start"`
	Requests    int64     `json:"requests"`
	Failed      int64     `json:"failed"`    // status != 'settled' 的条数
	FailRate    float64   `json:"fail_rate"` // failed/requests（无请求时为 0）
	P50MS       int64     `json:"p50_ms"`
	P95MS       int64     `json:"p95_ms"`
	P99MS       int64     `json:"p99_ms"`
}

// healthScanRow 用于扫描 SQL 结果：分位先按 float64 读出（percentile_cont
// 的返回类型），再在 Go 侧取整为 int64 装入 HealthPoint。
type healthScanRow struct {
	BucketStart time.Time `gorm:"column:bucket_start"`
	Requests    int64     `gorm:"column:requests"`
	Failed      int64     `gorm:"column:failed"`
	P50MS       float64   `gorm:"column:p50_ms"`
	P95MS       float64   `gorm:"column:p95_ms"`
	P99MS       float64   `gorm:"column:p99_ms"`
}

// HealthTimeline 按小时或日分桶聚合中继健康度：请求量、失败率、延迟 avg/p50/p95/p99。
//
// 数据来源是原始 usage_logs（按日汇总表不含 latency_ms 维度），因此受用量日志保留期
// 约束——这是 OPS 视角近期窗口（典型 24–72h）的视图，落在保留窗口内。
//
//   - bucket 取 "hour" 或 "day"，其他值按 "hour" 处理；
//   - model / channelID 非空/非零时叠加筛选；
//   - integrationID 非 nil 时按接入方作用域收窄（托管视角），与 heatmap/cost-report 同口径；
//   - 统计全部 status（含 failed/refunded），失败率才有意义；失败请求的 latency_ms 仍计入分位；
//   - 时间按服务器本地时区换算后再分桶，与 heatmap 同口径，不依赖 PG 会话时区。
//
// 返回值按 bucket_start 升序；空窗口返回长度为 0 的切片（由 handler 兜底为 []）。
func (r *StatsRepo) HealthTimeline(
	ctx context.Context,
	from, to time.Time,
	bucket string,
	model string,
	channelID int64,
	integrationID *int64,
) ([]HealthPoint, error) {
	zone := LocalZoneName()
	// created_at 是 TIMESTAMPTZ，(created_at AT TIME ZONE zone) 返回本地挂钟时间
	// （timestamp without time zone），再按 hour/day 截断分桶。
	// ::timestamp 显式标注类型，避免 date_trunc 的结果随 PG 版本变化。
	var bucketExpr string
	if bucket == "day" {
		bucketExpr = "(created_at AT TIME ZONE ?)::date"
	} else {
		bucket = "hour"
		bucketExpr = "date_trunc('hour', (created_at AT TIME ZONE ?)::timestamp)"
	}
	// 不限定 status：健康度时间线需统计失败率，失败请求也要进入分桶。
	// 失败计数用 domain.UsageSettled 常量替代魔法字符串，与 rawLogStatsQuery 同口径。
	failedExpr := "COUNT(*) FILTER (WHERE status <> '" + string(domain.UsageSettled) + "') AS failed"
	q := rawLogStatsQuery{
		from: from, to: to, // status 留空 → 不过滤
		model: model, channelID: channelID, integrationID: integrationID,
	}.apply(r.db.WithContext(ctx)).
		Select(bucketExpr+` AS bucket_start,
			COUNT(*) AS requests,
			`+failedExpr+`,
			COALESCE(percentile_cont(0.50) WITHIN GROUP (ORDER BY latency_ms),0) AS p50_ms,
			COALESCE(percentile_cont(0.95) WITHIN GROUP (ORDER BY latency_ms),0) AS p95_ms,
			COALESCE(percentile_cont(0.99) WITHIN GROUP (ORDER BY latency_ms),0) AS p99_ms`,
			zone)

	var rows []healthScanRow
	err := q.Group("bucket_start").Order("bucket_start").Scan(&rows).Error
	if err == gorm.ErrRecordNotFound {
		return []HealthPoint{}, nil
	}
	if err != nil {
		return nil, err
	}

	points := make([]HealthPoint, 0, len(rows))
	for _, row := range rows {
		failRate := float64(0)
		if row.Requests > 0 {
			failRate = float64(row.Failed) / float64(row.Requests)
		}
		// percentile_cont 的连续插值在浮点运算下可能产生 85.9999... 这类本应是整数
		// 的结果（数学上为 86）。math.Round 取整到最接近的整数，避免 OPS 看到少 1ms 的误导值。
		points = append(points, HealthPoint{
			// 分桶表达式返回 timestamp without time zone（本地挂钟），pgx 读入 time.Time
			// 时按 UTC 标注。这里把同样的挂钟数字重新挂在 time.Local 上，使 JSON 序列化
			// 带本地偏移，前端 dayjs 解析后格式化出的时间标签与桶的本地小时一致。
			BucketStart: localWallClock(row.BucketStart),
			Requests:    row.Requests,
			Failed:      row.Failed,
			FailRate:    failRate,
			P50MS:       int64(math.Round(row.P50MS)),
			P95MS:       int64(math.Round(row.P95MS)),
			P99MS:       int64(math.Round(row.P99MS)),
		})
	}
	return points, nil
}
