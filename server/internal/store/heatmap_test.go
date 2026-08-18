package store

import (
	"strconv"
	"testing"
	"time"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
)

func itoa(i int) string { return strconv.Itoa(i) }

// TestHeatmapBucketing 覆盖周×时热力图的分桶正确性：
// 按服务器本地时区换算 weekday/hour，只统计 settled，且只返回产生数据的格子。
func TestHeatmapBucketing(t *testing.T) {
	db := newStoreTestDB(t)
	repo := NewStatsRepo(db)

	// 选定已知本地时刻：2026-08-03（周一）09:30 与 2026-08-04（周二）18:10。
	// Go 的 Weekday() 与 PG 的 EXTRACT(DOW ...) 同语义（0=周日..6=周六）。
	mondayAM := time.Date(2026, 8, 3, 9, 30, 0, 0, time.Local)
	tuesdayPM := time.Date(2026, 8, 4, 18, 10, 0, 0, time.Local)
	for i, at := range []time.Time{mondayAM, mondayAM, tuesdayPM} {
		if err := db.Create(&UsageLog{
			RequestID: "hm-" + at.Format("150405") + "-" + itoa(i), UserID: 1, APIKeyID: 1,
			ModelName: "glm-5", ChannelID: 1, CreditsCharged: 100,
			Status: domain.UsageSettled, CreatedAt: at,
		}).Error; err != nil {
			t.Fatalf("种入用量日志失败: %v", err)
		}
	}
	// 非 settled 不计入。
	if err := db.Create(&UsageLog{
		RequestID: "hm-failed", UserID: 1, APIKeyID: 1, ModelName: "glm-5",
		CreditsCharged: 999, Status: domain.UsageFailed, CreatedAt: mondayAM,
	}).Error; err != nil {
		t.Fatalf("种入失败日志失败: %v", err)
	}
	// 他人的日志在 scopeUserID 限定下不应出现。
	if err := db.Create(&UsageLog{
		RequestID: "hm-other", UserID: 2, APIKeyID: 2, ModelName: "glm-5",
		CreditsCharged: 1, Status: domain.UsageSettled, CreatedAt: mondayAM,
	}).Error; err != nil {
		t.Fatalf("种入他人日志失败: %v", err)
	}

	from := mondayAM.AddDate(0, 0, -1)
	to := tuesdayPM.AddDate(0, 0, 1)
	cells, err := repo.Heatmap(t.Context(), 1, from, to, "", 0, 0, nil)
	if err != nil {
		t.Fatalf("Heatmap 查询失败: %v", err)
	}

	// 合计请求 = 3（A 的全部 settled），credits = 300。
	var totalReqs, totalCredits int64
	cellAt := map[[2]int]int64{}
	for _, c := range cells {
		// 维度合法性
		if c.DayOfWeek < 0 || c.DayOfWeek > 6 {
			t.Errorf("day_of_week 应在 0..6，实际 %d", c.DayOfWeek)
		}
		if c.Hour < 0 || c.Hour > 23 {
			t.Errorf("hour 应在 0..23，实际 %d", c.Hour)
		}
		totalReqs += c.Requests
		totalCredits += c.Credits
		cellAt[[2]int{c.DayOfWeek, c.Hour}] = c.Requests
	}
	if totalReqs != 3 {
		t.Errorf("合计请求数应为 3，实际 %d", totalReqs)
	}
	if totalCredits != 300 {
		t.Errorf("合计扣费应为 300，实际 %d", totalCredits)
	}

	// 周一 09:00 桶 = 2 条；周二 18:00 桶 = 1 条。
	mondayBucket := cellAt[[2]int{int(mondayAM.Weekday()), mondayAM.Hour()}]
	if mondayBucket != 2 {
		t.Errorf("周一 %02d:00 桶应 2 条，实际 %d", mondayAM.Hour(), mondayBucket)
	}
	tuesdayBucket := cellAt[[2]int{int(tuesdayPM.Weekday()), tuesdayPM.Hour()}]
	if tuesdayBucket != 1 {
		t.Errorf("周二 %02d:00 桶应 1 条，实际 %d", tuesdayPM.Hour(), tuesdayBucket)
	}
}

// TestHeatmapFilters 覆盖 model / channel / department 筛选叠加。
func TestHeatmapFilters(t *testing.T) {
	db := newStoreTestDB(t)
	repo := NewStatsRepo(db)

	anchor := time.Date(2026, 8, 3, 10, 0, 0, 0, time.Local)
	logs := []UsageLog{
		{RequestID: "f-1", UserID: 1, APIKeyID: 1, ModelName: "glm-5", ChannelID: 1,
			DepartmentID: 10, CreditsCharged: 100, Status: domain.UsageSettled, CreatedAt: anchor},
		{RequestID: "f-2", UserID: 1, APIKeyID: 1, ModelName: "qwen", ChannelID: 2,
			DepartmentID: 20, CreditsCharged: 200, Status: domain.UsageSettled, CreatedAt: anchor},
	}
	for i := range logs {
		if err := db.Create(&logs[i]).Error; err != nil {
			t.Fatalf("种入用量日志失败: %v", err)
		}
	}

	from := anchor.AddDate(0, 0, -1)
	to := anchor.AddDate(0, 0, 1)

	// 按 model 筛选：只剩 glm-5 的 1 条。
	cells, err := repo.Heatmap(t.Context(), 0, from, to, "glm-5", 0, 0, nil)
	if err != nil {
		t.Fatalf("model 筛选查询失败: %v", err)
	}
	if got := sumRequests(cells); got != 1 {
		t.Errorf("model=glm-5 应 1 条，实际 %d", got)
	}

	// 按 channel 筛选：channel_id=2 只剩 1 条。
	cells, err = repo.Heatmap(t.Context(), 0, from, to, "", 2, 0, nil)
	if err != nil {
		t.Fatalf("channel 筛选查询失败: %v", err)
	}
	if got := sumRequests(cells); got != 1 {
		t.Errorf("channel_id=2 应 1 条，实际 %d", got)
	}

	// 按 department 筛选：department_id=10 只剩 1 条。
	cells, err = repo.Heatmap(t.Context(), 0, from, to, "", 0, 10, nil)
	if err != nil {
		t.Fatalf("department 筛选查询失败: %v", err)
	}
	if got := sumRequests(cells); got != 1 {
		t.Errorf("department_id=10 应 1 条，实际 %d", got)
	}
}

func sumRequests(cells []HeatmapCell) int64 {
	var n int64
	for _, c := range cells {
		n += c.Requests
	}
	return n
}
