package maintenance

import (
	"fmt"
	"strings"
	"time"

	"github.com/liguopeng80/tokenzen-lite/server/internal/alerting"
	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// 本文件集中 checkLowBalances 与 checkDepartmentBudgets 的纯逻辑——
// 阈值判定、告警正文模板、日期边界——使这部分可独立于 DB 单测。

// lowBalanceAlertInput 构造一条低余额聚合告警所需的全部入参。
// 纯数据结构，不引用 Scheduler，便于在无 DB 环境下测试正文与去重键。
type lowBalanceAlertInput struct {
	Threshold domain.Credits
	// Users 已经按余额升序排列的低余额用户列表（取到通知上限而非列名单上限）。
	Users []store.LowBalanceUser
	// Total 符合条件的总人数，可能大于 len(Users)。
	Total int64
	// Now 当前时刻，用于去重键中的日期。注入而非 time.Now() 使函数可测。
	Now time.Time
}

// buildLowBalanceAlert 构造管理员侧的低余额聚合告警事件。
//
// 正文规则：名单按余额升序，列前 lowBalanceListLimit 人；超出部分只报总人数。
// 耗尽人数口径：名单未截断时精确统计；截断且列出部分全耗尽时改为「至少」，
// 因为名单外的低余额用户也可能已耗尽。
func buildLowBalanceAlert(in lowBalanceAlertInput) alerting.Event {
	named := in.Users
	if len(named) > lowBalanceListLimit {
		named = named[:lowBalanceListLimit]
	}
	names := make([]string, 0, len(named))
	depleted := 0
	for _, u := range named {
		names = append(names, fmt.Sprintf("%s（%d）", u.Username, u.CreditBalance))
		if u.CreditBalance <= 0 {
			depleted++
		}
	}
	listed := strings.Join(names, "、")
	truncated := in.Total > int64(len(named))
	if truncated {
		listed += fmt.Sprintf("，另有 %d 人未列出", in.Total-int64(len(named)))
	}
	depletedText := fmt.Sprintf("其中 %d 人已耗尽", depleted)
	if truncated && depleted == len(named) {
		depletedText = fmt.Sprintf("其中至少 %d 人已耗尽", depleted)
	}
	day := in.Now.In(time.Local).Format("2006-01-02")
	return alerting.Event{
		Type:     domain.AlertUserLowBalance,
		Severity: domain.AlertWarning,
		// 去重键含当日日期与人数：人数变化即视为新情况，立即再次告警，
		// 人数不变时按告警抑制窗口节流。
		DedupKey: fmt.Sprintf("user_low_balance:%s:%d", day, in.Total),
		Title:    fmt.Sprintf("%d 名用户余额低于预警阈值", in.Total),
		Message: fmt.Sprintf("余额低于 %d 积分的启用用户共 %d 人（%s）：%s。"+
			"余额耗尽后其 API 调用将被拒绝，请及时发放积分。",
			in.Threshold, in.Total, depletedText, listed),
		Payload: map[string]any{
			"threshold": in.Threshold, "total": in.Total, "users": in.Users,
		},
	}
}

// departmentOverBudgetInput 构造一条部门超预算告警所需的全部入参。
type departmentOverBudgetInput struct {
	Department     store.Department
	CreditsCharged domain.Credits
	Month          string // 已格式化的「YYYY-MM」
}

// buildDepartmentOverBudgetEvent 构造部门当月消费超预算的告警事件。
func buildDepartmentOverBudgetEvent(in departmentOverBudgetInput) alerting.Event {
	return alerting.Event{
		Type:     domain.AlertDepartmentOverBudget,
		Severity: domain.AlertWarning,
		DedupKey: fmt.Sprintf("department_over_budget:%d:%s",
			in.Department.ID, in.Month),
		Title: "部门当月消费已超预算：" + in.Department.Name,
		Message: fmt.Sprintf("部门「%s」%s 已消费 %d 积分，超出月度预算 %d 积分。"+
			"预算仅作对比目标，不拦截调用；如需限制，请为成员设置每日花费上限。",
			in.Department.Name, in.Month, in.CreditsCharged,
			in.Department.MonthlyBudgetCredits),
		Payload: map[string]any{
			"department_id":   in.Department.ID,
			"month":           in.Month,
			"credits_charged": in.CreditsCharged,
			"monthly_budget":  in.Department.MonthlyBudgetCredits,
		},
	}
}

// findBudgetOverruns 从聚合结果中筛出消费超出预算的行。
// budgets 只含设了预算（MonthlyBudgetCredits > 0）的部门，调用方负责构造。
func findBudgetOverruns(rows []store.AggRow,
	budgets map[int64]store.Department) []store.AggRow {

	var overruns []store.AggRow
	for _, row := range rows {
		dept, ok := budgets[row.GroupID]
		if !ok || row.CreditsCharged <= dept.MonthlyBudgetCredits {
			continue
		}
		overruns = append(overruns, row)
	}
	return overruns
}
