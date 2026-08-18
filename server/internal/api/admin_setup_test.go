package api

import (
	"net/http"
	"strconv"
	"testing"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
)

// setupChecks 取配置引导响应中「检查项标识 → 是否完成」的映射。
func setupChecks(t *testing.T, env map[string]any) (map[string]bool, bool) {
	t.Helper()
	data, ok := env["data"].(map[string]any)
	if !ok {
		t.Fatalf("配置引导响应缺少 data：%v", env)
	}
	raw, ok := data["checks"].([]any)
	if !ok {
		t.Fatalf("配置引导响应缺少 checks：%v", data)
	}
	done := make(map[string]bool, len(raw))
	for _, item := range raw {
		c, _ := item.(map[string]any)
		key, _ := c["key"].(string)
		done[key], _ = c["done"].(bool)
	}
	completed, _ := data["completed"].(bool)
	return done, completed
}

// 新装系统（无渠道、无模型、无员工、无积分、未填基址）的每一项必需检查都应为未完成，
// 且整体判定为未完成——这正是管理员打开管理端时需要看到的待办。
func TestSetupStatusOnFreshInstall(t *testing.T) {
	e := newTestEnv(t)
	adminC := e.seedAndLogin(t, "setupadmin", domain.RoleAdmin)

	resp, env := e.do(t, adminC, "GET", "/api/admin/setup-status", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("查询配置引导应 200，实际 %d：%v", resp.StatusCode, env)
	}
	done, completed := setupChecks(t, env)
	if completed {
		t.Errorf("新装系统不应判定为配置完成：%v", done)
	}
	for _, key := range []domain.SetupCheck{
		domain.SetupCheckChannel, domain.SetupCheckModel, domain.SetupCheckModelServable,
		domain.SetupCheckMember, domain.SetupCheckCredits, domain.SetupCheckServerAddress,
		domain.SetupCheckAlertChannel,
	} {
		if _, ok := done[string(key)]; !ok {
			t.Errorf("检查项 %s 缺失：%v", key, done)
		}
		if done[string(key)] {
			t.Errorf("新装系统的检查项 %s 不应为已完成", key)
		}
	}
}

// 逐项配置到位后各检查项依次转为已完成，全部必需项完成时整体判定为已完成。
// 其中「模型已上架但无渠道承载」是新装最常见的半成品状态，须单独可辨识。
func TestSetupStatusTurnsCompleteAsAdminConfigures(t *testing.T) {
	e := newTestEnv(t)
	rootC := e.seedAndLogin(t, "setuproot", domain.RoleRoot)

	// 上架一个模型，但渠道承载的是另一个模型名：model 完成、model_servable 未完成。
	if _, env := e.do(t, rootC, "POST", "/api/admin/models", map[string]any{
		"name": "setup-model", "display_name": "配置引导测试模型",
		"modality": "text", "billing_mode": "per_token", "status": "enabled",
	}); env["success"] != true {
		t.Fatalf("上架模型失败：%v", env)
	}
	if _, env := e.do(t, rootC, "POST", "/api/admin/channels", map[string]any{
		"name": "setup-ch", "provider": "openai", "protocol": "openai_compat",
		"base_url": "http://127.0.0.1:1", "api_key": "sk-upstream",
		"models": []string{"other-model"},
	}); env["success"] != true {
		t.Fatalf("新建渠道失败：%v", env)
	}
	_, env := e.do(t, rootC, "GET", "/api/admin/setup-status", nil)
	done, completed := setupChecks(t, env)
	if !done[string(domain.SetupCheckChannel)] || !done[string(domain.SetupCheckModel)] {
		t.Errorf("已建渠道与模型后对应检查项应完成：%v", done)
	}
	if done[string(domain.SetupCheckModelServable)] {
		t.Error("渠道未承载已上架模型时，model_servable 不应判定为完成")
	}
	if completed {
		t.Error("尚有必需项未完成时整体不应判定为完成")
	}

	// 把该模型加入渠道清单，并建员工、发积分、填基址。
	if _, env := e.do(t, rootC, "PUT", "/api/admin/channels/1", map[string]any{
		"name": "setup-ch", "provider": "openai", "protocol": "openai_compat",
		"base_url": "http://127.0.0.1:1", "api_key": "sk-upstream",
		"models": []string{"setup-model"},
	}); env["success"] != true {
		t.Fatalf("更新渠道模型清单失败：%v", env)
	}
	if _, env := e.do(t, rootC, "POST", "/api/admin/users", map[string]any{
		"username": "setupmember", "password": "member-password-1", "role": "user",
	}); env["success"] != true {
		t.Fatalf("新建员工账号失败：%v", env)
	}
	memberID := userIDOf(t, e, "setupmember")
	if _, env := e.do(t, rootC, "POST",
		"/api/admin/users/"+strconv.FormatInt(memberID, 10)+"/credits", map[string]any{
			"amount": 500_000, "note": "配置引导测试",
		}); env["success"] != true {
		t.Fatalf("发放积分失败：%v", env)
	}
	if _, env := e.do(t, rootC, "PUT", "/api/admin/settings", map[string]any{
		"key": "server_address", "value": "https://api.example.com",
	}); env["success"] != true {
		t.Fatalf("写入对外 API 基址失败：%v", env)
	}

	_, env = e.do(t, rootC, "GET", "/api/admin/setup-status", nil)
	done, completed = setupChecks(t, env)
	if !completed {
		t.Errorf("必需项全部配置到位后应判定为完成：%v", done)
	}
	// 告警通道非必需项：未配置也不阻断整体判定，但仍如实报告未完成。
	if done[string(domain.SetupCheckAlertChannel)] {
		t.Error("未配置任何告警通道时 alert_channel 不应为已完成")
	}
}
