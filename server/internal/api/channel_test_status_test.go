package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// 渠道连通测试端点 POST /api/admin/channels/{id}/test 在成功与失败两路径下
// 都正确写入 last_test_status（success / failure），供管理端列表展示。
func TestAdminTestChannelWritesLastTestStatus(t *testing.T) {
	e := newTestEnv(t)

	healthy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"x","model":"m","choices":[],"usage":{"prompt_tokens":1,"completion_tokens":1}}`)
	}))
	defer healthy.Close()
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"message":"invalid api key"}}`, http.StatusUnauthorized)
	}))
	defer failing.Close()

	okID := e.seedChannel(t, "ch-test-ok", healthy.URL, 0, []string{"glm-5"}, nil)
	badID := e.seedChannel(t, "ch-test-bad", failing.URL, 0, []string{"glm-5"}, nil)
	adminC := e.seedAndLogin(t, "chtestadmin", domain.RoleAdmin)

	// 成功路径：上游 200，应写入 success。
	resp, env := e.do(t, adminC, "POST", fmt.Sprintf("/api/admin/channels/%d/test", okID), nil)
	if resp.StatusCode != 200 {
		t.Fatalf("测试端点应 200，实际 %d", resp.StatusCode)
	}
	if ok, _ := env["data"].(map[string]any); ok == nil || ok["ok"] != true {
		t.Errorf("成功路径应返回 ok=true，实际 %v", env["data"])
	}
	var okCh store.Channel
	if err := e.db.First(&okCh, okID).Error; err != nil {
		t.Fatalf("查渠道失败: %v", err)
	}
	if okCh.LastTestStatus == nil || *okCh.LastTestStatus != string(domain.ChannelTestSuccess) {
		got := "<nil>"
		if okCh.LastTestStatus != nil {
			got = *okCh.LastTestStatus
		}
		t.Errorf("成功路径应写入 last_test_status=success，实际 %s", got)
	}
	if okCh.LastTestAt == nil {
		t.Error("成功路径应写入 last_test_at")
	}

	// 失败路径：上游 401，应写入 failure（先写 status 再 respond）。
	resp, env = e.do(t, adminC, "POST", fmt.Sprintf("/api/admin/channels/%d/test", badID), nil)
	if resp.StatusCode != 200 {
		t.Fatalf("测试端点应 200（业务失败仍用信封），实际 %d", resp.StatusCode)
	}
	if data, _ := env["data"].(map[string]any); data == nil || data["ok"] != false {
		t.Errorf("失败路径应返回 ok=false，实际 %v", env["data"])
	}
	var badCh store.Channel
	if err := e.db.First(&badCh, badID).Error; err != nil {
		t.Fatalf("查渠道失败: %v", err)
	}
	if badCh.LastTestStatus == nil || *badCh.LastTestStatus != string(domain.ChannelTestFailure) {
		got := "<nil>"
		if badCh.LastTestStatus != nil {
			got = *badCh.LastTestStatus
		}
		t.Errorf("失败路径应写入 last_test_status=failure，实际 %s", got)
	}
	if badCh.LastTestAt == nil {
		t.Error("失败路径也应写入 last_test_at")
	}
}
