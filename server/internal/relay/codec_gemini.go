package relay

// Gemini 协议编解码：canonical → generateContent 请求；响应 → canonical。
// Gemini 仅作为上游协议（下游不提供 Gemini 格式 API）。

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
)

// geminiUsage Gemini 语义 usage：promptTokenCount 含缓存。
type geminiUsage struct {
	PromptTokenCount        int64 `json:"promptTokenCount"`
	CandidatesTokenCount    int64 `json:"candidatesTokenCount"`
	CachedContentTokenCount int64 `json:"cachedContentTokenCount"`
}

func normalizeGeminiUsage(u *geminiUsage) domain.NormalizedUsage {
	base := u.PromptTokenCount - u.CachedContentTokenCount
	if base < 0 {
		base = 0
	}
	return domain.NormalizedUsage{
		BaseInput: base,
		CacheRead: u.CachedContentTokenCount,
		Output:    u.CandidatesTokenCount,
		Semantic:  domain.SemanticGemini,
	}
}

// encodeGeminiRequest canonical → Gemini generateContent 请求体。
func encodeGeminiRequest(req *CanonRequest) map[string]any {
	body := map[string]any{}
	if req.System != "" {
		body["systemInstruction"] = map[string]any{
			"parts": []map[string]any{{"text": req.System}},
		}
	}
	genCfg := map[string]any{}
	if req.MaxTokens > 0 {
		genCfg["maxOutputTokens"] = req.MaxTokens
	}
	if req.Temperature != nil {
		genCfg["temperature"] = *req.Temperature
	}
	if req.TopP != nil {
		genCfg["topP"] = *req.TopP
	}
	if req.TopK != nil {
		genCfg["topK"] = *req.TopK
	}
	if len(req.Stop) > 0 {
		genCfg["stopSequences"] = req.Stop
	}
	if req.Thinking != nil && req.Thinking.Enabled {
		// Gemini thinkingConfig.thinkingBudget 直接承载预算，无损映射。
		thinkCfg := map[string]any{"includeThoughts": true}
		if req.Thinking.BudgetTokens > 0 {
			thinkCfg["thinkingBudget"] = req.Thinking.BudgetTokens
		}
		genCfg["thinkingConfig"] = thinkCfg
	}
	if req.ResponseFormat != nil {
		// response_format → Gemini responseMimeType + responseSchema（json_schema 形态）。
		mime := req.ResponseFormat.Type
		if mime == "json_schema" && req.ResponseFormat.JSONSchema != nil {
			genCfg["responseMimeType"] = "application/json"
			genCfg["responseSchema"] = req.ResponseFormat.JSONSchema
		} else if mime == "json_object" {
			genCfg["responseMimeType"] = "application/json"
		}
	}
	if len(genCfg) > 0 {
		body["generationConfig"] = genCfg
	}
	// metadata：Gemini 无原生 user_id 字段，降级丢弃（不拒绝，影响仅限亲和路由键收窄）。
	if len(req.Metadata) > 0 {
		// 预留位：未来若 Gemini 引入用户级标识字段，可在此映射；当前丢弃。
	}
	if len(req.Tools) > 0 {
		decls := make([]map[string]any, 0, len(req.Tools))
		for _, t := range req.Tools {
			decls = append(decls, map[string]any{
				"name": t.Name, "description": t.Description, "parameters": t.Schema,
			})
		}
		body["tools"] = []map[string]any{{"functionDeclarations": decls}}
	}

	var contents []map[string]any
	for _, m := range req.Messages {
		role := "user"
		if m.Role == "assistant" {
			role = "model"
		}
		var parts []map[string]any
		for _, p := range m.Parts {
			switch p.Type {
			case "text":
				parts = append(parts, map[string]any{"text": p.Text})
			case "image":
				mime, data := splitDataURI(p.ImageURL, p.ImageMIME)
				parts = append(parts, map[string]any{
					"inlineData": map[string]any{"mimeType": mime, "data": data},
				})
			case "tool_use":
				var args any = map[string]any{}
				_ = json.Unmarshal([]byte(p.ToolArgs), &args)
				parts = append(parts, map[string]any{
					"functionCall": map[string]any{"name": p.ToolName, "args": args},
				})
			case "tool_result":
				parts = append(parts, map[string]any{
					"functionResponse": map[string]any{
						"name":     p.ToolName,
						"response": map[string]any{"result": p.ToolResult},
					},
				})
			}
		}
		if len(parts) == 0 {
			continue
		}
		contents = append(contents, map[string]any{"role": role, "parts": parts})
	}
	body["contents"] = contents
	return body
}

// decodeGeminiResponse Gemini 非流式响应 → canonical + usage。
func decodeGeminiResponse(raw []byte) (*CanonResponse, domain.NormalizedUsage, error) {
	var resp struct {
		Candidates []struct {
			FinishReason string `json:"finishReason"`
			Content      struct {
				Parts []map[string]any `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
		UsageMetadata geminiUsage `json:"usageMetadata"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, domain.NormalizedUsage{}, fmt.Errorf("Gemini 响应解析失败: %w", err)
	}
	out := &CanonResponse{ID: "gemini_relay"}
	if len(resp.Candidates) > 0 {
		c := resp.Candidates[0]
		switch strings.ToUpper(c.FinishReason) {
		case "MAX_TOKENS":
			out.StopReason = StopMaxTokens
		default:
			out.StopReason = StopEndTurn
		}
		toolIdx := 0
		for _, p := range c.Content.Parts {
			if txt, ok := p["text"].(string); ok {
				out.Parts = append(out.Parts, CanonPart{Type: "text", Text: txt})
			}
			// thought=true 的 text part 视为扩展思考，归入 thinking 块（Gemini 2.5+ 语义）。
			if thought, ok := p["thought"].(bool); ok && thought {
				if txt, ok := p["text"].(string); ok {
					out.Parts = append(out.Parts, CanonPart{Type: "thinking", Text: txt})
				}
				continue
			}
			if fc, ok := p["functionCall"].(map[string]any); ok {
				name, _ := fc["name"].(string)
				args, _ := json.Marshal(fc["args"])
				toolIdx++
				out.Parts = append(out.Parts, CanonPart{
					Type: "tool_use", ToolID: fmt.Sprintf("call_%d", toolIdx),
					ToolName: name, ToolArgs: string(args),
				})
				out.StopReason = StopToolUse
			}
		}
	}
	return out, normalizeGeminiUsage(&resp.UsageMetadata), nil
}

// rejectGeminiUnsupported 在编码前检查 Gemini 协议无法表达的字段。
// Gemini 原生支持 topK/thinkingConfig/responseSchema，但不支持 logprobs/seed。
func rejectGeminiUnsupported(canon *CanonRequest) error {
	if canon.Logprobs != nil {
		return unsupportedFeature("logprobs", "Gemini 协议不支持对数概率")
	}
	if canon.Seed != nil {
		return unsupportedFeature("seed", "Gemini 协议不支持确定性种子")
	}
	if canon.ToolChoice != nil && canon.ToolChoice.DisableParallel {
		return unsupportedFeature("tool_choice.disable_parallel_tool_use",
			"Gemini 协议不支持禁用并行工具调用")
	}
	return nil
}
