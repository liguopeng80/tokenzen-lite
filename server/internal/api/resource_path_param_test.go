package api

// 路径参数非法与资源不存在的表驱动用例（报告 P3-19）：模型、渠道、兑换码、用户
// 四类资源各补一组固定用例——路径标识传非数字、0、负数断言 400，传不存在的正整数
// 断言 404 且提示文案对应。辅助函数以路径模板、HTTP 方法、请求体为输入，
// 新增资源时只需追加一条 resourcePathParamCase。

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
)

// resourcePathParamCase 描述一个带 {id} 路径参数的端点。
type resourcePathParamCase struct {
	resource    string // 资源名，用于子测试命名
	method      string
	pathFor     func(idSeg string) string // 按原始路径段（可能非数字）拼出完整路径
	body        any
	notFoundID  int64  // 一个必然不存在的合法正整数 id
	notFoundMsg string // 404 响应期望的 message（精确匹配）
}

// runResourcePathParamCase 执行单条资源用例：非数字/0/负数三种非法 id 断言 400，
// 不存在的正整数 id 断言 404 且提示文案对应。
func runResourcePathParamCase(t *testing.T, e *testEnv, c *http.Client, tc resourcePathParamCase) {
	t.Helper()
	t.Run(tc.resource, func(t *testing.T) {
		for _, bad := range []string{"abc", "0", "-1"} {
			t.Run("路径参数非法_"+bad, func(t *testing.T) {
				resp, env := e.do(t, c, tc.method, tc.pathFor(bad), tc.body)
				if resp.StatusCode != http.StatusBadRequest {
					t.Errorf("id=%q 应 400，实际 %d，响应: %v", bad, resp.StatusCode, env)
				}
			})
		}
		t.Run("资源不存在", func(t *testing.T) {
			resp, env := e.do(t, c, tc.method, tc.pathFor(fmt.Sprint(tc.notFoundID)), tc.body)
			if resp.StatusCode != http.StatusNotFound {
				t.Fatalf("不存在的资源应 404，实际 %d，响应: %v", resp.StatusCode, env)
			}
			if msg, _ := env["message"].(string); msg != tc.notFoundMsg {
				t.Errorf("404 提示文案应为 %q，实际 %q", tc.notFoundMsg, msg)
			}
		})
	})
}

// TestResourcePathParamMatrix 覆盖模型、渠道、兑换码、用户四类资源的路径参数边界。
func TestResourcePathParamMatrix(t *testing.T) {
	e := newTestEnv(t)
	rootC := e.seedAndLogin(t, "root-pathparam", domain.RoleRoot)

	const notFoundID = 999999

	cases := []resourcePathParamCase{
		{
			resource:   "模型",
			method:     "GET",
			pathFor:    func(id string) string { return "/api/admin/models/" + id },
			notFoundID: notFoundID, notFoundMsg: "模型不存在",
		},
		{
			resource:   "渠道",
			method:     "GET",
			pathFor:    func(id string) string { return "/api/admin/channels/" + id },
			notFoundID: notFoundID, notFoundMsg: "渠道不存在",
		},
		{
			resource:   "兑换码",
			method:     "PUT",
			pathFor:    func(id string) string { return "/api/admin/redemptions/" + id + "/status" },
			body:       map[string]string{"status": "disabled"},
			notFoundID: notFoundID, notFoundMsg: "兑换码不存在或已被使用",
		},
		{
			resource:   "用户",
			method:     "GET",
			pathFor:    func(id string) string { return "/api/admin/users/" + id },
			notFoundID: notFoundID, notFoundMsg: "用户不存在",
		},
	}
	for _, tc := range cases {
		runResourcePathParamCase(t, e, rootC, tc)
	}
}

// TestResourcePathParamMatrixWriteEndpoints 补充写操作端点（PUT/DELETE）的同类边界，
// 与只读 GET 端点共用同一份 IDParam/404 逻辑，但需要各自独立的种子资源与请求体。
func TestResourcePathParamMatrixWriteEndpoints(t *testing.T) {
	e := newTestEnv(t)
	rootC := e.seedAndLogin(t, "root-pathparam2", domain.RoleRoot)
	const notFoundID = 999999

	cases := []resourcePathParamCase{
		{
			resource: "模型_更新",
			method:   "PUT",
			pathFor:  func(id string) string { return "/api/admin/models/" + id },
			body: map[string]any{"name": "whatever", "modality": "text",
				"billing_mode": "per_token", "status": "enabled"},
			notFoundID: notFoundID, notFoundMsg: "模型不存在",
		},
		{
			resource:   "模型_删除",
			method:     "DELETE",
			pathFor:    func(id string) string { return "/api/admin/models/" + id },
			notFoundID: notFoundID, notFoundMsg: "模型不存在",
		},
		{
			resource:   "渠道_删除",
			method:     "DELETE",
			pathFor:    func(id string) string { return "/api/admin/channels/" + id },
			notFoundID: notFoundID, notFoundMsg: "渠道不存在",
		},
		{
			resource:   "用户_删除",
			method:     "DELETE",
			pathFor:    func(id string) string { return "/api/admin/users/" + id },
			notFoundID: notFoundID, notFoundMsg: "用户不存在",
		},
	}
	for _, tc := range cases {
		runResourcePathParamCase(t, e, rootC, tc)
	}
}
