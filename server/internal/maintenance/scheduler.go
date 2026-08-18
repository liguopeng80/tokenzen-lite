// Package maintenance 执行周期性的数据维护：用量按日汇总、审计与用量日志的
// 保留期清理、每日花费计数清理、部门超预算检查、用户低余额检查。
// 各项的口径与依据见 docs/design/组织与审计模型.md。
package maintenance

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/liguopeng80/tokenzen-lite/server/internal/alerting"
	"github.com/liguopeng80/tokenzen-lite/server/internal/audit"
	"github.com/liguopeng80/tokenzen-lite/server/internal/billing"
	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/obs"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

const (
	// tickInterval 维护循环的执行间隔。各项任务自身按日期水位判重，
	// 因此一小时一轮既能及时补上错过的日期，也不会重复劳动。
	tickInterval = time.Hour
	// runTimeout 单轮维护的执行上限，防止一轮扫描卡住整个循环。
	runTimeout = 30 * time.Minute
	// maxRollupDaysPerRun 单轮最多补汇总的天数，避免长期停机后一次性
	// 扫全表把数据库压垮；未补完的日期由后续轮次继续处理。
	maxRollupDaysPerRun = 30
	// spendCounterRetentionDays 每日花费计数的保留天数。该计数只服务于
	// 当日限额判定，历史值的事实源是积分流水，无需长期保留。
	spendCounterRetentionDays = 7
	// lowBalanceListLimit 低余额告警正文中最多列出的用户数，超出部分只报总人数，
	// 避免大批量用户同时低余额时把告警正文撑爆通道的长度限制。
	lowBalanceListLimit = 20
	// lowBalanceNoticeLimit 单轮最多向多少个用户本人投递余额提醒。
	// 上限存在的意义是防止一次维护轮次把邮件通道打满；超出的用户由管理员侧的
	// 聚合告警覆盖，不会无人知晓。
	lowBalanceNoticeLimit = 200
	// grantBatchSize 按月自动发放每批处理的账号数。
	grantBatchSize = 200
	// maxGrantBatches 单轮最多处理的批次数，是防失控的上限而非业务约束：
	// 按 200 一批算可覆盖 1 万个账号，远超内部部署的规模。
	maxGrantBatches = 50
)

// Scheduler 周期执行数据维护任务。
type Scheduler struct {
	Settings    *store.SettingsRepo
	Rollup      *store.RollupRepo
	AuditLogs   *store.AuditLogRepo
	Spend       *store.SpendRepo
	Departments *store.DepartmentRepo
	Users       *store.UserRepo
	// UsageLogs 连续量告警（失败率、耗时分位）的数据来源。为 nil 时该项不执行。
	UsageLogs *store.UsageLogRepo
	Audit     *audit.Recorder
	Alerts    alerting.Notifier
	// Billing 按月自动发放积分的入口。为 nil 时该项不执行。
	Billing *billing.Service
	// RecordDir 请求录制文件的根目录；空表示不执行录制文件清理。
	RecordDir string
	// Now 可注入的时钟（测试用）。
	Now func() time.Time
}

func (s *Scheduler) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// Start 启动维护循环，ctx 取消时退出。
// 每一轮用 obs.RunSafe 包裹：RunOnce 内部已由 runTask 逐项兜底，
// 这一层是 RunOnce 自身出 bug 时的最后防线。
func (s *Scheduler) Start(ctx context.Context) {
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(tickInterval):
			}
			obs.RunSafe("maintenance.scheduler", func() { s.RunOnce(ctx) })
		}
	}()
}

// RunOnce 执行一轮全部维护任务。单项失败由 runTask 兜底，不影响其余项。
// 任务的执行顺序固定，便于排障时按日志顺序还原一轮内发生了什么。
func (s *Scheduler) RunOnce(ctx context.Context) {
	runCtx, cancel := context.WithTimeout(ctx, runTimeout)
	defer cancel()

	tasks := []struct {
		name string
		fn   taskFunc
	}{
		{"rollup_usage", s.rollupUsage},
		{"purge_audit_logs", s.purgeAuditLogs},
		{"purge_usage_logs", s.purgeUsageLogs},
		{"purge_spend_counters", s.purgeSpendCounters},
		{"purge_recordings", s.purgeRecordings},
		{"grant_monthly_credits", s.grantMonthlyCredits},
		{"check_department_budgets", s.checkDepartmentBudgets},
		{"check_low_balances", s.checkLowBalances},
		{"check_relay_health", s.checkRelayHealth},
	}
	for _, t := range tasks {
		s.runTask(t.name, runCtx, t.fn)
	}
}

