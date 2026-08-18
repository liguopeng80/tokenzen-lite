package relay

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
)

// Anthropic 请求 → canonical → OpenAI 请求（Claude Code→GLM 方向）。
func TestAnthropicToOpenAIRequest(t *testing.T) {
	anthropicReq := `{
		"model": "claude-sonnet-4-6",
		"max_tokens": 1024,
		"system": "You are helpful.",
		"messages": [
			{"role": "user", "content": "帮我查天气"},
			{"role": "assistant", "content": [
				{"type": "text", "text": "好的"},
				{"type": "tool_use", "id": "tu_1", "name": "get_weather",
				 "input": {"city": "上海"}}
			]},
			{"role": "user", "content": [
				{"type": "tool_result", "tool_use_id": "tu_1", "content": "晴 32 度"}
			]}
		],
		"tools": [{"name": "get_weather", "description": "查天气",
			"input_schema": {"type": "object", "properties": {"city": {"type": "string"}}}}],
		"tool_choice": {"type": "auto"},
		"stop_sequences": ["END"]
	}`
	var body map[string]any
	if err := json.Unmarshal([]byte(anthropicReq), &body); err != nil {
		t.Fatalf("测试数据解析失败: %v", err)
	}
	canon, err := decodeAnthropicRequest(body)
	if err != nil {
		t.Fatalf("decode 失败: %v", err)
	}
	if canon.System != "You are helpful." || canon.MaxTokens != 1024 {
		t.Errorf("system/max_tokens 解析错误: %q %d", canon.System, canon.MaxTokens)
	}
	if len(canon.Tools) != 1 || canon.Tools[0].Name != "get_weather" {
		t.Fatalf("工具解析错误: %+v", canon.Tools)
	}
	if canon.ToolChoice == nil || canon.ToolChoice.Mode != ToolChoiceAuto {
		t.Fatalf("tool_choice 应为 auto: %+v", canon.ToolChoice)
	}

	out := encodeOpenAIRequest(canon, "glm-5-turbo")
	raw, _ := json.Marshal(out)
	s := string(raw)
	if out["model"] != "glm-5-turbo" {
		t.Errorf("上游模型名错误: %v", out["model"])
	}
	msgs := out["messages"].([]map[string]any)
	if msgs[0]["role"] != "system" || msgs[0]["content"] != "You are helpful." {
		t.Errorf("system 应转为首条 system 消息: %v", msgs[0])
	}
	// assistant 的 tool_use 应转为 tool_calls
	if !strings.Contains(s, `"tool_calls"`) || !strings.Contains(s, `"get_weather"`) {
		t.Errorf("应包含 tool_calls: %s", s)
	}
	// tool_result 应转为 role=tool 消息
	foundTool := false
	for _, m := range msgs {
		if m["role"] == "tool" && m["tool_call_id"] == "tu_1" && m["content"] == "晴 32 度" {
			foundTool = true
		}
	}
	if !foundTool {
		t.Errorf("tool_result 应转为 tool 角色消息: %v", msgs)
	}
	if !strings.Contains(s, `"stop":["END"]`) {
		t.Errorf("stop_sequences 应转为 stop: %s", s)
	}
	// arguments 应为 JSON 字符串
	if !strings.Contains(s, `"arguments":"{\"city\":\"上海\"}"`) {
		t.Errorf("tool_use input 应序列化为 arguments 字符串: %s", s)
	}
}

