package api

import (
	"net/http"
	"testing"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// serviceStatusOf 以登录用户身份读取服务状态。
func serviceStatusOf(t *testing.T, e *testEnv, c *http.Client) map[string]any {
	t.Helper()
	resp, env := e.do(t, c, "GET", "/api/me/service-status", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("查服务状态应 200，实际 %d：%v", resp.StatusCode, env)
	}
	return env["data"].(map[string]any)
}

// 全部已上架模型都有启用渠道承载时，状态为正常。
func TestServiceStatusOperationalWhenAllModelsCarried(t *testing.T) {
	e := newTestEnv(t)
	c := e.seedAndLogin(t, "statusreader", domain.RoleUser)
	e.seedModel(t, "status-carried")
	e.seedChannel(t, "status-ch", "http://unused.example", 0, []string{"status-carried"}, nil)

	data := serviceStatusOf(t, e, c)
	if data["status"] != "operational" {
		t.Errorf("应为正常，实际 %v", data["status"])
	}
	if int(data["models_total"].(float64)) != 1 || int(data["models_available"].(float64)) != 1 {
		t.Errorf("模型可用数不符：%v", data)
	}
	if len(data["unavailable_models"].([]any)) != 0 {
		t.Errorf("不应有不可用模型：%v", data["unavailable_models"])
	}
}

// 有模型没有渠道承载时状态降级，并明确列出这些模型——员工调用它们必然被拒。
func TestServiceStatusDegradedWhenModelUncarried(t *testing.T) {
	e := newTestEnv(t)
	c := e.seedAndLogin(t, "statusdegraded", domain.RoleUser)
	e.seedModel(t, "status-carried-2")
	e.seedModel(t, "status-orphan")
	e.seedChannel(t, "status-ch-2", "http://unused.example", 0, []string{"status-carried-2"}, nil)

	data := serviceStatusOf(t, e, c)
	if data["status"] != "degraded" {
		t.Errorf("应为部分受影响，实际 %v", data["status"])
	}
	names := data["unavailable_models"].([]any)
	if len(names) != 1 || names[0] != "status-orphan" {
		t.Errorf("应列出无渠道承载的模型，实际 %v", names)
	}
}

// 一个可用模型都没有时是中断：此时任何调用都会失败。
func TestServiceStatusOutageWithoutAnyCarrier(t *testing.T) {
	e := newTestEnv(t)
	c := e.seedAndLogin(t, "statusoutage", domain.RoleUser)
	e.seedModel(t, "status-alone")

	data := serviceStatusOf(t, e, c)
	if data["status"] != "outage" {
		t.Errorf("应为中断，实际 %v", data["status"])
	}
}

// 厂商级汇总只给数量与状态，不泄露渠道名称、地址与优先级。
func TestServiceStatusHidesChannelConfiguration(t *testing.T) {
	e := newTestEnv(t)
	c := e.seedAndLogin(t, "statusprivacy", domain.RoleUser)
	e.seedModel(t, "status-model-3")
	e.seedChannel(t, "机密渠道名", "http://secret.internal.example", 0,
		[]string{"status-model-3"}, nil)
	deadID := e.seedChannel(t, "另一渠道", "http://unused.example", 0,
		[]string{"status-model-3"}, nil)
	if err := e.db.Model(&store.Channel{}).Where("id = ?", deadID).
		Update("status", domain.ChannelAutoDisabled).Error; err != nil {
		t.Fatalf("置为自动禁用失败：%v", err)
	}

	data := serviceStatusOf(t, e, c)
	providers := data["providers"].([]any)
	if len(providers) != 1 {
		t.Fatalf("两个渠道同厂商应聚合为一行，实际 %d 行", len(providers))
	}
	row := providers[0].(map[string]any)
	if int(row["total"].(float64)) != 2 || int(row["enabled"].(float64)) != 1 {
		t.Errorf("通道数量统计不符：%v", row)
	}
	if int(row["auto_disabled"].(float64)) != 1 {
		t.Errorf("应统计出自动禁用的通道数：%v", row)
	}
	if row["status"] != "degraded" {
		t.Errorf("有通道被自动禁用时该厂商应为受影响，实际 %v", row["status"])
	}
	for _, forbidden := range []string{"name", "base_url", "priority", "weight"} {
		if _, leaked := row[forbidden]; leaked {
			t.Errorf("厂商汇总不应包含渠道配置字段 %s：%v", forbidden, row)
		}
	}
}

// 服务状态需要登录：这是本站的运行情况，不对未登录访问者开放。
func TestServiceStatusRequiresLogin(t *testing.T) {
	e := newTestEnv(t)
	if resp, _ := e.do(t, e.client(t), "GET", "/api/me/service-status", nil); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("未登录访问应 401，实际 %d", resp.StatusCode)
	}
}
