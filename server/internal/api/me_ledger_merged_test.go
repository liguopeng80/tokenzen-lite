package api

import (
	"net/http"
	"testing"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// seedCallLedger 直接种入一次调用产生的两条流水（预扣 + 结算差额），
// 不经中继链路——本组用例验证的是展示口径，不是计费本身。
func seedCallLedger(t *testing.T, e *testEnv, userID int64, requestID string,
	precharge, adjust domain.Credits, balanceAfter domain.Credits) {

	t.Helper()
	rows := []store.LedgerEntry{
		{UserID: userID, EntryType: domain.LedgerConsume, Amount: -precharge,
			BalanceAfter: balanceAfter + adjust*-1, RequestID: requestID, Note: "请求预扣"},
		{UserID: userID, EntryType: domain.LedgerSettleAdjust, Amount: adjust,
			BalanceAfter: balanceAfter, RequestID: requestID, Note: "结算差额"},
	}
	for i := range rows {
		if err := e.db.Create(&rows[i]).Error; err != nil {
			t.Fatalf("种入流水失败：%v", err)
		}
	}
}

// 员工看到的是一次调用的实际扣费，不是内部记账过程：
// 预扣与结算差额合并为一条净额，展开才看得到那两笔。
func TestMeLedgerMergesCallEntries(t *testing.T) {
	e := newTestEnv(t)
	c := e.seedAndLogin(t, "ledgerreader", domain.RoleUser)
	userID := userIDOf(t, e, "ledgerreader")

	// 一次调用：预扣 19202，结算退回 19046，净扣 156。
	seedCallLedger(t, e, userID, "req-merge-1", 19202, 19046, 980_798)

	resp, env := e.do(t, c, "GET", "/api/me/ledger", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("查流水失败：%d %v", resp.StatusCode, env)
	}
	page := env["data"].(map[string]any)
	if total := int(page["total"].(float64)); total != 1 {
		t.Fatalf("一次调用应合并为 1 条账目，实际 %d", total)
	}
	row := page["items"].([]any)[0].(map[string]any)
	if amount := int64(row["amount"].(float64)); amount != -156 {
		t.Errorf("净额应为 -156（预扣 19202 减去退回 19046），实际 %d", amount)
	}
	if row["request_id"] != "req-merge-1" {
		t.Errorf("调用类账目应带请求标识，实际 %v", row["request_id"])
	}
	if bal := int64(row["balance_after"].(float64)); bal != 980_798 {
		t.Errorf("变动后余额应取组内最后一条，实际 %d", bal)
	}
	entries := row["entries"].([]any)
	if len(entries) != 2 {
		t.Fatalf("展开应看到预扣与结算差额两笔，实际 %d 笔", len(entries))
	}
	if entries[0].(map[string]any)["entry_type"] != string(domain.LedgerConsume) {
		t.Errorf("展开的第一笔应是预扣，实际 %v", entries[0])
	}
}

// 发放、兑换等单条账目不参与合并，各自成行。
func TestMeLedgerKeepsSingleEntriesSeparate(t *testing.T) {
	e := newTestEnv(t)
	c := e.seedAndLogin(t, "ledgergrant", domain.RoleUser)
	userID := userIDOf(t, e, "ledgergrant")

	if _, err := e.deps.Billing.Grant(t.Context(), userID, 1_000_000, 0, "初始额度", ""); err != nil {
		t.Fatalf("发放失败：%v", err)
	}
	seedCallLedger(t, e, userID, "req-merge-2", 5000, 4900, 999_900)

	resp, env := e.do(t, c, "GET", "/api/me/ledger", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("查流水失败：%d %v", resp.StatusCode, env)
	}
	page := env["data"].(map[string]any)
	if total := int(page["total"].(float64)); total != 2 {
		t.Fatalf("一次发放加一次调用应为 2 条账目，实际 %d", total)
	}
	// 按 ID 倒序：最近的调用在前，发放在后。
	items := page["items"].([]any)
	first := items[0].(map[string]any)
	if first["request_id"] != "req-merge-2" {
		t.Errorf("最近一条应是调用扣费，实际 %v", first)
	}
	second := items[1].(map[string]any)
	if second["entry_type"] != string(domain.LedgerGrant) {
		t.Errorf("发放应保持原类型，实际 %v", second["entry_type"])
	}
	if entries := second["entries"].([]any); len(entries) != 1 {
		t.Errorf("单条账目的展开应只有自身，实际 %d 笔", len(entries))
	}
}

// 合并视角下按 consume 筛选表示「只看调用扣费」，发放与兑换不出现。
func TestMeLedgerFilterOnlyCalls(t *testing.T) {
	e := newTestEnv(t)
	c := e.seedAndLogin(t, "ledgerfilter", domain.RoleUser)
	userID := userIDOf(t, e, "ledgerfilter")
	if _, err := e.deps.Billing.Grant(t.Context(), userID, 1_000_000, 0, "初始额度", ""); err != nil {
		t.Fatalf("发放失败：%v", err)
	}
	seedCallLedger(t, e, userID, "req-merge-3", 5000, 4900, 999_900)

	resp, env := e.do(t, c, "GET", "/api/me/ledger?entry_type=consume", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("查流水失败：%d %v", resp.StatusCode, env)
	}
	page := env["data"].(map[string]any)
	if total := int(page["total"].(float64)); total != 1 {
		t.Fatalf("只看调用扣费应为 1 条，实际 %d", total)
	}
	row := page["items"].([]any)[0].(map[string]any)
	if len(row["entries"].([]any)) != 2 {
		t.Errorf("筛选后仍应带内部记账明细：%v", row["entries"])
	}
}

// view=raw 返回未合并的原始流水，供对账核验。
func TestMeLedgerRawViewKeepsEveryEntry(t *testing.T) {
	e := newTestEnv(t)
	c := e.seedAndLogin(t, "ledgerraw", domain.RoleUser)
	userID := userIDOf(t, e, "ledgerraw")
	seedCallLedger(t, e, userID, "req-merge-4", 5000, 4900, 999_900)

	resp, env := e.do(t, c, "GET", "/api/me/ledger?view=raw", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("查流水失败：%d %v", resp.StatusCode, env)
	}
	page := env["data"].(map[string]any)
	if total := int(page["total"].(float64)); total != 2 {
		t.Fatalf("原始视角应为 2 条，实际 %d", total)
	}
}

// 合并后的分页按账目条数计算，同一次调用的两条流水不会被分页切开。
func TestMeLedgerPaginationCountsMergedRows(t *testing.T) {
	e := newTestEnv(t)
	c := e.seedAndLogin(t, "ledgerpage", domain.RoleUser)
	userID := userIDOf(t, e, "ledgerpage")
	for _, id := range []string{"req-page-1", "req-page-2", "req-page-3"} {
		seedCallLedger(t, e, userID, id, 5000, 4900, 999_900)
	}

	resp, env := e.do(t, c, "GET", "/api/me/ledger?page=1&page_size=2", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("查流水失败：%d %v", resp.StatusCode, env)
	}
	page := env["data"].(map[string]any)
	if total := int(page["total"].(float64)); total != 3 {
		t.Fatalf("总数应为合并后的 3 条，实际 %d", total)
	}
	items := page["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("首页应有 2 条，实际 %d", len(items))
	}
	for _, it := range items {
		if len(it.(map[string]any)["entries"].([]any)) != 2 {
			t.Errorf("每条账目都应完整带上两笔记账：%v", it)
		}
	}
}
