package store

import (
	"testing"
	"time"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
)

// 本文件是项目维度（0019）的 DB 集成测试，由主会话在 TZL_TEST_DATABASE_URL 上
// 与其它集成测试一起串行执行（go test -p 1 ./...）。每个用例复用 rollupEnv 的隔离环境。

// seedProjectUsage 写一条已结算的用量日志，带项目快照。
func seedProjectUsage(t *testing.T, env *rollupEnv, requestID string, userID, projectID int64,
	charged int64, at time.Time) {
	t.Helper()
	log := &UsageLog{
		RequestID: requestID, UserID: userID, APIKeyID: 1,
		DepartmentID: 0, ProjectID: projectID,
		ModelName: "glm-5", ChannelID: 1,
		PromptTokens: 100, CompletionTokens: 50,
		CreditsCharged: charged, CreditsCost: charged / 2,
		Status: domain.UsageSettled, CreatedAt: at,
	}
	if err := env.db.Create(log).Error; err != nil {
		t.Fatalf("写入用量日志失败: %v", err)
	}
}

// TestAggregateByProjectGroupsCorrectly 按项目聚合：不同项目各自成行；
// project_id=0（未归属）单独成行，显示为「未归属」。
func TestAggregateByProjectGroupsCorrectly(t *testing.T) {
	env := newRollupEnv(t)
	// 清空 api_keys 以保证回落标签确定。
	if err := env.db.Exec(`TRUNCATE api_keys RESTART IDENTITY CASCADE`).Error; err != nil {
		t.Fatalf("清空 api_keys 失败: %v", err)
	}
	// 建两个项目，对应 project_id 由 SEQUENCE 决定（1、2）。
	p1 := &Project{Name: "alpha", Status: domain.ProjectEnabled}
	p2 := &Project{Name: "beta", Status: domain.ProjectEnabled}
	if err := env.db.Create(p1).Error; err != nil {
		t.Fatalf("建项目 alpha 失败: %v", err)
	}
	if err := env.db.Create(p2).Error; err != nil {
		t.Fatalf("建项目 beta 失败: %v", err)
	}
	day := SpendDay(time.Now())
	seedProjectUsage(t, env, "p1-a", 1, p1.ID, 1000, day.Add(time.Hour))
	seedProjectUsage(t, env, "p1-b", 1, p1.ID, 500, day.Add(2*time.Hour))
	seedProjectUsage(t, env, "p2-a", 1, p2.ID, 300, day.Add(3*time.Hour))
	// project_id=0：未归属（密钥未挂项目）。
	seedProjectUsage(t, env, "p0-a", 1, 0, 77, day.Add(4*time.Hour))

	rows, err := env.repo.Aggregate(t.Context(), AggByProject,
		AggFilter{From: day, To: day.AddDate(0, 0, 1)})
	if err != nil {
		t.Fatalf("按项目聚合失败: %v", err)
	}
	byID := map[int64]AggRow{}
	for _, r := range rows {
		byID[r.GroupID] = r
	}
	if len(byID) != 3 {
		t.Fatalf("应按项目拆出 3 行（alpha/beta/未归属），实际 %d: %+v", len(byID), rows)
	}
	if r := byID[p1.ID]; r.CreditsCharged != 1500 || r.Requests != 2 || r.GroupKey != "alpha" {
		t.Errorf("项目 alpha 汇总不符（期望 charged=1500 req=2 name=alpha）: %+v", r)
	}
	if r := byID[p2.ID]; r.CreditsCharged != 300 || r.Requests != 1 || r.GroupKey != "beta" {
		t.Errorf("项目 beta 汇总不符: %+v", r)
	}
	unassigned, ok := byID[0]
	if !ok {
		t.Fatalf("未归属项目应单独成行: %+v", rows)
	}
	if unassigned.GroupKey != "未归属" {
		t.Errorf("project_id=0 的标签应为「未归属」，实际 %q", unassigned.GroupKey)
	}
	if unassigned.CreditsCharged != 77 {
		t.Errorf("未归属扣费期望 77，实际 %d", unassigned.CreditsCharged)
	}

	// AggFilter.ProjectID 收窄到单一项目：只返回该项目的聚合。
	filtered, err := env.repo.Aggregate(t.Context(), AggByProject,
		AggFilter{From: day, To: day.AddDate(0, 0, 1), ProjectID: &p1.ID})
	if err != nil {
		t.Fatalf("按项目筛选聚合失败: %v", err)
	}
	if len(filtered) != 1 || filtered[0].GroupID != p1.ID || filtered[0].Requests != 2 {
		t.Errorf("ProjectID=alpha 应只返回项目 alpha 的 2 次请求: %+v", filtered)
	}
	// ProjectID=&0 只筛未归属。
	zero := int64(0)
	unassignedOnly, err := env.repo.Aggregate(t.Context(), AggByProject,
		AggFilter{From: day, To: day.AddDate(0, 0, 1), ProjectID: &zero})
	if err != nil {
		t.Fatalf("筛未归属聚合失败: %v", err)
	}
	if len(unassignedOnly) != 1 || unassignedOnly[0].GroupID != 0 {
		t.Errorf("ProjectID=&0 应只返回未归属行: %+v", unassignedOnly)
	}
}

