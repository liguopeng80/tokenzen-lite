// Package relay 实现 /v1 中继核心：渠道选择、协议适配、SSE 代理、
// 预扣→结算→退款计费闭环。
package relay

import (
	"strings"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
)

// balanceKeywords 判定上游欠费的明确余额类文案（小写匹配）。
// 仅在非 429 的 4xx 响应体中生效：429 一律归为限流，见 ClassifyUpstreamError。
// 下面两条来自真实生产语料取证（~/copilot/llm-proxy/data/proxy.db
// 的 4xx response_body）：
//   - "该令牌额度已用尽"：令牌额度耗尽场景的标准中文文案。
//   - "token quota is not enough"：令牌剩余额度不足时的标准英文文案。
var balanceKeywords = []string{
	"insufficient balance",
	"insufficient funds",
	"insufficient_quota",
	"token quota is not enough",
	"余额不足",
	"欠费",
	"账户余额",
	"额度已用尽",
}

// ClassifyUpstreamError 按上游状态码与响应体分类错误。
// 分类驱动两个决策：是否计入渠道连续失败计数（达到阈值后自动禁用）、是否换渠道重试。
// 口径（2026-08-05 决策 1）：429 一律归为上游限流，不做文本细分；
// 上游欠费只认 402 状态码及响应体中明确的余额类文案。
func ClassifyUpstreamError(status int, body []byte) domain.ErrorClass {
	lower := strings.ToLower(string(body))
	switch {
	case status == 401 || status == 403:
		return domain.ErrClassAuthFatal
	case status == 402:
		return domain.ErrClassQuotaFatal
	case status == 429:
		// 部分厂商的 429 报文含额度类字样，但瞬时限速与欠费无法从文本可靠区分；
		// 一律按限流处理：换渠道重试，不计入禁用判定。
		return domain.ErrClassRateLimited
	case status >= 500:
		return domain.ErrClassTransient
	case status >= 400:
		if strings.Contains(lower, "invalid_api_key") || strings.Contains(lower, "account_deactivated") {
			return domain.ErrClassAuthFatal
		}
		if containsBalanceKeyword(lower) {
			return domain.ErrClassQuotaFatal
		}
		return domain.ErrClassBadRequest
	default:
		return domain.ErrClassNone
	}
}

// containsBalanceKeyword 判断小写化响应体是否含明确的余额类文案。
func containsBalanceKeyword(lower string) bool {
	for _, kw := range balanceKeywords {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

// CountsTowardAutoDisable 判断错误类是否计入渠道连续失败计数。
// 连续失败达到 channel_disable_failure_threshold 阈值时渠道被自动禁用，
// 之后由定时半开探测恢复（见 health.go）。
func CountsTowardAutoDisable(class domain.ErrorClass) bool {
	return class == domain.ErrClassAuthFatal || class == domain.ErrClassQuotaFatal
}

// ShouldRetryNextChannel 判断错误类是否应换渠道重试。
func ShouldRetryNextChannel(class domain.ErrorClass) bool {
	switch class {
	case domain.ErrClassAuthFatal, domain.ErrClassQuotaFatal,
		domain.ErrClassRateLimited, domain.ErrClassTransient:
		return true
	default:
		return false
	}
}
