package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// 模型可用性（P3-10）补充用例：下架模型回归、管理端筛选分页、
// Key 白名单交互、可用性实时性、目录标记与中继路由口径一致性。

// A2：disabled 状态的模型无论是否有渠道承载均不出现在公开目录
// （既有仅返回 enabled 模型的行为不被破坏）。
func TestPublicModelsExcludeDisabledModels(t *testing.T) {
	e := newTestEnv(t)
	e.seedModel(t, "live-model")
	// 下架模型：即使有启用渠道承载也不应出现
	m := &store.Model{Name: "retired-model", Modality: domain.ModalityText,
		BillingMode: domain.BillPerToken, Status: domain.ModelDisabled}
	if err := e.db.Create(m).Error; err != nil {
		t.Fatalf("建下架模型失败: %v", err)
	}
	e.seedChannel(t, "ch-both", "http://unused.example", 0,
		[]string{"live-model", "retired-model"}, nil)

	resp, env := e.do(t, catalogClient(t, e), "GET", "/api/me/models", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("公开目录应 200，实际 %d %v", resp.StatusCode, env)
	}
	items, _ := env["data"].([]any)
	if len(items) != 1 {
		t.Fatalf("公开目录应只含 1 个 enabled 模型，实际: %v", env["data"])
	}
	got := items[0].(map[string]any)
	if got["name"] != "live-model" {
		t.Errorf("目录应只含 live-model，实际 %v", got["name"])
	}
	if got["available"] != true {
		t.Errorf("live-model 有渠道承载，available 应为 true，实际 %v", got["available"])
	}
}

// A4：管理端模型列表带 keyword 与分页参数时，筛选与分页行为不变，
// 返回条目均带 channel_count。
func TestAdminModelListKeywordAndPagination(t *testing.T) {
	e := newTestEnv(t)
	adminC := e.seedAndLogin(t, "pageadmin", domain.RoleAdmin)
	e.seedModel(t, "alpha-one")
	e.seedModel(t, "alpha-two")
	e.seedModel(t, "beta-x")
	e.seedChannel(t, "ch-alpha", "http://unused.example", 0, []string{"alpha-one"}, nil)

	resp, env := e.do(t, adminC, "GET",
		"/api/admin/models/?keyword=alpha&page=1&page_size=1", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("管理端模型列表应 200，实际 %d %v", resp.StatusCode, env)
	}
	page := env["data"].(map[string]any)
	if int(page["total"].(float64)) != 2 {
		t.Errorf("keyword=alpha 应命中 2 个模型，实际 total=%v", page["total"])
	}
	items := page["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("page_size=1 应只返回 1 条，实际 %d 条", len(items))
	}
	// List 按 name 排序，第一页应为 alpha-one，且带正确的 channel_count
	first := items[0].(map[string]any)
	if first["name"] != "alpha-one" {
		t.Errorf("第一页应为 alpha-one，实际 %v", first["name"])
	}
	cnt, ok := first["channel_count"].(float64)
	if !ok || cnt != 1 {
		t.Errorf("alpha-one 的 channel_count 应为 1，实际 %v", first["channel_count"])
	}

	resp, env = e.do(t, adminC, "GET",
		"/api/admin/models/?keyword=alpha&page=2&page_size=1", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("第二页应 200，实际 %d %v", resp.StatusCode, env)
	}
	items = env["data"].(map[string]any)["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("第二页应返回 1 条，实际 %d 条", len(items))
	}
	second := items[0].(map[string]any)
	if second["name"] != "alpha-two" {
		t.Errorf("第二页应为 alpha-two，实际 %v", second["name"])
	}
	cnt, ok = second["channel_count"].(float64)
	if !ok || cnt != 0 {
		t.Errorf("alpha-two 无渠道承载，channel_count 应为 0，实际 %v", second["channel_count"])
	}
}

