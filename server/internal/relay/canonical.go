package relay

// canonical：跨协议转换的内部规范化模型。
// 2 种下游协议 × 3 种上游协议经由此中枢转换，避免两两直连的组合爆炸。
// 同协议直通路径不经过 canonical（零转换透传，见 conduit_openai.go 等）。

// CanonMessage 规范化消息。
type CanonMessage struct {
	Role  string // system / user / assistant / tool
	Parts []CanonPart
}

// CanonPart 消息内容片段。
type CanonPart struct {
	Type string // text / image / tool_use / tool_result / thinking

	Text string

	// image：data URI 或 URL
	ImageURL  string
	ImageMIME string

	// tool_use（assistant 发起调用）
	ToolID   string
	ToolName string
	ToolArgs string // JSON 字符串

	// tool_result（user 返回结果）
	ToolResultID string
	ToolResult   string
	ToolIsError  bool
}

// CanonTool 工具定义。
type CanonTool struct {
	Name        string
	Description string
	Schema      map[string]any // JSON Schema
}

// ToolChoiceMode 规范化工具选择模式。
type ToolChoiceMode string

const (
	// ToolChoiceUnset 未设置 tool_choice（不向上游传该字段）。
	ToolChoiceUnset ToolChoiceMode = ""
	// ToolChoiceAuto 模型自主决定是否调用工具。
	ToolChoiceAuto ToolChoiceMode = "auto"
	// ToolChoiceRequired 模型必须调用至少一个工具。
	ToolChoiceRequired ToolChoiceMode = "required"
	// ToolChoiceNone 禁止调用工具。
	ToolChoiceNone ToolChoiceMode = "none"
	// ToolChoiceTool 指定调用某个具体工具（Name 字段承载工具名）。
	ToolChoiceTool ToolChoiceMode = "tool"
)

// ToolChoice 工具选择策略。指针类型，nil = 未设置，区分零值。
type ToolChoice struct {
	Mode ToolChoiceMode
	// Name 当 Mode == ToolChoiceTool 时承载具体工具名。
	Name string
	// DisableParallel 禁止并行调用多个工具（仅 Anthropic 原生支持 disable_parallel_tool_use）。
	DisableParallel bool
}

// ThinkingConfig 扩展思考配置。
type ThinkingConfig struct {
	Enabled      bool
	BudgetTokens int64 // Anthropic thinking.budget_tokens / Gemini thinkingBudget
}

// LogprobsConfig 对数概率配置。
type LogprobsConfig struct {
	Enabled bool
	// TopN 返回每个 token 的前 N 个候选对数概率（OpenAI top_logprobs）。
	TopN int
}

// ResponseFormat 结构化输出配置。
type ResponseFormat struct {
	// Type "json_object" / "json_schema"
	Type string
	// JSONSchema 当 Type == "json_schema" 时的 schema 定义。
	JSONSchema map[string]any
}

// CanonRequest 规范化请求。
//
// 增补字段（TopK/Seed/Logprobs/ResponseFormat/Thinking/Metadata/ToolChoice 升级）以指针或
// 结构承载，nil/零值表示未设置，区分显式零值。跨协议路径下，目标上游协议无法表达的字段
// 由 codec.EncodeBody 返回 ErrUnsupportedFeature（见 codec.go），不静默丢弃。
type CanonRequest struct {
	Model       string
	System      string
	Messages    []CanonMessage
	MaxTokens   int64
	Temperature *float64
	TopP        *float64
	Stop        []string
	Stream      bool
	Tools       []CanonTool
	// ToolChoice 升级为结构体指针；nil = 未设置。
	ToolChoice *ToolChoice

	// 以下为 canonical 字段建模（方案 B 节）增补字段。
	TopK *int64
	Seed *int64
	// Logprobs 仅 OpenAI chat 端点原生支持；Anthropic/Gemini 上游 EncodeBody 拒绝。
	Logprobs *LogprobsConfig
	// ResponseFormat OpenAI/Gemini 上游支持；Anthropic 上游 EncodeBody 拒绝。
	ResponseFormat *ResponseFormat
	// Thinking 扩展思考：Anthropic 原生（thinking），Gemini 原生（thinkingBudget），
	// OpenAI 有损映射（reasoning_effort，按 BudgetTokens 阈值映射 low/medium/high）。
	Thinking *ThinkingConfig
	// Metadata 通用键值对。Anthropic 用 metadata.user_id，OpenAI 平铺为 user 字符串，
	// Gemini 无原生支持时降级丢弃（不拒绝）。键固定为规范名（如 "user_id"）。
	Metadata map[string]string
}

// CanonResponse 规范化非流式响应。
type CanonResponse struct {
	ID         string
	Model      string
	Parts      []CanonPart // text 与 tool_use 块
	StopReason string      // end_turn / max_tokens / tool_use / stop_sequence
}

// CanonEvent 规范化流事件。
type CanonEvent struct {
	Type string // text_delta / tool_use_start / tool_args_delta / block_stop / finish

	Text string // text_delta 的增量文本

	// tool_use_start
	ToolID   string
	ToolName string
	// tool_args_delta
	ArgsDelta string

	// finish
	StopReason string
}

// stop reason 的规范取值（Anthropic 语义为基准）。
const (
	StopEndTurn      = "end_turn"
	StopMaxTokens    = "max_tokens"
	StopToolUse      = "tool_use"
	StopStopSequence = "stop_sequence"
)

// openaiFinishToCanon OpenAI finish_reason → 规范 stop reason。
func openaiFinishToCanon(reason string) string {
	switch reason {
	case "length":
		return StopMaxTokens
	case "tool_calls", "function_call":
		return StopToolUse
	case "content_filter":
		return StopEndTurn
	default:
		return StopEndTurn
	}
}

// canonStopToOpenAI 规范 stop reason → OpenAI finish_reason。
func canonStopToOpenAI(reason string) string {
	switch reason {
	case StopMaxTokens:
		return "length"
	case StopToolUse:
		return "tool_calls"
	default:
		return "stop"
	}
}
