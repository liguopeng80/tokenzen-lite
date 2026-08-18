package api

import (
	"testing"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
)

// 覆盖 P2-14：密钥列表、积分流水、用户列表、用量日志四个列表端点的
// 分页边界——每页条数超限截断为 100、页码 0/负数归一为 1、非数字回落默认值。
// 断言以响应信封中回显的 page/page_size 为准，端到端验证统一解析生效。
func TestListPaginationBoundary(t *testing.T) {
	e := newTestEnv(t)
	adminC := e.seedAndLogin(t, "pageadmin", domain.RoleAdmin)

	endpoints := []struct {
		name string
		path string
	}{
		{"密钥列表", "/api/me/keys/"},
		{"积分流水", "/api/admin/ledger"},
		{"用户列表", "/api/admin/users/"},
		{"用量日志", "/api/admin/usage-logs"},
	}
	cases := []struct {
		name     string
		query    string
		wantPage int
		wantSize int
	}{
		{"每页条数超限截断为 100", "?page=1&page_size=999", 1, 100},
		{"页码 0 归一为 1", "?page=0&page_size=10", 1, 10},
		{"页码负数归一为 1", "?page=-2&page_size=10", 1, 10},
		{"非数字回落默认值", "?page=abc&page_size=xyz", 1, 20},
	}
	for _, ep := range endpoints {
		for _, tc := range cases {
			t.Run(ep.name+"/"+tc.name, func(t *testing.T) {
				resp, env := e.do(t, adminC, "GET", ep.path+tc.query, nil)
				if resp.StatusCode != 200 {
					t.Fatalf("请求应 200，实际 %d，响应: %v", resp.StatusCode, env)
				}
				page, ok := env["data"].(map[string]any)
				if !ok {
					t.Fatalf("响应 data 应为分页信封，实际: %v", env)
				}
				if got := int(page["page"].(float64)); got != tc.wantPage {
					t.Errorf("page 应为 %d，实际 %d", tc.wantPage, got)
				}
				if got := int(page["page_size"].(float64)); got != tc.wantSize {
					t.Errorf("page_size 应为 %d，实际 %d", tc.wantSize, got)
				}
			})
		}
	}
}
