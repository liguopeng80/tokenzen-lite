package api

import (
	"strings"
	"testing"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// U2：validatePriceForBillingMode 表驱动——定价与计费方式一致性校验。
// 校验写反会让模型带着全零单价被零扣费调用，因此逐项验证每个分支。
func TestValidatePriceForBillingMode(t *testing.T) {
	t.Run("per_token 全零 token 单价返回错误", func(t *testing.T) {
		// 仅配了按次单价、token 六项全零：应报错
		msg := validatePriceForBillingMode(domain.BillPerToken,
			&store.ModelPrice{PerCallPrice: 40_000})
		if !strings.Contains(msg, "至少需要一项非零的 token 单价") {
			t.Errorf("应提示至少一项非零 token 单价，实际 %q", msg)
		}
	})

	// per_token：六项 token 单价逐项单独非零，均应通过
	tokenCases := []struct {
		name string
		p    store.ModelPrice
	}{
		{"input 非零", store.ModelPrice{InputPrice: 1}},
		{"output 非零", store.ModelPrice{OutputPrice: 1}},
		{"cache_read 非零", store.ModelPrice{CacheReadPrice: 1}},
		{"cache_write 非零", store.ModelPrice{CacheWritePrice: 1}},
		{"audio_input 非零", store.ModelPrice{AudioInputPrice: 1}},
		{"audio_output 非零", store.ModelPrice{AudioOutputPrice: 1}},
	}
	for _, c := range tokenCases {
		t.Run("per_token 单项 "+c.name+" 通过", func(t *testing.T) {
			p := c.p
			if msg := validatePriceForBillingMode(domain.BillPerToken, &p); msg != "" {
				t.Errorf("单项非零应通过，实际报错 %q", msg)
			}
		})
	}

	t.Run("per_call 按次单价为零返回错误", func(t *testing.T) {
		msg := validatePriceForBillingMode(domain.BillPerCall,
			&store.ModelPrice{InputPrice: 1_000_000})
		if !strings.Contains(msg, "非零的按次单价") {
			t.Errorf("应提示非零按次单价，实际 %q", msg)
		}
	})
	t.Run("per_call 按次单价为负返回错误", func(t *testing.T) {
		msg := validatePriceForBillingMode(domain.BillPerCall,
			&store.ModelPrice{PerCallPrice: -1})
		if !strings.Contains(msg, "非零的按次单价") {
			t.Errorf("负按次单价应报错，实际 %q", msg)
		}
	})
	t.Run("per_call 按次单价为正通过", func(t *testing.T) {
		if msg := validatePriceForBillingMode(domain.BillPerCall,
			&store.ModelPrice{PerCallPrice: 40_000}); msg != "" {
			t.Errorf("合法按次单价应通过，实际报错 %q", msg)
		}
	})
}
