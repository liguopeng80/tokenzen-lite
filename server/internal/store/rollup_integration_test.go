package store

import (
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
)

// rollupEnv 是按日汇总测试的隔离环境：每个用例独立清空相关表。
type rollupEnv struct {
	db   *gorm.DB
	repo *RollupRepo
}

func newRollupEnv(t *testing.T) *rollupEnv {
	t.Helper()
	db := newStoreTestDB(t)
	if err := db.Exec(`TRUNCATE usage_logs, usage_daily_rollup, usage_rollup_state,
		departments, projects RESTART IDENTITY CASCADE`).Error; err != nil {
		t.Fatalf("清空汇总相关表失败: %v", err)
	}
	return &rollupEnv{db: db, repo: NewRollupRepo(db)}
}

// seedRollupUsage 写一条已结算的用量日志。
func seedRollupUsage(t *testing.T, env *rollupEnv, requestID string, userID, deptID int64,
	model string, charged, cost int64, at time.Time) {

	t.Helper()
	log := &UsageLog{
		RequestID: requestID, UserID: userID, DepartmentID: deptID,
		APIKeyID: 1, ModelName: model, ChannelID: 1,
		PromptTokens: 100, CompletionTokens: 50,
		CreditsCharged: charged, CreditsCost: cost,
		Status: domain.UsageSettled, CreatedAt: at,
	}
	if err := env.db.Create(log).Error; err != nil {
		t.Fatalf("写入用量日志失败: %v", err)
	}
}

