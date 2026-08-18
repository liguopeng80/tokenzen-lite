package store

import (
	"testing"
	"time"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
)

// TestHealthTimelinePercentiles 覆盖延迟分位的连续插值计算与失败率统计。
//
// 设计：桶 H1 放 5 条 settled（latency [10,20,20,30,100]），与任务规格示例一致，
// percentile_cont(0.5/0.95/0.99) 的预期值可手算：
//   - p50: 0.5*(5-1)=2.0      → v[2]=20
//   - p95: 0.95*4=3.8         → v[3]+0.8*(v[4]-v[3]) = 30+0.8*70 = 86
//   - p99: 0.99*4=3.96        → 30+0.96*70 = 97.2 → 截断 97
//
// 桶 H2 放 1 条 failed，验证 failed 计数与非 settled 也进入聚合。
func TestHealthTimelinePercentiles(t *testing.T) {
	db := newStoreTestDB(t)
	repo := NewStatsRepo(db)

	anchor := time.Date(2026, 8, 3, 9, 0, 0, 0, time.Local)
	// 桶 H1（09:00）的 5 条 settled
	for i, lat := range []int64{10, 20, 20, 30, 100} {
		if err := db.Create(&UsageLog{
			RequestID: "ht-h1-" + itoa(i), UserID: 1, APIKeyID: 1,
			ModelName: "glm-5", ChannelID: 1, LatencyMS: lat,
			Status: domain.UsageSettled, CreatedAt: anchor,
		}).Error; err != nil {
			t.Fatalf("种入 settled 日志失败: %v", err)
		}
	}
	// 桶 H2（10:00）的 1 条 failed
	h2 := anchor.Add(time.Hour)
	if err := db.Create(&UsageLog{
		RequestID: "ht-h2-failed", UserID: 1, APIKeyID: 1,
		ModelName: "glm-5", ChannelID: 1, LatencyMS: 200,
		Status: domain.UsageFailed, CreatedAt: h2,
	}).Error; err != nil {
		t.Fatalf("种入 failed 日志失败: %v", err)
	}

	from := anchor.Add(-1 * time.Hour)
	to := h2.Add(time.Hour)
	points, err := repo.HealthTimeline(t.Context(), from, to, "hour", "", 0, nil)
	if err != nil {
		t.Fatalf("HealthTimeline 查询失败: %v", err)
	}
	if len(points) != 2 {
		t.Fatalf("应返回 2 个小时桶，实际 %d", len(points))
	}

	// 升序：H1 在前，H2 在后
	h1, h2Point := points[0], points[1]
	if !h1.BucketStart.Equal(anchor) {
		t.Errorf("H1 bucket_start 应为 %v，实际 %v", anchor, h1.BucketStart)
	}

	// H1：5 条 settled，无失败，分位与均值手算值
	if h1.Requests != 5 {
		t.Errorf("H1 requests 应 5，实际 %d", h1.Requests)
	}
	if h1.Failed != 0 {
		t.Errorf("H1 failed 应 0，实际 %d", h1.Failed)
	}
	if h1.FailRate != 0 {
		t.Errorf("H1 fail_rate 应 0，实际 %v", h1.FailRate)
	}
	if h1.P50MS != 20 {
		t.Errorf("H1 p50_ms 应 20，实际 %d", h1.P50MS)
	}
	if h1.P95MS != 86 {
		t.Errorf("H1 p95_ms 应 86，实际 %d", h1.P95MS)
	}
	if h1.P99MS != 97 {
		t.Errorf("H1 p99_ms 应 97，实际 %d", h1.P99MS)
	}

	// H2：1 条 failed，fail_rate=1，p50/p95/p99 都等于自身 latency
	if h2Point.Requests != 1 {
		t.Errorf("H2 requests 应 1，实际 %d", h2Point.Requests)
	}
	if h2Point.Failed != 1 {
		t.Errorf("H2 failed 应 1，实际 %d", h2Point.Failed)
	}
	if h2Point.FailRate != 1 {
		t.Errorf("H2 fail_rate 应 1，实际 %v", h2Point.FailRate)
	}
	if h2Point.P50MS != 200 || h2Point.P95MS != 200 || h2Point.P99MS != 200 {
		t.Errorf("H2 分位应全为 200，实际 p50=%d p95=%d p99=%d",
			h2Point.P50MS, h2Point.P95MS, h2Point.P99MS)
	}
}

