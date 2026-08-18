package api

import (
	"fmt"
	"testing"
	"time"

	"gorm.io/datatypes"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/pricing"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// moneyAdminReportsRate 是测试基线使用的默认兑换率（与系统默认设置一致：1 人民币 = 1e6 积分）。
// 用作定价函数 CreditsToDecimalString 的期望入参，避免在多个用例里硬编码同一字面量。
const moneyAdminReportsRate = int64(1_000_000)

// wantMoney 由积分推期望货币串（默认兑换率、6 位小数）。
func wantMoney(t *testing.T, credits int64) string {
	t.Helper()
	return pricing.CreditsToDecimalString(credits, moneyAdminReportsRate, 6)
}

// TestAdminUsageLogsListMoneyFields 校验 /api/admin/usage-logs 列表行旁置 _money 字段，
// 且取值等于 pricing.CreditsToDecimalString(credits, rate, 6)。
func TestAdminUsageLogsListMoneyFields(t *testing.T) {
	e := newTestEnv(t)
	rootC := e.seedAndLogin(t, "moneyulroot", domain.RoleRoot)
	e.seedAndLogin(t, "moneyuluser", domain.RoleUser)
	uid := e.userIDByName(t, "moneyuluser")

	const charged, cost, precharged = int64(2000), int64(700), int64(2500)
	e.seedUsageLog(t, store.UsageLog{
		RequestID: "money-ul-1", UserID: uid, APIKeyID: 1, ModelName: "glm-5",
		CreditsPrecharged: precharged, CreditsCharged: charged, CreditsCost: cost,
	})

	resp, env := e.do(t, rootC, "GET", "/api/admin/usage-logs", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("用量日志列表应 200，实际 %d %v", resp.StatusCode, env)
	}
	items := pageItems(t, env)
	if len(items) == 0 {
		t.Fatalf("期望至少 1 条日志，实际 %d", len(items))
	}
	row := items[0].(map[string]any)
	for _, tc := range []struct {
		moneyField string
		credits    int64
	}{
		{"credits_precharged_money", precharged},
		{"credits_charged_money", charged},
		{"credits_cost_money", cost},
	} {
		got, ok := row[tc.moneyField].(string)
		if !ok {
			t.Errorf("缺少 %s（或类型非字符串）：%v", tc.moneyField, row)
			continue
		}
		if want := wantMoney(t, tc.credits); got != want {
			t.Errorf("%s 期望 %q，实际 %q", tc.moneyField, want, got)
		}
	}
	// 原积分整数字段仍存在且不变（无破坏性变更）。
	if v, _ := row["credits_charged"].(float64); int64(v) != charged {
		t.Errorf("credits_charged 整数字段应保留为 %d，实际 %v", charged, row["credits_charged"])
	}
}

// TestAdminCostReportMoneyFields 校验运营视角费用报表（store.AggRow）旁置 _money 字段。
func TestAdminCostReportMoneyFields(t *testing.T) {
	e := newTestEnv(t)
	rootC := e.seedAndLogin(t, "moneycrtroot", domain.RoleRoot)
	e.seedAndLogin(t, "moneycrtuser", domain.RoleUser)
	uid := e.userIDByName(t, "moneycrtuser")

	const charged, cost = int64(3000), int64(1000)
	e.seedUsageLog(t, store.UsageLog{
		RequestID: "money-crt-1", UserID: uid, APIKeyID: 1, ModelName: "moneymodel",
		CreditsCharged: charged, CreditsCost: cost,
	})

	resp, env := e.do(t, rootC, "GET", "/api/admin/stats/cost-report?group_by=user", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("费用报表应 200，实际 %d %v", resp.StatusCode, env)
	}
	data, _ := env["data"].(map[string]any)
	rows, _ := data["rows"].([]any)
	if len(rows) == 0 {
		t.Fatalf("期望至少 1 行，实际 %d", len(rows))
	}
	row := rows[0].(map[string]any)
	for _, tc := range []struct {
		moneyField string
		credits    int64
	}{
		{"credits_charged_money", charged},
		{"credits_cost_money", cost},
		{"margin_money", charged - cost},
	} {
		got, ok := row[tc.moneyField].(string)
		if !ok {
			t.Errorf("缺少 %s：%v", tc.moneyField, row)
			continue
		}
		if want := wantMoney(t, tc.credits); got != want {
			t.Errorf("%s 期望 %q，实际 %q", tc.moneyField, want, got)
		}
	}
}

// TestAdminCostReportManagedMoneyFields 校验托管视角费用报表（deptAggRow）
// 旁置 credits_charged_money，且仍剥除 credits_cost/margin（口径不变）。
func TestAdminCostReportManagedMoneyFields(t *testing.T) {
	e := newTestEnv(t)
	token, integID, _ := seedManagedToken(t, e, "money-mgd")

	hosted := &store.User{
		Username: "money-mgd-user", Role: domain.RoleUser, Status: domain.UserEnabled,
		IntegrationID: &integID, MustChangePassword: false,
	}
	if err := e.db.Create(hosted).Error; err != nil {
		t.Fatalf("种入用户失败: %v", err)
	}
	const charged, cost = int64(1500), int64(500)
	log := &store.UsageLog{
		RequestID: "money-mgd-req", UserID: hosted.ID, ModelName: "moneymodel",
		CallCount: 1, CreditsCharged: charged, CreditsCost: cost,
		IntegrationID: integID, Status: domain.UsageSettled,
		PriceSnapshot: datatypes.JSON("{}"), CreatedAt: time.Now(),
	}
	if err := e.deps.UsageLogs.Create(t.Context(), log); err != nil {
		t.Fatalf("种入用量日志失败: %v", err)
	}

	resp, env := doWithToken(t, e, token, "GET", "/api/admin/stats/cost-report?group_by=user", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("托管费用报表应 200，实际 %d %v", resp.StatusCode, env)
	}
	data, _ := env["data"].(map[string]any)
	rows, _ := data["rows"].([]any)
	if len(rows) == 0 {
		t.Fatalf("期望至少 1 行，实际 %d", len(rows))
	}
	row := rows[0].(map[string]any)
	// managed 视图剥除 cost/margin，相应地也不应有其 _money 旁置。
	for _, banned := range []string{"credits_cost", "margin", "credits_cost_money", "margin_money"} {
		if _, ok := row[banned]; ok {
			t.Errorf("托管视图不应含 %s：%v", banned, row)
		}
	}
	got, ok := row["credits_charged_money"].(string)
	if !ok {
		t.Fatalf("缺少 credits_charged_money：%v", row)
	}
	if want := wantMoney(t, charged); got != want {
		t.Errorf("credits_charged_money 期望 %q，实际 %q", want, got)
	}
}

// TestAdminOpsSummaryMoneyFields 校验 ops-summary 各嵌套结构（本月/上月合计、Top 排行）
// 全部旁置 _money 字段，且取值等于 pricing.CreditsToDecimalString。
func TestAdminOpsSummaryMoneyFields(t *testing.T) {
	e := newTestEnv(t)
	adminC := e.seedAndLogin(t, "moneyopsadmin", domain.RoleAdmin)
	e.seedAndLogin(t, "moneyopsuser", domain.RoleUser)
	uid := e.userIDByName(t, "moneyopsuser")

	now := time.Now()
	thisMonthStart := monthStartAPI(now)

	const tCharged, tCost = int64(4000), int64(600)
	// 本月两条同模型日志，验证合计与 Top 行的 _money。
	for i, charged := range []int64{2500, 1500} {
		e.seedUsageLog(t, store.UsageLog{
			RequestID: fmt.Sprintf("money-ops-t%d", i), UserID: uid, APIKeyID: 1,
			ModelName: "opsmodel", ChannelID: 1,
			CreditsCharged: charged, CreditsCost: int64(300),
			CreatedAt: thisMonthStart.Add(2 * 24 * time.Hour),
		})
	}

	month := thisMonthStart.Format("2006-01")
	resp, env := e.do(t, adminC, "GET",
		fmt.Sprintf("/api/admin/stats/ops-summary?month=%s", month), nil)
	if resp.StatusCode != 200 {
		t.Fatalf("ops-summary 应 200，实际 %d %v", resp.StatusCode, env)
	}
	data, _ := env["data"].(map[string]any)

	// this_month 嵌套字段
	thisMonth, _ := data["this_month"].(map[string]any)
	if thisMonth == nil {
		t.Fatalf("this_month 应为对象: %v", data)
	}
	for _, tc := range []struct {
		moneyField string
		credits    int64
	}{
		{"credits_charged_money", tCharged},
		{"credits_cost_money", tCost},
		{"margin_money", tCharged - tCost},
	} {
		got, ok := thisMonth[tc.moneyField].(string)
		if !ok {
			t.Errorf("this_month 缺少 %s：%v", tc.moneyField, thisMonth)
			continue
		}
		if want := wantMoney(t, tc.credits); got != want {
			t.Errorf("this_month.%s 期望 %q，实际 %q", tc.moneyField, want, got)
		}
	}

	// prev_month 对象结构应同构（含 _money 字段，即便上月无消费也是空串）。
	prevMonth, _ := data["prev_month"].(map[string]any)
	if _, ok := prevMonth["credits_charged_money"].(string); !ok {
		t.Errorf("prev_month 应含 credits_charged_money 字符串：%v", prevMonth)
	}

	// top_models 排行行旁置 _money
	topModels, _ := data["top_models"].([]any)
	if len(topModels) == 0 {
		t.Fatalf("top_models 应至少 1 行")
	}
	tm, _ := topModels[0].(map[string]any)
	if got, _ := tm["credits_charged_money"].(string); got == "" {
		t.Errorf("top_models 行应含非空 credits_charged_money：%v", tm)
	} else if want := wantMoney(t, tCharged); got != want {
		t.Errorf("top_models[0].credits_charged_money 期望 %q，实际 %q", want, got)
	}
	if got, _ := tm["credits_cost_money"].(string); got == "" {
		t.Errorf("top_models 行应含非空 credits_cost_money：%v", tm)
	}

	// top_users 排行行旁置 _money
	topUsers, _ := data["top_users"].([]any)
	if len(topUsers) == 0 {
		t.Fatalf("top_users 应至少 1 行")
	}
	tu, _ := topUsers[0].(map[string]any)
	if got, _ := tu["credits_charged_money"].(string); got != wantMoney(t, tCharged) {
		t.Errorf("top_users[0].credits_charged_money 期望 %q，实际 %v",
			wantMoney(t, tCharged), tu["credits_charged_money"])
	}

	// 确认原积分字段仍在（无破坏性）。
	if v, _ := thisMonth["credits_charged"].(float64); int64(v) != tCharged {
		t.Errorf("this_month.credits_charged 整数字段应保留为 %d，实际 %v", tCharged, thisMonth["credits_charged"])
	}
}
