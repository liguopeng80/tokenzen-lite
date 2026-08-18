package api

import (
	"fmt"
	"testing"
	"time"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// TestAdminHealthTimeline 覆盖管理端健康度时间线：
// 200、points 非空、fail_rate 落在 [0,1]、分位字段非负、bucket 字段合法。
func TestAdminHealthTimeline(t *testing.T) {
	e := newTestEnv(t)
	rootC := e.seedAndLogin(t, "healthroot", domain.RoleRoot)
	e.seedAndLogin(t, "healthuser", domain.RoleUser)
	uid := e.userIDByName(t, "healthuser")

	anchor := time.Date(2026, 8, 3, 9, 0, 0, 0, time.Local)
	// 5 条 settled + 1 条 failed，落进同一个小时桶
	for i, lat := range []int64{10, 20, 20, 30, 100} {
		e.seedUsageLog(t, store.UsageLog{
			RequestID: fmt.Sprintf("api-h1-%d", i), UserID: uid, APIKeyID: 1,
			ModelName: "glm-5", LatencyMS: lat, CreatedAt: anchor,
		})
	}
	e.seedUsageLog(t, store.UsageLog{
		RequestID: "api-h2-failed", UserID: uid, APIKeyID: 1,
		ModelName: "glm-5", LatencyMS: 200, CreatedAt: anchor.Add(time.Hour),
		Status: domain.UsageFailed,
	})

	start := anchor.Add(-1 * time.Hour).Unix()
	end := anchor.Add(2 * time.Hour).Unix()
	resp, env := e.do(t, rootC, "GET",
		fmt.Sprintf("/api/admin/stats/health-timeline?start_timestamp=%d&end_timestamp=%d&bucket=hour",
			start, end), nil)
	if resp.StatusCode != 200 {
		t.Fatalf("健康度时间线应 200，实际 %d %v", resp.StatusCode, env)
	}

	data, ok := env["data"].(map[string]any)
	if !ok {
		t.Fatalf("响应应为信封: %v", env)
	}
	if bucket, _ := data["bucket"].(string); bucket != "hour" {
		t.Errorf("bucket 应为 hour，实际 %q", bucket)
	}
	points, ok := data["points"].([]any)
	if !ok {
		t.Fatalf("points 应为数组: %v", data)
	}
	if len(points) == 0 {
		t.Fatalf("points 不应为空")
	}
	for _, p := range points {
		pt := p.(map[string]any)
		failRate := pt["fail_rate"].(float64)
		if failRate < 0 || failRate > 1 {
			t.Errorf("fail_rate 应在 [0,1]，实际 %v", failRate)
		}
		for _, k := range []string{"p50_ms", "p95_ms", "p99_ms"} {
			v := int64(pt[k].(float64))
			if v < 0 {
				t.Errorf("%s 应非负，实际 %d", k, v)
			}
		}
	}
}

// TestAdminHealthTimelineDefaultBucket 覆盖 bucket 默认推导：
// 窗口 ≤7 天默认 hour；超过 7 天默认 day。仅校验 bucket 字段，不依赖数据。
func TestAdminHealthTimelineDefaultBucket(t *testing.T) {
	e := newTestEnv(t)
	rootC := e.seedAndLogin(t, "healthroot2", domain.RoleRoot)

	// 24h 窗口 → hour
	end := time.Now().Unix()
	start := time.Now().Add(-24 * time.Hour).Unix()
	resp, env := e.do(t, rootC, "GET",
		fmt.Sprintf("/api/admin/stats/health-timeline?start_timestamp=%d&end_timestamp=%d", start, end), nil)
	if resp.StatusCode != 200 {
		t.Fatalf("24h 窗口应 200，实际 %d %v", resp.StatusCode, env)
	}
	if bucket := env["data"].(map[string]any)["bucket"]; bucket != "hour" {
		t.Errorf("24h 窗口默认 bucket 应 hour，实际 %v", bucket)
	}

	// 10 天窗口 → day
	start = time.Now().Add(-10 * 24 * time.Hour).Unix()
	resp, env = e.do(t, rootC, "GET",
		fmt.Sprintf("/api/admin/stats/health-timeline?start_timestamp=%d&end_timestamp=%d", start, end), nil)
	if resp.StatusCode != 200 {
		t.Fatalf("10 天窗口应 200，实际 %d %v", resp.StatusCode, env)
	}
	if bucket := env["data"].(map[string]any)["bucket"]; bucket != "day" {
		t.Errorf("10 天窗口默认 bucket 应 day，实际 %v", bucket)
	}
}
