package pricing

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sync"
)

//go:embed presets.json
var presetsRaw []byte

// PresetModel 是一条预置价目：厂商公开的美元官价，按 1M tokens 计（按次计费模型按每次调用计）。
// 单位为微美元，1 美元 = 1,000,000 微美元。零值表示厂商未对该项单独计价。
type PresetModel struct {
	Name           string   `json:"name"`
	DisplayName    string   `json:"display_name"`
	Description    string   `json:"description"`
	Modality       string   `json:"modality"`
	BillingMode    string   `json:"billing_mode"`
	InputUSD       int64    `json:"input_usd"`
	OutputUSD      int64    `json:"output_usd"`
	CacheReadUSD   int64    `json:"cache_read_usd"`
	CacheWriteUSD  int64    `json:"cache_write_usd"`
	PerCallUSD     int64    `json:"per_call_usd"`
	Provider       string   `json:"provider"`
	ContextWindow  int64    `json:"context_window"`
	MaxOutput      int64    `json:"max_output"`
	Capabilities   []string `json:"capabilities"`
	Alias          string   `json:"alias"`
	AudioInputUSD  int64    `json:"audio_input_usd"`
	AudioOutputUSD int64    `json:"audio_output_usd"`
}

// PresetProvider 是一个厂商的预置价目集合。
type PresetProvider struct {
	ID         string        `json:"id"`
	Name       string        `json:"name"`
	PricingURL string        `json:"pricing_url"`
	Models     []PresetModel `json:"models"`
}

// PresetCatalog 是全部预置价目。PricedAt 为价目采集月份，
// 厂商随时可能调价，导入前需对照 PricingURL 核对。
type PresetCatalog struct {
	PricedAt  string           `json:"priced_at"`
	Note      string           `json:"note"`
	Providers []PresetProvider `json:"providers"`
}

var (
	presetsOnce sync.Once
	presets     PresetCatalog
	presetsErr  error
)

// Presets 返回内置预置价目。解析失败时返回错误（编译期嵌入的文件损坏才会发生）。
func Presets() (PresetCatalog, error) {
	presetsOnce.Do(func() {
		if err := json.Unmarshal(presetsRaw, &presets); err != nil {
			presetsErr = fmt.Errorf("解析内置预置价目失败: %w", err)
		}
	})
	return presets, presetsErr
}

// ToCreditPrice 按给定汇率、兑换率与加价百分数，把一条预置价目折算为积分单价。
func (m PresetModel) ToCreditPrice(usdCnyRateMilli, creditsPerCNY int64, markupPercent int) Price {
	conv := func(v int64) int64 {
		return ConvertUSDToCredits(v, usdCnyRateMilli, creditsPerCNY, markupPercent)
	}
	return Price{
		InputPrice:       conv(m.InputUSD),
		OutputPrice:      conv(m.OutputUSD),
		CacheReadPrice:   conv(m.CacheReadUSD),
		CacheWritePrice:  conv(m.CacheWriteUSD),
		AudioInputPrice:  conv(m.AudioInputUSD),
		AudioOutputPrice: conv(m.AudioOutputUSD),
		PerCallPrice:     conv(m.PerCallUSD),
	}
}
