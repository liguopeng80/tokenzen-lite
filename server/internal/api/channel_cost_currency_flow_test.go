package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// 跨渠道比价字段契约：GET /admin/models/{id}/channel-costs 每条记录含
// channel_id、currency、input_cost、output_cost、cache_read_cost、cache_write_cost、per_call_cost，
// 与 docs/api-contract.md 渠道成本端点的字段清单一致。
func TestModelChannelCostsFieldContract(t *testing.T) {
	e := newTestEnv(t)
	modelID := e.seedModel(t, "glm-5")
	ch1 := e.seedChannel(t, "cmp-ch-credits", "http://127.0.0.1:1", 0, []string{"glm-5"}, nil)
	ch2 := e.seedChannel(t, "cmp-ch-usd", "http://127.0.0.1:1", 0, []string{"glm-5"}, nil)
	e.db.Create(&store.ChannelCost{
		ChannelID: ch1, ModelName: "glm-5", Currency: string(domain.CostCurrencyCredits),
		InputCost: 500_000, OutputCost: 1_000_000, CacheReadCost: 100_000,
		CacheWriteCost: 200_000, PerCallCost: 0,
	})
	e.db.Create(&store.ChannelCost{
		ChannelID: ch2, ModelName: "glm-5", Currency: string(domain.CostCurrencyUSD),
		InputCost: 30_000_000, OutputCost: 60_000_000, CacheReadCost: 3_000_000,
		CacheWriteCost: 6_000_000, PerCallCost: 10_000,
	})

	adminC := e.seedAndLogin(t, "costcmpadmin", domain.RoleAdmin)
	resp, env := e.do(t, adminC, "GET", fmt.Sprintf("/api/admin/models/%d/channel-costs", modelID), nil)
	if resp.StatusCode != 200 {
		t.Fatalf("比价查询失败: %d %v", resp.StatusCode, env)
	}
	rows := env["data"].([]any)
	if len(rows) != 2 {
		t.Fatalf("应有 2 条比价记录，实际 %d", len(rows))
	}
	// ListByModel 按 channel_id 排序
	type want struct {
		channelID                                     int64
		currency                                      string
		input, output, cacheRead, cacheWrite, perCall int64
	}
	wants := []want{
		{ch1, "credits", 500_000, 1_000_000, 100_000, 200_000, 0},
		{ch2, "usd", 30_000_000, 60_000_000, 3_000_000, 6_000_000, 10_000},
	}
	for i, w := range wants {
		row := rows[i].(map[string]any)
		for _, field := range []string{"channel_id", "currency", "input_cost",
			"output_cost", "cache_read_cost", "cache_write_cost", "per_call_cost"} {
			if _, ok := row[field]; !ok {
				t.Errorf("第 %d 条记录缺少字段 %s: %v", i+1, field, row)
			}
		}
		if int64(row["channel_id"].(float64)) != w.channelID || row["currency"] != w.currency ||
			int64(row["input_cost"].(float64)) != w.input ||
			int64(row["output_cost"].(float64)) != w.output ||
			int64(row["cache_read_cost"].(float64)) != w.cacheRead ||
			int64(row["cache_write_cost"].(float64)) != w.cacheWrite ||
			int64(row["per_call_cost"].(float64)) != w.perCall {
			t.Errorf("第 %d 条记录值不符: %v", i+1, row)
		}
	}
}

