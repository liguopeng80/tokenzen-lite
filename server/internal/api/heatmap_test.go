package api

import (
	"fmt"
	"testing"
	"time"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// TestMeHeatmap 覆盖用户侧活跃时段热力图：
// 200、cells 维度合法（day_of_week 0..6、hour 0..23）、且只统计本人 settled 日志。
func TestMeHeatmap(t *testing.T) {
	e := newTestEnv(t)
	userAC := e.seedAndLogin(t, "heatuserA", domain.RoleUser)
	e.seedAndLogin(t, "heatuserB", domain.RoleUser)
	idA := e.userIDByName(t, "heatuserA")
	idB := e.userIDByName(t, "heatuserB")

	// A 的三条 settled 日志：固定到已知本地时刻，便于断言落桶正确。
	// 周计算用 Go 的 Weekday（与 PG EXTRACT(DOW) 同语义：0=周日..6=周六）。
	mondayMorning := time.Date(2026, 8, 3, 9, 30, 0, 0, time.Local) // 2026-08-03 是周一
	tuesdayEvening := mondayMorning.AddDate(0, 0, 1).Add(9 * time.Hour)
	for i, ts := range []time.Time{mondayMorning, mondayMorning, tuesdayEvening} {
		e.seedUsageLog(t, store.UsageLog{
			RequestID: fmt.Sprintf("heat-a-%d", i), UserID: idA, APIKeyID: 1,
			ModelName: "glm-5", CreditsCharged: 100, CreatedAt: ts,
		})
	}
	// B 的日志不应出现在 A 的热力图里。
	e.seedUsageLog(t, store.UsageLog{
		RequestID: "heat-b-1", UserID: idB, APIKeyID: 2,
		ModelName: "glm-5", CreditsCharged: 999, CreatedAt: mondayMorning,
	})
	// A 的非 settled 日志不计入。
	e.seedUsageLog(t, store.UsageLog{
		RequestID: "heat-a-failed", UserID: idA, APIKeyID: 1,
		ModelName: "glm-5", CreditsCharged: 777, CreatedAt: mondayMorning,
		Status: domain.UsageFailed,
	})

	// 用覆盖种入时刻的显式区间，避免依赖默认 30 天回看的边界。
	start := mondayMorning.AddDate(0, 0, -1).Unix()
	end := tuesdayEvening.AddDate(0, 0, 1).Unix()
	resp, env := e.do(t, userAC, "GET",
		fmt.Sprintf("/api/me/heatmap?start_timestamp=%d&end_timestamp=%d", start, end), nil)
	if resp.StatusCode != 200 {
		t.Fatalf("用户侧热力图应 200，实际 %d %v", resp.StatusCode, env)
	}
	data, ok := env["data"].(map[string]any)
	if !ok {
		t.Fatalf("响应应为信封: %v", env)
	}
	cells, ok := data["cells"].([]any)
	if !ok {
		t.Fatalf("cells 应为数组: %v", data)
	}

	// 维度合法性
	for _, c := range cells {
		cell := c.(map[string]any)
		dow := int(cell["day_of_week"].(float64))
		hour := int(cell["hour"].(float64))
		if dow < 0 || dow > 6 {
			t.Errorf("day_of_week 应在 0..6，实际 %d", dow)
		}
		if hour < 0 || hour > 23 {
			t.Errorf("hour 应在 0..23，实际 %d", hour)
		}
	}

	// 周一 09:00 桶应有 2 条请求（A 的两条 settled），credits=200。
	wantDOW := int(mondayMorning.Weekday()) // Monday=1
	wantHour := mondayMorning.Hour()        // 9
	var mondayReqs int64
	for _, c := range cells {
		cell := c.(map[string]any)
		if int(cell["day_of_week"].(float64)) == wantDOW && int(cell["hour"].(float64)) == wantHour {
			mondayReqs = int64(cell["requests"].(float64))
		}
	}
	if mondayReqs != 2 {
		t.Errorf("周一 %02d:00 桶应 2 条（A 的 settled），实际 %d", wantHour, mondayReqs)
	}

	// 合计请求数应为 3（A 的全部 settled，B 与 failed 不计入）
	var total int64
	for _, c := range cells {
		total += int64(c.(map[string]any)["requests"].(float64))
	}
	if total != 3 {
		t.Errorf("合计请求数应为 3（仅 A 的 settled），实际 %d", total)
	}
}

// TestAdminHeatmap 覆盖管理端活跃时段热力图：
// 200、按 user_id 筛选生效、不带 user_id 为全站视角。
func TestAdminHeatmap(t *testing.T) {
	e := newTestEnv(t)
	rootC := e.seedAndLogin(t, "heatroot", domain.RoleRoot)
	e.seedAndLogin(t, "heattarget", domain.RoleUser)
	e.seedAndLogin(t, "heatother", domain.RoleUser)
	idTarget := e.userIDByName(t, "heattarget")
	idOther := e.userIDByName(t, "heatother")

	anchor := time.Date(2026, 8, 3, 14, 0, 0, 0, time.Local)
	e.seedUsageLog(t, store.UsageLog{
		RequestID: "heat-tgt-1", UserID: idTarget, APIKeyID: 1,
		ModelName: "glm-5", CreditsCharged: 100, CreatedAt: anchor,
	})
	e.seedUsageLog(t, store.UsageLog{
		RequestID: "heat-tgt-2", UserID: idTarget, APIKeyID: 1,
		ModelName: "glm-5", CreditsCharged: 50, CreatedAt: anchor,
	})
	e.seedUsageLog(t, store.UsageLog{
		RequestID: "heat-other-1", UserID: idOther, APIKeyID: 2,
		ModelName: "glm-5", CreditsCharged: 999, CreatedAt: anchor,
	})

	start := anchor.AddDate(0, 0, -1).Unix()
	end := anchor.AddDate(0, 0, 1).Unix()

	// 按 user_id 收窄到 target：合计 2 条
	resp, env := e.do(t, rootC, "GET",
		fmt.Sprintf("/api/admin/stats/heatmap?user_id=%d&start_timestamp=%d&end_timestamp=%d",
			idTarget, start, end), nil)
	if resp.StatusCode != 200 {
		t.Fatalf("管理端热力图应 200，实际 %d %v", resp.StatusCode, env)
	}
	data := env["data"].(map[string]any)
	cells := data["cells"].([]any)
	var total int64
	for _, c := range cells {
		total += int64(c.(map[string]any)["requests"].(float64))
	}
	if total != 2 {
		t.Errorf("按 user_id 筛选应只剩 target 的 2 条，实际 %d", total)
	}

	// 不带 user_id 的全站视角：合计 3 条
	resp, env = e.do(t, rootC, "GET",
		fmt.Sprintf("/api/admin/stats/heatmap?start_timestamp=%d&end_timestamp=%d", start, end), nil)
	if resp.StatusCode != 200 {
		t.Fatalf("全站热力图应 200，实际 %d %v", resp.StatusCode, env)
	}
	cells = env["data"].(map[string]any)["cells"].([]any)
	total = 0
	for _, c := range cells {
		total += int64(c.(map[string]any)["requests"].(float64))
	}
	if total != 3 {
		t.Errorf("全站热力图合计应为 3，实际 %d", total)
	}
}

// TestAdminHeatmapEndOfDayIncludesToday 是「末日后一天被丢掉」缺陷的回归守卫。
//
// 缺陷根因：前端发送 end_timestamp = dayjs().endOf('day').unix()（当日 23:59:59），
// 旧实现对其取 SpendDay 截断到当日 0 点，配合 store 的 created_at < to（排他上界）
// 把 end 当日整日排除。修正语义：end 为包含的自然日，to 取次日 0 点。
//
// 本用例种入一条 created_at = now 的日志，以 end = 今日 endOf('day') 查询，
// 断言该条被计入。若回归缺陷（to 被截断到今日 0 点），该条会被排除。
func TestAdminHeatmapEndOfDayIncludesToday(t *testing.T) {
	e := newTestEnv(t)
	rootC := e.seedAndLogin(t, "heateod", domain.RoleRoot)
	uid := e.userIDByName(t, "heateod")

	// 一条落在「现在」的日志（今日内任意时刻）。
	e.seedUsageLog(t, store.UsageLog{
		RequestID: "heat-eod-today", UserID: uid, APIKeyID: 1,
		ModelName: "glm-5", CreditsCharged: 100, CreatedAt: time.Now(),
	})

	// 前端口径：end_timestamp = 今日 23:59:59。start 取 7 天前。
	now := time.Now()
	start := now.AddDate(0, 0, -7).Unix()
	end := time.Date(now.Year(), now.Month(), now.Day(), 23, 59, 59, 0, time.Local).Unix()
	resp, env := e.do(t, rootC, "GET",
		fmt.Sprintf("/api/admin/stats/heatmap?start_timestamp=%d&end_timestamp=%d", start, end), nil)
	if resp.StatusCode != 200 {
		t.Fatalf("热力图应 200，实际 %d %v", resp.StatusCode, env)
	}
	cells := env["data"].(map[string]any)["cells"].([]any)
	var total int64
	for _, c := range cells {
		total += int64(c.(map[string]any)["requests"].(float64))
	}
	if total != 1 {
		t.Errorf("end=今日 endOf('day') 应包含今日数据（合计 1 条），实际 %d——疑似回归了 SpendDay 截断缺陷", total)
	}
}

// TestAdminHeatmapInvalidRange 非法时间范围（end 日早于 start 日）返回 400。
func TestAdminHeatmapInvalidRange(t *testing.T) {
	e := newTestEnv(t)
	rootC := e.seedAndLogin(t, "heatbad", domain.RoleRoot)
	// end 比 start 早 2 天 → 按包含日语义 to ≤ from → 400。
	now := time.Now()
	start := now.Unix()
	end := now.AddDate(0, 0, -2).Unix()
	resp, env := e.do(t, rootC, "GET",
		fmt.Sprintf("/api/admin/stats/heatmap?start_timestamp=%d&end_timestamp=%d", start, end), nil)
	if resp.StatusCode != 400 {
		t.Errorf("非法时间范围应 400，实际 %d %v", resp.StatusCode, env)
	}
}
