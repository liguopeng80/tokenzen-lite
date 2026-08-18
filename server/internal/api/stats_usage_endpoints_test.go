package api

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// 覆盖 P2-12：用量日志与统计查询端点的用户数据隔离、分组取值回落、
// 天数边界、日志详情 400/404、兑换码列表与作废、模型增删改 404 分支。

// seedUsageLog 直接入库一条用量日志（绕过中继，专供查询端点测试）。
func (e *testEnv) seedUsageLog(t *testing.T, l store.UsageLog) {
	t.Helper()
	if l.Status == "" {
		l.Status = domain.UsageSettled
	}
	if err := e.db.Create(&l).Error; err != nil {
		t.Fatalf("种入用量日志失败: %v", err)
	}
}

// userIDByName 按用户名查用户 ID。
func (e *testEnv) userIDByName(t *testing.T, username string) int64 {
	t.Helper()
	var u store.User
	if err := e.db.Where("username = ?", username).First(&u).Error; err != nil {
		t.Fatalf("查用户 %s 失败: %v", username, err)
	}
	return u.ID
}

// pageItems 从响应信封中取分页 items。
func pageItems(t *testing.T, env map[string]any) []any {
	t.Helper()
	page, ok := env["data"].(map[string]any)
	if !ok {
		t.Fatalf("响应 data 不是分页信封: %v", env)
	}
	items, ok := page["items"].([]any)
	if !ok {
		t.Fatalf("分页信封缺少 items: %v", page)
	}
	return items
}

// TestMeUsageLogsUserIsolation 是 P2-12 的核心隔离用例：
// 用户 A 在自己的用量日志接口上携带用户 B 的 user_id 参数，
// 返回条目必须全部属于 A；user_id 筛选仅在管理端视角生效。
func TestMeUsageLogsUserIsolation(t *testing.T) {
	e := newTestEnv(t)
	userAC := e.seedAndLogin(t, "isoalice", domain.RoleUser)
	e.seedAndLogin(t, "isobob", domain.RoleUser)
	idA := e.userIDByName(t, "isoalice")
	idB := e.userIDByName(t, "isobob")

	for i := 0; i < 2; i++ {
		e.seedUsageLog(t, store.UsageLog{
			RequestID: fmt.Sprintf("iso-a-%d", i), UserID: idA, APIKeyID: 1,
			ModelName: "glm-5", CreditsCharged: 100,
		})
	}
	for i := 0; i < 3; i++ {
		e.seedUsageLog(t, store.UsageLog{
			RequestID: fmt.Sprintf("iso-b-%d", i), UserID: idB, APIKeyID: 2,
			ModelName: "glm-5", CreditsCharged: 999,
		})
	}

	// A 携带 B 的 user_id，返回条目必须全部属于 A
	resp, env := e.do(t, userAC, "GET",
		fmt.Sprintf("/api/me/usage-logs?user_id=%d", idB), nil)
	if resp.StatusCode != 200 {
		t.Fatalf("用户查询自身用量日志应 200，实际 %d %v", resp.StatusCode, env)
	}
	items := pageItems(t, env)
	if len(items) != 2 {
		t.Fatalf("A 应只看到自己的 2 条日志，实际 %d 条", len(items))
	}
	for _, it := range items {
		row := it.(map[string]any)
		if int64(row["user_id"].(float64)) != idA {
			t.Errorf("携带他人 user_id 时返回了他人日志: %v", row)
		}
		if !strings.HasPrefix(row["request_id"].(string), "iso-a-") {
			t.Errorf("返回条目不属于 A: %v", row)
		}
	}

	// 不带参数的基线：与携带他人 user_id 的结果一致
	resp, env = e.do(t, userAC, "GET", "/api/me/usage-logs", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("不带参数查询应 200，实际 %d", resp.StatusCode)
	}
	if got := len(pageItems(t, env)); got != 2 {
		t.Errorf("A 不带参数应看到 2 条，实际 %d", got)
	}

	// 管理端视角：user_id 筛选生效，可查到 B 的 3 条
	rootC := e.seedAndLogin(t, "isoroot", domain.RoleRoot)
	resp, env = e.do(t, rootC, "GET",
		fmt.Sprintf("/api/admin/usage-logs?user_id=%d", idB), nil)
	if resp.StatusCode != 200 {
		t.Fatalf("管理端按用户筛选应 200，实际 %d %v", resp.StatusCode, env)
	}
	items = pageItems(t, env)
	if len(items) != 3 {
		t.Fatalf("管理端筛选 B 应得 3 条，实际 %d", len(items))
	}
	for _, it := range items {
		if uid := int64(it.(map[string]any)["user_id"].(float64)); uid != idB {
			t.Errorf("管理端按 B 筛选返回了他人日志 user_id=%d", uid)
		}
	}
}

