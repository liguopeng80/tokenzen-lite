package relay

// OpenAI 协议编解码：请求/响应与 canonical 互转。

import (
	"encoding/json"
	"fmt"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
)

// decodeOpenAIRequest OpenAI chat 请求 → canonical。
func decodeOpenAIRequest(body map[string]any) (*CanonRequest, error) {
	req := &CanonRequest{}
	req.Model, _ = body["model"].(string)
	if req.Model == "" {
		return nil, fmt.Errorf("缺少 model 字段")
	}
	if mt, ok := body["max_tokens"].(float64); ok {
		req.MaxTokens = int64(mt)
	} else if mt, ok := body["max_completion_tokens"].(float64); ok {
		req.MaxTokens = int64(mt)
	}
	if t, ok := body["temperature"].(float64); ok {
		req.Temperature = &t
	}
	if p, ok := body["top_p"].(float64); ok {
		req.TopP = &p
	}
	// top_k：OpenAI chat 端点不支持，decode 仅记录（跨协议路由到 OpenAI 时由 EncodeBody 拒绝）。
	if k, ok := body["top_k"].(float64); ok {
		ki := int64(k)
		req.TopK = &ki
	}
	if seed, ok := body["seed"].(float64); ok {
		s := int64(seed)
		req.Seed = &s
	}
	if lp, ok := body["logprobs"].(bool); ok && lp {
		req.Logprobs = &LogprobsConfig{Enabled: true}
		if n, ok := body["top_logprobs"].(float64); ok {
			req.Logprobs.TopN = int(n)
		}
	}
	if rf, ok := body["response_format"].(map[string]any); ok {
		rft, _ := rf["type"].(string)
		rfCfg := &ResponseFormat{Type: rft}
		if js, ok := rf["json_schema"].(map[string]any); ok {
			if schema, ok := js["schema"].(map[string]any); ok {
				rfCfg.JSONSchema = schema
			}
		}
		req.ResponseFormat = rfCfg
	}
	// reasoning_effort → Thinking（有损映射：low/medium/high 不携带精确 budget）。
	if re, ok := body["reasoning_effort"].(string); ok && re != "" {
		req.Thinking = &ThinkingConfig{Enabled: true, BudgetTokens: reasoningEffortToBudget(re)}
	}
	// user 字符串 → Metadata["user_id"]（OpenAI 平铺表达，往返保形）。
	if u, ok := body["user"].(string); ok && u != "" {
		req.Metadata = map[string]string{"user_id": u}
	}
	req.Stream, _ = body["stream"].(bool)
	switch stop := body["stop"].(type) {
	case string:
		req.Stop = []string{stop}
	case []any:
		for _, s := range stop {
			if str, ok := s.(string); ok {
				req.Stop = append(req.Stop, str)
			}
		}
	}
	if tools, ok := body["tools"].([]any); ok {
		for _, tl := range tools {
			tm, _ := tl.(map[string]any)
			fn, _ := tm["function"].(map[string]any)
			if fn == nil {
				continue
			}
			tool := CanonTool{}
			tool.Name, _ = fn["name"].(string)
			tool.Description, _ = fn["description"].(string)
			tool.Schema, _ = fn["parameters"].(map[string]any)
			req.Tools = append(req.Tools, tool)
		}
	}
	switch tc := body["tool_choice"].(type) {
	case string:
		// OpenAI 字符串取值：auto / required / none
		req.ToolChoice = &ToolChoice{Mode: ToolChoiceMode(tc)}
	case map[string]any:
		if fn, ok := tc["function"].(map[string]any); ok {
			name, _ := fn["name"].(string)
			req.ToolChoice = &ToolChoice{Mode: ToolChoiceTool, Name: name}
		}
	}
	msgs, _ := body["messages"].([]any)
	for _, m := range msgs {
		mm, _ := m.(map[string]any)
		if mm == nil {
			continue
		}
		role, _ := mm["role"].(string)
		if role == "system" || role == "developer" {
			if txt, ok := mm["content"].(string); ok {
				if req.System != "" {
					req.System += "\n"
				}
				req.System += txt
			}
			continue
		}
		msg := CanonMessage{Role: role}
		if role == "tool" {
			id, _ := mm["tool_call_id"].(string)
			content, _ := mm["content"].(string)
			msg.Parts = append(msg.Parts, CanonPart{
				Type: "tool_result", ToolResultID: id, ToolResult: content,
			})
			req.Messages = append(req.Messages, msg)
			continue
		}
		switch content := mm["content"].(type) {
		case string:
			if content != "" {
				msg.Parts = append(msg.Parts, CanonPart{Type: "text", Text: content})
			}
		case []any:
			for _, part := range content {
				pm, _ := part.(map[string]any)
				if pm == nil {
					continue
				}
				switch pm["type"] {
				case "text":
					txt, _ := pm["text"].(string)
					msg.Parts = append(msg.Parts, CanonPart{Type: "text", Text: txt})
				case "image_url":
					iu, _ := pm["image_url"].(map[string]any)
					url, _ := iu["url"].(string)
					msg.Parts = append(msg.Parts, CanonPart{Type: "image", ImageURL: url})
				}
			}
		}
		if calls, ok := mm["tool_calls"].([]any); ok {
			for _, call := range calls {
				cm, _ := call.(map[string]any)
				fn, _ := cm["function"].(map[string]any)
				if fn == nil {
					continue
				}
				id, _ := cm["id"].(string)
				name, _ := fn["name"].(string)
				args, _ := fn["arguments"].(string)
				msg.Parts = append(msg.Parts, CanonPart{
					Type: "tool_use", ToolID: id, ToolName: name, ToolArgs: args,
				})
			}
		}
		req.Messages = append(req.Messages, msg)
	}
	return req, nil
}

