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

// seedImageModel 创建按次计费图像模型（单价 4 万积分/张）并挂一个 openai_compat 渠道。
func (e *testEnv) seedImageModel(t *testing.T, chName, upstreamURL string) int64 {
	t.Helper()
	m := &store.Model{Name: "gpt-image-1", Modality: domain.ModalityImage,
		BillingMode: domain.BillPerCall, Status: domain.ModelEnabled}
	if err := e.db.Create(m).Error; err != nil {
		t.Fatalf("建模型失败: %v", err)
	}
	if err := e.db.Create(&store.ModelPrice{ModelID: m.ID, PerCallPrice: 40_000}).Error; err != nil {
		t.Fatalf("建定价失败: %v", err)
	}
	return e.seedChannel(t, chName, upstreamURL, 0, []string{"gpt-image-1"}, nil)
}

// postImages 发起图像生成请求并返回响应。
func (e *testEnv) postImages(t *testing.T, apiKey string, body map[string]any) (*http.Response, []byte) {
	t.Helper()
	payload, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", e.srv.URL+"/v1/images/generations", bytes.NewReader(payload))
	req.Header.Set("Authorization", "Bearer "+apiKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	raw := new(bytes.Buffer)
	_, _ = raw.ReadFrom(resp.Body)
	resp.Body.Close()
	return resp, raw.Bytes()
}

// I3 多返图取较小值：请求 1 张、上游返回 3 张 → 只计 1 次，无备注。
func TestImagesPerCallBillingOverReturn(t *testing.T) {
	e := newTestEnv(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"created":1,"data":[{"b64_json":"YQ=="},{"b64_json":"Yg=="},{"b64_json":"Yw=="}]}`)
	}))
	defer upstream.Close()

	userID, key := e.seedRelayUser(t, "imgover", 1_000_000, nil)
	e.seedImageModel(t, "img-over-ch", upstream.URL)

	resp, raw := e.postImages(t, key, map[string]any{
		"model": "gpt-image-1", "prompt": "一只猫", "n": 1,
	})
	if resp.StatusCode != 200 {
		t.Fatalf("应成功: %d %s", resp.StatusCode, raw)
	}
	if bal := e.userBalance(t, userID); bal != 1_000_000-40_000 {
		t.Errorf("多返图应按请求张数计费，期望余额 %d，实际 %d", 1_000_000-40_000, bal)
	}
	log := e.waitUsageLog(t, userID)
	if log.CallCount != 1 || log.CreditsCharged != 40_000 {
		t.Errorf("应取较小值计 1 次: count=%d charged=%d", log.CallCount, log.CreditsCharged)
	}
	if log.ErrorMessage != "" {
		t.Errorf("多返图不属少返，备注应为空，实际: %q", log.ErrorMessage)
	}
	if log.Status != domain.UsageSettled {
		t.Errorf("状态应为 settled，实际: %s", log.Status)
	}
	e.assertReconcile(t)
}

// I4 请求缺 n 字段默认 1 张：上游返回 1 张 → 计 1 次正常结算。
func TestImagesPerCallBillingDefaultN(t *testing.T) {
	e := newTestEnv(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"created":1,"data":[{"b64_json":"aW1n"}]}`)
	}))
	defer upstream.Close()

	userID, key := e.seedRelayUser(t, "imgdefn", 1_000_000, nil)
	e.seedImageModel(t, "img-defn-ch", upstream.URL)

	resp, raw := e.postImages(t, key, map[string]any{
		"model": "gpt-image-1", "prompt": "一只猫",
	})
	if resp.StatusCode != 200 {
		t.Fatalf("应成功: %d %s", resp.StatusCode, raw)
	}
	log := e.waitUsageLog(t, userID)
	if log.CallCount != 1 || log.CreditsCharged != 40_000 || log.CreditsPrecharged != 40_000 {
		t.Errorf("缺 n 应按 1 张计费: count=%d precharged=%d charged=%d",
			log.CallCount, log.CreditsPrecharged, log.CreditsCharged)
	}
	if log.Status != domain.UsageSettled {
		t.Errorf("状态应为 settled，实际: %s", log.Status)
	}
	if bal := e.userBalance(t, userID); bal != 1_000_000-40_000 {
		t.Errorf("期望余额 %d，实际 %d", 1_000_000-40_000, bal)
	}
	e.assertReconcile(t)
}

