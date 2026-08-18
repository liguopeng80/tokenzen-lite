package api

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// 员工自助的密钥操作留痕：密钥泄漏或出现异常调用时，事后追查要能查出
// 这个密钥何时由谁创建、改过什么、何时删除。
func TestMeKeyOperationsAudited(t *testing.T) {
	e := newTestEnv(t)
	c := e.seedAndLogin(t, "keyauditor", domain.RoleUser)

	resp, env := e.do(t, c, "POST", "/api/me/keys/", map[string]any{"name": "追溯密钥"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("建密钥失败：%d %v", resp.StatusCode, env)
	}
	created := env["data"].(map[string]any)
	keyID := int64(created["id"].(float64))
	plain := created["key"].(string)

	createLog := latestAudit(t, e, domain.AuditAPIKeyCreate)
	if createLog.TargetType != domain.AuditTargetAPIKey || createLog.TargetID != keyID {
		t.Errorf("创建审计的对象应为该密钥，实际 %s #%d", createLog.TargetType, createLog.TargetID)
	}
	if createLog.OperatorName != "keyauditor" {
		t.Errorf("操作人应为密钥持有人，实际 %q", createLog.OperatorName)
	}
	// 快照不得含密钥明文或哈希。
	snapshot := string(createLog.AfterState)
	if strings.Contains(snapshot, plain) {
		t.Fatalf("审计快照泄漏密钥明文：%s", snapshot)
	}
	if !strings.Contains(snapshot, "key_prefix") {
		t.Errorf("快照应记录密钥前缀便于对号：%s", snapshot)
	}

	// 改名：审计应同时记下改前与改后。
	if resp, env := e.do(t, c, "PUT", fmt.Sprintf("/api/me/keys/%d", keyID),
		map[string]any{"name": "改名后的密钥"}); resp.StatusCode != http.StatusOK {
		t.Fatalf("改密钥失败：%d %v", resp.StatusCode, env)
	}
	updateLog := latestAudit(t, e, domain.AuditAPIKeyUpdate)
	if !strings.Contains(string(updateLog.BeforeState), "追溯密钥") {
		t.Errorf("应记录改动前的名称：%s", updateLog.BeforeState)
	}
	if !strings.Contains(string(updateLog.AfterState), "改名后的密钥") {
		t.Errorf("应记录改动后的名称：%s", updateLog.AfterState)
	}

	// 删除：审计留下删除前的快照。
	if resp, env := e.do(t, c, "DELETE", fmt.Sprintf("/api/me/keys/%d", keyID), nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("删密钥失败：%d %v", resp.StatusCode, env)
	}
	deleteLog := latestAudit(t, e, domain.AuditAPIKeyDelete)
	if deleteLog.TargetID != keyID {
		t.Errorf("删除审计的对象应为该密钥，实际 #%d", deleteLog.TargetID)
	}
	if !strings.Contains(string(deleteLog.BeforeState), "改名后的密钥") {
		t.Errorf("删除审计应留下删除前的快照：%s", deleteLog.BeforeState)
	}
}

// 删除密钥保留记录：用量日志里的 api_key_id 仍要能解析出这个密钥是谁的、叫什么，
// 否则事后追查会断线。同时删除后的密钥不再参与认证与列表。
func TestMeKeyDeleteIsSoftAndRevokesAccess(t *testing.T) {
	e := newTestEnv(t)
	c := e.seedAndLogin(t, "softdelkey", domain.RoleUser)

	resp, env := e.do(t, c, "POST", "/api/me/keys/", map[string]any{"name": "待删除密钥"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("建密钥失败：%d %v", resp.StatusCode, env)
	}
	keyID := int64(env["data"].(map[string]any)["id"].(float64))

	if resp, env := e.do(t, c, "DELETE", fmt.Sprintf("/api/me/keys/%d", keyID), nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("删密钥失败：%d %v", resp.StatusCode, env)
	}

	// 记录仍在库中，带删除时间。
	var row store.APIKey
	if err := e.db.Unscoped().First(&row, keyID).Error; err != nil {
		t.Fatalf("删除后记录应保留，实际查不到：%v", err)
	}
	if !row.DeletedAt.Valid {
		t.Error("删除后应带删除时间")
	}
	if row.Name != "待删除密钥" {
		t.Errorf("删除后名称应保留，实际 %q", row.Name)
	}

	// 常规查询不再命中：列表、单条读取都当作不存在。
	resp, env = e.do(t, c, "GET", "/api/me/keys/", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("查密钥列表失败：%d %v", resp.StatusCode, env)
	}
	if total := env["data"].(map[string]any)["total"].(float64); total != 0 {
		t.Errorf("已删除的密钥不应出现在列表中，实际 total=%v", total)
	}
	if resp, _ := e.do(t, c, "GET", fmt.Sprintf("/api/me/keys/%d", keyID), nil); resp.StatusCode != http.StatusNotFound {
		t.Errorf("读取已删除密钥应 404，实际 %d", resp.StatusCode)
	}

	// 删除的密钥不再占用数量上限。
	count, err := e.deps.Keys.CountByUser(t.Context(), row.UserID)
	if err != nil {
		t.Fatalf("统计密钥数失败：%v", err)
	}
	if count != 0 {
		t.Errorf("已删除的密钥不应计入数量上限，实际 %d", count)
	}
}

// 删除的密钥不能再用于调用：软删除只保留档案，不保留访问能力。
func TestDeletedKeyRejectedByRelayAuth(t *testing.T) {
	e := newTestEnv(t)
	c := e.seedAndLogin(t, "softdelrelay", domain.RoleUser)
	resp, env := e.do(t, c, "POST", "/api/me/keys/", map[string]any{"name": "调用密钥"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("建密钥失败：%d %v", resp.StatusCode, env)
	}
	created := env["data"].(map[string]any)
	keyID := int64(created["id"].(float64))
	plain := created["key"].(string)

	if resp, _ := e.do(t, c, "DELETE", fmt.Sprintf("/api/me/keys/%d", keyID), nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("删密钥失败：%d", resp.StatusCode)
	}

	status, _ := e.postV1(t, plain, "/v1/chat/completions", map[string]any{
		"model": "any-model", "messages": []map[string]string{{"role": "user", "content": "hi"}},
	})
	if status != http.StatusUnauthorized {
		t.Fatalf("已删除的密钥应认证失败（401），实际 %d", status)
	}
}