// TestHealthTimelineDayBucketAndFilters 覆盖按日分桶、model/channel 筛选。
func TestHealthTimelineDayBucketAndFilters(t *testing.T) {
	db := newStoreTestDB(t)
	repo := NewStatsRepo(db)

	day1 := time.Date(2026, 8, 3, 10, 0, 0, 0, time.Local)
	day2 := time.Date(2026, 8, 4, 22, 0, 0, 0, time.Local)
	logs := []UsageLog{
		{RequestID: "d1-glm-1", UserID: 1, APIKeyID: 1, ModelName: "glm-5",
			ChannelID: 1, LatencyMS: 100, Status: domain.UsageSettled, CreatedAt: day1},
		{RequestID: "d1-glm-2", UserID: 1, APIKeyID: 1, ModelName: "glm-5",
			ChannelID: 1, LatencyMS: 300, Status: domain.UsageSettled, CreatedAt: day1},
		{RequestID: "d1-qwen", UserID: 1, APIKeyID: 1, ModelName: "qwen",
			ChannelID: 2, LatencyMS: 500, Status: domain.UsageSettled, CreatedAt: day1},
		{RequestID: "d2-glm", UserID: 1, APIKeyID: 1, ModelName: "glm-5",
			ChannelID: 1, LatencyMS: 200, Status: domain.UsageFailed, CreatedAt: day2},
	}
	for i := range logs {
		if err := db.Create(&logs[i]).Error; err != nil {
			t.Fatalf("种入用量日志失败: %v", err)
		}
	}

	from := day1.AddDate(0, 0, -1)
	to := day2.AddDate(0, 0, 1)

	// 按日分桶：2 个桶
	points, err := repo.HealthTimeline(t.Context(), from, to, "day", "", 0, nil)
	if err != nil {
		t.Fatalf("day 分桶查询失败: %v", err)
	}
	if len(points) != 2 {
		t.Fatalf("按日应 2 桶，实际 %d", len(points))
	}

	// 按 model=glm-5 筛选：剩 3 条
	points, err = repo.HealthTimeline(t.Context(), from, to, "day", "glm-5", 0, nil)
	if err != nil {
		t.Fatalf("model 筛选查询失败: %v", err)
	}
	var totalReqs int64
	for _, p := range points {
		totalReqs += p.Requests
	}
	if totalReqs != 3 {
		t.Errorf("model=glm-5 应 3 条，实际 %d", totalReqs)
	}

	// 按 channel_id=2 筛选：剩 1 条
	points, err = repo.HealthTimeline(t.Context(), from, to, "day", "", 2, nil)
	if err != nil {
		t.Fatalf("channel 筛选查询失败: %v", err)
	}
	totalReqs = 0
	for _, p := range points {
		totalReqs += p.Requests
	}
	if totalReqs != 1 {
		t.Errorf("channel_id=2 应 1 条，实际 %d", totalReqs)
	}

	// 非法 bucket 值按 hour 处理，不应 panic
	if _, err := repo.HealthTimeline(t.Context(), from, to, "weird", "", 0, nil); err != nil {
		t.Fatalf("非法 bucket 应按 hour 兜底，不应报错: %v", err)
	}
}

// TestHealthTimelineEmptyWindow 空窗口返回空切片而非 nil。
func TestHealthTimelineEmptyWindow(t *testing.T) {
	db := newStoreTestDB(t)
	repo := NewStatsRepo(db)

	from := time.Date(2026, 8, 3, 9, 0, 0, 0, time.Local)
	to := from.Add(time.Hour)
	points, err := repo.HealthTimeline(t.Context(), from, to, "hour", "", 0, nil)
	if err != nil {
		t.Fatalf("空窗口查询失败: %v", err)
	}
	if points == nil {
		t.Fatalf("空窗口应返回非 nil 切片")
	}
	if len(points) != 0 {
		t.Errorf("空窗口应 0 桶，实际 %d", len(points))
	}
}
