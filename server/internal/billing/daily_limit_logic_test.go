package billing

import (
	"testing"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
)

// TestProjectedDailySpendExceeds 覆盖用户级与 Key 级每日上限共用的阈值决策纯函数。
// 不依赖数据库，可独立运行。边界：未达上限放行、恰好达上限放行、超出拒绝、
// 0=不限、负数（迁移 CHECK 与入参校验已在上游拒绝，此处仅作防御性兜底，视同不限）。
func TestProjectedDailySpendExceeds(t *testing.T) {
	const limit = domain.Credits(5_000)
	cases := []struct {
		name  string
		spent domain.Credits
		delta domain.Credits
		limit domain.Credits
		want  bool
	}{
		{"未达上限放行", 1_000, 1_000, limit, false},
		{"恰好达上限放行（projected==limit）", 3_000, 2_000, limit, false},
		{"超出拒绝（projected>limit）", 4_000, 2_000, limit, true},
		{"零计数恰好达上限放行", 0, 5_000, limit, false},
		{"零计数超出拒绝", 0, 5_001, limit, true},
		{"上限 0 表示不限", 999_999, 999_999, 0, false},
		{"负数 limit 视同不限（防御性，上游已拒）", 999_999, 999_999, -1, false},
		{"delta 为 0 恒不超", 5_000, 0, limit, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ProjectedDailySpendExceeds(tc.spent, tc.delta, tc.limit); got != tc.want {
				t.Errorf("ProjectedDailySpendExceeds(%d, %d, %d) = %v, want %v",
					tc.spent, tc.delta, tc.limit, got, tc.want)
			}
		})
	}
}
