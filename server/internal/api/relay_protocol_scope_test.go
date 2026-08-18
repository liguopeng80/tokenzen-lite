package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// 混合渠道不误伤：向量模型同时绑定 anthropic 渠道与 openai_compat 渠道时，
// 协议过滤只排除 anthropic 渠道，请求经 openai_compat 渠道成功结算（报告 P3-7）。
func TestEmbeddingsMixedProtocolChannels(t *testing.T) {
	e := newTestEnv(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"object":"list","model":"emb-upstream",
			"data":[{"object":"embedding","index":0,"embedding":[0.1,0.2]}],
			"usage":{"prompt_tokens":100,"total_tokens":100}}`)
	}))
	defer upstream.Close()

	userID, key := e.seedRelayUser(t, "embmixed", 100_000, nil)
	m := &store.Model{Name: "text-emb-mixed", Modality: domain.ModalityEmbedding,
		BillingMode: domain.BillPerToken, Status: domain.ModelEnabled}
	if err := e.db.Create(m).Error; err != nil {
		t.Fatalf("建模型失败: %v", err)
	}
	if err := e.db.Create(&store.ModelPrice{ModelID: m.ID, InputPrice: 500_000}).Error; err != nil {
		t.Fatalf("建价格失败: %v", err)
	}
	// anthropic 协议渠道（不可达地址）+ openai_compat 协议渠道（httptest 上游）
	e.seedChannelProto(t, "emb-mixed-anth", "http://127.0.0.1:1",
		domain.ProtocolAnthropic, []string{"text-emb-mixed"}, nil)
	e.seedChannel(t, "emb-mixed-oai", upstream.URL, 0, []string{"text-emb-mixed"}, nil)

	resp, raw := e.v1Request(t, "POST", "/v1/embeddings",
		map[string]any{"model": "text-emb-mixed", "input": "文本"},
		map[string]string{"Authorization": "Bearer " + key})
	if resp.StatusCode != 200 {
		t.Fatalf("存在 openai_compat 渠道时应成功，实际 %d %s", resp.StatusCode, raw)
	}
	// 100 token × 0.5 = 50 积分
	if bal := e.userBalance(t, userID); bal != 100_000-50 {
		t.Errorf("期望余额 %d，实际 %d", 100_000-50, bal)
	}
	log := e.waitUsageLog(t, userID)
	if log.Status != domain.UsageSettled || log.CreditsCharged != 50 {
		t.Errorf("应经 openai_compat 渠道结算 50 积分，实际 status=%s charged=%d",
			log.Status, log.CreditsCharged)
	}
	e.assertReconcile(t)
}

// 图像端点镜像用例：per_call 图像模型仅绑定 gemini 协议渠道时，
// /v1/images/generations 返回 503 channel_protocol_unsupported（区别于
// no_channel），预扣全额退款、用量日志 refunded（报告 P3-7）。
func TestImagesProtocolScope(t *testing.T) {
	e := newTestEnv(t)
	userID, key := e.seedRelayUser(t, "imgscope", 1_000_000, nil)
	m := &store.Model{Name: "img-scope", Modality: domain.ModalityImage,
		BillingMode: domain.BillPerCall, Status: domain.ModelEnabled}
	if err := e.db.Create(m).Error; err != nil {
		t.Fatalf("建模型失败: %v", err)
	}
	if err := e.db.Create(&store.ModelPrice{ModelID: m.ID, PerCallPrice: 40_000}).Error; err != nil {
		t.Fatalf("建价格失败: %v", err)
	}
	// 唯一渠道是 gemini 协议
	e.seedChannelProto(t, "img-gem-ch", "http://127.0.0.1:1",
		domain.ProtocolGemini, []string{"img-scope"}, nil)

	resp, raw := e.v1Request(t, "POST", "/v1/images/generations",
		map[string]any{"model": "img-scope", "prompt": "一只猫", "n": 1},
		map[string]string{"Authorization": "Bearer " + key})
	if resp.StatusCode != 503 {
		t.Fatalf("仅 gemini 渠道时 images 应 503，实际 %d %s", resp.StatusCode, raw)
	}
	if !strings.Contains(string(raw), "channel_protocol_unsupported") {
		t.Errorf("业务码应为 channel_protocol_unsupported，实际 %s", raw)
	}
	if bal := e.userBalance(t, userID); bal != 1_000_000 {
		t.Errorf("应全额退款，期望余额 1000000，实际 %d", bal)
	}
	log := e.waitUsageLog(t, userID)
	if log.Status != domain.UsageRefunded {
		t.Errorf("用量日志应 refunded，实际 %s", log.Status)
	}
	e.assertReconcile(t)
}