// TestMeUsageSummaryGroupBy 覆盖用量汇总的分组取值与非法值回落：
// model/key 按对应键分组，day 与任意非法取值回落到按日分组；仅统计 settled。
func TestMeUsageSummaryGroupBy(t *testing.T) {
	e := newTestEnv(t)
	userC := e.seedAndLogin(t, "sumuser", domain.RoleUser)
	uid := e.userIDByName(t, "sumuser")

	e.seedUsageLog(t, store.UsageLog{
		RequestID: "sum-1", UserID: uid, APIKeyID: 41, ModelName: "m-alpha",
		PromptTokens: 10, CompletionTokens: 5, CreditsCharged: 100,
	})
	e.seedUsageLog(t, store.UsageLog{
		RequestID: "sum-2", UserID: uid, APIKeyID: 41, ModelName: "m-alpha",
		PromptTokens: 10, CompletionTokens: 5, CreditsCharged: 100,
	})
	e.seedUsageLog(t, store.UsageLog{
		RequestID: "sum-3", UserID: uid, APIKeyID: 42, ModelName: "m-beta",
		PromptTokens: 20, CompletionTokens: 10, CreditsCharged: 50,
	})
	// 非 settled 的日志不计入汇总
	e.seedUsageLog(t, store.UsageLog{
		RequestID: "sum-refunded", UserID: uid, APIKeyID: 41, ModelName: "m-alpha",
		CreditsCharged: 777, Status: domain.UsageRefunded,
	})

	summaryRows := func(t *testing.T, query string) []map[string]any {
		t.Helper()
		resp, env := e.do(t, userC, "GET", "/api/me/usage-summary"+query, nil)
		if resp.StatusCode != 200 {
			t.Fatalf("汇总查询 %q 应 200，实际 %d %v", query, resp.StatusCode, env)
		}
		raw, ok := env["data"].([]any)
		if !ok {
			t.Fatalf("汇总响应 data 应为数组: %v", env)
		}
		rows := make([]map[string]any, 0, len(raw))
		for _, r := range raw {
			rows = append(rows, r.(map[string]any))
		}
		return rows
	}

	// group_by=model：按模型名分组，数字与明细一致
	rows := summaryRows(t, "?group_by=model")
	if len(rows) != 2 {
		t.Fatalf("按模型分组应 2 行，实际 %d: %v", len(rows), rows)
	}
	byKey := map[string]map[string]any{}
	for _, r := range rows {
		byKey[r["group_key"].(string)] = r
	}
	alpha := byKey["m-alpha"]
	if alpha == nil {
		t.Fatalf("缺少 m-alpha 分组: %v", rows)
	}
	if int64(alpha["requests"].(float64)) != 2 ||
		int64(alpha["credits_charged"].(float64)) != 200 ||
		int64(alpha["total_tokens"].(float64)) != 30 {
		t.Errorf("m-alpha 汇总数字不符（refunded 不应计入）: %v", alpha)
	}
	if byKey["m-beta"] == nil {
		t.Errorf("缺少 m-beta 分组: %v", rows)
	}

	// group_by=key：按密钥分组（无密钥记录时回落为「密钥 #id」）
	rows = summaryRows(t, "?group_by=key")
	if len(rows) != 2 {
		t.Fatalf("按密钥分组应 2 行，实际 %d: %v", len(rows), rows)
	}
	keys := map[string]bool{}
	for _, r := range rows {
		keys[r["group_key"].(string)] = true
	}
	if !keys["密钥 #41"] || !keys["密钥 #42"] {
		t.Errorf("按密钥分组键不符: %v", rows)
	}

	// 非法取值回落到按日分组，与不带参数一致
	dayPattern := regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	for _, query := range []string{"?group_by=bogus", ""} {
		rows = summaryRows(t, query)
		if len(rows) != 1 {
			t.Fatalf("查询 %q 应回落为按日分组（1 行），实际 %d: %v", query, len(rows), rows)
		}
		if !dayPattern.MatchString(rows[0]["group_key"].(string)) {
			t.Errorf("查询 %q 的分组键应为日期，实际: %v", query, rows[0]["group_key"])
		}
		if int64(rows[0]["requests"].(float64)) != 3 {
			t.Errorf("查询 %q 的按日请求数应为 3，实际: %v", query, rows[0])
		}
	}
}

// TestMeUsageDailyDaysBoundary 覆盖按日用量的天数边界：
// 非法与缺省回落 30 天，超出上限截断为 365 天。
func TestMeUsageDailyDaysBoundary(t *testing.T) {
	e := newTestEnv(t)
	userC := e.seedAndLogin(t, "dailyuser", domain.RoleUser)
	uid := e.userIDByName(t, "dailyuser")

	now := time.Now()
	e.seedUsageLog(t, store.UsageLog{
		RequestID: "daily-today", UserID: uid, APIKeyID: 1, ModelName: "glm-5",
		CreditsCharged: 100, CreatedAt: now.Add(-time.Hour),
	})
	e.seedUsageLog(t, store.UsageLog{
		RequestID: "daily-40d", UserID: uid, APIKeyID: 1, ModelName: "glm-5",
		CreditsCharged: 100, CreatedAt: now.AddDate(0, 0, -40),
	})
	e.seedUsageLog(t, store.UsageLog{
		RequestID: "daily-400d", UserID: uid, APIKeyID: 1, ModelName: "glm-5",
		CreditsCharged: 100, CreatedAt: now.AddDate(0, 0, -400),
	})

	cases := []struct {
		name  string
		query string
		want  int // 期望返回的天数行数
	}{
		{"7 天窗口只含今天", "?days=7", 1},
		{"60 天窗口含 40 天前", "?days=60", 2},
		{"缺省回落 30 天", "", 1},
		{"days=0 回落 30 天", "?days=0", 1},
		{"负数回落 30 天", "?days=-5", 1},
		{"非数字回落 30 天", "?days=abc", 1},
		{"超上限截断 365 天（400 天前不计入）", "?days=9999", 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, env := e.do(t, userC, "GET", "/api/me/usage-daily"+tc.query, nil)
			if resp.StatusCode != 200 {
				t.Fatalf("按日用量查询应 200，实际 %d %v", resp.StatusCode, env)
			}
			rows, ok := env["data"].([]any)
			if !ok {
				t.Fatalf("按日用量响应 data 应为数组: %v", env)
			}
			if len(rows) != tc.want {
				t.Errorf("期望 %d 行，实际 %d: %v", tc.want, len(rows), rows)
			}
		})
	}
}

