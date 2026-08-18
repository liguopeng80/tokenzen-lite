package relay

// provider 前缀路由的纯逻辑单测：slug → domain.Provider 归一、候选切片过滤。
// 不连数据库，可在不设置 TZL_TEST_DATABASE_URL 时运行。
// DB 集成场景见 provider_route_integration_test.go（由主会话串行运行）。

import (
	"testing"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// SlugToProvider：全部常见别名（品牌/产品/厂商）归一到 domain.Provider。
// 收全部常见别名是 CEO 关注点（kimi/glm），任何常见写法都应命中同一 provider。
func TestSlugToProviderAliases(t *testing.T) {
	cases := []struct {
		slug string
		want domain.Provider
	}{
		{"openai", domain.ProviderOpenAI},
		{"anthropic", domain.ProviderAnthropic},
		{"gemini", domain.ProviderGemini},
		{"google", domain.ProviderGemini}, // 品牌别名
		{"glm", domain.ProviderZhipu},     // 模型系列别名
		{"zhipu", domain.ProviderZhipu},   // 厂商名
		{"chatglm", domain.ProviderZhipu}, // 产品别名
		{"kimi", domain.ProviderMoonshot}, // 产品别名
		{"moonshot", domain.ProviderMoonshot},
		{"deepseek", domain.ProviderDeepSeek},
		{"qwen", domain.ProviderQwen},
		{"tongyi", domain.ProviderQwen}, // 品牌别名
		{"minimax", domain.ProviderMinimax},
		{"xai", domain.ProviderXAI},
		{"grok", domain.ProviderXAI}, // 产品别名
		{"custom", domain.ProviderCustom},
	}
	for _, c := range cases {
		t.Run(c.slug, func(t *testing.T) {
			got, ok := SlugToProvider(c.slug)
			if !ok {
				t.Fatalf("SlugToProvider(%q) 未命中，期望 %s", c.slug, c.want)
			}
			if got != c.want {
				t.Fatalf("SlugToProvider(%q) = %s，期望 %s", c.slug, got, c.want)
			}
		})
	}
}

// 大小写归一：容忍 /Anthropic/v1/... 等写法。
func TestSlugToProviderCaseInsensitive(t *testing.T) {
	for _, slug := range []string{"Anthropic", "OPENAI", "Kimi", "GLM", "DeepSeek"} {
		if _, ok := SlugToProvider(slug); !ok {
			t.Fatalf("SlugToProvider(%q) 应在大小写归一后命中", slug)
		}
	}
}

// 未命中的 slug 返回 (空, false)——路由层据此返回 404 provider_not_found。
func TestSlugToProviderUnknownSlug(t *testing.T) {
	for _, slug := range []string{"unknown", "", "claude", "gpt", "openai-compat", "xyz"} {
		if _, ok := SlugToProvider(slug); ok {
			t.Fatalf("SlugToProvider(%q) 不应命中任何 provider", slug)
		}
	}
}

// filterByProvider：provider 为零值时直返原切片（/v1/* 跨 provider 行为不变）。
func TestFilterByProviderZeroValueReturnsAll(t *testing.T) {
	channels := []store.Channel{
		{ID: 1, Provider: domain.ProviderOpenAI},
		{ID: 2, Provider: domain.ProviderAnthropic},
		{ID: 3, Provider: domain.ProviderZhipu},
	}
	got := filterByProvider(channels, "")
	if len(got) != len(channels) {
		t.Fatalf("零值 provider 应直返原切片，期望 %d 条，实际 %d 条", len(channels), len(got))
	}
	for i := range channels {
		if got[i].ID != channels[i].ID {
			t.Fatalf("零值 provider 直返应保持顺序与元素，位置 %d 期望 ID=%d 实际 ID=%d",
				i, channels[i].ID, got[i].ID)
		}
	}
}

// filterByProvider：非零值时仅保留同 provider 渠道，剔除其余。
func TestFilterByProviderNarrowsToSameProvider(t *testing.T) {
	channels := []store.Channel{
		{ID: 1, Provider: domain.ProviderAnthropic},
		{ID: 2, Provider: domain.ProviderOpenAI},
		{ID: 3, Provider: domain.ProviderAnthropic},
		{ID: 4, Provider: domain.ProviderZhipu},
	}
	got := filterByProvider(channels, domain.ProviderAnthropic)
	if len(got) != 2 {
		t.Fatalf("期望 2 条 anthropic 渠道，实际 %d 条", len(got))
	}
	for _, ch := range got {
		if ch.Provider != domain.ProviderAnthropic {
			t.Fatalf("过滤后存在非 anthropic 渠道: %s", ch.Provider)
		}
	}
}

// filterByProvider：该 provider 无任何渠道时返回空切片（调用方据此返回 no_channel，不回退其他 provider）。
func TestFilterByProviderNoMatchReturnsEmpty(t *testing.T) {
	channels := []store.Channel{
		{ID: 1, Provider: domain.ProviderOpenAI},
		{ID: 2, Provider: domain.ProviderZhipu},
	}
	got := filterByProvider(channels, domain.ProviderAnthropic)
	if len(got) != 0 {
		t.Fatalf("无匹配 provider 时应返回空切片，实际 %d 条", len(got))
	}
}

// filterByProvider：不修改原切片（不可变约定，coding-style.md Immutability）。
func TestFilterByProviderDoesNotMutateInput(t *testing.T) {
	channels := []store.Channel{
		{ID: 1, Provider: domain.ProviderOpenAI},
		{ID: 2, Provider: domain.ProviderAnthropic},
	}
	_ = filterByProvider(channels, domain.ProviderAnthropic)
	if channels[0].Provider != domain.ProviderOpenAI || channels[1].Provider != domain.ProviderAnthropic {
		t.Fatalf("filterByProvider 不应修改原切片")
	}
	if len(channels) != 2 {
		t.Fatalf("filterByProvider 不应改变原切片长度")
	}
}
