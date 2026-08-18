package api

import (
	"fmt"
	"testing"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
)

// M2 API 流程：管理员分配积分 → 建模型与定价 → 生成兑换码 → 用户兑换 → 余额与流水核对。
func TestBillingFlow(t *testing.T) {
	e := newTestEnv(t)
	rootC := e.seedAndLogin(t, "root1", domain.RoleRoot)
	userC := e.seedAndLogin(t, "buyer", domain.RoleUser)

	// 1. 管理员给用户分配 100 万积分
	resp, env := e.do(t, rootC, "POST", "/api/admin/users/2/credits",
		map[string]any{"amount": 1_000_000, "note": "开通额度"})
	if resp.StatusCode != 200 {
		t.Fatalf("分配积分失败: %d %v", resp.StatusCode, env)
	}

	// 2. 建模型 + 定价 + 时段倍率
	resp, env = e.do(t, rootC, "POST", "/api/admin/models/", map[string]any{
		"name": "glm-5", "display_name": "GLM-5", "modality": "text",
	})
	if resp.StatusCode != 201 {
		t.Fatalf("创建模型失败: %d %v", resp.StatusCode, env)
	}
	modelID := int64(env["data"].(map[string]any)["id"].(float64))

	resp, _ = e.do(t, rootC, "PUT", fmt.Sprintf("/api/admin/models/%d/price", modelID),
		map[string]any{"input_price": 720_000, "output_price": 2_300_000})
	if resp.StatusCode != 200 {
		t.Fatalf("设置价格失败: %d", resp.StatusCode)
	}
	resp, _ = e.do(t, rootC, "PUT", fmt.Sprintf("/api/admin/models/%d/peak-rules", modelID),
		map[string]any{"rules": []map[string]any{{
			"timezone": "Asia/Shanghai", "start_minute": 840, "end_minute": 1080,
			"days_of_week": []int{1, 2, 3, 4, 5, 6, 7}, "multiplier_percent": 300, "enabled": true,
		}}})
	if resp.StatusCode != 200 {
		t.Fatalf("设置时段规则失败: %d", resp.StatusCode)
	}

	// 3. 公开模型目录可见（含价格与规则）
	resp, env = e.do(t, catalogClient(t, e), "GET", "/api/me/models", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("公开模型目录失败: %d", resp.StatusCode)
	}
	list := env["data"].([]any)
	if len(list) != 1 {
		t.Fatalf("公开目录应有 1 个模型，实际 %d", len(list))
	}
	m0 := list[0].(map[string]any)
	if m0["price"] == nil || m0["peak_rules"] == nil {
		t.Errorf("公开目录应含价格与时段规则: %v", m0)
	}

	// 4. 生成兑换码并由用户兑换
	resp, env = e.do(t, rootC, "POST", "/api/admin/redemptions/batch",
		map[string]any{"count": 2, "credits": 500_000, "name": "测试卡"})
	if resp.StatusCode != 201 {
		t.Fatalf("生成兑换码失败: %d %v", resp.StatusCode, env)
	}
	codes := env["data"].(map[string]any)["codes"].([]any)
	if len(codes) != 2 {
		t.Fatalf("应返回 2 个明文兑换码，实际 %d", len(codes))
	}
	code := codes[0].(string)

	resp, _ = e.do(t, userC, "POST", "/api/me/redeem", map[string]string{"code": code})
	if resp.StatusCode != 200 {
		t.Fatalf("兑换失败: %d", resp.StatusCode)
	}
	// 重复兑换同一码应失败
	resp, _ = e.do(t, userC, "POST", "/api/me/redeem", map[string]string{"code": code})
	if resp.StatusCode != 400 {
		t.Errorf("重复兑换应 400，实际 %d", resp.StatusCode)
	}

	// 5. 余额 = 分配 100 万 + 兑换 50 万
	resp, env = e.do(t, userC, "GET", "/api/me/balance", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("查询余额失败: %d", resp.StatusCode)
	}
	balance := env["data"].(map[string]any)
	if int64(balance["credit_balance"].(float64)) != 1_500_000 {
		t.Errorf("期望余额 1500000，实际 %v", balance["credit_balance"])
	}
	if int64(balance["exchange_rate_credits_per_cny"].(float64)) != 1_000_000 {
		t.Errorf("默认兑换率应为 1000000，实际 %v", balance["exchange_rate_credits_per_cny"])
	}

	// 6. 用户流水应有 2 条（grant + redeem）
	resp, env = e.do(t, userC, "GET", "/api/me/ledger", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("查询流水失败: %d", resp.StatusCode)
	}
	if total := env["data"].(map[string]any)["total"].(float64); int(total) != 2 {
		t.Errorf("流水应 2 条，实际 %v", total)
	}

	// 7. 设置接口：root 可改兑换率，admin 不可
	resp, _ = e.do(t, rootC, "PUT", "/api/admin/settings",
		map[string]any{"key": "exchange_rate_credits_per_cny", "value": 2_000_000})
	if resp.StatusCode != 200 {
		t.Fatalf("root 更新设置失败: %d", resp.StatusCode)
	}
	adminC := e.seedAndLogin(t, "ops", domain.RoleAdmin)
	resp, _ = e.do(t, adminC, "GET", "/api/admin/settings", nil)
	if resp.StatusCode != 403 {
		t.Errorf("admin 访问设置应 403，实际 %d", resp.StatusCode)
	}
	// 非法值被拒
	resp, _ = e.do(t, rootC, "PUT", "/api/admin/settings",
		map[string]any{"key": "exchange_rate_credits_per_cny", "value": -5})
	if resp.StatusCode != 400 {
		t.Errorf("非法兑换率应 400，实际 %d", resp.StatusCode)
	}
	resp, _ = e.do(t, rootC, "PUT", "/api/admin/settings",
		map[string]any{"key": "unknown_key", "value": 1})
	if resp.StatusCode != 400 {
		t.Errorf("未注册键应 400，实际 %d", resp.StatusCode)
	}
}
