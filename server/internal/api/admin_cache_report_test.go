package api

import (
	"fmt"
	"testing"
	"time"

	"gorm.io/datatypes"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// TestAdminCacheReport 覆盖管理端缓存分析：
//  1. 200 返回；
//  2. 命中率口径与 /me/cache-report 一致——单用户场景下 admin overall 等于该用户的 me overall；
//  3. 分组行额外暴露 credits_cost 且非负；
//  4. group_by=channel 维度合法（admin 特有维度）。
func TestAdminCacheReport(t *testing.T) {
	e := newTestEnv(t)
	rootC := e.seedAndLogin(t, "admcacheroot", domain.RoleRoot)
	userC := e.seedAndLogin(t, "admcacheuser", domain.RoleUser)
	uid := e.userIDByName(t, "admcacheuser")

	// 与 TestMeCacheReportByModel 同种子：缓存读 2000、prompt 1000、命中率 2/3。
	e.seedUsageLog(t, store.UsageLog{
		RequestID: "adm-cache-1", UserID: uid, APIKeyID: 41, ModelName: "m-alpha",
		PromptTokens: 300, CompletionTokens: 10, CacheReadTokens: 700, CacheWriteTokens: 50,
		CreditsCharged: 100, CreditsCost: 40,
	})
	e.seedUsageLog(t, store.UsageLog{
		RequestID: "adm-cache-2", UserID: uid, APIKeyID: 41, ModelName: "m-alpha",
		PromptTokens: 200, CompletionTokens: 5, CacheReadTokens: 800, CacheWriteTokens: 0,
		CreditsCharged: 80, CreditsCost: 30,
	})
	e.seedUsageLog(t, store.UsageLog{
		RequestID: "adm-cache-3", UserID: uid, APIKeyID: 42, ModelName: "m-beta",
		PromptTokens: 500, CompletionTokens: 5, CacheReadTokens: 500, CacheWriteTokens: 20,
		CreditsCharged: 50, CreditsCost: 20,
	})

	start := time.Now().AddDate(0, 0, -1).Unix()
	end := time.Now().AddDate(0, 0, 1).Unix()
	path := fmt.Sprintf("/api/admin/stats/cache-report?group_by=model&start_timestamp=%d&end_timestamp=%d", start, end)

	resp, env := e.do(t, rootC, "GET", path, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("管理端缓存分析应 200，实际 %d %v", resp.StatusCode, env)
	}
	data, ok := env["data"].(map[string]any)
	if !ok {
		t.Fatalf("响应应为信封: %v", env)
	}

	// 1. overall 命中率口径与 me 一致：缓存读 2000 / prompt 1000 → 2/3。
	overall, ok := data["overall"].(map[string]any)
	if !ok {
		t.Fatalf("缺少 overall: %v", data)
	}
	if int64(overall["requests"].(float64)) != 3 {
		t.Errorf("overall.requests 应为 3: %v", overall)
	}
	if int64(overall["cache_read_tokens"].(float64)) != 2000 {
		t.Errorf("overall.cache_read_tokens 应为 2000: %v", overall)
	}
	if hit := overall["cache_hit_rate"].(float64); hit < 0.666 || hit > 0.667 {
		t.Errorf("overall.cache_hit_rate 应约为 0.667: %v", hit)
	}
	// overall 不含成本字段（口径与 me 一致）。
	for _, banned := range []string{"credits_cost", "credits_cost_money", "margin"} {
		if _, present := overall[banned]; present {
			t.Errorf("overall 不应暴露 %s: %v", banned, overall)
		}
	}

	// 2. 分组行暴露 credits_cost 且非负；并核对 alpha 的命中率口径与 me 一致。
	groups, ok := data["groups"].([]any)
	if !ok {
		t.Fatalf("groups 应为数组: %v", data)
	}
	if len(groups) != 2 {
		t.Fatalf("按模型应 2 行，实际 %d", len(groups))
	}
	for _, g := range groups {
		row := g.(map[string]any)
		cost, present := row["credits_cost"]
		if !present {
			t.Errorf("管理端分组行应暴露 credits_cost: %v", row)
			continue
		}
		if costCost, _ := cost.(float64); costCost < 0 {
			t.Errorf("credits_cost 应非负: %v", row)
		}
		if _, present := row["credits_cost_money"]; !present {
			t.Errorf("管理端分组行应暴露 credits_cost_money: %v", row)
		}
	}
	alphaRow := groups[0].(map[string]any)
	if alphaRow["group_key"] != "m-alpha" {
		alphaRow = groups[1].(map[string]any)
	}
	if hit := alphaRow["cache_hit_rate"].(float64); hit < 0.749 || hit > 0.751 {
		t.Errorf("m-alpha 命中率应约为 0.75（与 me 一致）: %v", hit)
	}

	// 3. group_by=channel 维度合法（不报错、返回 200）。
	respCh, envCh := e.do(t, rootC, "GET",
		fmt.Sprintf("/api/admin/stats/cache-report?group_by=channel&start_timestamp=%d&end_timestamp=%d", start, end), nil)
	if respCh.StatusCode != 200 {
		t.Fatalf("group_by=channel 应 200，实际 %d %v", respCh.StatusCode, envCh)
	}

	// 4. 口径等价对照：同种子数据下，该用户的 me overall.cache_hit_rate
	// 与 admin overall.cache_hit_rate 数值一致（单用户场景二者必相等）。
	respMe, envMe := e.do(t, userC, "GET",
		fmt.Sprintf("/api/me/cache-report?group_by=model&start_timestamp=%d&end_timestamp=%d", start, end), nil)
	if respMe.StatusCode != 200 {
		t.Fatalf("me 缓存分析应 200，实际 %d %v", respMe.StatusCode, envMe)
	}
	meOverall := envMe["data"].(map[string]any)["overall"].(map[string]any)
	meHit := meOverall["cache_hit_rate"].(float64)
	adminHit := overall["cache_hit_rate"].(float64)
	if meHit != adminHit {
		t.Errorf("单用户场景下 me 与 admin overall.cache_hit_rate 应相等: me=%v admin=%v", meHit, adminHit)
	}
}

// TestAdminCacheReportManagedScope 校验托管视角的作用域过滤：
// 两个接入方 A、B 各自种入用量日志，A 令牌只能看到本接入方的缓存数据。
func TestAdminCacheReportManagedScope(t *testing.T) {
	e := newTestEnv(t)
	tokenA, integA, _ := seedManagedToken(t, e, "admcache-scope-a")
	_, integB, _ := seedManagedToken(t, e, "admcache-scope-b")

	// 在 A、B 两个接入方作用域各建一个用户并种入带缓存 token 的用量日志。
	hostedA := &store.User{
		Username: "admcache-scope-a-user", Role: domain.RoleUser, Status: domain.UserEnabled,
		IntegrationID: &integA, MustChangePassword: false,
	}
	if err := e.db.Create(hostedA).Error; err != nil {
		t.Fatalf("种入用户失败: %v", err)
	}
	hostedB := &store.User{
		Username: "admcache-scope-b-user", Role: domain.RoleUser, Status: domain.UserEnabled,
		IntegrationID: &integB, MustChangePassword: false,
	}
	if err := e.db.Create(hostedB).Error; err != nil {
		t.Fatalf("种入用户失败: %v", err)
	}
	now := time.Now()
	for _, tc := range []struct {
		uid, integ int64
		req        string
	}{
		{hostedA.ID, integA, "admcache-scope-a-req"},
		{hostedB.ID, integB, "admcache-scope-b-req"},
	} {
		log := &store.UsageLog{
			RequestID: tc.req, UserID: tc.uid, ModelName: "scope-model",
			IntegrationID: tc.integ, Status: domain.UsageSettled,
			PromptTokens: 100, CacheReadTokens: 400, CreditsCharged: 100, CreditsCost: 30,
			PriceSnapshot: datatypes.JSON("{}"), CreatedAt: now,
		}
		if err := e.deps.UsageLogs.Create(t.Context(), log); err != nil {
			t.Fatalf("种入用量日志失败: %v", err)
		}
	}

	start := now.AddDate(0, 0, -1).Unix()
	end := now.AddDate(0, 0, 1).Unix()
	resp, env := doWithToken(t, e, tokenA, "GET",
		fmt.Sprintf("/api/admin/stats/cache-report?group_by=model&start_timestamp=%d&end_timestamp=%d", start, end), nil)
	if resp.StatusCode != 200 {
		t.Fatalf("托管视角缓存分析应 200，实际 %d %v", resp.StatusCode, env)
	}
	data := env["data"].(map[string]any)
	overall := data["overall"].(map[string]any)
	// A 令牌只看到本接入方的一条日志：prompt 100，cache_read 400。
	if int64(overall["requests"].(float64)) != 1 {
		t.Errorf("托管视角只应看到本接入方 1 条记录，overall.requests=%v", overall["requests"])
	}
	if int64(overall["cache_read_tokens"].(float64)) != 400 {
		t.Errorf("托管视角 cache_read_tokens 应为 400: %v", overall["cache_read_tokens"])
	}
}