// encodeOpenAIRequest canonical → OpenAI 请求体。
func encodeOpenAIRequest(req *CanonRequest, upstreamModel string) map[string]any {
	body := map[string]any{"model": upstreamModel}
	if req.MaxTokens > 0 {
		body["max_tokens"] = req.MaxTokens
	}
	if req.Temperature != nil {
		body["temperature"] = *req.Temperature
	}
	if req.TopP != nil {
		body["top_p"] = *req.TopP
	}
	if len(req.Stop) > 0 {
		body["stop"] = req.Stop
	}
	if req.Seed != nil {
		body["seed"] = *req.Seed
	}
	if req.Logprobs != nil {
		body["logprobs"] = req.Logprobs.Enabled
		if req.Logprobs.TopN > 0 {
			body["top_logprobs"] = req.Logprobs.TopN
		}
	}
	if req.ResponseFormat != nil {
		rf := map[string]any{"type": req.ResponseFormat.Type}
		if req.ResponseFormat.JSONSchema != nil {
			rf["json_schema"] = map[string]any{"schema": req.ResponseFormat.JSONSchema}
		}
		body["response_format"] = rf
	}
	if req.Thinking != nil && req.Thinking.Enabled {
		// BudgetTokens → reasoning_effort 有损映射（阈值见 budgetToReasoningEffort）。
		body["reasoning_effort"] = budgetToReasoningEffort(req.Thinking.BudgetTokens)
	}
	if uid, ok := req.Metadata["user_id"]; ok {
		body["user"] = uid
	}
	if req.Stream {
		body["stream"] = true
		body["stream_options"] = map[string]any{"include_usage": true}
	}
	if len(req.Tools) > 0 {
		tools := make([]map[string]any, 0, len(req.Tools))
		for _, t := range req.Tools {
			tools = append(tools, map[string]any{
				"type": "function",
				"function": map[string]any{
					"name": t.Name, "description": t.Description, "parameters": t.Schema,
				},
			})
		}
		body["tools"] = tools
	}
	if tc := req.ToolChoice; tc != nil {
		switch tc.Mode {
		case ToolChoiceAuto, ToolChoiceRequired, ToolChoiceNone:
			body["tool_choice"] = string(tc.Mode)
		case ToolChoiceTool:
			body["tool_choice"] = map[string]any{
				"type": "function", "function": map[string]any{"name": tc.Name},
			}
		}
	}

	var msgs []map[string]any
	if req.System != "" {
		msgs = append(msgs, map[string]any{"role": "system", "content": req.System})
	}
	for _, m := range req.Messages {
		// tool_result 需拆成独立的 tool 角色消息
		var toolResults []CanonPart
		var textParts []CanonPart
		var toolUses []CanonPart
		for _, p := range m.Parts {
			switch p.Type {
			case "tool_result":
				toolResults = append(toolResults, p)
			case "tool_use":
				toolUses = append(toolUses, p)
			default:
				textParts = append(textParts, p)
			}
		}
		for _, tr := range toolResults {
			msgs = append(msgs, map[string]any{
				"role": "tool", "tool_call_id": tr.ToolResultID, "content": tr.ToolResult,
			})
		}
		if len(textParts) == 0 && len(toolUses) == 0 {
			continue
		}
		msg := map[string]any{"role": m.Role}
		if hasImage(textParts) {
			parts := make([]map[string]any, 0, len(textParts))
			for _, p := range textParts {
				switch p.Type {
				case "text":
					parts = append(parts, map[string]any{"type": "text", "text": p.Text})
				case "image":
					parts = append(parts, map[string]any{
						"type": "image_url", "image_url": map[string]any{"url": p.ImageURL},
					})
				}
			}
			msg["content"] = parts
		} else {
			content := ""
			for _, p := range textParts {
				content += p.Text
			}
			msg["content"] = content
		}
		if len(toolUses) > 0 {
			calls := make([]map[string]any, 0, len(toolUses))
			for _, tu := range toolUses {
				calls = append(calls, map[string]any{
					"id": tu.ToolID, "type": "function",
					"function": map[string]any{"name": tu.ToolName, "arguments": tu.ToolArgs},
				})
			}
			msg["tool_calls"] = calls
		}
		msgs = append(msgs, msg)
	}
	body["messages"] = msgs
	return body
}

