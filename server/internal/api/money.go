package api

import (
	"github.com/liguopeng80/tokenzen-lite/server/internal/pricing"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// moneyCtx 把"积分 → 货币定点字符串"的换算参数打包，供处理器构造响应时为每个
// 积分整数字段旁置一个同名 _money 裸小数串。
//
// 设计要点：
//   - 默认兑换率 1e6 → 6 位小数（无损），逐行可精确汇总；
//   - 货币串不含符号（程序化消费，符号为元数据，见 /api/site/config 的 currency_symbol）；
//   - 纯增量：原有积分整数字段全部保留，仅新增 _money 字段，无破坏性变更。
type moneyCtx struct {
	rate     int64
	decimals int
}

// newMoneyCtx 由"每货币单位的积分数"（系统设置 exchange_rate_credits_per_cny）构造换算上下文。
func newMoneyCtx(creditsPerUnit int64) moneyCtx {
	return moneyCtx{rate: creditsPerUnit, decimals: pricing.DetailDecimals(creditsPerUnit)}
}

// money 把积分换算为货币定点字符串（裸数字，无符号）。
func (m moneyCtx) money(credits int64) string {
	return pricing.CreditsToDecimalString(credits, m.rate, m.decimals)
}

// wrapList 把 []T 映射为 []W（逐项经 fn），避免每个处理器手写循环。
func wrapList[T any, W any](items []T, fn func(T) W) []W {
	out := make([]W, len(items))
	for i, v := range items {
		out[i] = fn(v)
	}
	return out
}

// ---- 跨控制器共享的包装类型 ----
// 这些 store 结构体被 admin 与 /me 两侧的多个处理器返回，集中定义避免重复与命名漂移。
// 控制器专属的包装类型放在各自处理器文件内。

// ledgerEntryWithMoney 包装 store.LedgerEntry，旁置 amount/balance_after 的货币串。
type ledgerEntryWithMoney struct {
	store.LedgerEntry
	AmountMoney       string `json:"amount_money"`
	BalanceAfterMoney string `json:"balance_after_money"`
}

func wrapLedgerEntry(e store.LedgerEntry, mc moneyCtx) ledgerEntryWithMoney {
	return ledgerEntryWithMoney{
		LedgerEntry:       e,
		AmountMoney:       mc.money(e.Amount),
		BalanceAfterMoney: mc.money(e.BalanceAfter),
	}
}

// apiKeyWithMoney 包装 store.APIKey，旁置额度/已用/每日上限的货币串。
// CreditLimit 为指针，nil 表示不限，货币串留空。
type apiKeyWithMoney struct {
	store.APIKey
	CreditLimitMoney     string `json:"credit_limit_money"`
	CreditUsedMoney      string `json:"credit_used_money"`
	DailySpendLimitMoney string `json:"daily_spend_limit_money"`
}

func wrapAPIKey(k store.APIKey, mc moneyCtx) apiKeyWithMoney {
	limitMoney := ""
	if k.CreditLimit != nil {
		limitMoney = mc.money(*k.CreditLimit)
	}
	return apiKeyWithMoney{
		APIKey:               k,
		CreditLimitMoney:     limitMoney,
		CreditUsedMoney:      mc.money(k.CreditUsed),
		DailySpendLimitMoney: mc.money(k.DailySpendLimit),
	}
}

// dailyStatWithMoney 包装 store.DailyStat，旁置扣费/成本的货币串。
type dailyStatWithMoney struct {
	store.DailyStat
	CreditsChargedMoney string `json:"credits_charged_money"`
	CreditsCostMoney    string `json:"credits_cost_money"`
}

func wrapDailyStat(s store.DailyStat, mc moneyCtx) dailyStatWithMoney {
	return dailyStatWithMoney{
		DailyStat:           s,
		CreditsChargedMoney: mc.money(s.CreditsCharged),
		CreditsCostMoney:    mc.money(s.CreditsCost),
	}
}

// heatmapCellWithMoney 包装 store.HeatmapCell，旁置扣费的货币串。
// 字段名用 credits_charged_money 与 store 的 JSON 字段 credits_charged 对齐。
type heatmapCellWithMoney struct {
	store.HeatmapCell
	CreditsMoney string `json:"credits_charged_money"`
}

func wrapHeatmapCell(c store.HeatmapCell, mc moneyCtx) heatmapCellWithMoney {
	return heatmapCellWithMoney{HeatmapCell: c, CreditsMoney: mc.money(c.Credits)}
}
