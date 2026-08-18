package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
)

// --- 双闸门（总并发 + 大请求配额）补充集成测试 ---

// largeChatPayload 构造请求体超过 1 MiB 的 chat/completions 请求。
func largeChatPayload() []byte {
	b, _ := json.Marshal(map[string]any{
		"model": "glm-5",
		"messages": []map[string]any{{"role": "user",
			"content": strings.Repeat("a", (1<<20)+1024)}},
	})
	return b
}

// newBlockingUpstream 返回阻塞式上游桩：请求到达后向 entered 发信号并挂起，
// 直到 release 关闭才返回成功响应。
func newBlockingUpstream(t *testing.T) (srv *httptest.Server, entered chan struct{}, release chan struct{}) {
	t.Helper()
	entered = make(chan struct{}, 4)
	release = make(chan struct{})
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entered <- struct{}{}
		<-release
		fmt.Fprint(w, `{"id":"x","model":"glm-5","choices":[{"message":{"content":"hi"}}],
			"usage":{"prompt_tokens":10,"completion_tokens":10}}`)
	}))
	t.Cleanup(srv.Close)
	return srv, entered, release
}

// holdLargeRequest 发出一个大请求占住大请求配额并等待其到达阻塞式上游。
// 返回的函数用于释放上游并等待占位请求结束。
func (e *testEnv) holdLargeRequest(t *testing.T, apiKey string,
	entered chan struct{}, release chan struct{}) (stop func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		req, _ := http.NewRequest("POST",
			e.srv.URL+"/v1/chat/completions", bytes.NewReader(largeChatPayload()))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+apiKey)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Errorf("占位大请求失败: %v", err)
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("占位大请求放行后应成功，实际 %d", resp.StatusCode)
		}
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("等待占位大请求到达上游超时")
	}
	return func() {
		close(release)
		<-done
	}
}

// TestRelayGuardLargeCountsTowardTotal 大请求同时占用总并发槽位：
// 总上限 1 被一个大请求占住后，小请求也被拒绝，证明大请求不是绕过总闸门的旁路。
func TestRelayGuardLargeCountsTowardTotal(t *testing.T) {
	e := newTestEnv(t)
	upstream, entered, release := newBlockingUpstream(t)

	_, blockedKey := e.seedRelayUser(t, "dual-total-a", 2_000_000_000, nil)
	_, probeKey := e.seedRelayUser(t, "dual-total-b", 0, nil)
	e.seedModel(t, "glm-5")
	e.seedChannel(t, "ch-dual-total", upstream.URL, 0, []string{"glm-5"}, nil)
	e.setSetting(t, "max_concurrent_requests", "1")
	e.setSetting(t, "max_concurrent_large_requests", "1")
	defer func() {
		e.setSetting(t, "max_concurrent_requests", "40")
		e.setSetting(t, "max_concurrent_large_requests", "2")
	}()

	stop := e.holdLargeRequest(t, blockedKey, entered, release)
	defer stop()

	resp, body := e.guardPost(t, probeKey) // 小请求
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("大请求占满总并发后小请求应 503，实际 %d，响应: %v", resp.StatusCode, body)
	}
	if errObj, ok := body["error"].(map[string]any); !ok || errObj["code"] != "overloaded" {
		t.Errorf("期望 error.code=overloaded，实际: %v", body)
	}
}

