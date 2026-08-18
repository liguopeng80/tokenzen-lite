package api

import (
	"fmt"
	"net/http"
	"testing"
)

// TestManagedIssueKeyUsable 覆盖验收 #5：托管服务令牌代用户签发 Key，
// 取得明文后凭该 Key 调 /v1/key/info 成功——证明签发的凭据可用，开通闭环闭合。
// （/v1/key/info 只鉴权并返回 Key 限额与余额，不需要真实上游，足以验证凭据可用。）
func TestManagedIssueKeyUsable(t *testing.T) {
	e := newTestEnv(t)
	token, _, _ := seedManagedToken(t, e, "aiwb3")

	// 1. 托管令牌建一个本接入方的无口令用户。
	resp, env := doWithToken(t, e, token, "POST", "/api/admin/users/",
		map[string]any{"username": "hosted-r2"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("建用户应 201，实际 %d %v", resp.StatusCode, env)
	}
	data, _ := env["data"].(map[string]any)
	userID := int64(data["id"].(float64))

	// 2. 代该用户签发 Key，取得明文与 Key id。
	resp, env = doWithToken(t, e, token, "POST",
		fmt.Sprintf("/api/admin/users/%d/keys", userID),
		map[string]string{"name": "issued"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("代签发 Key 应 201，实际 %d %v", resp.StatusCode, env)
	}
	keyData, _ := env["data"].(map[string]any)
	keyPlain, _ := keyData["key"].(string)
	if keyPlain == "" {
		t.Fatalf("代签发响应未返回明文 key")
	}
	keyID := int64(keyData["id"].(float64))

	// 3. 凭该 Key 调 /v1/key/info，应 200（Key 已生效、用户启用）。
	req, err := http.NewRequest("GET", e.srv.URL+"/v1/key/info", nil)
	if err != nil {
		t.Fatalf("构建请求失败: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+keyPlain)
	kresp, err := (&http.Client{}).Do(req)
	if err != nil {
		t.Fatalf("调用 /v1/key/info 失败: %v", err)
	}
	defer kresp.Body.Close()
	if kresp.StatusCode != http.StatusOK {
		t.Errorf("代签发的 Key 应能调通 /v1/key/info（200），实际 %d", kresp.StatusCode)
	}

	// 4. 停用该 Key 后同请求应 403 key_disabled。
	resp, _ = doWithToken(t, e, token, "PUT",
		fmt.Sprintf("/api/admin/users/%d/keys/%d", userID, keyID),
		map[string]string{"status": "disabled"})
	if resp.StatusCode != http.StatusOK {
		t.Errorf("停用 Key 应 200，实际 %d", resp.StatusCode)
	}
	req2, err := http.NewRequest("GET", e.srv.URL+"/v1/key/info", nil)
	if err != nil {
		t.Fatalf("构建请求失败: %v", err)
	}
	req2.Header.Set("Authorization", "Bearer "+keyPlain)
	kresp2, err := (&http.Client{}).Do(req2)
	if err != nil {
		t.Fatalf("调用 /v1/key/info 失败: %v", err)
	}
	defer kresp2.Body.Close()
	if kresp2.StatusCode != http.StatusForbidden {
		t.Errorf("停用后的 Key 调用应 403，实际 %d", kresp2.StatusCode)
	}
}
