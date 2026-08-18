package maintenance

import (
	"strings"
	"testing"
	"time"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// buildLowBalanceAlert 的正文与去重键完全由入参决定，不依赖 DB。
// 本组测试覆盖名单截断、耗尽人数口径、去重键含日期等关键分支。

func TestBuildLowBalanceAlertIncludesAllNamedUsers(t *testing.T) {
	now := time.Date(2026, 8, 9, 10, 0, 0, 0, time.Local)
	ev := buildLowBalanceAlert(lowBalanceAlertInput{
		Threshold: 1000,
		Users: []store.LowBalanceUser{
			{ID: 1, Username: "alice", CreditBalance: 0},
			{ID: 2, Username: "bob", CreditBalance: 500},
		},
		Total: 2,
		Now:   now,
	})
	if !strings.Contains(ev.Message, "alice") || !strings.Contains(ev.Message, "bob") {
		t.Errorf("正文应包含全部列出用户：%s", ev.Message)
	}
	if !strings.Contains(ev.Message, "其中 1 人已耗尽") {
		t.Errorf("余额为 0 的用户应计入耗尽人数：%s", ev.Message)
	}
	if got := ev.Payload["total"]; got != int64(2) {
		t.Errorf("Payload total 应为 2，实际 %v", got)
	}
	if !strings.Contains(ev.DedupKey, "2026-08-09") {
		t.Errorf("去重键应含当日日期：%s", ev.DedupKey)
	}
}

// 名单超出列名单上限时截断，正文给出未列出的人数。
func TestBuildLowBalanceAlertTruncatesLongList(t *testing.T) {
	users := make([]store.LowBalanceUser, lowBalanceListLimit+5)
	for i := range users {
		users[i] = store.LowBalanceUser{
			ID: int64(i + 1), Username: "u" + itoa(i), CreditBalance: 1,
		}
	}
	total := int64(len(users) + 10) // total 远大于列出人数
	ev := buildLowBalanceAlert(lowBalanceAlertInput{
		Threshold: 1000,
		Users:     users,
		Total:     total,
		Now:       time.Date(2026, 8, 9, 10, 0, 0, 0, time.Local),
	})
	if !strings.Contains(ev.Message, "未列出") {
		t.Errorf("截断时应提示未列出的人数：%s", ev.Message)
	}
}

// 名单截断且列出部分全耗尽时，耗尽人数改为「至少」。
func TestBuildLowBalanceAlertDepletedAtLeastWhenTruncatedAllDepleted(t *testing.T) {
	users := make([]store.LowBalanceUser, lowBalanceListLimit)
	for i := range users {
		users[i] = store.LowBalanceUser{
			ID: int64(i + 1), Username: "u" + itoa(i), CreditBalance: 0,
		}
	}
	ev := buildLowBalanceAlert(lowBalanceAlertInput{
		Threshold: 1000,
		Users:     users,
		Total:     int64(lowBalanceListLimit + 50), // 触发截断
		Now:       time.Date(2026, 8, 9, 10, 0, 0, 0, time.Local),
	})
	if !strings.Contains(ev.Message, "至少") {
		t.Errorf("截断且列出部分全耗尽时耗尽口径应为「至少」：%s", ev.Message)
	}
}

// findBudgetOverruns 只筛出超出预算的部门行；未设预算与未超的都不返回。
func TestFindBudgetOverruns(t *testing.T) {
	budgets := map[int64]store.Department{
		1: {ID: 1, Name: "超预算部", MonthlyBudgetCredits: 1000},
		2: {ID: 2, Name: "未超预算部", MonthlyBudgetCredits: 10_000},
	}
	rows := []store.AggRow{
		{GroupID: 1, CreditsCharged: 5000}, // 超
		{GroupID: 2, CreditsCharged: 5000}, // 未超
		{GroupID: 3, CreditsCharged: 9999}, // 未设预算
		{GroupID: 1, CreditsCharged: 1000}, // 等于预算不算超
	}
	overruns := findBudgetOverruns(rows, budgets)
	if len(overruns) != 1 {
		t.Fatalf("应只筛出 1 行（超预算的部门），实际 %d 行", len(overruns))
	}
	if overruns[0].GroupID != 1 || overruns[0].CreditsCharged != 5000 {
		t.Errorf("筛出的应是部门 1 的 5000 积分行，实际 %+v", overruns[0])
	}
}

func TestBuildDepartmentOverBudgetEvent(t *testing.T) {
	dept := store.Department{ID: 7, Name: "研发部", MonthlyBudgetCredits: 2000}
	ev := buildDepartmentOverBudgetEvent(departmentOverBudgetInput{
		Department:     dept,
		CreditsCharged: 5000,
		Month:          "2026-08",
	})
	if !strings.Contains(ev.Title, "研发部") {
		t.Errorf("标题应含部门名：%s", ev.Title)
	}
	if !strings.Contains(ev.DedupKey, "2026-08") {
		t.Errorf("去重键应含月份：%s", ev.DedupKey)
	}
	if got := ev.Payload["department_id"]; got != int64(7) {
		t.Errorf("Payload department_id 应为 7，实际 %v", got)
	}
	if got := ev.Payload["monthly_budget"]; got != domain.Credits(2000) {
		t.Errorf("Payload monthly_budget 应为 2000，实际 %v", got)
	}
}