// 渠道侧成本回读字段契约：GET /admin/channels/{id}/costs 每条记录含
// model_name、currency、input_cost、output_cost、cache_read_cost、cache_write_cost、per_call_cost，
// 与 docs/api-contract.md 渠道成本端点的字段清单一致（模型侧比价端点契约见
// TestModelChannelCostsFieldContract，两端点均需守护）。
func TestChannelCostsReadbackFieldContract(t *testing.T) {
	e := newTestEnv(t)
	e.seedModel(t, "glm-5")
	chID := e.seedChannel(t, "readback-ch", "http://127.0.0.1:1", 0, []string{"glm-5"}, nil)
	adminC := e.seedAndLogin(t, "costreadadmin", domain.RoleAdmin)

	resp, env := e.do(t, adminC, "PUT", fmt.Sprintf("/api/admin/channels/%d/costs", chID),
		map[string]any{"costs": []map[string]any{{
			"model_name": "glm-5", "currency": "usd",
			"input_cost": 30_000_000, "output_cost": 60_000_000,
			"cache_read_cost": 3_000_000, "cache_write_cost": 6_000_000,
			"per_call_cost": 10_000,
		}}})
	if resp.StatusCode != 200 {
		t.Fatalf("设置成本失败: %d %v", resp.StatusCode, env)
	}
	resp, env = e.do(t, adminC, "GET", fmt.Sprintf("/api/admin/channels/%d/costs", chID), nil)
	if resp.StatusCode != 200 {
		t.Fatalf("查询成本失败: %d %v", resp.StatusCode, env)
	}
	rows := env["data"].([]any)
	if len(rows) != 1 {
		t.Fatalf("应回读 1 条成本，实际 %d", len(rows))
	}
	row := rows[0].(map[string]any)
	for _, field := range []string{"model_name", "currency", "input_cost",
		"output_cost", "cache_read_cost", "cache_write_cost", "per_call_cost"} {
		if _, ok := row[field]; !ok {
			t.Errorf("回读记录缺少契约字段 %s: %v", field, row)
		}
	}
	if row["model_name"] != "glm-5" || row["currency"] != "usd" ||
		int64(row["input_cost"].(float64)) != 30_000_000 ||
		int64(row["output_cost"].(float64)) != 60_000_000 ||
		int64(row["cache_read_cost"].(float64)) != 3_000_000 ||
		int64(row["cache_write_cost"].(float64)) != 6_000_000 ||
		int64(row["per_call_cost"].(float64)) != 10_000 {
		t.Errorf("回读记录值不符: %v", row)
	}
}

// 全量替换语义下的币种变更：同一 model_name 先以 credits 保存、再以 usd 与新单价
// 整体 PUT，回读应只剩新行（currency 与金额同步更新，无旧行残留）——
// channel_costs 为全量替换，不是逐行 upsert。
func TestChannelCostsReplaceCurrencyChange(t *testing.T) {
	e := newTestEnv(t)
	e.seedModel(t, "glm-5")
	chID := e.seedChannel(t, "replace-ch", "http://127.0.0.1:1", 0, []string{"glm-5"}, nil)
	adminC := e.seedAndLogin(t, "costreplaceadmin", domain.RoleAdmin)

	resp, env := e.do(t, adminC, "PUT", fmt.Sprintf("/api/admin/channels/%d/costs", chID),
		map[string]any{"costs": []map[string]any{{
			"model_name": "glm-5", "currency": "credits",
			"input_cost": 500_000, "output_cost": 1_000_000,
		}}})
	if resp.StatusCode != 200 {
		t.Fatalf("首次保存成本失败: %d %v", resp.StatusCode, env)
	}
	resp, env = e.do(t, adminC, "PUT", fmt.Sprintf("/api/admin/channels/%d/costs", chID),
		map[string]any{"costs": []map[string]any{{
			"model_name": "glm-5", "currency": "usd",
			"input_cost": 30_000_000, "output_cost": 60_000_000,
		}}})
	if resp.StatusCode != 200 {
		t.Fatalf("二次保存成本失败: %d %v", resp.StatusCode, env)
	}
	resp, env = e.do(t, adminC, "GET", fmt.Sprintf("/api/admin/channels/%d/costs", chID), nil)
	if resp.StatusCode != 200 {
		t.Fatalf("查询成本失败: %d %v", resp.StatusCode, env)
	}
	rows := env["data"].([]any)
	if len(rows) != 1 {
		t.Fatalf("全量替换后应只剩 1 条记录（无旧行残留），实际 %d", len(rows))
	}
	row := rows[0].(map[string]any)
	if row["currency"] != "usd" ||
		int64(row["input_cost"].(float64)) != 30_000_000 ||
		int64(row["output_cost"].(float64)) != 60_000_000 {
		t.Errorf("币种与金额应同步更新为 usd 新单价: %v", row)
	}
	var count int64
	e.db.Model(&store.ChannelCost{}).Where("channel_id = ?", chID).Count(&count)
	if count != 1 {
		t.Errorf("数据库应只有 1 行该渠道成本，实际 %d", count)
	}
}