// TestAggregateByProjectHistoricalMerge 验证迁移前已汇总日期（rollup 中 project_id=0）
// 在按项目维度时报表合并显示为「未归属」。模拟方式：直接向 rollup 插一行 project_id=0，
// 代表 0019 迁移前的历史已汇总数据；当日原始日志全部带具体 project_id。
func TestAggregateByProjectHistoricalMerge(t *testing.T) {
	env := newRollupEnv(t)
	if err := env.db.Exec(`TRUNCATE api_keys RESTART IDENTITY CASCADE`).Error; err != nil {
		t.Fatalf("清空 api_keys 失败: %v", err)
	}
	older := SpendDay(time.Now().AddDate(0, 0, -3))
	today := SpendDay(time.Now())

	// 历史：直接插 rollup 行，project_id=0（迁移前历史汇总），代表按项目不可拆的历史数据。
	env.db.Exec(`INSERT INTO usage_daily_rollup
		(day, user_id, department_id, project_id, model_name, channel_id, integration_id, api_key_id,
		 requests, prompt_tokens, completion_tokens, cache_read_tokens, cache_write_tokens,
		 credits_charged, credits_cost)
		VALUES (?, 1, 0, 0, 'glm-5', 1, 0, 0, 5, 500, 250, 0, 0, 5000, 2000)`, older)
	// 同步登记 rollup 水位：Aggregate 以 usage_rollup_state 划分聚合表与原始日志的分界，
	// 缺水位时 [from, rollupTo) 段为空，rollup 行不会被读。生产中 0019 迁移为既有 rollup 行
	// 保留状态、只补 project_id 列；此处须同构模拟，否则历史行被默认读原始日志段漏掉。
	env.db.Exec(`INSERT INTO usage_rollup_state (day, rows_rolled, completed_at)
		VALUES (?, 1, now())`, older)

	// 当日：原始日志，归属项目 1。
	p1 := &Project{Name: "proj-today", Status: domain.ProjectEnabled}
	if err := env.db.Create(p1).Error; err != nil {
		t.Fatalf("建项目失败: %v", err)
	}
	seedProjectUsage(t, env, "today-1", 1, p1.ID, 2000, today.Add(time.Hour))

	rows, err := env.repo.Aggregate(t.Context(), AggByProject,
		AggFilter{From: older, To: today.AddDate(0, 0, 1)})
	if err != nil {
		t.Fatalf("聚合失败: %v", err)
	}
	byID := map[int64]AggRow{}
	for _, r := range rows {
		byID[r.GroupID] = r
	}
	// 历史的 project_id=0 与当日的 project_id=p1.ID 各成一行；0 行为「未归属」。
	hist, ok := byID[0]
	if !ok {
		t.Fatalf("历史 project_id=0 应合并为未归属行: %+v", rows)
	}
	if hist.GroupKey != "未归属" {
		t.Errorf("历史 project_id=0 显示为「未归属」，实际 %q", hist.GroupKey)
	}
	if hist.CreditsCharged != 5000 || hist.Requests != 5 {
		t.Errorf("历史未归属汇总不符（期望 charged=5000 req=5）: %+v", hist)
	}
	if r := byID[p1.ID]; r.CreditsCharged != 2000 || r.GroupKey != "proj-today" {
		t.Errorf("当日项目汇总不符: %+v", r)
	}
}

