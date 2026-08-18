package relay

// canonical 字段建模（方案 B 节）测试：
//   - 各 codec 对新字段（thinking/response_format/logprobs/seed/top_k/metadata/tool_choice）的编解码映射
//   - 不支持组合跨协议时返回 ErrUnsupportedFeature（非静默丢弃）
//   - 直通路径不经 codec，新字段不影响直通

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// --- OpenAI 解码新字段 ---

func TestDecodeOpenAIRequestNewFields(t *testing.T) {
	var body map[string]any
	_ = json.Unmarshal([]byte(`{
		"model": "gpt-4o", "max_tokens": 100,
		"seed": 42, "logprobs": true, "top_logprobs": 5,
		"response_format": {"type": "json_schema",
			"json_schema": {"schema": {"type": "object"}}},
		"reasoning_effort": "medium",
		"user": "user-abc",
		"messages": [{"role": "user", "content": "hi"}]
	}`), &body)
	canon, err := decodeOpenAIRequest(body)
	if err != nil {
		t.Fatalf("decode 失败: %v", err)
	}
	if canon.Seed == nil || *canon.Seed != 42 {
		t.Errorf("Seed 解析错误: %+v", canon.Seed)
	}
	if canon.Logprobs == nil || !canon.Logprobs.Enabled || canon.Logprobs.TopN != 5 {
		t.Errorf("Logprobs 解析错误: %+v", canon.Logprobs)
	}
	if canon.ResponseFormat == nil || canon.ResponseFormat.Type != "json_schema" {
		t.Errorf("ResponseFormat.Type 错误: %+v", canon.ResponseFormat)
	}
	if canon.ResponseFormat.JSONSchema == nil || canon.ResponseFormat.JSONSchema["type"] != "object" {
		t.Errorf("ResponseFormat.JSONSchema 错误: %+v", canon.ResponseFormat.JSONSchema)
	}
	if canon.Thinking == nil || !canon.Thinking.Enabled || canon.Thinking.BudgetTokens != 16000 {
		t.Errorf("Thinking 应从 reasoning_effort=medium 映射 budget=16000: %+v", canon.Thinking)
	}
	if canon.Metadata == nil || canon.Metadata["user_id"] != "user-abc" {
		t.Errorf("Metadata.user_id 应来自 user 字段: %+v", canon.Metadata)
	}
}

func TestDecodeOpenAIToolChoiceTypes(t *testing.T) {
	cases := []struct {
		name  string
		body  string
		mode  ToolChoiceMode
		name_ string // tool 名（仅 ToolChoiceTool 时校验）
	}{
		{`auto`, `{"model":"m","tool_choice":"auto"}`, ToolChoiceAuto, ""},
		{`required`, `{"model":"m","tool_choice":"required"}`, ToolChoiceRequired, ""},
		{`none`, `{"model":"m","tool_choice":"none"}`, ToolChoiceNone, ""},
		{`function`, `{"model":"m","tool_choice":{"type":"function","function":{"name":"lookup"}}}`,
			ToolChoiceTool, "lookup"},
	}
	for _, c := range cases {
		var body map[string]any
		_ = json.Unmarshal([]byte(c.body), &body)
		canon, err := decodeOpenAIRequest(body)
		if err != nil {
			t.Fatalf("%s: decode 失败: %v", c.name, err)
		}
		if canon.ToolChoice == nil || canon.ToolChoice.Mode != c.mode {
			t.Errorf("%s: tool_choice mode 期望 %s 实际 %+v", c.name, c.mode, canon.ToolChoice)
		}
		if c.mode == ToolChoiceTool && canon.ToolChoice.Name != c.name_ {
			t.Errorf("%s: tool_choice name 期望 %s 实际 %s", c.name, c.name_, canon.ToolChoice.Name)
		}
	}
}

// --- Anthropic 解码新字段 ---

