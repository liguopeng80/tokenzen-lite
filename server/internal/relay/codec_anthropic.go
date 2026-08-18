package relay

// Anthropic 协议编解码：请求/响应与 canonical 互转，及 anthropic usage 归一化。

import (
	"encoding/json"
	"fmt"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
)

// anthropicUsage Anthropic 语义 usage：input_tokens 不含缓存。
type anthropicUsage struct {
	InputTokens              int64 `json:"input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
}

func normalizeAnthropicUsage(u *anthropicUsage) domain.NormalizedUsage {
	return domain.NormalizedUsage{
		BaseInput:  u.InputTokens,
		CacheRead:  u.CacheReadInputTokens,
		CacheWrite: u.CacheCreationInputTokens,
		Output:     u.OutputTokens,
		Semantic:   domain.SemanticAnthropic,
	}
}

// decodeAnthropicRequest Anthropic /v1/messages 请求 → canonical。
func decodeAnthropicRequest(body map[string]any) (*CanonRequest, error) {
	req := &CanonRequest{}
	req.Model, _ = body["model"].(string)
	if mt, ok := body["max_tokens"].(float64); ok {
		req.MaxTokens = int64(mt)
	}
	if t, ok := body["temperature"].(float64); ok {
		req.Temperature = &t
	}
	if p, ok := body["top_p"].(float64); ok {
		req.TopP = &p
	}
	req.Stream, _ = body["stream"].(bool)
	if stops, ok := body["stop_sequences"].([]any); ok {
		for _, s := range stops {
			if str, ok := s.(string); ok {
				req.Stop = append(req.Stop, str)
			}
		}
	}
	// system: 字符串或 content block 数组
	switch sys := body["system"].(type) {
	case string:
		req.System = sys
	case []any:
		for _, b := range sys {
			if bm, ok := b.(map[string]any); ok {
				if txt, ok := bm["text"].(string); ok {
					if req.System != "" {
						req.System += "\n"
					}
					req.System += txt
				}
			}
		}
	}
	if k, ok := body["top_k"].(float64); ok {
		ki := int64(k)
		req.TopK = &ki
	}
	// thinking 配置：{type: "enabled", budget_tokens: N}
	if think, ok := body["thinking"].(map[string]any); ok {
		if t, _ := think["type"].(string); t == "enabled" {
			cfg := &ThinkingConfig{Enabled: true}
			if bt, ok := think["budget_tokens"].(float64); ok {
				cfg.BudgetTokens = int64(bt)
			}
			req.Thinking = cfg
		}
	}
	// metadata.user_id → Metadata
	if md, ok := body["metadata"].(map[string]any); ok {
		if uid, ok := md["user_id"].(string); ok {
			req.Metadata = map[string]string{"user_id": uid}
		}
	}
	// tools
	if tools, ok := body["tools"].([]any); ok {
		for _, tl := range tools {
			tm, ok := tl.(map[string]any)
			if !ok {
				continue
			}
			tool := CanonTool{}
			tool.Name, _ = tm["name"].(string)
			tool.Description, _ = tm["description"].(string)
			tool.Schema, _ = tm["input_schema"].(map[string]any)
			req.Tools = append(req.Tools, tool)
		}
	}
	if tc, ok := body["tool_choice"].(map[string]any); ok {
		choice := &ToolChoice{}
		switch tc["type"] {
		case "auto":
			choice.Mode = ToolChoiceAuto
		case "any":
			choice.Mode = ToolChoiceRequired
		case "none":
			choice.Mode = ToolChoiceNone
		case "tool":
			choice.Mode = ToolChoiceTool
			choice.Name, _ = tc["name"].(string)
		default:
			choice = nil // 未知 type，忽略
		}
		if choice != nil {
			if dp, ok := tc["disable_parallel_tool_use"].(bool); ok {
				choice.DisableParallel = dp
			}
			req.ToolChoice = choice
		}
	}
	// messages
	msgs, _ := body["messages"].([]any)
	for _, m := range msgs {
		mm, ok := m.(map[string]any)
		if !ok {
			continue
		}
		msg := CanonMessage{}
		msg.Role, _ = mm["role"].(string)
		switch content := mm["content"].(type) {
		case string:
			msg.Parts = append(msg.Parts, CanonPart{Type: "text", Text: content})
		case []any:
			for _, blk := range content {
				bm, ok := blk.(map[string]any)
				if !ok {
					continue
				}
				switch bm["type"] {
				case "text":
					txt, _ := bm["text"].(string)
					msg.Parts = append(msg.Parts, CanonPart{Type: "text", Text: txt})
				case "image":
					src, _ := bm["source"].(map[string]any)
					mime, _ := src["media_type"].(string)
					data, _ := src["data"].(string)
					msg.Parts = append(msg.Parts, CanonPart{
						Type: "image", ImageMIME: mime,
						ImageURL: "data:" + mime + ";base64," + data,
					})
				case "tool_use":
					id, _ := bm["id"].(string)
					name, _ := bm["name"].(string)
					args, _ := json.Marshal(bm["input"])
					msg.Parts = append(msg.Parts, CanonPart{
						Type: "tool_use", ToolID: id, ToolName: name, ToolArgs: string(args),
					})
				case "tool_result":
					id, _ := bm["tool_use_id"].(string)
					isErr, _ := bm["is_error"].(bool)
					msg.Parts = append(msg.Parts, CanonPart{
						Type: "tool_result", ToolResultID: id,
						ToolResult: flattenToolResultContent(bm["content"]), ToolIsError: isErr,
					})
				}
			}
		}
		req.Messages = append(req.Messages, msg)
	}
	if req.Model == "" {
		return nil, fmt.Errorf("缺少 model 字段")
	}
	return req, nil
}

// flattenToolResultContent tool_result 的 content 可为字符串或 text 块数组。
func flattenToolResultContent(v any) string {
	switch c := v.(type) {
	case string:
		return c
	case []any:
		out := ""
		for _, blk := range c {
			if bm, ok := blk.(map[string]any); ok {
				if txt, ok := bm["text"].(string); ok {
					out += txt
				}
			}
		}
		return out
	}
	return ""
}

// encodeAnthropicRequest canonical → Anthropic 请求体。
func encodeAnthropicRequest(req *CanonRequest, upstreamModel string) map[string]any {
	body := map[string]any{
		"model":      upstreamModel,
		"max_tokens": req.MaxTokens,
	}
	if body["max_tokens"] == int64(0) {
		body["max_tokens"] = int64(4096) // Anthropic 必填
	}
	if req.System != "" {
		body["system"] = req.System
	}
	if req.Temperature != nil {
		body["temperature"] = *req.Temperature
	}
	if req.TopP != nil {
		body["top_p"] = *req.TopP
	}
	if req.TopK != nil {
		body["top_k"] = *req.TopK
	}
	if len(req.Stop) > 0 {
		body["stop_sequences"] = req.Stop
	}
	if req.Thinking != nil && req.Thinking.Enabled {
		think := map[string]any{"type": "enabled"}
		if req.Thinking.BudgetTokens > 0 {
			think["budget_tokens"] = req.Thinking.BudgetTokens
		}
		body["thinking"] = think
	}
	if uid, ok := req.Metadata["user_id"]; ok {
		body["metadata"] = map[string]any{"user_id": uid}
	}
	if req.Stream {
		body["stream"] = true
	}
	if len(req.Tools) > 0 {
		tools := make([]map[string]any, 0, len(req.Tools))
		for _, t := range req.Tools {
			tools = append(tools, map[string]any{
				"name": t.Name, "description": t.Description, "input_schema": t.Schema,
			})
		}
		body["tools"] = tools
	}
	if tc := req.ToolChoice; tc != nil {
		var choice map[string]any
		switch tc.Mode {
		case ToolChoiceAuto:
			choice = map[string]any{"type": "auto"}
		case ToolChoiceRequired:
			choice = map[string]any{"type": "any"}
		case ToolChoiceNone:
			choice = map[string]any{"type": "none"}
		case ToolChoiceTool:
			choice = map[string]any{"type": "tool", "name": tc.Name}
		}
		if choice != nil {
			if tc.DisableParallel {
				choice["disable_parallel_tool_use"] = true
			}
			body["tool_choice"] = choice
		}
	}

	msgs := make([]map[string]any, 0, len(req.Messages))
	for _, m := range req.Messages {
		if m.Role == "system" {
			continue // 已并入 system 字段
		}
		role := m.Role
		if role == "tool" {
			role = "user"
		}
		blocks := make([]map[string]any, 0, len(m.Parts))
		for _, p := range m.Parts {
			switch p.Type {
			case "text":
				blocks = append(blocks, map[string]any{"type": "text", "text": p.Text})
			case "image":
				mime, data := splitDataURI(p.ImageURL, p.ImageMIME)
				blocks = append(blocks, map[string]any{
					"type": "image",
					"source": map[string]any{
						"type": "base64", "media_type": mime, "data": data,
					},
				})
			case "tool_use":
				var input any = map[string]any{}
				_ = json.Unmarshal([]byte(p.ToolArgs), &input)
				blocks = append(blocks, map[string]any{
					"type": "tool_use", "id": p.ToolID, "name": p.ToolName, "input": input,
				})
			case "tool_result":
				blocks = append(blocks, map[string]any{
					"type": "tool_result", "tool_use_id": p.ToolResultID,
					"content": p.ToolResult, "is_error": p.ToolIsError,
				})
			}
		}
		msgs = append(msgs, map[string]any{"role": role, "content": blocks})
	}
	body["messages"] = msgs
	return body
}

// decodeAnthropicResponse Anthropic 非流式响应 → canonical + usage。
func decodeAnthropicResponse(raw []byte) (*CanonResponse, domain.NormalizedUsage, error) {
	var resp struct {
		ID         string           `json:"id"`
		Model      string           `json:"model"`
		StopReason string           `json:"stop_reason"`
		Content    []map[string]any `json:"content"`
		Usage      anthropicUsage   `json:"usage"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, domain.NormalizedUsage{}, fmt.Errorf("Anthropic 响应解析失败: %w", err)
	}
	out := &CanonResponse{ID: resp.ID, Model: resp.Model, StopReason: resp.StopReason}
	for _, blk := range resp.Content {
		switch blk["type"] {
		case "text":
			txt, _ := blk["text"].(string)
			out.Parts = append(out.Parts, CanonPart{Type: "text", Text: txt})
		case "thinking":
			// 扩展思考内容：canonical 表示为 thinking 块，跨协议下游自行决定呈现或丢弃。
			txt, _ := blk["thinking"].(string)
			out.Parts = append(out.Parts, CanonPart{Type: "thinking", Text: txt})
		case "tool_use":
			id, _ := blk["id"].(string)
			name, _ := blk["name"].(string)
			args, _ := json.Marshal(blk["input"])
			out.Parts = append(out.Parts, CanonPart{
				Type: "tool_use", ToolID: id, ToolName: name, ToolArgs: string(args),
			})
		}
	}
	return out, normalizeAnthropicUsage(&resp.Usage), nil
}