// TestAdminUsageLogDetail 覆盖日志详情的 400/404 与正常直达。
func TestAdminUsageLogDetail(t *testing.T) {
	e := newTestEnv(t)
	rootC := e.seedAndLogin(t, "detailroot", domain.RoleRoot)
	uid := e.userIDByName(t, "detailroot")
	e.seedUsageLog(t, store.UsageLog{
		RequestID: "req-detail-1", UserID: uid, APIKeyID: 1,
		ModelName: "glm-5", CreditsCharged: 1234,
	})

	resp, env := e.do(t, rootC, "GET", "/api/admin/usage-logs/detail", nil)
	if resp.StatusCode != 400 {
		t.Errorf("缺少 request_id 应 400，实际 %d %v", resp.StatusCode, env)
	}
	resp, env = e.do(t, rootC, "GET", "/api/admin/usage-logs/detail?request_id=no-such", nil)
	if resp.StatusCode != 404 {
		t.Errorf("不存在的 request_id 应 404，实际 %d %v", resp.StatusCode, env)
	}
	resp, env = e.do(t, rootC, "GET", "/api/admin/usage-logs/detail?request_id=req-detail-1", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("详情直达应 200，实际 %d %v", resp.StatusCode, env)
	}
	detail := env["data"].(map[string]any)
	if detail["request_id"] != "req-detail-1" ||
		int64(detail["credits_charged"].(float64)) != 1234 {
		t.Errorf("详情内容不符: %v", detail)
	}
}

// TestAdminRedemptionListAndVoid 覆盖兑换码列表（明文不回显、筛选）与作废：
// 未用可禁用可恢复，已用不可改，非法状态 400，不存在 404。
func TestAdminRedemptionListAndVoid(t *testing.T) {
	e := newTestEnv(t)
	rootC := e.seedAndLogin(t, "redroot", domain.RoleRoot)

	resp, env := e.do(t, rootC, "POST", "/api/admin/redemptions/batch",
		map[string]any{"count": 3, "credits": 1000, "name": "测试批次"})
	if resp.StatusCode != 201 {
		t.Fatalf("批量生成兑换码应 201，实际 %d %v", resp.StatusCode, env)
	}
	data := env["data"].(map[string]any)
	codes := data["codes"].([]any)
	if len(codes) != 3 {
		t.Fatalf("应生成 3 个兑换码，实际 %d", len(codes))
	}

	// 列表：分页信封、总数、不回显明文
	resp, env = e.do(t, rootC, "GET", "/api/admin/redemptions/", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("兑换码列表应 200，实际 %d %v", resp.StatusCode, env)
	}
	page := env["data"].(map[string]any)
	if int(page["total"].(float64)) != 3 {
		t.Errorf("兑换码列表 total 应为 3，实际 %v", page["total"])
	}
	raw, _ := json.Marshal(page)
	for _, c := range codes {
		if strings.Contains(string(raw), c.(string)) {
			t.Error("兑换码列表不应回显明文兑换码")
		}
	}
	if strings.Contains(string(raw), "code_hash") {
		t.Error("兑换码列表不应包含 code_hash")
	}

	// 作废（id=1）与恢复
	resp, env = e.do(t, rootC, "PUT", "/api/admin/redemptions/1/status",
		map[string]string{"status": "disabled"})
	if resp.StatusCode != 200 {
		t.Fatalf("作废兑换码应 200，实际 %d %v", resp.StatusCode, env)
	}
	var r1 store.Redemption
	if err := e.db.First(&r1, 1).Error; err != nil || r1.Status != domain.RedemptionDisabled {
		t.Errorf("作废后状态应为 disabled，实际 %v (err=%v)", r1.Status, err)
	}
	resp, _ = e.do(t, rootC, "PUT", "/api/admin/redemptions/1/status",
		map[string]string{"status": "unused"})
	if resp.StatusCode != 200 {
		t.Errorf("恢复兑换码应 200，实际 %d", resp.StatusCode)
	}

	// 非法状态与不存在的兑换码
	resp, _ = e.do(t, rootC, "PUT", "/api/admin/redemptions/1/status",
		map[string]string{"status": "used"})
	if resp.StatusCode != 400 {
		t.Errorf("状态设为 used 应 400，实际 %d", resp.StatusCode)
	}
	resp, _ = e.do(t, rootC, "PUT", "/api/admin/redemptions/9999/status",
		map[string]string{"status": "disabled"})
	if resp.StatusCode != 404 {
		t.Errorf("不存在的兑换码应 404，实际 %d", resp.StatusCode)
	}

	// 已使用的兑换码不可作废
	e.db.Model(&store.Redemption{}).Where("id = ?", 2).
		Update("status", domain.RedemptionUsed)
	resp, _ = e.do(t, rootC, "PUT", "/api/admin/redemptions/2/status",
		map[string]string{"status": "disabled"})
	if resp.StatusCode != 404 {
		t.Errorf("作废已使用的兑换码应 404，实际 %d", resp.StatusCode)
	}

	// 按状态筛选：used 仅 1 条
	resp, env = e.do(t, rootC, "GET", "/api/admin/redemptions/?status=used", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("按状态筛选应 200，实际 %d", resp.StatusCode)
	}
	if total := int(env["data"].(map[string]any)["total"].(float64)); total != 1 {
		t.Errorf("按 used 筛选应 1 条，实际 %d", total)
	}
}

