package api

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"gorm.io/datatypes"

	"github.com/liguopeng80/tokenzen-lite/server/internal/auth"
	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// 越权矩阵（P2-11）：覆盖全部受保护端点的表驱动权限用例。
//
// authzRouteInventory 是路由清单常量：每个端点登记访问级别与授权成功的请求样例。
// TestAuthzRouteInventoryComplete 用 chi.Walk 与实际路由双向核对——
// 新增端点未登记进清单、或清单里的端点已被删除时，测试直接失败。
// TestAuthzMatrix 按访问级别为每个端点生成用例行：
//   - user 级：匿名 401、普通用户 2xx
//   - admin 级：匿名 401、普通用户 403、管理员 2xx
//   - root 级：匿名 401、普通用户 403、管理员 403、超级管理员 2xx
//   - api_key 级（/v1 下游端点）：匿名（无 API Key）401
//   - public 级：仅参与清单完整性核对，不产生矩阵行

// routeAccess 是端点的访问级别。
type routeAccess string

const (
	accessPublic routeAccess = "public"  // 无需认证
	accessAPIKey routeAccess = "api_key" // API Key 认证（/v1 下游端点）
	accessUser   routeAccess = "user"    // 会话认证，最低角色 user
	accessAdmin  routeAccess = "admin"   // 会话认证，最低角色 admin
	accessRoot   routeAccess = "root"    // 会话认证，root 独占
)

// protectedRoute 是路由清单中的一行。
type protectedRoute struct {
	method  string
	pattern string // chi 注册模式，用于与实际路由核对
	access  routeAccess
	path    string // 授权请求的具体路径（含种子数据 id 与查询串）；空表示与 pattern 相同
	body    any    // 授权请求的请求体
	wantOK  int    // 授权角色请求的期望状态码（2xx）
}

// 矩阵种子数据的固定 id（TRUNCATE ... RESTART IDENTITY 保证确定性）：
// 用户：1=alice(user) 2=bob(admin) 3=carol(root) 4=victim(user) 5=victim2(user，供删除)
// 模型：1=matrix-model-keep 2=matrix-model-del（供删除）
// 渠道：1=matrix-ch-keep 2=matrix-ch-del（供删除）
// 兑换码：1=用户兑换用 2=管理员作废用
// API Key：1=alice 的密钥
const authzRedeemCode = "tzr-matrix-code-1"

// channelBody 渠道创建/更新的合法请求体（base_url 指向不可达地址，测试连通端点快速失败）。
func channelBody(name string) map[string]any {
	return map[string]any{
		"name": name, "provider": "openai", "protocol": "openai_compat",
		"base_url": "http://127.0.0.1:1", "api_key": "sk-upstream",
		"models": []string{"matrix-model-keep"},
	}
}

