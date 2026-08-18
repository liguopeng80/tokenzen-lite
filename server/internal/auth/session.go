package auth

import (
	"context"
	"database/sql"
	"net/http"
	"time"

	"github.com/alexedwards/scs/postgresstore"
	"github.com/alexedwards/scs/v2"
)

const sessionUserIDKey = "user_id"

// NewSessionManager 构建基于 PostgreSQL 的 session 管理器。
// secure 为 true 时 cookie 仅经 HTTPS 传输（生产环境）。
func NewSessionManager(sqlDB *sql.DB, secure bool) *scs.SessionManager {
	sm := scs.New()
	sm.Store = postgresstore.New(sqlDB)
	sm.Lifetime = 7 * 24 * time.Hour
	sm.Cookie.Name = "tzl_session"
	sm.Cookie.HttpOnly = true
	sm.Cookie.SameSite = http.SameSiteLaxMode
	sm.Cookie.Secure = secure
	return sm
}

// LoginSession 将用户 ID 写入 session（登录成功后调用）。
// 先轮换 token 防 session fixation。
func LoginSession(sm *scs.SessionManager, r *http.Request, userID int64) error {
	if err := sm.RenewToken(r.Context()); err != nil {
		return err
	}
	sm.Put(r.Context(), sessionUserIDKey, userID)
	return nil
}

// LogoutSession 销毁当前 session。
func LogoutSession(sm *scs.SessionManager, r *http.Request) error {
	return sm.Destroy(r.Context())
}

// SessionUserID 读取 session 中的用户 ID；未登录返回 0。
func SessionUserID(sm *scs.SessionManager, r *http.Request) int64 {
	return sm.GetInt64(r.Context(), sessionUserIDKey)
}

// DestroyUserSessions 作废指定用户的全部登录会话（密码变更成功后调用）。
// 遍历会话存储，删除 user_id 匹配的会话；当前请求的会话同样会被删除，
// 调用方如需保持当前登录，须随后调用 LoginSession 重新签发。
func DestroyUserSessions(ctx context.Context, sm *scs.SessionManager, userID int64) error {
	return sm.Iterate(ctx, func(c context.Context) error {
		if sm.GetInt64(c, sessionUserIDKey) == userID {
			return sm.Destroy(c)
		}
		return nil
	})
}
