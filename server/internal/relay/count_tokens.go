package relay

// POST /v1/messages/count_tokens：Anthropic 下游的 token 计数端点。
// Claude Code 与 Anthropic 官方 SDK 在发起对话前调用它展示上下文占用。
//
// 该端点不消耗上游 token，因此不计费、不写用量日志，只走身份与模型策略校验。
// 优先转发给承载该模型的 anthropic 协议渠道以取得上游的权威计数；
// 没有这类渠道或上游未实现该端点时，回落到与预扣费同一套本地估算，
// 使跨协议渠道（openai_compat、gemini）上的 Anthropic 客户端仍能拿到可用数值。

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/obs"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
	"github.com/liguopeng80/tokenzen-lite/server/internal/strutil"
)

// countTokensMaxAttempts 转发时最多尝试的 anthropic 渠道数。
// 计数请求失败不影响业务，不值得穷举全部渠道。
const countTokensMaxAttempts = 2

// HandleCountTokens 处理 POST /v1/messages/count_tokens。
func (e *Engine) HandleCountTokens(w http.ResponseWriter, r *http.Request, ident Identity) {
	ctx := r.Context()
	writeErr := WriteAnthropicError

	r.Body = http.MaxBytesReader(w, r.Body, maxRelayBodyBytes)
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_request_error", "请求体不是合法 JSON")
		return
	}
	publicModel, _ := body["model"].(string)
	if publicModel == "" {
		writeErr(w, http.StatusBadRequest, "invalid_request_error", "缺少 model 字段")
		return
	}
	// 与对话端点使用同一套模型策略与上架校验：计数端点若放宽校验，
	// 会把「哪些模型存在」泄露给无权访问该模型的密钥。
	model, _, _, ok := e.prepareModel(ctx, w, ident, publicModel,
		domain.ModalityText, domain.BillPerToken, writeErr)
	if !ok {
		return
	}

	if raw, ok := e.forwardCountTokens(ctx, body, model.Name, ident.Provider); ok {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(raw)
		return
	}

	estimated := estimateAnthropicInputTokens(body)
	obs.Logger(ctx).Info("token 计数回落到本地估算",
		"model", publicModel, "input_tokens", estimated)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{"input_tokens": estimated})
}

// forwardCountTokens 向 anthropic 协议渠道转发计数请求，成功时返回上游原始响应体。
// 任何失败都只返回 false 由调用方回落到本地估算：计数请求失败不影响对话可用性，
// 因此既不计入渠道连续失败计数，也不触发自动禁用——上游代理未实现该端点是常见情况，
// 据此禁用渠道会连带切断正常的对话流量。
//
// provider 非零值时（/{provider}/v1/* 入口）只在该 provider 的 anthropic 渠道内转发，
// 不回退其他 provider——但 provider 前缀不改变 count_tokens 的回落语义：该 provider 无
// anthropic 渠道时照常回落本地估算（返回 false）。
func (e *Engine) forwardCountTokens(ctx context.Context, body map[string]any,
	publicModel string, provider domain.Provider) ([]byte, bool) {

	channels, err := e.Channels.ListEnabledForModel(ctx, publicModel)
	if err != nil {
		obs.Logger(ctx).Warn("查询计数渠道失败", "model", publicModel, "error", err)
		return nil, false
	}
	channels = filterByProvider(channels, provider)
	candidates := make([]store.Channel, 0, len(channels))
	for _, ch := range channels {
		if ch.Protocol == domain.ProtocolAnthropic {
			candidates = append(candidates, ch)
		}
	}
	if len(candidates) == 0 {
		return nil, false
	}

	upCtx, cancel := e.upstreamContext(ctx)
	defer cancel()

	tried := map[int64]bool{}
	for attempt := 0; attempt < countTokensMaxAttempts; attempt++ {
		ch := SelectChannel(candidates, tried)
		if ch == nil {
			return nil, false
		}
		tried[ch.ID] = true

		apiKey, err := e.Secrets.Decrypt(ch.APIKeyEncrypted)
		if err != nil {
			obs.Logger(ctx).Error("渠道密钥解密失败", "channel_id", ch.ID, "error", err)
			continue
		}
		req, err := buildCountTokensRequest(upCtx, ch, apiKey,
			copyBody(body), ch.MappedModel(publicModel))
		if err != nil {
			obs.Logger(ctx).Warn("构建计数请求失败", "channel_id", ch.ID, "error", err)
			continue
		}
		resp, err := e.Client.Do(req)
		if err != nil {
			obs.Logger(ctx).Warn("计数请求上游网络错误", "channel_id", ch.ID, "error", err)
			continue
		}
		raw, readErr := io.ReadAll(io.LimitReader(resp.Body, 8192))
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK || readErr != nil {
			obs.Logger(ctx).Warn("计数请求上游返回错误",
				"channel_id", ch.ID, "status", resp.StatusCode,
				"body_excerpt", strutil.Truncate(string(raw), 200))
			continue
		}
		return raw, true
	}
	return nil, false
}

// buildCountTokensRequest 构造上游 /v1/messages/count_tokens 请求。
// 与对话请求同源：同样改写模型名、套用渠道的参数与请求头覆盖。
func buildCountTokensRequest(ctx context.Context, ch *store.Channel, apiKey string,
	body map[string]any, upstreamModel string) (*http.Request, error) {

	body["model"] = upstreamModel
	// 计数端点不接受流式与生成上限参数，携带会被上游判为非法请求。
	delete(body, "stream")
	delete(body, "max_tokens")
	for k, v := range ch.ParamOverrides() {
		body[k] = v
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("序列化上游请求失败: %w", err)
	}
	url := strings.TrimRight(ch.BaseURL, "/") + "/v1/messages/count_tokens"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")
	for k, v := range ch.HeaderOverrides() {
		req.Header.Set(k, v)
	}
	return req, nil
}

// estimateAnthropicInputTokens 估算 Anthropic 请求的输入 token 数。
// 在 messages 的基础上追加 system 提示词——system 在 Anthropic 协议里独立于
// messages，漏算会让长系统提示词的会话被显著低估。
func estimateAnthropicInputTokens(body map[string]any) int64 {
	total := estimatePromptTokens(body)
	return total + estimateTokensFromText(textLenOfAnthropicField(body["system"]))
}

// textLenOfAnthropicField 计算 Anthropic 的「字符串或内容块数组」字段的文本长度。
func textLenOfAnthropicField(v any) int {
	switch f := v.(type) {
	case string:
		return len(f)
	case []any:
		total := 0
		for _, part := range f {
			if pm, ok := part.(map[string]any); ok {
				if txt, ok := pm["text"].(string); ok {
					total += len(txt)
				}
			}
		}
		return total
	default:
		return 0
	}
}
