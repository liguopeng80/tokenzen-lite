package api

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// 覆盖 P2-12 补充用例：跨用户 api_key_id 组合不泄露、常规筛选、
// 汇总隔离与密钥名分组、管理端全站按日聚合、兑换码筛选与兑换闭环、
// 模型路径参数非法 400。

// TestMeUsageLogsCrossUserKeyFilter 覆盖 I2：用户 A 携带 B 的 api_key_id
// 查询自己的用量日志，强制 user_id 与跨用户密钥组合必须返回空集。
func TestMeUsageLogsCrossUserKeyFilter(t *testing.T) {
	e := newTestEnv(t)
	userAC := e.seedAndLogin(t, "xkalice", domain.RoleUser)
	e.seedAndLogin(t, "xkbob", domain.RoleUser)
	idA := e.userIDByName(t, "xkalice")
	idB := e.userIDByName(t, "xkbob")

	e.seedUsageLog(t, store.UsageLog{
		RequestID: "xk-a-1", UserID: idA, APIKeyID: 101, ModelName: "glm-5",
	})
	e.seedUsageLog(t, store.UsageLog{
		RequestID: "xk-b-1", UserID: idB, APIKeyID: 202, ModelName: "glm-5",
	})

	resp, env := e.do(t, userAC, "GET", "/api/me/usage-logs?api_key_id=202", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("跨用户 api_key_id 查询应 200，实际 %d %v", resp.StatusCode, env)
	}
	if items := pageItems(t, env); len(items) != 0 {
		t.Errorf("A 携带 B 的 api_key_id 应返回空集，实际 %d 条: %v", len(items), items)
	}
}

// TestMeUsageLogsFilters 覆盖 I4：model/status/request_id/时间窗筛选
// 与分页信封字段。
func TestMeUsageLogsFilters(t *testing.T) {
	e := newTestEnv(t)
	userC := e.seedAndLogin(t, "filteruser", domain.RoleUser)
	uid := e.userIDByName(t, "filteruser")

	now := time.Now()
	e.seedUsageLog(t, store.UsageLog{
		RequestID: "flt-alpha", UserID: uid, APIKeyID: 1, ModelName: "m-alpha",
		CreatedAt: now.Add(-time.Hour),
	})
	e.seedUsageLog(t, store.UsageLog{
		RequestID: "flt-beta", UserID: uid, APIKeyID: 1, ModelName: "m-beta",
		Status: domain.UsageRefunded, CreatedAt: now.Add(-48 * time.Hour),
	})

	assertOne := func(t *testing.T, query, wantReqID string) map[string]any {
		t.Helper()
		resp, env := e.do(t, userC, "GET", "/api/me/usage-logs"+query, nil)
		if resp.StatusCode != 200 {
			t.Fatalf("筛选 %q 应 200，实际 %d %v", query, resp.StatusCode, env)
		}
		page := env["data"].(map[string]any)
		for _, field := range []string{"page", "page_size", "total", "items"} {
			if _, ok := page[field]; !ok {
				t.Errorf("分页信封缺少字段 %s: %v", field, page)
			}
		}
		items := pageItems(t, env)
		if len(items) != 1 {
			t.Fatalf("筛选 %q 应命中 1 条，实际 %d: %v", query, len(items), items)
		}
		row := items[0].(map[string]any)
		if row["request_id"] != wantReqID {
			t.Errorf("筛选 %q 命中条目不符，期望 %s 实际 %v", query, wantReqID, row["request_id"])
		}
		return row
	}

	assertOne(t, "?model=m-alpha", "flt-alpha")
	assertOne(t, "?status=refunded", "flt-beta")
	assertOne(t, "?request_id=flt-alpha", "flt-alpha")
	// 时间窗：只覆盖最近 2 小时，排除 48 小时前的那条
	assertOne(t, fmt.Sprintf("?start_timestamp=%d&end_timestamp=%d",
		now.Add(-2*time.Hour).Unix(), now.Unix()), "flt-alpha")
}

