package api

import (
	"net/http/httptest"
	"testing"
)

// 覆盖 P2-14：统一分页解析 PageParams 的边界行为。
// 纯函数测试，不依赖数据库。

func TestPageParamsBoundary(t *testing.T) {
	cases := []struct {
		name     string
		query    string
		wantPage int
		wantSize int
	}{
		{"参数缺省回落默认值", "", 1, 20},
		{"页码 0 归一为 1", "?page=0&page_size=10", 1, 10},
		{"页码负数归一为 1", "?page=-3&page_size=10", 1, 10},
		{"页码非数字回落为 1", "?page=abc&page_size=10", 1, 10},
		{"每页条数 0 回落默认值", "?page=2&page_size=0", 2, 20},
		{"每页条数负数回落默认值", "?page=2&page_size=-5", 2, 20},
		{"每页条数非数字回落默认值", "?page=2&page_size=xyz", 2, 20},
		{"每页条数上边界 100 保留", "?page=1&page_size=100", 1, 100},
		{"每页条数 101 截断为 100", "?page=1&page_size=101", 1, 100},
		{"每页条数 999 截断为 100", "?page=1&page_size=999", 1, 100},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/api/admin/users/"+tc.query, nil)
			page, pageSize := PageParams(r)
			if page != tc.wantPage || pageSize != tc.wantSize {
				t.Errorf("PageParams(%q) = (%d, %d)，期望 (%d, %d)",
					tc.query, page, pageSize, tc.wantPage, tc.wantSize)
			}
		})
	}
}
