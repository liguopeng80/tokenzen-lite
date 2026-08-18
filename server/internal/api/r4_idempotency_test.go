package api

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// TestIdempotentCreateUserAndKey 覆盖验收 #7：同一 idempotency_key 重复提交建用户与签发 Key，
// 各只生效一次，第二次返回首次结果并标明重放。
func TestIdempotentCreateUserAndKey(t *testing.T) {
	e := newTestEnv(t)
	token, _, _ := seedManagedToken(t, e, "aiwb4")

	// 1. 重复建用户。
	body := map[string]any{
		"username": "idem-user", "external_ref": "idem1", "idempotency_key": "retry-user-1",
	}
	resp1, env1 := doWithToken(t, e, token, "POST", "/api/admin/users/", body)
	if resp1.StatusCode != http.StatusCreated {
		t.Fatalf("首次建用户应 201，实际 %d %v", resp1.StatusCode, env1)
	}
	userID := int64(env1["data"].(map[string]any)["id"].(float64))

	resp2, env2 := doWithToken(t, e, token, "POST", "/api/admin/users/", body)
	if resp2.StatusCode != http.StatusOK {
		t.Errorf("重放建用户应 200，实际 %d", resp2.StatusCode)
	}
	if msg, _ := env2["message"].(string); !strings.Contains(msg, "重放") {
		t.Errorf("重放应在 message 标明，实际 %q", msg)
	}
	if replayID := int64(env2["data"].(map[string]any)["id"].(float64)); replayID != userID {
		t.Errorf("重放应返回首次用户 id %d，实际 %d", userID, replayID)
	}
	var n int64
	if err := e.db.Raw("SELECT count(*) FROM users WHERE username = 'idem-user'").Row().Scan(&n); err != nil {
		t.Fatalf("查询用户数失败: %v", err)
	}
	if n != 1 {
		t.Errorf("重复提交应只建一个用户，实际 %d", n)
	}

	// 2. 重复签发 Key。
	kbody := map[string]any{"name": "idem-key", "idempotency_key": "retry-key-1"}
	kresp1, kenv1 := doWithToken(t, e, token, "POST",
		fmt.Sprintf("/api/admin/users/%d/keys", userID), kbody)
	if kresp1.StatusCode != http.StatusCreated {
		t.Fatalf("首次签发 Key 应 201，实际 %d %v", kresp1.StatusCode, kenv1)
	}
	keyID := int64(kenv1["data"].(map[string]any)["id"].(float64))
	kresp2, kenv2 := doWithToken(t, e, token, "POST",
		fmt.Sprintf("/api/admin/users/%d/keys", userID), kbody)
	if kresp2.StatusCode != http.StatusOK {
		t.Errorf("重放签发应 200，实际 %d", kresp2.StatusCode)
	}
	if msg, _ := kenv2["message"].(string); !strings.Contains(msg, "重放") {
		t.Errorf("重放签发应标明，实际 %q", msg)
	}
	if replayKeyID := int64(kenv2["data"].(map[string]any)["id"].(float64)); replayKeyID != keyID {
		t.Errorf("重放应返回首次 key id %d，实际 %d", keyID, replayKeyID)
	}
	var kn int64
	if err := e.db.Raw("SELECT count(*) FROM api_keys WHERE user_id = ? AND deleted_at IS NULL", userID).Row().Scan(&kn); err != nil {
		t.Fatalf("查询密钥数失败: %v", err)
	}
	if kn != 1 {
		t.Errorf("重复提交应只签一个 key，实际 %d", kn)
	}

	// 3. 非法幂等键格式应 400。
	bad := map[string]any{"username": "idem-bad", "idempotency_key": "含空格 的键"}
	bresp, _ := doWithToken(t, e, token, "POST", "/api/admin/users/", bad)
	if bresp.StatusCode != http.StatusBadRequest {
		t.Errorf("非法幂等键应 400，实际 %d", bresp.StatusCode)
	}
}
