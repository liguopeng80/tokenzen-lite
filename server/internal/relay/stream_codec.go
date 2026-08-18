package relay

// 流式编解码：三种上游流 → CanonEvent 序列；CanonEvent 序列 → 两种下游流。

import (
	"encoding/json"
	"fmt"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
)

// --- 上游流解码器 ---

// streamDecoder 把上游的一个 SSE data 负载解码为规范事件序列。
// 解码器同时负责嗅探 usage。
type streamDecoder interface {
	// Decode 处理一个 data 负载（不含 "data: " 前缀，不含 [DONE]）。
	Decode(payload []byte) []CanonEvent
	// Usage 返回累计嗅探到的 usage。
	Usage() (domain.NormalizedUsage, bool)
}

// openaiStreamDecoder OpenAI chunk 流解码。
type openaiStreamDecoder struct {
	usage      domain.NormalizedUsage
	usageFound bool
	// tool_call 增量按 index 聚合
	openToolIdx int  // 当前打开的工具块 index（-1 = 无）
	textOpen    bool // 是否已产生过文本增量
}

func newOpenAIStreamDecoder() *openaiStreamDecoder {
	return &openaiStreamDecoder{openToolIdx: -1}
}

func (d *openaiStreamDecoder) Usage() (domain.NormalizedUsage, bool) {
	return d.usage, d.usageFound
}

