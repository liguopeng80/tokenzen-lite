package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// 模型可用性（P3-10）：公开目录与 /v1/models 必须反映是否存在启用渠道承载，
// 管理端模型列表附带承载渠道数。

// TestPublicModelsAvailabilityFlag 公开目录为每个模型返回 available 标记：
// 有启用渠道承载 → true；无渠道、或仅被禁用渠道承载 → false。
func TestPublicModelsAvailabilityFlag(t *testing.T) {
	e := newTestEnv(t)
	e.seedModel(t, "carried-model")
	e.seedModel(t, "orphan-model")
	e.seedModel(t, "disabled-carrier-model")
	e.seedChannel(t, "ch-live", "http://unused.example", 0, []string{"carried-model"}, nil)
	deadID := e.seedChannel(t, "ch-dead", "http://unused.example", 0,
		[]string{"disabled-carrier-model"}, nil)
	e.db.Model(&store.Channel{}).Where("id = ?", deadID).
		Update("status", domain.ChannelManualDisabled)

	resp, env := e.do(t, catalogClient(t, e), "GET", "/api/me/models", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("公开目录应 200，实际 %d %v", resp.StatusCode, env)
	}
	items, ok := env["data"].([]any)
	if !ok || len(items) != 3 {
		t.Fatalf("公开目录应含 3 个模型（不因不可用而剔除），实际: %v", env["data"])
	}
	got := map[string]bool{}
	for _, it := range items {
		m := it.(map[string]any)
		avail, ok := m["available"].(bool)
		if !ok {
			t.Fatalf("条目应含 available 布尔标记，实际: %v", m)
		}
		got[m["name"].(string)] = avail
	}
	want := map[string]bool{
		"carried-model":          true,
		"orphan-model":           false,
		"disabled-carrier-model": false,
	}
	for name, avail := range want {
		if got[name] != avail {
			t.Errorf("模型 %s 的 available 应为 %v，实际 %v", name, avail, got[name])
		}
	}
}

// TestV1ModelsFilteredByChannelCarriage /v1/models 只返回存在启用渠道承载的模型。
func TestV1ModelsFilteredByChannelCarriage(t *testing.T) {
	e := newTestEnv(t)
	_, key := e.seedRelayUser(t, "carriage-user", 1_000, nil)
	e.seedModel(t, "carried-model")
	e.seedModel(t, "orphan-model")
	e.seedChannel(t, "ch-live", "http://unused.example", 0, []string{"carried-model"}, nil)

	req, _ := http.NewRequest("GET", e.srv.URL+"/v1/models", nil)
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("请求 /v1/models 失败: %v", err)
	}
	defer resp.Body.Close()
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if resp.StatusCode != 200 {
		t.Fatalf("/v1/models 应 200，实际 %d %v", resp.StatusCode, body)
	}
	data, _ := body["data"].([]any)
	if len(data) != 1 || data[0].(map[string]any)["id"] != "carried-model" {
		t.Errorf("/v1/models 应只含有启用渠道承载的模型，实际 %v", data)
	}
}

// TestAdminModelListChannelCount 管理端模型列表返回启用渠道承载数，
// 禁用渠道不计入。
func TestAdminModelListChannelCount(t *testing.T) {
	e := newTestEnv(t)
	adminC := e.seedAndLogin(t, "countadmin", domain.RoleAdmin)
	e.seedModel(t, "carried-model")
	e.seedModel(t, "orphan-model")
	e.seedChannel(t, "ch-a", "http://unused.example", 0, []string{"carried-model"}, nil)
	e.seedChannel(t, "ch-b", "http://unused.example", 0, []string{"carried-model"}, nil)
	deadID := e.seedChannel(t, "ch-dead", "http://unused.example", 0,
		[]string{"carried-model"}, nil)
	e.db.Model(&store.Channel{}).Where("id = ?", deadID).
		Update("status", domain.ChannelManualDisabled)

	resp, env := e.do(t, adminC, "GET", "/api/admin/models/", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("管理端模型列表应 200，实际 %d %v", resp.StatusCode, env)
	}
	page := env["data"].(map[string]any)
	got := map[string]float64{}
	for _, it := range page["items"].([]any) {
		m := it.(map[string]any)
		cnt, ok := m["channel_count"].(float64)
		if !ok {
			t.Fatalf("条目应含 channel_count 字段，实际: %v", m)
		}
		got[m["name"].(string)] = cnt
	}
	if got["carried-model"] != 2 {
		t.Errorf("carried-model 承载渠道数应为 2（禁用渠道不计入），实际 %v", got["carried-model"])
	}
	if got["orphan-model"] != 0 {
		t.Errorf("orphan-model 承载渠道数应为 0，实际 %v", got["orphan-model"])
	}
}
