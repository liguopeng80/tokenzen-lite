package domain

// Provider 上游厂商标识（业务归属，与协议解耦）。
type Provider string

const (
	ProviderOpenAI    Provider = "openai"
	ProviderAnthropic Provider = "anthropic"
	ProviderGemini    Provider = "gemini"
	ProviderZhipu     Provider = "zhipu"
	ProviderQwen      Provider = "qwen"
	ProviderDeepSeek  Provider = "deepseek"
	ProviderMinimax   Provider = "minimax"
	ProviderXAI       Provider = "xai"
	ProviderMoonshot  Provider = "moonshot"
	ProviderCustom    Provider = "custom"
)

// ValidProvider 判断厂商取值是否合法。
func ValidProvider(p Provider) bool {
	switch p {
	case ProviderOpenAI, ProviderAnthropic, ProviderGemini, ProviderZhipu,
		ProviderQwen, ProviderDeepSeek, ProviderMinimax, ProviderXAI,
		ProviderMoonshot, ProviderCustom:
		return true
	}
	return false
}

// /{provider}/v1/* 前缀路由新增的错误码（权威定义见 docs/api-contract.md 错误码表）。
// 现有的 /v1 错误码仍以裸字符串形式分散在 relay 与 api 包内，此处仅为本次新增码值
// 提供具名常量，避免裸字符串表达业务含义（coding-style.md Constants & Enums）。
const (
	// ErrCodeModelProviderMismatch URL 前缀锁定的 provider 与请求体 model 归属厂商不一致。
	// 属请求语义错误（HTTP 400），客户端应改用一致的前缀或模型，不重试。
	ErrCodeModelProviderMismatch = "model_provider_mismatch"
	// ErrCodeProviderNotFound URL 前缀的 provider slug 未命中任何已知厂商别名。
	// HTTP 404；在认证与限流之前直接返回，不占用限流配额。
	ErrCodeProviderNotFound = "provider_not_found"
	// ErrCodeUnsupportedFeature 跨协议路由时请求体携带目标上游协议无法表达的字段。
	// 由 relay.ErrUnsupportedFeature 触发：canonicalConduit.BuildRequest 经 codec.EncodeBody
	// 能力检查发现目标协议无法承载该字段（如 logprobs/seed 路由到 Anthropic 上游、
	// top_k 路由到 OpenAI chat 端点），返回明确错误而非静默丢弃。
	// HTTP 400；不重试、不换渠道。同协议直通路径不经 canonical，不触发此错误。
	ErrCodeUnsupportedFeature = "unsupported_feature"
)

// ChannelProtocol 渠道使用的上游协议。
type ChannelProtocol string

const (
	ProtocolOpenAICompat ChannelProtocol = "openai_compat"
	ProtocolAnthropic    ChannelProtocol = "anthropic"
	ProtocolGemini       ChannelProtocol = "gemini"
)

// ValidProtocol 判断协议取值是否合法。
func ValidProtocol(p ChannelProtocol) bool {
	for _, v := range AllProtocols() {
		if p == v {
			return true
		}
	}
	return false
}

// AllProtocols 返回全部 ChannelProtocol 常量，单一事实源。
// 供启动期覆盖检查引用（如 relay.TestCodecRegistryCoverage 断言每个协议都在 codec 注册表注册）。
// 新增协议常量时必须同步追加到此处，否则 ValidProtocol 与覆盖检查都会立即失败。
func AllProtocols() []ChannelProtocol {
	return []ChannelProtocol{
		ProtocolOpenAICompat,
		ProtocolAnthropic,
		ProtocolGemini,
	}
}

// ProtocolSupportsModality 下游端点 × 上游协议支持矩阵（权威定义见 docs/glossary.md
// 的 ChannelProtocol 节）：文本对话模型三种协议均可承载（同协议直通或跨协议转换）；
// 向量与图像模型仅 openai_compat 协议渠道可承载（/v1/embeddings、/v1/images/generations
// 为 OpenAI 协议直通，无跨协议转换）。管理端渠道校验与中继候选过滤共同引用本函数。
func ProtocolSupportsModality(p ChannelProtocol, m Modality) bool {
	if m == ModalityText {
		return ValidProtocol(p)
	}
	return p == ProtocolOpenAICompat
}

// ChannelStatus 渠道状态。
type ChannelStatus string

const (
	ChannelEnabled        ChannelStatus = "enabled"
	ChannelManualDisabled ChannelStatus = "manual_disabled"
	ChannelAutoDisabled   ChannelStatus = "auto_disabled"
)

// ChannelTestStatus 渠道连通性测试结果状态（管理端手工测试 / 半开探测写入）。
// 与 AuditResult 的 success/failure 取值一致但语义不同：审计结果描述一次受审计操作的终态，
// 此处描述渠道最近一次连通测试的成功与否，落库在 channels.last_test_status。
type ChannelTestStatus string

const (
	ChannelTestSuccess ChannelTestStatus = "success"
	ChannelTestFailure ChannelTestStatus = "failure"
)

// UsageStatus 一次请求的计费终态。
type UsageStatus string

const (
	UsageSettled  UsageStatus = "settled"  // 已按真实用量结算
	UsageRefunded UsageStatus = "refunded" // 请求失败已全额退款
	UsageFailed   UsageStatus = "failed"   // 失败：无预扣（余额不足被拒）或结算写入失败（预扣待补偿）
)

// ErrorClass 上游错误分类，驱动渠道禁用与重试决策。
// 致命错误类计入渠道连续失败计数，达到阈值后自动禁用渠道（非单次触发），
// 自动禁用的渠道由定时半开探测恢复。
type ErrorClass string

const (
	ErrClassNone        ErrorClass = ""
	ErrClassAuthFatal   ErrorClass = "auth_fatal"   // 401/403/密钥失效 → 计入连续失败，换渠道重试
	ErrClassQuotaFatal  ErrorClass = "quota_fatal"  // 402/明确余额类文案 → 计入连续失败，换渠道重试
	ErrClassRateLimited ErrorClass = "rate_limited" // 429 一律限流 → 不计入，换渠道重试
	ErrClassTransient   ErrorClass = "transient"    // 5xx/超时/网络 → 不计入，换渠道重试
	ErrClassBadRequest  ErrorClass = "bad_request"  // 400/内容策略 → 不重试，透传
	// ErrClassStreamAborted 流式响应中途中断：上游已开始返回内容，读取过程中断开。
	// 已产生的 token 照常结算，因此状态仍是已结算，但这条调用的回答是不完整的。
	// 员工反映「回答被截断」时，管理员据此在用量日志里认出这类调用。
	// 响应已经开始输出，无法改换渠道重发，故不参与重试与自动禁用判定。
	ErrClassStreamAborted ErrorClass = "stream_aborted"
)
