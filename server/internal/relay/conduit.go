package relay

// conduit：一次请求的"下游协议 × 上游协议"通道。
// 同协议 = 直通（透传 + model 改写 + usage 嗅探）；跨协议 = 经 canonical 转换。

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

// downstream 下游协议。
type downstream string

const (
	dsOpenAI    downstream = "openai"
	dsAnthropic downstream = "anthropic"
)

// conduit 非流式与流式的协议通道。
type conduit interface {
	BuildRequest(ctx context.Context, ch *store.Channel, apiKey, upstreamModel string) (*http.Request, error)
	// TransformResponse 上游 200 响应 → (下游响应体, usage, usage 是否存在, error)
	TransformResponse(raw []byte) ([]byte, domain.NormalizedUsage, bool, error)
	NewStream() conduitStream
}

// conduitStream 流式通道：上游 data 负载 → 发给客户端的完整 SSE 帧。
type conduitStream interface {
	ProcessPayload(payload []byte) [][]byte
	// ProcessDone 上游流结束（[DONE] 或连接关闭）时的收尾帧。
	ProcessDone() [][]byte
	Usage() (domain.NormalizedUsage, bool)
}

// newConduit 按下游/上游协议组合构建通道。
func newConduit(ds downstream, ch *store.Channel, body map[string]any,
	publicModel string, stream bool) (conduit, error) {

	switch {
	case ds == dsOpenAI && ch.Protocol == domain.ProtocolOpenAICompat:
		return &openaiPassthrough{body: body, publicModel: publicModel, stream: stream}, nil
	case ds == dsAnthropic && ch.Protocol == domain.ProtocolAnthropic:
		return &anthropicPassthrough{body: body, publicModel: publicModel, stream: stream}, nil
	default:
		var canon *CanonRequest
		var err error
		if ds == dsOpenAI {
			canon, err = decodeOpenAIRequest(body)
		} else {
			canon, err = decodeAnthropicRequest(body)
		}
		if err != nil {
			return nil, err
		}
		canon.Stream = stream
		return &canonicalConduit{
			ds: ds, up: ch.Protocol, canon: canon, publicModel: publicModel,
		}, nil
	}
}

// --- OpenAI 直通 ---

type openaiPassthrough struct {
	body        map[string]any
	publicModel string
	stream      bool
}

func (c *openaiPassthrough) BuildRequest(ctx context.Context, ch *store.Channel,
	apiKey, upstreamModel string) (*http.Request, error) {
	return buildOpenAIRequest(ctx, ch, apiKey, copyBody(c.body), upstreamModel, c.stream, "/chat/completions")
}

func (c *openaiPassthrough) TransformResponse(raw []byte) ([]byte, domain.NormalizedUsage, bool, error) {
	out, usage, err := parseOpenAIResponse(raw, c.publicModel)
	return out, usage, usage.TotalTokens() > 0, err
}

func (c *openaiPassthrough) NewStream() conduitStream {
	return &openaiPassStream{publicModel: c.publicModel}
}

type openaiPassStream struct {
	publicModel string
	usage       domain.NormalizedUsage
	usageFound  bool
}

func (s *openaiPassStream) ProcessPayload(payload []byte) [][]byte {
	rewritten, u, found := rewriteOpenAIChunk(payload, s.publicModel)
	if found {
		s.usage, s.usageFound = u, true
	}
	return [][]byte{[]byte("data: " + string(rewritten) + "\n\n")}
}

func (s *openaiPassStream) ProcessDone() [][]byte {
	return [][]byte{[]byte("data: [DONE]\n\n")}
}

func (s *openaiPassStream) Usage() (domain.NormalizedUsage, bool) { return s.usage, s.usageFound }

// --- Anthropic 直通 ---

type anthropicPassthrough struct {
	body        map[string]any
	publicModel string
	stream      bool
}

func buildAnthropicRequest(ctx context.Context, ch *store.Channel, apiKey string,
	body map[string]any, upstreamModel string) (*http.Request, error) {

	body["model"] = upstreamModel
	for k, v := range ch.ParamOverrides() {
		body[k] = v
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("序列化上游请求失败: %w", err)
	}
	url := strings.TrimRight(ch.BaseURL, "/") + "/v1/messages"
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

func (c *anthropicPassthrough) BuildRequest(ctx context.Context, ch *store.Channel,
	apiKey, upstreamModel string) (*http.Request, error) {
	return buildAnthropicRequest(ctx, ch, apiKey, copyBody(c.body), upstreamModel)
}

func (c *anthropicPassthrough) TransformResponse(raw []byte) ([]byte, domain.NormalizedUsage, bool, error) {
	var resp map[string]json.RawMessage
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, domain.NormalizedUsage{}, false, fmt.Errorf("上游响应不是合法 JSON: %w", err)
	}
	var usage domain.NormalizedUsage
	found := false
	if rawUsage, ok := resp["usage"]; ok {
		var u anthropicUsage
		if err := json.Unmarshal(rawUsage, &u); err == nil {
			usage = normalizeAnthropicUsage(&u)
			found = usage.TotalTokens() > 0
		}
	}
	nameJSON, _ := json.Marshal(c.publicModel)
	resp["model"] = nameJSON
	out, err := json.Marshal(resp)
	return out, usage, found, err
}

