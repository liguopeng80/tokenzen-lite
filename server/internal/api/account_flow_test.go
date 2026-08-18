package api

import (
	"net/http"
	"testing"
)

// TestAccountSelfServiceFlow 覆盖 P3-16：账号自助流程串联。
// 注册（用户名不合规 400、重名 409、成功 201）→ 登录 → 改密码（原密码错误 400、
// 新密码过短 400、成功 200）→ 旧密码登录 401、新密码登录 200 → 登出 → 已登出会话
// 访问当前用户接口 401。
func TestAccountSelfServiceFlow(t *testing.T) {
	e := newTestEnv(t)
	// 自助注册默认关闭（内部部署由管理员建号），本流程测的是开放注册后的行为。
	e.setSetting(t, "register_enabled", "true")

	// 1. 注册：用户名不合规（不满足 3-32 位字母数字下划线连字符）应 400。
	c := e.client(t)
	resp, env := e.do(t, c, "POST", "/api/auth/register",
		map[string]string{"username": "ab", "password": "initial-password"})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("用户名不合规应 400，实际 %d %v", resp.StatusCode, env)
	}

	// 2. 注册成功：用户名合规。
	resp, env = e.do(t, c, "POST", "/api/auth/register",
		map[string]string{"username": "selfservice1", "password": "initial-password", "display_name": "自助流程用户"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("注册应 201，实际 %d %v", resp.StatusCode, env)
	}

	// 3. 重名注册应 409。
	resp, env = e.do(t, c, "POST", "/api/auth/register",
		map[string]string{"username": "selfservice1", "password": "another-password"})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("重名注册应 409，实际 %d %v", resp.StatusCode, env)
	}

	// 4. 登录。
	resp, env = e.do(t, c, "POST", "/api/auth/login",
		map[string]string{"username": "selfservice1", "password": "initial-password"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("登录应 200，实际 %d %v", resp.StatusCode, env)
	}

	// 5. 改密码：原密码错误应 400，且会话不受影响（不作废）。
	resp, env = e.do(t, c, "PUT", "/api/auth/password",
		map[string]string{"original_password": "wrong-original", "password": "new-password-1"})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("原密码错误应 400，实际 %d %v", resp.StatusCode, env)
	}
	resp, _ = e.do(t, c, "GET", "/api/auth/me", nil)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("原密码错误的改密尝试不应影响当前会话，实际 %d", resp.StatusCode)
	}

	// 6. 改密码：新密码过短（低于 8 位）应 400。
	resp, env = e.do(t, c, "PUT", "/api/auth/password",
		map[string]string{"original_password": "initial-password", "password": "short"})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("新密码过短应 400，实际 %d %v", resp.StatusCode, env)
	}

	// 7. 改密码：成功。
	resp, env = e.do(t, c, "PUT", "/api/auth/password",
		map[string]string{"original_password": "initial-password", "password": "new-password-1"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("改密码应 200，实际 %d %v", resp.StatusCode, env)
	}

	// 8. 旧密码登录应 401，新密码登录应 200（各用独立会话验证，不复用当前会话）。
	resp, _ = e.do(t, e.client(t), "POST", "/api/auth/login",
		map[string]string{"username": "selfservice1", "password": "initial-password"})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("改密后旧密码登录应 401，实际 %d", resp.StatusCode)
	}
	resp, env = e.do(t, e.client(t), "POST", "/api/auth/login",
		map[string]string{"username": "selfservice1", "password": "new-password-1"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("改密后新密码登录应 200，实际 %d %v", resp.StatusCode, env)
	}

	// 9. 登出：改密码后会重新签发当前会话（c 此时仍是有效会话），登出后该会话访问
	// 当前用户接口应 401。
	resp, env = e.do(t, c, "POST", "/api/auth/logout", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("登出应 200，实际 %d %v", resp.StatusCode, env)
	}
	resp, _ = e.do(t, c, "GET", "/api/auth/me", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("登出后访问当前用户接口应 401，实际 %d", resp.StatusCode)
	}
}

// TestSiteConfigReturnsAllFields 覆盖 P3-16：站点公开配置接口成功用例，
// 断言前端换算人民币金额所依赖的字段齐全。未登录可访问。
func TestSiteConfigReturnsAllFields(t *testing.T) {
	e := newTestEnv(t)
	resp, env := e.do(t, e.client(t), "GET", "/api/site/config", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("站点配置接口应 200，实际 %d %v", resp.StatusCode, env)
	}
	if ok, _ := env["success"].(bool); !ok {
		t.Errorf("响应信封 success 应为 true，实际 %v", env["success"])
	}
	data, ok := env["data"].(map[string]any)
	if !ok {
		t.Fatalf("响应信封 data 应为对象，实际 %v", env["data"])
	}
	// server_address 是用户端接入指引展示的 Base URL 来源；缺失会让指引回落到
	// 浏览器站点地址，在 /v1 挂独立域名的部署里给出错误的接入地址。
	// low_balance_threshold_credits 是门户余额预警的唯一取值来源；缺失会让
	// 门户回落到「不预警」，用户在积分耗尽前收不到任何提示。
	// currency_symbol 是界面金额展示符号的唯一来源；缺失会让前端回落到硬编码符号。
	for _, field := range []string{"site_name", "exchange_rate_credits_per_cny",
		"register_enabled", "server_address", "low_balance_threshold_credits", "currency_symbol",
		"profile_display_name_editable", "profile_email_editable"} {
		if _, exists := data[field]; !exists {
			t.Errorf("站点配置响应缺少字段 %q", field)
		}
	}
	if rate, ok := data["exchange_rate_credits_per_cny"].(float64); !ok || rate <= 0 {
		t.Errorf("exchange_rate_credits_per_cny 应为正数，实际 %v", data["exchange_rate_credits_per_cny"])
	}
	if sym, ok := data["currency_symbol"].(string); !ok || sym == "" {
		t.Errorf("currency_symbol 应为非空字符串，实际 %v", data["currency_symbol"])
	}
}

// TestHealthzReturnsStatusFields 覆盖 P3-16：健康检查接口成功用例，
// 断言 status 与 usage_log_dropped 字段齐全（不在 /api 信封路径下，独立校验）。
func TestHealthzReturnsStatusFields(t *testing.T) {
	e := newTestEnv(t)
	resp, env := e.do(t, e.client(t), "GET", "/healthz", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("健康检查接口应 200，实际 %d %v", resp.StatusCode, env)
	}
	data, ok := env["data"].(map[string]any)
	if !ok {
		t.Fatalf("健康检查响应 data 应为对象，实际 %v", env["data"])
	}
	if status, _ := data["status"].(string); status != "ok" {
		t.Errorf("健康检查 status 应为 ok，实际 %v", data["status"])
	}
	if _, exists := data["usage_log_dropped"]; !exists {
		t.Error("健康检查响应缺少 usage_log_dropped 字段")
	}
}