// TestProjectAndDepartmentDimensionsOrthogonal 验证项目与部门是两个正交维度：
// 同一批日志按部门聚合与按项目聚合，各自的总扣费相等（同一份数据两种切法）。
func TestProjectAndDepartmentDimensionsOrthogonal(t *testing.T) {
	env := newRollupEnv(t)
	if err := env.db.Exec(`TRUNCATE api_keys RESTART IDENTITY CASCADE`).Error; err != nil {
		t.Fatalf("清空 api_keys 失败: %v", err)
	}
	day := SpendDay(time.Now())
	// 两条日志：部门 7 项目 1、部门 8 项目 2。部门与项目取值互不相关（正交）。
	log1 := &UsageLog{
		RequestID: "ortho-1", UserID: 1, APIKeyID: 1,
		DepartmentID: 7, ProjectID: 1, ModelName: "glm-5", ChannelID: 1,
		PromptTokens: 100, CreditsCharged: 1000, CreditsCost: 400,
		Status: domain.UsageSettled, CreatedAt: day.Add(time.Hour),
	}
	log2 := &UsageLog{
		RequestID: "ortho-2", UserID: 1, APIKeyID: 1,
		DepartmentID: 8, ProjectID: 2, ModelName: "glm-5", ChannelID: 1,
		PromptTokens: 100, CreditsCharged: 2000, CreditsCost: 800,
		Status: domain.UsageSettled, CreatedAt: day.Add(2 * time.Hour),
	}
	if err := env.db.Create(log1).Error; err != nil {
		t.Fatalf("写日志 1 失败: %v", err)
	}
	if err := env.db.Create(log2).Error; err != nil {
		t.Fatalf("写日志 2 失败: %v", err)
	}

	filter := AggFilter{From: day, To: day.AddDate(0, 0, 1)}
	byDept, err := env.repo.Aggregate(t.Context(), AggByDepartment, filter)
	if err != nil {
		t.Fatalf("按部门聚合失败: %v", err)
	}
	byProj, err := env.repo.Aggregate(t.Context(), AggByProject, filter)
	if err != nil {
		t.Fatalf("按项目聚合失败: %v", err)
	}
	sumCharged := func(rows []AggRow) int64 {
		var s int64
		for _, r := range rows {
			s += int64(r.CreditsCharged)
		}
		return s
	}
	if got := sumCharged(byDept); got != 3000 {
		t.Errorf("按部门聚合总扣费期望 3000，实际 %d", got)
	}
	if got := sumCharged(byProj); got != 3000 {
		t.Errorf("按项目聚合总扣费期望 3000，实际 %d", got)
	}
	// 部门 7 与项目 1 各对应 log1 的扣费；部门 8 与项目 2 各对应 log2。
	deptOf := func(id int64) AggRow {
		for _, r := range byDept {
			if r.GroupID == id {
				return r
			}
		}
		return AggRow{}
	}
	projOf := func(id int64) AggRow {
		for _, r := range byProj {
			if r.GroupID == id {
				return r
			}
		}
		return AggRow{}
	}
	if deptOf(7).CreditsCharged != 1000 || projOf(1).CreditsCharged != 1000 {
		t.Errorf("部门 7 与项目 1 应各为 1000（log1）: dept=%+v proj=%+v",
			deptOf(7), projOf(1))
	}
	if deptOf(8).CreditsCharged != 2000 || projOf(2).CreditsCharged != 2000 {
		t.Errorf("部门 8 与项目 2 应各为 2000（log2）: dept=%+v proj=%+v",
			deptOf(8), projOf(2))
	}
}

