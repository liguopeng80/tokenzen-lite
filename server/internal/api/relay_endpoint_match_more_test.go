package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// 端点与计费方式匹配校验的补充用例：简化中继端点（embeddings/images）上的
// 计费方式不匹配分支、无定价模型的计费方式变更放行、遗留全零单价数据的现状记录。

// I5：形态匹配（image）但按 token 计费的图像模型调 /v1/images/generations，
// 应命中简化中继端点上的计费方式不匹配分支：400 model_endpoint_mismatch，余额不变。
func TestPerTokenImageModelRejectedOnImagesEndpoint(t *testing.T) {
	e := newTestEnv(t)
	userID, key := e.seedRelayUser(t, "epm-img-tok", 1_000_000, nil)
	m := &store.Model{Name: "img-token-model", Modality: domain.ModalityImage,
		BillingMode: domain.BillPerToken, Status: domain.ModelEnabled}
	e.db.Create(m)
	e.db.Create(&store.ModelPrice{ModelID: m.ID, InputPrice: 500_000})

	status, body := e.postV1(t, key, "/v1/images/generations", map[string]any{
		"model": "img-token-model", "prompt": "一只猫",
	})
	assertEndpointMismatch(t, status, body, "计费方式")
	if bal := e.userBalance(t, userID); bal != 1_000_000 {
		t.Errorf("拒绝的请求不应扣积分，余额期望 1000000，实际 %d", bal)
	}
	e.assertReconcile(t)
}

// I6：形态匹配（embedding）但按次计费的向量模型调 /v1/embeddings，
// 应 400 model_endpoint_mismatch，余额不变。
func TestPerCallEmbeddingModelRejectedOnEmbeddingsEndpoint(t *testing.T) {
	e := newTestEnv(t)
	userID, key := e.seedRelayUser(t, "epm-emb-call", 1_000_000, nil)
	m := &store.Model{Name: "emb-percall-model", Modality: domain.ModalityEmbedding,
		BillingMode: domain.BillPerCall, Status: domain.ModelEnabled}
	e.db.Create(m)
	e.db.Create(&store.ModelPrice{ModelID: m.ID, PerCallPrice: 40_000})

	status, body := e.postV1(t, key, "/v1/embeddings", map[string]any{
		"model": "emb-percall-model", "input": "文本",
	})
	assertEndpointMismatch(t, status, body, "计费方式")
	if bal := e.userBalance(t, userID); bal != 1_000_000 {
		t.Errorf("拒绝的请求不应扣积分，余额期望 1000000，实际 %d", bal)
	}
	e.assertReconcile(t)
}

// I12：尚未配置定价（Price 为空）的模型变更计费方式应放行——
// 定价一致性校验只在已有价格行时生效，防止把新建模型的正常配置流程堵死。
func TestBillingModeChangeAllowedWhenNoPriceConfigured(t *testing.T) {
	e := newTestEnv(t)
	rootC := e.seedAndLogin(t, "epm-noprice-root", domain.RoleRoot)

	resp, env := e.do(t, rootC, "POST", "/api/admin/models/", map[string]any{
		"name": "noprice-flip", "modality": "text", "billing_mode": "per_token",
	})
	if resp.StatusCode != 201 {
		t.Fatalf("创建模型失败: %d %v", resp.StatusCode, env)
	}
	id := int64(env["data"].(map[string]any)["id"].(float64))

	resp, env = e.do(t, rootC, "PUT", fmt.Sprintf("/api/admin/models/%d", id), map[string]any{
		"name": "noprice-flip", "modality": "text", "billing_mode": "per_call",
	})
	if resp.StatusCode != 200 {
		t.Errorf("无定价模型变更计费方式应 200，实际 %d %v", resp.StatusCode, env)
	}
}