// TestMeUsageSummaryKeyNameAndIsolation 覆盖 I6/I8：按密钥分组时
// 存在的密钥显示名称（LEFT JOIN api_keys），且汇总不包含他人用量。
func TestMeUsageSummaryKeyNameAndIsolation(t *testing.T) {
	e := newTestEnv(t)
	userAC := e.seedAndLogin(t, "sumiso-a", domain.RoleUser)
	e.seedAndLogin(t, "sumiso-b", domain.RoleUser)
	idA := e.userIDByName(t, "sumiso-a")
	idB := e.userIDByName(t, "sumiso-b")

	// A 的真实密钥记录：分组键应显示密钥名
	key := store.APIKey{
		UserID: idA, Name: "生产密钥", KeyHash: "hash-sumiso-a",
		KeyPrefix: "tzk-aaa", Status: domain.KeyEnabled,
	}
	if err := e.db.Create(&key).Error; err != nil {
		t.Fatalf("种入密钥失败: %v", err)
	}

	e.seedUsageLog(t, store.UsageLog{
		RequestID: "si-a-1", UserID: idA, APIKeyID: key.ID, ModelName: "glm-5",
		PromptTokens: 10, CompletionTokens: 5, CreditsCharged: 300,
	})
	// 已不存在的密钥：回落为「密钥 #id」
	e.seedUsageLog(t, store.UsageLog{
		RequestID: "si-a-2", UserID: idA, APIKeyID: 90001, ModelName: "glm-5",
		CreditsCharged: 200,
	})
	// B 的大额用量：不得混入 A 的汇总
	e.seedUsageLog(t, store.UsageLog{
		RequestID: "si-b-1", UserID: idB, APIKeyID: key.ID, ModelName: "glm-5",
		CreditsCharged: 99999,
	})

	resp, env := e.do(t, userAC, "GET", "/api/me/usage-summary?group_by=key", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("按密钥汇总应 200，实际 %d %v", resp.StatusCode, env)
	}
	rows := env["data"].([]any)
	if len(rows) != 2 {
		t.Fatalf("A 按密钥汇总应 2 行，实际 %d: %v", len(rows), rows)
	}
	var totalCharged int64
	keys := map[string]bool{}
	for _, r := range rows {
		row := r.(map[string]any)
		keys[row["group_key"].(string)] = true
		totalCharged += int64(row["credits_charged"].(float64))
	}
	if !keys["生产密钥"] {
		t.Errorf("存在的密钥应按名称分组，实际分组键: %v", keys)
	}
	if !keys["密钥 #90001"] {
		t.Errorf("已删除密钥应回落为「密钥 #id」，实际分组键: %v", keys)
	}
	if totalCharged != 500 {
		t.Errorf("A 的汇总应只含自己的 500 积分（不含 B 的 99999），实际 %d", totalCharged)
	}
}

// TestAdminUsageDailyAggregatesAllUsers 覆盖 I10：管理端按日用量为
// 全站聚合，同日多用户求和正确；非 settled 不计入。
func TestAdminUsageDailyAggregatesAllUsers(t *testing.T) {
	e := newTestEnv(t)
	rootC := e.seedAndLogin(t, "aggroot", domain.RoleRoot)
	e.seedAndLogin(t, "agg-a", domain.RoleUser)
	e.seedAndLogin(t, "agg-b", domain.RoleUser)
	idA := e.userIDByName(t, "agg-a")
	idB := e.userIDByName(t, "agg-b")

	created := time.Now().Add(-time.Hour)
	e.seedUsageLog(t, store.UsageLog{
		RequestID: "agg-a-1", UserID: idA, APIKeyID: 1, ModelName: "glm-5",
		CreditsCharged: 100, PromptTokens: 10, CreatedAt: created,
	})
	e.seedUsageLog(t, store.UsageLog{
		RequestID: "agg-b-1", UserID: idB, APIKeyID: 2, ModelName: "glm-5",
		CreditsCharged: 250, PromptTokens: 20, CreatedAt: created,
	})
	e.seedUsageLog(t, store.UsageLog{
		RequestID: "agg-failed", UserID: idB, APIKeyID: 2, ModelName: "glm-5",
		CreditsCharged: 999, Status: domain.UsageFailed, CreatedAt: created,
	})

	resp, env := e.do(t, rootC, "GET", "/api/admin/stats/usage-daily", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("管理端按日用量应 200，实际 %d %v", resp.StatusCode, env)
	}
	rows := env["data"].([]any)
	if len(rows) != 1 {
		t.Fatalf("全站按日聚合应 1 行，实际 %d: %v", len(rows), rows)
	}
	row := rows[0].(map[string]any)
	if int64(row["requests"].(float64)) != 2 ||
		int64(row["credits_charged"].(float64)) != 350 {
		t.Errorf("全站同日聚合应为 A+B 求和（2 请求 / 350 积分，failed 不计入），实际: %v", row)
	}
}

