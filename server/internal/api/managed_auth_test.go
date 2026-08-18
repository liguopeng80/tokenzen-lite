package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"gorm.io/datatypes"

	"github.com/liguopeng80/tokenzen-lite/server/internal/auth"
	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// seedManagedToken 在库中种入一个接入方、其服务账号（role=managed、无口令）与一个启用中的
// 服务令牌，返回令牌明文、接入方 ID 与令牌 ID。模拟批次 F 的创建流程，供认证与桶分流测试。
func seedManagedToken(t *testing.T, e *testEnv, slug string) (plain string, integID, tokenID int64) {
	t.Helper()
	integ := &store.Integration{Name: slug, Slug: slug, Status: domain.IntegrationEnabled}
	if err := e.deps.Integrations.Create(t.Context(), integ); err != nil {
		t.Fatalf("种入接入方失败: %v", err)
	}
	svc := &store.User{
		Username: "svc:" + slug, Role: domain.RoleManaged, Status: domain.UserEnabled,
		IntegrationID: &integ.ID, MustChangePassword: false,
	}
	if err := e.db.Create(svc).Error; err != nil {
		t.Fatalf("种入服务账号失败: %v", err)
	}
	gen, err := auth.GenerateServiceToken()
	if err != nil {
		t.Fatalf("生成服务令牌失败: %v", err)
	}
	st := &store.ServiceToken{
		IntegrationID: integ.ID, Name: "test-token",
		TokenHash: auth.HashKey(gen.Plain), TokenPrefix: gen.Prefix,
		Status: domain.ServiceTokenEnabled,
	}
	if err := e.deps.ServiceTokens.Create(t.Context(), st); err != nil {
		t.Fatalf("种入服务令牌失败: %v", err)
	}
	return gen.Plain, integ.ID, st.ID
}

// doWithToken 用服务令牌（Authorization: Bearer）发请求，不带 cookie，模拟接入方后端进程。
func doWithToken(t *testing.T, e *testEnv, token, method, path string, body any) (*http.Response, map[string]any) {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("编码请求体失败: %v", err)
		}
	}
	req, err := http.NewRequest(method, e.srv.URL+path, &buf)
	if err != nil {
		t.Fatalf("构建请求失败: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer resp.Body.Close()
	var env map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&env)
	return resp, env
}

// TestManagedServiceTokenAuth 覆盖 R1 认证与桶分流（验收 #1/#2/#4）：
// 服务令牌进托管桶 200、被运营桶按角色拒 403（已认证后的拒绝，非 401）、停用后 401。
func TestManagedServiceTokenAuth(t *testing.T) {
	e := newTestEnv(t)
	token, _, tokenID := seedManagedToken(t, e, "aiwb")

	resp, _ := doWithToken(t, e, token, "GET", "/api/admin/users/", nil)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("服务令牌访问托管桶 GET /admin/users/ 应 200，实际 %d", resp.StatusCode)
	}
	for _, p := range []string{"/api/admin/channels/", "/api/admin/stats/profit", "/api/admin/stats/overview"} {
		resp, _ := doWithToken(t, e, token, "GET", p, nil)
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("服务令牌访问运营桶 %s 应 403，实际 %d", p, resp.StatusCode)
		}
	}
	if err := e.deps.ServiceTokens.UpdateStatus(t.Context(), tokenID, domain.ServiceTokenDisabled); err != nil {
		t.Fatalf("停用令牌失败: %v", err)
	}
	resp, _ = doWithToken(t, e, token, "GET", "/api/admin/users/", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("停用令牌后应 401，实际 %d", resp.StatusCode)
	}
}

// TestManagedCostReportStripping 覆盖验收 #3：托管视角费用报表剥除 credits_cost/margin，
// 消费额与用量照常可见；运营 admin 同请求含这两列。
func TestManagedCostReportStripping(t *testing.T) {
	e := newTestEnv(t)
	token, integID, _ := seedManagedToken(t, e, "aiwb2")
	adminC := e.seedAndLogin(t, "stripadmin", domain.RoleAdmin)

	hosted := &store.User{
		Username: "stripuser", Role: domain.RoleUser, Status: domain.UserEnabled,
		IntegrationID: &integID, MustChangePassword: false,
	}
	if err := e.db.Create(hosted).Error; err != nil {
		t.Fatalf("种入用户失败: %v", err)
	}
	log := &store.UsageLog{
		RequestID: "strip-req-1", UserID: hosted.ID, ModelName: "strip-model",
		CallCount: 1, PromptTokens: 10, CompletionTokens: 20,
		CreditsCharged: 1000, CreditsCost: 400, Status: domain.UsageSettled,
		IntegrationID: integID, PriceSnapshot: datatypes.JSON("{}"),
		CreatedAt: time.Now(),
	}
	if err := e.deps.UsageLogs.Create(t.Context(), log); err != nil {
		t.Fatalf("种入用量日志失败: %v", err)
	}

	_, aEnv := e.do(t, adminC, "GET", "/api/admin/stats/cost-report?group_by=user", nil)
	aData, _ := aEnv["data"].(map[string]any)
	aRows, _ := aData["rows"].([]any)
	if len(aRows) == 0 {
		t.Fatalf("admin cost-report 应至少一行")
	}
	aRow, _ := aRows[0].(map[string]any)
	if _, ok := aRow["credits_cost"]; !ok {
		t.Errorf("admin cost-report 行应含 credits_cost，实际 %v", aRow)
	}

	_, mEnv := doWithToken(t, e, token, "GET", "/api/admin/stats/cost-report?group_by=user", nil)
	mData, _ := mEnv["data"].(map[string]any)
	mRows, _ := mData["rows"].([]any)
	if len(mRows) == 0 {
		t.Fatalf("托管 cost-report 应至少一行（作用域内有用量）")
	}
	mRow, _ := mRows[0].(map[string]any)
	if _, ok := mRow["credits_cost"]; ok {
		t.Errorf("托管 cost-report 行不应含 credits_cost，实际 %v", mRow)
	}
	if _, ok := mRow["margin"]; ok {
		t.Errorf("托管 cost-report 行不应含 margin，实际 %v", mRow)
	}
	if _, ok := mRow["credits_charged"]; !ok {
		t.Errorf("托管 cost-report 行应含 credits_charged，实际 %v", mRow)
	}
}
