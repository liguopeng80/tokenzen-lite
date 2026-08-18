package store

import (
	"testing"
	"time"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
)

// opsTestEnv 经营分析测试的隔离环境：清空汇总、用量日志与积分流水相关表，
// 并种入若干用户（credit_ledger.user_id 受外键约束）。
func opsTestEnv(t *testing.T) *rollupEnv {
	t.Helper()
	env := newRollupEnv(t)
	if err := env.db.Exec(`TRUNCATE credit_ledger RESTART IDENTITY CASCADE`).Error; err != nil {
		t.Fatalf("清空积分流水失败: %v", err)
	}
	// users 表经 newStoreTestDB 清空，rollup 侧不再清；种入 id=1/2 供用量与流水引用。
	for _, name := range []string{"ops-user-1", "ops-user-2"} {
		u := &User{
			Username: name, PasswordHash: "x",
			Role: domain.RoleUser, Status: domain.UserEnabled,
		}
		if err := env.db.Create(u).Error; err != nil {
			t.Fatalf("种入用户 %s 失败: %v", name, err)
		}
	}
	return env
}

// seedOpsUsage 写一条已结算的用量日志（带成本）。
func seedOpsUsage(t *testing.T, env *rollupEnv, requestID string, userID int64,
	model string, charged, cost int64, at time.Time) {
	t.Helper()
	log := &UsageLog{
		RequestID: requestID, UserID: userID, APIKeyID: 1,
		ModelName: model, ChannelID: 1,
		PromptTokens: 100, CompletionTokens: 50,
		CreditsCharged: charged, CreditsCost: cost,
		Status: domain.UsageSettled, CreatedAt: at,
	}
	if err := env.db.Create(log).Error; err != nil {
		t.Fatalf("写入用量日志失败: %v", err)
	}
}

// seedOpsLedger 写一条积分流水。
func seedOpsLedger(t *testing.T, env *rollupEnv, userID int64,
	et domain.LedgerEntryType, amount domain.Credits, at time.Time) {
	t.Helper()
	e := &LedgerEntry{
		UserID: userID, EntryType: et, Amount: amount,
		RefType: "test", CreatedAt: at,
	}
	if err := env.db.Create(e).Error; err != nil {
		t.Fatalf("写入积分流水失败: %v", err)
	}
}