// TestAdminCalendar 覆盖管理端首页日历热力图端点 GET /admin/stats/calendar：
// 200 返回 DailyStat 列表（含货币串）、按日升序、默认 90 天窗口，且不暴露用户隔离。
// 该路径走 RollupRepo.Aggregate（按日维度），原始日志按保留期清理后结果仍保留。
func TestAdminCalendar(t *testing.T) {
	e := newTestEnv(t)
	rootC := e.seedAndLogin(t, "calroot", domain.RoleRoot)
	uid := e.userIDByName(t, "calroot")

	now := time.Now()
	e.seedUsageLog(t, store.UsageLog{
		RequestID: "cal-today", UserID: uid, APIKeyID: 1, ModelName: "glm-5",
		PromptTokens: 10, CompletionTokens: 5, CreditsCharged: 100, CreditsCost: 30,
		// 「今天」的种子直接取 now：若取 now-1h，用例在本地时间 00:00–01:00
		// 之间运行时两行种子会归入同一天，行数断言随之失败。
		CreatedAt: now,
	})
	e.seedUsageLog(t, store.UsageLog{
		RequestID: "cal-yesterday", UserID: uid, APIKeyID: 1, ModelName: "glm-5",
		PromptTokens: 20, CompletionTokens: 10, CreditsCharged: 50, CreditsCost: 15,
		CreatedAt: now.AddDate(0, 0, -1),
	})

	resp, env := e.do(t, rootC, "GET", "/api/admin/stats/calendar?days=30", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("日历端点应 200，实际 %d %v", resp.StatusCode, env)
	}
	rows, ok := env["data"].([]any)
	if !ok {
		t.Fatalf("日历响应 data 应为数组: %v", env)
	}
	if len(rows) != 2 {
		t.Fatalf("30 天窗口应含 2 行（今天 + 昨天），实际 %d: %v", len(rows), rows)
	}
	// 按日维度应为日期升序。
	prev := ""
	for _, r := range rows {
		row := r.(map[string]any)
		// 货币串旁置：管理端可见扣费金额。
		if _, present := row["credits_charged_money"]; !present {
			t.Errorf("日历行应旁置 credits_charged_money: %v", row)
		}
		day, _ := row["day"].(string)
		if day == "" {
			t.Errorf("日历行缺少 day: %v", row)
		}
		if prev != "" && day < prev {
			t.Errorf("日历行应升序，遇到 %s 在 %s 之后", day, prev)
		}
		prev = day
	}

	// 托管视角作用域过滤：本接入方只看到本接入方的消费行。
	tokenA, integA, _ := seedManagedToken(t, e, "cal-scope-a")
	_, integB, _ := seedManagedToken(t, e, "cal-scope-b")
	// 各自种入一条带作用域的用量日志。
	seedScopedUsageLog(t, e, uid, integA, "cal-scope-a-req")
	seedScopedUsageLog(t, e, uid, integB, "cal-scope-b-req")
	respA, envA := doWithToken(t, e, tokenA, "GET", "/api/admin/stats/calendar?days=30", nil)
	if respA.StatusCode != 200 {
		t.Fatalf("托管 A 令牌访问日历应 200，实际 %d %v", respA.StatusCode, envA)
	}
	rowsA := envA["data"].([]any)
	if len(rowsA) != 1 {
		t.Fatalf("托管 A 令牌应只看到本接入方 1 行（其他行属 B 与运营内部），实际 %d: %v", len(rowsA), rowsA)
	}
}

// TestAdminModelNotFoundBranches 覆盖模型增删改及关联端点对不存在模型的 404。
func TestAdminModelNotFoundBranches(t *testing.T) {
	e := newTestEnv(t)
	adminC := e.seedAndLogin(t, "modeladmin", domain.RoleAdmin)

	cases := []struct {
		name   string
		method string
		path   string
		body   any
	}{
		{"查看不存在模型", "GET", "/api/admin/models/9999", nil},
		{"更新不存在模型", "PUT", "/api/admin/models/9999",
			map[string]any{"name": "ghost-model"}},
		{"删除不存在模型", "DELETE", "/api/admin/models/9999", nil},
		{"为不存在模型设置价格", "PUT", "/api/admin/models/9999/price",
			map[string]any{"input_price": 1}},
		{"为不存在模型设置时段规则", "PUT", "/api/admin/models/9999/peak-rules",
			map[string]any{"rules": []any{}}},
		{"查看不存在模型的渠道成本", "GET", "/api/admin/models/9999/channel-costs", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, env := e.do(t, adminC, tc.method, tc.path, tc.body)
			if resp.StatusCode != 404 {
				t.Errorf("期望 404，实际 %d，响应: %v", resp.StatusCode, env)
			}
		})
	}
}