// OpenAI 响应 → canonical → Anthropic 响应。
func TestOpenAIToAnthropicResponse(t *testing.T) {
	openaiResp := `{
		"id": "chatcmpl-1", "model": "glm-5-turbo",
		"choices": [{"finish_reason": "tool_calls", "message": {
			"content": "我来查一下",
			"tool_calls": [{"id": "call_1", "type": "function",
				"function": {"name": "get_weather", "arguments": "{\"city\":\"上海\"}"}}]
		}}],
		"usage": {"prompt_tokens": 100, "completion_tokens": 30,
			"prompt_tokens_details": {"cached_tokens": 40}}
	}`
	canon, usage, err := decodeOpenAIResponse([]byte(openaiResp))
	if err != nil {
		t.Fatalf("decode 失败: %v", err)
	}
	if usage.BaseInput != 60 || usage.CacheRead != 40 || usage.Output != 30 {
		t.Errorf("usage 归一化错误: %+v", usage)
	}
	if canon.StopReason != StopToolUse {
		t.Errorf("finish_reason=tool_calls 应映射 tool_use: %s", canon.StopReason)
	}

	out := encodeAnthropicResponse(canon, "claude-sonnet-4-6", usage)
	var resp map[string]any
	_ = json.Unmarshal(out, &resp)
	if resp["model"] != "claude-sonnet-4-6" || resp["stop_reason"] != "tool_use" {
		t.Errorf("响应头信息错误: %v", resp)
	}
	content := resp["content"].([]any)
	if len(content) != 2 {
		t.Fatalf("应有 text + tool_use 两个块: %v", content)
	}
	tu := content[1].(map[string]any)
	if tu["type"] != "tool_use" || tu["name"] != "get_weather" {
		t.Errorf("tool_use 块错误: %v", tu)
	}
	input := tu["input"].(map[string]any)
	if input["city"] != "上海" {
		t.Errorf("tool input 应还原为对象: %v", input)
	}
	au := resp["usage"].(map[string]any)
	if au["input_tokens"].(float64) != 60 || au["cache_read_input_tokens"].(float64) != 40 {
		t.Errorf("anthropic usage 输出错误: %v", au)
	}
}