// authzRouteInventory 返回全部端点的清单。清单顺序即矩阵执行顺序：
// 破坏性操作（删除）各自使用独立的种子资源，不影响后续行。
func authzRouteInventory() []protectedRoute {
	return []protectedRoute{
		// 公开端点（仅参与完整性核对）
		{method: "GET", pattern: "/healthz", access: accessPublic},
		// /metrics 自行校验令牌或 root 会话，不经角色中间件；
		// 仅参与路由清单完整性核对，权限边界另见 TestMetricsRequiresRootOrToken。
		{method: "GET", pattern: "/metrics", access: accessPublic},
		{method: "POST", pattern: "/api/auth/login", access: accessPublic},
		{method: "POST", pattern: "/api/auth/logout", access: accessPublic},
		{method: "POST", pattern: "/api/auth/register", access: accessPublic},
		{method: "GET", pattern: "/api/site/config", access: accessPublic},

		// 会话端点（user 级）
		{method: "GET", pattern: "/api/auth/me", access: accessUser, wantOK: 200},
		{method: "PUT", pattern: "/api/auth/password", access: accessUser,
			body: map[string]string{"original_password": "password-alice", "password": "password-alice-2"}, wantOK: 200},
		{method: "PUT", pattern: "/api/auth/profile", access: accessUser,
			body: map[string]string{"display_name": "Alice Matrix"}, wantOK: 200},

		// 用户端（user 级）
		{method: "GET", pattern: "/api/me/balance", access: accessUser, wantOK: 200},
		{method: "POST", pattern: "/api/me/redeem", access: accessUser,
			body: map[string]string{"code": authzRedeemCode}, wantOK: 200},
		{method: "GET", pattern: "/api/me/ledger", access: accessUser, wantOK: 200},
		{method: "GET", pattern: "/api/me/usage-logs", access: accessUser, wantOK: 200},
		{method: "GET", pattern: "/api/me/usage-logs/detail", access: accessUser,
			path: "/api/me/usage-logs/detail?request_id=matrix-req-1", wantOK: 200},
		{method: "GET", pattern: "/api/me/usage-logs/export", access: accessUser, wantOK: 200},
		{method: "GET", pattern: "/api/me/usage-summary", access: accessUser, wantOK: 200},
		{method: "GET", pattern: "/api/me/usage-daily", access: accessUser, wantOK: 200},
		{method: "GET", pattern: "/api/me/cache-report", access: accessUser, wantOK: 200},
		{method: "GET", pattern: "/api/me/token-report", access: accessUser, wantOK: 200},
		{method: "GET", pattern: "/api/me/heatmap", access: accessUser, wantOK: 200},
		{method: "GET", pattern: "/api/me/models", access: accessUser, wantOK: 200},
		{method: "GET", pattern: "/api/me/service-status", access: accessUser, wantOK: 200},
		{method: "GET", pattern: "/api/me/keys/", access: accessUser, wantOK: 200},
		{method: "POST", pattern: "/api/me/keys/", access: accessUser,
			body: map[string]string{"name": "matrix-new-key"}, wantOK: 201},
		{method: "GET", pattern: "/api/me/keys/{id}", access: accessUser,
			path: "/api/me/keys/1", wantOK: 200},
		{method: "PUT", pattern: "/api/me/keys/{id}", access: accessUser,
			path: "/api/me/keys/1", body: map[string]string{"name": "matrix-key-renamed"}, wantOK: 200},
		{method: "DELETE", pattern: "/api/me/keys/{id}", access: accessUser,
			path: "/api/me/keys/1", wantOK: 200},

		// 部门负责人视图（user 级）：登录即可访问，具体部门的可见性由
		// departments.owner_user_id 逐次校验，非负责人访问的用例见 dept_reports_test.go。
		{method: "GET", pattern: "/api/dept/departments", access: accessUser, wantOK: 200},
		{method: "GET", pattern: "/api/dept/budget", access: accessUser,
			path: "/api/dept/budget?department_id=3", wantOK: 200},
		{method: "GET", pattern: "/api/dept/cost-report", access: accessUser,
			path: "/api/dept/cost-report?department_id=3", wantOK: 200},
		{method: "GET", pattern: "/api/dept/members", access: accessUser,
			path: "/api/dept/members?department_id=3", wantOK: 200},

		// 管理端：用户管理（admin 级）
		{method: "GET", pattern: "/api/admin/users/", access: accessAdmin, wantOK: 200},
		{method: "POST", pattern: "/api/admin/users/", access: accessAdmin,
			body: map[string]string{"username": "matrixnewuser", "password": "password123"}, wantOK: 201},
		{method: "GET", pattern: "/api/admin/users/{id}", access: accessAdmin,
			path: "/api/admin/users/4", wantOK: 200},
		{method: "PUT", pattern: "/api/admin/users/{id}", access: accessAdmin,
			path: "/api/admin/users/4", body: map[string]string{"display_name": "Victim"}, wantOK: 200},
		{method: "DELETE", pattern: "/api/admin/users/{id}", access: accessAdmin,
			path: "/api/admin/users/5", wantOK: 200},
		{method: "POST", pattern: "/api/admin/users/{id}/status", access: accessAdmin,
			path: "/api/admin/users/4/status", body: map[string]string{"status": "enabled"}, wantOK: 200},
		{method: "POST", pattern: "/api/admin/users/{id}/reset-password", access: accessAdmin,
			path: "/api/admin/users/4/reset-password", body: map[string]string{"password": "password123456"}, wantOK: 200},
		{method: "GET", pattern: "/api/admin/users/{id}/keys", access: accessAdmin,
			path: "/api/admin/users/4/keys", wantOK: 200},
		// 代签发 / 停用 / 吊销 Key（托管桶，admin 亦放行）：明文仅创建时返回一次。
		{method: "POST", pattern: "/api/admin/users/{id}/keys", access: accessAdmin,
			path: "/api/admin/users/4/keys", body: map[string]string{"name": "matrix-issued-key"}, wantOK: 201},
		{method: "PUT", pattern: "/api/admin/users/{id}/keys/{key_id}", access: accessAdmin,
			path: "/api/admin/users/4/keys/2", body: map[string]string{"name": "matrix-issued-renamed"}, wantOK: 200},
		{method: "DELETE", pattern: "/api/admin/users/{id}/keys/{key_id}", access: accessAdmin,
			path: "/api/admin/users/4/keys/2", wantOK: 200},
		// 按外部标识精确检索（托管桶，admin 亦放行）
		{method: "GET", pattern: "/api/admin/users/external/{ref}", access: accessAdmin,
			path: "/api/admin/users/external/matrix-ext-user", wantOK: 200},
		{method: "POST", pattern: "/api/admin/users/{id}/credits", access: accessAdmin,
			path: "/api/admin/users/4/credits", body: map[string]any{"amount": 1000, "note": "matrix"}, wantOK: 200},

		// 管理端：模型管理（admin 级）
		{method: "GET", pattern: "/api/admin/models/", access: accessAdmin, wantOK: 200},
		{method: "POST", pattern: "/api/admin/models/", access: accessAdmin,
			body: map[string]string{"name": "matrix-model-new"}, wantOK: 201},
		{method: "GET", pattern: "/api/admin/models/{id}", access: accessAdmin,
			path: "/api/admin/models/1", wantOK: 200},
		{method: "PUT", pattern: "/api/admin/models/{id}", access: accessAdmin,
			path: "/api/admin/models/1", body: map[string]string{"name": "matrix-model-keep"}, wantOK: 200},
		{method: "DELETE", pattern: "/api/admin/models/{id}", access: accessAdmin,
			path: "/api/admin/models/2", wantOK: 200},
		{method: "PUT", pattern: "/api/admin/models/{id}/price", access: accessAdmin,
			path: "/api/admin/models/1/price", body: map[string]any{"input_price": 1000}, wantOK: 200},
		{method: "PUT", pattern: "/api/admin/models/{id}/peak-rules", access: accessAdmin,
			path: "/api/admin/models/1/peak-rules", body: map[string]any{"rules": []any{}}, wantOK: 200},
		{method: "GET", pattern: "/api/admin/models/{id}/channel-costs", access: accessAdmin,
			path: "/api/admin/models/1/channel-costs", wantOK: 200},
		{method: "POST", pattern: "/api/admin/models/import", access: accessAdmin,
			body: map[string]any{"items": []any{map[string]any{
				"name": "matrix-model-import", "price": map[string]any{"input_price": 1000},
			}}}, wantOK: 200},
		{method: "GET", pattern: "/api/admin/models/pricing-presets", access: accessAdmin, wantOK: 200},

		// 管理端：模型远程导入（admin 级）——非 http(s) scheme 在校验层被拒，
		// 不经 handler，故 wantOK 设 400（authz 矩阵只核对鉴权，不核对业务校验）。
		{method: "POST", pattern: "/api/admin/models/import-remote", access: accessAdmin,
			body: map[string]any{"source_url": ""}, wantOK: 400},

		// 管理端：部门管理（admin 级）
		{method: "GET", pattern: "/api/admin/departments/", access: accessAdmin, wantOK: 200},
		{method: "POST", pattern: "/api/admin/departments/", access: accessAdmin,
			body: map[string]any{"name": "matrix-dept-new"}, wantOK: 201},
		{method: "GET", pattern: "/api/admin/departments/{id}", access: accessAdmin,
			path: "/api/admin/departments/1", wantOK: 200},
		{method: "PUT", pattern: "/api/admin/departments/{id}", access: accessAdmin,
			path: "/api/admin/departments/1", body: map[string]any{"name": "matrix-dept-keep"}, wantOK: 200},
		{method: "POST", pattern: "/api/admin/departments/{id}/members", access: accessAdmin,
			path: "/api/admin/departments/1/members",
			body: map[string]any{"user_ids": []int64{4}, "remove": true}, wantOK: 200},
		{method: "DELETE", pattern: "/api/admin/departments/{id}", access: accessAdmin,
			path: "/api/admin/departments/2", wantOK: 200},
		{method: "GET", pattern: "/api/admin/departments/external/{ref}", access: accessAdmin,
			path: "/api/admin/departments/external/matrix-ext-dept", wantOK: 200},

		// 管理端：项目管理（admin 级，与部门同托管桶、同登记级别）
		{method: "GET", pattern: "/api/admin/projects/", access: accessAdmin, wantOK: 200},
		{method: "POST", pattern: "/api/admin/projects/", access: accessAdmin,
			body: map[string]any{"name": "matrix-proj-new"}, wantOK: 201},
		{method: "GET", pattern: "/api/admin/projects/external/{ref}", access: accessAdmin,
			path: "/api/admin/projects/external/matrix-ext-proj", wantOK: 200},
		{method: "GET", pattern: "/api/admin/projects/{id}", access: accessAdmin,
			path: "/api/admin/projects/1", wantOK: 200},
		{method: "PUT", pattern: "/api/admin/projects/{id}", access: accessAdmin,
			path: "/api/admin/projects/1", body: map[string]any{"name": "matrix-proj-keep"}, wantOK: 200},
		{method: "DELETE", pattern: "/api/admin/projects/{id}", access: accessAdmin,
			path: "/api/admin/projects/2", wantOK: 200},

		// 管理端：批量操作（admin 级）
		{method: "POST", pattern: "/api/admin/users/import", access: accessAdmin,
			body: map[string]any{"items": []any{map[string]any{
				"username": "matriximported", "password": "password123",
			}}}, wantOK: 200},
		{method: "POST", pattern: "/api/admin/credits/batch-grant", access: accessAdmin,
			body: map[string]any{"user_ids": []int64{4}, "amount": 100, "note": "matrix"}, wantOK: 200},
		{method: "POST", pattern: "/api/admin/users/batch-status", access: accessAdmin,
			body: map[string]any{"user_ids": []int64{4}, "status": "enabled"}, wantOK: 200},

		// 管理端：兑换码（admin 级）
		{method: "GET", pattern: "/api/admin/redemptions/", access: accessAdmin, wantOK: 200},
		{method: "POST", pattern: "/api/admin/redemptions/batch", access: accessAdmin,
			body: map[string]any{"count": 1, "credits": 1000}, wantOK: 201},
		{method: "PUT", pattern: "/api/admin/redemptions/{id}/status", access: accessAdmin,
			path: "/api/admin/redemptions/2/status", body: map[string]string{"status": "disabled"}, wantOK: 200},

		// 管理端：渠道管理（admin 级）
		{method: "GET", pattern: "/api/admin/channels/", access: accessAdmin, wantOK: 200},
		{method: "POST", pattern: "/api/admin/channels/", access: accessAdmin,
			body: channelBody("matrix-ch-new"), wantOK: 201},
		{method: "GET", pattern: "/api/admin/channels/{id}", access: accessAdmin,
			path: "/api/admin/channels/1", wantOK: 200},
		{method: "PUT", pattern: "/api/admin/channels/{id}", access: accessAdmin,
			path: "/api/admin/channels/1", body: channelBody("matrix-ch-keep"), wantOK: 200},
		{method: "DELETE", pattern: "/api/admin/channels/{id}", access: accessAdmin,
			path: "/api/admin/channels/2", wantOK: 200},
		{method: "PUT", pattern: "/api/admin/channels/{id}/status", access: accessAdmin,
			path: "/api/admin/channels/1/status", body: map[string]string{"status": "enabled"}, wantOK: 200},
		{method: "POST", pattern: "/api/admin/channels/{id}/test", access: accessAdmin,
			path: "/api/admin/channels/1/test", wantOK: 200},
		{method: "GET", pattern: "/api/admin/channels/{id}/costs", access: accessAdmin,
			path: "/api/admin/channels/1/costs", wantOK: 200},
		{method: "PUT", pattern: "/api/admin/channels/{id}/costs", access: accessAdmin,
			path: "/api/admin/channels/1/costs", body: map[string]any{"costs": []any{}}, wantOK: 200},

		// 管理端：流水 / 用量日志 / 统计（admin 级）
		{method: "GET", pattern: "/api/admin/ledger", access: accessAdmin, wantOK: 200},
		{method: "GET", pattern: "/api/admin/usage-logs", access: accessAdmin, wantOK: 200},
		{method: "GET", pattern: "/api/admin/usage-logs/detail", access: accessAdmin,
			path: "/api/admin/usage-logs/detail?request_id=matrix-req-1", wantOK: 200},
		{method: "GET", pattern: "/api/admin/setup-status", access: accessAdmin, wantOK: 200},
		{method: "GET", pattern: "/api/admin/stats/overview", access: accessAdmin, wantOK: 200},
		{method: "GET", pattern: "/api/admin/stats/usage-daily", access: accessAdmin, wantOK: 200},
		{method: "GET", pattern: "/api/admin/stats/profit", access: accessAdmin, wantOK: 200},
		{method: "GET", pattern: "/api/admin/stats/cost-report", access: accessAdmin, wantOK: 200},
		{method: "GET", pattern: "/api/admin/stats/department-budget", access: accessAdmin, wantOK: 200},
		{method: "GET", pattern: "/api/admin/stats/project-budget", access: accessAdmin, wantOK: 200},
		{method: "GET", pattern: "/api/admin/stats/heatmap", access: accessAdmin, wantOK: 200},
		{method: "GET", pattern: "/api/admin/stats/calendar", access: accessAdmin, wantOK: 200},
		{method: "GET", pattern: "/api/admin/stats/health-timeline", access: accessAdmin, wantOK: 200},
		{method: "GET", pattern: "/api/admin/stats/ops-summary", access: accessAdmin, wantOK: 200},
		{method: "GET", pattern: "/api/admin/stats/cost-by-calltype", access: accessAdmin, wantOK: 200},
		{method: "GET", pattern: "/api/admin/stats/cache-report", access: accessAdmin, wantOK: 200},
		{method: "GET", pattern: "/api/admin/stats/runtime", access: accessAdmin, wantOK: 200},
		{method: "GET", pattern: "/api/admin/usage-logs/export", access: accessAdmin, wantOK: 200},

		// 管理端：审计与告警（admin 级）
		{method: "GET", pattern: "/api/admin/audit-logs/", access: accessAdmin, wantOK: 200},
		{method: "GET", pattern: "/api/admin/audit-logs/actions", access: accessAdmin, wantOK: 200},
		{method: "GET", pattern: "/api/admin/alerts/", access: accessAdmin, wantOK: 200},
		// 未配置任何告警通道时测试端点返回 400，此处只核对权限边界：
		// 匿名 401、普通用户 403，管理员能进入业务处理即达成用例目的。
		{method: "POST", pattern: "/api/admin/alerts/test", access: accessAdmin, wantOK: 400},

		// 管理端：系统设置（root 独占）
		{method: "GET", pattern: "/api/admin/settings", access: accessRoot, wantOK: 200},
		{method: "PUT", pattern: "/api/admin/settings", access: accessRoot,
			body: map[string]any{"key": "rate_limit_per_key_rpm", "value": 120}, wantOK: 200},

		// 管理端：接入方与服务令牌运营（root 独占）。种子 integration id=1 供详情/
		// 改名/令牌/停用等用例取 2xx；POST /integrations 建独立新接入方避免冲突。
		{method: "GET", pattern: "/api/admin/integrations/", access: accessRoot, wantOK: 200},
		{method: "POST", pattern: "/api/admin/integrations/", access: accessRoot,
			body: map[string]string{"name": "matrix-integ-new", "slug": "matrix-integ-new"}, wantOK: 201},
		{method: "GET", pattern: "/api/admin/integrations/{id}", access: accessRoot,
			path: "/api/admin/integrations/1", wantOK: 200},
		{method: "PUT", pattern: "/api/admin/integrations/{id}", access: accessRoot,
			path: "/api/admin/integrations/1",
			body: map[string]string{"name": "matrix-integ"}, wantOK: 200},
		{method: "POST", pattern: "/api/admin/integrations/{id}/service-tokens", access: accessRoot,
			path: "/api/admin/integrations/1/service-tokens",
			body: map[string]string{"name": "matrix-token"}, wantOK: 201},
		{method: "GET", pattern: "/api/admin/integrations/{id}/service-tokens", access: accessRoot,
			path: "/api/admin/integrations/1/service-tokens", wantOK: 200},
		{method: "PUT", pattern: "/api/admin/integrations/{id}/service-tokens/{token_id}/status", access: accessRoot,
			path: "/api/admin/integrations/1/service-tokens/1/status",
			body: map[string]string{"status": "disabled"}, wantOK: 200},
		{method: "DELETE", pattern: "/api/admin/integrations/{id}/service-tokens/{token_id}", access: accessRoot,
			path: "/api/admin/integrations/1/service-tokens/1", wantOK: 200},
		{method: "POST", pattern: "/api/admin/integrations/{id}/disable", access: accessRoot,
			path: "/api/admin/integrations/1/disable", wantOK: 200},

		// 下游 /v1 端点（API Key 认证，匿名应 401）
		{method: "POST", pattern: "/v1/chat/completions", access: accessAPIKey},
		{method: "POST", pattern: "/v1/messages", access: accessAPIKey},
		{method: "POST", pattern: "/v1/messages/count_tokens", access: accessAPIKey},
		{method: "POST", pattern: "/v1/embeddings", access: accessAPIKey},
		{method: "POST", pattern: "/v1/images/generations", access: accessAPIKey},
		{method: "GET", pattern: "/v1/models", access: accessAPIKey},
		{method: "GET", pattern: "/v1/key/info", access: accessAPIKey},

		// 下游 /{provider}/v1 端点（API Key 认证，匿名应 401）：provider 前缀入口，认证/
		// 计费/限流/协议转换全链路与 /v1 一致；清单 path 用合法 slug（openai）以便匿名请求
		// 通过 requireProvider 后进入 guardV1 的 API Key 校验返回 401，而非 slug 解析 404。
		{method: "POST", pattern: "/{provider}/v1/chat/completions", access: accessAPIKey,
			path: "/openai/v1/chat/completions"},
		{method: "POST", pattern: "/{provider}/v1/messages", access: accessAPIKey,
			path: "/openai/v1/messages"},
		{method: "POST", pattern: "/{provider}/v1/messages/count_tokens", access: accessAPIKey,
			path: "/openai/v1/messages/count_tokens"},
		{method: "POST", pattern: "/{provider}/v1/embeddings", access: accessAPIKey,
			path: "/openai/v1/embeddings"},
		{method: "POST", pattern: "/{provider}/v1/images/generations", access: accessAPIKey,
			path: "/openai/v1/images/generations"},
		{method: "GET", pattern: "/{provider}/v1/models", access: accessAPIKey,
			path: "/openai/v1/models"},
		{method: "GET", pattern: "/{provider}/v1/key/info", access: accessAPIKey,
			path: "/openai/v1/key/info"},
	}
}

