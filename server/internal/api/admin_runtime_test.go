package api

import (
	"net/http"
	"testing"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
)

// TestAdminRuntime 覆盖管理端运维大屏运行指标端点 GET /admin/stats/runtime：
// 200 返回 Snapshot JSON（含 gauges/counters/histograms/generated_at）；
// managed 托管令牌可访问（admin 桶）；无令牌 401。
// 依赖 TZL_TEST_DATABASE_URL，未设置自动 skip。
func TestAdminRuntime(t *testing.T) {
	e := newTestEnv(t)
	managedTok, _, _ := seedManagedToken(t, e, "rt-scope")
	adminC := e.seedAndLogin(t, "rtadmin", domain.RoleAdmin)

	// 管理员会话访问：200 且响应含四类字段。
	resp, env := e.do(t, adminC, "GET", "/api/admin/stats/runtime", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("管理员访问 runtime 应 200，实际 %d %v", resp.StatusCode, env)
	}
	data, ok := env["data"].(map[string]any)
	if !ok {
		t.Fatalf("响应 data 应为对象: %v", env)
	}
	for _, key := range []string{"gauges", "counters", "histograms", "generated_at"} {
		if _, present := data[key]; !present {
			t.Errorf("响应 data 应含 %q: %v", key, data)
		}
	}

	// 托管令牌（managed）走 admin 桶，亦可访问。
	respM, envM := doWithToken(t, e, managedTok, "GET", "/api/admin/stats/runtime", nil)
	if respM.StatusCode != 200 {
		t.Fatalf("托管令牌访问 runtime 应 200，实际 %d %v", respM.StatusCode, envM)
	}

	// 无令牌 401。
	anon := e.client(t)
	respA, _ := e.do(t, anon, "GET", "/api/admin/stats/runtime", nil)
	if respA.StatusCode != http.StatusUnauthorized {
		t.Errorf("匿名访问应 401，实际 %d", respA.StatusCode)
	}
}
