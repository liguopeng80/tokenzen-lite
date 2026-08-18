package api

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
)

// getRaw 发起一次不解析响应信封的请求，返回状态码与响应体原文。
// /metrics 输出的是 Prometheus 文本格式，不套用 {success, message, data}。
func (e *testEnv) getRaw(t *testing.T, c *http.Client, path string, headers map[string]string) (int, string) {
	t.Helper()
	req, err := http.NewRequest("GET", e.srv.URL+path, nil)
	if err != nil {
		t.Fatalf("构建请求失败: %v", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if c == nil {
		c = &http.Client{}
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("请求 %s 失败: %v", path, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

// 指标含模型名、渠道 ID 与各接口调用量，属内部运营信息：匿名与普通用户、
// 管理员一律拒绝，只有 root 会话或配置的令牌可读。
func TestMetricsRequiresRootOrToken(t *testing.T) {
	e := newTestEnv(t)

	if code, _ := e.getRaw(t, nil, "/metrics", nil); code != http.StatusUnauthorized {
		t.Errorf("匿名访问 /metrics 应 401，实际 %d", code)
	}
	userC := e.seedAndLogin(t, "metricsuser", domain.RoleUser)
	if code, _ := e.getRaw(t, userC, "/metrics", nil); code != http.StatusUnauthorized {
		t.Errorf("普通用户访问 /metrics 应 401，实际 %d", code)
	}
	adminC := e.seedAndLogin(t, "metricsadmin", domain.RoleAdmin)
	if code, _ := e.getRaw(t, adminC, "/metrics", nil); code != http.StatusUnauthorized {
		t.Errorf("管理员访问 /metrics 应 401，实际 %d", code)
	}

	rootC := e.seedAndLogin(t, "metricsroot", domain.RoleRoot)
	code, body := e.getRaw(t, rootC, "/metrics", nil)
	if code != http.StatusOK {
		t.Fatalf("root 会话访问 /metrics 应 200，实际 %d", code)
	}
	// 请求量指标由访问日志中间件记录，因此本次测试自身的请求已经计入。
	if !strings.Contains(body, "tzl_http_requests_total") {
		t.Errorf("导出内容应含 HTTP 请求计数：\n%s", body)
	}
}

// 配置了访问令牌后，抓取端可用 Bearer 令牌读取；令牌不匹配仍拒绝。
func TestMetricsAcceptsConfiguredToken(t *testing.T) {
	e := newTestEnv(t)
	e.deps.Cfg.MetricsToken = "scrape-token-1"

	if code, _ := e.getRaw(t, nil, "/metrics",
		map[string]string{"Authorization": "Bearer wrong-token"}); code != http.StatusUnauthorized {
		t.Errorf("令牌不匹配应 401，实际 %d", code)
	}
	code, body := e.getRaw(t, nil, "/metrics",
		map[string]string{"Authorization": "Bearer scrape-token-1"})
	if code != http.StatusOK {
		t.Fatalf("令牌匹配应 200，实际 %d", code)
	}
	if !strings.Contains(body, "# TYPE") {
		t.Errorf("导出内容应为 Prometheus 文本格式：\n%s", body)
	}
}
