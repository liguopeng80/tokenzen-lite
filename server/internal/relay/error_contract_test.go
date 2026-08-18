package relay

// 契约固化测试：docs/api-contract.md /v1 章节所述的错误格式与流式收尾帧格式，
// 以本文件断言的实际行为为准（报告 P2-16）。

import (
	"encoding/json"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
)

// decodeJSON 把响应体解析为 map，失败即 fatal。
func decodeJSON(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("响应体不是合法 JSON: %v\n%s", err, raw)
	}
	return m
}

// OpenAI 错误格式：{"error":{"message","type","code"}} 且 type == code。
func TestWriteOpenAIErrorFormat(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteOpenAIError(rec, 402, "insufficient_credits", "积分余额不足")

	if rec.Code != 402 {
		t.Errorf("状态码应为 402，实际 %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type 应为 application/json，实际 %q", ct)
	}
	body := decodeJSON(t, rec.Body.Bytes())
	errObj, ok := body["error"].(map[string]any)
	if !ok {
		t.Fatalf("响应体应含 error 对象，实际: %v", body)
	}
	if errObj["message"] != "积分余额不足" {
		t.Errorf("error.message 错误: %v", errObj["message"])
	}
	if errObj["type"] != "insufficient_credits" || errObj["code"] != "insufficient_credits" {
		t.Errorf("error.type 与 error.code 均应为业务码且相等，实际 type=%v code=%v",
			errObj["type"], errObj["code"])
	}
	if _, hasTopType := body["type"]; hasTopType {
		t.Errorf("OpenAI 格式不应含顶层 type 字段: %v", body)
	}
}

// Anthropic 错误格式：{"type":"error","error":{"type","message"}}，无 code 字段。
func TestWriteAnthropicErrorFormat(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteAnthropicError(rec, 404, "model_not_found", "模型不存在")

	if rec.Code != 404 {
		t.Errorf("状态码应为 404，实际 %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type 应为 application/json，实际 %q", ct)
	}
	body := decodeJSON(t, rec.Body.Bytes())
	if body["type"] != "error" {
		t.Errorf("顶层 type 应为 error，实际 %v", body["type"])
	}
	errObj, ok := body["error"].(map[string]any)
	if !ok {
		t.Fatalf("响应体应含 error 对象，实际: %v", body)
	}
	if errObj["type"] != "model_not_found" || errObj["message"] != "模型不存在" {
		t.Errorf("error.type/message 错误: %v", errObj)
	}
	if _, hasCode := errObj["code"]; hasCode {
		t.Errorf("Anthropic 格式不应含 error.code 字段: %v", errObj)
	}
}

// 上游错误透传：body 含 error 键 → 原状态码 + body 原样（JSON 语义等价）透传；
// body 非 JSON → 按下游协议包装为 upstream_error。
func TestPassUpstreamErrorPassthrough(t *testing.T) {
	upstreamBody := []byte(`{"error":{"message":"context length exceeded",` +
		`"type":"invalid_request_error","code":"ctx_len"},"request_id":"req-1"}`)

	for _, ds := range []downstream{dsOpenAI, dsAnthropic} {
		t.Run("json_error_passthrough_"+string(ds), func(t *testing.T) {
			rec := httptest.NewRecorder()
			passUpstreamError(rec, 400, upstreamBody, ds)
			if rec.Code != 400 {
				t.Errorf("应透传原状态码 400，实际 %d", rec.Code)
			}
			got := decodeJSON(t, rec.Body.Bytes())
			want := decodeJSON(t, upstreamBody)
			if !reflect.DeepEqual(got, want) {
				t.Errorf("上游错误体应原样透传（不随下游协议改写）。\n期望: %v\n实际: %v", want, got)
			}
		})
	}

	t.Run("non_json_wrapped_openai", func(t *testing.T) {
		rec := httptest.NewRecorder()
		passUpstreamError(rec, 500, []byte("upstream exploded"), dsOpenAI)
		if rec.Code != 500 {
			t.Errorf("状态码应为 500，实际 %d", rec.Code)
		}
		body := decodeJSON(t, rec.Body.Bytes())
		errObj, _ := body["error"].(map[string]any)
		if errObj == nil || errObj["code"] != "upstream_error" {
			t.Errorf("非 JSON 上游体应包装为 OpenAI 格式 upstream_error: %v", body)
		}
		if msg, _ := errObj["message"].(string); !strings.Contains(msg, "upstream exploded") {
			t.Errorf("包装后 message 应含上游原文: %v", errObj["message"])
		}
	})

	t.Run("non_json_wrapped_anthropic", func(t *testing.T) {
		rec := httptest.NewRecorder()
		passUpstreamError(rec, 500, []byte("upstream exploded"), dsAnthropic)
		body := decodeJSON(t, rec.Body.Bytes())
		if body["type"] != "error" {
			t.Errorf("非 JSON 上游体在 Anthropic 下游应有顶层 type=error: %v", body)
		}
		errObj, _ := body["error"].(map[string]any)
		if errObj == nil || errObj["type"] != "upstream_error" {
			t.Errorf("应包装为 Anthropic 格式 upstream_error: %v", body)
		}
	})
}

// parseSSEData 提取一帧 SSE 的 data 负载。
func parseSSEData(t *testing.T, frame []byte) []byte {
	t.Helper()
	for _, line := range strings.Split(string(frame), "\n") {
		if strings.HasPrefix(line, "data: ") {
			return []byte(strings.TrimPrefix(line, "data: "))
		}
	}
	t.Fatalf("帧中无 data 行: %q", frame)
	return nil
}