// TestOpsSummaryTotalsAndMoM 验证本月/上月总额、环比数学（含零分母→nil）与排行顺序。
// 上月的用量经 RollDay 进入聚合表，验证经营分析复用保留期安全的聚合路径。
func TestOpsSummaryTotalsAndMoM(t *testing.T) {
	env := opsTestEnv(t)

	now := time.Now()
	thisMonthStart := monthStart(now)
	prevMonthStart := thisMonthStart.AddDate(0, -1, 0)

	// 本月：模型 A 3000/1200（2 条），模型 B 1000/400（1 条）；用户 1 用 A 2 条，用户 2 用 B 1 条。
	seedOpsUsage(t, env, "ops-this-1", 1, "modelA", 2000, 800, thisMonthStart.Add(2*24*time.Hour))
	seedOpsUsage(t, env, "ops-this-2", 1, "modelA", 1000, 400, thisMonthStart.Add(3*24*time.Hour))
	seedOpsUsage(t, env, "ops-this-3", 2, "modelB", 1000, 400, thisMonthStart.Add(4*24*time.Hour))
	// 本月充值：grant 5000 + redeem 3000 = 8000。
	seedOpsLedger(t, env, 1, domain.LedgerGrant, 5000, thisMonthStart.Add(time.Hour))
	seedOpsLedger(t, env, 2, domain.LedgerRedeem, 3000, thisMonthStart.Add(2*time.Hour))
	// 退费不应计入充值。
	seedOpsLedger(t, env, 1, domain.LedgerRefund, 999, thisMonthStart.Add(3*time.Hour))

	// 上月：模型 A 1000/500（1 条）。充值 2000。
	prevDay := prevMonthStart.Add(5 * 24 * time.Hour)
	seedOpsUsage(t, env, "ops-prev-1", 1, "modelA", 1000, 500, prevDay)
	seedOpsLedger(t, env, 1, domain.LedgerGrant, 2000, prevMonthStart.Add(time.Hour))

	// 汇总上月那一天，使该月的数据走聚合表段，本月仍读原始日志——验证两段合并。
	if _, err := env.repo.RollDay(t.Context(), SpendDay(prevDay)); err != nil {
		t.Fatalf("汇总上月失败: %v", err)
	}

	summary, err := env.repo.OpsSummary(t.Context(), now, nil)
	if err != nil {
		t.Fatalf("OpsSummary 失败: %v", err)
	}

	if summary.Month != thisMonthStart.Format("2006-01") {
		t.Errorf("Month 期望 %s，实际 %s", thisMonthStart.Format("2006-01"), summary.Month)
	}

	// 本月合计：3000+1000=4000 扣费，1200+400=1600 成本，差额 2400，3 次请求，充值 8000。
	tm := summary.ThisMonth
	if tm.Requests != 3 {
		t.Errorf("本月请求期望 3，实际 %d", tm.Requests)
	}
	if tm.CreditsCharged != 4000 || tm.CreditsCost != 1600 || tm.Margin != 2400 {
		t.Errorf("本月合计不符（期望 charged=4000 cost=1600 margin=2400）: %+v", tm)
	}
	if tm.TopupCredits != 8000 {
		t.Errorf("本月充值期望 8000，实际 %d", tm.TopupCredits)
	}

	// 上月合计：1000 扣费，500 成本，1 次请求，充值 2000。
	pm := summary.PrevMonth
	if pm.Requests != 1 || pm.CreditsCharged != 1000 || pm.CreditsCost != 500 || pm.TopupCredits != 2000 {
		t.Errorf("上月合计不符: %+v", pm)
	}

	// 环比：扣费 (4000-1000)/1000*100 = 300；成本 (1600-500)/500*100 = 220；
	// 请求 (3-1)/1*100 = 200；充值 (8000-2000)/2000*100 = 300。
	assertPtr(t, "charged_pct", summary.MoM.ChargedPct, 300)
	assertPtr(t, "cost_pct", summary.MoM.CostPct, 220)
	assertPtr(t, "request_pct", summary.MoM.RequestPct, 200)
	assertPtr(t, "topup_pct", summary.MoM.TopupPct, 300)

	// Top 模型：modelA(3000) 在前，modelB(1000) 在后。
	if len(summary.TopModels) != 2 {
		t.Fatalf("Top 模型期望 2 行，实际 %d: %+v", len(summary.TopModels), summary.TopModels)
	}
	if summary.TopModels[0].GroupKey != "modelA" || summary.TopModels[0].CreditsCharged != 3000 {
		t.Errorf("Top 模型首位应为 modelA(3000): %+v", summary.TopModels[0])
	}
	if summary.TopModels[1].GroupKey != "modelB" || summary.TopModels[1].CreditsCharged != 1000 {
		t.Errorf("Top 模型次位应为 modelB(1000): %+v", summary.TopModels[1])
	}

	// Top 用户：用户 1(3000) 在前，用户 2(1000) 在后。
	if len(summary.TopUsers) != 2 {
		t.Fatalf("Top 用户期望 2 行，实际 %d", len(summary.TopUsers))
	}
	if summary.TopUsers[0].GroupID != 1 || summary.TopUsers[0].CreditsCharged != 3000 {
		t.Errorf("Top 用户首位应为用户 1(3000): %+v", summary.TopUsers[0])
	}
}