// OpenAI chunk 流 → Anthropic SSE 事件序列重建（含工具调用）。
func TestOpenAIStreamToAnthropicEvents(t *testing.T) {
	chunks := []string{
		`{"choices":[{"delta":{"content":"你"}}]}`,
		`{"choices":[{"delta":{"content":"好"}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1",
			"function":{"name":"get_weather","arguments":""}}]}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,
			"function":{"arguments":"{\"city\":"}}]}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,
			"function":{"arguments":"\"上海\"}"}}]}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`{"choices":[],"usage":{"prompt_tokens":100,"completion_tokens":25}}`,
	}
	cs := (&canonicalConduit{
		ds: dsAnthropic, up: domain.ProtocolOpenAICompat, publicModel: "claude-sonnet-4-6",
	}).NewStream()

	var output strings.Builder
	for _, c := range chunks {
		for _, f := range cs.ProcessPayload([]byte(c)) {
			output.Write(f)
		}
	}
	for _, f := range cs.ProcessDone() {
		output.Write(f)
	}
	got := output.String()

	// 事件顺序校验
	order := []string{
		"event: message_start",
		"event: content_block_start", // text 块
		`"text":"你"`,
		`"text":"好"`,
		"event: content_block_stop",
		`"name":"get_weather"`,
		`"partial_json":"{\"city\":"`,
		`"partial_json":"\"上海\"}"`,
		"event: message_delta",
		`"stop_reason":"tool_use"`,
		`"output_tokens":25`,
		"event: message_stop",
	}
	pos := 0
	for _, marker := range order {
		idx := strings.Index(got[pos:], marker)
		if idx < 0 {
			t.Fatalf("事件序列缺失或顺序错误，未找到 %q。\n完整输出:\n%s", marker, got)
		}
		pos += idx
	}
	// model 名必须是公开名
	if strings.Contains(got, "glm") {
		t.Errorf("输出不应泄露上游模型名: %s", got)
	}
	usage, found := cs.Usage()
	if !found || usage.BaseInput != 100 || usage.Output != 25 {
		t.Errorf("usage 嗅探错误: found=%v %+v", found, usage)
	}
}

// Anthropic 事件流 → OpenAI chunk 流（反向）。
func TestAnthropicStreamToOpenAIChunks(t *testing.T) {
	events := []string{
		`{"type":"message_start","message":{"usage":{"input_tokens":80,"cache_read_input_tokens":20}}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":12}}`,
		`{"type":"message_stop"}`,
	}
	cs := (&canonicalConduit{
		ds: dsOpenAI, up: domain.ProtocolAnthropic, publicModel: "claude-opus-4-6",
	}).NewStream()

	var out strings.Builder
	for _, ev := range events {
		for _, f := range cs.ProcessPayload([]byte(ev)) {
			out.Write(f)
		}
	}
	for _, f := range cs.ProcessDone() {
		out.Write(f)
	}
	got := out.String()
	if !strings.Contains(got, `"content":"Hello"`) {
		t.Errorf("应含文本增量 chunk: %s", got)
	}
	if !strings.Contains(got, `"finish_reason":"stop"`) {
		t.Errorf("应含终止 chunk: %s", got)
	}
	if !strings.HasSuffix(got, "data: [DONE]\n\n") {
		t.Errorf("应以 [DONE] 结束: %s", got)
	}
	usage, found := cs.Usage()
	if !found || usage.BaseInput != 80 || usage.CacheRead != 20 || usage.Output != 12 {
		t.Errorf("usage 嗅探错误: %+v", usage)
	}
}

// Gemini 响应解码。
func TestDecodeGeminiResponse(t *testing.T) {
	raw := `{
		"candidates": [{"finishReason": "STOP", "content": {"parts": [{"text": "回答"}]}}],
		"usageMetadata": {"promptTokenCount": 150, "candidatesTokenCount": 40,
			"cachedContentTokenCount": 50}
	}`
	canon, usage, err := decodeGeminiResponse([]byte(raw))
	if err != nil {
		t.Fatalf("decode 失败: %v", err)
	}
	if usage.BaseInput != 100 || usage.CacheRead != 50 || usage.Output != 40 {
		t.Errorf("gemini usage 归一化错误: %+v", usage)
	}
	if usage.Semantic != domain.SemanticGemini {
		t.Errorf("语义标记错误: %s", usage.Semantic)
	}
	if len(canon.Parts) != 1 || canon.Parts[0].Text != "回答" {
		t.Errorf("内容解析错误: %+v", canon.Parts)
	}
}

// OpenAI 请求 → canonical → Gemini 请求。
func TestOpenAIToGeminiRequest(t *testing.T) {
	var body map[string]any
	_ = json.Unmarshal([]byte(`{
		"model": "gemini-3-flash", "max_tokens": 256, "temperature": 0.5,
		"messages": [
			{"role": "system", "content": "简洁回答"},
			{"role": "user", "content": "你好"},
			{"role": "assistant", "content": "你好！"},
			{"role": "user", "content": "再见"}
		]
	}`), &body)
	canon, err := decodeOpenAIRequest(body)
	if err != nil {
		t.Fatalf("decode 失败: %v", err)
	}
	out := encodeGeminiRequest(canon)
	raw, _ := json.Marshal(out)
	s := string(raw)
	if !strings.Contains(s, `"systemInstruction"`) || !strings.Contains(s, "简洁回答") {
		t.Errorf("system 应转为 systemInstruction: %s", s)
	}
	contents := out["contents"].([]map[string]any)
	if len(contents) != 3 {
		t.Fatalf("应有 3 条 contents: %d", len(contents))
	}
	if contents[1]["role"] != "model" {
		t.Errorf("assistant 应转为 model 角色: %v", contents[1])
	}
	if !strings.Contains(s, `"maxOutputTokens":256`) {
		t.Errorf("generationConfig 错误: %s", s)
	}
}

// Anthropic 直通流：usage 嗅探 + message_start 的 model 改写。
func TestAnthropicPassthroughStream(t *testing.T) {
	cs := (&anthropicPassthrough{publicModel: "claude-opus-4-6"}).NewStream()
	frames := cs.ProcessPayload([]byte(
		`{"type":"message_start","message":{"model":"claude-internal-xyz",` +
			`"usage":{"input_tokens":10,"cache_creation_input_tokens":5}}}`))
	joined := string(frames[0])
	if !strings.Contains(joined, "event: message_start") {
		t.Errorf("应重建 event 行: %s", joined)
	}
	if !strings.Contains(joined, `"model":"claude-opus-4-6"`) || strings.Contains(joined, "internal-xyz") {
		t.Errorf("message_start 中的 model 应改写: %s", joined)
	}
	cs.ProcessPayload([]byte(`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":7}}`))
	usage, found := cs.Usage()
	if !found || usage.BaseInput != 10 || usage.CacheWrite != 5 || usage.Output != 7 {
		t.Errorf("直通流 usage 嗅探错误: %+v", usage)
	}
}