// TestRelayGuardLargeSlotRelease 大请求槽位释放：
// 占位大请求正常完成后，后续大请求立即放行；中继失败（上游 5xx）后槽位同样释放。
func TestRelayGuardLargeSlotRelease(t *testing.T) {
	e := newTestEnv(t)

	okUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"id":"x","model":"glm-5","choices":[{"message":{"content":"hi"}}],
			"usage":{"prompt_tokens":10,"completion_tokens":10}}`)
	}))
	defer okUpstream.Close()
	failUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"message":"boom"}}`, http.StatusInternalServerError)
	}))
	defer failUpstream.Close()

	_, key := e.seedRelayUser(t, "dual-release", 2_000_000_000, nil)
	e.seedModel(t, "glm-5")
	e.seedModel(t, "glm-fail")
	e.seedChannel(t, "ch-rel-ok", okUpstream.URL, 0, []string{"glm-5"}, nil)
	e.seedChannel(t, "ch-rel-fail", failUpstream.URL, 0, []string{"glm-fail"}, nil)
	e.setSetting(t, "max_concurrent_large_requests", "1")
	defer e.setSetting(t, "max_concurrent_large_requests", "2")

	// 子用例 1：正常完成后槽位释放，连续两个大请求都应放行。
	for i := 0; i < 2; i++ {
		resp, body := e.guardPostBody(t, key, largeChatPayload())
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("第 %d 个大请求应放行并成功，实际 %d，响应: %v", i+1, resp.StatusCode, body)
		}
	}

	// 子用例 2：中继失败（上游 5xx）后槽位释放。
	failPayload, _ := json.Marshal(map[string]any{
		"model": "glm-fail",
		"messages": []map[string]any{{"role": "user",
			"content": strings.Repeat("b", (1<<20)+1024)}},
	})
	resp, body := e.guardPostBody(t, key, failPayload)
	if resp.StatusCode == http.StatusServiceUnavailable {
		t.Fatalf("失败用大请求不应被并发闸门拒绝，响应: %v", body)
	}
	if resp.StatusCode == http.StatusOK {
		t.Fatalf("上游 5xx 的大请求不应成功，实际 %d", resp.StatusCode)
	}
	// 失败请求结束后，大配额槽位必须已释放。
	resp2, body2 := e.guardPostBody(t, key, largeChatPayload())
	if resp2.StatusCode != http.StatusOK {
		t.Errorf("失败请求结束后大配额应已释放，后续大请求应成功，实际 %d，响应: %v",
			resp2.StatusCode, body2)
	}
}

// TestRelayGuardChunkedTreatedAsLarge Content-Length 缺失（chunked 编码）的请求
// 按大请求保守准入：大配额占满时即使实际体积很小也被拒绝。
func TestRelayGuardChunkedTreatedAsLarge(t *testing.T) {
	e := newTestEnv(t)
	upstream, entered, release := newBlockingUpstream(t)

	_, blockedKey := e.seedRelayUser(t, "dual-chunked-a", 2_000_000_000, nil)
	_, probeKey := e.seedRelayUser(t, "dual-chunked-b", 0, nil)
	e.seedModel(t, "glm-5")
	e.seedChannel(t, "ch-chunked", upstream.URL, 0, []string{"glm-5"}, nil)
	e.setSetting(t, "max_concurrent_large_requests", "1")
	defer e.setSetting(t, "max_concurrent_large_requests", "2")

	stop := e.holdLargeRequest(t, blockedKey, entered, release)
	defer stop()

	// 小体积但 chunked 编码（长度未知）的请求。
	payload, _ := json.Marshal(map[string]any{
		"model": "glm-5", "messages": []map[string]any{{"role": "user", "content": "hi"}},
	})
	req, _ := http.NewRequest("POST",
		e.srv.URL+"/v1/chat/completions", bytes.NewReader(payload))
	req.ContentLength = -1 // 强制 Transfer-Encoding: chunked
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+probeKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("chunked 请求失败: %v", err)
	}
	defer resp.Body.Close()
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("长度未知的请求应按大请求被拒绝（503），实际 %d，响应: %v", resp.StatusCode, body)
	}
	if errObj, ok := body["error"].(map[string]any); !ok || errObj["code"] != "overloaded" {
		t.Errorf("期望 error.code=overloaded，实际: %v", body)
	}
}