// TestOpsSummaryZeroPrevDenominator 上月分母为 0 时，环比字段应为 nil，避免除零。
func TestOpsSummaryZeroPrevDenominator(t *testing.T) {
	env := opsTestEnv(t)
	now := time.Now()
	thisMonthStart := monthStart(now)

	// 本月有消费，上月完全无消费/无充值。
	seedOpsUsage(t, env, "ops-zd-1", 1, "modelA", 1500, 600, thisMonthStart.Add(time.Hour))
	seedOpsLedger(t, env, 1, domain.LedgerGrant, 4000, thisMonthStart.Add(2*time.Hour))

	summary, err := env.repo.OpsSummary(t.Context(), now, nil)
	if err != nil {
		t.Fatalf("OpsSummary 失败: %v", err)
	}
	if summary.MoM.ChargedPct != nil {
		t.Errorf("上月扣费为 0 时 charged_pct 应为 nil，实际 %v", *summary.MoM.ChargedPct)
	}
	if summary.MoM.CostPct != nil {
		t.Errorf("上月成本为 0 时 cost_pct 应为 nil，实际 %v", *summary.MoM.CostPct)
	}
	if summary.MoM.RequestPct != nil {
		t.Errorf("上月请求为 0 时 request_pct 应为 nil，实际 %v", *summary.MoM.RequestPct)
	}
	if summary.MoM.TopupPct != nil {
		t.Errorf("上月充值为 0 时 topup_pct 应为 nil，实际 %v", *summary.MoM.TopupPct)
	}
	// 本月数据仍正确。
	if summary.ThisMonth.CreditsCharged != 1500 || summary.ThisMonth.TopupCredits != 4000 {
		t.Errorf("本月合计不符: %+v", summary.ThisMonth)
	}
}

// TestOpsSummaryIntegrationScope 接入方作用域：只统计本接入方的用量与充值。
func TestOpsSummaryIntegrationScope(t *testing.T) {
	env := opsTestEnv(t)
	now := time.Now()
	thisMonthStart := monthStart(now)
	iid := int64(7)

	// 接入方 7 的账号与外部账号各一条用量。
	seedOpsUsage(t, env, "ops-iid-in", 1, "modelA", 2000, 800, thisMonthStart.Add(time.Hour))
	seedOpsUsage(t, env, "ops-iid-out", 2, "modelA", 9000, 9000, thisMonthStart.Add(2*time.Hour))
	// 标记用户 1 属接入方 7、用户 2 属本机直管（integration_id=0）。
	if err := env.db.Model(&UsageLog{}).Where("request_id = ?", "ops-iid-in").
		Update("integration_id", iid).Error; err != nil {
		t.Fatalf("标记用量接入方失败: %v", err)
	}
	// 接入方 7 的充值。
	seedOpsLedger(t, env, 1, domain.LedgerGrant, 5000, thisMonthStart.Add(3*time.Hour))
	if err := env.db.Model(&LedgerEntry{}).Where("user_id = ? AND entry_type = ?", 1, domain.LedgerGrant).
		Update("integration_id", iid).Error; err != nil {
		t.Fatalf("标记流水接入方失败: %v", err)
	}

	summary, err := env.repo.OpsSummary(t.Context(), now, &iid)
	if err != nil {
		t.Fatalf("OpsSummary 失败: %v", err)
	}
	if summary.ThisMonth.CreditsCharged != 2000 {
		t.Errorf("接入方作用域内扣费期望 2000，实际 %d", summary.ThisMonth.CreditsCharged)
	}
	if summary.ThisMonth.TopupCredits != 5000 {
		t.Errorf("接入方作用域内充值期望 5000，实际 %d", summary.ThisMonth.TopupCredits)
	}
}

// monthStart 返回 t 所在自然月的起点（服务器时区）。
func monthStart(t time.Time) time.Time {
	local := t.In(time.Local)
	return time.Date(local.Year(), local.Month(), 1, 0, 0, 0, 0, time.Local)
}

func assertPtr(t *testing.T, label string, got *float64, want float64) {
	t.Helper()
	if got == nil {
		t.Errorf("%s 期望 %.2f，实际 nil", label, want)
		return
	}
	if diff := *got - want; diff < -0.01 || diff > 0.01 {
		t.Errorf("%s 期望 %.2f，实际 %.2f", label, want, *got)
	}
}