func (d *openaiStreamDecoder) Decode(payload []byte) []CanonEvent {
	var chunk struct {
		Choices []struct {
			FinishReason string `json:"finish_reason"`
			Delta        struct {
				Content   string `json:"content"`
				ToolCalls []struct {
					Index    int    `json:"index"`
					ID       string `json:"id"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"delta"`
		} `json:"choices"`
		Usage *openaiUsage `json:"usage"`
	}
	if err := json.Unmarshal(payload, &chunk); err != nil {
		return nil
	}
	if chunk.Usage != nil && (chunk.Usage.PromptTokens > 0 || chunk.Usage.CompletionTokens > 0) {
		d.usage = normalizeOpenAIUsage(chunk.Usage)
		d.usageFound = true
	}
	var events []CanonEvent
	for _, c := range chunk.Choices {
		if c.Delta.Content != "" {
			if d.openToolIdx >= 0 {
				events = append(events, CanonEvent{Type: "block_stop"})
				d.openToolIdx = -1
			}
			d.textOpen = true
			events = append(events, CanonEvent{Type: "text_delta", Text: c.Delta.Content})
		}
		for _, call := range c.Delta.ToolCalls {
			if call.ID != "" || call.Function.Name != "" {
				// 新工具调用开始
				if d.openToolIdx >= 0 || d.textOpen {
					events = append(events, CanonEvent{Type: "block_stop"})
					d.textOpen = false
				}
				d.openToolIdx = call.Index
				events = append(events, CanonEvent{
					Type: "tool_use_start", ToolID: call.ID, ToolName: call.Function.Name,
				})
			}
			if call.Function.Arguments != "" {
				events = append(events, CanonEvent{
					Type: "tool_args_delta", ArgsDelta: call.Function.Arguments,
				})
			}
		}
		if c.FinishReason != "" {
			if d.openToolIdx >= 0 || d.textOpen {
				events = append(events, CanonEvent{Type: "block_stop"})
				d.openToolIdx = -1
				d.textOpen = false
			}
			events = append(events, CanonEvent{
				Type: "finish", StopReason: openaiFinishToCanon(c.FinishReason),
			})
		}
	}
	return events
}

// anthropicStreamDecoder Anthropic 事件流解码。
type anthropicStreamDecoder struct {
	usage      domain.NormalizedUsage
	usageFound bool
	// blockType 记录当前 content block 的类型，用于跳过 thinking/redacted_thinking
	// 等在 canonical 中没有对应表示的块——这些块不产生 block_start，其 content_block_stop
	// 也不应产出 block_stop，否则下游编码器会因 closeBlock 错位而打乱块索引。
	blockType string
}

func newAnthropicStreamDecoder() *anthropicStreamDecoder { return &anthropicStreamDecoder{} }

func (d *anthropicStreamDecoder) Usage() (domain.NormalizedUsage, bool) {
	return d.usage, d.usageFound
}

func (d *anthropicStreamDecoder) Decode(payload []byte) []CanonEvent {
	var ev struct {
		Type    string `json:"type"`
		Message *struct {
			Usage anthropicUsage `json:"usage"`
		} `json:"message"`
		ContentBlock *struct {
			Type string `json:"type"`
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"content_block"`
		Delta *struct {
			Type        string `json:"type"`
			Text        string `json:"text"`
			PartialJSON string `json:"partial_json"`
			StopReason  string `json:"stop_reason"`
		} `json:"delta"`
		Usage *anthropicUsage `json:"usage"`
	}
	if err := json.Unmarshal(payload, &ev); err != nil {
		return nil
	}
	switch ev.Type {
	case "message_start":
		if ev.Message != nil {
			d.usage.BaseInput = ev.Message.Usage.InputTokens
			d.usage.CacheRead = ev.Message.Usage.CacheReadInputTokens
			d.usage.CacheWrite = ev.Message.Usage.CacheCreationInputTokens
			d.usage.Semantic = domain.SemanticAnthropic
			d.usageFound = true
		}
	case "content_block_start":
		if ev.ContentBlock != nil {
			d.blockType = ev.ContentBlock.Type
			if ev.ContentBlock.Type == "tool_use" {
				return []CanonEvent{{
					Type: "tool_use_start", ToolID: ev.ContentBlock.ID, ToolName: ev.ContentBlock.Name,
				}}
			}
		}
	case "content_block_delta":
		// thinking/redacted_thinking 块的增量（thinking_delta、signature_delta）
		// 在 canonical 中没有对应表示，跳过，避免推理内容并入文本或工具累积。
		if ev.Delta != nil && d.blockType != "thinking" && d.blockType != "redacted_thinking" {
			switch ev.Delta.Type {
			case "text_delta":
				return []CanonEvent{{Type: "text_delta", Text: ev.Delta.Text}}
			case "input_json_delta":
				return []CanonEvent{{Type: "tool_args_delta", ArgsDelta: ev.Delta.PartialJSON}}
			}
		}
	case "content_block_stop":
		bt := d.blockType
		d.blockType = ""
		// thinking/redacted_thinking 块未在 canonical 开块（无 text_delta/tool_use_start），
		// 不发 block_stop，以免下游编码器对未打开的块执行 closeBlock 造成索引错位。
		if bt == "thinking" || bt == "redacted_thinking" {
			return nil
		}
		return []CanonEvent{{Type: "block_stop"}}
	case "message_delta":
		if ev.Usage != nil {
			d.usage.Output = ev.Usage.OutputTokens
			d.usageFound = true
		}
		if ev.Delta != nil && ev.Delta.StopReason != "" {
			return []CanonEvent{{Type: "finish", StopReason: ev.Delta.StopReason}}
		}
	}
	return nil
}

// geminiStreamDecoder Gemini streamGenerateContent（alt=sse）解码。
type geminiStreamDecoder struct {
	usage      domain.NormalizedUsage
	usageFound bool
	toolIdx    int
}

func newGeminiStreamDecoder() *geminiStreamDecoder { return &geminiStreamDecoder{} }

func (d *geminiStreamDecoder) Usage() (domain.NormalizedUsage, bool) {
	return d.usage, d.usageFound
}

func (d *geminiStreamDecoder) Decode(payload []byte) []CanonEvent {
	var chunk struct {
		Candidates []struct {
			FinishReason string `json:"finishReason"`
			Content      struct {
				Parts []map[string]any `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
		UsageMetadata *geminiUsage `json:"usageMetadata"`
	}
	if err := json.Unmarshal(payload, &chunk); err != nil {
		return nil
	}
	if chunk.UsageMetadata != nil && chunk.UsageMetadata.PromptTokenCount > 0 {
		d.usage = normalizeGeminiUsage(chunk.UsageMetadata)
		d.usageFound = true
	}
	var events []CanonEvent
	for _, c := range chunk.Candidates {
		for _, p := range c.Content.Parts {
			if txt, ok := p["text"].(string); ok && txt != "" {
				events = append(events, CanonEvent{Type: "text_delta", Text: txt})
			}
			if fc, ok := p["functionCall"].(map[string]any); ok {
				name, _ := fc["name"].(string)
				args, _ := json.Marshal(fc["args"])
				d.toolIdx++
				events = append(events,
					CanonEvent{Type: "tool_use_start",
						ToolID: fmt.Sprintf("call_%d", d.toolIdx), ToolName: name},
					CanonEvent{Type: "tool_args_delta", ArgsDelta: string(args)},
					CanonEvent{Type: "block_stop"},
				)
			}
		}
		if c.FinishReason != "" {
			stop := StopEndTurn
			if c.FinishReason == "MAX_TOKENS" {
				stop = StopMaxTokens
			}
			events = append(events, CanonEvent{Type: "finish", StopReason: stop})
		}
	}
	return events
}

// --- 下游流编码器 ---

// streamEncoder 把规范事件编码为发给客户端的 SSE 帧（完整行，含尾部空行）。
type streamEncoder interface {
	Encode(ev CanonEvent) [][]byte
	// Finish 流结束时的收尾帧（usage 用于终帧填充）。
	Finish(usage domain.NormalizedUsage) [][]byte
}

// anthropicStreamEncoder 重建 Anthropic 事件序列
// （message_start → content_block_* → message_delta → message_stop）。
type anthropicStreamEncoder struct {
	publicModel string
	started     bool
	blockIndex  int
	blockOpen   bool
	blockType   string // text / tool_use
	stopReason  string
}

func newAnthropicStreamEncoder(publicModel string) *anthropicStreamEncoder {
	return &anthropicStreamEncoder{publicModel: publicModel, blockIndex: -1}
}

func sseFrame(event string, payload any) []byte {
	data, _ := json.Marshal(payload)
	return []byte("event: " + event + "\ndata: " + string(data) + "\n\n")
}

func (e *anthropicStreamEncoder) ensureStart() [][]byte {
	if e.started {
		return nil
	}
	e.started = true
	return [][]byte{sseFrame("message_start", map[string]any{
		"type": "message_start",
		"message": map[string]any{
			"id": "msg_relay", "type": "message", "role": "assistant",
			"model": e.publicModel, "content": []any{},
			"stop_reason": nil, "stop_sequence": nil,
			"usage": map[string]any{"input_tokens": 0, "output_tokens": 0},
		},
	})}
}

func (e *anthropicStreamEncoder) closeBlock() [][]byte {
	if !e.blockOpen {
		return nil
	}
	e.blockOpen = false
	return [][]byte{sseFrame("content_block_stop", map[string]any{
		"type": "content_block_stop", "index": e.blockIndex,
	})}
}

func (e *anthropicStreamEncoder) Encode(ev CanonEvent) [][]byte {
	frames := e.ensureStart()
	switch ev.Type {
	case "text_delta":
		if !e.blockOpen || e.blockType != "text" {
			frames = append(frames, e.closeBlock()...)
			e.blockIndex++
			e.blockOpen, e.blockType = true, "text"
			frames = append(frames, sseFrame("content_block_start", map[string]any{
				"type": "content_block_start", "index": e.blockIndex,
				"content_block": map[string]any{"type": "text", "text": ""},
			}))
		}
		frames = append(frames, sseFrame("content_block_delta", map[string]any{
			"type": "content_block_delta", "index": e.blockIndex,
			"delta": map[string]any{"type": "text_delta", "text": ev.Text},
		}))
	case "tool_use_start":
		frames = append(frames, e.closeBlock()...)
		e.blockIndex++
		e.blockOpen, e.blockType = true, "tool_use"
		frames = append(frames, sseFrame("content_block_start", map[string]any{
			"type": "content_block_start", "index": e.blockIndex,
			"content_block": map[string]any{
				"type": "tool_use", "id": ev.ToolID, "name": ev.ToolName, "input": map[string]any{},
			},
		}))
	case "tool_args_delta":
		frames = append(frames, sseFrame("content_block_delta", map[string]any{
			"type": "content_block_delta", "index": e.blockIndex,
			"delta": map[string]any{"type": "input_json_delta", "partial_json": ev.ArgsDelta},
		}))
	case "block_stop":
		frames = append(frames, e.closeBlock()...)
	case "finish":
		e.stopReason = ev.StopReason
	}
	return frames
}

func (e *anthropicStreamEncoder) Finish(usage domain.NormalizedUsage) [][]byte {
	frames := e.ensureStart()
	frames = append(frames, e.closeBlock()...)
	stop := e.stopReason
	if stop == "" {
		stop = StopEndTurn
	}
	frames = append(frames,
		sseFrame("message_delta", map[string]any{
			"type":  "message_delta",
			"delta": map[string]any{"stop_reason": stop, "stop_sequence": nil},
			"usage": map[string]any{
				"input_tokens": usage.BaseInput, "output_tokens": usage.Output,
				"cache_read_input_tokens":     usage.CacheRead,
				"cache_creation_input_tokens": usage.CacheWrite,
			},
		}),
		sseFrame("message_stop", map[string]any{"type": "message_stop"}),
	)
	return frames
}

// openaiStreamEncoder 把规范事件编码为 OpenAI chunk 流。
type openaiStreamEncoder struct {
	publicModel string
	stopReason  string
	toolIndex   int
	inTool      bool
}

func newOpenAIStreamEncoder(publicModel string) *openaiStreamEncoder {
	return &openaiStreamEncoder{publicModel: publicModel, toolIndex: -1}
}

func (e *openaiStreamEncoder) chunk(delta map[string]any, finish any) []byte {
	data, _ := json.Marshal(map[string]any{
		"id": "chatcmpl-relay", "object": "chat.completion.chunk", "model": e.publicModel,
		"choices": []map[string]any{{"index": 0, "delta": delta, "finish_reason": finish}},
	})
	return []byte("data: " + string(data) + "\n\n")
}

func (e *openaiStreamEncoder) Encode(ev CanonEvent) [][]byte {
	switch ev.Type {
	case "text_delta":
		return [][]byte{e.chunk(map[string]any{"content": ev.Text}, nil)}
	case "tool_use_start":
		e.toolIndex++
		e.inTool = true
		return [][]byte{e.chunk(map[string]any{
			"tool_calls": []map[string]any{{
				"index": e.toolIndex, "id": ev.ToolID, "type": "function",
				"function": map[string]any{"name": ev.ToolName, "arguments": ""},
			}},
		}, nil)}
	case "tool_args_delta":
		return [][]byte{e.chunk(map[string]any{
			"tool_calls": []map[string]any{{
				"index":    e.toolIndex,
				"function": map[string]any{"arguments": ev.ArgsDelta},
			}},
		}, nil)}
	case "finish":
		e.stopReason = ev.StopReason
	}
	return nil
}

func (e *openaiStreamEncoder) Finish(usage domain.NormalizedUsage) [][]byte {
	stop := canonStopToOpenAI(e.stopReason)
	if e.stopReason == "" {
		stop = "stop"
	}
	final := e.chunk(map[string]any{}, stop)
	usageChunk, _ := json.Marshal(map[string]any{
		"id": "chatcmpl-relay", "object": "chat.completion.chunk", "model": e.publicModel,
		"choices": []any{},
		"usage": map[string]any{
			"prompt_tokens":     usage.BaseInput + usage.CacheRead,
			"completion_tokens": usage.Output,
			"total_tokens":      usage.BaseInput + usage.CacheRead + usage.Output,
		},
	})
	return [][]byte{final,
		[]byte("data: " + string(usageChunk) + "\n\n"),
		[]byte("data: [DONE]\n\n")}
}
