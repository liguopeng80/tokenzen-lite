package relay

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// openaiUsage 是 OpenAI 语义的 usage 结构。
type openaiUsage struct {
	PromptTokens        int64 `json:"prompt_tokens"`
	CompletionTokens    int64 `json:"completion_tokens"`
	PromptTokensDetails struct {
		CachedTokens int64 `json:"cached_tokens"`
		AudioTokens  int64 `json:"audio_tokens"`
	} `json:"prompt_tokens_details"`
	CompletionTokensDetails struct {
		AudioTokens int64 `json:"audio_tokens"`
	} `json:"completion_tokens_details"`
}

// normalizeOpenAIUsage 归一化 OpenAI 语义：prompt_tokens 含缓存与音频，需拆出。
func normalizeOpenAIUsage(u *openaiUsage) domain.NormalizedUsage {
	base := u.PromptTokens - u.PromptTokensDetails.CachedTokens - u.PromptTokensDetails.AudioTokens
	if base < 0 {
		base = 0
	}
	output := u.CompletionTokens - u.CompletionTokensDetails.AudioTokens
	if output < 0 {
		output = 0
	}
	return domain.NormalizedUsage{
		BaseInput:   base,
		CacheRead:   u.PromptTokensDetails.CachedTokens,
		Output:      output,
		AudioInput:  u.PromptTokensDetails.AudioTokens,
		AudioOutput: u.CompletionTokensDetails.AudioTokens,
		Semantic:    domain.SemanticOpenAI,
	}
}

// buildOpenAIRequest 构建发往 openai_compat 渠道的上游请求：
// 改写 model、注入鉴权与 include_usage、应用渠道参数/请求头覆盖。
//
// base_url 约定：渠道的 BaseURL 应包含版本根路径（如 https://api.openai.com/v1、
// 智谱的 https://open.bigmodel.cn/api/paas/v4），path 仅追加 /chat/completions。
// 这样不同厂商的版本前缀（/v1、/paas/v4、/compatible-mode/v1 等）由 base_url 表达，
// codec 不再硬编码 /v1，避免与 base_url 中已有的版本段重复。
func buildOpenAIRequest(ctx context.Context, ch *store.Channel, apiKey string,
	body map[string]any, upstreamModel string, stream bool, path string) (*http.Request, error) {

	body["model"] = upstreamModel
	if stream {
		// 确保流式响应最后一个 chunk 携带 usage
		opts, _ := body["stream_options"].(map[string]any)
		if opts == nil {
			opts = map[string]any{}
		}
		opts["include_usage"] = true
		body["stream_options"] = opts
	}
	for k, v := range ch.ParamOverrides() {
		body[k] = v
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("序列化上游请求失败: %w", err)
	}
	url := strings.TrimRight(ch.BaseURL, "/") + path
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("构建上游请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	for k, v := range ch.HeaderOverrides() {
		req.Header.Set(k, v)
	}
	return req, nil
}

// parseOpenAIResponse 解析非流式响应：提取 usage 并把 model 改回公开名。
// 返回改写后的响应体。
func parseOpenAIResponse(raw []byte, publicModel string) ([]byte, domain.NormalizedUsage, error) {
	var resp map[string]json.RawMessage
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, domain.NormalizedUsage{}, fmt.Errorf("上游响应不是合法 JSON: %w", err)
	}
	var usage domain.NormalizedUsage
	if rawUsage, ok := resp["usage"]; ok {
		var u openaiUsage
		if err := json.Unmarshal(rawUsage, &u); err == nil {
			usage = normalizeOpenAIUsage(&u)
		}
	}
	nameJSON, _ := json.Marshal(publicModel)
	resp["model"] = nameJSON
	out, err := json.Marshal(resp)
	if err != nil {
		return nil, usage, fmt.Errorf("重写响应失败: %w", err)
	}
	return out, usage, nil
}

// rewriteOpenAIChunk 处理一个流式 data chunk：改写 model、嗅探 usage。
// 返回改写后的 chunk；usage 存在时通过 found 返回。
func rewriteOpenAIChunk(data []byte, publicModel string) (out []byte, usage domain.NormalizedUsage, found bool) {
	var chunk map[string]json.RawMessage
	if err := json.Unmarshal(data, &chunk); err != nil {
		return data, usage, false // 非 JSON 原样透传
	}
	if rawUsage, ok := chunk["usage"]; ok && string(rawUsage) != "null" {
		var u openaiUsage
		if err := json.Unmarshal(rawUsage, &u); err == nil &&
			(u.PromptTokens > 0 || u.CompletionTokens > 0) {
			usage = normalizeOpenAIUsage(&u)
			found = true
		}
	}
	if _, ok := chunk["model"]; ok {
		nameJSON, _ := json.Marshal(publicModel)
		chunk["model"] = nameJSON
	}
	rewritten, err := json.Marshal(chunk)
	if err != nil {
		return data, usage, found
	}
	return rewritten, usage, found
}

// estimateTokensFromText 无 usage 时的兜底估算：按 UTF-8 字节数 / 4。
func estimateTokensFromText(byteLen int) int64 {
	n := int64(byteLen) / 4
	if n < 1 && byteLen > 0 {
		n = 1
	}
	return n
}

// estimatePromptTokens 从请求体粗估 prompt token 数（预扣费用）。
func estimatePromptTokens(body map[string]any) int64 {
	total := 0
	if msgs, ok := body["messages"].([]any); ok {
		for _, m := range msgs {
			mm, ok := m.(map[string]any)
			if !ok {
				continue
			}
			switch c := mm["content"].(type) {
			case string:
				total += len(c)
			case []any:
				for _, part := range c {
					if pm, ok := part.(map[string]any); ok {
						if txt, ok := pm["text"].(string); ok {
							total += len(txt)
						}
					}
				}
			}
		}
	}
	if input, ok := body["input"].(string); ok {
		total += len(input)
	}
	return estimateTokensFromText(total)
}