// checkLowBalances 检查是否有用户余额低于预警阈值。
// 用户余额耗尽时其全部调用同时开始被拒绝，本人未必及时察觉，
// 因此在耗尽之前先通知管理员补发积分。阈值与门户展示的预警线是同一取值。
func (s *Scheduler) checkLowBalances(ctx context.Context) error {
	if s.Users == nil || s.Alerts == nil {
		return nil
	}
	threshold := s.Settings.GetInt64(ctx, "low_balance_threshold_credits")
	if threshold <= 0 {
		return nil
	}
	// 取到通知上限而非列名单上限：告警正文只列前若干人，但要给本人发提醒的是全部。
	users, total, err := s.Users.ListLowBalance(ctx, threshold, lowBalanceNoticeLimit)
	if err != nil {
		return fmt.Errorf("查询低余额用户: %w", err)
	}
	if total == 0 {
		return nil
	}
	s.notifyLowBalanceUsers(ctx, threshold, users)
	s.Alerts.Raise(ctx, buildLowBalanceAlert(lowBalanceAlertInput{
		Threshold: threshold,
		Users:     users,
		Total:     total,
		Now:       s.now(),
	}))
	return nil
}

// rollupUsage 汇总昨日及更早尚未汇总的用量。
// 只汇总已结束的自然日，避开孤儿预扣补偿改写日志状态的窗口。
func (s *Scheduler) rollupUsage(ctx context.Context) error {
	if s.Rollup == nil || !s.Settings.GetBool(ctx, "usage_rollup_enabled") {
		return nil
	}
	yesterday := store.SpendDay(s.now()).AddDate(0, 0, -1)
	days, err := s.Rollup.PendingDays(ctx, yesterday, maxRollupDaysPerRun)
	if err != nil {
		return fmt.Errorf("查询待汇总日期: %w", err)
	}
	for _, day := range days {
		rows, err := s.Rollup.RollDay(ctx, day)
		if err != nil {
			return fmt.Errorf("汇总 %s: %w", day.Format("2006-01-02"), err)
		}
		obs.Logger(ctx).Info("用量按日汇总完成",
			"day", day.Format("2006-01-02"), "rows", rows)
	}
	return nil
}

// purgeAuditLogs 按保留期清理审计记录，并为清理动作本身补记一条审计，
// 使「审计记录被清理」这件事本身可追溯。
func (s *Scheduler) purgeAuditLogs(ctx context.Context) error {
	days := s.Settings.GetInt64(ctx, "audit_log_retention_days")
	if days <= 0 || s.AuditLogs == nil {
		return nil
	}
	cutoff := s.now().AddDate(0, 0, -int(days))
	deleted, err := s.AuditLogs.PurgeOlderThan(ctx, cutoff)
	if err != nil {
		return fmt.Errorf("清理过期审计记录: %w", err)
	}
	if deleted == 0 {
		return nil
	}
	obs.Logger(ctx).Info("已清理过期审计记录",
		"deleted", deleted, "before", cutoff.Format(time.RFC3339))
	s.Audit.RecordSystem(ctx, audit.Entry{
		Action: domain.AuditPurge, TargetType: domain.AuditTargetAudit,
		After: map[string]any{
			"deleted": deleted, "before": cutoff.Format(time.RFC3339), "retention_days": days,
		},
		Message: "按保留期自动清理审计记录",
	})
	return nil
}

// purgeUsageLogs 按保留期清理原始用量日志。清理前由仓储校验被清理范围内
// 每一天都已完成汇总——先删后汇总会让那段时间的报表数据永久消失。
// 仓储拒绝（未完成汇总）只记 Warn，不作为错误上报：这是预期的跳过而非失败。
func (s *Scheduler) purgeUsageLogs(ctx context.Context) error {
	days := s.Settings.GetInt64(ctx, "usage_log_retention_days")
	if days <= 0 || s.Rollup == nil {
		return nil
	}
	cutoff := s.now().AddDate(0, 0, -int(days))
	deleted, err := s.Rollup.PurgeUsageLogsBefore(ctx, cutoff)
	if err != nil {
		obs.Logger(ctx).Warn("清理过期用量日志已跳过", "error", err)
		return nil
	}
	if deleted > 0 {
		obs.Logger(ctx).Info("已清理过期用量日志",
			"deleted", deleted, "before", cutoff.Format("2006-01-02"))
	}
	return nil
}