// I5 响应不可解析回退：上游 200 但返回非 JSON → 按请求张数计费，不误判为 0 张。
func TestImagesPerCallBillingUnparseableResponse(t *testing.T) {
	e := newTestEnv(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `PNG-binary-not-json`)
	}))
	defer upstream.Close()

	userID, key := e.seedRelayUser(t, "imgraw", 1_000_000, nil)
	e.seedImageModel(t, "img-raw-ch", upstream.URL)

	resp, raw := e.postImages(t, key, map[string]any{
		"model": "gpt-image-1", "prompt": "一只猫", "n": 2,
	})
	if resp.StatusCode != 200 {
		t.Fatalf("透传应成功: %d %s", resp.StatusCode, raw)
	}
	log := e.waitUsageLog(t, userID)
	if log.CallCount != 2 || log.CreditsCharged != 80_000 {
		t.Errorf("不可判定响应应回退请求张数计费: count=%d charged=%d",
			log.CallCount, log.CreditsCharged)
	}
	if bal := e.userBalance(t, userID); bal != 1_000_000-80_000 {
		t.Errorf("期望余额 %d（不退款），实际 %d", 1_000_000-80_000, bal)
	}
	e.assertReconcile(t)
}

// I5 变体：缺 data 字段的合法 JSON 同样回退请求张数计费。
func TestImagesPerCallBillingMissingDataField(t *testing.T) {
	e := newTestEnv(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"created":1}`)
	}))
	defer upstream.Close()

	userID, key := e.seedRelayUser(t, "imgnodata", 1_000_000, nil)
	e.seedImageModel(t, "img-nodata-ch", upstream.URL)

	resp, raw := e.postImages(t, key, map[string]any{
		"model": "gpt-image-1", "prompt": "一只猫", "n": 2,
	})
	if resp.StatusCode != 200 {
		t.Fatalf("应成功: %d %s", resp.StatusCode, raw)
	}
	log := e.waitUsageLog(t, userID)
	if log.CallCount != 2 || log.CreditsCharged != 80_000 {
		t.Errorf("缺 data 字段应回退请求张数计费: count=%d charged=%d",
			log.CallCount, log.CreditsCharged)
	}
	e.assertReconcile(t)
}

// I6 空 data 数组：上游 200 且 data=[] → 计 0 次，预扣全额经结算差额退回，备注记录差异。
func TestImagesPerCallBillingEmptyData(t *testing.T) {
	e := newTestEnv(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"created":1,"data":[]}`)
	}))
	defer upstream.Close()

	userID, key := e.seedRelayUser(t, "imgempty", 1_000_000, nil)
	e.seedImageModel(t, "img-empty-ch", upstream.URL)

	resp, raw := e.postImages(t, key, map[string]any{
		"model": "gpt-image-1", "prompt": "一只猫", "n": 2,
	})
	if resp.StatusCode != 200 {
		t.Fatalf("应成功: %d %s", resp.StatusCode, raw)
	}
	log := e.waitUsageLog(t, userID)
	if log.CallCount != 0 || log.CreditsCharged != 0 {
		t.Errorf("空 data 应计 0 次: count=%d charged=%d", log.CallCount, log.CreditsCharged)
	}
	if log.CreditsPrecharged != 80_000 {
		t.Errorf("预扣应按请求张数 80000，实际 %d", log.CreditsPrecharged)
	}
	if log.ErrorMessage == "" {
		t.Error("空 data 属少返图，备注应记录差异")
	}
	if log.Status != domain.UsageSettled {
		t.Errorf("状态应为 settled，实际: %s", log.Status)
	}
	if bal := e.userBalance(t, userID); bal != 1_000_000 {
		t.Errorf("预扣应全额退回，期望余额 1000000，实际 %d", bal)
	}
	e.assertReconcile(t)
}

