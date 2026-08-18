package api

import (
	"fmt"
	"testing"
	"time"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// TestAdminOpsSummary 覆盖管理端经营分析端点：本月/上月/环比/Top 字段齐全、口径正确。
func TestAdminOpsSummary(t *testing.T) {
	e := newTestEnv(t)
	adminC := e.seedAndLogin(t, "opsadmin", domain.RoleAdmin)
	e.seedAndLogin(t, "opsuser", domain.RoleUser)
	uid := e.userIDByName(t, "opsuser")

	now := time.Now()
	thisMonthStart := monthStartAPI(now)
	prevMonthStart := thisMonthStart.AddDate(0, -1, 0)

	// 本月：2 条 modelA + 1 条 modelB。
	e.seedUsageLog(t, store.UsageLog{
		RequestID: "ops-api-t1", UserID: uid, APIKeyID: 1,
		ModelName: "modelA", ChannelID: 1,
		CreditsCharged: 2000, CreditsCost: 800, CreatedAt: thisMonthStart.Add(2 * 24 * time.Hour),
	})
	e.seedUsageLog(t, store.UsageLog{
		RequestID: "ops-api-t2", UserID: uid, APIKeyID: 1,
		ModelName: "modelA", ChannelID: 1,
		CreditsCharged: 1000, CreditsCost: 400, CreatedAt: thisMonthStart.Add(3 * 24 * time.Hour),
	})
	e.seedUsageLog(t, store.UsageLog{
		RequestID: "ops-api-t3", UserID: uid, APIKeyID: 1,
		ModelName: "modelB", ChannelID: 1,
		CreditsCharged: 1000, CreditsCost: 400, CreatedAt: thisMonthStart.Add(4 * 24 * time.Hour),
	})
	// 本月充值 grant。
	if err := e.db.Create(&store.LedgerEntry{
		UserID: uid, EntryType: domain.LedgerGrant, Amount: 5000,
		RefType: "test", CreatedAt: thisMonthStart.Add(time.Hour),
	}).Error; err != nil {
		t.Fatalf("种入流水失败: %v", err)
	}

	// 上月：1 条 modelA，充值 grant。
	e.seedUsageLog(t, store.UsageLog{
		RequestID: "ops-api-p1", UserID: uid, APIKeyID: 1,
		ModelName: "modelA", ChannelID: 1,
		CreditsCharged: 1000, CreditsCost: 500, CreatedAt: prevMonthStart.Add(5 * 24 * time.Hour),
	})
	if err := e.db.Create(&store.LedgerEntry{
		UserID: uid, EntryType: domain.LedgerGrant, Amount: 2000,
		RefType: "test", CreatedAt: prevMonthStart.Add(time.Hour),
	}).Error; err != nil {
		t.Fatalf("种入流水失败: %v", err)
	}

	month := thisMonthStart.Format("2006-01")
	resp, env := e.do(t, adminC, "GET",
		fmt.Sprintf("/api/admin/stats/ops-summary?month=%s", month), nil)
	if resp.StatusCode != 200 {
		t.Fatalf("ops-summary 应 200，实际 %d %v", resp.StatusCode, env)
	}
	data, ok := env["data"].(map[string]any)
	if !ok {
		t.Fatalf("响应应为信封: %v", env)
	}
	if got := data["month"]; got != month {
		t.Errorf("month 期望 %s，实际 %v", month, got)
	}
	thisMonth, ok := data["this_month"].(map[string]any)
	if !ok {
		t.Fatalf("this_month 应为对象: %v", data)
	}
	if charged := int64(thisMonth["credits_charged"].(float64)); charged != 4000 {
		t.Errorf("本月扣费期望 4000，实际 %d", charged)
	}
	if topup := int64(thisMonth["topup_credits"].(float64)); topup != 5000 {
		t.Errorf("本月充值期望 5000，实际 %d", topup)
	}
	prevMonth, ok := data["prev_month"].(map[string]any)
	if !ok {
		t.Fatalf("prev_month 应为对象: %v", data)
	}
	if charged := int64(prevMonth["credits_charged"].(float64)); charged != 1000 {
		t.Errorf("上月扣费期望 1000，实际 %d", charged)
	}
	mom, ok := data["mom"].(map[string]any)
	if !ok {
		t.Fatalf("mom 应为对象: %v", data)
	}
	if mom["charged_pct"] == nil {
		t.Error("charged_pct 不应为 nil（上月有消费）")
	}
	topModels, ok := data["top_models"].([]any)
	if !ok {
		t.Fatalf("top_models 应为数组: %v", data)
	}
	if len(topModels) != 2 {
		t.Errorf("top_models 期望 2 行，实际 %d", len(topModels))
	}
	topUsers, ok := data["top_users"].([]any)
	if !ok || len(topUsers) != 1 {
		t.Errorf("top_users 期望 1 行，实际 %v", data["top_users"])
	}
}

// TestAdminOpsSummaryBadRequest 非法月份格式返回 400。
func TestAdminOpsSummaryBadRequest(t *testing.T) {
	e := newTestEnv(t)
	adminC := e.seedAndLogin(t, "opsadmin2", domain.RoleAdmin)
	resp, _ := e.do(t, adminC, "GET", "/api/admin/stats/ops-summary?month=not-a-month", nil)
	if resp.StatusCode != 400 {
		t.Errorf("非法月份应 400，实际 %d", resp.StatusCode)
	}
}

// monthStartAPI 返回 t 所在自然月起点（服务器时区）。
func monthStartAPI(t time.Time) time.Time {
	local := t.In(time.Local)
	return time.Date(local.Year(), local.Month(), 1, 0, 0, 0, 0, time.Local)
}