func (c *anthropicPassthrough) NewStream() conduitStream {
	return &anthropicPassStream{publicModel: c.publicModel, decoder: newAnthropicStreamDecoder()}
}

type anthropicPassStream struct {
	publicModel string
	decoder     *anthropicStreamDecoder
}

func (s *anthropicPassStream) ProcessPayload(payload []byte) [][]byte {
	// 嗅探 usage
	s.decoder.Decode(payload)
	// 改写 message_start 中的 model 后按事件类型重发
	var ev map[string]json.RawMessage
	if err := json.Unmarshal(payload, &ev); err != nil {
		return [][]byte{[]byte("data: " + string(payload) + "\n\n")}
	}
	var evType string
	_ = json.Unmarshal(ev["type"], &evType)
	if evType == "message_start" {
		var full map[string]any
		if err := json.Unmarshal(payload, &full); err == nil {
			if msg, ok := full["message"].(map[string]any); ok {
				msg["model"] = s.publicModel
			}
			payload, _ = json.Marshal(full)
		}
	}
	if evType == "" {
		return [][]byte{[]byte("data: " + string(payload) + "\n\n")}
	}
	return [][]byte{[]byte("event: " + evType + "\ndata: " + string(payload) + "\n\n")}
}

func (s *anthropicPassStream) ProcessDone() [][]byte { return nil }

func (s *anthropicPassStream) Usage() (domain.NormalizedUsage, bool) { return s.decoder.Usage() }

// --- 跨协议（canonical）---

type canonicalConduit struct {
	ds          downstream
	up          domain.ChannelProtocol
	canon       *CanonRequest
	publicModel string
}

func (c *canonicalConduit) BuildRequest(ctx context.Context, ch *store.Channel,
	apiKey, upstreamModel string) (*http.Request, error) {

	codec, err := codecFor(c.up)
	if err != nil {
		return nil, err
	}
	body, err := codec.EncodeBody(c.canon, upstreamModel)
	if err != nil {
		return nil, err
	}
	return codec.BuildHTTPRequest(ctx, ch, apiKey, upstreamModel, body, c.canon.Stream)
}

func (c *canonicalConduit) TransformResponse(raw []byte) ([]byte, domain.NormalizedUsage, bool, error) {
	codec, err := codecFor(c.up)
	if err != nil {
		return nil, domain.NormalizedUsage{}, false, err
	}
	canonResp, usage, err := codec.DecodeResponse(raw)
	if err != nil {
		return nil, usage, false, err
	}
	// 下游 2 路编码（OpenAI / Anthropic）保留二分——只有 2 个下游协议，
	// 建 downstreamRegistry 收益不足；若引入第 3 下游协议再对称建表。
	var out []byte
	if c.ds == dsOpenAI {
		out = encodeOpenAIResponse(canonResp, c.publicModel, usage)
	} else {
		out = encodeAnthropicResponse(canonResp, c.publicModel, usage)
	}
	return out, usage, usage.TotalTokens() > 0, nil
}

func (c *canonicalConduit) NewStream() conduitStream {
	// NewStream 接口不返错；codecFor 缺注册属程序员错误（新增协议常量时漏补 init 注册），
	// panic 与 HTTP 层 chimw.Recoverer 兜底契约一致。当前不可达——newConduit 只为
	// 已知协议构造 canonicalConduit，且 TestCodecRegistryCoverage 在测试层断言全注册。
	codec, err := codecFor(c.up)
	if err != nil {
		panic(err)
	}
	var enc streamEncoder
	if c.ds == dsAnthropic {
		enc = newAnthropicStreamEncoder(c.publicModel)
	} else {
		enc = newOpenAIStreamEncoder(c.publicModel)
	}
	return &canonicalStream{dec: codec.NewStreamDecoder(), enc: enc}
}

type canonicalStream struct {
	dec  streamDecoder
	enc  streamEncoder
	done bool
}

func (s *canonicalStream) ProcessPayload(payload []byte) [][]byte {
	var frames [][]byte
	for _, ev := range s.dec.Decode(payload) {
		frames = append(frames, s.enc.Encode(ev)...)
	}
	return frames
}

func (s *canonicalStream) ProcessDone() [][]byte {
	if s.done {
		return nil
	}
	s.done = true
	usage, _ := s.dec.Usage()
	return s.enc.Finish(usage)
}

func (s *canonicalStream) Usage() (domain.NormalizedUsage, bool) { return s.dec.Usage() }
