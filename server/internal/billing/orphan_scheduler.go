package billing

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/liguopeng80/tokenzen-lite/server/internal/alerting"
	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/obs"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// orphanSettingRecheck 是定时回收关闭（间隔设为 0）时的设置复查周期：
// 循环仍按该周期醒来重读设置，使管理员在线打开回收后无需重启服务。
const orphanSettingRecheck = time.Minute

// orphanCleanupTimeout 单轮回收的执行上限，防止一轮扫描退款卡住整个循环。
const orphanCleanupTimeout = 5 * time.Minute

// OrphanCleanupScheduler 周期执行孤儿预扣回收。
//
// 孤儿预扣满足"余额 == 流水之和"的对账不变式，常规对账发现不了（见 orphan.go 头注释）。
// 在本调度器之前，回收只发生在服务启动时，进程长期不重启就等于不回收——
// 结算写库失败的请求会把用户积分一直扣住不退。
type OrphanCleanupScheduler struct {
	Service  *Service
	Settings *store.SettingsRepo
	// Threshold 孤儿判定阈值，零值时取 DefaultOrphanPrechargeThreshold。
	Threshold time.Duration
	// Alerts 主动告警通道；为 nil 时只记日志不投递。
	Alerts alerting.Notifier
}

// Start 启动回收循环，ctx 取消时退出。间隔由 orphan_cleanup_interval_sec 控制
// （0 = 关闭回收，但仍定期复查设置）。
// 每一轮用 obs.RunSafe 包裹：RunOnce panic 只记日志，不会杀死回收循环。
func (s *OrphanCleanupScheduler) Start(ctx context.Context) {
	go func() {
		for {
			interval := time.Duration(s.Settings.GetInt64(ctx, "orphan_cleanup_interval_sec")) * time.Second
			wait := interval
			if wait <= 0 {
				wait = orphanSettingRecheck
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(wait):
			}
			if interval > 0 {
				obs.RunSafe("orphan_cleanup", func() { s.RunOnce(ctx) })
			}
		}
	}()
}

// RunOnce 执行一轮回收并记录结果，供循环与测试调用。
func (s *OrphanCleanupScheduler) RunOnce(ctx context.Context) OrphanCleanupResult {
	threshold := s.Threshold
	if threshold <= 0 {
		threshold = DefaultOrphanPrechargeThreshold
	}
	runCtx, cancel := context.WithTimeout(ctx, orphanCleanupTimeout)
	defer cancel()

	result, err := s.Service.CleanupOrphanPrecharges(runCtx, threshold)
	if err != nil {
		slog.Error("定时孤儿预扣回收失败", "error", err,
			"scanned", result.Scanned, "refunded", result.Refunded)
		return result
	}
	if result.Refunded > 0 {
		slog.Warn("定时孤儿预扣回收补退了积分，说明存在未正常结算的请求",
			"scanned", result.Scanned, "refunded", result.Refunded,
			"already_handled", result.AlreadyHandled, "credits", result.RefundedCredits)
		if s.Alerts != nil {
			s.Alerts.Raise(ctx, alerting.Event{
				Type:     domain.AlertOrphanPrechargeFound,
				Severity: domain.AlertWarning,
				DedupKey: "orphan_precharge_found",
				Title:    "回收了未正常结算请求的预扣积分",
				Message: fmt.Sprintf("本轮回收补退 %d 条预扣（合计 %d 积分）。"+
					"出现孤儿预扣说明有请求在结算阶段写库失败，用户积分曾被扣住，"+
					"请检查该时段的服务日志。", result.Refunded, result.RefundedCredits),
				Payload: map[string]any{
					"scanned": result.Scanned, "refunded": result.Refunded,
					"credits": result.RefundedCredits,
				},
			})
		}
	}
	return result
}
