package relay

// stream_codec 单元测试：聚焦 anthropicStreamDecoder 对 thinking/redacted_thinking
// 内容块的处理——跨协议（Anthropic 上游 → OpenAI/Anthropic 下游）流式路径下，
// 推理块不应丢失/误并入文本，块索引不错位，usage 嗅探不被干扰。
//
// 不依赖数据库，可直接 `go test ./internal/relay/ -run TestAnthropicStream`。

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
)

// realThinkingEventSequence 模拟真实 Anthropic 带 thinking 的 SSE 事件序列：
//
//	message_start
//	thinking 块（thinking_delta + signature_delta）
//	text 块
//	tool_use 块
//	message_delta + message_stop
//
// 结构参考 ~/copilot/llm-proxy/testcases/0014_post_v1_messages.json。
func realThinkingEventSequence() []string {
	return []string{
		`{"type":"message_start","message":{"id":"msg_1","usage":{` +
			`"input_tokens":120,"cache_read_input_tokens":30,"cache_creation_input_tokens":10}}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"REASONING LEAKED"}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"sig-abc"}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}`,
		`{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"Hello"}}`,
		`{"type":"content_block_stop","index":1}`,
		`{"type":"content_block_start","index":2,"content_block":{"type":"tool_use","id":"tool_1","name":"get_weather","input":{}}}`,
		`{"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"{\"city\":\"上海\"}"}}`,
		`{"type":"content_block_stop","index":2}`,
		`{"type":"message_delta","delta":{"stop_reason":"tool_use","stop_sequence":null},"usage":{"output_tokens":45}}`,
		`{"type":"message_stop"}`,
	}
}

// TestAnthropicStreamDecoderThinkingNotInText 验证 decoder 直接行为：
// thinking 增量不被误解析为 text_delta，且 thinking 块的 content_block_stop 不产 block_stop。
func TestAnthropicStreamDecoderThinkingNotInText(t *testing.T) {
	dec := newAnthropicStreamDecoder()
	var got []CanonEvent
	for _, payload := range realThinkingEventSequence() {
		got = append(got, dec.Decode([]byte(payload))...)
	}

	// 收集所有 text_delta 的文本与 block_stop 计数。
	var textBuf strings.Builder
	blockStops := 0
	toolStarts := 0
	for _, ev := range got {
		switch ev.Type {
		case "text_delta":
			textBuf.WriteString(ev.Text)
		case "block_stop":
			blockStops++
		case "tool_use_start":
			toolStarts++
		}
	}

	// thinking 内容不得泄漏到 canonical text 流。
	if strings.Contains(textBuf.String(), "REASONING") {
		t.Errorf("thinking 文本泄漏到 text_delta: %q", textBuf.String())
	}
	if textBuf.String() != "Hello" {
		t.Errorf("文本累积错误，want=Hello got=%q", textBuf.String())
	}

	// 3 个 content block（thinking/text/tool_use），但 thinking 不产 block_stop，
	// 因此 block_stop 应为 2（text + tool_use）。
	if blockStops != 2 {
		t.Errorf("block_stop 计数错误，want=2（text+tool_use，不含 thinking）got=%d", blockStops)
	}
	if toolStarts != 1 {
		t.Errorf("tool_use_start 计数错误，want=1 got=%d", toolStarts)
	}

	// usage 嗅探：input/cache/output 都应正确，thinking 块不干扰。
	usage, found := dec.Usage()
	if !found {
		t.Fatalf("usage 未嗅探到")
	}
	if usage.BaseInput != 120 || usage.CacheRead != 30 || usage.CacheWrite != 10 || usage.Output != 45 {
		t.Errorf("usage 嗅探错误: %+v", usage)
	}
	if usage.Semantic != domain.SemanticAnthropic {
		t.Errorf("usage 语义标记错误: %s", usage.Semantic)
	}
}

// TestAnthropicStreamDecoderRedactedThinking 验证 redacted_thinking 块
// （content_block_start 携带完整 data，无 delta）被跳过且不产 block_stop。
func TestAnthropicStreamDecoderRedactedThinking(t *testing.T) {
	payloads := []string{
		`{"type":"message_start","message":{"usage":{"input_tokens":50}}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"redacted_thinking","data":"ENCRYPTED-BLOB"}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}`,
		`{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"answer"}}`,
		`{"type":"content_block_stop","index":1}`,
		`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":8}}`,
		`{"type":"message_stop"}`,
	}
	dec := newAnthropicStreamDecoder()
	var got []CanonEvent
	for _, p := range payloads {
		got = append(got, dec.Decode([]byte(p))...)
	}

	var textBuf strings.Builder
	blockStops := 0
	for _, ev := range got {
		switch ev.Type {
		case "text_delta":
			textBuf.WriteString(ev.Text)
		case "block_stop":
			blockStops++
		}
	}
	if strings.Contains(textBuf.String(), "ENCRYPTED") {
		t.Errorf("redacted_thinking 数据泄漏: %q", textBuf.String())
	}
	if textBuf.String() != "answer" {
		t.Errorf("文本错误: %q", textBuf.String())
	}
	// 仅 text 块产 block_stop（redacted_thinking 不产）。
	if blockStops != 1 {
		t.Errorf("block_stop 计数错误，want=1 got=%d", blockStops)
	}
	usage, _ := dec.Usage()
	if usage.BaseInput != 50 || usage.Output != 8 {
		t.Errorf("usage 错误: %+v", usage)
	}
}