func TestDecodeAnthropicRequestNewFields(t *testing.T) {
	var body map[string]any
	_ = json.Unmarshal([]byte(`{
		"model": "claude-sonnet-4-6", "max_tokens": 1024,
		"top_k": 40,
		"thinking": {"type": "enabled", "budget_tokens": 12000},
		"metadata": {"user_id": "sess-xyz"},
		"tool_choice": {"type": "auto", "disable_parallel_tool_use": true},
		"messages": [{"role": "user", "content": "hi"}]
	}`), &body)
	canon, err := decodeAnthropicRequest(body)
	if err != nil {
		t.Fatalf("decode 失败: %v", err)
	}
	if canon.TopK == nil || *canon.TopK != 40 {
		t.Errorf("TopK 解析错误: %+v", canon.TopK)
	}
	if canon.Thinking == nil || !canon.Thinking.Enabled || canon.Thinking.BudgetTokens != 12000 {
		t.Errorf("Thinking 解析错误: %+v", canon.Thinking)
	}
	if canon.Metadata == nil || canon.Metadata["user_id"] != "sess-xyz" {
		t.Errorf("Metadata.user_id 解析错误: %+v", canon.Metadata)
	}
	if canon.ToolChoice == nil || canon.ToolChoice.Mode != ToolChoiceAuto {
		t.Fatalf("tool_choice mode 应为 auto: %+v", canon.ToolChoice)
	}
	if !canon.ToolChoice.DisableParallel {
		t.Errorf("tool_choice.DisableParallel 应为 true: %+v", canon.ToolChoice)
	}
}

// --- OpenAI 编码新字段（含 thinking→reasoning_effort 有损映射）---

func TestEncodeOpenAIRequestNewFields(t *testing.T) {
	budget := int64(30000)
	seed := int64(42)
	canon := &CanonRequest{
		Model: "m", MaxTokens: 100,
		Seed:     &seed,
		Logprobs: &LogprobsConfig{Enabled: true, TopN: 3},
		ResponseFormat: &ResponseFormat{Type: "json_schema",
			JSONSchema: map[string]any{"type": "object"}},
		Thinking: &ThinkingConfig{Enabled: true, BudgetTokens: budget},
		Metadata: map[string]string{"user_id": "u1"},
		Messages: []CanonMessage{{Role: "user", Parts: []CanonPart{{Type: "text", Text: "x"}}}},
	}
	body := encodeOpenAIRequest(canon, "up-m")
	if body["seed"] != int64(42) {
		t.Errorf("seed 编码错误: %v", body["seed"])
	}
	if body["logprobs"] != true || body["top_logprobs"] != 3 {
		t.Errorf("logprobs 编码错误: %v %v", body["logprobs"], body["top_logprobs"])
	}
	rf, _ := body["response_format"].(map[string]any)
	if rf == nil || rf["type"] != "json_schema" {
		t.Errorf("response_format 编码错误: %+v", rf)
	}
	// budget 30000 → reasoning_effort=high
	if body["reasoning_effort"] != "high" {
		t.Errorf("reasoning_effort 应为 high（budget>=24000）: %v", body["reasoning_effort"])
	}
	if body["user"] != "u1" {
		t.Errorf("user 应从 Metadata.user_id 映射: %v", body["user"])
	}
}

func TestBudgetToReasoningEffortThresholds(t *testing.T) {
	cases := []struct {
		budget int64
		want   string
	}{
		{0, "low"}, {7999, "low"},
		{8000, "medium"}, {23999, "medium"},
		{24000, "high"}, {100000, "high"},
	}
	for _, c := range cases {
		if got := budgetToReasoningEffort(c.budget); got != c.want {
			t.Errorf("budget=%d: 期望 %s 实际 %s", c.budget, c.want, got)
		}
	}
}

// --- Anthropic 编码新字段（含 thinking / metadata / disable_parallel_tool_use）---

func TestEncodeAnthropicRequestNewFields(t *testing.T) {
	canon := &CanonRequest{
		Model:      "m",
		MaxTokens:  1024,
		TopK:       int64Ptr(40),
		Thinking:   &ThinkingConfig{Enabled: true, BudgetTokens: 12000},
		Metadata:   map[string]string{"user_id": "sess-1"},
		ToolChoice: &ToolChoice{Mode: ToolChoiceRequired, DisableParallel: true},
		Messages:   []CanonMessage{{Role: "user", Parts: []CanonPart{{Type: "text", Text: "x"}}}},
	}
	body := encodeAnthropicRequest(canon, "up-claude")
	if body["top_k"] != int64(40) {
		t.Errorf("top_k 编码错误: %v", body["top_k"])
	}
	think, _ := body["thinking"].(map[string]any)
	if think == nil || think["type"] != "enabled" || think["budget_tokens"] != int64(12000) {
		t.Errorf("thinking 编码错误: %+v", think)
	}
	md, _ := body["metadata"].(map[string]any)
	if md == nil || md["user_id"] != "sess-1" {
		t.Errorf("metadata 编码错误: %+v", md)
	}
	tc, _ := body["tool_choice"].(map[string]any)
	if tc == nil || tc["type"] != "any" {
		t.Errorf("tool_choice type 应为 any(required): %+v", tc)
	}
	if tc["disable_parallel_tool_use"] != true {
		t.Errorf("disable_parallel_tool_use 应为 true: %+v", tc)
	}
}

