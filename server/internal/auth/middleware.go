package auth

import (
	"context"
	"net/http"
	"strings"

	"github.com/alexedwards/scs/v2"

	"github.com/liguopeng80/tokenzen-lite/server/internal/api/respond"
	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/obs"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

type ctxKey int

const (
	currentUserKey ctxKey = 0
	scopeKey       ctxKey = 1
)

// CurrentUser 从 ctx 取当前登录用户；不存在返回 nil。
func CurrentUser(ctx context.Context) *store.User {
	u, _ := ctx.Value(currentUserKey).(*store.User)
	return u
}

// WithUser 将用户写入 ctx（认证中间件与测试使用）。
func WithUser(ctx context.Context, u *store.User) context.Context {
	return context.WithValue(ctx, currentUserKey, u)
}

// WithScope 把当前请求的作用域（接入方 integration_id）写入 ctx。
// 仅托管管理员有作用域；运营方 admin/root 不写入（视角不限作用域）。
func WithScope(ctx context.Context, integrationID int64) context.Context {
	return context.WithValue(ctx, scopeKey, integrationID)
}

// ScopeIntegrationID 返回 ctx 中的作用域 integration_id。nil 表示运营方视角（不限作用域），
// 非 nil 表示托管视角，应作为各查询过滤的 integration_id。
func ScopeIntegrationID(ctx context.Context) *int64 {
	v, _ := ctx.Value(scopeKey).(int64)
	if v == 0 {
		return nil
	}
	id := v
	return &id
}

// Middleware 提供 session 认证与角色门禁。
type Middleware struct {
	Sessions *scs.SessionManager
	Users    *store.UserRepo
	// Integrations 与 ServiceTokens 用于管理端服务令牌认证；nil 时管理端只接受会话认证。
	Integrations  *store.IntegrationRepo
	ServiceTokens *store.ServiceTokenRepo
}

// RequireRole 返回要求最低角色的中间件；加载用户注入 ctx。
func (m *Middleware) RequireRole(min domain.Role) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			uid := SessionUserID(m.Sessions, r)
			if uid == 0 {
				logAuthFail(r.Context(), r, "session", failSessionMissing)
				respond.Fail(w, http.StatusUnauthorized, "未登录或登录已过期")
				return
			}
			u, err := m.Users.GetByID(r.Context(), uid)
			if err != nil {
				logAuthFail(r.Context(), r, "session", failUserNotFound, "user_id", uid, "error", err.Error())
				respond.Fail(w, http.StatusUnauthorized, "未登录或登录已过期")
				return
			}
			if u.Status != domain.UserEnabled {
				logAuthFail(r.Context(), r, "session", failUserDisabled, "user_id", u.ID)
				respond.Fail(w, http.StatusForbidden, "账号已被禁用")
				return
			}
			if !u.Role.AtLeast(min) {
				obs.Logger(r.Context()).Warn("越权访问被拒绝",
					"user_id", u.ID, "role", u.Role, "required", min, "path", r.URL.Path)
				respond.Fail(w, http.StatusForbidden, "权限不足")
				return
			}
			next.ServeHTTP(w, r.WithContext(WithUser(r.Context(), u)))
		})
	}
}

