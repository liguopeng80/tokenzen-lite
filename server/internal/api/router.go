// Package api 组装 HTTP 路由与中间件。
package api

import (
	"net/http"

	"github.com/alexedwards/scs/v2"
	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"gorm.io/gorm"

	"github.com/liguopeng80/tokenzen-lite/server/internal/alerting"
	"github.com/liguopeng80/tokenzen-lite/server/internal/api/respond"
	"github.com/liguopeng80/tokenzen-lite/server/internal/audit"
	"github.com/liguopeng80/tokenzen-lite/server/internal/auth"
	"github.com/liguopeng80/tokenzen-lite/server/internal/billing"
	"github.com/liguopeng80/tokenzen-lite/server/internal/config"
	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/obs"
	"github.com/liguopeng80/tokenzen-lite/server/internal/ratelimit"
	"github.com/liguopeng80/tokenzen-lite/server/internal/relay"
	"github.com/liguopeng80/tokenzen-lite/server/internal/secrets"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// Deps 是路由层依赖集合，随里程碑推进逐步扩充。
//
// NewRouter 内部按 feature 把 Deps 的字段拆给各 sub-controller（authController、
// meKeysController 等），路由注册从 d.handleX 改为 c.handleX。Deps 仅作为装配根与
// NewRouter 的契约保留——main.go 与集成测试构造 Deps 后传入，签名不变。
type Deps struct {
	Cfg           *config.Config
	DB            *gorm.DB
	Sessions      *scs.SessionManager
	Users         *store.UserRepo
	Keys          *store.APIKeyRepo
	Models        *store.ModelRepo
	Ledger        *store.LedgerRepo
	Redemptions   *store.RedemptionRepo
	Settings      *store.SettingsRepo
	Billing       *billing.Service
	Channels      *store.ChannelRepo
	Costs         *store.ChannelCostRepo
	UsageLogs     *store.UsageLogRepo
	Secrets       *secrets.Box
	Relay         *relay.Engine
	Stats         *store.StatsRepo
	Limiter       ratelimit.Limiter
	Gate          *ratelimit.ConcurrencyGate
	LoginLock     *ratelimit.FailureLocker
	Departments   *store.DepartmentRepo
	Projects      *store.ProjectRepo
	AuditLogs     *store.AuditLogRepo
	Audit         *audit.Recorder
	AlertEvents   *store.AlertEventRepo
	Alerts        *alerting.Service
	Spend         *store.SpendRepo
	Rollup        *store.RollupRepo
	Integrations  *store.IntegrationRepo
	ServiceTokens *store.ServiceTokenRepo
	Idempotency   *store.IdempotencyRepo
}

// systemController 承载进程级运维端点：健康检查（依赖 DB 与中继的日志丢弃计数）
// 与指标导出（令牌或 root 会话鉴权）。
type systemController struct {
	Cfg      *config.Config
	DB       *gorm.DB
	Sessions *scs.SessionManager
	Users    *store.UserRepo
	Relay    *relay.Engine
}

// NewRouter 构建全部路由。
func NewRouter(d Deps) http.Handler {
	mw := &auth.Middleware{
		Sessions: d.Sessions, Users: d.Users,
		Integrations: d.Integrations, ServiceTokens: d.ServiceTokens,
	}
	requireUser := mw.RequireRole(domain.RoleUser)

	// 按 feature 把 Deps 字段拆给各 sub-controller：handler 仅见本 feature 所需句柄，
	// 不再共享全句柄依赖（原 god-struct Deps 仍作为装配根契约保留）。
	sys := &systemController{
		Cfg: d.Cfg, DB: d.DB, Sessions: d.Sessions, Users: d.Users, Relay: d.Relay,
	}
	pub := &publicController{Channels: d.Channels, Models: d.Models, Settings: d.Settings}
	authn := &authController{
		Audit: d.Audit, Cfg: d.Cfg, Departments: d.Departments,
		Limiter: d.Limiter, LoginLock: d.LoginLock,
		Sessions: d.Sessions, Settings: d.Settings, Users: d.Users,
	}
	meKeys := &meKeysController{
		Audit: d.Audit, Keys: d.Keys, Projects: d.Projects, Settings: d.Settings,
	}
	meStats := &meStatsController{
		Billing: d.Billing, Ledger: d.Ledger, Settings: d.Settings, Users: d.Users,
		Channels: d.Channels, Models: d.Models, UsageLogs: d.UsageLogs,
		Rollup: d.Rollup, Stats: d.Stats,
	}
	dept := &deptController{Departments: d.Departments, Rollup: d.Rollup, Users: d.Users}
	extLookup := &externalLookupController{
		Departments: d.Departments, Projects: d.Projects, Users: d.Users,
		Settings: d.Settings,
	}
	userAdmin := &userAdminController{
		Audit: d.Audit, Billing: d.Billing, Departments: d.Departments,
		Keys: d.Keys, Ledger: d.Ledger, Sessions: d.Sessions,
		UsageLogs: d.UsageLogs, Users: d.Users, Settings: d.Settings,
		Projects: d.Projects, Idempotency: d.Idempotency,
	}
	catalogAdmin := &catalogAdminController{
		Audit: d.Audit, Channels: d.Channels, Costs: d.Costs, Models: d.Models,
		Relay: d.Relay, Secrets: d.Secrets, Settings: d.Settings,
		Projects: d.Projects, Idempotency: d.Idempotency,
	}
	orgAdmin := &orgAdminController{
		Audit: d.Audit, Departments: d.Departments, Users: d.Users,
		AuditLogs: d.AuditLogs, Idempotency: d.Idempotency, Settings: d.Settings,
	}
	integrationsAdmin := &integrationsAdminController{
		Audit: d.Audit, DB: d.DB, Integrations: d.Integrations,
		ServiceTokens: d.ServiceTokens, Users: d.Users,
	}
	billingAdmin := &billingAdminController{
		Audit: d.Audit, Billing: d.Billing, Ledger: d.Ledger,
		Redemptions: d.Redemptions, Secrets: d.Secrets, Settings: d.Settings,
		Users: d.Users, AlertEvents: d.AlertEvents, Alerts: d.Alerts, Stats: d.Stats,
	}
	reportsAdmin := &reportsAdminController{
		Departments: d.Departments, Projects: d.Projects, Rollup: d.Rollup,
		UsageLogs: d.UsageLogs, Costs: d.Costs, Models: d.Models, Stats: d.Stats,
		Settings: d.Settings,
	}
	relayCtl := &relayController{
		Channels: d.Channels, Departments: d.Departments,
		Gate: d.Gate, Keys: d.Keys, Limiter: d.Limiter, Models: d.Models,
		Relay: d.Relay, Settings: d.Settings, Users: d.Users,
	}
	// d.Alerts 是 *alerting.Service（具体类型），而 relayController.Alerts 是 alerting.Notifier
	// 接口：直接赋值会在 d.Alerts 为 nil 时产生 typed-nil（非 nil 接口持有 nil 指针），使
	// raiseAlert 的 c.Alerts == nil 守卫失效。仅在非 nil 时赋值，保持接口为真正的 nil。
	if d.Alerts != nil {
		relayCtl.Alerts = d.Alerts
	}

	// 来源 IP 头仅在显式配置可信代理来源时采信（Cfg 为 nil 的路由枚举测试除外）。
	var trustedProxies []string
	if d.Cfg != nil {
		trustedProxies = d.Cfg.TrustedProxies
	}

	r := chi.NewRouter()
	r.Use(obs.RealIPMiddleware(trustedProxies))
	r.Use(obs.RequestIDMiddleware)
	r.Use(obs.AccessLogMiddleware)
	r.Use(chimw.Recoverer)
	r.Use(d.Sessions.LoadAndSave)

	r.Get("/healthz", sys.handleHealthz)
	r.Get("/metrics", sys.handleMetrics)

	r.Route("/api", func(r chi.Router) {
		r.Route("/auth", func(r chi.Router) {
			r.Post("/login", authn.handleLogin)
			r.Post("/logout", authn.handleLogout)
			r.Post("/register", authn.handleRegister)
			r.Group(func(r chi.Router) {
				r.Use(requireUser)
				r.Get("/me", authn.handleMe)
				r.Put("/password", authn.handleChangePassword)
				r.Put("/profile", authn.handleUpdateProfile)
			})
		})

		// 公开只读接口：只有站点配置。登录页需要它取站点名称与兑换率。
		r.Get("/site/config", pub.handleSiteConfig)

		r.Route("/me", func(r chi.Router) {
			r.Use(requireUser)
			r.Use(mw.RequirePasswordChanged)
			r.Get("/balance", meStats.handleMeBalance)
			r.Post("/redeem", meStats.handleMeRedeem)
			r.Get("/ledger", meStats.handleMeListLedger)
			r.Get("/usage-logs", meStats.handleMeListUsageLogs)
			r.Get("/usage-logs/detail", meStats.handleMeGetUsageLog)
			r.Get("/usage-logs/export", meStats.handleMeExportUsageLogs)
			r.Get("/usage-summary", meStats.handleMeUsageSummary)
			r.Get("/usage-daily", meStats.handleMeUsageDaily)
			r.Get("/cache-report", meStats.handleMeCacheReport)
			r.Get("/token-report", meStats.handleMeTokenReport)
			r.Get("/heatmap", meStats.handleMeHeatmap)
			r.Get("/service-status", meStats.handleMeServiceStatus)
			// 模型目录需要登录：上架了哪些模型属于内部信息，
			// 不对能打开门户页面的任何人开放。
			r.Get("/models", pub.handlePublicModels)
			r.Route("/keys", func(r chi.Router) {
				r.Get("/", meKeys.handleMeListKeys)
				r.Post("/", meKeys.handleMeCreateKey)
				r.Get("/{id}", meKeys.handleMeGetKey)
				r.Put("/{id}", meKeys.handleMeUpdateKey)
				r.Delete("/{id}", meKeys.handleMeDeleteKey)
			})
		})

		// 部门负责人视图：仅需登录，具体范围按 departments.owner_user_id
		// 与请求中的 department_id 逐次校验，不依赖角色。
		r.Route("/dept", func(r chi.Router) {
			r.Use(requireUser)
			r.Use(mw.RequirePasswordChanged)
			r.Get("/departments", dept.handleDeptListDepartments)
			r.Get("/budget", dept.handleDeptBudget)
			r.Get("/cost-report", dept.handleDeptCostReport)
			r.Get("/members", dept.handleDeptMembers)
		})

		// 管理端按角色分三桶：运营桶（admin 起步，托管令牌被拒）、
		// 托管桶（managed 起步，托管服务令牌、运营会话、root 均可）、root 桶（settings）。
		// mw.AdminAuth 是双源认证：接入方服务令牌或运营会话，均把识别出的用户注入 ctx，
		// 托管令牌同时写入作用域。后续 RequireAtLeast 按注入角色分桶，
		// RequirePasswordChanged 按注入用户的 must_change 校验（服务账号 must_change=false 放行）。
		r.Route("/admin", func(r chi.Router) {
			r.Use(mw.AdminAuth)
			r.Use(mw.RequirePasswordChanged)

			// 运营桶：渠道、模型、兑换码、告警、利润/总览报表、批量运营操作。
			// users/import 与 users/batch-status 是静态路径，先于托管桶的 /users/{id}
			// 注册，避免被参数路由吞掉。
			r.Group(func(r chi.Router) {
				r.Use(mw.RequireAtLeast(domain.RoleAdmin))
				r.Route("/channels", func(r chi.Router) {
					r.Get("/", catalogAdmin.handleAdminListChannels)
					r.Post("/", catalogAdmin.handleAdminCreateChannel)
					r.Get("/{id}", catalogAdmin.handleAdminGetChannel)
					r.Put("/{id}", catalogAdmin.handleAdminUpdateChannel)
					r.Delete("/{id}", catalogAdmin.handleAdminDeleteChannel)
					r.Put("/{id}/status", catalogAdmin.handleAdminSetChannelStatus)
					r.Post("/{id}/test", catalogAdmin.handleAdminTestChannel)
					r.Get("/{id}/costs", catalogAdmin.handleAdminGetChannelCosts)
					r.Put("/{id}/costs", catalogAdmin.handleAdminSetChannelCosts)
				})
				r.Route("/models", func(r chi.Router) {
					r.Get("/", catalogAdmin.handleAdminListModels)
					r.Post("/", catalogAdmin.handleAdminCreateModel)
					// 静态子路径需先于 /{id} 注册，否则会被当作模型 ID 匹配
					r.Post("/import", catalogAdmin.handleAdminImportModels)
					r.Post("/import-remote", catalogAdmin.handleAdminImportRemote)
					r.Get("/pricing-presets", catalogAdmin.handleAdminPricingPresets)
					r.Get("/{id}", catalogAdmin.handleAdminGetModel)
					r.Put("/{id}", catalogAdmin.handleAdminUpdateModel)
					r.Delete("/{id}", catalogAdmin.handleAdminDeleteModel)
					r.Put("/{id}/price", catalogAdmin.handleAdminSetModelPrice)
					r.Put("/{id}/peak-rules", catalogAdmin.handleAdminSetPeakRules)
					r.Get("/{id}/channel-costs", reportsAdmin.handleAdminModelChannelCosts)
				})
				r.Route("/redemptions", func(r chi.Router) {
					r.Get("/", billingAdmin.handleAdminListRedemptions)
					r.Post("/batch", billingAdmin.handleAdminCreateRedemptions)
					r.Put("/{id}/status", billingAdmin.handleAdminSetRedemptionStatus)
				})
				r.Route("/alerts", func(r chi.Router) {
					r.Get("/", billingAdmin.handleAdminListAlerts)
					r.Post("/test", billingAdmin.handleAdminTestAlert)
				})
				r.Post("/users/import", userAdmin.handleAdminImportUsers)
				r.Post("/users/batch-status", userAdmin.handleAdminBatchSetUserStatus)
				r.Post("/credits/batch-grant", userAdmin.handleAdminBatchGrantCredits)
				r.Get("/setup-status", billingAdmin.handleAdminSetupStatus)
				r.Get("/stats/profit", reportsAdmin.handleAdminProfit)
				r.Get("/stats/overview", reportsAdmin.handleAdminStatsOverview)
			})

			// 托管桶：用户与部门的日常代管、流水与用量、成本口径报表、审计。
			// managed/admin/root 三种角色均放行；具体对象是否可管由各 handler 的
			// canManage / canAccessDepartment 逐次校验。
			r.Group(func(r chi.Router) {
				r.Use(mw.RequireAtLeast(domain.RoleManaged))
				r.Route("/users", func(r chi.Router) {
					r.Get("/", userAdmin.handleAdminListUsers)
					r.Post("/", userAdmin.handleAdminCreateUser)
					// 静态子路径须先于 /{id} 注册，否则会被当作用户 ID 匹配
					r.Get("/external/{ref}", extLookup.handleAdminGetUserByExternalRef)
					r.Get("/{id}", userAdmin.handleAdminGetUser)
					r.Put("/{id}", userAdmin.handleAdminUpdateUser)
					r.Delete("/{id}", userAdmin.handleAdminDeleteUser)
					r.Post("/{id}/status", userAdmin.handleAdminSetUserStatus)
					r.Post("/{id}/reset-password", userAdmin.handleAdminResetPassword)
					r.Get("/{id}/keys", userAdmin.handleAdminListUserKeys)
					r.Post("/{id}/keys", userAdmin.handleAdminCreateUserKey)
					r.Put("/{id}/keys/{key_id}", userAdmin.handleAdminUpdateUserKey)
					r.Delete("/{id}/keys/{key_id}", userAdmin.handleAdminDeleteUserKey)
					r.Post("/{id}/credits", billingAdmin.handleAdminGrantCredits)
				})
				r.Route("/departments", func(r chi.Router) {
					r.Get("/", orgAdmin.handleAdminListDepartments)
					r.Post("/", orgAdmin.handleAdminCreateDepartment)
					// 静态子路径须先于 /{id} 注册
					r.Get("/external/{ref}", extLookup.handleAdminGetDepartmentByExternalRef)
					r.Get("/{id}", orgAdmin.handleAdminGetDepartment)
					r.Put("/{id}", orgAdmin.handleAdminUpdateDepartment)
					r.Delete("/{id}", orgAdmin.handleAdminDeleteDepartment)
					r.Post("/{id}/members", orgAdmin.handleAdminSetDepartmentMembers)
				})
				r.Route("/projects", func(r chi.Router) {
					r.Get("/", catalogAdmin.handleAdminListProjects)
					r.Post("/", catalogAdmin.handleAdminCreateProject)
					// 静态子路径须先于 /{id} 注册
					r.Get("/external/{ref}", extLookup.handleAdminGetProjectByExternalRef)
					r.Get("/{id}", catalogAdmin.handleAdminGetProject)
					r.Put("/{id}", catalogAdmin.handleAdminUpdateProject)
					r.Delete("/{id}", catalogAdmin.handleAdminDeleteProject)
				})
				r.Get("/ledger", billingAdmin.handleAdminListLedger)
				r.Get("/usage-logs", reportsAdmin.handleAdminListUsageLogs)
				r.Get("/usage-logs/detail", reportsAdmin.handleAdminGetUsageLog)
				r.Get("/usage-logs/export", reportsAdmin.handleAdminExportUsageLogs)
				r.Route("/audit-logs", func(r chi.Router) {
					r.Get("/", orgAdmin.handleAdminListAuditLogs)
					r.Get("/actions", orgAdmin.handleAdminAuditActions)
				})
				r.Get("/stats/cost-report", reportsAdmin.handleAdminCostReport)
				r.Get("/stats/department-budget", reportsAdmin.handleAdminDepartmentBudget)
				r.Get("/stats/project-budget", reportsAdmin.handleAdminProjectBudget)
				r.Get("/stats/usage-daily", reportsAdmin.handleAdminUsageDaily)
				r.Get("/stats/calendar", reportsAdmin.handleAdminCalendar)
				r.Get("/stats/cache-report", reportsAdmin.handleAdminCacheReport)
				r.Get("/stats/runtime", reportsAdmin.handleAdminRuntime)
				r.Get("/stats/heatmap", reportsAdmin.handleAdminHeatmap)
				r.Get("/stats/health-timeline", reportsAdmin.handleAdminHealthTimeline)
				r.Get("/stats/ops-summary", reportsAdmin.handleAdminOpsSummary)
				r.Get("/stats/cost-by-calltype", reportsAdmin.handleAdminCostByCallType)
			})

			// root 桶：系统设置与接入方运营。
			r.Group(func(r chi.Router) {
				r.Use(mw.RequireAtLeast(domain.RoleRoot))
				r.Get("/settings", billingAdmin.handleAdminGetSettings)
				r.Put("/settings", billingAdmin.handleAdminUpdateSetting)
				// 接入方与服务令牌运营入口（批次 F）。静态子路径须先于 /{id} 注册，
				// 避免被参数路由吞掉。
				r.Route("/integrations", func(r chi.Router) {
					r.Get("/", integrationsAdmin.handleAdminListIntegrations)
					r.Post("/", integrationsAdmin.handleAdminCreateIntegration)
					// {id} 下的静态子路径先于 {id} 的参数子路径
					r.Post("/{id}/service-tokens", integrationsAdmin.handleAdminCreateServiceToken)
					r.Get("/{id}/service-tokens", integrationsAdmin.handleAdminListServiceTokens)
					r.Post("/{id}/disable", integrationsAdmin.handleAdminDisableIntegration)
					r.Get("/{id}", integrationsAdmin.handleAdminGetIntegration)
					r.Put("/{id}", integrationsAdmin.handleAdminUpdateIntegration)
					r.Put("/{id}/service-tokens/{token_id}/status", integrationsAdmin.handleAdminSetServiceTokenStatus)
					r.Delete("/{id}/service-tokens/{token_id}", integrationsAdmin.handleAdminDeleteServiceToken)
				})
			})
		})
	})

	// 下游 LLM API（API Key 认证）
	r.Route("/v1", func(r chi.Router) {
		r.Post("/chat/completions", relayCtl.handleV1ChatCompletions)
		r.Post("/messages", relayCtl.handleV1Messages)
		r.Post("/messages/count_tokens", relayCtl.handleV1CountTokens)
		r.Post("/embeddings", relayCtl.handleV1Embeddings)
		r.Post("/images/generations", relayCtl.handleV1Images)
		r.Get("/models", relayCtl.handleV1Models)
		r.Get("/key/info", relayCtl.handleV1KeyInfo)
	})

	// 下游 LLM API 的 provider 前缀入口（/{provider}/v1/*）：锁定候选渠道的厂商，
	// 仅同 provider 容错，不回退其他 provider。provider 与请求体 model 归属不一致时
	// 返回 model_provider_mismatch。slug 解析失败在认证/限流之前直接 404 provider_not_found。
	// 两类入口的认证、计费、限流、协议转换全链路一致；详见 relay_handlers.go。
	r.Route("/{provider}/v1", func(r chi.Router) {
		r.Post("/chat/completions", relayCtl.handleV1ProviderChatCompletions)
		r.Post("/messages", relayCtl.handleV1ProviderMessages)
		r.Post("/messages/count_tokens", relayCtl.handleV1ProviderCountTokens)
		r.Post("/embeddings", relayCtl.handleV1ProviderEmbeddings)
		r.Post("/images/generations", relayCtl.handleV1ProviderImages)
		r.Get("/models", relayCtl.handleV1ProviderModels)
		// key/info 是账号级端点，与 provider 无关；挂载以兼容把 base_url 设为
		// /{provider}/v1 的客户端，provider 前缀对该端点为 no-op（响应与 /v1/key/info 一致）。
		r.Get("/key/info", relayCtl.handleV1KeyInfo)
	})

	r.NotFound(func(w http.ResponseWriter, _ *http.Request) {
		respond.Fail(w, http.StatusNotFound, "接口不存在")
	})
	return r
}

func (s *systemController) handleHealthz(w http.ResponseWriter, r *http.Request) {
	sqlDB, err := s.DB.DB()
	if err == nil {
		err = sqlDB.PingContext(r.Context())
	}
	if err != nil {
		obs.Logger(r.Context()).Error("健康检查失败", "error", err)
		respond.Fail(w, http.StatusServiceUnavailable, "数据库不可用")
		return
	}
	// usage_log_dropped：用量日志队列累计丢弃条数，非零即应告警排查写入积压。
	respond.OK(w, map[string]any{
		"status":            "ok",
		"usage_log_dropped": s.Relay.DroppedUsageLogCount(),
	})
}