// purgeSpendCounters 两类花费计数同保留期、同轮清理，互不阻塞：
// 一类失败不影响另一类已完成的清理。
func (s *Scheduler) purgeSpendCounters(ctx context.Context) error {
	if s.Spend == nil {
		return nil
	}
	cutoff := s.now().AddDate(0, 0, -spendCounterRetentionDays)
	var errs []error
	if _, err := s.Spend.PurgeOlderThan(ctx, cutoff); err != nil {
		errs = append(errs, fmt.Errorf("每日花费计数: %w", err))
	}
	if _, err := s.Spend.PurgeKeySpendOlderThan(ctx, cutoff); err != nil {
		errs = append(errs, fmt.Errorf("Key 每日花费计数: %w", err))
	}
	return errors.Join(errs...)
}

// purgeRecordings 按保留期清理录制文件。按文件 mtime 判定，扫描
// <RecordDir>/recordings/ 目录下修改时间早于截止线的文件。0 = 不清理。
// 扫描目录失败的常见原因是路径不存在或权限不足，按 Warn 对待，不阻塞维护循环。
func (s *Scheduler) purgeRecordings(ctx context.Context) error {
	if s.RecordDir == "" {
		return nil
	}
	days := s.Settings.GetInt64(ctx, "record_retention_days")
	if days <= 0 {
		return nil
	}
	cutoff := s.now().AddDate(0, 0, -int(days))
	deleted, err := purgeRecordingsByMtime(filepath.Join(s.RecordDir, "recordings"), cutoff)
	if err != nil {
		obs.Logger(ctx).Warn("清理过期录制文件已跳过", "error", err)
		return nil
	}
	if deleted > 0 {
		obs.Logger(ctx).Info("已清理过期录制文件",
			"deleted", deleted, "before", cutoff.Format(time.RFC3339))
	}
	return nil
}

// checkDepartmentBudgets 检查各部门当月消费是否超出月度预算。
// 预算是报表对比目标，超出只告警不拦截调用。
func (s *Scheduler) checkDepartmentBudgets(ctx context.Context) error {
	if s.Departments == nil || s.Rollup == nil || s.Alerts == nil {
		return nil
	}
	departments, err := s.Departments.ListAll(ctx)
	if err != nil {
		return fmt.Errorf("查询部门列表: %w", err)
	}
	budgets := make(map[int64]store.Department, len(departments))
	for i := range departments {
		if departments[i].MonthlyBudgetCredits > 0 {
			budgets[departments[i].ID] = departments[i]
		}
	}
	if len(budgets) == 0 {
		return nil
	}

	now := s.now().In(time.Local)
	from := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.Local)
	rows, err := s.Rollup.Aggregate(ctx, store.AggByDepartment,
		store.AggFilter{From: from, To: from.AddDate(0, 1, 0)})
	if err != nil {
		return fmt.Errorf("查询部门当月消费: %w", err)
	}
	month := now.Format("2006-01")
	for _, row := range findBudgetOverruns(rows, budgets) {
		s.Alerts.Raise(ctx, buildDepartmentOverBudgetEvent(departmentOverBudgetInput{
			Department:     budgets[row.GroupID],
			CreditsCharged: row.CreditsCharged,
			Month:          month,
		}))
	}
	return nil
}

// purgeRecordingsByMtime 递归扫描 dir，删除 mtime 早于 cutoff 的普通文件。
// 目录不删除（保留日期分区结构），返回已删除文件数。
func purgeRecordingsByMtime(dir string, cutoff time.Time) (int, error) {
	_, err := os.Stat(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	deleted := 0
	err = filepath.WalkDir(dir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil // 跳过无法 stat 的条目，不中断清理
		}
		if info.ModTime().Before(cutoff) {
			if rmErr := os.Remove(path); rmErr == nil {
				deleted++
			}
		}
		return nil
	})
	return deleted, err
}
