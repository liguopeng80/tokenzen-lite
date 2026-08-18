package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
)

// 设置项被误改后要能从审计里查回原值：只记新值等于查不回去。
func TestSettingUpdateRecordsPreviousValue(t *testing.T) {
	e := newTestEnv(t)
	rootC := e.seedAndLogin(t, "settingauditroot", domain.RoleRoot)

	// 首次修改：改动前是注册的默认值 1000000。
	if resp, env := e.do(t, rootC, "PUT", "/api/admin/settings",
		map[string]any{"key": "exchange_rate_credits_per_cny", "value": 2_000_000}); resp.StatusCode != http.StatusOK {
		t.Fatalf("更新设置失败：%d %v", resp.StatusCode, env)
	}
	log := latestAudit(t, e, domain.AuditSettingUpdate)
	if !strings.Contains(string(log.BeforeState), "1000000") {
		t.Errorf("应记录改动前的默认值：before=%s", log.BeforeState)
	}
	if !strings.Contains(string(log.AfterState), "2000000") {
		t.Errorf("应记录改动后的新值：after=%s", log.AfterState)
	}

	// 二次修改：改动前是上一次写入的值。
	if resp, env := e.do(t, rootC, "PUT", "/api/admin/settings",
		map[string]any{"key": "exchange_rate_credits_per_cny", "value": 3_000_000}); resp.StatusCode != http.StatusOK {
		t.Fatalf("更新设置失败：%d %v", resp.StatusCode, env)
	}
	log = latestAudit(t, e, domain.AuditSettingUpdate)
	if !strings.Contains(string(log.BeforeState), "2000000") {
		t.Errorf("应记录上一次写入的值：before=%s", log.BeforeState)
	}
}

// 密文设置项两侧都只表达「被改过」，不记取值——审计表的可读范围比密钥本身更宽。
func TestSecretSettingUpdateRedactsBothSides(t *testing.T) {
	e := newTestEnv(t)
	rootC := e.seedAndLogin(t, "secretauditroot", domain.RoleRoot)

	const secret = "smtp-plaintext-should-never-appear"
	if resp, env := e.do(t, rootC, "PUT", "/api/admin/settings",
		map[string]any{"key": "alert_smtp_password", "value": secret}); resp.StatusCode != http.StatusOK {
		t.Fatalf("更新密文设置失败：%d %v", resp.StatusCode, env)
	}
	log := latestAudit(t, e, domain.AuditSettingUpdate)
	snapshot := string(log.BeforeState) + string(log.AfterState)
	if strings.Contains(snapshot, secret) {
		t.Fatalf("审计快照泄漏 SMTP 密码：%s", snapshot)
	}
	if !strings.Contains(string(log.AfterState), domain.AuditRedacted) {
		t.Errorf("应以占位符表达密码已修改：%s", log.AfterState)
	}
}

// 部门的成本中心编码在审计里必须留痕：它是对接财务系统的关键标识，
// 与兑换码同名但性质完全不同。
func TestDepartmentCodeNotRedactedInAudit(t *testing.T) {
	e := newTestEnv(t)
	adminC := e.seedAndLogin(t, "codeauditadmin", domain.RoleAdmin)
	createDepartment(t, e, adminC, map[string]any{"name": "财务对账部", "code": "CC-FIN-001"})

	log := latestAudit(t, e, domain.AuditDepartmentCreate)
	if !strings.Contains(string(log.AfterState), "CC-FIN-001") {
		t.Fatalf("成本中心编码应如实记录，实际 %s", log.AfterState)
	}
}

// 兑换码批次的审计不含任何兑换码明文。
func TestRedemptionAuditNeverLeaksCodes(t *testing.T) {
	e := newTestEnv(t)
	adminC := e.seedAndLogin(t, "redeemauditadmin", domain.RoleAdmin)

	resp, env := e.do(t, adminC, "POST", "/api/admin/redemptions/batch",
		map[string]any{"count": 2, "credits": 1000, "name": "测试批次"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("生成兑换码失败：%d %v", resp.StatusCode, env)
	}
	codes := env["data"].(map[string]any)["codes"].([]any)
	log := latestAudit(t, e, domain.AuditRedemptionBatch)
	snapshot := string(log.AfterState)
	for _, code := range codes {
		if strings.Contains(snapshot, code.(string)) {
			t.Fatalf("审计快照泄漏兑换码明文：%s", snapshot)
		}
	}
}
