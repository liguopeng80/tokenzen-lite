package relay

// codec：上游协议的 canonical 互转契约（UpstreamCodec）与注册表。
// 三处 canonicalConduit 方法（BuildRequest / TransformResponse / NewStream）原本各持一份
// 按上游协议分发的 switch，新增协议需手工补全三处——P2-4 静默回落乱码的根因。
// 现收敛为按协议注册的 UpstreamCodec，分发改为查表（codecFor），缺注册即返错。
// 同协议直通路径（openaiPassthrough / anthropicPassthrough）不经 codec，与注册表正交。

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// ErrUnsupportedFeature 跨协议路由时请求体携带目标上游协议无法表达的字段。
// 由 codec.EncodeBody 能力检查返回，canonicalConduit.BuildRequest 透传，
// relayWithRetry 归为不可重试错误，400 返回客户端（错误码 unsupported_feature）。
// 同协议直通路径不经 codec，不触发此错误。
var ErrUnsupportedFeature = errors.New("relay: 目标上游协议不支持该字段，请改用同协议渠道或移除该字段")

// unsupportedFeature 包装 ErrUnsupportedFeature 并指明具体字段，便于上层格式化消息。
func unsupportedFeature(field, reason string) error {
	if reason == "" {
		return fmt.Errorf("%w: %s", ErrUnsupportedFeature, field)
	}
	return fmt.Errorf("%w: %s（%s）", ErrUnsupportedFeature, field, reason)
}

// UpstreamCodec 上游协议的 canonical 互转契约。
// 实现方在 init() 经 registerCodec 注册到 codecRegistry。
type UpstreamCodec interface {
	// EncodeBody canonical → 上游请求体（model 由实现注入到 body）。
	EncodeBody(canon *CanonRequest, upstreamModel string) (body map[string]any, err error)
	// BuildHTTPRequest 构建上游 HTTP 请求：URL、鉴权头、body 序列化。
	// stream 控制流式相关 query 参数与 stream_options 注入。
	BuildHTTPRequest(ctx context.Context, ch *store.Channel, apiKey, upstreamModel string,
		body map[string]any, stream bool) (*http.Request, error)
	// DecodeResponse 上游非流式响应体 → canonical + usage。
	DecodeResponse(raw []byte) (*CanonResponse, domain.NormalizedUsage, error)
	// NewStreamDecoder 返回该协议的流式解码器。
	NewStreamDecoder() streamDecoder
}

// codecRegistry 按 ChannelProtocol 注册的 codec 表。
var codecRegistry = map[domain.ChannelProtocol]UpstreamCodec{}

// registerCodec 注册一个协议的 codec，由 init() 调用。
func registerCodec(p domain.ChannelProtocol, c UpstreamCodec) {
	codecRegistry[p] = c
}

// codecFor 查表返回协议对应的 codec；未注册返回明确 error。
// 缺注册属程序员错误（新增协议常量时漏补 init 注册），三层保障见 conduit.go 注释。
func codecFor(p domain.ChannelProtocol) (UpstreamCodec, error) {
	c, ok := codecRegistry[p]
	if !ok {
		return nil, fmt.Errorf("relay: 协议 %q 未注册 codec（请补全 codec 注册）", p)
	}
	return c, nil
}

func init() {
	registerCodec(domain.ProtocolOpenAICompat, &openaiCodec{})
	registerCodec(domain.ProtocolAnthropic, &anthropicCodec{})
	registerCodec(domain.ProtocolGemini, &geminiCodec{})
}

// --- openaiCodec：openai_compat 协议的薄包装 ---

type openaiCodec struct{}

func (openaiCodec) EncodeBody(canon *CanonRequest, upstreamModel string) (map[string]any, error) {
	if err := rejectOpenAIUnsupported(canon); err != nil {
		return nil, err
	}
	return encodeOpenAIRequest(canon, upstreamModel), nil
}

func (openaiCodec) BuildHTTPRequest(ctx context.Context, ch *store.Channel, apiKey, upstreamModel string,
	body map[string]any, stream bool) (*http.Request, error) {
	return buildOpenAIRequest(ctx, ch, apiKey, body, upstreamModel, stream, "/chat/completions")
}

func (openaiCodec) DecodeResponse(raw []byte) (*CanonResponse, domain.NormalizedUsage, error) {
	return decodeOpenAIResponse(raw)
}

func (openaiCodec) NewStreamDecoder() streamDecoder {
	return newOpenAIStreamDecoder()
}

// --- anthropicCodec：anthropic 协议的薄包装 ---

type anthropicCodec struct{}

func (anthropicCodec) EncodeBody(canon *CanonRequest, upstreamModel string) (map[string]any, error) {
	if err := rejectAnthropicUnsupported(canon); err != nil {
		return nil, err
	}
	return encodeAnthropicRequest(canon, upstreamModel), nil
}

func (anthropicCodec) BuildHTTPRequest(ctx context.Context, ch *store.Channel, apiKey, upstreamModel string,
	body map[string]any, stream bool) (*http.Request, error) {
	// stream 信息已由 encodeAnthropicRequest 写入 body["stream"]，buildAnthropicRequest 沿用旧签名。
	return buildAnthropicRequest(ctx, ch, apiKey, body, upstreamModel)
}

func (anthropicCodec) DecodeResponse(raw []byte) (*CanonResponse, domain.NormalizedUsage, error) {
	return decodeAnthropicResponse(raw)
}

func (anthropicCodec) NewStreamDecoder() streamDecoder {
	return newAnthropicStreamDecoder()
}

// --- geminiCodec：gemini 协议的薄包装 ---
// 注：原 conduit.go 的 buildGeminiRequest 内联逻辑（URL 拼接 + action 选择 + 序列化）
// 收敛到 BuildHTTPRequest，与 EncodeBody（encodeGeminiRequest）拆分对齐其他 codec。

type geminiCodec struct{}

func (geminiCodec) EncodeBody(canon *CanonRequest, upstreamModel string) (map[string]any, error) {
	if err := rejectGeminiUnsupported(canon); err != nil {
		return nil, err
	}
	return encodeGeminiRequest(canon), nil
}

func (geminiCodec) BuildHTTPRequest(ctx context.Context, ch *store.Channel, apiKey, upstreamModel string,
	body map[string]any, stream bool) (*http.Request, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	action := ":generateContent"
	if stream {
		action = ":streamGenerateContent?alt=sse"
	}
	url := strings.TrimRight(ch.BaseURL, "/") + "/v1beta/models/" + upstreamModel + action
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", apiKey)
	for k, v := range ch.HeaderOverrides() {
		req.Header.Set(k, v)
	}
	return req, nil
}

func (geminiCodec) DecodeResponse(raw []byte) (*CanonResponse, domain.NormalizedUsage, error) {
	return decodeGeminiResponse(raw)
}

func (geminiCodec) NewStreamDecoder() streamDecoder {
	return newGeminiStreamDecoder()
}
