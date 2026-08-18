package pricing

import "math/big"

// DetailDecimals 返回"单个积分"在给定兑换率下无损可见所需的最小小数位数。
// 口径：使 10^d ≥ creditsPerUnit 的最小 d，从而 1 积分 = 1/creditsPerUnit 在该精度下
// 至少落到末位（非零可见）。兑换率为 10 的幂（默认 1e6）时该精度即无损——
// creditsPerUnit=1e6 → 6，与"1 积分 = 1e-6 货币单位"一致。
// creditsPerUnit ≤ 1 时返回 0。
func DetailDecimals(creditsPerUnit int64) int {
	if creditsPerUnit <= 1 {
		return 0
	}
	d := 0
	for p := int64(1); p < creditsPerUnit; p *= 10 {
		d++
	}
	return d
}

// CreditsToDecimalString 把积分按"每货币单位 creditsPerUnit 积分"换算为定点小数字符串，
// 保留 decimals 位小数（四舍五入），不含货币符号。供 CSV 导出等需要裸数值的场景：
// 默认兑换率（1e6）下 decimals=6 为无损，逐行金额可精确汇总。
//
// 入参 creditsPerUnit ≤ 0 或 decimals < 0 时返回 "0"。
// 用大整数运算，避免 credits × 10^decimals 超出 int64 时的中间积溢出。
func CreditsToDecimalString(credits, creditsPerUnit int64, decimals int) string {
	if creditsPerUnit <= 0 || decimals < 0 {
		return "0"
	}
	neg := credits < 0
	if neg {
		credits = -credits
	}
	num := new(big.Int).SetInt64(credits)
	if decimals > 0 {
		num.Mul(num, pow10BigInt(decimals))
	}
	den := new(big.Int).SetInt64(creditsPerUnit)
	q, r := new(big.Int).QuoRem(num, den, new(big.Int))
	// 四舍五入：余数 ×2 ≥ 分母则进位。
	if new(big.Int).Mul(r, big.NewInt(2)).Cmp(den) >= 0 {
		q.Add(q, big.NewInt(1))
	}
	s := q.String()
	if decimals == 0 {
		if neg {
			return "-" + s
		}
		return s
	}
	// 补前导零至至少 decimals+1 位（保证至少 1 位整数 + decimals 位小数）。
	for len(s) <= decimals {
		s = "0" + s
	}
	out := s[:len(s)-decimals] + "." + s[len(s)-decimals:]
	if neg {
		return "-" + out
	}
	return out
}

// pow10BigInt 返回 10^n（n ≥ 0）。
func pow10BigInt(n int) *big.Int {
	p := big.NewInt(1)
	ten := big.NewInt(10)
	for i := 0; i < n; i++ {
		p.Mul(p, ten)
	}
	return p
}