// TestAuthzRouteInventoryComplete 双向核对路由清单与实际注册的路由。
// 不依赖数据库：NewRouter 仅做路由注册，chi.Walk 不执行中间件。
func TestAuthzRouteInventoryComplete(t *testing.T) {
	router, ok := NewRouter(Deps{}).(chi.Routes)
	if !ok {
		t.Fatal("NewRouter 返回值未实现 chi.Routes，无法枚举路由")
	}
	actual := map[string]bool{}
	err := chi.Walk(router, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		actual[method+" "+strings.ReplaceAll(route, "/*/", "/")] = true
		return nil
	})
	if err != nil {
		t.Fatalf("枚举路由失败: %v", err)
	}
	declared := map[string]bool{}
	for _, rt := range authzRouteInventory() {
		key := rt.method + " " + rt.pattern
		if declared[key] {
			t.Errorf("路由清单存在重复条目: %s", key)
		}
		declared[key] = true
	}
	for key := range actual {
		if !declared[key] {
			t.Errorf("端点 %s 未登记进越权矩阵路由清单（authz_matrix_test.go），请补充访问级别与授权用例", key)
		}
	}
	for key := range declared {
		if !actual[key] {
			t.Errorf("路由清单中的 %s 在实际路由中不存在，请更新清单", key)
		}
	}
}

