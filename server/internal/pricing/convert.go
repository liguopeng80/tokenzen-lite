package pricing

import "math/big"

// microUSDPerUSD 是美元官价的表示精度：1 美元 = 1,000,000 微美元。
// 厂商价目常见到小数点后 3 位（如 $0.075 / 1M tokens），用微美元整数表示可无损承载。
const microUSDPerUSD = 1_000_000

// rateMilliPerUnit 是美元兑人民币汇率的表示精度（千分数，7200 = 7.200）。
const rateMilliPerUnit = 1000

// percentPerUnit 是加价百分数的基准（100 = 按官价平价，不加价）。
const percentPerUnit = 100

// microUSDRatioDenom 是微美元金额折算积分的固定分母：
// 微美元 → 微人民币（×汇率千分数/1000）→ 积分（×兑换率/1e6），
// 合并分母 = microUSDPerUSD × rateMilliPerUnit = 1e9。
const microUSDRatioDenom = microUSDPerUSD * rateMilliPerUnit

// ceilMicroUSDRatio 把"微美元 × 汇率 × 兑换率（× 任意加价系数）"的连乘分子
// 按给定分母向上取整为积分。用大整数运算避免中间积溢出（$100 官价场景中间积约 1.4e20）。
// 取整方向与计费（CalcTokenCredits）一致：向上取整，运营方不因取整倒亏。
// 收入侧（模型上架）与成本侧（中继成本折算）共用本函数保证成本/收入同向取整。
func ceilMicroUSDRatio(num, den *big.Int) int64 {
	if num.Sign() <= 0 || den.Sign() <= 0 {
		return 0
	}
	quo, rem := new(big.Int).QuoRem(num, den, new(big.Int))
	if rem.Sign() != 0 {
		quo.Add(quo, big.NewInt(1))
	}
	if !quo.IsInt64() {
		// 折算结果超出 int64 只可能来自误填的极端入参；返回上限而非静默截断成小数值，
		// 使上架前的定价预览显示为异常大的数字，便于人工发现。
		return int64(^uint64(0) >> 1)
	}
	return quo.Int64()
}

// ConvertMicroUSDTToCredits 把微美元金额折算为积分（向上取整）。
// 供成本侧把美元记账的渠道成本折回积分。与收入侧 ConvertUSDToCredits 共用
// ceilMicroUSDRatio，保证两端口径一致——成本/收入报表取整同向，利润不失真。
//
// 计算式：microUSD / 1e6 × (usdCnyRateMilli / 1000) × creditsPerCNY，向上取整。
// 入参为零或负时返回 0（兼容未配置汇率的 0 值与零成本）。
func ConvertMicroUSDTToCredits(usdMicro, usdCnyRateMilli, creditsPerCNY int64) int64 {
	if usdMicro <= 0 || usdCnyRateMilli <= 0 || creditsPerCNY <= 0 {
		return 0
	}
	num := new(big.Int).SetInt64(usdMicro)
	num.Mul(num, big.NewInt(usdCnyRateMilli))
	num.Mul(num, big.NewInt(creditsPerCNY))
	return ceilMicroUSDRatio(num, big.NewInt(microUSDRatioDenom))
}

// ConvertUSDToCredits 把上游厂商的美元官价折算为本站积分单价。
//
// 参数含义：
//   - microUSD：厂商官价，微美元。token 类单价是"每 1M tokens 的价格"，
//     按次计费是"每次调用的价格"；两者折算方式相同，量纲随入参传递。
//   - usdCnyRateMilli：美元兑人民币汇率千分数（系统设置 usd_cny_rate_milli）。
//   - creditsPerCNY：1 人民币兑换的积分数（系统设置 exchange_rate_credits_per_cny）。
//   - markupPercent：加价百分数，100 表示按官价平价折算，130 表示在官价基础上加价 30%。
//
// 计算式：microUSD / 1e6 × (usdCnyRateMilli / 1000) × creditsPerCNY × (markupPercent / 100)，
// 向上取整。运营方不应因取整倒亏，故取整方向与计费一致，一律向上。
//
// 中间乘积会超出 int64（官价 100 美元、汇率 7.2、兑换率 1e6、加价 100% 时约 1.4e20），
// 因此这里用大整数运算。本函数只在模型上架与批量导入时调用，不在计费热路径上。
func ConvertUSDToCredits(microUSD, usdCnyRateMilli, creditsPerCNY int64, markupPercent int) int64 {
	if microUSD <= 0 || usdCnyRateMilli <= 0 || creditsPerCNY <= 0 || markupPercent <= 0 {
		return 0
	}
	num := new(big.Int).SetInt64(microUSD)
	num.Mul(num, big.NewInt(usdCnyRateMilli))
	num.Mul(num, big.NewInt(creditsPerCNY))
	num.Mul(num, big.NewInt(int64(markupPercent)))
	den := big.NewInt(microUSDRatioDenom * percentPerUnit) // 1e9 × 100
	return ceilMicroUSDRatio(num, den)
}