// seedAPIKey 直接入库一条密钥记录（绕过哈希与创建接口，专供维度测试）。
// newTestEnv 已 RESTART IDENTITY 清空 api_keys，首个种入的密钥 id 从 1 起。
func (e *testEnv) seedAPIKey(t *testing.T, userID int64, name string) int64 {
	t.Helper()
	k := &store.APIKey{
		UserID:    userID,
		Name:      name,
		KeyHash:   fmt.Sprintf("hash-%s-%d", name, time.Now().UnixNano()),
		KeyPrefix: "sk-test-",
		Status:    domain.KeyEnabled,
	}
	if err := e.db.Create(k).Error; err != nil {
		t.Fatalf("种入密钥失败: %v", err)
	}
	return k.ID
}

// TestMeUsageSummaryByKeyWithRange 覆盖按密钥维度 + 任意时间范围的用量汇总：
// 200、按密钥名成行、且不暴露采购成本（credits_cost/margin 不得出现在行内）。
// 该路径走 RollupRepo.Aggregate，原始日志按保留期清理后结果仍保留。
func TestMeUsageSummaryByKeyWithRange(t *testing.T) {
	e := newTestEnv(t)
	userC := e.seedAndLogin(t, "keysumuser", domain.RoleUser)
	uid := e.userIDByName(t, "keysumuser")
	keyA := e.seedAPIKey(t, uid, "alpha-key")
	keyB := e.seedAPIKey(t, uid, "beta-key")

	e.seedUsageLog(t, store.UsageLog{
		RequestID: "ks-1", UserID: uid, APIKeyID: keyA, ModelName: "glm-5",
		PromptTokens: 10, CompletionTokens: 5, CreditsCharged: 100,
	})
	e.seedUsageLog(t, store.UsageLog{
		RequestID: "ks-2", UserID: uid, APIKeyID: keyA, ModelName: "glm-5",
		PromptTokens: 10, CompletionTokens: 5, CreditsCharged: 100,
	})
	e.seedUsageLog(t, store.UsageLog{
		RequestID: "ks-3", UserID: uid, APIKeyID: keyB, ModelName: "glm-5",
		PromptTokens: 20, CompletionTokens: 10, CreditsCharged: 50,
	})

	// 任意时间范围：覆盖今天（start/end Unix 秒）。
	start := time.Now().AddDate(0, 0, -1).Unix()
	end := time.Now().AddDate(0, 0, 1).Unix()
	resp, env := e.do(t, userC, "GET",
		fmt.Sprintf("/api/me/usage-summary?group_by=key&start_timestamp=%d&end_timestamp=%d", start, end), nil)
	if resp.StatusCode != 200 {
		t.Fatalf("按密钥汇总应 200，实际 %d %v", resp.StatusCode, env)
	}
	rows, ok := env["data"].([]any)
	if !ok {
		t.Fatalf("汇总响应 data 应为数组: %v", env)
	}
	byKey := map[string]map[string]any{}
	for _, r := range rows {
		row := r.(map[string]any)
		byKey[row["group_key"].(string)] = row
		// 用户侧不得暴露采购成本与差额。
		if _, present := row["credits_cost"]; present {
			t.Errorf("用户侧汇总行不得包含 credits_cost: %v", row)
		}
		if _, present := row["margin"]; present {
			t.Errorf("用户侧汇总行不得包含 margin: %v", row)
		}
	}
	alpha, ok := byKey["alpha-key"]
	if !ok {
		t.Fatalf("缺少 alpha-key 分组: %v", byKey)
	}
	if int64(alpha["requests"].(float64)) != 2 ||
		int64(alpha["credits_charged"].(float64)) != 200 ||
		int64(alpha["total_tokens"].(float64)) != 30 {
		t.Errorf("alpha-key 汇总不符: %v", alpha)
	}
	if beta := byKey["beta-key"]; beta == nil ||
		int64(beta["requests"].(float64)) != 1 ||
		int64(beta["credits_charged"].(float64)) != 50 {
		t.Errorf("beta-key 汇总不符: %v", byKey["beta-key"])
	}
}

