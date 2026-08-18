package api

import (
	"encoding/csv"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// 覆盖 /api/me/usage-logs/detail 与 /api/me/usage-logs/export：
// 详情字段、作用域隔离（他人 request_id 404）、运营字段不泄露、
// CSV 表头与脱敏、空结果、导出筛选与用户作用域。

// meSensitiveKeys 是用户侧响应里不得出现的运营字段名。
var meSensitiveKeys = []string{
	"channel_id", "channel_name", "credits_cost", "price_snapshot",
	"upstream_model", "protocol", "integration_id", "client_ip",
	"credits_precharged", "peak_multiplier_percent", "department_id",
	"usage_semantic",
}

// assertNoSensitiveKeys 断言单条响应行（map）不含任何运营敏感字段。
func assertNoSensitiveKeys(t *testing.T, label string, row map[string]any) {
	t.Helper()
	for _, k := range meSensitiveKeys {
		if _, ok := row[k]; ok {
			t.Errorf("%s 不应暴露运营字段 %s（值: %v）", label, k, row[k])
		}
	}
}

// doRawGet 用已登录客户端（携带会话 cookie）发起 GET，返回完整响应体字节，
// 供 CSV 流式响应的正文断言。调用方负责关闭返回的响应体。
func doRawGet(t *testing.T, c *http.Client, url string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		t.Fatalf("构建请求失败: %v", err)
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("读取响应体失败: %v", err)
	}
	return resp, body
}

// TestMeUsageLogDetailOwn 覆盖正常详情：本人 request_id 返回 token 明细、扣费、耗时、状态。
func TestMeUsageLogDetailOwn(t *testing.T) {
	e := newTestEnv(t)
	userC := e.seedAndLogin(t, "meudetailown", domain.RoleUser)
	uid := e.userIDByName(t, "meudetailown")
	e.seedUsageLog(t, store.UsageLog{
		RequestID: "meud-own", UserID: uid, APIKeyID: 7, ModelName: "glm-5",
		PromptTokens: 100, CompletionTokens: 30, CacheReadTokens: 10, CacheWriteTokens: 5,
		CreditsCharged: 1234, LatencyMS: 567, IsStream: true, Status: domain.UsageSettled,
		ErrorMessage: "", ChannelID: 99, CreditsCost: 500, UpstreamModel: "secret-up",
	})

	resp, env := e.do(t, userC, "GET", "/api/me/usage-logs/detail?request_id=meud-own", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("本人详情应 200，实际 %d %v", resp.StatusCode, env)
	}
	row := env["data"].(map[string]any)
	if row["request_id"] != "meud-own" {
		t.Errorf("request_id 不符: %v", row["request_id"])
	}
	if got := int64(row["prompt_tokens"].(float64)); got != 100 {
		t.Errorf("prompt_tokens 应 100，实际 %d", got)
	}
	if got := int64(row["credits_charged"].(float64)); got != 1234 {
		t.Errorf("credits_charged 应 1234，实际 %d", got)
	}
	if got := int64(row["latency_ms"].(float64)); got != 567 {
		t.Errorf("latency_ms 应 567，实际 %d", got)
	}
	if isStream, _ := row["is_stream"].(bool); !isStream {
		t.Errorf("is_stream 应 true，实际 %v", row["is_stream"])
	}
	// 运营字段不得泄露（种子里故意填了 channel_id/upstream_model/credits_cost）。
	assertNoSensitiveKeys(t, "详情", row)
}

// TestMeUsageLogDetailMissingRequestID 覆盖缺少 request_id 参数 → 400。
func TestMeUsageLogDetailMissingRequestID(t *testing.T) {
	e := newTestEnv(t)
	userC := e.seedAndLogin(t, "meudetailmiss", domain.RoleUser)
	resp, env := e.do(t, userC, "GET", "/api/me/usage-logs/detail", nil)
	if resp.StatusCode != 400 {
		t.Errorf("缺少 request_id 应 400，实际 %d %v", resp.StatusCode, env)
	}
}

// TestMeUsageLogDetailNonexistent 覆盖不存在的 request_id → 404。
func TestMeUsageLogDetailNonexistent(t *testing.T) {
	e := newTestEnv(t)
	userC := e.seedAndLogin(t, "meudetailnone", domain.RoleUser)
	resp, env := e.do(t, userC, "GET", "/api/me/usage-logs/detail?request_id=no-such-req", nil)
	if resp.StatusCode != 404 {
		t.Errorf("不存在的 request_id 应 404，实际 %d %v", resp.StatusCode, env)
	}
}