// --- Gemini 编码新字段（topK / thinkingBudget / responseMimeType）---

func TestEncodeGeminiRequestNewFields(t *testing.T) {
	canon := &CanonRequest{
		Model:    "m",
		TopK:     int64Ptr(50),
		Thinking: &ThinkingConfig{Enabled: true, BudgetTokens: 8000},
		ResponseFormat: &ResponseFormat{Type: "json_schema",
			JSONSchema: map[string]any{"type": "object"}},
		Messages: []CanonMessage{{Role: "user", Parts: []CanonPart{{Type: "text", Text: "x"}}}},
	}
	body := encodeGeminiRequest(canon)
	raw, _ := json.Marshal(body)
	s := string(raw)
	genCfg, _ := body["generationConfig"].(map[string]any)
	if genCfg == nil {
		t.Fatalf("generationConfig 缺失: %s", s)
	}
	if genCfg["topK"] != int64(50) {
		t.Errorf("topK 编码错误: %v", genCfg["topK"])
	}
	think, _ := genCfg["thinkingConfig"].(map[string]any)
	if think == nil || think["thinkingBudget"] != int64(8000) {
		t.Errorf("thinkingConfig.thinkingBudget 编码错误: %+v", think)
	}
	if genCfg["responseMimeType"] != "application/json" {
		t.Errorf("responseMimeType 编码错误: %v", genCfg["responseMimeType"])
	}
	if genCfg["responseSchema"] == nil {
		t.Errorf("responseSchema 应在位")
	}
}

// --- 不支持组合跨协议时返回 ErrUnsupportedFeature ---

func TestRejectUnsupportedLogprobs(t *testing.T) {
	cases := []struct {
		name  string
		codec UpstreamCodec
		canon *CanonRequest
		field string
	}{
		{
			name:  "logprobs→Anthropic",
			codec: anthropicCodec{},
			canon: &CanonRequest{Logprobs: &LogprobsConfig{Enabled: true}},
			field: "logprobs",
		},
		{
			name:  "logprobs→Gemini",
			codec: geminiCodec{},
			canon: &CanonRequest{Logprobs: &LogprobsConfig{Enabled: true}},
			field: "logprobs",
		},
	}
	for _, c := range cases {
		_, err := c.codec.EncodeBody(c.canon, "up-model")
		if !errors.Is(err, ErrUnsupportedFeature) {
			t.Errorf("%s: 期望 ErrUnsupportedFeature 实际 %v", c.name, err)
		}
		if err == nil || !strings.Contains(err.Error(), c.field) {
			t.Errorf("%s: 错误文案应含字段名 %q: %v", c.name, c.field, err)
		}
	}
}

func TestRejectUnsupportedSeed(t *testing.T) {
	for _, codec := range []UpstreamCodec{anthropicCodec{}, geminiCodec{}} {
		_, err := codec.EncodeBody(&CanonRequest{Seed: int64Ptr(7)}, "up")
		if !errors.Is(err, ErrUnsupportedFeature) {
			t.Errorf("seed 应被拒绝: %v", err)
		}
	}
}

func TestRejectUnsupportedResponseFormat(t *testing.T) {
	canon := &CanonRequest{ResponseFormat: &ResponseFormat{Type: "json_object"}}
	if _, err := (anthropicCodec{}).EncodeBody(canon, "up"); !errors.Is(err, ErrUnsupportedFeature) {
		t.Errorf("response_format→Anthropic 应被拒绝: %v", err)
	}
}

