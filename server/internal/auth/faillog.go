package auth

import (
	"context"
	"net/http"

	"github.com/liguopeng80/tokenzen-lite/server/internal/obs"
)

// 认证失败类型常量。仅写入服务端日志用于排查，客户端响应仍为统一 401/403 文案，
// 不向客户端回显失败类型，避免账号枚举（不区分「用户不存在」与「凭证错误」等）。
const (
	failSessionMissing      = "session_missing"      // session 中无 user_id（未登录/已过期）
	failUserNotFound        = "user_not_found"       // 会话或令牌指向的用户不存在
	failUserDisabled        = "user_disabled"        // 用户状态非 enabled
	failTokenInvalid        = "invalid_token"        // 服务令牌哈希查不到或解析错误
	failTokenDisabled       = "token_disabled"       // 服务令牌已停用
	failIntegrationMismatch = "integration_mismatch" // 服务账号 integration_id 与令牌归属不一致
)

// logAuthFail 输出结构化的认证失败日志。仅用于服务端排查，调用方仍返回原 401/403 文案。
// source ∈ {"session", "service_token"}；failType 见上方常量；extra 为附加的 key-value 对。
// request_id 由 obs.Logger 从 ctx 自动附加。
func logAuthFail(ctx context.Context, r *http.Request, source, failType string, extra ...any) {
	args := make([]any, 0, 8+len(extra))
	args = append(args,
		"source", source,
		"client_ip", obs.ClientIP(r),
		"fail_type", failType,
		"path", r.URL.Path,
	)
	args = append(args, extra...)
	obs.Logger(ctx).Warn("认证失败", args...)
}
