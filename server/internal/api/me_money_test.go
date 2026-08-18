package api

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/pricing"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// 覆盖 /api/me/* 自助端点的 _money 旁置字段：积分整数（int）与货币定点串（string）
// 同帧输出，供程序化消费者直接取用。期望值用与生产同源的 pricing.CreditsToDecimalString
// 计算，默认兑换率 1e6 → 6 位小数（无损）。

const meMoneyTestRate = int64(1_000_000)

// meMoneyOf 把积分按默认兑换率换算为期望货币串，与处理器内 newMoneyCtx(rate).money 同口径。
func meMoneyOf(credits int64) string {
	return pricing.CreditsToDecimalString(credits, meMoneyTestRate, pricing.DetailDecimals(meMoneyTestRate))
}

// TestMeBalanceHasMoneyFields 覆盖 /api/me/balance：余额与已用积分旁置货币串。
func TestMeBalanceHasMoneyFields(t *testing.T) {
	e := newTestEnv(t)
	c := e.seedAndLogin(t, "mebalmoney", domain.RoleUser)
	uid := e.userIDByName(t, "mebalmoney")
	// 发放已知额度，使余额非零、货币串可断言。
	if _, err := e.deps.Billing.Grant(t.Context(), uid, 1_234_567, 0, "测试发放", ""); err != nil {
		t.Fatalf("发放失败：%v", err)
	}

	resp, env := e.do(t, c, "GET", "/api/me/balance", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("查余额失败：%d %v", resp.StatusCode, env)
	}
	data := env["data"].(map[string]any)
	bal := int64(data["credit_balance"].(float64))
	if bal != 1_234_567 {
		t.Fatalf("credit_balance 应 1234567，实际 %d", bal)
	}
	if got, want := data["credit_balance_money"], meMoneyOf(bal); got != want {
		t.Errorf("credit_balance_money 应 %q，实际 %v", want, got)
	}
	used := int64(data["credit_used"].(float64))
	if got, want := data["credit_used_money"], meMoneyOf(used); got != want {
		t.Errorf("credit_used_money 应 %q，实际 %v", want, got)
	}
}

// TestMeUsageLogsListHasMoney 覆盖 /api/me/usage-logs 列表：每行扣费积分旁置货币串。
func TestMeUsageLogsListHasMoney(t *testing.T) {
	e := newTestEnv(t)
	c := e.seedAndLogin(t, "meulmoney", domain.RoleUser)
	uid := e.userIDByName(t, "meulmoney")
	e.seedUsageLog(t, store.UsageLog{
		RequestID: "meul-money-1", UserID: uid, APIKeyID: 1, ModelName: "glm-5",
		CreditsCharged: 1234,
	})

	resp, env := e.do(t, c, "GET", "/api/me/usage-logs", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("查用量日志失败：%d %v", resp.StatusCode, env)
	}
	items := pageItems(t, env)
	if len(items) == 0 {
		t.Fatal("应返回至少 1 条")
	}
	row := items[0].(map[string]any)
	charged := int64(row["credits_charged"].(float64))
	if charged != 1234 {
		t.Fatalf("credits_charged 应 1234，实际 %d", charged)
	}
	if got, want := row["credits_charged_money"], meMoneyOf(charged); got != want {
		t.Errorf("credits_charged_money 应 %q，实际 %v", want, got)
	}
}

// TestMeLedgerMergedHasMoney 覆盖 /api/me/ledger 合并视角：净额、变动后余额、
// 展开明细都旁置货币串。
func TestMeLedgerMergedHasMoney(t *testing.T) {
	e := newTestEnv(t)
	c := e.seedAndLogin(t, "meledmoney", domain.RoleUser)
	uid := e.userIDByName(t, "meledmoney")
	// 预扣 19202 + 结算退回 19046 = 净扣 156。
	seedCallLedger(t, e, uid, "req-money-1", 19202, 19046, 980_798)

	resp, env := e.do(t, c, "GET", "/api/me/ledger", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("查流水失败：%d %v", resp.StatusCode, env)
	}
	page := env["data"].(map[string]any)
	row := page["items"].([]any)[0].(map[string]any)
	amount := int64(row["amount"].(float64)) // -156
	if got, want := row["amount_money"], meMoneyOf(amount); got != want {
		t.Errorf("amount_money 应 %q，实际 %v", want, got)
	}
	bal := int64(row["balance_after"].(float64))
	if got, want := row["balance_after_money"], meMoneyOf(bal); got != want {
		t.Errorf("balance_after_money 应 %q，实际 %v", want, got)
	}
	entries := row["entries"].([]any)
	if len(entries) == 0 {
		t.Fatal("展开明细不应为空")
	}
	e0 := entries[0].(map[string]any)
	eAmt := int64(e0["amount"].(float64))
	if got, want := e0["amount_money"], meMoneyOf(eAmt); got != want {
		t.Errorf("明细 amount_money 应 %q，实际 %v", want, got)
	}
}

// TestMeKeysHaveMoney 覆盖 /api/me/keys：额度、已用、每日上限旁置货币串，
// 列表与单条详情一致。
func TestMeKeysHaveMoney(t *testing.T) {
	e := newTestEnv(t)
	c := e.seedAndLogin(t, "mekeymoney", domain.RoleUser)
	limit := domain.Credits(2_000_000)
	resp, env := e.do(t, c, "POST", "/api/me/keys/", map[string]any{
		"name": "moneykey", "credit_limit": limit,
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("建密钥失败：%d %v", resp.StatusCode, env)
	}
	keyID := int64(env["data"].(map[string]any)["id"].(float64))

	// 单条详情带货币串。
	resp, env = e.do(t, c, "GET", fmt.Sprintf("/api/me/keys/%d", keyID), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("查密钥失败：%d %v", resp.StatusCode, env)
	}
	k := env["data"].(map[string]any)
	lm := int64(k["credit_limit"].(float64))
	if got, want := k["credit_limit_money"], meMoneyOf(lm); got != want {
		t.Errorf("credit_limit_money 应 %q，实际 %v", want, got)
	}
	used := int64(k["credit_used"].(float64))
	if got, want := k["credit_used_money"], meMoneyOf(used); got != want {
		t.Errorf("credit_used_money 应 %q，实际 %v", want, got)
	}
	dsl := int64(k["daily_spend_limit"].(float64))
	if got, want := k["daily_spend_limit_money"], meMoneyOf(dsl); got != want {
		t.Errorf("daily_spend_limit_money 应 %q，实际 %v", want, got)
	}

	// 列表项同样带货币串。
	resp, env = e.do(t, c, "GET", "/api/me/keys/", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("查密钥列表失败：%d %v", resp.StatusCode, env)
	}
	items := env["data"].(map[string]any)["items"].([]any)
	if len(items) == 0 {
		t.Fatal("列表应含已创建的密钥")
	}
	lk := items[0].(map[string]any)
	if _, ok := lk["credit_limit_money"]; !ok {
		t.Errorf("列表项应含 credit_limit_money：%v", lk)
	}
}
