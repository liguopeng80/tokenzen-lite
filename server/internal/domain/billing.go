package domain

import "time"

// LedgerEntryType 积分流水类型。
type LedgerEntryType string

const (
	LedgerGrant        LedgerEntryType = "grant"         // 管理员分配（正数）
	LedgerRevoke       LedgerEntryType = "revoke"        // 管理员扣回（负数）
	LedgerRedeem       LedgerEntryType = "redeem"        // 兑换码充值（正数）
	LedgerConsume      LedgerEntryType = "consume"       // 请求预扣（负数）
	LedgerRefund       LedgerEntryType = "refund"        // 请求失败退款（正数）
	LedgerSettleAdjust LedgerEntryType = "settle_adjust" // 结算差额（正=退还，负=补扣）
)

// CostCurrency 渠道成本币种（channel_costs.currency），定义见 docs/glossary.md 的 CostCurrency 节。
// credits：积分 / 1M tokens，按次计费为 积分 / 次；
// usd：微美元 / 1M tokens（1 美元 = 1,000,000 微美元），按次计费为 微美元 / 次。
type CostCurrency string

const (
	CostCurrencyCredits CostCurrency = "credits"
	CostCurrencyUSD     CostCurrency = "usd"
)

// CostCurrencies 是全部合法的渠道成本币种，单一事实源。
// 供启动期覆盖检查与入参校验（admin_channels 写入路径）引用，新增币种时同步追加。
var CostCurrencies = []CostCurrency{CostCurrencyCredits, CostCurrencyUSD}

// Valid 判断渠道成本币种取值是否合法。
func (c CostCurrency) Valid() bool {
	for _, v := range CostCurrencies {
		if c == v {
			return true
		}
	}
	return false
}

// RedemptionStatus 兑换码状态。前三个为存储态，落在 redemptions.status；
// RedemptionExpired 是由存储态与过期时间推导出的展示态，不入库——
// 过期不是一次状态变更，而是时间到点后的持续判定，写库会引入
// 「谁来改、什么时候改」的额外机制，且回改过期时间后无法回到未使用。
type RedemptionStatus string

const (
	RedemptionUnused   RedemptionStatus = "unused"
	RedemptionUsed     RedemptionStatus = "used"
	RedemptionDisabled RedemptionStatus = "disabled"
	RedemptionExpired  RedemptionStatus = "expired"
)

// EffectiveRedemptionStatus 返回展示态：未使用但已过期的码显示为已过期。
// 已核销与已禁用的码不受过期时间影响——那两个状态已经说明了它不可用的原因。
func EffectiveRedemptionStatus(stored RedemptionStatus, expiresAt *time.Time, now time.Time) RedemptionStatus {
	if stored == RedemptionUnused && expiresAt != nil && !expiresAt.After(now) {
		return RedemptionExpired
	}
	return stored
}

// Modality 模型形态。
type Modality string

const (
	ModalityText      Modality = "text"
	ModalityEmbedding Modality = "embedding"
	ModalityImage     Modality = "image"
)

// BillingMode 计费方式。
type BillingMode string

const (
	BillPerToken BillingMode = "per_token"
	BillPerCall  BillingMode = "per_call"
)

// ModelStatus 模型上下架状态。
type ModelStatus string

const (
	ModelEnabled  ModelStatus = "enabled"
	ModelDisabled ModelStatus = "disabled"
)

// UsageSemantic 上游 usage 字段的语义体系。
type UsageSemantic string

const (
	SemanticOpenAI    UsageSemantic = "openai"    // prompt_tokens 含缓存命中，需减去
	SemanticAnthropic UsageSemantic = "anthropic" // input_tokens 不含缓存，缓存独立字段
	SemanticGemini    UsageSemantic = "gemini"    // promptTokenCount 含缓存，需减去
)

// NormalizedUsage 是各协议 usage 归一化后的统一表示（计费唯一输入）。
// 各字段均为 token 数；按次计费时使用 CallCount。
type NormalizedUsage struct {
	BaseInput   int64 // 非缓存输入 token
	CacheRead   int64 // 缓存读取 token
	CacheWrite  int64 // 缓存写入 token
	Output      int64 // 输出 token
	AudioInput  int64 // 音频输入 token
	AudioOutput int64 // 音频输出 token
	CallCount   int64 // 按次计费的次数
	Estimated   bool  // usage 缺失时按字节估算的标记
	Semantic    UsageSemantic
}

// TotalTokens 返回全部 token 合计（日志展示用）。
func (u NormalizedUsage) TotalTokens() int64 {
	return u.BaseInput + u.CacheRead + u.CacheWrite + u.Output + u.AudioInput + u.AudioOutput
}

// MonthlyGrantMode 按月自动发放积分的口径。
type MonthlyGrantMode string

const (
	// MonthlyGrantTopUp 补足到额度：余额已达额度的账号本月不再发放，
	// 未用完的额度不累积到下月。
	MonthlyGrantTopUp MonthlyGrantMode = "topup"
	// MonthlyGrantAdd 增发固定额度：不看当前余额，未用完的部分累积。
	MonthlyGrantAdd MonthlyGrantMode = "add"
)

// Valid 判断按月发放口径是否受支持。
func (m MonthlyGrantMode) Valid() bool {
	return m == MonthlyGrantTopUp || m == MonthlyGrantAdd
}

// MonthlyGrantModes 是全部合法的按月发放口径。
var MonthlyGrantModes = []MonthlyGrantMode{MonthlyGrantTopUp, MonthlyGrantAdd}
