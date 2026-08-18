package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// TestRelayAliasResolvesToRealModel 全局别名解析：调用方用短名 opus 调用，
// 中继解析为真实模型名 claude-opus-5，按真实名路由渠道、计费，用量日志记真实名。
//
// 解析顺序（docs/glossary.md「模型能力属性」节）：
// 请求 model → 全局别名 → 渠道 model_mapping → 上游型号。
// 本测试验证第一层（全局别名）生效，下游全部自然走真实名。
func TestRelayAliasResolvesToRealModel(t *testing.T) {
	e := newTestEnv(t)
	var gotBody map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"x","model":"upstream-opus","choices":[{"message":{"content":"hi"}}],
			"usage":{"prompt_tokens":100,"completion_tokens":50}}`)
	}))
	defer upstream.Close()

	userID, key := e.seedRelayUser(t, "aliasuser", 1_000_000, nil)

	// 建真实模型 claude-opus-5，设置 alias=opus。
	m := &store.Model{
		Name: "claude-opus-5", Modality: domain.ModalityText,
		BillingMode: domain.BillPerToken, Status: domain.ModelEnabled,
		Alias: "opus", Provider: "anthropic", Capabilities: []byte("[]"),
	}
	if err := e.db.Create(m).Error; err != nil {
		t.Fatalf("建模型失败: %v", err)
	}
	price := &store.ModelPrice{ModelID: m.ID, InputPrice: 1_000_000, OutputPrice: 2_000_000}
	if err := e.db.Create(price).Error; err != nil {
		t.Fatalf("建价格失败: %v", err)
	}

	// 渠道承载真实模型名 claude-opus-5（不是别名 opus）。
	e.seedChannel(t, "ch-alias", upstream.URL, 0, []string{"claude-opus-5"},
		map[string]string{"claude-opus-5": "upstream-opus"})

	// 用别名 opus 调用。
	resp, body := e.relayPost(t, key, map[string]any{
		"model":      "opus",
		"messages":   []map[string]any{{"role": "user", "content": "你好"}},
		"max_tokens": 100,
	})
	if resp.StatusCode != 200 {
		t.Fatalf("用别名调用应成功: %d %s", resp.StatusCode, body)
	}

	// 上游收到映射后的模型名（真实名 → 渠道映射 → 上游型号）。
	if gotBody["model"] != "upstream-opus" {
		t.Errorf("上游应收到映射后的模型名 upstream-opus，实际 %v", gotBody["model"])
	}

	// 用量日志应记真实模型名 claude-opus-5（不是别名 opus）。
	log := e.waitUsageLog(t, userID)
	if log.ModelName != "claude-opus-5" {
		t.Errorf("用量日志应记真实名 claude-opus-5，实际 %s", log.ModelName)
	}
	if log.Status != domain.UsageSettled {
		t.Errorf("用量日志应 settled，实际 %s", log.Status)
	}
	e.assertReconcile(t)
}