// TestMeUsageLogDetailOtherUserScope 覆盖作用域隔离：访问他人的 request_id → 404，
// 且响应与「不存在」不可区分（避免探测归属）。
func TestMeUsageLogDetailOtherUserScope(t *testing.T) {
	e := newTestEnv(t)
	userAC := e.seedAndLogin(t, "meudetaila", domain.RoleUser)
	e.seedAndLogin(t, "meudetailb", domain.RoleUser)
	idB := e.userIDByName(t, "meudetailb")
	e.seedUsageLog(t, store.UsageLog{
		RequestID: "meud-b-secret", UserID: idB, APIKeyID: 1, ModelName: "glm-5",
		CreditsCharged: 9999,
	})

	resp, env := e.do(t, userAC, "GET", "/api/me/usage-logs/detail?request_id=meud-b-secret", nil)
	if resp.StatusCode != 404 {
		t.Errorf("他人 request_id 应 404，实际 %d %v", resp.StatusCode, env)
	}
	// 与「不存在」响应不可区分：同一提示文案。
	if msg, _ := env["message"].(string); !strings.Contains(msg, "不存在") {
		t.Errorf("他人 request_id 的 404 提示应含「不存在」，实际 %q", msg)
	}
}

// TestMeUsageLogsListNoSensitiveLeak 覆盖列表脱敏：/me/usage-logs 行不含任何运营字段。
func TestMeUsageLogsListNoSensitiveLeak(t *testing.T) {
	e := newTestEnv(t)
	userC := e.seedAndLogin(t, "meulistleak", domain.RoleUser)
	uid := e.userIDByName(t, "meulistleak")
	e.seedUsageLog(t, store.UsageLog{
		RequestID: "leak-1", UserID: uid, APIKeyID: 1, ModelName: "glm-5",
		ChannelID: 42, CreditsCost: 777, UpstreamModel: "hidden", Protocol: domain.ProtocolOpenAICompat,
		ClientIP: "1.2.3.4", CreditsPrecharged: 1000,
	})

	resp, env := e.do(t, userC, "GET", "/api/me/usage-logs", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("列表应 200，实际 %d %v", resp.StatusCode, env)
	}
	items := pageItems(t, env)
	if len(items) == 0 {
		t.Fatal("应返回至少 1 条")
	}
	for _, it := range items {
		row, _ := it.(map[string]any)
		assertNoSensitiveKeys(t, "列表行", row)
		// 用户可见字段仍应在。
		if _, ok := row["credits_charged"]; !ok {
			t.Errorf("列表行应含用户可见字段 credits_charged: %v", row)
		}
	}
}