// TestMeCacheReportByModel 覆盖用户侧缓存分析报表：
// 200、整体汇总与按模型分组均含 cache_hit_rate、缓存 token，且不暴露采购成本与差额。
func TestMeCacheReportByModel(t *testing.T) {
	e := newTestEnv(t)
	userC := e.seedAndLogin(t, "cacheuser", domain.RoleUser)
	uid := e.userIDByName(t, "cacheuser")

	e.seedUsageLog(t, store.UsageLog{
		RequestID: "cache-1", UserID: uid, APIKeyID: 41, ModelName: "m-alpha",
		PromptTokens: 300, CompletionTokens: 10, CacheReadTokens: 700, CacheWriteTokens: 50,
		CreditsCharged: 100, CreditsCost: 40,
	})
	e.seedUsageLog(t, store.UsageLog{
		RequestID: "cache-2", UserID: uid, APIKeyID: 41, ModelName: "m-alpha",
		PromptTokens: 200, CompletionTokens: 5, CacheReadTokens: 800, CacheWriteTokens: 0,
		CreditsCharged: 80, CreditsCost: 30,
	})
	e.seedUsageLog(t, store.UsageLog{
		RequestID: "cache-3", UserID: uid, APIKeyID: 42, ModelName: "m-beta",
		PromptTokens: 500, CompletionTokens: 5, CacheReadTokens: 500, CacheWriteTokens: 20,
		CreditsCharged: 50, CreditsCost: 20,
	})
	// 非 settled 不计入。
	e.seedUsageLog(t, store.UsageLog{
		RequestID: "cache-failed", UserID: uid, APIKeyID: 41, ModelName: "m-alpha",
		PromptTokens: 999, CacheReadTokens: 999, CreditsCharged: 777,
		Status: domain.UsageFailed,
	})

	start := time.Now().AddDate(0, 0, -1).Unix()
	end := time.Now().AddDate(0, 0, 1).Unix()
	resp, env := e.do(t, userC, "GET",
		fmt.Sprintf("/api/me/cache-report?group_by=model&start_timestamp=%d&end_timestamp=%d", start, end), nil)
	if resp.StatusCode != 200 {
		t.Fatalf("缓存分析应 200，实际 %d %v", resp.StatusCode, env)
	}
	data, ok := env["data"].(map[string]any)
	if !ok {
		t.Fatalf("响应应为信封: %v", env)
	}
	// from/to 经 meUsageRange 按自然日对齐（SpendDay），与原始 Unix 时间戳不完全相等，
	// 只校验落在查询范围内且 from < to。
	fromTs := int64(data["from"].(float64))
	toTs := int64(data["to"].(float64))
	if fromTs <= 0 || toTs <= 0 || fromTs >= toTs {
		t.Errorf("from/to 应为合法区间: from=%d to=%d", fromTs, toTs)
	}
	if fromTs > start || toTs > end+24*3600 {
		t.Errorf("from/to 应落在查询窗口内（含日界对齐余量）: from=%d start=%d to=%d end=%d", fromTs, start, toTs, end)
	}
	overall, ok := data["overall"].(map[string]any)
	if !ok {
		t.Fatalf("缺少 overall: %v", data)
	}
	// 整体：缓存读 700+800+500=2000，prompt 300+200+500=1000，命中率 2000/(2000+1000)=2/3。
	if int64(overall["requests"].(float64)) != 3 {
		t.Errorf("整体请求数应为 3（failed 不计）: %v", overall)
	}
	if int64(overall["cache_read_tokens"].(float64)) != 2000 {
		t.Errorf("整体缓存读应为 2000: %v", overall)
	}
	if int64(overall["cache_write_tokens"].(float64)) != 70 {
		t.Errorf("整体缓存写应为 70: %v", overall)
	}
	if hit := overall["cache_hit_rate"].(float64); hit < 0.666 || hit > 0.667 {
		t.Errorf("整体命中率应约为 0.667（2/3）: %v", hit)
	}
	// 用户侧不得暴露采购成本与差额。
	for _, key := range []string{"credits_cost", "margin"} {
		if _, present := overall[key]; present {
			t.Errorf("overall 不得暴露 %s: %v", key, overall)
		}
	}

	groups, ok := data["groups"].([]any)
	if !ok {
		t.Fatalf("groups 应为数组: %v", data)
	}
	if len(groups) != 2 {
		t.Fatalf("按模型应 2 行，实际 %d: %v", len(groups), groups)
	}
	byKey := map[string]map[string]any{}
	for _, g := range groups {
		row := g.(map[string]any)
		byKey[row["group_key"].(string)] = row
		// 每行必含命中率字段。
		if _, ok := row["cache_hit_rate"].(float64); !ok {
			t.Errorf("分组行缺少 cache_hit_rate: %v", row)
		}
		// 用户侧分组行同样不得暴露成本与差额。
		for _, key := range []string{"credits_cost", "margin"} {
			if _, present := row[key]; present {
				t.Errorf("分组行不得暴露 %s: %v", key, row)
			}
		}
	}
	alpha := byKey["m-alpha"]
	if alpha == nil {
		t.Fatalf("缺少 m-alpha: %v", byKey)
	}
	// m-alpha：缓存读 1500，prompt 500，命中率 1500/2000=0.75。
	if int64(alpha["cache_read_tokens"].(float64)) != 1500 {
		t.Errorf("m-alpha 缓存读应为 1500: %v", alpha)
	}
	if hit := alpha["cache_hit_rate"].(float64); hit < 0.749 || hit > 0.751 {
		t.Errorf("m-alpha 命中率应约为 0.75: %v", hit)
	}
}

// TestMeCacheReportByDay 覆盖按日维度：行按日期升序，可用于时间轴作图。
func TestMeCacheReportByDay(t *testing.T) {
	e := newTestEnv(t)
	userC := e.seedAndLogin(t, "cacheday", domain.RoleUser)
	uid := e.userIDByName(t, "cacheday")
	now := time.Now()

	e.seedUsageLog(t, store.UsageLog{
		RequestID: "cd-1", UserID: uid, APIKeyID: 1, ModelName: "glm-5",
		PromptTokens: 100, CacheReadTokens: 300, CreditsCharged: 10,
		CreatedAt: now.AddDate(0, 0, -1),
	})
	e.seedUsageLog(t, store.UsageLog{
		RequestID: "cd-2", UserID: uid, APIKeyID: 1, ModelName: "glm-5",
		PromptTokens: 100, CacheReadTokens: 100, CreditsCharged: 10,
		CreatedAt: now,
	})

	resp, env := e.do(t, userC, "GET", "/api/me/cache-report?group_by=day&days=7", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("按日缓存分析应 200，实际 %d %v", resp.StatusCode, env)
	}
	data := env["data"].(map[string]any)
	groups := data["groups"].([]any)
	if len(groups) != 2 {
		t.Fatalf("应 2 行（昨天 + 今天），实际 %d", len(groups))
	}
	// 按日维度应为日期升序。
	prev := ""
	for _, g := range groups {
		key := g.(map[string]any)["group_key"].(string)
		if prev != "" && key < prev {
			t.Errorf("按日维度应升序，遇到 %s 在 %s 之后", prev, key)
		}
		prev = key
	}
}

