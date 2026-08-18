package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// 利润报表与 usage_logs 对账：charged/cost/margin 与明细逐条累加一致。
func TestProfitReportReconciliation(t *testing.T) {
	e := newTestEnv(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"id":"x","model":"m","choices":[],
			"usage":{"prompt_tokens":1000,"completion_tokens":500}}`)
	}))
	defer upstream.Close()

	userID, key := e.seedRelayUser(t, "profit-user", 1_000_000, nil)
	e.seedModel(t, "glm-5")
	chID := e.seedChannel(t, "profit-ch", upstream.URL, 0, []string{"glm-5"}, nil)
	// 成本价：input 0.5 积分/token、output 1 积分/token → 单次成本 1000×0.5+500×1 = 1000
	e.db.Create(&store.ChannelCost{
		ChannelID: chID, ModelName: "glm-5", Currency: "credits",
		InputCost: 500_000, OutputCost: 1_000_000,
	})

	// 打三次请求：每次 charged 2000、cost 1000
	for i := 0; i < 3; i++ {
		resp, body := e.relayPost(t, key, map[string]any{
			"model": "glm-5", "messages": []map[string]any{{"role": "user", "content": "hi"}},
		})
		if resp.StatusCode != 200 {
			t.Fatalf("第 %d 次请求失败: %d %s", i+1, resp.StatusCode, body)
		}
	}
	// 等待异步日志落库
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		var n int64
		e.db.Model(&store.UsageLog{}).Where("user_id = ?", userID).Count(&n)
		if n == 3 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	rootC := e.seedAndLogin(t, "statroot", domain.RoleRoot)
	resp, env := e.do(t, rootC, "GET", "/api/admin/stats/profit?group_by=channel", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("利润报表失败: %d %v", resp.StatusCode, env)
	}
	rows := env["data"].([]any)
	if len(rows) != 1 {
		t.Fatalf("应有 1 行渠道利润，实际 %d", len(rows))
	}
	row := rows[0].(map[string]any)
	if row["group_key"] != "profit-ch" {
		t.Errorf("分组键应为渠道名: %v", row)
	}
	if int64(row["credits_charged"].(float64)) != 6000 ||
		int64(row["credits_cost"].(float64)) != 3000 ||
		int64(row["margin"].(float64)) != 3000 {
		t.Errorf("利润数字与明细不一致: %v", row)
	}

	// 与 usage_logs 明细核对
	var sum struct{ Charged, Cost int64 }
	e.db.Model(&store.UsageLog{}).Where("user_id = ? AND status = 'settled'", userID).
		Select("COALESCE(SUM(credits_charged),0) AS charged, COALESCE(SUM(credits_cost),0) AS cost").
		Scan(&sum)
	if sum.Charged != 6000 || sum.Cost != 3000 {
		t.Errorf("usage_logs 累加不符: %+v", sum)
	}

	// overview 与 user summary 可用性
	resp, _ = e.do(t, rootC, "GET", "/api/admin/stats/overview", nil)
	if resp.StatusCode != 200 {
		t.Errorf("overview 失败: %d", resp.StatusCode)
	}
	resp, env = e.do(t, rootC, "GET", "/api/admin/stats/usage-daily?days=7", nil)
	if resp.StatusCode != 200 {
		t.Errorf("usage-daily 失败: %d", resp.StatusCode)
	}
	e.assertReconcile(t)
}

// TestProfitHonorsFromToWindow 是「profit 端点丢失 from/to 契约」缺陷的回归守卫。
//
// 缺陷根因：profit 端点的文档契约与前端 wrapper 均用 from/to（Unix 秒），曾误改为
// 走 resolveDayRange（读 start_timestamp/end_timestamp），导致显式 from/to 被静默忽略，
// 调用方只能拿到默认 30 天窗口。
//
// 本用例把日志种入一个远早于「现在」的固定锚点（默认窗口覆盖不到），再用显式 from/to
// 圈中锚点查询：契约正确时返回种入数据，回归（from/to 被忽略、回落默认窗口）时返回空。
func TestProfitHonorsFromToWindow(t *testing.T) {
	e := newTestEnv(t)
	rootC := e.seedAndLogin(t, "profittoroot", domain.RoleRoot)
	uid := e.userIDByName(t, "profittoroot")

	// 锚点定在远早于现在的固定日期，确保默认 30 天窗口不会覆盖到。
	anchor := time.Date(2025, 1, 15, 12, 0, 0, 0, time.Local)
	e.seedUsageLog(t, store.UsageLog{
		RequestID: "profit-win-in", UserID: uid, APIKeyID: 1,
		ModelName: "glm-5", CreditsCharged: 100, CreatedAt: anchor,
	})
	// 窗口外（锚点前 10 天）：契约正确时不应被计入。
	e.seedUsageLog(t, store.UsageLog{
		RequestID: "profit-win-out", UserID: uid, APIKeyID: 1,
		ModelName: "glm-5", CreditsCharged: 100, CreatedAt: anchor.AddDate(0, 0, -10),
	})

	// 显式 from/to 圈中 anchor 当日（±1 天）。
	from := anchor.AddDate(0, 0, -1).Unix()
	to := anchor.AddDate(0, 0, 1).Unix()
	resp, env := e.do(t, rootC, "GET",
		fmt.Sprintf("/api/admin/stats/profit?group_by=model&from=%d&to=%d", from, to), nil)
	if resp.StatusCode != 200 {
		t.Fatalf("利润报表应 200，实际 %d %v", resp.StatusCode, env)
	}
	rows := env["data"].([]any)
	if len(rows) != 1 {
		t.Fatalf("显式 from/to 窗口应只返回 1 个模型分组，实际 %d——疑似 from/to 被忽略", len(rows))
	}
	row := rows[0].(map[string]any)
	if int64(row["requests"].(float64)) != 1 {
		t.Errorf("窗口内应只计 1 条（窗口外的 profit-win-out 不应计入），实际 %v——疑似 from/to 被忽略",
			row["requests"])
	}
}
