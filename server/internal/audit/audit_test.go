package audit

import (
	"encoding/json"
	"testing"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
)

// 敏感字段在审计快照中只表达「被修改过」，连旧值也不记录。
func TestRedactHidesSensitiveValues(t *testing.T) {
	state := map[string]any{
		"username":            "alice",
		"password":            "hunter2",
		"api_key_encrypted":   "AAAA-ciphertext",
		"alert_smtp_password": "smtp-secret",
		"nested": map[string]any{
			"password_hash": "$2a$10$abcdefg",
			"role":          "admin",
		},
	}
	got := Redact(domain.AuditTargetUser, state)

	if got["username"] != "alice" {
		t.Errorf("非敏感字段应原样保留，实际 %v", got["username"])
	}
	for _, key := range []string{"password", "api_key_encrypted", "alert_smtp_password"} {
		if got[key] != domain.AuditRedacted {
			t.Errorf("敏感字段 %s 应脱敏，实际 %v", key, got[key])
		}
	}
	nested, ok := got["nested"].(map[string]any)
	if !ok {
		t.Fatalf("嵌套对象应保持为对象，实际 %T", got["nested"])
	}
	if nested["password_hash"] != domain.AuditRedacted {
		t.Errorf("嵌套敏感字段应脱敏，实际 %v", nested["password_hash"])
	}
	if nested["role"] != "admin" {
		t.Errorf("嵌套非敏感字段应保留，实际 %v", nested["role"])
	}

	// 脱敏返回副本，不得改动调用方持有的原始快照。
	if state["password"] != "hunter2" {
		t.Errorf("原始快照被修改，实际 %v", state["password"])
	}
	if inner := state["nested"].(map[string]any); inner["password_hash"] != "$2a$10$abcdefg" {
		t.Errorf("原始嵌套快照被修改，实际 %v", inner["password_hash"])
	}
}

// 序列化后的快照里不得出现任何敏感值的原文。
func TestEncodeStateNeverLeaksSecrets(t *testing.T) {
	raw := encodeState(domain.AuditTargetChannel, map[string]any{
		"api_key": "sk-upstream-plaintext", "name": "渠道甲",
	})
	if raw == nil {
		t.Fatal("快照不应为空")
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("快照解析失败: %v", err)
	}
	if decoded["api_key"] != domain.AuditRedacted {
		t.Errorf("上游密钥应脱敏，实际 %v", decoded["api_key"])
	}
	if decoded["name"] != "渠道甲" {
		t.Errorf("普通字段应保留，实际 %v", decoded["name"])
	}
}

// 空快照序列化为 nil，避免在审计表里塞满 "{}"。
func TestEncodeStateEmpty(t *testing.T) {
	if raw := encodeState(domain.AuditTargetUser, nil); raw != nil {
		t.Errorf("空快照应为 nil，实际 %s", raw)
	}
	if raw := encodeState(domain.AuditTargetUser, map[string]any{}); raw != nil {
		t.Errorf("零字段快照应为 nil，实际 %s", raw)
	}
}

// Diff 只保留取值实际发生变化的字段。
func TestDiffKeepsOnlyChangedFields(t *testing.T) {
	before := map[string]any{"name": "旧名", "status": "enabled", "models": []string{"a"}}
	after := map[string]any{"name": "新名", "status": "enabled", "models": []string{"a", "b"}}

	changedBefore, changedAfter := Diff(before, after)

	if _, ok := changedAfter["status"]; ok {
		t.Errorf("未变化的字段不应出现在变更后快照: %v", changedAfter)
	}
	if changedAfter["name"] != "新名" || changedBefore["name"] != "旧名" {
		t.Errorf("变化字段应同时出现在前后快照: before=%v after=%v", changedBefore, changedAfter)
	}
	if _, ok := changedAfter["models"]; !ok {
		t.Errorf("切片取值变化应被识别: %v", changedAfter)
	}
}

// 变更前不存在的键视为新增：只出现在变更后快照。
func TestDiffTreatsMissingKeyAsAdded(t *testing.T) {
	changedBefore, changedAfter := Diff(
		map[string]any{}, map[string]any{"daily_spend_limit": int64(1000)})
	if _, ok := changedBefore["daily_spend_limit"]; ok {
		t.Errorf("新增字段不应出现在变更前快照: %v", changedBefore)
	}
	if changedAfter["daily_spend_limit"] != int64(1000) {
		t.Errorf("新增字段应出现在变更后快照: %v", changedAfter)
	}
}

// 字段名在不同对象上重名：兑换码的 code 是凭据，部门的 code 是成本中心编码。
// 前者必须脱敏，后者必须留痕，否则财务对账出现分歧时查不出谁改成了什么。
func TestRedactScopesCodeByTargetType(t *testing.T) {
	state := map[string]any{"code": "CC-RD-001", "name": "研发部"}

	dept := Redact(domain.AuditTargetDepartment, state)
	if dept["code"] != "CC-RD-001" {
		t.Errorf("部门的成本中心编码应原样记录，实际 %v", dept["code"])
	}

	redemption := Redact(domain.AuditTargetRedemption, map[string]any{"code": "TZL-ABCD-1234"})
	if redemption["code"] != domain.AuditRedacted {
		t.Errorf("兑换码应脱敏，实际 %v", redemption["code"])
	}
	batch := Redact(domain.AuditTargetRedemption, map[string]any{"codes": []string{"A", "B"}})
	if batch["codes"] != domain.AuditRedacted {
		t.Errorf("兑换码批次应脱敏，实际 %v", batch["codes"])
	}
}

// 跨对象通用的敏感字段不受对象类型影响。
func TestRedactAlwaysHidesGlobalSecrets(t *testing.T) {
	for _, target := range []domain.AuditTargetType{
		domain.AuditTargetUser, domain.AuditTargetDepartment,
		domain.AuditTargetChannel, domain.AuditTargetAPIKey,
	} {
		got := Redact(target, map[string]any{"password": "hunter2", "key_hash": "abc"})
		if got["password"] != domain.AuditRedacted || got["key_hash"] != domain.AuditRedacted {
			t.Errorf("对象类型 %s 上的通用敏感字段未脱敏：%v", target, got)
		}
	}
}