// TestMeTokenReport 覆盖用户侧 token 结构报告：四类 billed token（输入/缓存命中读/缓存写入/输出）
// 的合计与按模型分组、不暴露采购成本与差额。
func TestMeTokenReport(t *testing.T) {
	e := newTestEnv(t)
	userC := e.seedAndLogin(t, "tokenuser", domain.RoleUser)
	uid := e.userIDByName(t, "tokenuser")

	e.seedUsageLog(t, store.UsageLog{
		RequestID: "tok-1", UserID: uid, APIKeyID: 51, ModelName: "m-alpha",
		PromptTokens: 300, CompletionTokens: 10, CacheReadTokens: 700, CacheWriteTokens: 50,
		CreditsCharged: 100, CreditsCost: 40,
	})
	e.seedUsageLog(t, store.UsageLog{
		RequestID: "tok-2", UserID: uid, APIKeyID: 51, ModelName: "m-alpha",
		PromptTokens: 200, CompletionTokens: 5, CacheReadTokens: 800, CacheWriteTokens: 0,
		CreditsCharged: 80, CreditsCost: 30,
	})
	e.seedUsageLog(t, store.UsageLog{
		RequestID: "tok-3", UserID: uid, APIKeyID: 52, ModelName: "m-beta",
		PromptTokens: 500, CompletionTokens: 5, CacheReadTokens: 500, CacheWriteTokens: 20,
		CreditsCharged: 50, CreditsCost: 20,
	})
	// 非 settled 不计入。
	e.seedUsageLog(t, store.UsageLog{
		RequestID: "tok-failed", UserID: uid, APIKeyID: 51, ModelName: "m-alpha",
		PromptTokens: 999, CompletionTokens: 999, CreditsCharged: 777,
		Status: domain.UsageFailed,
	})

	start := time.Now().AddDate(0, 0, -1).Unix()
	end := time.Now().AddDate(0, 0, 1).Unix()
	resp, env := e.do(t, userC, "GET",
		fmt.Sprintf("/api/me/token-report?group_by=model&start_timestamp=%d&end_timestamp=%d", start, end), nil)
	if resp.StatusCode != 200 {
		t.Fatalf("token 结构应 200，实际 %d %v", resp.StatusCode, env)
	}
	data, ok := env["data"].(map[string]any)
	if !ok {
		t.Fatalf("响应应为信封: %v", env)
	}
	overall, ok := data["overall"].(map[string]any)
	if !ok {
		t.Fatalf("缺少 overall: %v", data)
	}
	// 整体：prompt 1000、cache_read 2000、cache_write 70、completion 20，合计 3090。
	if int64(overall["prompt_tokens"].(float64)) != 1000 {
		t.Errorf("整体 prompt 应为 1000: %v", overall)
	}
	if int64(overall["cache_read_tokens"].(float64)) != 2000 {
		t.Errorf("整体 cache_read 应为 2000: %v", overall)
	}
	if int64(overall["cache_write_tokens"].(float64)) != 70 {
		t.Errorf("整体 cache_write 应为 70: %v", overall)
	}
	if int64(overall["completion_tokens"].(float64)) != 20 {
		t.Errorf("整体 completion 应为 20: %v", overall)
	}
	if int64(overall["total_tokens"].(float64)) != 3090 {
		t.Errorf("整体 total 应为 3090: %v", overall)
	}
	// 用户侧不得暴露采购成本与差额。
	for _, key := range []string{"credits_cost", "margin"} {
		if _, present := overall[key]; present {
			t.Errorf("overall 不得暴露 %s: %v", key, overall)
		}
	}

	groups, ok := data["groups"].([]any)
	if !ok {
		t.Fatalf("groups 应为数组: %v", data)
	}
	if len(groups) != 2 {
		t.Fatalf("按模型应 2 行，实际 %d: %v", len(groups), groups)
	}
	byKey := map[string]map[string]any{}
	for _, g := range groups {
		row := g.(map[string]any)
		byKey[row["group_key"].(string)] = row
		// 用户侧分组行同样不得暴露成本与差额。
		for _, key := range []string{"credits_cost", "margin"} {
			if _, present := row[key]; present {
				t.Errorf("分组行不得暴露 %s: %v", key, row)
			}
		}
		// total 应等于四类之和。
		sum := int64(row["prompt_tokens"].(float64)) +
			int64(row["cache_read_tokens"].(float64)) +
			int64(row["cache_write_tokens"].(float64)) +
			int64(row["completion_tokens"].(float64))
		if int64(row["total_tokens"].(float64)) != sum {
			t.Errorf("分组 total 应为四类之和 %d: %v", sum, row)
		}
	}
	alpha := byKey["m-alpha"]
	if alpha == nil {
		t.Fatalf("缺少 m-alpha: %v", byKey)
	}
	// m-alpha：prompt 500、cache_read 1500、cache_write 50、completion 15，合计 2065。
	if int64(alpha["total_tokens"].(float64)) != 2065 {
		t.Errorf("m-alpha total 应为 2065: %v", alpha)
	}
}

