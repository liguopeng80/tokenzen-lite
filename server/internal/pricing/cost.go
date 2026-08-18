package pricing

// 本文件承载成本侧的组合纯函数（C3 设计：computeCost 下沉）。
// store.ChannelCost 字段与 Price 一一对应，但 pricing 不引入 GORM 依赖，
// 因此只接受已从实体转好的 Price 入参；IO（Costs.Get + Settings 读汇率）留调用方。
// 取整方向与计费（CalcTokenCredits / ConvertUSDToCredits）一致：向上取整，
// 运营方不因取整倒亏，且成本/收入报表同向取整，利润不失真。

import "github.com/liguopeng80/tokenzen-lite/server/internal/domain"

// ComputeCostCredits 把渠道成本单价按用量折算为成本积分。
//
// 行为（与原 relay.computeCost 内联实现等价）：
//   - token 类（usage.CallCount == 0）走 CalcTokenCredits；
//   - 按次类（usage.CallCount > 0）走 CalcPerCallCredits；
//   - 均按 BaseMultiplierPercent（成本侧无时段倍率）计；
//   - currency == domain.CostCurrencyUSD 时叠加 ConvertMicroUSDTToCredits，
//     把微美元记账的成本按汇率折回积分（与收入侧同向取整）。
//
// 纯函数：无 I/O、无状态。currency 非法值按 credits 币种处理（不折算）。
func ComputeCostCredits(usage domain.NormalizedUsage, p Price, currency string,
	usdCnyRateMilli, creditsPerCNY int64) domain.Credits {

	var credits int64
	if usage.CallCount > 0 {
		credits = CalcPerCallCredits(usage.CallCount, p, BaseMultiplierPercent)
	} else {
		credits = CalcTokenCredits(usage, p, BaseMultiplierPercent)
	}
	if currency == string(domain.CostCurrencyUSD) {
		credits = ConvertMicroUSDTToCredits(credits, usdCnyRateMilli, creditsPerCNY)
	}
	return domain.Credits(credits)
}