// TestRelayGuardRejectionHasNoBillingSideEffect 门禁拒绝发生在预扣费之前：
// 被 503 拒绝的请求不得产生余额变动、积分流水或用量日志。
func TestRelayGuardRejectionHasNoBillingSideEffect(t *testing.T) {
	e := newTestEnv(t)
	upstream, entered, release := newBlockingUpstream(t)

	_, blockedKey := e.seedRelayUser(t, "dual-billing-a", 2_000_000_000, nil)
	probeUID, probeKey := e.seedRelayUser(t, "dual-billing-b", 0, nil)
	e.seedModel(t, "glm-5")
	e.seedChannel(t, "ch-billing", upstream.URL, 0, []string{"glm-5"}, nil)
	e.setSetting(t, "max_concurrent_large_requests", "1")
	defer e.setSetting(t, "max_concurrent_large_requests", "2")

	stop := e.holdLargeRequest(t, blockedKey, entered, release)
	defer stop()

	resp, _ := e.guardPostBody(t, probeKey, largeChatPayload())
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("大配额占满应 503，实际 %d", resp.StatusCode)
	}

	if bal := e.userBalance(t, probeUID); bal != 0 {
		t.Errorf("被拒请求不应改变余额，期望 0 实际 %d", bal)
	}
	var ledgerCount, usageCount int64
	if err := e.db.Table("credit_ledger").Where("user_id = ?", probeUID).
		Count(&ledgerCount).Error; err != nil {
		t.Fatalf("查流水失败: %v", err)
	}
	if ledgerCount != 0 {
		t.Errorf("被拒请求不应产生积分流水，实际 %d 条", ledgerCount)
	}
	if err := e.db.Table("usage_logs").Where("user_id = ?", probeUID).
		Count(&usageCount).Error; err != nil {
		t.Fatalf("查用量日志失败: %v", err)
	}
	if usageCount != 0 {
		t.Errorf("被拒请求不应产生用量日志，实际 %d 条", usageCount)
	}
}

// TestRelayGuardCoversAllRelayEndpoints /v1/messages（Anthropic 下游）与 /v1/embeddings
// 同受双闸门约束：大配额占满后这两个端点的大请求同样被 503 拒绝。
func TestRelayGuardCoversAllRelayEndpoints(t *testing.T) {
	e := newTestEnv(t)
	upstream, entered, release := newBlockingUpstream(t)

	_, blockedKey := e.seedRelayUser(t, "dual-endpoints-a", 2_000_000_000, nil)
	_, probeKey := e.seedRelayUser(t, "dual-endpoints-b", 0, nil)
	e.seedModel(t, "glm-5")
	e.seedChannel(t, "ch-endpoints", upstream.URL, 0, []string{"glm-5"}, nil)
	e.setSetting(t, "max_concurrent_large_requests", "1")
	defer e.setSetting(t, "max_concurrent_large_requests", "2")

	stop := e.holdLargeRequest(t, blockedKey, entered, release)
	defer stop()

	bigText := strings.Repeat("a", (1<<20)+1024)
	endpoints := []struct {
		path    string
		payload map[string]any
	}{
		{"/v1/messages", map[string]any{
			"model": "glm-5", "max_tokens": 16,
			"messages": []map[string]any{{"role": "user", "content": bigText}},
		}},
		{"/v1/embeddings", map[string]any{
			"model": "glm-5", "input": bigText,
		}},
	}
	for _, ep := range endpoints {
		t.Run(ep.path, func(t *testing.T) {
			payload, _ := json.Marshal(ep.payload)
			req, _ := http.NewRequest("POST", e.srv.URL+ep.path, bytes.NewReader(payload))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+probeKey)
			req.Header.Set("x-api-key", probeKey)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("请求失败: %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusServiceUnavailable {
				t.Errorf("%s 大请求在大配额占满时应 503，实际 %d", ep.path, resp.StatusCode)
			}
		})
	}
}

