// Package pricing 实现模型定价查询、时段倍率求值与积分计算。
// 计费为整数运算（用 math/big.Int 累计分子防 int64 中间积溢出，做法同 convert.go），
// 一次性向上取整，禁止浮点。
package pricing

import (
	"math"
	"math/big"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
)

// tokensPerUnit 是 token 类单价的计价单位（积分 / 1M tokens）。
const tokensPerUnit = 1_000_000

// BaseMultiplierPercent 表示无时段倍率时的基准（100 = 1.0 倍）。
const BaseMultiplierPercent = 100

// Price 是一个模型的直接单价集合（积分计价）。
type Price struct {
	InputPrice       int64
	OutputPrice      int64
	CacheReadPrice   int64
	CacheWritePrice  int64
	AudioInputPrice  int64
	AudioOutputPrice int64
	PerCallPrice     int64
}

// addMul 把 tokens×price 累加到 num；tokens 或 price 非正时无贡献（与乘 0 等价，跳过仅为避免无谓分配）。
func addMul(num *big.Int, tokens, price int64) {
	if tokens <= 0 || price <= 0 {
		return
	}
	t := new(big.Int).SetInt64(tokens)
	t.Mul(t, big.NewInt(price))
	num.Add(num, t)
}

// ceilBigDiv 返回 ceil(num/den)，并按以下规则收敛边界：
//   - num<=0 或 den<=0：返回 0（与旧 ceilDiv 的零用量短路一致）；
//   - 结果超出 int64 上限：返回 math.MaxInt64，让预扣自然失败于余额不足，
//     而非静默回 0 放行（旧实现的 int64 中间积溢出为负值后被 ceilDiv 的
//     a<=0 短路成 0，会绕过日上限、密钥额度、余额拒绝三道边界）。
func ceilBigDiv(num, den *big.Int) int64 {
	if num.Sign() <= 0 || den.Sign() <= 0 {
		return 0
	}
	quo, rem := new(big.Int).QuoRem(num, den, new(big.Int))
	if rem.Sign() != 0 {
		quo.Add(quo, big.NewInt(1))
	}
	if !quo.IsInt64() {
		return math.MaxInt64
	}
	return quo.Int64()
}

// CalcTokenCredits 按 token 用量计算积分消耗。
// multiplierPercent 为时段倍率百分数（100 = 无加成）。
func CalcTokenCredits(u domain.NormalizedUsage, p Price, multiplierPercent int) domain.Credits {
	if multiplierPercent < BaseMultiplierPercent {
		multiplierPercent = BaseMultiplierPercent
	}
	num := new(big.Int)
	addMul(num, u.BaseInput, p.InputPrice)
	addMul(num, u.CacheRead, p.CacheReadPrice)
	addMul(num, u.CacheWrite, p.CacheWritePrice)
	addMul(num, u.Output, p.OutputPrice)
	addMul(num, u.AudioInput, p.AudioInputPrice)
	addMul(num, u.AudioOutput, p.AudioOutputPrice)
	num.Mul(num, big.NewInt(int64(multiplierPercent)))
	den := big.NewInt(int64(BaseMultiplierPercent) * tokensPerUnit)
	return ceilBigDiv(num, den)
}

// CalcPerCallCredits 按次数计算积分消耗（图像生成等）。
// multiplierPercent 为时段倍率百分数（100 = 无加成）。
func CalcPerCallCredits(count int64, p Price, multiplierPercent int) domain.Credits {
	if multiplierPercent < BaseMultiplierPercent {
		multiplierPercent = BaseMultiplierPercent
	}
	num := new(big.Int)
	addMul(num, count, p.PerCallPrice)
	num.Mul(num, big.NewInt(int64(multiplierPercent)))
	den := big.NewInt(int64(BaseMultiplierPercent))
	return ceilBigDiv(num, den)
}
