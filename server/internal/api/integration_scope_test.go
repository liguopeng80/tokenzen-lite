package api

import (
	"fmt"
	"net/http"
	"testing"

	"gorm.io/datatypes"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// TestIntegrationScope 覆盖验收 #10：跨作用域隔离。
// 两个接入方 A、B 各持服务令牌；A 的令牌只能在用户、流水、用量三个列表看到本接入方对象，
// 访问他接入方对象按「不存在」处理（404）。运营 admin 视角不受作用域限制作为对照。
func TestIntegrationScope(t *testing.T) {
	e := newTestEnv(t)
	tokenA, integA, _ := seedManagedToken(t, e, "scope-a")
	tokenB, integB, _ := seedManagedToken(t, e, "scope-b")

	// A 令牌建用户 ua，B 令牌建用户 ub。两者均落在各自接入方作用域内。
	_, env := doWithToken(t, e, tokenA, "POST", "/api/admin/users/",
		map[string]any{"username": "scope-a-user"})
	if resp := env; resp["data"] == nil {
		t.Fatalf("A 令牌建用户失败: %v", resp)
	}
	uaID := int64(env["data"].(map[string]any)["id"].(float64))

	respB, envB := doWithToken(t, e, tokenB, "POST", "/api/admin/users/",
		map[string]any{"username": "scope-b-user"})
	if respB.StatusCode != http.StatusCreated {
		t.Fatalf("B 令牌建用户应 201，实际 %d %v", respB.StatusCode, envB)
	}
	ubID := int64(envB["data"].(map[string]any)["id"].(float64))

	// 各自给本接入方用户发积分，产生作用域内的流水（同时验证快照写入 integration_id）。
	if resp, _ := doWithToken(t, e, tokenA, "POST",
		fmt.Sprintf("/api/admin/users/%d/credits", uaID),
		map[string]any{"amount": 5000, "note": "scope-a grant"}); resp.StatusCode != http.StatusOK {
		t.Fatalf("A 令牌给 ua 发积分失败: %d", resp.StatusCode)
	}
	if resp, _ := doWithToken(t, e, tokenB, "POST",
		fmt.Sprintf("/api/admin/users/%d/credits", ubID),
		map[string]any{"amount": 7000, "note": "scope-b grant"}); resp.StatusCode != http.StatusOK {
		t.Fatalf("B 令牌给 ub 发积分失败: %d", resp.StatusCode)
	}

	// 跨作用域发积分应被「不存在」拦截（loadManagedUser 已做作用域校验）。
	if resp, _ := doWithToken(t, e, tokenA, "POST",
		fmt.Sprintf("/api/admin/users/%d/credits", ubID),
		map[string]any{"amount": 1000}); resp.StatusCode != http.StatusNotFound {
		t.Errorf("A 令牌给 B 的用户发积分应 404，实际 %d", resp.StatusCode)
	}

	// 直接在库中种入带作用域的用量日志，校验列表过滤（绕过中继的真实上游）。
	seedScopedUsageLog(t, e, uaID, integA, "scope-a-req")
	seedScopedUsageLog(t, e, ubID, integB, "scope-b-req")

	// 1. 用户列表：A 只含 ua，不含 ub、不含服务账号、不含运营方内部用户。
	assertUserListScoped(t, e, tokenA, "A", uaID, []int64{ubID})

	// 2. 跨作用域访问对象详情与改状态：均 404（与「不存在」同响应）。
	for _, p := range []string{
		fmt.Sprintf("/api/admin/users/%d", ubID),
	} {
		if resp, _ := doWithToken(t, e, tokenA, "GET", p, nil); resp.StatusCode != http.StatusNotFound {
			t.Errorf("A 令牌 GET %s 应 404，实际 %d", p, resp.StatusCode)
		}
	}
	if resp, _ := doWithToken(t, e, tokenA, "POST",
		fmt.Sprintf("/api/admin/users/%d/status", ubID),
		map[string]string{"status": "disabled"}); resp.StatusCode != http.StatusNotFound {
		t.Errorf("A 令牌改 B 用户状态应 404，实际 %d", resp.StatusCode)
	}

	// 3. 流水列表：A 只看到本接入方记录。
	assertLedgerScoped(t, e, tokenA, "A", uaID, ubID)

	// 4. 用量日志列表：A 只看到本接入方记录。
	assertUsageLogsScoped(t, e, tokenA, "A", uaID, ubID)

	// 5. 用量日志 detail：A 访问 B 的 request_id → 404。
	if resp, _ := doWithToken(t, e, tokenA, "GET",
		"/api/admin/usage-logs/detail?request_id=scope-b-req", nil); resp.StatusCode != http.StatusNotFound {
		t.Errorf("A 令牌查 B 的用量 detail 应 404，实际 %d", resp.StatusCode)
	}
	if resp, _ := doWithToken(t, e, tokenA, "GET",
		"/api/admin/usage-logs/detail?request_id=scope-a-req", nil); resp.StatusCode != http.StatusOK {
		t.Errorf("A 令牌查本接入方用量 detail 应 200，实际 %d", resp.StatusCode)
	}

	// 对照：运营 admin 不受作用域限制，能看见两个接入方的对象。
	adminC := e.seedAndLogin(t, "scope-admin", domain.RoleAdmin)
	_, env = e.do(t, adminC, "GET", "/api/admin/users/", nil)
	usernames := usernamesFromPage(env["data"])
	for _, want := range []string{"scope-a-user", "scope-b-user"} {
		if !usernames[want] {
			t.Errorf("运营 admin 用户列表应含 %s（不受作用域限制），实际 %v", want, usernames)
		}
	}
}

// seedScopedUsageLog 直接在库中种入一条带作用域快照的已结算用量日志。
func seedScopedUsageLog(t *testing.T, e *testEnv, userID, integID int64, requestID string) {
	t.Helper()
	log := &store.UsageLog{
		RequestID: requestID, UserID: userID, ModelName: "scope-model",
		IntegrationID: integID, Status: domain.UsageSettled,
		PromptTokens: 10, CompletionTokens: 5, CreditsCharged: 100,
		PriceSnapshot: datatypes.JSON("{}"),
	}
	if err := e.deps.UsageLogs.Create(t.Context(), log); err != nil {
		t.Fatalf("种入用量日志失败: %v", err)
	}
}

// assertUserListScoped 校验令牌视角的用户列表只含本接入方的普通用户，
// 不含他接入方用户、不含服务账号与运营方内部用户。
func assertUserListScoped(t *testing.T, e *testEnv, token, label string, wantID int64, bannedIDs []int64) {
	t.Helper()
	_, env := doWithToken(t, e, token, "GET", "/api/admin/users/", nil)
	data, _ := env["data"].(map[string]any)
	items, _ := data["items"].([]any)
	if len(items) == 0 {
		t.Fatalf("%s 令牌用户列表为空", label)
	}
	var ids []int64
	seenWant := false
	for _, it := range items {
		row, _ := it.(map[string]any)
		id := int64(row["id"].(float64))
		ids = append(ids, id)
		if id == wantID {
			seenWant = true
		}
		username, _ := row["username"].(string)
		// 服务账号与运营方内部用户均不应出现。
		if len(username) >= 4 && username[:4] == "svc:" {
			t.Errorf("%s 令牌用户列表出现服务账号 %s", label, username)
		}
	}
	if !seenWant {
		t.Errorf("%s 令牌用户列表应含本接入方用户 %d，实际 %v", label, wantID, ids)
	}
	for _, banned := range bannedIDs {
		for _, id := range ids {
			if id == banned {
				t.Errorf("%s 令牌用户列表不应含他接入方用户 %d", label, banned)
			}
		}
	}
}

// assertLedgerScoped 校验令牌视角的流水列表只含本接入方用户，不含他接入方。
func assertLedgerScoped(t *testing.T, e *testEnv, token, label string, wantUID, bannedUID int64) {
	t.Helper()
	_, env := doWithToken(t, e, token, "GET", "/api/admin/ledger", nil)
	data, _ := env["data"].(map[string]any)
	items, _ := data["items"].([]any)
	if len(items) == 0 {
		t.Fatalf("%s 令牌流水列表为空", label)
	}
	seenWant, seenBanned := false, false
	for _, it := range items {
		row, _ := it.(map[string]any)
		uid := int64(row["user_id"].(float64))
		if uid == wantUID {
			seenWant = true
		}
		if uid == bannedUID {
			seenBanned = true
		}
	}
	if !seenWant {
		t.Errorf("%s 令牌流水列表应含本接入方用户 %d 的流水", label, wantUID)
	}
	if seenBanned {
		t.Errorf("%s 令牌流水列表不应含他接入方用户 %d 的流水", label, bannedUID)
	}
}

// assertUsageLogsScoped 校验令牌视角的用量日志列表只含本接入方，不含他接入方。
func assertUsageLogsScoped(t *testing.T, e *testEnv, token, label string, wantUID, bannedUID int64) {
	t.Helper()
	_, env := doWithToken(t, e, token, "GET", "/api/admin/usage-logs", nil)
	data, _ := env["data"].(map[string]any)
	items, _ := data["items"].([]any)
	if len(items) == 0 {
		t.Fatalf("%s 令牌用量日志列表为空", label)
	}
	seenWant, seenBanned := false, false
	for _, it := range items {
		row, _ := it.(map[string]any)
		uid := int64(row["user_id"].(float64))
		if uid == wantUID {
			seenWant = true
		}
		if uid == bannedUID {
			seenBanned = true
		}
	}
	if !seenWant {
		t.Errorf("%s 令牌用量日志列表应含本接入方用户 %d 的记录", label, wantUID)
	}
	if seenBanned {
		t.Errorf("%s 令牌用量日志列表不应含他接入方用户 %d 的记录", label, bannedUID)
	}
}

// usernamesFromPage 把分页响应里的 items 收集成 username 集合，便于做包含断言。
func usernamesFromPage(data any) map[string]bool {
	out := map[string]bool{}
	m, _ := data.(map[string]any)
	items, _ := m["items"].([]any)
	for _, it := range items {
		row, _ := it.(map[string]any)
		if name, ok := row["username"].(string); ok {
			out[name] = true
		}
	}
	return out
}