// 汇总同一日重复执行得到相同结果，不会把数字翻倍。
func TestRollDayIsIdempotent(t *testing.T) {
	env := newRollupEnv(t)
	day := SpendDay(time.Now().AddDate(0, 0, -2))
	seedRollupUsage(t, env, "roll-1", 1, 7, "glm-5", 1000, 400, day.Add(3*time.Hour))
	seedRollupUsage(t, env, "roll-2", 1, 7, "glm-5", 2000, 800, day.Add(5*time.Hour))

	for i := 0; i < 2; i++ {
		if _, err := env.repo.RollDay(t.Context(), day); err != nil {
			t.Fatalf("第 %d 次汇总失败: %v", i+1, err)
		}
	}
	var rows []UsageDailyRollup
	if err := env.db.Where("day = ?", day).Find(&rows).Error; err != nil {
		t.Fatalf("查询聚合失败: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("同一维度应只有一行聚合，实际 %d 行", len(rows))
	}
	if rows[0].Requests != 2 || rows[0].CreditsCharged != 3000 || rows[0].CreditsCost != 1200 {
		t.Errorf("重复汇总不应累加：%+v", rows[0])
	}
}

// 未结算的请求不计入报表，与既有报表口径一致。
func TestRollDayOnlyCountsSettled(t *testing.T) {
	env := newRollupEnv(t)
	day := SpendDay(time.Now().AddDate(0, 0, -2))
	seedRollupUsage(t, env, "settled-1", 1, 0, "glm-5", 1000, 400, day.Add(time.Hour))

	failed := &UsageLog{
		RequestID: "failed-1", UserID: 1, ModelName: "glm-5",
		CreditsCharged: 999, Status: domain.UsageFailed, CreatedAt: day.Add(2 * time.Hour),
	}
	if err := env.db.Create(failed).Error; err != nil {
		t.Fatalf("写入失败日志失败: %v", err)
	}
	if _, err := env.repo.RollDay(t.Context(), day); err != nil {
		t.Fatalf("汇总失败: %v", err)
	}
	var total int64
	if err := env.db.Model(&UsageDailyRollup{}).Where("day = ?", day).
		Select("COALESCE(SUM(credits_charged),0)").Scan(&total).Error; err != nil {
		t.Fatalf("查询聚合失败: %v", err)
	}
	if total != 1000 {
		t.Errorf("只应汇总已结算记录，期望 1000，实际 %d", total)
	}
}

// 报表结果不随「是否已汇总」改变：聚合表段与原始日志段合并后总额一致。
func TestAggregateMergesRollupAndRawSegments(t *testing.T) {
	env := newRollupEnv(t)
	now := time.Now()
	older := SpendDay(now.AddDate(0, 0, -3))
	today := SpendDay(now)
	seedRollupUsage(t, env, "seg-old", 1, 7, "glm-5", 1000, 400, older.Add(time.Hour))
	seedRollupUsage(t, env, "seg-today", 1, 7, "glm-5", 2000, 900, today.Add(time.Hour))

	filter := AggFilter{From: older, To: today.AddDate(0, 0, 1)}
	before, err := env.repo.Aggregate(t.Context(), AggByUser, filter)
	if err != nil {
		t.Fatalf("汇总前查询失败: %v", err)
	}
	if len(before) != 1 || before[0].CreditsCharged != 3000 || before[0].Requests != 2 {
		t.Fatalf("汇总前应直接聚合原始日志：%+v", before)
	}

	// 汇总较早的一天后，该日改由聚合表提供，当日仍读原始日志。
	if _, err := env.repo.RollDay(t.Context(), older); err != nil {
		t.Fatalf("汇总失败: %v", err)
	}
	after, err := env.repo.Aggregate(t.Context(), AggByUser, filter)
	if err != nil {
		t.Fatalf("汇总后查询失败: %v", err)
	}
	if len(after) != 1 || after[0].CreditsCharged != 3000 || after[0].Requests != 2 {
		t.Fatalf("汇总前后报表结果应一致：%+v", after)
	}
	if after[0].Margin != 3000-1300 {
		t.Errorf("利润应为扣费减成本，实际 %d", after[0].Margin)
	}
}

// 按部门聚合时，未分配部门单独成行而不是被丢弃。
func TestAggregateByDepartmentKeepsUnassigned(t *testing.T) {
	env := newRollupEnv(t)
	day := SpendDay(time.Now())
	seedRollupUsage(t, env, "dept-assigned", 1, 7, "glm-5", 1000, 0, day.Add(time.Hour))
	seedRollupUsage(t, env, "dept-none", 2, 0, "glm-5", 500, 0, day.Add(2*time.Hour))

	rows, err := env.repo.Aggregate(t.Context(), AggByDepartment,
		AggFilter{From: day, To: day.AddDate(0, 0, 1)})
	if err != nil {
		t.Fatalf("按部门聚合失败: %v", err)
	}
	byID := map[int64]AggRow{}
	for _, r := range rows {
		byID[r.GroupID] = r
	}
	unassigned, ok := byID[0]
	if !ok {
		t.Fatalf("未分配部门的消费应单独成行：%+v", rows)
	}
	if unassigned.GroupKey != "未分配" {
		t.Errorf("未分配部门的显示名应为「未分配」，实际 %q", unassigned.GroupKey)
	}
	if unassigned.CreditsCharged != 500 {
		t.Errorf("未分配部门扣费期望 500，实际 %d", unassigned.CreditsCharged)
	}
	if byID[7].CreditsCharged != 1000 {
		t.Errorf("已删除部门的消费仍应保留在报表中：%+v", byID[7])
	}
	if byID[7].GroupKey == "" {
		t.Error("已删除部门应有可读的显示名")
	}
}

// 未完成汇总的日期不允许清理原始日志：先删后汇总会让报表数据永久消失。
func TestPurgeUsageLogsRequiresRollupFirst(t *testing.T) {
	env := newRollupEnv(t)
	day := SpendDay(time.Now().AddDate(0, 0, -5))
	seedRollupUsage(t, env, "purge-1", 1, 0, "glm-5", 1000, 0, day.Add(time.Hour))

	cutoff := time.Now().AddDate(0, 0, -1)
	if _, err := env.repo.PurgeUsageLogsBefore(t.Context(), cutoff); err == nil {
		t.Fatal("未汇总时应拒绝清理")
	}
	var remaining int64
	if err := env.db.Model(&UsageLog{}).Count(&remaining).Error; err != nil {
		t.Fatalf("统计日志失败: %v", err)
	}
	if remaining != 1 {
		t.Fatalf("拒绝清理时不得删除任何记录，实际剩余 %d 条", remaining)
	}

	// 补齐从最早日志到清理点之间每一天的汇总后即可清理。
	for d := day; d.Before(SpendDay(cutoff)); d = d.AddDate(0, 0, 1) {
		if _, err := env.repo.RollDay(t.Context(), d); err != nil {
			t.Fatalf("汇总 %s 失败: %v", d.Format("2006-01-02"), err)
		}
	}
	deleted, err := env.repo.PurgeUsageLogsBefore(t.Context(), cutoff)
	if err != nil {
		t.Fatalf("汇总完成后清理应成功: %v", err)
	}
	if deleted != 1 {
		t.Errorf("应清理 1 条原始日志，实际 %d 条", deleted)
	}
	// 明细清掉了，报表数据仍在。
	rows, err := env.repo.Aggregate(t.Context(), AggByUser,
		AggFilter{From: day, To: time.Now().AddDate(0, 0, 1)})
	if err != nil {
		t.Fatalf("清理后报表查询失败: %v", err)
	}
	if len(rows) != 1 || rows[0].CreditsCharged != 1000 {
		t.Errorf("清理原始日志后报表数据应保留：%+v", rows)
	}
}

// PendingDays 只返回尚未汇总的日期，已汇总的不重复处理。
func TestPendingDaysSkipsCompleted(t *testing.T) {
	env := newRollupEnv(t)
	day := SpendDay(time.Now().AddDate(0, 0, -3))
	seedRollupUsage(t, env, "pending-1", 1, 0, "glm-5", 100, 0, day.Add(time.Hour))

	upTo := SpendDay(time.Now().AddDate(0, 0, -1))
	first, err := env.repo.PendingDays(t.Context(), upTo, 30)
	if err != nil {
		t.Fatalf("查询待汇总日期失败: %v", err)
	}
	if len(first) != 3 {
		t.Fatalf("应有 3 天待汇总（-3、-2、-1），实际 %d 天", len(first))
	}
	for _, d := range first {
		if _, err := env.repo.RollDay(t.Context(), d); err != nil {
			t.Fatalf("汇总失败: %v", err)
		}
	}
	second, err := env.repo.PendingDays(t.Context(), upTo, 30)
	if err != nil {
		t.Fatalf("再次查询待汇总日期失败: %v", err)
	}
	if len(second) != 0 {
		t.Errorf("已汇总的日期不应重复出现：%v", second)
	}
}

// 无任何用量日志时不产生待汇总日期，避免新装系统空转扫描。
func TestPendingDaysEmptyOnFreshInstall(t *testing.T) {
	env := newRollupEnv(t)
	days, err := env.repo.PendingDays(t.Context(), time.Now(), 30)
	if err != nil {
		t.Fatalf("查询失败: %v", err)
	}
	if len(days) != 0 {
		t.Errorf("无用量日志时不应有待汇总日期：%v", days)
	}
}

// seedCacheUsage 写一条已结算的用量日志，带缓存 token。
func seedCacheUsage(t *testing.T, env *rollupEnv, requestID string, userID int64,
	prompt, completion, cacheRead, cacheWrite int64, at time.Time) {
	t.Helper()
	log := &UsageLog{
		RequestID: requestID, UserID: userID, APIKeyID: 1,
		ModelName: "glm-5", ChannelID: 1,
		PromptTokens: prompt, CompletionTokens: completion,
		CacheReadTokens: cacheRead, CacheWriteTokens: cacheWrite,
		CreditsCharged: 100, CreditsCost: 40,
		Status: domain.UsageSettled, CreatedAt: at,
	}
	if err := env.db.Create(log).Error; err != nil {
		t.Fatalf("写入用量日志失败: %v", err)
	}
}

// TestAggregateCacheTokensAcrossRollupBoundary 验证 Aggregate 携带缓存 token，
// 且在汇总边界两侧（聚合表段 + 原始日志段）合并后总额正确，与直接聚合原始日志一致。
// 这是缓存分析报表的保留期安全保证：原始日志被清理后聚合表段仍提供缓存口径。
func TestAggregateCacheTokensAcrossRollupBoundary(t *testing.T) {
	env := newRollupEnv(t)
	older := SpendDay(time.Now().AddDate(0, 0, -3))
	today := SpendDay(time.Now())
	// 较早日（将被 RollDay）：两条，缓存读 300 + 100。
	seedCacheUsage(t, env, "cache-old-a", 1, 100, 50, 300, 80, older.Add(time.Hour))
	seedCacheUsage(t, env, "cache-old-b", 1, 100, 50, 100, 20, older.Add(2*time.Hour))
	// 当日（仍读原始日志）：缓存读 200。
	seedCacheUsage(t, env, "cache-today", 1, 100, 50, 200, 40, today.Add(time.Hour))

	filter := AggFilter{From: older, To: today.AddDate(0, 0, 1)}
	// 汇总前：全部读原始日志，作为基线。
	before, err := env.repo.Aggregate(t.Context(), AggByUser, filter)
	if err != nil {
		t.Fatalf("汇总前聚合失败: %v", err)
	}
	if len(before) != 1 {
		t.Fatalf("基线应 1 行，实际 %d: %+v", len(before), before)
	}
	if before[0].CacheReadTokens != 600 || before[0].CacheWriteTokens != 140 {
		t.Errorf("基线缓存 token 不符（期望 read=600 write=140）: %+v", before[0])
	}

	// 汇总较早的一天：该日改由聚合表提供，当日仍读原始日志，两段合并。
	if _, err := env.repo.RollDay(t.Context(), older); err != nil {
		t.Fatalf("汇总失败: %v", err)
	}
	after, err := env.repo.Aggregate(t.Context(), AggByUser, filter)
	if err != nil {
		t.Fatalf("汇总后聚合失败: %v", err)
	}
	if len(after) != 1 {
		t.Fatalf("汇总后应 1 行，实际 %d: %+v", len(after), after)
	}
	if after[0].CacheReadTokens != 600 || after[0].CacheWriteTokens != 140 {
		t.Errorf("汇总边界两侧缓存 token 应与基线一致（期望 read=600 write=140）: %+v", after[0])
	}
	// 既有字段未受影响：请求数、token、扣费仍正确。
	if after[0].Requests != 3 || after[0].PromptTokens != 300 || after[0].CompletionTokens != 150 {
		t.Errorf("汇总后既有字段不符: %+v", after[0])
	}
}

// seedKeyUsage 写一条已结算的用量日志，可指定 api_key_id（rollup 维度测试专用）。
func seedKeyUsage(t *testing.T, env *rollupEnv, requestID string, userID, keyID int64, charged int64, at time.Time) {
	t.Helper()
	log := &UsageLog{
		RequestID: requestID, UserID: userID, APIKeyID: keyID,
		ModelName: "glm-5", ChannelID: 1,
		PromptTokens: 100, CompletionTokens: 50,
		CreditsCharged: charged, CreditsCost: charged / 2,
		Status: domain.UsageSettled, CreatedAt: at,
	}
	if err := env.db.Create(log).Error; err != nil {
		t.Fatalf("写入用量日志失败: %v", err)
	}
}

// 按密钥聚合：不同密钥各自成行；api_key_id=0 的历史汇总标记为「按密钥不可拆」，
// 非零密钥无对应 api_keys 记录时回落为「密钥 #id」。AggFilter.APIKeyID 收窄到单密钥。
func TestAggregateByKey(t *testing.T) {
	env := newRollupEnv(t)
	// 清空 api_keys 以保证回落标签确定（无 id=11/12 的密钥记录）。
	if err := env.db.Exec(`TRUNCATE api_keys RESTART IDENTITY CASCADE`).Error; err != nil {
		t.Fatalf("清空 api_keys 失败: %v", err)
	}
	day := SpendDay(time.Now())
	seedKeyUsage(t, env, "k11-a", 1, 11, 1000, day.Add(time.Hour))
	seedKeyUsage(t, env, "k11-b", 1, 11, 500, day.Add(2*time.Hour))
	seedKeyUsage(t, env, "k12-a", 1, 12, 300, day.Add(3*time.Hour))
	// api_key_id=0 代表迁移前的历史已汇总数据（不可按密钥拆分）。
	seedKeyUsage(t, env, "k0-a", 1, 0, 77, day.Add(4*time.Hour))

	rows, err := env.repo.Aggregate(t.Context(), AggByKey,
		AggFilter{From: day, To: day.AddDate(0, 0, 1)})
	if err != nil {
		t.Fatalf("按密钥聚合失败: %v", err)
	}
	byID := map[int64]AggRow{}
	for _, r := range rows {
		byID[r.GroupID] = r
	}
	if len(byID) != 3 {
		t.Fatalf("应按密钥拆出 3 行，实际 %d: %+v", len(byID), rows)
	}
	if r := byID[11]; r.CreditsCharged != 1500 || r.Requests != 2 {
		t.Errorf("密钥 11 汇总不符（期望 charged=1500 req=2）: %+v", r)
	}
	if r := byID[12]; r.CreditsCharged != 300 || r.Requests != 1 {
		t.Errorf("密钥 12 汇总不符: %+v", r)
	}
	hist, ok := byID[0]
	if !ok {
		t.Fatalf("api_key_id=0 的历史汇总应单独成行: %+v", rows)
	}
	if hist.GroupKey != "历史汇总（按密钥不可拆）" {
		t.Errorf("api_key_id=0 的标签应为历史汇总占位，实际 %q", hist.GroupKey)
	}
	if byID[11].GroupKey != "密钥 #11" || byID[12].GroupKey != "密钥 #12" {
		t.Errorf("无密钥记录时应回落为「密钥 #id」: %q / %q",
			byID[11].GroupKey, byID[12].GroupKey)
	}

	// AggFilter.APIKeyID 收窄到单一密钥：只返回该密钥的聚合。
	filtered, err := env.repo.Aggregate(t.Context(), AggByKey,
		AggFilter{From: day, To: day.AddDate(0, 0, 1), APIKeyID: 11})
	if err != nil {
		t.Fatalf("按密钥筛选聚合失败: %v", err)
	}
	if len(filtered) != 1 || filtered[0].GroupID != 11 || filtered[0].Requests != 2 {
		t.Errorf("APIKeyID=11 应只返回密钥 11 的 2 次请求: %+v", filtered)
	}
}

// 按密钥聚合跨汇总边界：较早的一天已 RollDay（走聚合表，含 api_key_id），
// 当天仍读原始日志；两段合并后每个密钥的总额与直接聚合原始日志一致。
func TestAggregateByKeyAcrossRollupBoundary(t *testing.T) {
	env := newRollupEnv(t)
	if err := env.db.Exec(`TRUNCATE api_keys RESTART IDENTITY CASCADE`).Error; err != nil {
		t.Fatalf("清空 api_keys 失败: %v", err)
	}
	older := SpendDay(time.Now().AddDate(0, 0, -3))
	today := SpendDay(time.Now())
	// 较早日：密钥 21 两条、密钥 22 一条。
	seedKeyUsage(t, env, "kb-21-a", 1, 21, 1000, older.Add(time.Hour))
	seedKeyUsage(t, env, "kb-21-b", 1, 21, 500, older.Add(2*time.Hour))
	seedKeyUsage(t, env, "kb-22-a", 1, 22, 300, older.Add(3*time.Hour))
	// 当日：密钥 21 一条、密钥 22 一条。
	seedKeyUsage(t, env, "kb-21-c", 1, 21, 700, today.Add(time.Hour))
	seedKeyUsage(t, env, "kb-22-b", 1, 22, 200, today.Add(2*time.Hour))

	filter := AggFilter{From: older, To: today.AddDate(0, 0, 1)}
	// 汇总前：全部读原始日志，作为基线。
	before, err := env.repo.Aggregate(t.Context(), AggByKey, filter)
	if err != nil {
		t.Fatalf("汇总前聚合失败: %v", err)
	}
	byID := func(rows []AggRow) map[int64]AggRow {
		m := map[int64]AggRow{}
		for _, r := range rows {
			m[r.GroupID] = r
		}
		return m
	}
	base := byID(before)
	if base[21].CreditsCharged != 2200 || base[21].Requests != 3 {
		t.Fatalf("基线密钥 21 不符: %+v", base[21])
	}
	if base[22].CreditsCharged != 500 || base[22].Requests != 2 {
		t.Fatalf("基线密钥 22 不符: %+v", base[22])
	}

	// 汇总较早的一天：该日改由聚合表（已含 api_key_id）提供，当日仍读原始日志。
	if _, err := env.repo.RollDay(t.Context(), older); err != nil {
		t.Fatalf("汇总失败: %v", err)
	}
	after, err := env.repo.Aggregate(t.Context(), AggByKey, filter)
	if err != nil {
		t.Fatalf("汇总后聚合失败: %v", err)
	}
	got := byID(after)
	if got[21].CreditsCharged != 2200 || got[21].Requests != 3 {
		t.Errorf("汇总前后密钥 21 总额应一致: %+v", got[21])
	}
	if got[22].CreditsCharged != 500 || got[22].Requests != 2 {
		t.Errorf("汇总前后密钥 22 总额应一致: %+v", got[22])
	}
	// 汇总后的聚合表确实按 api_key_id 拆出了多行（验证 RollDay 的 GROUP BY 含 api_key_id）。
	var rolledRows []UsageDailyRollup
	if err := env.db.Where("day = ?", older).Find(&rolledRows).Error; err != nil {
		t.Fatalf("查询聚合失败: %v", err)
	}
	if len(rolledRows) != 2 {
		t.Fatalf("较早一日应按密钥拆出 2 行聚合，实际 %d", len(rolledRows))
	}
}
