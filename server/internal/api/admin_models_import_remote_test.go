package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// remoteCatalog 构造一份最小可用的远程 PresetCatalog JSON。
func remoteCatalog() map[string]any {
	return map[string]any{
		"priced_at": "2026-08",
		"note":      "测试远程价目",
		"providers": []map[string]any{
			{
				"id": "anthropic", "name": "Anthropic",
				"pricing_url": "https://example.com/pricing",
				"models": []map[string]any{
					{
						"name": "remote-claude", "display_name": "Remote Claude",
						"modality": "text", "billing_mode": "per_token",
						"provider": "anthropic", "context_window": 200000,
						"capabilities": []string{"vision"},
						"input_usd":    3000000, "output_usd": 15000000,
					},
				},
			},
		},
	}
}

// TestImportRemoteSuccess 远程价目拉取成功后逐条导入，复用 importOneModel 的写入逻辑。
func TestImportRemoteSuccess(t *testing.T) {
	e := newTestEnv(t)
	c := e.seedAndLogin(t, "remote-admin", domain.RoleAdmin)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(remoteCatalog())
	}))
	defer srv.Close()

	resp, env := e.do(t, c, "POST", "/api/admin/models/import-remote", map[string]any{
		"source_url": srv.URL, "markup_percent": 100, "overwrite": true,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("远程导入应 200，实际 %d %v", resp.StatusCode, env)
	}
	data, _ := env["data"].(map[string]any)
	created, _ := data["created"].(float64)
	failed, _ := data["failed"].(float64)
	if created != 1 || failed != 0 {
		t.Fatalf("期望 created=1 failed=0，实际 created=%v failed=%v", created, failed)
	}

	// 验证导入的模型携带新字段。
	var m store.Model
	if err := e.db.Where("name = ?", "remote-claude").First(&m).Error; err != nil {
		t.Fatalf("远程导入的模型未落库: %v", err)
	}
	if m.Provider != "anthropic" {
		t.Errorf("provider 应为 anthropic，实际 %s", m.Provider)
	}
	if m.ContextWindow != 200000 {
		t.Errorf("context_window 应为 200000，实际 %d", m.ContextWindow)
	}
}

// TestImportRemoteNon2xx 远程端点返回非 2xx 时拒绝导入。
func TestImportRemoteNon2xx(t *testing.T) {
	e := newTestEnv(t)
	c := e.seedAndLogin(t, "remote-admin-err", domain.RoleAdmin)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	resp, _ := e.do(t, c, "POST", "/api/admin/models/import-remote", map[string]any{
		"source_url": srv.URL, "markup_percent": 100,
	})
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("远程 500 应 502，实际 %d", resp.StatusCode)
	}
}

// TestImportRemoteBadJSON 远程响应不是合法 JSON 时返回 400。
func TestImportRemoteBadJSON(t *testing.T) {
	e := newTestEnv(t)
	c := e.seedAndLogin(t, "remote-admin-bad", domain.RoleAdmin)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("{not valid json"))
	}))
	defer srv.Close()

	resp, _ := e.do(t, c, "POST", "/api/admin/models/import-remote", map[string]any{
		"source_url": srv.URL, "markup_percent": 100,
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("非法 JSON 应 400，实际 %d", resp.StatusCode)
	}
}

// TestImportRemoteNonHTTPScheme source_url 非 http(s) scheme 时拒绝（防 file://、ftp:// 等）。
func TestImportRemoteNonHTTPScheme(t *testing.T) {
	e := newTestEnv(t)
	c := e.seedAndLogin(t, "remote-admin-scheme", domain.RoleAdmin)

	for _, rawURL := range []string{"file:///etc/passwd", "ftp://example.com/cat.json", ""} {
		resp, _ := e.do(t, c, "POST", "/api/admin/models/import-remote", map[string]any{
			"source_url": rawURL, "markup_percent": 100,
		})
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("source_url %q 应 400，实际 %d", rawURL, resp.StatusCode)
		}
	}
}
