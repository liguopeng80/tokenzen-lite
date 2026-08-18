package maintenance

import (
	"context"
	"time"

	"github.com/liguopeng80/tokenzen-lite/server/internal/obs"
)

const (
	// perTaskTimeout 单项维护任务的执行上限，独立于整轮的 runTimeout。
	// 没有单项限制时一条慢查询会吃掉绝大部分整轮预算，后续任务被饿死；
	// 5 分钟对单项足够宽裕（按月发放的批处理也有自己的批次上限做防失控），
	// 同时保证一轮内多项任务都能分到预算。
	perTaskTimeout = 5 * time.Minute

	// 指标名（与 obs 已有指标同前缀 tzl_）。在此包内定义是因为维护包是唯一使用方，
	// 不需要在 obs 包中登记。
	metricMaintenanceTaskRuns     = "tzl_maintenance_task_runs_total"
	metricMaintenanceTaskDuration = "tzl_maintenance_task_duration_seconds"
)

// taskFunc 是维护任务的统一签名。返回 error 时由 runTask 统一记录；
// panic 时由 runTask 内置的 recover 兜底，不连累其余任务。
type taskFunc func(ctx context.Context) error

// runTask 是单项维护任务的横切包装：入口与结束日志、计时、计数、单一 panic 兜底。
//
// 任务自身仍可在内部按需记录带任务特定上下文的 Info/Warn 日志（如汇总完成的日期、
// 清理跳过的原因）；ERROR 级别的失败统一由本函数按 task 名记录，避免每项任务抄写一遍。
// 每项任务从 parent 派生独立的 perTaskTimeout 预算，单项超时不传染其余项。
func (s *Scheduler) runTask(name string, parent context.Context, fn taskFunc) {
	// context.WithTimeout 取 parent deadline 与 perTaskTimeout 的较早者，
	// 因此整轮预算耗尽时任务同样会被取消。
	taskCtx, cancel := context.WithTimeout(parent, perTaskTimeout)
	defer cancel()

	start := time.Now()
	log := obs.Logger(taskCtx)
	log.Info("维护任务开始", "task", name)

	// result 取 ok / error / panic 三态，作为计数器标签区分任务终态。
	result := "ok"
	func() {
		defer func() {
			if r := recover(); r != nil {
				result = "panic"
				log.Error("维护任务 panic，已恢复", "task", name, "panic", r)
			}
		}()
		if err := fn(taskCtx); err != nil {
			result = "error"
			log.Error("维护任务失败", "task", name, "error", err)
		}
	}()

	elapsed := time.Since(start)
	obs.DefaultMetrics().Inc(metricMaintenanceTaskRuns, "task", name, "result", result)
	obs.DefaultMetrics().Observe(metricMaintenanceTaskDuration, elapsed.Seconds(), "task", name)
	log.Info("维护任务结束", "task", name,
		"duration_ms", elapsed.Milliseconds(), "result", result)
}
