package api

import (
	"fmt"
	"testing"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// 管理端按用户名筛选用量日志：管理员手上有的是用户名，
// 不该先去用户管理页把 ID 查出来再回来填。
func TestAdminUsageLogsUsernameFilter(t *testing.T) {
	e := newTestEnv(t)
	rootC := e.seedAndLogin(t, "nameroot", domain.RoleRoot)
	e.seedAndLogin(t, "zhangsan", domain.RoleUser)
	userLiC := e.seedAndLogin(t, "lisi", domain.RoleUser)
	idZhang := e.userIDByName(t, "zhangsan")
	idLi := e.userIDByName(t, "lisi")

	for i := 0; i < 2; i++ {
		e.seedUsageLog(t, store.UsageLog{
			RequestID: fmt.Sprintf("name-zhang-%d", i), UserID: idZhang, APIKeyID: 1,
			ModelName: "glm-5", CreditsCharged: 100,
		})
	}
	e.seedUsageLog(t, store.UsageLog{
		RequestID: "name-li-0", UserID: idLi, APIKeyID: 2,
		ModelName: "glm-5", CreditsCharged: 200,
	})

	adminItems := func(t *testing.T, query string) []any {
		t.Helper()
		resp, env := e.do(t, rootC, "GET", "/api/admin/usage-logs"+query, nil)
		if resp.StatusCode != 200 {
			t.Fatalf("查询 %q 应 200，实际 %d %v", query, resp.StatusCode, env)
		}
		return pageItems(t, env)
	}

	items := adminItems(t, "?username=zhangsan")
	if len(items) != 2 {
		t.Fatalf("按 zhangsan 筛选应得 2 条，实际 %d", len(items))
	}
	for _, it := range items {
		if uid := int64(it.(map[string]any)["user_id"].(float64)); uid != idZhang {
			t.Errorf("按用户名筛选返回了他人日志 user_id=%d", uid)
		}
	}

	// 模糊匹配：只输入片段也能命中。
	if got := len(adminItems(t, "?username=zhang")); got != 2 {
		t.Errorf("按片段 zhang 筛选应得 2 条，实际 %d", got)
	}
	if got := len(adminItems(t, "?username=lisi")); got != 1 {
		t.Errorf("按 lisi 筛选应得 1 条，实际 %d", got)
	}
	if got := len(adminItems(t, "?username=nobody")); got != 0 {
		t.Errorf("无匹配用户名应得 0 条，实际 %d", got)
	}
	// 用户名与 user_id 同时给出时条件叠加，互相矛盾则为空集。
	if got := len(adminItems(t, fmt.Sprintf("?username=zhangsan&user_id=%d", idLi))); got != 0 {
		t.Errorf("用户名与 user_id 矛盾时应得 0 条，实际 %d", got)
	}

	// 员工侧不接受该参数：那里的用户维度已强制限定为本人。
	resp, env := e.do(t, userLiC, "GET", "/api/me/usage-logs?username=zhangsan", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("员工查询自身日志应 200，实际 %d %v", resp.StatusCode, env)
	}
	items = pageItems(t, env)
	if len(items) != 1 {
		t.Fatalf("员工携带他人用户名应仍只看到自己的 1 条，实际 %d", len(items))
	}
	if uid := int64(items[0].(map[string]any)["user_id"].(float64)); uid != idLi {
		t.Errorf("员工侧返回了他人日志 user_id=%d", uid)
	}
}