// RequirePasswordChanged 拒绝尚未完成首次改密的用户访问业务接口。
//
// 须叠加在 RequireRole 之后：当前用户由后者注入上下文。放行范围是 /api/auth 下的
// 自身操作（读取自身、改密、更新资料、登出）——初始密码由管理员设定并经转达，在本人改掉之前
// 它同时存在于管理员与传递链路上，此时允许创建 API Key 会让该密钥的实际持有者
// 无法认定。/v1 下游调用不受本中间件约束：那里的凭证是 API Key，与密码无关，
// 阻断它只会让管理员重置密码这一动作顺带中断员工正在跑的任务。
func (m *Middleware) RequirePasswordChanged(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if u := CurrentUser(r.Context()); u != nil && u.MustChangePassword {
			obs.Logger(r.Context()).Warn("未完成首次改密，接口访问被拒绝",
				"user_id", u.ID, "path", r.URL.Path)
			respond.Fail(w, http.StatusForbidden, "请先修改初始密码")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RequireAtLeast 要求上游中间件（AdminAuth）已注入的用户角色不低于 min。
// 与 RequireRole 的区别：不重新加载会话，直接读注入的 CurrentUser，
// 用于管理端服务令牌与会话两种认证方式合流后的按桶分流。
func (m *Middleware) RequireAtLeast(min domain.Role) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			u := CurrentUser(r.Context())
			if u == nil {
				logAuthFail(r.Context(), r, "session", failSessionMissing)
				respond.Fail(w, http.StatusUnauthorized, "未登录或登录已过期")
				return
			}
			if !u.Role.AtLeast(min) {
				obs.Logger(r.Context()).Warn("越权访问被拒绝",
					"user_id", u.ID, "role", u.Role, "required", min, "path", r.URL.Path)
				respond.Fail(w, http.StatusForbidden, "权限不足")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// AdminAuth 是管理端的双源认证：接入方服务令牌（Authorization: Bearer tzs-…）或运营方会话。
// 两种来源都把识别出的用户注入 ctx；托管服务令牌同时写入作用域。后续 RequireAtLeast 按角色
// 分桶，RequirePasswordChanged 按注入用户的 must_change 校验（服务账号 must_change=false 自动放行）。
// 服务令牌不经会话，因此不受会话过期与 must_change_password 门槛影响。
// 服务令牌访问其角色不达标的桶（如渠道、定价）时，在此已被认证，由 RequireAtLeast 返回 403，
// 而非把已认证的令牌当作未认证返回 401。
func (m *Middleware) AdminAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if token := bearerServiceToken(r); token != "" && m.ServiceTokens != nil {
			u, reason := m.authenticateServiceToken(r.Context(), token)
			if u == nil {
				logAuthFail(r.Context(), r, "service_token", reason)
				respond.Fail(w, http.StatusUnauthorized, "服务令牌无效或已停用")
				return
			}
			ctx := WithUser(r.Context(), u)
			if u.IntegrationID != nil {
				ctx = WithScope(ctx, *u.IntegrationID)
			}
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}
		uid := SessionUserID(m.Sessions, r)
		if uid == 0 {
			logAuthFail(r.Context(), r, "session", failSessionMissing)
			respond.Fail(w, http.StatusUnauthorized, "未登录或登录已过期")
			return
		}
		u, err := m.Users.GetByID(r.Context(), uid)
		if err != nil {
			logAuthFail(r.Context(), r, "session", failUserNotFound, "user_id", uid, "error", err.Error())
			respond.Fail(w, http.StatusUnauthorized, "未登录或登录已过期")
			return
		}
		if u.Status != domain.UserEnabled {
			logAuthFail(r.Context(), r, "session", failUserDisabled, "user_id", u.ID)
			respond.Fail(w, http.StatusForbidden, "账号已被禁用")
			return
		}
		next.ServeHTTP(w, r.WithContext(WithUser(r.Context(), u)))
	})
}

// bearerServiceToken 从 Authorization 头取服务令牌明文；非 tzs- 前缀返回空串。
func bearerServiceToken(r *http.Request) string {
	h := strings.TrimSpace(r.Header.Get("Authorization"))
	h = strings.TrimPrefix(h, "Bearer ")
	h = strings.TrimPrefix(h, "bearer ")
	if LooksLikeServiceToken(h) {
		return h
	}
	return ""
}

// authenticateServiceToken 校验服务令牌并解析其代表的服务账号用户。
// 返回 (user, failReason)：成功时 failReason 为空，失败时 failReason 用于服务端日志。
// 客户端响应仍由调用方统一回 401，不向客户端区分「令牌不存在」与「已停用」等，避免账号枚举。
func (m *Middleware) authenticateServiceToken(ctx context.Context, token string) (*store.User, string) {
	st, err := m.ServiceTokens.GetByHash(ctx, HashKey(token))
	if err != nil || st == nil {
		return nil, failTokenInvalid
	}
	if st.Status != domain.ServiceTokenEnabled {
		return nil, failTokenDisabled
	}
	u, err := m.Users.GetServiceAccount(ctx, st.IntegrationID)
	if err != nil || u == nil {
		return nil, failUserNotFound
	}
	if u.Status != domain.UserEnabled {
		return nil, failUserDisabled
	}
	// 服务账号的 integration_id 必须与令牌归属一致（停用级联后可能短暂不一致，拒绝）。
	if u.IntegrationID == nil || *u.IntegrationID != st.IntegrationID {
		return nil, failIntegrationMismatch
	}
	return u, ""
}