// TestMeUsageLogExportCSV 覆盖导出：BOM 前缀、Content-Type/Disposition、
// 表头脱敏（无渠道/成本/差额列）、行内容、用户作用域（不含他人记录）。
func TestMeUsageLogExportCSV(t *testing.T) {
	e := newTestEnv(t)
	userAC := e.seedAndLogin(t, "meuexpa", domain.RoleUser)
	e.seedAndLogin(t, "meuexpb", domain.RoleUser)
	idA := e.userIDByName(t, "meuexpa")
	idB := e.userIDByName(t, "meuexpb")
	e.seedUsageLog(t, store.UsageLog{
		RequestID: "exp-a-1", UserID: idA, APIKeyID: 5, ModelName: "glm-5",
		PromptTokens: 50, CompletionTokens: 20, CreditsCharged: 300, LatencyMS: 100,
		ChannelID: 8, CreditsCost: 200,
	})
	e.seedUsageLog(t, store.UsageLog{
		RequestID: "exp-b-1", UserID: idB, APIKeyID: 6, ModelName: "glm-5",
		CreditsCharged: 99999, ChannelID: 9, CreditsCost: 1,
	})

	resp, body := doRawGet(t, userAC, e.srv.URL+"/api/me/usage-logs/export")
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("导出应 200，实际 %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/csv") {
		t.Errorf("Content-Type 应 text/csv，实际 %q", ct)
	}
	if cd := resp.Header.Get("Content-Disposition"); !strings.Contains(cd, "attachment") ||
		!strings.Contains(cd, ".csv") {
		t.Errorf("Content-Disposition 不符: %q", cd)
	}
	// UTF-8 BOM 前缀。
	if len(body) < 3 || body[0] != 0xEF || body[1] != 0xBB || body[2] != 0xBF {
		t.Errorf("导出体应以 UTF-8 BOM 开头，实际前 3 字节: %v", body[:min3(len(body))])
	}
	reader := csv.NewReader(strings.NewReader(string(body[3:])))
	rows, err := reader.ReadAll()
	if err != nil {
		t.Fatalf("解析 CSV 失败: %v", err)
	}
	if len(rows) < 2 {
		t.Fatalf("应至少含表头 + 1 行数据，实际 %d 行", len(rows))
	}
	header := rows[0]
	// 表头不含渠道/成本/差额/价格快照等运营列。
	for _, banned := range []string{"渠道", "成本", "差额", "价格", "部门", "接入方"} {
		for _, h := range header {
			if strings.Contains(h, banned) {
				t.Errorf("CSV 表头不应含运营列 %q（命中 %q）", banned, h)
			}
		}
	}
	// 表头应含用户可见列。
	wantCols := []string{"时间", "请求标识", "模型", "扣费积分", "状态"}
	for _, want := range wantCols {
		found := false
		for _, h := range header {
			if h == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("CSV 表头应含 %q，实际 %v", want, header)
		}
	}
	// 数据行只含 A 自己的记录，不含 B。
	dataRows := make([]string, len(rows)-1)
	for i, r := range rows[1:] {
		dataRows[i] = strings.Join(r, ",")
	}
	allBody := strings.Join(dataRows, ",")
	if !strings.Contains(allBody, "exp-a-1") {
		t.Errorf("导出应含本人记录 exp-a-1，实际: %v", rows[1:])
	}
	if strings.Contains(allBody, "exp-b-1") {
		t.Errorf("导出不应含他人记录 exp-b-1，实际: %v", rows[1:])
	}
	// 正文不得出现种子里的渠道/成本数字（脱敏）。
	if strings.Contains(allBody, "99999") {
		t.Errorf("导出正文泄露了他人扣费 99999: %v", rows[1:])
	}
}

// TestMeUsageLogExportEmpty 覆盖空结果：仍写表头与 BOM，无数据行。
func TestMeUsageLogExportEmpty(t *testing.T) {
	e := newTestEnv(t)
	userC := e.seedAndLogin(t, "meuexpempty", domain.RoleUser)

	resp, body := doRawGet(t, userC, e.srv.URL+"/api/me/usage-logs/export")
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("空导出应 200，实际 %d", resp.StatusCode)
	}
	if len(body) < 3 || body[0] != 0xEF || body[1] != 0xBB || body[2] != 0xBF {
		t.Fatalf("空导出仍应有 UTF-8 BOM")
	}
	reader := csv.NewReader(strings.NewReader(string(body[3:])))
	rows, err := reader.ReadAll()
	if err != nil {
		t.Fatalf("解析 CSV 失败: %v", err)
	}
	if len(rows) != 1 {
		t.Errorf("空结果应只含表头 1 行，实际 %d 行: %v", len(rows), rows)
	}
}

// TestMeUsageLogExportFilter 覆盖导出筛选：按 model 过滤只导出匹配的本人记录。
func TestMeUsageLogExportFilter(t *testing.T) {
	e := newTestEnv(t)
	userC := e.seedAndLogin(t, "meuexpfilter", domain.RoleUser)
	uid := e.userIDByName(t, "meuexpfilter")
	e.seedUsageLog(t, store.UsageLog{
		RequestID: "flt-model-a", UserID: uid, APIKeyID: 1, ModelName: "model-a",
	})
	e.seedUsageLog(t, store.UsageLog{
		RequestID: "flt-model-b", UserID: uid, APIKeyID: 1, ModelName: "model-b",
	})

	resp, body := doRawGet(t, userC, e.srv.URL+"/api/me/usage-logs/export?model=model-a")
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("筛选导出应 200，实际 %d", resp.StatusCode)
	}
	reader := csv.NewReader(strings.NewReader(string(body[3:])))
	rows, err := reader.ReadAll()
	if err != nil {
		t.Fatalf("解析 CSV 失败: %v", err)
	}
	// 表头 + 1 行（只含 model-a）。
	if len(rows) != 2 {
		t.Fatalf("model=model-a 筛选导出应 1 行数据，实际 %d 行: %v", len(rows)-1, rows[1:])
	}
	if !strings.Contains(rows[1][1], "flt-model-a") {
		t.Errorf("筛选导出应含 flt-model-a，实际: %v", rows[1])
	}
}

func min3(n int) int {
	if n < 3 {
		return n
	}
	return 3
}
