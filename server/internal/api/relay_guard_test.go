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
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// --- /v1 门禁测试：认证、限流、并发七种拒绝路径 ---

// guardPost 用可选 API Key 调 /v1/chat/completions（apiKey 为空时不带认证头）。
func (e *testEnv) guardPost(t *testing.T, apiKey string) (*http.Response, map[string]any) {
	t.Helper()
	payload, _ := json.Marshal(map[string]any{
		"model": "glm-5", "messages": []map[string]any{{"role": "user", "content": "hi"}},
	})
	return e.guardPostBody(t, apiKey, payload)
}

// guardPostBody 用指定原始请求体调 /v1/chat/completions。
func (e *testEnv) guardPostBody(t *testing.T, apiKey string, payload []byte) (*http.Response, map[string]any) {
	t.Helper()
	req, err := http.NewRequest("POST", e.srv.URL+"/v1/chat/completions", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("构建请求失败: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("门禁请求失败: %v", err)
	}
	defer resp.Body.Close()
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	return resp, body
}

// setSetting 写入设置项（rawJSON 为 JSON 原始值）。
func (e *testEnv) setSetting(t *testing.T, key, rawJSON string) {
	t.Helper()
	if err := e.deps.Settings.Set(context.Background(), key, json.RawMessage(rawJSON)); err != nil {
		t.Fatalf("写入设置 %s 失败: %v", key, err)
	}
}

// TestRelayGuardRejections 表驱动覆盖 /v1 门禁的七种拒绝路径，
// 断言响应体为 OpenAI 错误格式且 error.code 取值正确。
func TestRelayGuardRejections(t *testing.T) {
	e := newTestEnv(t)

	// 阻塞式上游桩：并发用例用它占住唯一的并发槽位。
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	blockingUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entered <- struct{}{}
		<-release
		fmt.Fprint(w, `{"id":"x","model":"glm-5","choices":[{"message":{"content":"hi"}}],
			"usage":{"prompt_tokens":10,"completion_tokens":10}}`)
	}))
	defer blockingUpstream.Close()

	cases := []struct {
		name string
		// setup 返回本用例使用的 API Key（空串 = 不带认证头）与可选清理函数。
		setup      func(t *testing.T) (apiKey string, cleanup func())
		wantStatus int
		wantCode   string
	}{
		{
			name:       "密钥缺失",
			setup:      func(t *testing.T) (string, func()) { return "", nil },
			wantStatus: http.StatusUnauthorized,
			wantCode:   "invalid_api_key",
		},
		{
			name: "密钥禁用",
			setup: func(t *testing.T) (string, func()) {
				uid, key := e.seedRelayUser(t, "guard-disabled", 0, nil)
				e.db.Model(&store.APIKey{}).Where("user_id = ?", uid).
					Update("status", domain.KeyDisabled)
				return key, nil
			},
			wantStatus: http.StatusForbidden,
			wantCode:   "key_disabled",
		},
		{
			name: "密钥过期",
			setup: func(t *testing.T) (string, func()) {
				uid, key := e.seedRelayUser(t, "guard-expired", 0, nil)
				e.db.Model(&store.APIKey{}).Where("user_id = ?", uid).
					Update("expires_at", time.Now().Add(-time.Hour))
				return key, nil
			},
			wantStatus: http.StatusForbidden,
			wantCode:   "key_expired",
		},
		{
			name: "来源 IP 不在白名单",
			setup: func(t *testing.T) (string, func()) {
				uid, key := e.seedRelayUser(t, "guard-ip", 0, nil)
				e.db.Model(&store.APIKey{}).Where("user_id = ?", uid).
					Update("allowed_ips", []byte(`["10.255.255.1"]`))
				return key, nil
			},
			wantStatus: http.StatusForbidden,
			wantCode:   "ip_not_allowed",
		},
		{
			name: "账号停用",
			setup: func(t *testing.T) (string, func()) {
				uid, key := e.seedRelayUser(t, "guard-banned", 0, nil)
				e.db.Model(&store.User{}).Where("id = ?", uid).
					Update("status", domain.UserDisabled)
				return key, nil
			},
			wantStatus: http.StatusForbidden,
			wantCode:   "user_disabled",
		},
		{
			name: "每分钟限流",
			setup: func(t *testing.T) (string, func()) {
				_, key := e.seedRelayUser(t, "guard-rpm", 0, nil)
				e.setSetting(t, "rate_limit_per_key_rpm", "1")
				// 第一次请求耗尽窗口配额（能通过门禁即可，后续业务结果不关心）
				if resp, _ := e.guardPost(t, key); resp.StatusCode == http.StatusTooManyRequests {
					t.Fatal("首次请求不应被限流")
				}
				return key, func() { e.setSetting(t, "rate_limit_per_key_rpm", "120") }
			},
			wantStatus: http.StatusTooManyRequests,
			wantCode:   "rate_limited",
		},
		{
			name: "并发已满",
			setup: func(t *testing.T) (string, func()) {
				_, blockedKey := e.seedRelayUser(t, "guard-conc-a", 1_000_000, nil)
				_, key := e.seedRelayUser(t, "guard-conc-b", 0, nil)
				e.seedModel(t, "glm-5")
				e.seedChannel(t, "ch-block", blockingUpstream.URL, 0, []string{"glm-5"}, nil)
				e.setSetting(t, "max_concurrent_requests", "1")

				// 占位请求在阻塞式上游中挂起，持有唯一并发槽位。
				done := make(chan struct{})
				go func() {
					defer close(done)
					payload, _ := json.Marshal(map[string]any{
						"model": "glm-5", "messages": []map[string]any{{"role": "user", "content": "hi"}},
					})
					req, _ := http.NewRequest("POST",
						e.srv.URL+"/v1/chat/completions", bytes.NewReader(payload))
					req.Header.Set("Content-Type", "application/json")
					req.Header.Set("Authorization", "Bearer "+blockedKey)
					resp, err := http.DefaultClient.Do(req)
					if err != nil {
						t.Errorf("占位请求失败: %v", err)
						return
					}
					defer resp.Body.Close()
					if resp.StatusCode != http.StatusOK {
						t.Errorf("占位请求放行后应成功，实际 %d", resp.StatusCode)
					}
				}()
				select {
				case <-entered:
				case <-time.After(5 * time.Second):
					t.Fatal("等待占位请求到达上游超时")
				}
				return key, func() {
					close(release)
					<-done
					e.setSetting(t, "max_concurrent_requests", "40")
				}
			},
			wantStatus: http.StatusServiceUnavailable,
			wantCode:   "overloaded",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			key, cleanup := tc.setup(t)
			if cleanup != nil {
				defer cleanup()
			}
			resp, body := e.guardPost(t, key)
			if resp.StatusCode != tc.wantStatus {
				t.Errorf("期望状态 %d，实际 %d，响应: %v", tc.wantStatus, resp.StatusCode, body)
			}
			errObj, ok := body["error"].(map[string]any)
			if !ok {
				t.Fatalf("响应体应为 OpenAI 错误格式（含 error 对象），实际: %v", body)
			}
			if errObj["code"] != tc.wantCode {
				t.Errorf("期望 error.code=%q，实际 %v", tc.wantCode, errObj["code"])
			}
			if msg, _ := errObj["message"].(string); msg == "" {
				t.Error("error.message 不应为空")
			}
		})
	}
}