// seedAuthzFixtures 种入矩阵所需的全部资源（id 见清单顶部注释）。
func seedAuthzFixtures(t *testing.T, e *testEnv) {
	t.Helper()
	ctx := t.Context()

	// 供删除/管理操作的普通用户（不登录，无会话）。
	// victim2 必须保持零积分流水与零用量日志，否则删除端点会按护栏返回 409
	// 而非矩阵期望的 200（护栏用例见 user_deletion_guard_test.go）。
	for i, name := range []string{"victim", "victim2"} {
		hash, err := auth.HashPassword("password-" + name)
		if err != nil {
			t.Fatalf("哈希失败: %v", err)
		}
		u := &store.User{Username: name, PasswordHash: hash, Role: domain.RoleUser, Status: domain.UserEnabled}
		if i == 0 {
			u.ExternalRef = "matrix-ext-user"
		}
		if err := e.db.Create(u).Error; err != nil {
			t.Fatalf("种入用户 %s 失败: %v", name, err)
		}
	}

	// alice 的 API Key（id=1）
	key := &store.APIKey{
		UserID: 1, Name: "matrix-seed-key",
		KeyHash: "matrix-hash-1", KeyPrefix: "tzl-matrix", Status: domain.KeyEnabled,
	}
	if err := e.deps.Keys.Create(ctx, key); err != nil {
		t.Fatalf("种入 API Key 失败: %v", err)
	}
	// victim（user id=4）的 API Key（id=2），供管理端代签发的 PUT/DELETE 用例。
	victimKey := &store.APIKey{
		UserID: 4, Name: "matrix-victim-key",
		KeyHash: "matrix-hash-2", KeyPrefix: "tzl-vict", Status: domain.KeyEnabled,
	}
	if err := e.deps.Keys.Create(ctx, victimKey); err != nil {
		t.Fatalf("种入 victim API Key 失败: %v", err)
	}

	// 模型（id=1 保留，id=2 供删除）
	for _, name := range []string{"matrix-model-keep", "matrix-model-del"} {
		m := &store.Model{
			Name: name, Modality: domain.ModalityText,
			BillingMode: domain.BillPerToken, Status: domain.ModelEnabled,
		}
		if err := e.deps.Models.Create(ctx, m); err != nil {
			t.Fatalf("种入模型 %s 失败: %v", name, err)
		}
	}

	// 渠道（id=1 保留，id=2 供删除）
	encrypted, err := e.deps.Secrets.Encrypt("sk-upstream")
	if err != nil {
		t.Fatalf("加密上游密钥失败: %v", err)
	}
	for _, name := range []string{"matrix-ch-keep", "matrix-ch-del"} {
		ch := &store.Channel{
			Name: name, Provider: domain.ProviderOpenAI, Protocol: domain.ProtocolOpenAICompat,
			BaseURL: "http://127.0.0.1:1", APIKeyEncrypted: encrypted,
			Models:       toJSONField([]string{"matrix-model-keep"}),
			ModelMapping: []byte("{}"), Status: domain.ChannelEnabled,
			Priority: 0, Weight: 1,
			ParamOverride: []byte("{}"), HeaderOverride: []byte("{}"),
		}
		if err := e.deps.Channels.Create(ctx, ch); err != nil {
			t.Fatalf("种入渠道 %s 失败: %v", name, err)
		}
	}

	// 部门（id=1 保留，id=2 供删除；两者均无成员，删除端点方可返回 200）
	for i, name := range []string{"matrix-dept-keep", "matrix-dept-del"} {
		dept := &store.Department{Name: name, Status: domain.DepartmentEnabled}
		if i == 0 {
			dept.ExternalRef = "matrix-ext-dept"
		}
		if err := e.deps.Departments.Create(ctx, dept); err != nil {
			t.Fatalf("种入部门 %s 失败: %v", name, err)
		}
	}
	// 部门 id=3：负责人为 alice（user 级），供 /api/dept 下的负责人视图取得 2xx。
	// 单独一个部门而不复用 id=1，是因为矩阵中的部门更新行会重写 id=1 的字段，
	// 复用会让负责人视图的用例依赖矩阵行的执行顺序。
	aliceID := int64(1)
	ownedDept := &store.Department{
		Name: "matrix-dept-owned", Status: domain.DepartmentEnabled, OwnerUserID: &aliceID,
	}
	if err := e.deps.Departments.Create(ctx, ownedDept); err != nil {
		t.Fatalf("种入负责人部门失败: %v", err)
	}
	// 负责人还必须是本部门成员：查账权限同时要求这两个条件。
	if err := e.db.Model(&store.User{}).Where("id = ?", aliceID).
		Update("department_id", ownedDept.ID).Error; err != nil {
		t.Fatalf("把负责人划入部门失败: %v", err)
	}

	// 项目（id=1 保留并带外部标识供 external/{ref} 用例，id=2 供删除）：与部门同构、
	// 同属托管桶（managed/admin/root），矩阵登记级别与部门一致（accessAdmin）。
	for i, name := range []string{"matrix-proj-keep", "matrix-proj-del"} {
		proj := &store.Project{Name: name, Status: domain.ProjectEnabled}
		if i == 0 {
			proj.ExternalRef = "matrix-ext-proj"
		}
		if err := e.deps.Projects.Create(ctx, proj); err != nil {
			t.Fatalf("种入项目 %s 失败: %v", name, err)
		}
	}

	// 兑换码（id=1 用户兑换用，id=2 管理员作废用）
	items := []store.Redemption{
		{BatchID: "matrix", CodeHash: auth.HashKey(authzRedeemCode),
			Credits: 1000, Status: domain.RedemptionUnused},
		{BatchID: "matrix", CodeHash: auth.HashKey("tzr-matrix-code-2"),
			Credits: 1000, Status: domain.RedemptionUnused},
	}
	if err := e.deps.Redemptions.CreateBatch(ctx, items); err != nil {
		t.Fatalf("种入兑换码失败: %v", err)
	}

	// 用量日志（供 detail 查询）
	log := &store.UsageLog{
		RequestID: "matrix-req-1", UserID: 1, APIKeyID: 1,
		ModelName: "matrix-model-keep", CallCount: 1,
		Status: domain.UsageSettled, PriceSnapshot: datatypes.JSON("{}"),
	}
	if err := e.deps.UsageLogs.Create(ctx, log); err != nil {
		t.Fatalf("种入用量日志失败: %v", err)
	}

	// 接入方 id=1 与其服务账号，供 root 桶的接入方/服务令牌用例取 2xx。
	integ := &store.Integration{
		Name: "matrix-integ", Slug: "matrix-integ", Status: domain.IntegrationEnabled,
	}
	if err := e.deps.Integrations.Create(ctx, integ); err != nil {
		t.Fatalf("种入接入方失败: %v", err)
	}
	svcAccount := &store.User{
		Username: "svc:matrix-integ", Role: domain.RoleManaged, Status: domain.UserEnabled,
		IntegrationID: &integ.ID,
	}
	if err := e.deps.Users.Create(ctx, svcAccount); err != nil {
		t.Fatalf("种入接入方服务账号失败: %v", err)
	}
}