// usd 成本的中继结算折算：InputCost=30,000,000 微美元/1M tokens（30 美元/1M），
// 1000 input tokens → 30,000 微美元 → ×7200/1000 ×1e6/1e6 = 216,000 积分。
func TestRelayUsdCostConversion(t *testing.T) {
	e := newTestEnv(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"id":"x","model":"m","choices":[],
			"usage":{"prompt_tokens":1000,"completion_tokens":0}}`)
	}))
	defer upstream.Close()

	userID, key := e.seedRelayUser(t, "usdcostuser", 1_000_000, nil)
	e.seedModel(t, "glm-5")
	chID := e.seedChannel(t, "usdcost-ch", upstream.URL, 0, []string{"glm-5"}, nil)
	e.db.Create(&store.ChannelCost{
		ChannelID: chID, ModelName: "glm-5", Currency: string(domain.CostCurrencyUSD),
		InputCost: 30_000_000,
	})

	resp, body := e.relayPost(t, key, map[string]any{
		"model": "glm-5", "messages": []map[string]any{{"role": "user", "content": "hi"}},
	})
	if resp.StatusCode != 200 {
		t.Fatalf("中继请求失败: %d %s", resp.StatusCode, body)
	}
	log := e.waitUsageLog(t, userID)
	if log.Status != domain.UsageSettled {
		t.Fatalf("应结算完成，实际 %s", log.Status)
	}
	if log.CreditsCost != 216_000 {
		t.Errorf("usd 成本折算错误：期望 216000 积分，实际 %d", log.CreditsCost)
	}
	e.assertReconcile(t)
}

// usd 按次成本折算：PerCallCost=10,000 微美元/次，2 次调用 → 20,000 微美元
// → ×7200/1000 ×1e6/1e6 = 144,000 积分（glossary：usd 按次计费单位为 微美元/次）。
func TestImagesUsdPerCallCostConversion(t *testing.T) {
	e := newTestEnv(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"created":1,"data":[{"b64_json":"aW1n"},{"b64_json":"aW1n"}]}`)
	}))
	defer upstream.Close()

	userID, key := e.seedRelayUser(t, "usdimguser", 1_000_000, nil)
	m := &store.Model{Name: "gpt-image-1", Modality: domain.ModalityImage,
		BillingMode: domain.BillPerCall, Status: domain.ModelEnabled}
	e.db.Create(m)
	e.db.Create(&store.ModelPrice{ModelID: m.ID, PerCallPrice: 40_000})
	chID := e.seedChannel(t, "usdimg-ch", upstream.URL, 0, []string{"gpt-image-1"}, nil)
	e.db.Create(&store.ChannelCost{
		ChannelID: chID, ModelName: "gpt-image-1", Currency: string(domain.CostCurrencyUSD),
		PerCallCost: 10_000,
	})

	payload, _ := json.Marshal(map[string]any{
		"model": "gpt-image-1", "prompt": "一只猫", "n": 2, "size": "1024x1024",
	})
	req, _ := http.NewRequest("POST", e.srv.URL+"/v1/images/generations", bytes.NewReader(payload))
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("图像请求失败: %v", err)
	}
	raw := new(bytes.Buffer)
	_, _ = raw.ReadFrom(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("图像请求应成功: %d %s", resp.StatusCode, raw.String())
	}
	log := e.waitUsageLog(t, userID)
	if log.CallCount != 2 {
		t.Fatalf("应按 2 次计费，实际 %d", log.CallCount)
	}
	if log.CreditsCost != 144_000 {
		t.Errorf("usd 按次成本折算错误：期望 144000 积分，实际 %d", log.CreditsCost)
	}
	e.assertReconcile(t)
}

// 渠道未配置该模型成本时结算成功且成本记 0。
func TestRelayCostZeroWhenNoCostRecord(t *testing.T) {
	e := newTestEnv(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"id":"x","model":"m","choices":[],
			"usage":{"prompt_tokens":100,"completion_tokens":50}}`)
	}))
	defer upstream.Close()

	userID, key := e.seedRelayUser(t, "nocostuser", 100_000, nil)
	e.seedModel(t, "glm-5")
	e.seedChannel(t, "nocost-ch", upstream.URL, 0, []string{"glm-5"}, nil)

	resp, body := e.relayPost(t, key, map[string]any{
		"model": "glm-5", "messages": []map[string]any{{"role": "user", "content": "hi"}},
	})
	if resp.StatusCode != 200 {
		t.Fatalf("中继请求失败: %d %s", resp.StatusCode, body)
	}
	log := e.waitUsageLog(t, userID)
	if log.Status != domain.UsageSettled {
		t.Fatalf("应结算完成，实际 %s", log.Status)
	}
	if log.CreditsCost != 0 {
		t.Errorf("无成本记录时 cost_credits 应为 0，实际 %d", log.CreditsCost)
	}
	e.assertReconcile(t)
}