// TestIsLargeRequest 大请求判定：按 Content-Length 分类，长度未知按大请求保守处理。
func TestIsLargeRequest(t *testing.T) {
	cases := []struct {
		name          string
		contentLength int64
		want          bool
	}{
		{"空请求体", 0, false},
		{"恰为阈值 1 MiB", 1 << 20, false},
		{"超过阈值", (1 << 20) + 1, true},
		{"长度未知（chunked）", -1, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/v1/chat/completions", nil)
			r.ContentLength = tc.contentLength
			if got := isLargeRequest(r); got != tc.want {
				t.Errorf("ContentLength=%d 期望 %v，实际 %v", tc.contentLength, tc.want, got)
			}
		})
	}
}

// TestRelayGuardLargeRequestQuota 请求体超过 1 MiB 的请求受独立并发配额约束：
// 配额占满时后续大请求被拒绝（503 overloaded），一般请求不受影响。
func TestRelayGuardLargeRequestQuota(t *testing.T) {
	e := newTestEnv(t)

	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	blockingUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entered <- struct{}{}
		<-release
		fmt.Fprint(w, `{"id":"x","model":"glm-5","choices":[{"message":{"content":"hi"}}],
			"usage":{"prompt_tokens":10,"completion_tokens":10}}`)
	}))
	defer blockingUpstream.Close()

	_, blockedKey := e.seedRelayUser(t, "large-quota-a", 1_000_000_000, nil)
	_, probeKey := e.seedRelayUser(t, "large-quota-b", 0, nil)
	e.seedModel(t, "glm-5")
	e.seedChannel(t, "ch-large", blockingUpstream.URL, 0, []string{"glm-5"}, nil)
	e.setSetting(t, "max_concurrent_large_requests", "1")
	defer e.setSetting(t, "max_concurrent_large_requests", "2")

	largePayload := func() []byte {
		b, _ := json.Marshal(map[string]any{
			"model": "glm-5",
			"messages": []map[string]any{{"role": "user",
				"content": strings.Repeat("a", (1<<20)+1024)}},
		})
		return b
	}

	// 占位大请求在阻塞式上游中挂起，持有唯一的大请求槽位。
	done := make(chan struct{})
	go func() {
		defer close(done)
		req, _ := http.NewRequest("POST",
			e.srv.URL+"/v1/chat/completions", bytes.NewReader(largePayload()))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+blockedKey)
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
	defer func() {
		close(release)
		<-done
	}()

	// 大请求配额占满 → 第二个大请求被拒绝。
	resp, body := e.guardPostBody(t, probeKey, largePayload())
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("大请求配额占满应返回 503，实际 %d，响应: %v", resp.StatusCode, body)
	}
	if errObj, ok := body["error"].(map[string]any); !ok || errObj["code"] != "overloaded" {
		t.Errorf("期望 error.code=overloaded，实际: %v", body)
	}

	// 一般请求不占大请求配额，应通过并发闸门（之后因业务原因失败也不应是 503）。
	respSmall, bodySmall := e.guardPost(t, probeKey)
	if respSmall.StatusCode == http.StatusServiceUnavailable {
		t.Errorf("一般请求不应被大请求配额拒绝，响应: %v", bodySmall)
	}
}