// I13：已下架模型调对话端点应 404 model_not_found——存在性校验先于形态/计费方式匹配，
// 下架模型即使形态与计费方式都不匹配也不应泄露 400 匹配细节。
func TestDisabledModelRelayReturns404(t *testing.T) {
	e := newTestEnv(t)
	userID, key := e.seedRelayUser(t, "epm-disabled", 1_000_000, nil)
	m := &store.Model{Name: "disabled-img-model", Modality: domain.ModalityImage,
		BillingMode: domain.BillPerCall, Status: domain.ModelDisabled}
	e.db.Create(m)
	e.db.Create(&store.ModelPrice{ModelID: m.ID, PerCallPrice: 40_000})

	status, body := e.postV1(t, key, "/v1/chat/completions", map[string]any{
		"model":    "disabled-img-model",
		"messages": []map[string]any{{"role": "user", "content": "hi"}},
	})
	if status != 404 {
		t.Fatalf("下架模型应 404，实际 %d %v", status, body)
	}
	errObj, _ := body["error"].(map[string]any)
	if errObj == nil {
		t.Fatalf("响应应含 error 对象，实际 %v", body)
	}
	code := errObj["code"]
	if code == nil {
		code = errObj["type"]
	}
	if code != "model_not_found" {
		t.Errorf("期望 model_not_found，实际 %v", code)
	}
	if bal := e.userBalance(t, userID); bal != 1_000_000 {
		t.Errorf("拒绝的请求不应扣积分，余额期望 1000000，实际 %d", bal)
	}
}

// I14：形态与计费方式均匹配但未配置价格行的模型调匹配端点 → 503 model_not_priced，不扣费。
func TestUnpricedModelReturns503NotPriced(t *testing.T) {
	e := newTestEnv(t)
	userID, key := e.seedRelayUser(t, "epm-unpriced", 1_000_000, nil)
	m := &store.Model{Name: "unpriced-model", Modality: domain.ModalityText,
		BillingMode: domain.BillPerToken, Status: domain.ModelEnabled}
	e.db.Create(m) // 不建价格行

	status, body := e.postV1(t, key, "/v1/chat/completions", map[string]any{
		"model":    "unpriced-model",
		"messages": []map[string]any{{"role": "user", "content": "hi"}},
	})
	if status != 503 {
		t.Fatalf("未配置定价应 503，实际 %d %v", status, body)
	}
	errObj, _ := body["error"].(map[string]any)
	if errObj == nil {
		t.Fatalf("响应应含 error 对象，实际 %v", body)
	}
	code := errObj["code"]
	if code == nil {
		code = errObj["type"]
	}
	if code != "model_not_priced" {
		t.Errorf("期望 model_not_priced，实际 %v", code)
	}
	if bal := e.userBalance(t, userID); bal != 1_000_000 {
		t.Errorf("拒绝的请求不应扣积分，余额期望 1000000，实际 %d", bal)
	}
}

// I15（现状记录，对应残余风险）：管理端校验堵住了新增全零单价，但遗留数据
// （直接入库的 per_token 文本模型 + 全零 token 单价价格行）仍会以 0 积分放行中继。
// 本用例先固化当前行为（扣费为 0、请求成功），待补数据修复迁移或启动扫描后翻转断言。
// TODO: 落地遗留数据修复后，把断言改为拒绝服务（如 503 model_not_priced）。
func TestLegacyZeroPricePerTokenModelRelaysAtZeroCost(t *testing.T) {
	e := newTestEnv(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"x","model":"legacy-zero","choices":[{"message":{"content":"hi"}}],
			"usage":{"prompt_tokens":1000,"completion_tokens":500}}`)
	}))
	defer upstream.Close()

	userID, key := e.seedRelayUser(t, "epm-legacy-zero", 1_000_000, nil)
	m := &store.Model{Name: "legacy-zero", Modality: domain.ModalityText,
		BillingMode: domain.BillPerToken, Status: domain.ModelEnabled}
	e.db.Create(m)
	// 全零价格行：管理端 API 已拒绝这种写入，这里模拟历史遗留数据直接入库
	e.db.Create(&store.ModelPrice{ModelID: m.ID})
	e.seedChannel(t, "legacy-ch", upstream.URL, 0, []string{"legacy-zero"}, nil)

	resp, raw := e.relayPost(t, key, map[string]any{
		"model": "legacy-zero", "max_tokens": 100,
		"messages": []map[string]any{{"role": "user", "content": "hi"}},
	})
	if resp.StatusCode != 200 {
		t.Fatalf("现状：全零单价遗留模型应以 0 积分放行，实际 %d %s", resp.StatusCode, raw)
	}
	if bal := e.userBalance(t, userID); bal != 1_000_000 {
		t.Errorf("现状：扣费应为 0，余额期望 1000000，实际 %d", bal)
	}
	e.assertReconcile(t)
}