// TestAdminRedemptionListFilters 覆盖 I14 的 batch_id 与 keyword 筛选。
func TestAdminRedemptionListFilters(t *testing.T) {
	e := newTestEnv(t)
	rootC := e.seedAndLogin(t, "redfilter", domain.RoleRoot)

	resp, env := e.do(t, rootC, "POST", "/api/admin/redemptions/batch",
		map[string]any{"count": 2, "credits": 500, "name": "春季促销"})
	if resp.StatusCode != 201 {
		t.Fatalf("生成批次一应 201，实际 %d %v", resp.StatusCode, env)
	}
	batch1 := env["data"].(map[string]any)["batch_id"].(string)
	resp, env = e.do(t, rootC, "POST", "/api/admin/redemptions/batch",
		map[string]any{"count": 3, "credits": 500, "name": "内部测试"})
	if resp.StatusCode != 201 {
		t.Fatalf("生成批次二应 201，实际 %d %v", resp.StatusCode, env)
	}
	// batch_id 为秒级时间戳，同一秒内生成的两个批次会同号；
	// 测试目标是筛选逻辑本身，直接把批次二改成可区分的编号。
	if err := e.db.Model(&store.Redemption{}).Where("name = ?", "内部测试").
		Update("batch_id", "batch-two").Error; err != nil {
		t.Fatalf("改写批次二编号失败: %v", err)
	}

	assertTotal := func(t *testing.T, query string, want int) {
		t.Helper()
		resp, env := e.do(t, rootC, "GET", "/api/admin/redemptions/"+query, nil)
		if resp.StatusCode != 200 {
			t.Fatalf("筛选 %q 应 200，实际 %d %v", query, resp.StatusCode, env)
		}
		if total := int(env["data"].(map[string]any)["total"].(float64)); total != want {
			t.Errorf("筛选 %q 的 total 应为 %d，实际 %d", query, want, total)
		}
	}
	assertTotal(t, "?batch_id="+batch1, 2)
	assertTotal(t, "?keyword=%E6%98%A5%E5%AD%A3", 2) // "春季"
	assertTotal(t, "?status=unused", 5)
}

