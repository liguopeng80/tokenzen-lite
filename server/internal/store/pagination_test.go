package store

import "testing"

// 覆盖 P2-14：仓储层分页兜底 NormalizePagination 的边界行为。
// 纯函数测试，不依赖数据库。

func TestNormalizePaginationBoundary(t *testing.T) {
	cases := []struct {
		name     string
		page     int
		pageSize int
		wantPage int
		wantSize int
	}{
		{"合法参数原样保留", 2, 50, 2, 50},
		{"页码 0 归一为 1", 0, 10, 1, 10},
		{"页码负数归一为 1", -3, 10, 1, 10},
		{"每页条数 0 回落默认值", 1, 0, 1, DefaultPageSize},
		{"每页条数负数回落默认值", 1, -5, 1, DefaultPageSize},
		{"每页条数上边界 100 保留", 1, 100, 1, MaxPageSize},
		{"每页条数 101 截断为 100", 1, 101, 1, MaxPageSize},
		{"每页条数 999 截断为 100", 1, 999, 1, MaxPageSize},
		{"零值双参数全部回落", 0, 0, 1, DefaultPageSize},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			page, pageSize := NormalizePagination(tc.page, tc.pageSize)
			if page != tc.wantPage || pageSize != tc.wantSize {
				t.Errorf("NormalizePagination(%d, %d) = (%d, %d)，期望 (%d, %d)",
					tc.page, tc.pageSize, page, pageSize, tc.wantPage, tc.wantSize)
			}
		})
	}
}