// TestAdminCostReportByKey 覆盖管理端费用报表的「按密钥」维度：200 且按密钥成行。
func TestAdminCostReportByKey(t *testing.T) {
	e := newTestEnv(t)
	rootC := e.seedAndLogin(t, "costroot", domain.RoleRoot)
	uid := e.userIDByName(t, "costroot")
	keyA := e.seedAPIKey(t, uid, "cost-key-a")
	keyB := e.seedAPIKey(t, uid, "cost-key-b")

	e.seedUsageLog(t, store.UsageLog{
		RequestID: "cr-1", UserID: uid, APIKeyID: keyA, ModelName: "glm-5",
		CreditsCharged: 100, CreditsCost: 40,
	})
	e.seedUsageLog(t, store.UsageLog{
		RequestID: "cr-2", UserID: uid, APIKeyID: keyB, ModelName: "glm-5",
		CreditsCharged: 80, CreditsCost: 30,
	})

	start := time.Now().AddDate(0, 0, -1).Unix()
	end := time.Now().AddDate(0, 0, 1).Unix()
	resp, env := e.do(t, rootC, "GET",
		fmt.Sprintf("/api/admin/stats/cost-report?group_by=key&start_timestamp=%d&end_timestamp=%d", start, end), nil)
	if resp.StatusCode != 200 {
		t.Fatalf("按密钥费用报表应 200，实际 %d %v", resp.StatusCode, env)
	}
	data, ok := env["data"].(map[string]any)
	if !ok {
		t.Fatalf("费用报表响应应为信封: %v", env)
	}
	if data["group_by"] != "key" {
		t.Errorf("group_by 应回显为 key，实际 %v", data["group_by"])
	}
	rows, ok := data["rows"].([]any)
	if !ok {
		t.Fatalf("费用报表 rows 应为数组: %v", data)
	}
	names := map[string]bool{}
	for _, r := range rows {
		row := r.(map[string]any)
		names[row["group_key"].(string)] = true
		// 管理端可见成本与差额。
		if _, present := row["credits_cost"]; !present {
			t.Errorf("管理端费用报表行应包含 credits_cost: %v", row)
		}
	}
	if !names["cost-key-a"] || !names["cost-key-b"] {
		t.Errorf("按密钥费用报表应含两个密钥行，实际: %v", names)
	}

	// api_key_id 筛选收窄到单密钥。
	resp, env = e.do(t, rootC, "GET",
		fmt.Sprintf("/api/admin/stats/cost-report?group_by=key&api_key_id=%d", keyA), nil)
	if resp.StatusCode != 200 {
		t.Fatalf("按密钥筛选应 200，实际 %d %v", resp.StatusCode, env)
	}
	filtered := env["data"].(map[string]any)["rows"].([]any)
	if len(filtered) != 1 {
		t.Fatalf("api_key_id 筛选应只剩 1 行，实际 %d: %v", len(filtered), filtered)
	}
	if filtered[0].(map[string]any)["group_key"] != "cost-key-a" {
		t.Errorf("筛选结果应为 cost-key-a: %v", filtered[0])
	}
}

// TestAdminCostReportEndOfDayIncludesToday 是费用报表「末日后一天被丢掉」缺陷的回归守卫。
//
// 缺陷根因与 TestAdminHeatmapEndOfDayIncludesToday 同源：前端发送
// end_timestamp = dayjs().endOf('day').unix()（当日 23:59:59），旧实现对其取 SpendDay
// 截断到当日 0 点，配合 created_at < to（排他上界）把 end 当日整日排除。
// 费用报表走 aggFilterFromQuery（独立于 resolveDayRange），需独立守卫。
func TestAdminCostReportEndOfDayIncludesToday(t *testing.T) {
	e := newTestEnv(t)
	rootC := e.seedAndLogin(t, "costeod", domain.RoleRoot)
	uid := e.userIDByName(t, "costeod")

	e.seedUsageLog(t, store.UsageLog{
		RequestID: "cost-eod-today", UserID: uid, APIKeyID: 1, ModelName: "glm-5",
		CreditsCharged: 100, CreditsCost: 30, CreatedAt: time.Now(),
	})

	// 前端口径：end_timestamp = 今日 23:59:59。
	now := time.Now()
	start := now.AddDate(0, 0, -7).Unix()
	end := time.Date(now.Year(), now.Month(), now.Day(), 23, 59, 59, 0, time.Local).Unix()
	resp, env := e.do(t, rootC, "GET",
		fmt.Sprintf("/api/admin/stats/cost-report?group_by=model&start_timestamp=%d&end_timestamp=%d", start, end), nil)
	if resp.StatusCode != 200 {
		t.Fatalf("费用报表应 200，实际 %d %v", resp.StatusCode, env)
	}
	rows := env["data"].(map[string]any)["rows"].([]any)
	var totalCharged float64
	for _, r := range rows {
		totalCharged += r.(map[string]any)["credits_charged"].(float64)
	}
	if totalCharged != 100 {
		t.Errorf("end=今日 endOf('day') 应包含今日数据（合计 100），实际 %v——疑似回归了 SpendDay 截断缺陷", totalCharged)
	}
}
