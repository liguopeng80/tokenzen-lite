package relay

// /{provider}/v1/* 前缀路由的 provider slug 归一与候选渠道过滤。
// provider 前缀锁定候选渠道的 channels.provider：同 provider 多渠道照常容错，
// 该 provider 全部渠道不可用时直接返回错误，不回退其他 provider。

import (
	"strings"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// providerSlugs 把 URL slug（含品牌名/产品名/厂商名等常见别名）归一到 domain.Provider。
// 与 packages/shared 的 ProviderCatalog 同源：新增厂商时两处同步。
// 未命中返回 false，路由层转 404 provider_not_found。
var providerSlugs = map[string]domain.Provider{
	"openai":    domain.ProviderOpenAI,
	"anthropic": domain.ProviderAnthropic,
	"gemini":    domain.ProviderGemini,
	"google":    domain.ProviderGemini,   // 品牌别名
	"glm":       domain.ProviderZhipu,    // 模型系列别名
	"zhipu":     domain.ProviderZhipu,    // 厂商名
	"chatglm":   domain.ProviderZhipu,    // 产品别名
	"kimi":      domain.ProviderMoonshot, // 产品别名
	"moonshot":  domain.ProviderMoonshot, // 厂商名
	"deepseek":  domain.ProviderDeepSeek,
	"qwen":      domain.ProviderQwen,
	"tongyi":    domain.ProviderQwen, // 品牌别名
	"minimax":   domain.ProviderMinimax,
	"xai":       domain.ProviderXAI,
	"grok":      domain.ProviderXAI, // 产品别名
	"custom":    domain.ProviderCustom,
}

// SlugToProvider 把 URL 前缀的 provider slug 解析为 domain.Provider。
// 输入做大小写归一（容忍 /Anthropic/v1/... 等写法）；未命中返回 (空值, false)。
func SlugToProvider(slug string) (domain.Provider, bool) {
	p, ok := providerSlugs[strings.ToLower(slug)]
	return p, ok
}

// filterByProvider 在候选渠道切片上按 provider 收窄。
// provider 为零值（空串）时直返原切片——对应无前缀的 /v1/* 入口，保持现有跨 provider 行为。
//
// 本函数是 provider 前缀路由「候选集定义」层的唯一收窄点：选择器 SelectChannel、
// 加权随机、错误分类、自动禁用、亲和（方案 C 节 selectWithAffinity）全部在过滤后的
// 切片上运行，感知不到 provider。未来渠道亲和（selectWithAffinity）在此切片内做选择。
func filterByProvider(channels []store.Channel, provider domain.Provider) []store.Channel {
	if provider == "" {
		return channels
	}
	out := make([]store.Channel, 0, len(channels))
	for i := range channels {
		if channels[i].Provider == provider {
			out = append(out, channels[i])
		}
	}
	return out
}