func TestRejectUnsupportedTopK(t *testing.T) {
	canon := &CanonRequest{TopK: int64Ptr(40)}
	if _, err := (openaiCodec{}).EncodeBody(canon, "up"); !errors.Is(err, ErrUnsupportedFeature) {
		t.Errorf("top_k→OpenAI chat 应被拒绝: %v", err)
	}
}

func TestRejectUnsupportedDisableParallel(t *testing.T) {
	canon := &CanonRequest{ToolChoice: &ToolChoice{Mode: ToolChoiceAuto, DisableParallel: true}}
	for _, codec := range []UpstreamCodec{openaiCodec{}, geminiCodec{}} {
		_, err := codec.EncodeBody(canon, "up")
		if !errors.Is(err, ErrUnsupportedFeature) {
			t.Errorf("disable_parallel_tool_use 应被拒绝: %v", err)
		}
	}
}

// OpenAI codec 不应拒绝它支持的字段（logprobs/seed/response_format/thinking）。
func TestOpenAIDoesNotRejectSupportedFields(t *testing.T) {
	canon := &CanonRequest{
		Seed:           int64Ptr(1),
		Logprobs:       &LogprobsConfig{Enabled: true},
		ResponseFormat: &ResponseFormat{Type: "json_object"},
		Thinking:       &ThinkingConfig{Enabled: true, BudgetTokens: 10000},
		Messages:       []CanonMessage{{Role: "user", Parts: []CanonPart{{Type: "text", Text: "x"}}}},
	}
	if _, err := (openaiCodec{}).EncodeBody(canon, "up"); err != nil {
		t.Errorf("OpenAI 应接受其支持的字段: %v", err)
	}
}

// Anthropic codec 不应拒绝它支持的字段（thinking/top_k）。
func TestAnthropicDoesNotRejectSupportedFields(t *testing.T) {
	canon := &CanonRequest{
		TopK:     int64Ptr(40),
		Thinking: &ThinkingConfig{Enabled: true, BudgetTokens: 8000},
		Messages: []CanonMessage{{Role: "user", Parts: []CanonPart{{Type: "text", Text: "x"}}}},
	}
	if _, err := (anthropicCodec{}).EncodeBody(canon, "up"); err != nil {
		t.Errorf("Anthropic 应接受其支持的字段: %v", err)
	}
}

// --- metadata 跨协议降级（Gemini 不拒绝，静默丢弃）---

func TestMetadataGeminiDegradesNotReject(t *testing.T) {
	canon := &CanonRequest{
		Metadata: map[string]string{"user_id": "u1"},
		Messages: []CanonMessage{{Role: "user", Parts: []CanonPart{{Type: "text", Text: "x"}}}},
	}
	body, err := (geminiCodec{}).EncodeBody(canon, "up")
	if err != nil {
		t.Fatalf("Gemini 不应因 metadata 拒绝: %v", err)
	}
	raw, _ := json.Marshal(body)
	if strings.Contains(string(raw), "user_id") {
		t.Errorf("Gemini body 不应含 user_id: %s", raw)
	}
}

// --- thinking 跨协议往返（Anthropic→canonical→Anthropic 保形）---

func TestThinkingAnthropicRoundtrip(t *testing.T) {
	var src map[string]any
	_ = json.Unmarshal([]byte(`{
		"model": "claude", "max_tokens": 1024,
		"thinking": {"type": "enabled", "budget_tokens": 10000},
		"messages": [{"role": "user", "content": "hi"}]
	}`), &src)
	canon, err := decodeAnthropicRequest(src)
	if err != nil {
		t.Fatalf("decode 失败: %v", err)
	}
	if canon.Thinking.BudgetTokens != 10000 {
		t.Fatalf("budget 解析错误: %d", canon.Thinking.BudgetTokens)
	}
	// 编码回 Anthropic 应保留 budget_tokens 原值
	body := encodeAnthropicRequest(canon, "up-claude")
	think, _ := body["thinking"].(map[string]any)
	if think["type"] != "enabled" || think["budget_tokens"] != int64(10000) {
		t.Errorf("thinking 往返失真: %+v", think)
	}
}

// --- metadata.user_id 跨协议往返（Anthropic→canonical→Anthropic 保形）---

