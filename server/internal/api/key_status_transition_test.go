package api

// --- API Key depleted（额度耗尽）状态转换测试（评审整改 P3-6 / 决策 2）---
//
// 写入路径：预扣命中密钥额度上限时状态置为 depleted（仅从 enabled 转换）。
// 恢复路径：额度上限被上调至高于已用额度、或被清除时恢复为 enabled；
// 新上限不高于已用额度时保持 depleted；手工禁用的密钥不受该路径影响。

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/relay"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// seedLimitedKeyUser 创建用户（分配积分并保持登录）+ 设独立额度上限的密钥，
// 返回登录客户端、密钥 ID 与密钥明文。
func (e *testEnv) seedLimitedKeyUser(t *testing.T, username string,
	credits, keyLimit int64) (*http.Client, int64, string) {
	t.Helper()
	c := e.seedAndLogin(t, username, domain.RoleUser)
	var u store.User
	if err := e.db.Where("username = ?", username).First(&u).Error; err != nil {
		t.Fatalf("查用户失败: %v", err)
	}
	if credits > 0 {
		if _, err := e.deps.Billing.Grant(context.Background(), u.ID, credits, 0, "测试额度", ""); err != nil {
			t.Fatalf("分配积分失败: %v", err)
		}
	}
	resp, env := e.do(t, c, "POST", "/api/me/keys/",
		map[string]any{"name": "limited-key", "credit_limit": keyLimit})
	if resp.StatusCode != 201 {
		t.Fatalf("建 Key 失败: %d %v", resp.StatusCode, env)
	}
	data := env["data"].(map[string]any)
	return c, int64(data["id"].(float64)), data["key"].(string)
}

// keyStatus 读取密钥当前状态。
func (e *testEnv) keyStatus(t *testing.T, keyID int64) domain.KeyStatus {
	t.Helper()
	var k store.APIKey
	if err := e.db.First(&k, keyID).Error; err != nil {
		t.Fatalf("查密钥失败: %v", err)
	}
	return k.Status
}

// 预扣命中额度上限：密钥状态写入 depleted，后续调用在认证层被拒绝。
func TestKeyDepletedWrittenOnPrechargeLimitHit(t *testing.T) {
	e := newTestEnv(t)
	e.seedModel(t, "glm-5")
	_, keyID, plainKey := e.seedLimitedKeyUser(t, "depleted-write", 1_000_000, 100) // 上限远低于预扣需求

	resp, body := e.relayPost(t, plainKey, map[string]any{
		"model": "glm-5", "messages": []map[string]any{{"role": "user", "content": "hi"}},
	})
	if resp.StatusCode != 402 || !strings.Contains(string(body), "key_quota_exceeded") {
		t.Fatalf("预扣命中上限应 402 key_quota_exceeded，实际 %d %s", resp.StatusCode, body)
	}
	if got := e.keyStatus(t, keyID); got != domain.KeyDepleted {
		t.Fatalf("预扣命中上限后状态应为 depleted，实际 %s", got)
	}

	// 状态已 depleted：再次调用在认证层拒绝，不再进入计费
	resp, body = e.relayPost(t, plainKey, map[string]any{
		"model": "glm-5", "messages": []map[string]any{{"role": "user", "content": "hi"}},
	})
	if resp.StatusCode != 403 || !strings.Contains(string(body), "key_unavailable") {
		t.Fatalf("depleted 密钥应 403 key_unavailable，实际 %d %s", resp.StatusCode, body)
	}
	e.assertReconcile(t)
}