func hasImage(parts []CanonPart) bool {
	for _, p := range parts {
		if p.Type == "image" {
			return true
		}
	}
	return false
}

// budgetToReasoningEffort Anthropic thinking budget_tokens → OpenAI reasoning_effort 有损映射。
// 阈值依据 Claude 扩展思考的典型 budget 区间：低端 < 8k、中端 8k~24k、高端 >= 24k。
// 仅用于 OpenAI 上游编码；反向（OpenAI reasoning_effort → Thinking.BudgetTokens）见
// reasoningEffortToBudget，两者非完全可逆，跨协议往返有损。
func budgetToReasoningEffort(budget int64) string {
	switch {
	case budget < 8000:
		return "low"
	case budget < 24000:
		return "medium"
	default:
		return "high"
	}
}

// reasoningEffortToBudget OpenAI reasoning_effort → 近似 budget_tokens（用于 canonical 表示）。
// 取各档的中位近似值，跨协议往返后 reasoning_effort 不变。
func reasoningEffortToBudget(effort string) int64 {
	switch effort {
	case "low":
		return 4000
	case "medium":
		return 16000
	case "high":
		return 32000
	default:
		return 16000
	}
}

// rejectOpenAIUnsupported 在编码前检查 OpenAI chat 端点无法表达的字段。
// 命中返回 ErrUnsupportedFeature，由 canonicalConduit.BuildRequest 透传，
// relayWithRetry 归为 400 不可重试（错误码 unsupported_feature）。
func rejectOpenAIUnsupported(canon *CanonRequest) error {
	if canon.TopK != nil {
		return unsupportedFeature("top_k", "OpenAI chat 端点不支持该采样参数")
	}
	if canon.ToolChoice != nil && canon.ToolChoice.DisableParallel {
		return unsupportedFeature("tool_choice.disable_parallel_tool_use",
			"OpenAI 协议不支持禁用并行工具调用")
	}
	return nil
}

// decodeOpenAIResponse OpenAI 非流式响应 → canonical + usage。
func decodeOpenAIResponse(raw []byte) (*CanonResponse, domain.NormalizedUsage, error) {
	var resp struct {
		ID      string `json:"id"`
		Model   string `json:"model"`
		Choices []struct {
			FinishReason string `json:"finish_reason"`
			Message      struct {
				Content   string `json:"content"`
				ToolCalls []struct {
					ID       string `json:"id"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
		Usage openaiUsage `json:"usage"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, domain.NormalizedUsage{}, fmt.Errorf("OpenAI 响应解析失败: %w", err)
	}
	out := &CanonResponse{ID: resp.ID, Model: resp.Model}
	if len(resp.Choices) > 0 {
		c := resp.Choices[0]
		out.StopReason = openaiFinishToCanon(c.FinishReason)
		if c.Message.Content != "" {
			out.Parts = append(out.Parts, CanonPart{Type: "text", Text: c.Message.Content})
		}
		for _, call := range c.Message.ToolCalls {
			out.Parts = append(out.Parts, CanonPart{
				Type: "tool_use", ToolID: call.ID,
				ToolName: call.Function.Name, ToolArgs: call.Function.Arguments,
			})
		}
	}
	return out, normalizeOpenAIUsage(&resp.Usage), nil
}

// encodeOpenAIResponse canonical → OpenAI 非流式响应体。
func encodeOpenAIResponse(resp *CanonResponse, publicModel string,
	usage domain.NormalizedUsage) []byte {

	message := map[string]any{"role": "assistant", "content": nil}
	text := ""
	var calls []map[string]any
	for _, p := range resp.Parts {
		switch p.Type {
		case "text":
			text += p.Text
		case "thinking":
			// OpenAI 非流式响应无 thinking 块表示；跨协议路径下丢弃（降级，不拒绝）。
		case "tool_use":
			calls = append(calls, map[string]any{
				"id": p.ToolID, "type": "function",
				"function": map[string]any{"name": p.ToolName, "arguments": p.ToolArgs},
			})
		}
	}
	if text != "" {
		message["content"] = text
	}
	if len(calls) > 0 {
		message["tool_calls"] = calls
	}
	id := resp.ID
	if id == "" {
		id = "chatcmpl-relay"
	}
	out, _ := json.Marshal(map[string]any{
		"id": id, "object": "chat.completion", "model": publicModel,
		"choices": []map[string]any{{
			"index": 0, "message": message,
			"finish_reason": canonStopToOpenAI(resp.StopReason),
		}},
		"usage": map[string]any{
			"prompt_tokens":     usage.BaseInput + usage.CacheRead,
			"completion_tokens": usage.Output,
			"total_tokens":      usage.BaseInput + usage.CacheRead + usage.Output,
			"prompt_tokens_details": map[string]any{
				"cached_tokens": usage.CacheRead,
			},
		},
	})
	return out
}