// TestConcurrencySettingsRejectInvalidValues 管理端设置写入链路：
// 两个并发准入设置项拒绝负值与超上限值（负值会被并发闸门解释为"不限制"，
// 等同静默关闭内存保护），拒绝后生效值保持不变；合法值仍可写入。
func TestConcurrencySettingsRejectInvalidValues(t *testing.T) {
	e := newTestEnv(t)
	rootC := e.seedAndLogin(t, "guard-set-root", domain.RoleRoot)

	for _, key := range []string{"max_concurrent_requests", "max_concurrent_large_requests"} {
		for _, bad := range []int{-1, -100, 1001} {
			resp, env := e.do(t, rootC, "PUT", "/api/admin/settings",
				map[string]any{"key": key, "value": bad})
			if resp.StatusCode != http.StatusBadRequest {
				t.Errorf("%s 写入 %d 应 400，实际 %d，响应: %v", key, bad, resp.StatusCode, env)
			}
		}
	}

	// 拒绝写入后生效值应保持默认，内存保护未被关闭。
	if v := e.deps.Settings.GetInt64(context.Background(), "max_concurrent_requests"); v != 40 {
		t.Errorf("拒绝非法值后 max_concurrent_requests 生效值应仍为 40，实际 %d", v)
	}

	// 合法值仍可写入。
	resp, env := e.do(t, rootC, "PUT", "/api/admin/settings",
		map[string]any{"key": "max_concurrent_requests", "value": 80})
	if resp.StatusCode != http.StatusOK {
		t.Errorf("合法值 80 应写入成功，实际 %d，响应: %v", resp.StatusCode, env)
	}
	e.setSetting(t, "max_concurrent_requests", "40")
}

// TestSettingsEffectiveAllExposesLargeQuota 管理端设置读取链路：
// EffectiveAll 输出应包含 max_concurrent_large_requests（默认值 + 描述）。
func TestSettingsEffectiveAllExposesLargeQuota(t *testing.T) {
	e := newTestEnv(t)
	all := e.deps.Settings.EffectiveAll(context.Background())
	for _, item := range all {
		if item["key"] == "max_concurrent_large_requests" {
			if v, ok := item["value"].(int64); !ok || v != 2 {
				t.Errorf("默认生效值应为 int64(2)，实际 %v (%T)", item["value"], item["value"])
			}
			if d, _ := item["describe"].(string); d == "" {
				t.Error("describe 不应为空")
			}
			return
		}
	}
	t.Fatal("EffectiveAll 未包含 max_concurrent_large_requests")
}

// TestRelayGuardOversizedBodyRejectedAndSlotReleased 请求体上限与并发准入协同：
// Content-Length 超过 20 MiB 的请求被 MaxBytesReader 截断，JSON 解码失败返回 400；
// 该请求结束后其占用的大配额槽位必须已释放，后续大请求可正常放行。
func TestRelayGuardOversizedBodyRejectedAndSlotReleased(t *testing.T) {
	e := newTestEnv(t)

	okUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"id":"x","model":"glm-5","choices":[{"message":{"content":"hi"}}],
			"usage":{"prompt_tokens":10,"completion_tokens":10}}`)
	}))
	defer okUpstream.Close()

	_, key := e.seedRelayUser(t, "oversize-body", 2_000_000_000, nil)
	e.seedModel(t, "glm-5")
	e.seedChannel(t, "ch-oversize", okUpstream.URL, 0, []string{"glm-5"}, nil)
	e.setSetting(t, "max_concurrent_large_requests", "1")
	defer e.setSetting(t, "max_concurrent_large_requests", "2")

	// 超过 20 MiB 上限的请求体（21 MiB 文本）。
	oversized, _ := json.Marshal(map[string]any{
		"model": "glm-5",
		"messages": []map[string]any{{"role": "user",
			"content": strings.Repeat("a", 21<<20)}},
	})
	resp, body := e.guardPostBody(t, key, oversized)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("超限请求体应被截断并返回 400，实际 %d，响应: %v", resp.StatusCode, body)
	}
	if errObj, ok := body["error"].(map[string]any); !ok || errObj["code"] != "invalid_request_error" {
		t.Errorf("期望 error.code=invalid_request_error，实际: %v", body)
	}

	// 超限请求结束后，大配额槽位（上限 1）必须已释放：后续大请求应放行并成功。
	resp2, body2 := e.guardPostBody(t, key, largeChatPayload())
	if resp2.StatusCode != http.StatusOK {
		t.Errorf("超限请求结束后大配额应已释放，后续大请求应成功，实际 %d，响应: %v",
			resp2.StatusCode, body2)
	}
}