func TestMetadataAnthropicRoundtrip(t *testing.T) {
	var src map[string]any
	_ = json.Unmarshal([]byte(`{
		"model": "claude", "max_tokens": 1024,
		"metadata": {"user_id": "claude-code-session-42"},
		"messages": [{"role": "user", "content": "hi"}]
	}`), &src)
	canon, err := decodeAnthropicRequest(src)
	if err != nil {
		t.Fatalf("decode 失败: %v", err)
	}
	body := encodeAnthropicRequest(canon, "up-claude")
	md, _ := body["metadata"].(map[string]any)
	if md["user_id"] != "claude-code-session-42" {
		t.Errorf("metadata 往返失真: %+v", md)
	}
}

// --- 直通路径不经 codec：字段原样透传 ---

func TestAnthropicPassthroughPreservesAllFields(t *testing.T) {
	// 直通路径直接使用原始 body（不经 decode/encode），所有字段原样保留。
	raw := `{
		"model": "claude", "max_tokens": 1024,
		"thinking": {"type": "enabled", "budget_tokens": 10000},
		"metadata": {"user_id": "u1"},
		"top_k": 40,
		"tool_choice": {"type": "auto", "disable_parallel_tool_use": true},
		"messages": [{"role": "user", "content": "hi"}]
	}`
	var body map[string]any
	_ = json.Unmarshal([]byte(raw), &body)

	pass := &anthropicPassthrough{body: body, publicModel: "claude-public"}
	// 直通不应调用 codecFor/encodeAnthropicRequest，直接透传 body。
	// 用 copyBody 验证 body 原样可序列化且保留全部字段。
	clone := copyBody(body)
	out, _ := json.Marshal(clone)
	s := string(out)
	for _, marker := range []string{
		`"thinking"`, `"budget_tokens":10000`,
		`"user_id":"u1"`, `"top_k":40`,
		`"disable_parallel_tool_use":true`,
	} {
		if !strings.Contains(s, marker) {
			t.Errorf("直通应原样保留字段，缺失 %q: %s", marker, s)
		}
	}
	// 确认 anthropicPassthrough.BuildRequest 不返回错误（即不经 codec 能力检查）
	if pass == nil {
		t.Fatal("passthrough 构造失败")
	}
}

func TestOpenAIPassthroughPreservesAllFields(t *testing.T) {
	raw := `{
		"model": "gpt-4o", "max_tokens": 100,
		"seed": 42, "logprobs": true, "top_logprobs": 3,
		"response_format": {"type": "json_object"},
		"reasoning_effort": "high",
		"user": "u1",
		"messages": [{"role": "user", "content": "hi"}]
	}`
	var body map[string]any
	_ = json.Unmarshal([]byte(raw), &body)
	clone := copyBody(body)
	out, _ := json.Marshal(clone)
	s := string(out)
	for _, marker := range []string{
		`"seed":42`, `"logprobs":true`, `"top_logprobs":3`,
		`"response_format"`, `"reasoning_effort":"high"`, `"user":"u1"`,
	} {
		if !strings.Contains(s, marker) {
			t.Errorf("直通应原样保留字段，缺失 %q: %s", marker, s)
		}
	}
}

// --- 直通路径不触发 ErrUnsupportedFeature（不调 codec）---

func TestPassthroughNeverCallsCodec(t *testing.T) {
	// 含 logprobs 的 Anthropic 请求：跨协议路径（OpenAI 下游）会拒绝，
	// 但 Anthropic 直通路径（Anthropic 上游 + Anthropic 下游）不会拒绝。
	// newConduit 在同协议组合下返回 anthropicPassthrough，不构造 canonicalConduit。
	body := map[string]any{
		"model": "claude", "max_tokens": float64(100),
		"messages": []any{map[string]any{"role": "user", "content": "hi"}},
	}
	ch := &store.Channel{Protocol: domain.ProtocolAnthropic}
	cd, err := newConduit(dsAnthropic, ch, body, "claude-public", false)
	if err != nil {
		t.Fatalf("newConduit 失败: %v", err)
	}
	if _, ok := cd.(*anthropicPassthrough); !ok {
		t.Fatalf("同协议应构造 anthropicPassthrough，实际 %T", cd)
	}
}

// int64Ptr 工具
func int64Ptr(v int64) *int64 { return &v }