// encodeAnthropicResponse canonical → Anthropic 非流式响应体。
func encodeAnthropicResponse(resp *CanonResponse, publicModel string,
	usage domain.NormalizedUsage) []byte {

	content := make([]map[string]any, 0, len(resp.Parts))
	for _, p := range resp.Parts {
		switch p.Type {
		case "text":
			content = append(content, map[string]any{"type": "text", "text": p.Text})
		case "thinking":
			blk := map[string]any{"type": "thinking", "thinking": p.Text}
			if p.Text != "" {
				blk["signature"] = "relay_redacted"
			}
			content = append(content, blk)
		case "tool_use":
			var input any = map[string]any{}
			_ = json.Unmarshal([]byte(p.ToolArgs), &input)
			content = append(content, map[string]any{
				"type": "tool_use", "id": p.ToolID, "name": p.ToolName, "input": input,
			})
		}
	}
	id := resp.ID
	if id == "" {
		id = "msg_relay"
	}
	stop := resp.StopReason
	if stop == "" {
		stop = StopEndTurn
	}
	out, _ := json.Marshal(map[string]any{
		"id": id, "type": "message", "role": "assistant",
		"model": publicModel, "content": content,
		"stop_reason": stop, "stop_sequence": nil,
		"usage": map[string]any{
			"input_tokens":                usage.BaseInput,
			"output_tokens":               usage.Output,
			"cache_read_input_tokens":     usage.CacheRead,
			"cache_creation_input_tokens": usage.CacheWrite,
		},
	})
	return out
}

