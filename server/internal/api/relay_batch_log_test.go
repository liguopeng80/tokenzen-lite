package api

// 批量请求下用量日志无丢失（架构评审报告 P3-2 测试计划）：
// 成功与失败混合的连续请求，usage_logs 行数与请求数一致——
// 有界队列在常规容量下不触发丢弃，队列化不丢日志。

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

func TestRelayBatchRequestsNoLogLoss(t *testing.T) {
	e := newTestEnv(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		if bytes.Contains(raw, []byte("FAIL")) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(400)
			fmt.Fprint(w, `{"error":{"message":"bad request","type":"invalid_request_error"}}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"x","model":"glm-batch","choices":[{"message":{"content":"hi"}}],
			"usage":{"prompt_tokens":100,"completion_tokens":50}}`)
	}))
	defer upstream.Close()

	userID, key := e.seedRelayUser(t, "batchlog", 10_000_000, nil)
	e.seedModel(t, "pub-batch-model")
	e.seedChannel(t, "ch-batch", upstream.URL, 0, []string{"pub-batch-model"},
		map[string]string{"pub-batch-model": "glm-batch"})

	const total = 30
	okCount, failCount := 0, 0
	for i := 0; i < total; i++ {
		wantFail := i%3 == 2 // 每第 3 个请求让上游返回 400（失败退款路径同样必须落日志）
		content := fmt.Sprintf("msg-%d", i)
		if wantFail {
			content = fmt.Sprintf("FAIL-%d", i)
		}
		payload, _ := json.Marshal(map[string]any{
			"model":    "pub-batch-model",
			"messages": []map[string]any{{"role": "user", "content": content}},
		})
		req, _ := http.NewRequest("POST", e.srv.URL+"/v1/chat/completions", bytes.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+key)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("第 %d 个请求失败: %v", i, err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if wantFail {
			if resp.StatusCode != 400 {
				t.Fatalf("第 %d 个请求应失败 400，实际 %d", i, resp.StatusCode)
			}
			failCount++
		} else {
			if resp.StatusCode != 200 {
				t.Fatalf("第 %d 个请求应成功 200，实际 %d", i, resp.StatusCode)
			}
			okCount++
		}
	}

	// 轮询等待异步队列全部落库
	deadline := time.Now().Add(5 * time.Second)
	var count int64
	for time.Now().Before(deadline) {
		e.db.Model(&store.UsageLog{}).Where("user_id = ?", userID).Count(&count)
		if count == total {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if count != total {
		t.Fatalf("用量日志应无丢失：期望 %d 条（成功 %d + 失败 %d），实际 %d",
			total, okCount, failCount, count)
	}
	e.assertReconcile(t)
}

// 停机序列刷盘：处理若干请求后按停机顺序（handler 已结束 → Close writer）关闭，
// Close 返回后队列中全部日志已落库，无需轮询等待。
// main.go 的信号处理流程不在本层覆盖。
func TestRelayShutdownFlushesUsageLogs(t *testing.T) {
	e := newTestEnv(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"x","model":"glm-shut","choices":[{"message":{"content":"hi"}}],
			"usage":{"prompt_tokens":10,"completion_tokens":5}}`)
	}))
	defer upstream.Close()

	userID, key := e.seedRelayUser(t, "shutflush", 1_000_000, nil)
	e.seedModel(t, "pub-shut-model")
	e.seedChannel(t, "ch-shut", upstream.URL, 0, []string{"pub-shut-model"},
		map[string]string{"pub-shut-model": "glm-shut"})

	const total = 5
	for i := 0; i < total; i++ {
		resp, _ := e.relayPost(t, key, map[string]any{
			"model":    "pub-shut-model",
			"messages": []map[string]any{{"role": "user", "content": fmt.Sprintf("m-%d", i)}},
		})
		if resp.StatusCode != 200 {
			t.Fatalf("第 %d 个请求应成功，实际 %d", i, resp.StatusCode)
		}
	}

	// 此时全部 handler 已返回（relayPost 同步读完响应体），按停机顺序关闭 writer
	closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	e.deps.Relay.Close(closeCtx)

	// Close 返回即已刷盘：直接断言行数，不轮询
	var count int64
	e.db.Model(&store.UsageLog{}).Where("user_id = ?", userID).Count(&count)
	if count != total {
		t.Fatalf("停机刷盘后应有 %d 条用量日志，实际 %d", total, count)
	}
	e.assertReconcile(t)
}