// 额度上限调整驱动的恢复路径：上调不足额不恢复，上调足额恢复，清除上限恢复。
func TestKeyDepletedRestoredByLimitAdjust(t *testing.T) {
	e := newTestEnv(t)
	e.seedModel(t, "glm-5")
	c, keyID, plainKey := e.seedLimitedKeyUser(t, "depleted-restore", 1_000_000, 100)
	keyPath := fmt.Sprintf("/api/me/keys/%d", keyID)

	// 模拟历史累计消费已占满上限，任意预扣都会命中上限
	if err := e.db.Exec(`UPDATE api_keys SET credit_used = 100 WHERE id = ?`, keyID).Error; err != nil {
		t.Fatalf("预置已用额度失败: %v", err)
	}
	if resp, body := e.relayPost(t, plainKey, map[string]any{
		"model": "glm-5", "messages": []map[string]any{{"role": "user", "content": "hi"}},
	}); resp.StatusCode != 402 {
		t.Fatalf("预扣应命中上限 402，实际 %d %s", resp.StatusCode, body)
	}
	if got := e.keyStatus(t, keyID); got != domain.KeyDepleted {
		t.Fatalf("前置条件失败：状态应为 depleted，实际 %s", got)
	}

	// 边界：新上限等于已用额度（无剩余额度）→ 保持 depleted
	if resp, env := e.do(t, c, "PUT", keyPath, map[string]any{"credit_limit": 100}); resp.StatusCode != 200 {
		t.Fatalf("调整额度应 200，实际 %d %v", resp.StatusCode, env)
	}
	if got := e.keyStatus(t, keyID); got != domain.KeyDepleted {
		t.Fatalf("上限等于已用额度时应保持 depleted，实际 %s", got)
	}

	// 上调至高于已用额度 → 恢复 enabled
	if resp, env := e.do(t, c, "PUT", keyPath, map[string]any{"credit_limit": 200}); resp.StatusCode != 200 {
		t.Fatalf("上调额度应 200，实际 %d %v", resp.StatusCode, env)
	}
	if got := e.keyStatus(t, keyID); got != domain.KeyEnabled {
		t.Fatalf("上限上调至高于已用额度后应恢复 enabled，实际 %s", got)
	}

	// 再次耗尽后清除上限 → 恢复 enabled
	if err := e.db.Exec(`UPDATE api_keys SET credit_used = 200 WHERE id = ?`, keyID).Error; err != nil {
		t.Fatalf("预置已用额度失败: %v", err)
	}
	if resp, body := e.relayPost(t, plainKey, map[string]any{
		"model": "glm-5", "messages": []map[string]any{{"role": "user", "content": "hi"}},
	}); resp.StatusCode != 402 {
		t.Fatalf("预扣应再次命中上限 402，实际 %d %s", resp.StatusCode, body)
	}
	if got := e.keyStatus(t, keyID); got != domain.KeyDepleted {
		t.Fatalf("前置条件失败：状态应为 depleted，实际 %s", got)
	}
	if resp, env := e.do(t, c, "PUT", keyPath, map[string]any{"clear_limit": true}); resp.StatusCode != 200 {
		t.Fatalf("清除上限应 200，实际 %d %v", resp.StatusCode, env)
	}
	if got := e.keyStatus(t, keyID); got != domain.KeyEnabled {
		t.Fatalf("清除上限后应恢复 enabled，实际 %s", got)
	}
	e.assertReconcile(t)
}

// 仅更新无关字段（名称）不触发恢复：恢复逻辑只挂在额度上限变更上。
func TestKeyDepletedNotRestoredByUnrelatedUpdate(t *testing.T) {
	e := newTestEnv(t)
	e.seedModel(t, "glm-5")
	c, keyID, plainKey := e.seedLimitedKeyUser(t, "depleted-rename", 1_000_000, 100)

	if resp, body := e.relayPost(t, plainKey, map[string]any{
		"model": "glm-5", "messages": []map[string]any{{"role": "user", "content": "hi"}},
	}); resp.StatusCode != 402 {
		t.Fatalf("预扣应命中上限 402，实际 %d %s", resp.StatusCode, body)
	}
	if got := e.keyStatus(t, keyID); got != domain.KeyDepleted {
		t.Fatalf("前置条件失败：状态应为 depleted，实际 %s", got)
	}

	keyPath := fmt.Sprintf("/api/me/keys/%d", keyID)
	if resp, env := e.do(t, c, "PUT", keyPath, map[string]any{"name": "renamed"}); resp.StatusCode != 200 {
		t.Fatalf("改名应 200，实际 %d %v", resp.StatusCode, env)
	}
	if got := e.keyStatus(t, keyID); got != domain.KeyDepleted {
		t.Fatalf("仅改名不得恢复状态，应保持 depleted，实际 %s", got)
	}
}

// 误伤防护：密钥限额充足但用户积分余额不足 → 402 insufficient_credits，
// 密钥状态必须保持 enabled（余额不足不得写 depleted）。
func TestKeyStaysEnabledOnInsufficientUserCredits(t *testing.T) {
	e := newTestEnv(t)
	e.seedModel(t, "glm-5")
	// 用户 0 积分；密钥上限远高于预扣需求
	_, keyID, plainKey := e.seedLimitedKeyUser(t, "poor-user", 0, 10_000_000)

	resp, body := e.relayPost(t, plainKey, map[string]any{
		"model": "glm-5", "messages": []map[string]any{{"role": "user", "content": "hi"}},
	})
	if resp.StatusCode != 402 || !strings.Contains(string(body), "insufficient_credits") {
		t.Fatalf("余额不足应 402 insufficient_credits，实际 %d %s", resp.StatusCode, body)
	}
	if got := e.keyStatus(t, keyID); got != domain.KeyEnabled {
		t.Fatalf("余额不足不得改写密钥状态，应保持 enabled，实际 %s", got)
	}
	e.assertReconcile(t)
}