// TestRollDayGroupsByProject 验证 RollDay 的 GROUP BY 含 project_id：
// 汇总后聚合表按项目拆出多行，project_id 取值正确。
func TestRollDayGroupsByProject(t *testing.T) {
	env := newRollupEnv(t)
	day := SpendDay(time.Now().AddDate(0, 0, -2))
	seedProjectUsage(t, env, "rp-1-a", 1, 31, 1000, day.Add(time.Hour))
	seedProjectUsage(t, env, "rp-1-b", 1, 31, 500, day.Add(2*time.Hour))
	seedProjectUsage(t, env, "rp-2-a", 1, 32, 300, day.Add(3*time.Hour))
	seedProjectUsage(t, env, "rp-0-a", 1, 0, 77, day.Add(4*time.Hour))

	if _, err := env.repo.RollDay(t.Context(), day); err != nil {
		t.Fatalf("汇总失败: %v", err)
	}
	var rows []UsageDailyRollup
	if err := env.db.Where("day = ?", day).Find(&rows).Error; err != nil {
		t.Fatalf("查询聚合失败: %v", err)
	}
	byProj := map[int64]UsageDailyRollup{}
	for _, r := range rows {
		byProj[r.ProjectID] = r
	}
	if len(byProj) != 3 {
		t.Fatalf("应按项目拆出 3 行聚合，实际 %d", len(byProj))
	}
	if r := byProj[31]; r.Requests != 2 || r.CreditsCharged != 1500 {
		t.Errorf("项目 31 汇总不符（期望 req=2 charged=1500）: %+v", r)
	}
	if r := byProj[32]; r.Requests != 1 || r.CreditsCharged != 300 {
		t.Errorf("项目 32 汇总不符: %+v", r)
	}
	if r := byProj[0]; r.Requests != 1 || r.CreditsCharged != 77 {
		t.Errorf("未归属汇总不符: %+v", r)
	}
}

// TestRollDayProjectSnapshotStability 验证快照稳定性：密钥改挂项目后，
// 已汇总日期的 rollup 行不变（历史口径稳定）。
func TestRollDayProjectSnapshotStability(t *testing.T) {
	env := newRollupEnv(t)
	day := SpendDay(time.Now().AddDate(0, 0, -2))
	seedProjectUsage(t, env, "snap-1", 1, 41, 1000, day.Add(time.Hour))
	if _, err := env.repo.RollDay(t.Context(), day); err != nil {
		t.Fatalf("首次汇总失败: %v", err)
	}
	var before []UsageDailyRollup
	env.db.Where("day = ?", day).Find(&before)
	if len(before) != 1 || before[0].ProjectID != 41 {
		t.Fatalf("汇总后应有 1 行 project_id=41: %+v", before)
	}
	// 模拟密钥改挂项目：新日志归属项目 42，但同一日再次汇总不会改动历史快照含义——
	// RollDay 先删后插，按日志当前 project_id 重新分桶。这里验证日志 project_id 变化后
	// 重汇总反映最新日志归属（不是查询时 JOIN，而是重写聚合）。
	seedProjectUsage(t, env, "snap-2", 1, 42, 500, day.Add(2*time.Hour))
	if _, err := env.repo.RollDay(t.Context(), day); err != nil {
		t.Fatalf("再次汇总失败: %v", err)
	}
	var after []UsageDailyRollup
	env.db.Where("day = ?", day).Find(&after)
	byProj := map[int64]UsageDailyRollup{}
	for _, r := range after {
		byProj[r.ProjectID] = r
	}
	if len(after) != 2 {
		t.Fatalf("重汇总应按项目拆出 2 行（41/42），实际 %d", len(after))
	}
	if r := byProj[41]; r.CreditsCharged != 1000 {
		t.Errorf("项目 41 历史日志的扣费应保留: %+v", r)
	}
	if r := byProj[42]; r.CreditsCharged != 500 {
		t.Errorf("项目 42 新日志扣费应为 500: %+v", r)
	}
}
