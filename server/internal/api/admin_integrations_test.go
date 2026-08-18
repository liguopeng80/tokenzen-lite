package api

import (
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// TestAdminIntegrationLifecycle 覆盖批次 F 的运营入口端到端（root 会话驱动）：
// 创建接入方 → 签发服务令牌 → 令牌通过 AdminAuth 进托管桶 → 停用接入方后令牌失效 → 列表可查。
func TestAdminIntegrationLifecycle(t *testing.T) {
	e := newTestEnv(t)
	rootC := e.seedAndLogin(t, "froot", domain.RoleRoot)

	// 1. root 创建接入方（status=enabled）。
	resp, env := e.do(t, rootC, "POST", "/api/admin/integrations/",
		map[string]string{"name": "运营接入方A", "slug": "ops-integ-a"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("创建接入方应 201，实际 %d，响应: %v", resp.StatusCode, env)
	}
	data, _ := env["data"].(map[string]any)
	integID := int64(data["id"].(float64))
	if data["slug"] != "ops-integ-a" || data["status"] != string(domain.IntegrationEnabled) {
		t.Fatalf("接入方返回字段不符: %v", data)
	}

	// 自动建的服务账号显示名称应取自接入方名称，不应为空。
	var svc store.User
	if err := e.db.Where("username = ?", "svc:ops-integ-a").First(&svc).Error; err != nil {
		t.Fatalf("应能找到接入方服务账号: %v", err)
	}
	if svc.DisplayName != "运营接入方A" {
		t.Errorf("服务账号显示名称应为接入方名称，实际 %q", svc.DisplayName)
	}

	// 2. 为该接入方签发服务令牌，明文仅本次返回。
	resp, env = e.do(t, rootC, "POST",
		"/api/admin/integrations/"+strconv.FormatInt(integID, 10)+"/service-tokens",
		map[string]string{"name": "生产令牌"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("签发服务令牌应 201，实际 %d，响应: %v", resp.StatusCode, env)
	}
	data, _ = env["data"].(map[string]any)
	plain, _ := data["token"].(string)
	if !strings.HasPrefix(plain, "tzs-") {
		t.Fatalf("服务令牌明文应以 tzs- 开头，实际: %q", plain)
	}

	// 3. 用该令牌调托管桶 GET /admin/users/，应通过 AdminAuth 认证返回 200。
	resp, _ = doWithToken(t, e, plain, "GET", "/api/admin/users/", nil)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("新建服务令牌访问托管桶应 200，实际 %d", resp.StatusCode)
	}

	// 4. 停用整个接入方（级联停用服务账号），同一令牌应 401。
	resp, env = e.do(t, rootC, "POST",
		"/api/admin/integrations/"+strconv.FormatInt(integID, 10)+"/disable", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("停用接入方应 200，实际 %d，响应: %v", resp.StatusCode, env)
	}
	resp, _ = doWithToken(t, e, plain, "GET", "/api/admin/users/", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("停用接入方后令牌应 401，实际 %d", resp.StatusCode)
	}

	// 5. 接入方列表仍可查（停用不删数据）。
	resp, env = e.do(t, rootC, "GET", "/api/admin/integrations/", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("接入方列表应 200，实际 %d，响应: %v", resp.StatusCode, env)
	}
}