// TestAuthzMatrix 按路由清单执行越权矩阵。
func TestAuthzMatrix(t *testing.T) {
	e := newTestEnv(t)
	anon := e.client(t)
	userC := e.seedAndLogin(t, "alice", domain.RoleUser) // id=1
	adminC := e.seedAndLogin(t, "bob", domain.RoleAdmin) // id=2
	rootC := e.seedAndLogin(t, "carol", domain.RoleRoot) // id=3
	seedAuthzFixtures(t, e)

	for _, rt := range authzRouteInventory() {
		path := rt.path
		if path == "" {
			path = rt.pattern
		}
		run := func(label string, c *http.Client, want int) {
			t.Run(fmt.Sprintf("%s %s %s", rt.method, rt.pattern, label), func(t *testing.T) {
				resp, env := e.do(t, c, rt.method, path, rt.body)
				if resp.StatusCode != want {
					t.Errorf("期望 %d，实际 %d，响应: %v", want, resp.StatusCode, env)
				}
			})
		}
		switch rt.access {
		case accessPublic:
			// 公开端点不产生矩阵行，仅参与清单完整性核对
		case accessAPIKey:
			run("匿名应401", anon, 401)
		case accessUser:
			run("匿名应401", anon, 401)
			run("用户应成功", userC, rt.wantOK)
		case accessAdmin:
			run("匿名应401", anon, 401)
			run("用户应403", userC, 403)
			run("管理员应成功", adminC, rt.wantOK)
		case accessRoot:
			run("匿名应401", anon, 401)
			run("用户应403", userC, 403)
			run("管理员应403", adminC, 403)
			run("root应成功", rootC, rt.wantOK)
		default:
			t.Fatalf("未知访问级别: %s（端点 %s %s）", rt.access, rt.method, rt.pattern)
		}
	}
}