// A6：Key 白名单含模型 X（有渠道）与 Y（无渠道）→ /v1/models 仅返回 X
// （白名单 ∩ 有渠道承载的交集）。
func TestV1ModelsWhitelistIntersectCarriage(t *testing.T) {
	e := newTestEnv(t)
	userID, key := e.seedRelayUser(t, "wl-user", 1_000, nil)
	e.seedModel(t, "wl-carried")
	e.seedModel(t, "wl-orphan")
	e.seedModel(t, "outside-whitelist")
	e.seedChannel(t, "ch-wl", "http://unused.example", 0,
		[]string{"wl-carried", "outside-whitelist"}, nil)
	e.db.Model(&store.APIKey{}).Where("user_id = ?", userID).
		Update("allowed_models", []byte(`["wl-carried","wl-orphan"]`))

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
	if len(data) != 1 || data[0].(map[string]any)["id"] != "wl-carried" {
		t.Errorf("应只返回白名单内且有渠道承载的 wl-carried，实际 %v", data)
	}
}

// A7：可用性实时性——经 PUT /admin/channels/{id}/status 禁用唯一承载渠道后，
// 公开目录立即反映 available=false（无过期缓存）。
func TestPublicModelsAvailabilityReflectsChannelStatusChange(t *testing.T) {
	e := newTestEnv(t)
	adminC := e.seedAndLogin(t, "flipadmin", domain.RoleAdmin)
	e.seedModel(t, "flip-model")
	chID := e.seedChannel(t, "ch-flip", "http://unused.example", 0,
		[]string{"flip-model"}, nil)

	availOf := func(t *testing.T) bool {
		t.Helper()
		resp, env := e.do(t, catalogClient(t, e), "GET", "/api/me/models", nil)
		if resp.StatusCode != 200 {
			t.Fatalf("公开目录应 200，实际 %d %v", resp.StatusCode, env)
		}
		for _, it := range env["data"].([]any) {
			m := it.(map[string]any)
			if m["name"] == "flip-model" {
				return m["available"].(bool)
			}
		}
		t.Fatal("公开目录未找到 flip-model")
		return false
	}

	if !availOf(t) {
		t.Fatal("禁用渠道前 flip-model 应 available=true")
	}
	resp, env := e.do(t, adminC, "PUT",
		"/api/admin/channels/"+strconv.FormatInt(chID, 10)+"/status",
		map[string]any{"status": "manual_disabled"})
	if resp.StatusCode != 200 {
		t.Fatalf("禁用渠道应 200，实际 %d %v", resp.StatusCode, env)
	}
	if availOf(t) {
		t.Error("禁用唯一承载渠道后 flip-model 应 available=false")
	}
}

// A8：口径一致性——公开目录 available=false 的模型，
// POST /v1/chat/completions 调用返回 503 no_channel。
func TestUnavailableModelRelayConsistency(t *testing.T) {
	e := newTestEnv(t)
	_, key := e.seedRelayUser(t, "consist-user", 100_000, nil)
	e.seedModel(t, "dead-model") // 上架但无渠道承载

	resp, env := e.do(t, catalogClient(t, e), "GET", "/api/me/models", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("公开目录应 200，实际 %d %v", resp.StatusCode, env)
	}
	found := false
	for _, it := range env["data"].([]any) {
		m := it.(map[string]any)
		if m["name"] == "dead-model" {
			found = true
			if m["available"] != false {
				t.Fatalf("dead-model 应 available=false，实际 %v", m["available"])
			}
		}
	}
	if !found {
		t.Fatal("公开目录未找到 dead-model")
	}

	relayResp, raw := e.relayPost(t, key, map[string]any{
		"model": "dead-model", "messages": []map[string]any{{"role": "user", "content": "hi"}},
	})
	if relayResp.StatusCode != 503 {
		t.Fatalf("不可用模型调用应 503，实际 %d %s", relayResp.StatusCode, raw)
	}
	if !strings.Contains(string(raw), "no_channel") {
		t.Errorf("业务码应为 no_channel，实际 %s", raw)
	}
}