// OpenAI 下游流式收尾：finish_reason 终结 chunk → usage chunk（choices 空数组）→ [DONE]。
func TestOpenAIStreamEncoderFinish(t *testing.T) {
	enc := newOpenAIStreamEncoder("pub-model")
	frames := enc.Finish(domain.NormalizedUsage{BaseInput: 100, CacheRead: 20, Output: 30})
	if len(frames) != 3 {
		t.Fatalf("收尾应输出 3 帧，实际 %d: %q", len(frames), frames)
	}

	// 帧 1：finish_reason 终结 chunk
	var final struct {
		Model   string `json:"model"`
		Choices []struct {
			FinishReason *string        `json:"finish_reason"`
			Delta        map[string]any `json:"delta"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(parseSSEData(t, frames[0]), &final); err != nil {
		t.Fatalf("终结 chunk 解析失败: %v", err)
	}
	if len(final.Choices) != 1 || final.Choices[0].FinishReason == nil ||
		*final.Choices[0].FinishReason != "stop" {
		t.Errorf("首帧应为 finish_reason=stop 的终结 chunk: %s", frames[0])
	}
	if final.Model != "pub-model" {
		t.Errorf("终结 chunk model 应为公开模型名，实际 %q", final.Model)
	}

	// 帧 2：usage chunk，choices 为空数组
	var usageChunk struct {
		Choices []any `json:"choices"`
		Usage   *struct {
			PromptTokens     int64 `json:"prompt_tokens"`
			CompletionTokens int64 `json:"completion_tokens"`
			TotalTokens      int64 `json:"total_tokens"`
		} `json:"usage"`
	}
	raw := parseSSEData(t, frames[1])
	if err := json.Unmarshal(raw, &usageChunk); err != nil {
		t.Fatalf("usage chunk 解析失败: %v", err)
	}
	if usageChunk.Usage == nil {
		t.Fatalf("第二帧应含 usage: %s", raw)
	}
	if usageChunk.Choices == nil || len(usageChunk.Choices) != 0 {
		t.Errorf("usage chunk 的 choices 应为空数组: %s", raw)
	}
	if usageChunk.Usage.PromptTokens != 120 { // BaseInput 100 + CacheRead 20
		t.Errorf("prompt_tokens 应为 BaseInput+CacheRead=120，实际 %d", usageChunk.Usage.PromptTokens)
	}
	if usageChunk.Usage.CompletionTokens != 30 || usageChunk.Usage.TotalTokens != 150 {
		t.Errorf("completion/total 应为 30/150，实际 %d/%d",
			usageChunk.Usage.CompletionTokens, usageChunk.Usage.TotalTokens)
	}

	// 帧 3：[DONE]
	if string(frames[2]) != "data: [DONE]\n\n" {
		t.Errorf("末帧应为 data: [DONE]，实际 %q", frames[2])
	}
}

// Anthropic 下游流式收尾：message_delta（usage 四字段 + stop_reason）→ message_stop，无 [DONE]。
func TestAnthropicStreamEncoderFinish(t *testing.T) {
	enc := newAnthropicStreamEncoder("pub-model")
	// 先喂一个 finish 事件（记录 stop_reason，不立即出帧），首帧会带出 message_start
	startFrames := enc.Encode(CanonEvent{Type: "finish", StopReason: StopEndTurn})
	frames := enc.Finish(domain.NormalizedUsage{
		BaseInput: 100, CacheRead: 20, CacheWrite: 10, Output: 30,
	})
	all := append(startFrames, frames...)

	if len(frames) < 2 {
		t.Fatalf("收尾至少应输出 message_delta 与 message_stop 两帧，实际 %d", len(frames))
	}
	deltaFrame := frames[len(frames)-2]
	stopFrame := frames[len(frames)-1]

	if !strings.HasPrefix(string(deltaFrame), "event: message_delta\n") {
		t.Fatalf("倒数第二帧应为 message_delta，实际 %q", deltaFrame)
	}
	var delta struct {
		Delta struct {
			StopReason string `json:"stop_reason"`
		} `json:"delta"`
		Usage map[string]any `json:"usage"`
	}
	if err := json.Unmarshal(parseSSEData(t, deltaFrame), &delta); err != nil {
		t.Fatalf("message_delta 解析失败: %v", err)
	}
	if delta.Delta.StopReason != StopEndTurn {
		t.Errorf("stop_reason 应为 %q，实际 %q", StopEndTurn, delta.Delta.StopReason)
	}
	wantUsage := map[string]float64{
		"input_tokens": 100, "output_tokens": 30,
		"cache_read_input_tokens": 20, "cache_creation_input_tokens": 10,
	}
	for field, want := range wantUsage {
		got, ok := delta.Usage[field].(float64)
		if !ok || got != want {
			t.Errorf("message_delta.usage.%s 应为 %v，实际 %v", field, want, delta.Usage[field])
		}
	}

	if !strings.HasPrefix(string(stopFrame), "event: message_stop\n") {
		t.Errorf("末帧应为 message_stop，实际 %q", stopFrame)
	}
	for _, f := range all {
		if strings.Contains(string(f), "[DONE]") {
			t.Errorf("Anthropic 下游流不应输出 [DONE] 帧: %q", f)
		}
	}
}