// I8 + I9 少返图备注 API 透出与渠道成本口径：
// n=3、上游返回 2 张 → 扣 80000；/api/me/usage-logs 与 /api/admin/usage-logs/detail
// 均透出 error_message 且内容一致；CreditsCost 按实际计费张数（2 张）计算。
func TestImagesShortReturnNoteExposureAndCost(t *testing.T) {
	e := newTestEnv(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"created":1,"data":[{"b64_json":"YQ=="},{"b64_json":"Yg=="}]}`)
	}))
	defer upstream.Close()

	userID, key := e.seedRelayUser(t, "imgnote", 1_000_000, nil)
	chID := e.seedImageModel(t, "img-note-ch", upstream.URL)
	// 渠道成本：积分记账，1 万积分/张
	if err := e.db.Create(&store.ChannelCost{
		ChannelID: chID, ModelName: "gpt-image-1",
		Currency: string(domain.CostCurrencyCredits), PerCallCost: 10_000,
	}).Error; err != nil {
		t.Fatalf("建渠道成本失败: %v", err)
	}

	resp, raw := e.postImages(t, key, map[string]any{
		"model": "gpt-image-1", "prompt": "一只猫", "n": 3,
	})
	if resp.StatusCode != 200 {
		t.Fatalf("应成功: %d %s", resp.StatusCode, raw)
	}
	log := e.waitUsageLog(t, userID)
	if log.CallCount != 2 || log.CreditsCharged != 80_000 || log.CreditsPrecharged != 120_000 {
		t.Fatalf("少返图结算错误: count=%d precharged=%d charged=%d",
			log.CallCount, log.CreditsPrecharged, log.CreditsCharged)
	}
	if log.ErrorMessage == "" {
		t.Fatal("少返图备注应非空")
	}
	// I9：成本口径跟随实际计费张数 2 张 × 10000 = 20000，而非请求张数 3 张
	if log.CreditsCost != 20_000 {
		t.Errorf("渠道成本应按实际计费张数计算（2×10000=20000），实际 %d", log.CreditsCost)
	}

	// 用户侧透出
	c := e.client(t)
	loginResp, loginEnv := e.do(t, c, "POST", "/api/auth/login",
		map[string]string{"username": "imgnote", "password": "password-imgnote"})
	if loginResp.StatusCode != 200 {
		t.Fatalf("登录失败: %d %v", loginResp.StatusCode, loginEnv)
	}
	listResp, env := e.do(t, c, "GET", "/api/me/usage-logs", nil)
	if listResp.StatusCode != 200 {
		t.Fatalf("查询用量日志失败: %d %v", listResp.StatusCode, env)
	}
	items := pageItems(t, env)
	if len(items) == 0 {
		t.Fatal("应返回至少 1 条用量日志")
	}
	row := items[0].(map[string]any)
	meNote, ok := row["error_message"].(string)
	if !ok || meNote == "" {
		t.Fatalf("/api/me/usage-logs 应透出非空 error_message，实际: %v", row["error_message"])
	}
	if meNote != log.ErrorMessage {
		t.Errorf("用户侧备注与落库不一致: %q vs %q", meNote, log.ErrorMessage)
	}

	// 管理侧透出
	adminC := e.seedAndLogin(t, "imgnoteadmin", domain.RoleAdmin)
	detailResp, detailEnv := e.do(t, adminC, "GET",
		"/api/admin/usage-logs/detail?request_id="+log.RequestID, nil)
	if detailResp.StatusCode != 200 {
		t.Fatalf("管理侧明细查询失败: %d %v", detailResp.StatusCode, detailEnv)
	}
	detail, ok := detailEnv["data"].(map[string]any)
	if !ok {
		t.Fatalf("明细响应缺少 data: %v", detailEnv)
	}
	adminNote, _ := detail["error_message"].(string)
	if adminNote != meNote {
		t.Errorf("管理侧与用户侧备注不一致: %q vs %q", adminNote, meNote)
	}
	e.assertReconcile(t)
}
