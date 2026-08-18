package maintenance

import (
	"context"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/liguopeng80/tokenzen-lite/server/internal/audit"
	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// fullScheduler 构造一个装配齐全的调度器，时钟固定在给定时刻。
func fullScheduler(db *gorm.DB, alerts *captureNotifier, now time.Time) *Scheduler {
	return &Scheduler{
		Settings:    store.NewSettingsRepo(db),
		Rollup:      store.NewRollupRepo(db),
		AuditLogs:   store.NewAuditLogRepo(db),
		Spend:       store.NewSpendRepo(db),
		Departments: store.NewDepartmentRepo(db),
		Users:       store.NewUserRepo(db),
		UsageLogs:   store.NewUsageLogRepo(db),
		Audit:       audit.NewRecorder(store.NewAuditLogRepo(db)),
		Alerts:      alerts,
		Now:         func() time.Time { return now },
	}
}

// truncateMaintenanceTables 清空本组用例涉及的全部表。
// newTestDB 只清了 users、settings、usage_logs，这里补齐其余几张。
func truncateMaintenanceTables(t *testing.T, db *gorm.DB) {
	t.Helper()
	if err := db.Exec(`TRUNCATE usage_daily_rollup, usage_rollup_state, audit_logs,
		daily_spend, daily_spend_by_key, departments, credit_ledger, usage_logs RESTART IDENTITY CASCADE`).Error; err != nil {
		t.Fatalf("清空维护相关表失败: %v", err)
	}
}

// seedUsageLog 种入一条已结算的用量日志，created_at 指定到具体时刻。
func seedUsageLog(t *testing.T, db *gorm.DB, requestID string, userID, deptID int64,
	credits domain.Credits, at time.Time) {

	t.Helper()
	log := &store.UsageLog{
		RequestID: requestID, UserID: userID, DepartmentID: deptID,
		ModelName: "maint-model", ChannelID: 1, PromptTokens: 10, CompletionTokens: 20,
		CreditsCharged: credits, CreditsCost: credits / 2, Status: domain.UsageSettled,
	}
	if err := db.Create(log).Error; err != nil {
		t.Fatalf("种入用量日志失败: %v", err)
	}
	if err := db.Model(&store.UsageLog{}).Where("id = ?", log.ID).
		Update("created_at", at).Error; err != nil {
		t.Fatalf("回填用量日志时间失败: %v", err)
	}
}

// 按日汇总只处理已结束的自然日：当天的日志还可能被孤儿预扣补偿改写状态，
// 提前汇总会把中间态定格进报表。
func TestRollupUsageOnlyCoversFinishedDays(t *testing.T) {
	db := newTestDB(t)
	truncateMaintenanceTables(t, db)
	ctx := context.Background()
	now := time.Date(2026, 8, 7, 10, 0, 0, 0, time.Local)

	seedUsageLog(t, db, "roll-yesterday", 1, 0, 1000, now.AddDate(0, 0, -1))
	seedUsageLog(t, db, "roll-today", 1, 0, 2000, now)

	setSetting(t, db, "usage_rollup_enabled", "true")
	fullScheduler(db, &captureNotifier{}, now).rollupUsage(ctx)

	rows := aggregatedCredits(t, db)
	if rows[1000] != 1 {
		t.Errorf("昨日的用量应已汇总，汇总结果：%v", rows)
	}
	if rows[2000] != 0 {
		t.Errorf("当天的用量不应汇总，汇总结果：%v", rows)
	}
}

// 汇总开关关闭时不动任何数据。
func TestRollupUsageSkippedWhenDisabled(t *testing.T) {
	db := newTestDB(t)
	truncateMaintenanceTables(t, db)
	now := time.Date(2026, 8, 7, 10, 0, 0, 0, time.Local)
	seedUsageLog(t, db, "roll-off", 1, 0, 1000, now.AddDate(0, 0, -1))

	setSetting(t, db, "usage_rollup_enabled", "false")
	fullScheduler(db, &captureNotifier{}, now).rollupUsage(context.Background())

	if rows := aggregatedCredits(t, db); len(rows) != 0 {
		t.Errorf("关闭汇总时不应产生汇总行，实际 %v", rows)
	}
}

// aggregatedCredits 返回 usage_daily 中各金额出现的次数，用于判断哪些日志已被汇总。
func aggregatedCredits(t *testing.T, db *gorm.DB) map[int64]int {
	t.Helper()
	type row struct {
		CreditsCharged int64
	}
	var rows []row
	if err := db.Table("usage_daily_rollup").Select("credits_charged").Scan(&rows).Error; err != nil {
		t.Fatalf("查询汇总表失败: %v", err)
	}
	out := map[int64]int{}
	for _, r := range rows {
		out[r.CreditsCharged]++
	}
	return out
}

// 用量日志的保留期清理只删已完成汇总的日期：先删后汇总会让那段时间的报表永久消失。
func TestPurgeUsageLogsRequiresRollupFirst(t *testing.T) {
	db := newTestDB(t)
	truncateMaintenanceTables(t, db)
	ctx := context.Background()
	now := time.Date(2026, 8, 7, 10, 0, 0, 0, time.Local)
	old := now.AddDate(0, 0, -100)

	seedUsageLog(t, db, "purge-old", 1, 0, 1000, old)
	setSetting(t, db, "usage_log_retention_days", "90")

	// 尚未汇总：清理被仓储拒绝，日志原样保留。
	s := fullScheduler(db, &captureNotifier{}, now)
	s.purgeUsageLogs(ctx)
	if n := usageLogCount(t, db); n != 1 {
		t.Fatalf("未完成汇总时不应删除日志，剩余 %d 条", n)
	}

	// 完成汇总后再清理，日志被删除，汇总数据仍在。
	setSetting(t, db, "usage_rollup_enabled", "true")
	s.rollupUsage(ctx)
	s.purgeUsageLogs(ctx)
	if n := usageLogCount(t, db); n != 0 {
		t.Fatalf("已汇总的过期日志应被清理，剩余 %d 条", n)
	}
	if rows := aggregatedCredits(t, db); rows[1000] != 1 {
		t.Errorf("清理原始日志不应影响汇总数据，实际 %v", rows)
	}
}

// 保留期为 0 表示不清理。
func TestPurgeUsageLogsDisabledByZeroRetention(t *testing.T) {
	db := newTestDB(t)
	truncateMaintenanceTables(t, db)
	now := time.Date(2026, 8, 7, 10, 0, 0, 0, time.Local)
	seedUsageLog(t, db, "purge-keep", 1, 0, 1000, now.AddDate(0, 0, -400))

	setSetting(t, db, "usage_log_retention_days", "0")
	setSetting(t, db, "usage_rollup_enabled", "true")
	s := fullScheduler(db, &captureNotifier{}, now)
	s.rollupUsage(context.Background())
	s.purgeUsageLogs(context.Background())

	if n := usageLogCount(t, db); n != 1 {
		t.Errorf("保留期为 0 时不应清理，剩余 %d 条", n)
	}
}

func usageLogCount(t *testing.T, db *gorm.DB) int64 {
	t.Helper()
	var n int64
	if err := db.Model(&store.UsageLog{}).Count(&n).Error; err != nil {
		t.Fatalf("统计用量日志失败: %v", err)
	}
	return n
}

// 审计记录的清理动作本身要留痕，否则「审计里查不到」与「记录被清理」无法区分。
func TestPurgeAuditLogsRecordsItsOwnAction(t *testing.T) {
	db := newTestDB(t)
	truncateMaintenanceTables(t, db)
	ctx := context.Background()
	now := time.Date(2026, 8, 7, 10, 0, 0, 0, time.Local)

	// 一条超出保留期、一条在期内。数据库触发器只允许删除写入满 30 天的记录，
	// 因此过期样本要造在 30 天以前。
	seedAuditLog(t, db, domain.AuditUserCreate, now.AddDate(0, 0, -200))
	seedAuditLog(t, db, domain.AuditUserCreate, now.AddDate(0, 0, -10))

	setSetting(t, db, "audit_log_retention_days", "180")
	fullScheduler(db, &captureNotifier{}, now).purgeAuditLogs(ctx)

	var remaining []store.AuditLog
	if err := db.Order("id").Find(&remaining).Error; err != nil {
		t.Fatalf("查询审计记录失败: %v", err)
	}
	// 期内的一条 + 清理动作本身的一条。
	if len(remaining) != 2 {
		t.Fatalf("应剩下期内记录与清理留痕共 2 条，实际 %d 条", len(remaining))
	}
	if remaining[1].Action != domain.AuditPurge {
		t.Errorf("清理动作本身应留痕，实际 %s", remaining[1].Action)
	}
	if remaining[1].TargetType != domain.AuditTargetAudit {
		t.Errorf("清理留痕的对象类型应为审计记录，实际 %s", remaining[1].TargetType)
	}
}

// 没有过期记录时不产生噪声留痕。
func TestPurgeAuditLogsSilentWhenNothingExpired(t *testing.T) {
	db := newTestDB(t)
	truncateMaintenanceTables(t, db)
	now := time.Date(2026, 8, 7, 10, 0, 0, 0, time.Local)
	seedAuditLog(t, db, domain.AuditUserCreate, now.AddDate(0, 0, -10))

	setSetting(t, db, "audit_log_retention_days", "180")
	fullScheduler(db, &captureNotifier{}, now).purgeAuditLogs(context.Background())

	var n int64
	if err := db.Model(&store.AuditLog{}).Where("action = ?", domain.AuditPurge).
		Count(&n).Error; err != nil {
		t.Fatalf("统计清理留痕失败: %v", err)
	}
	if n != 0 {
		t.Errorf("无过期记录时不应留痕，实际 %d 条", n)
	}
}

// seedAuditLog 种入一条指定时间的审计记录。
// 时间在插入时一次写定：审计表由触发器强制只追加，插入后改不了。
func seedAuditLog(t *testing.T, db *gorm.DB, action domain.AuditAction, at time.Time) {
	t.Helper()
	log := &store.AuditLog{
		Action: action, TargetType: domain.AuditTargetUser, TargetID: 1,
		Result: domain.AuditSuccess, OperatorName: "tester", CreatedAt: at,
	}
	if err := db.Create(log).Error; err != nil {
		t.Fatalf("种入审计记录失败: %v", err)
	}
}

// seedDailySpend 直接写 daily_spend 计数（测试播种；生产写入已收敛进 billing.applyTx）。
func seedDailySpend(t *testing.T, db *gorm.DB, userID int64, day time.Time, credits domain.Credits) {
	t.Helper()
	if credits == 0 {
		return
	}
	if err := db.Exec(`
		INSERT INTO daily_spend (user_id, day, credits, updated_at)
		VALUES (?, (? AT TIME ZONE ?)::date, ?, now())
		ON CONFLICT (user_id, day) DO UPDATE
		SET credits = daily_spend.credits + EXCLUDED.credits, updated_at = now()`,
		userID, day, store.LocalZoneName(), credits).Error; err != nil {
		t.Fatalf("写入花费计数失败: %v", err)
	}
}

// 每日花费计数按固定保留期清理：它只服务于当日上限判定，不是账务事实源。
func TestPurgeSpendCountersDropsOldBuckets(t *testing.T) {
	db := newTestDB(t)
	truncateMaintenanceTables(t, db)
	now := time.Date(2026, 8, 7, 10, 0, 0, 0, time.Local)
	// daily_spend 的 user_id 有外键，先种一个用户。
	seedUser(t, db, "spendowner", 1000, 0, domain.UserEnabled)
	spendUserID := userIDByName(t, db, "spendowner")

	for _, offset := range []int{-1, -spendCounterRetentionDays - 1} {
		day := store.SpendDay(now.AddDate(0, 0, offset))
		seedDailySpend(t, db, spendUserID, day, 100)
	}

	fullScheduler(db, &captureNotifier{}, now).purgeSpendCounters(context.Background())

	var days []time.Time
	if err := db.Model(&store.DailySpend{}).Order("day").Pluck("day", &days).Error; err != nil {
		t.Fatalf("查询花费计数失败: %v", err)
	}
	if len(days) != 1 {
		t.Fatalf("超出保留期的分桶应被清理，剩余 %d 个", len(days))
	}
	// DATE 列读回为「日期 00:00 UTC」；两侧都过 SpendDay 归一到服务器本地零点
	// 再比较，既不依赖 PG 会话时区，又能在「写错日期」时仍然失败。
	if !store.SpendDay(days[0]).Equal(store.SpendDay(now.AddDate(0, 0, -1))) {
		t.Errorf("保留的应是昨日分桶，实际 %v", days[0])
	}
}

// 部门当月消费超出预算时告警；预算只作对比目标，不拦截调用。
func TestCheckDepartmentBudgetsAlertsOnOverrun(t *testing.T) {
	db := newTestDB(t)
	truncateMaintenanceTables(t, db)
	ctx := context.Background()
	// 取当月中旬，保证种入的日志落在「本月」窗口内。
	now := time.Date(2026, 8, 15, 10, 0, 0, 0, time.Local)

	overID := seedDepartment(t, db, "超预算部", 1000)
	underID := seedDepartment(t, db, "未超预算部", 1_000_000)
	seedUsageLog(t, db, "budget-over", 1, overID, 5000, now.AddDate(0, 0, -1))
	seedUsageLog(t, db, "budget-under", 2, underID, 5000, now.AddDate(0, 0, -1))

	setSetting(t, db, "usage_rollup_enabled", "true")
	alerts := &captureNotifier{}
	s := fullScheduler(db, alerts, now)
	s.rollupUsage(ctx)
	s.checkDepartmentBudgets(ctx)

	events := alerts.eventsOfType(domain.AlertDepartmentOverBudget)
	if len(events) != 1 {
		t.Fatalf("只有超预算的部门应触发告警，实际 %d 条：%v", len(events), alerts.events)
	}
	if id, _ := events[0].Payload["department_id"].(int64); id != overID {
		t.Errorf("告警应指向超预算的部门，实际 %v", events[0].Payload)
	}
}

// 未设预算（0）的部门不参与判定。
func TestCheckDepartmentBudgetsIgnoresUnbudgeted(t *testing.T) {
	db := newTestDB(t)
	truncateMaintenanceTables(t, db)
	ctx := context.Background()
	now := time.Date(2026, 8, 15, 10, 0, 0, 0, time.Local)

	deptID := seedDepartment(t, db, "无预算部", 0)
	seedUsageLog(t, db, "budget-none", 1, deptID, 999_999, now.AddDate(0, 0, -1))

	setSetting(t, db, "usage_rollup_enabled", "true")
	alerts := &captureNotifier{}
	s := fullScheduler(db, alerts, now)
	s.rollupUsage(ctx)
	s.checkDepartmentBudgets(ctx)

	if events := alerts.eventsOfType(domain.AlertDepartmentOverBudget); len(events) != 0 {
		t.Errorf("未设预算的部门不应告警，实际 %v", events)
	}
}

func seedDepartment(t *testing.T, db *gorm.DB, name string, budget domain.Credits) int64 {
	t.Helper()
	d := &store.Department{Name: name, Status: domain.DepartmentEnabled,
		MonthlyBudgetCredits: budget}
	if err := db.Create(d).Error; err != nil {
		t.Fatalf("种入部门 %s 失败: %v", name, err)
	}
	return d.ID
}

// 整轮编排把各项串起来执行；某一项没有数据不影响其余项。
func TestRunOnceExecutesEveryAction(t *testing.T) {
	db := newTestDB(t)
	truncateMaintenanceTables(t, db)
	now := time.Date(2026, 8, 15, 10, 0, 0, 0, time.Local)

	deptID := seedDepartment(t, db, "整轮部", 1000)
	seedUser(t, db, "runonceuser", 10, 500, domain.UserEnabled)
	runOnceUserID := userIDByName(t, db, "runonceuser")
	seedUsageLog(t, db, "runonce-1", runOnceUserID, deptID, 5000, now.AddDate(0, 0, -1))
	seedAuditLog(t, db, domain.AuditUserCreate, now.AddDate(0, 0, -200))
	seedDailySpend(t, db, runOnceUserID,
		store.SpendDay(now.AddDate(0, 0, -spendCounterRetentionDays-1)), 100)

	setSetting(t, db, "usage_rollup_enabled", "true")
	setSetting(t, db, "audit_log_retention_days", "180")
	setSetting(t, db, "low_balance_threshold_credits", "100")

	alerts := &captureNotifier{}
	fullScheduler(db, alerts, now).RunOnce(context.Background())

	if rows := aggregatedCredits(t, db); rows[5000] != 1 {
		t.Errorf("整轮应完成按日汇总，实际 %v", rows)
	}
	var purged int64
	if err := db.Model(&store.AuditLog{}).Where("action = ?", domain.AuditPurge).
		Count(&purged).Error; err != nil {
		t.Fatalf("统计清理留痕失败: %v", err)
	}
	if purged != 1 {
		t.Errorf("整轮应完成审计保留期清理，留痕 %d 条", purged)
	}
	var spendRows int64
	if err := db.Model(&store.DailySpend{}).Count(&spendRows).Error; err != nil {
		t.Fatalf("统计花费计数失败: %v", err)
	}
	if spendRows != 0 {
		t.Errorf("整轮应完成花费计数清理，剩余 %d 个分桶", spendRows)
	}
	if len(alerts.eventsOfType(domain.AlertDepartmentOverBudget)) != 1 {
		t.Errorf("整轮应完成部门超预算检查，实际告警 %v", alerts.events)
	}
	if len(alerts.eventsOfType(domain.AlertUserLowBalance)) != 1 {
		t.Errorf("整轮应完成低余额检查，实际告警 %v", alerts.events)
	}
}

// 组件缺失时对应的动作跳过，不发生空指针。生产上这对应「未配置告警通道」等情形。
func TestRunOnceToleratesMissingComponents(t *testing.T) {
	db := newTestDB(t)
	truncateMaintenanceTables(t, db)
	s := &Scheduler{
		Settings: store.NewSettingsRepo(db),
		Now:      func() time.Time { return time.Date(2026, 8, 15, 10, 0, 0, 0, time.Local) },
	}
	s.RunOnce(context.Background())
}

// userIDByName 取用户名对应的用户 ID。
func userIDByName(t *testing.T, db *gorm.DB, username string) int64 {
	t.Helper()
	var u store.User
	if err := db.Where("username = ?", username).First(&u).Error; err != nil {
		t.Fatalf("查询用户 %s 失败: %v", username, err)
	}
	return u.ID
}
