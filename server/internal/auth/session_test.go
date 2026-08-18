package auth

import (
	"context"
	"testing"

	"github.com/alexedwards/scs/v2"
)

// newMemSessionManager 返回基于内存存储（scs 默认 memstore）的会话管理器，不依赖数据库。
func newMemSessionManager() *scs.SessionManager {
	return scs.New()
}

// createSession 在会话存储中创建一个会话并返回其 token。
// userID 为 0 时创建匿名会话（不写入 user_id，写入无关键以确保会话被持久化）。
func createSession(t *testing.T, sm *scs.SessionManager, userID int64) string {
	t.Helper()
	ctx, err := sm.Load(context.Background(), "")
	if err != nil {
		t.Fatalf("加载新会话失败: %v", err)
	}
	if userID != 0 {
		sm.Put(ctx, sessionUserIDKey, userID)
	} else {
		sm.Put(ctx, "locale", "zh-CN")
	}
	token, _, err := sm.Commit(ctx)
	if err != nil {
		t.Fatalf("提交会话失败: %v", err)
	}
	return token
}

// sessionExists 检查 token 对应的会话是否仍存在于存储中。
func sessionExists(t *testing.T, sm *scs.SessionManager, token string) bool {
	t.Helper()
	_, found, err := sm.Store.Find(token)
	if err != nil {
		t.Fatalf("查询会话存储失败: %v", err)
	}
	return found
}

// TestDestroyUserSessionsRevokesOnlyTargetUser 覆盖 P2-4 单元用例：
// 撤销指定用户的全部会话，不波及其他用户的会话。
func TestDestroyUserSessionsRevokesOnlyTargetUser(t *testing.T) {
	sm := newMemSessionManager()
	tokenA1 := createSession(t, sm, 1)
	tokenA2 := createSession(t, sm, 1)
	tokenB := createSession(t, sm, 2)

	if err := DestroyUserSessions(context.Background(), sm, 1); err != nil {
		t.Fatalf("DestroyUserSessions 返回错误: %v", err)
	}

	if sessionExists(t, sm, tokenA1) {
		t.Error("用户 A 的会话 1 应已失效，实际仍存在")
	}
	if sessionExists(t, sm, tokenA2) {
		t.Error("用户 A 的会话 2 应已失效，实际仍存在")
	}
	if !sessionExists(t, sm, tokenB) {
		t.Error("用户 B 的会话不应被波及，实际已被删除")
	}
}

// TestDestroyUserSessionsNoSessions 覆盖 P2-4 单元用例：
// 对无任何会话的用户调用不报错，其他会话不受影响。
func TestDestroyUserSessionsNoSessions(t *testing.T) {
	sm := newMemSessionManager()
	tokenB := createSession(t, sm, 2)

	if err := DestroyUserSessions(context.Background(), sm, 99); err != nil {
		t.Fatalf("对无会话用户调用应返回 nil，实际: %v", err)
	}
	if !sessionExists(t, sm, tokenB) {
		t.Error("无关用户的会话不应被删除")
	}
}

// TestDestroyUserSessionsSkipsAnonymous 覆盖 P2-4 单元用例：
// 会话池中存在未写入 user_id 的匿名会话时，撤销指定用户不误删匿名会话、遍历不报错。
func TestDestroyUserSessionsSkipsAnonymous(t *testing.T) {
	sm := newMemSessionManager()
	anonToken := createSession(t, sm, 0)
	userToken := createSession(t, sm, 1)

	if err := DestroyUserSessions(context.Background(), sm, 1); err != nil {
		t.Fatalf("存在匿名会话时遍历不应报错: %v", err)
	}
	if !sessionExists(t, sm, anonToken) {
		t.Error("匿名会话不应被误删")
	}
	if sessionExists(t, sm, userToken) {
		t.Error("目标用户会话应已失效")
	}
}

// Secure 标志必须原样传到 cookie 配置上：该标志决定浏览器是否保存会话 cookie，
// 与站点实际协议不一致时用户登录后立即掉登录态。
func TestNewSessionManagerAppliesSecureFlag(t *testing.T) {
	for _, secure := range []bool{true, false} {
		sm := NewSessionManager(nil, secure)
		if sm.Cookie.Secure != secure {
			t.Errorf("secure=%v 时 Cookie.Secure 应为 %v，实际 %v",
				secure, secure, sm.Cookie.Secure)
		}
		// HttpOnly 与 SameSite 不受该开关影响，明文部署下仍须保持。
		if !sm.Cookie.HttpOnly {
			t.Errorf("secure=%v 时 HttpOnly 不应被关闭", secure)
		}
	}
}