// 手工启用闭环：用户对 depleted 密钥直接 PUT status=enabled 被允许；
// 随后调用再次命中上限 402 并重新写回 depleted——系统判定与手工切换互不破坏。
func TestKeyDepletedManualEnableThenRedepleted(t *testing.T) {
	e := newTestEnv(t)
	e.seedModel(t, "glm-5")
	c, keyID, plainKey := e.seedLimitedKeyUser(t, "manual-enable", 1_000_000, 100)
	relayBody := map[string]any{
		"model": "glm-5", "messages": []map[string]any{{"role": "user", "content": "hi"}},
	}

	if resp, body := e.relayPost(t, plainKey, relayBody); resp.StatusCode != 402 {
		t.Fatalf("预扣应命中上限 402，实际 %d %s", resp.StatusCode, body)
	}
	if got := e.keyStatus(t, keyID); got != domain.KeyDepleted {
		t.Fatalf("前置条件失败：状态应为 depleted，实际 %s", got)
	}

	keyPath := fmt.Sprintf("/api/me/keys/%d", keyID)
	if resp, env := e.do(t, c, "PUT", keyPath, map[string]any{"status": "enabled"}); resp.StatusCode != 200 {
		t.Fatalf("手工启用应 200，实际 %d %v", resp.StatusCode, env)
	}
	if got := e.keyStatus(t, keyID); got != domain.KeyEnabled {
		t.Fatalf("手工启用后状态应为 enabled，实际 %s", got)
	}

	// 上限未变：再次调用仍命中上限，系统重新写回 depleted
	resp, body := e.relayPost(t, plainKey, relayBody)
	if resp.StatusCode != 402 || !strings.Contains(string(body), "key_quota_exceeded") {
		t.Fatalf("再次调用应 402 key_quota_exceeded，实际 %d %s", resp.StatusCode, body)
	}
	if got := e.keyStatus(t, keyID); got != domain.KeyDepleted {
		t.Fatalf("再次命中上限后应重新写回 depleted，实际 %s", got)
	}
	e.assertReconcile(t)
}

// expired 惰性写入：过期密钥首次调用认证被拒 403 key_expired，
// 同时状态被惰性写为 expired（与术语表"系统惰性写入"条目一致）。
func TestKeyExpiredLazilyWrittenOnAuth(t *testing.T) {
	e := newTestEnv(t)
	uid, plainKey := e.seedRelayUser(t, "lazy-expired", 0, nil)
	if err := e.db.Model(&store.APIKey{}).Where("user_id = ?", uid).
		Update("expires_at", time.Now().Add(-time.Hour)).Error; err != nil {
		t.Fatalf("预置过期时间失败: %v", err)
	}
	var k store.APIKey
	if err := e.db.Where("user_id = ?", uid).First(&k).Error; err != nil {
		t.Fatalf("查密钥失败: %v", err)
	}

	resp, body := e.relayPost(t, plainKey, map[string]any{
		"model": "glm-5", "messages": []map[string]any{{"role": "user", "content": "hi"}},
	})
	if resp.StatusCode != 403 || !strings.Contains(string(body), "key_expired") {
		t.Fatalf("过期密钥应 403 key_expired，实际 %d %s", resp.StatusCode, body)
	}
	if got := e.keyStatus(t, k.ID); got != domain.KeyExpired {
		t.Fatalf("过期密钥认证后状态应被惰性写为 expired，实际 %s", got)
	}
}

// 手工禁用的密钥：预扣命中上限不改写状态，额度调整也不将其恢复为 enabled。
func TestKeyDepletedDoesNotOverrideDisabled(t *testing.T) {
	e := newTestEnv(t)
	c, keyID, _ := e.seedLimitedKeyUser(t, "depleted-disabled", 0, 100)
	keyPath := fmt.Sprintf("/api/me/keys/%d", keyID)

	if resp, env := e.do(t, c, "PUT", keyPath, map[string]any{"status": "disabled"}); resp.StatusCode != 200 {
		t.Fatalf("禁用密钥应 200，实际 %d %v", resp.StatusCode, env)
	}
	if err := e.db.Exec(`UPDATE api_keys SET credit_used = 100 WHERE id = ?`, keyID).Error; err != nil {
		t.Fatalf("预置已用额度失败: %v", err)
	}

	// 直接走计费会话：预扣命中上限返回额度不足，但不得改写 disabled 状态
	var k store.APIKey
	if err := e.db.First(&k, keyID).Error; err != nil {
		t.Fatalf("查密钥失败: %v", err)
	}
	session := relay.NewBillingSession(e.db, e.deps.Billing, "req-disabled-guard", &k, 0)
	if err := session.Precharge(context.Background(), 50); !errors.Is(err, relay.ErrKeyDepleted) {
		t.Fatalf("预扣应返回 ErrKeyDepleted，实际 %v", err)
	}
	if got := e.keyStatus(t, keyID); got != domain.KeyDisabled {
		t.Fatalf("disabled 密钥不得被改写为 depleted，实际 %s", got)
	}

	// 额度调整的恢复路径仅作用于 depleted，不得启用手工禁用的密钥
	if resp, env := e.do(t, c, "PUT", keyPath, map[string]any{"credit_limit": 500}); resp.StatusCode != 200 {
		t.Fatalf("上调额度应 200，实际 %d %v", resp.StatusCode, env)
	}
	if got := e.keyStatus(t, keyID); got != domain.KeyDisabled {
		t.Fatalf("额度调整不得改变 disabled 状态，实际 %s", got)
	}
}