// TestAnthropicStreamThinkingToOpenAI 端到端：Anthropic 上游（带 thinking）
// → OpenAI 下游，验证推理内容不出现、文本与工具调用正确、usage 不被破坏。
func TestAnthropicStreamThinkingToOpenAI(t *testing.T) {
	cs := (&canonicalConduit{
		ds: dsOpenAI, up: domain.ProtocolAnthropic, publicModel: "gpt-4o",
	}).NewStream()

	var out strings.Builder
	for _, payload := range realThinkingEventSequence() {
		for _, f := range cs.ProcessPayload([]byte(payload)) {
			out.Write(f)
		}
	}
	for _, f := range cs.ProcessDone() {
		out.Write(f)
	}
	got := out.String()

	// thinking 内容不得出现在 OpenAI 下游流。
	if strings.Contains(got, "REASONING") || strings.Contains(got, "sig-abc") {
		t.Errorf("thinking 内容泄漏到 OpenAI 流: %s", got)
	}
	// 文本与工具调用应保留。
	if !strings.Contains(got, `"content":"Hello"`) {
		t.Errorf("文本增量丢失: %s", got)
	}
	if !strings.Contains(got, `"name":"get_weather"`) {
		t.Errorf("工具调用丢失: %s", got)
	}
	if !strings.Contains(got, `"finish_reason":"tool_calls"`) {
		t.Errorf("stop_reason 映射错误: %s", got)
	}
	// usage 终帧。
	if !strings.Contains(got, `"prompt_tokens":150`) { // BaseInput 120 + CacheRead 30
		t.Errorf("usage prompt_tokens 错误: %s", got)
	}
	if !strings.Contains(got, `"completion_tokens":45`) {
		t.Errorf("usage completion_tokens 错误: %s", got)
	}
}

// TestAnthropicStreamThinkingToAnthropicBlockIndex 端到端：Anthropic 上游（带 thinking）
// 经 canonical 重建为 Anthropic 下游事件，验证重建后的 content_block 索引从 0 连续递增，
// thinking 块不占据索引位（因其在 canonical 中被跳过）。
func TestAnthropicStreamThinkingToAnthropicBlockIndex(t *testing.T) {
	cs := (&canonicalConduit{
		ds: dsAnthropic, up: domain.ProtocolAnthropic, publicModel: "claude-sonnet-4-6",
	}).NewStream()

	var out strings.Builder
	for _, payload := range realThinkingEventSequence() {
		for _, f := range cs.ProcessPayload([]byte(payload)) {
			out.Write(f)
		}
	}
	for _, f := range cs.ProcessDone() {
		out.Write(f)
	}
	got := out.String()

	// 解析所有 content_block_start 的 index，按出现顺序收集。
	var indices []int
	for _, line := range strings.Split(got, "\n") {
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		var ev struct {
			Type         string `json:"type"`
			Index        *int   `json:"index"`
			ContentBlock *struct {
				Type string `json:"type"`
			} `json:"content_block"`
		}
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data:")), &ev); err != nil {
			continue
		}
		if ev.Type == "content_block_start" && ev.ContentBlock != nil && ev.Index != nil {
			indices = append(indices, *ev.Index)
		}
	}

	// 上游 thinking(text?)tool_use 三块；thinking 在 canonical 被跳过，
	// 重建后下游只有 text(0) 与 tool_use(1)，索引从 0 连续。
	want := []int{0, 1}
	if len(indices) != len(want) {
		t.Fatalf("content_block_start 数量错误，want=%v got=%v (full=%s)", want, indices, got)
	}
	for i := range want {
		if indices[i] != want[i] {
			t.Errorf("content_block_start index[%d] 错误，want=%d got=%d\n%s", i, want[i], indices[i], got)
		}
	}
	// thinking 内容不得出现在重建流。
	if strings.Contains(got, "REASONING") {
		t.Errorf("thinking 内容泄漏到 Anthropic 重建流: %s", got)
	}
}

// TestAnthropicStreamDecoderThinkingGuardOnDelta 验证：即使某个 thinking 块内
// 出现 type=text_delta 的增量（防御性场景），blockType 守卫也会阻止其并入文本流。
func TestAnthropicStreamDecoderThinkingGuardOnDelta(t *testing.T) {
	dec := newAnthropicStreamDecoder()
	payloads := []string{
		`{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}`,
		// 异常但需防御：thinking 块内夹带 text_delta。
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"LEAK"}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}`,
		`{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"OK"}}`,
		`{"type":"content_block_stop","index":1}`,
	}
	var got []CanonEvent
	for _, p := range payloads {
		got = append(got, dec.Decode([]byte(p))...)
	}
	var textBuf strings.Builder
	for _, ev := range got {
		if ev.Type == "text_delta" {
			textBuf.WriteString(ev.Text)
		}
	}
	if textBuf.String() != "OK" {
		t.Errorf("thinking 块内的 text_delta 未被守卫拦截: %q", textBuf.String())
	}
}