// TestRedemptionVoidAndRedeemLoop 覆盖 I15/I16：作废后兑换失败且
// 余额流水无变化，恢复后兑换成功且余额与流水入账。
func TestRedemptionVoidAndRedeemLoop(t *testing.T) {
	e := newTestEnv(t)
	rootC := e.seedAndLogin(t, "voidroot", domain.RoleRoot)
	userC := e.seedAndLogin(t, "voiduser", domain.RoleUser)
	uid := e.userIDByName(t, "voiduser")

	resp, env := e.do(t, rootC, "POST", "/api/admin/redemptions/batch",
		map[string]any{"count": 1, "credits": 5000, "name": "作废闭环"})
	if resp.StatusCode != 201 {
		t.Fatalf("生成兑换码应 201，实际 %d %v", resp.StatusCode, env)
	}
	code := env["data"].(map[string]any)["codes"].([]any)[0].(string)

	balanceOf := func(t *testing.T) int64 {
		t.Helper()
		var u store.User
		if err := e.db.First(&u, uid).Error; err != nil {
			t.Fatalf("查余额失败: %v", err)
		}
		return int64(u.CreditBalance)
	}
	ledgerCount := func(t *testing.T) int64 {
		t.Helper()
		var n int64
		e.db.Model(&store.LedgerEntry{}).Where("user_id = ?", uid).Count(&n)
		return n
	}

	// 作废后兑换：400，余额与流水均无变化
	resp, _ = e.do(t, rootC, "PUT", "/api/admin/redemptions/1/status",
		map[string]string{"status": "disabled"})
	if resp.StatusCode != 200 {
		t.Fatalf("作废兑换码应 200，实际 %d", resp.StatusCode)
	}
	balBefore, ledBefore := balanceOf(t), ledgerCount(t)
	resp, env = e.do(t, userC, "POST", "/api/me/redeem", map[string]string{"code": code})
	if resp.StatusCode != 400 {
		t.Fatalf("兑换已作废的码应 400，实际 %d %v", resp.StatusCode, env)
	}
	// 提示须指向作废这一具体原因：员工据此知道要找管理员，而不是反复重试。
	if msg, _ := env["message"].(string); !strings.Contains(msg, "已被作废") {
		t.Errorf("作废码兑换的提示不符: %v", msg)
	}
	if balanceOf(t) != balBefore || ledgerCount(t) != ledBefore {
		t.Errorf("作废码兑换失败后余额或流水发生了变化")
	}

	// 恢复为 unused 后兑换：成功，余额增加且流水入账
	resp, _ = e.do(t, rootC, "PUT", "/api/admin/redemptions/1/status",
		map[string]string{"status": "unused"})
	if resp.StatusCode != 200 {
		t.Fatalf("恢复兑换码应 200，实际 %d", resp.StatusCode)
	}
	resp, env = e.do(t, userC, "POST", "/api/me/redeem", map[string]string{"code": code})
	if resp.StatusCode != 200 {
		t.Fatalf("恢复后兑换应成功，实际 %d %v", resp.StatusCode, env)
	}
	if got := balanceOf(t); got != balBefore+5000 {
		t.Errorf("兑换后余额应增加 5000，实际 %d（原 %d）", got, balBefore)
	}
	if got := ledgerCount(t); got != ledBefore+1 {
		t.Errorf("兑换后应新增 1 条流水，实际条数 %d（原 %d）", got, ledBefore)
	}

	// 已使用的码不可再兑换
	resp, _ = e.do(t, userC, "POST", "/api/me/redeem", map[string]string{"code": code})
	if resp.StatusCode != 400 {
		t.Errorf("重复兑换已使用的码应 400，实际 %d", resp.StatusCode)
	}
}

// TestAdminModelInvalidIDParam 覆盖 I19：模型路径参数非法时统一 400。
func TestAdminModelInvalidIDParam(t *testing.T) {
	e := newTestEnv(t)
	adminC := e.seedAndLogin(t, "badidadmin", domain.RoleAdmin)

	cases := []struct {
		name   string
		method string
		path   string
		body   any
	}{
		{"GET 非数字 id", "GET", "/api/admin/models/abc", nil},
		{"PUT id 为 0", "PUT", "/api/admin/models/0", map[string]any{"name": "x"}},
		{"DELETE 负数 id", "DELETE", "/api/admin/models/-1", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, env := e.do(t, adminC, tc.method, tc.path, tc.body)
			if resp.StatusCode != 400 {
				t.Errorf("期望 400，实际 %d %v", resp.StatusCode, env)
			}
			if msg, _ := env["message"].(string); !strings.Contains(msg, "路径参数 id 不合法") {
				t.Errorf("提示信息不符: %v", msg)
			}
		})
	}
}