// splitDataURI 从 data URI 提取 MIME 与 base64 数据。
func splitDataURI(uri, fallbackMIME string) (mime, data string) {
	mime = fallbackMIME
	if mime == "" {
		mime = "image/png"
	}
	const prefix = "data:"
	if len(uri) > len(prefix) && uri[:len(prefix)] == prefix {
		rest := uri[len(prefix):]
		for i := 0; i < len(rest); i++ {
			if rest[i] == ';' || rest[i] == ',' {
				mime = rest[:i]
				if j := indexByte(rest, ','); j >= 0 {
					data = rest[j+1:]
				}
				return mime, data
			}
		}
	}
	return mime, ""
}

func indexByte(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}

// rejectAnthropicUnsupported 在编码前检查 Anthropic 协议无法表达的字段。
// Anthropic 原生支持 thinking/top_k/metadata，但不支持 logprobs/seed/response_format。
func rejectAnthropicUnsupported(canon *CanonRequest) error {
	if canon.Logprobs != nil {
		return unsupportedFeature("logprobs", "Anthropic 协议不支持对数概率")
	}
	if canon.Seed != nil {
		return unsupportedFeature("seed", "Anthropic 协议不支持确定性种子")
	}
	if canon.ResponseFormat != nil {
		return unsupportedFeature("response_format", "Anthropic 协议无原生结构化输出字段")
	}
	return nil
}