// TestAuthzRoleEscalationBoundaries 固化角色提升边界：
// 创建高于普通用户的角色是 root 独占操作（中间件放行后由处理函数二次校验）。
func TestAuthzRoleEscalationBoundaries(t *testing.T) {
	e := newTestEnv(t)
	adminC := e.seedAndLogin(t, "bob", domain.RoleAdmin)
	rootC := e.seedAndLogin(t, "carol", domain.RoleRoot)

	resp, env := e.do(t, adminC, "POST", "/api/admin/users/",
		map[string]any{"username": "newadmin", "password": "password123", "role": "admin"})
	if resp.StatusCode != 403 {
		t.Errorf("管理员创建管理员应 403，实际 %d，响应: %v", resp.StatusCode, env)
	}
	resp, env = e.do(t, rootC, "POST", "/api/admin/users/",
		map[string]any{"username": "newadmin2", "password": "password123", "role": "admin"})
	if resp.StatusCode != 201 {
		t.Errorf("root 创建管理员应 201，实际 %d，响应: %v", resp.StatusCode, env)
	}
	resp, env = e.do(t, rootC, "GET", "/api/admin/users/", nil)
	if resp.StatusCode != 200 {
		t.Errorf("root 查看用户列表应 200，实际 %d，响应: %v", resp.StatusCode, env)
	}
}
