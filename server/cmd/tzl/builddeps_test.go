package main

// buildDeps 的纯逻辑单测（不连数据库）：
//   - 全部装配字段非 nil（防止抽取时漏字段导致 NewRouter 空指针）
//   - 引用一致性：Deps 与 Relay.Engine 共用同一份 store/billing 实例
//     （防止抽取时误用两份实例导致状态分叉）
//
// buildDeps 仅做结构装配，仓库构造器接受 nil db 不 panic（db 字段只是被持有，
// 不在装配期调用）。因此无需 TZL_TEST_DATABASE_URL 即可验证装配正确性。

import (
	"testing"

	"github.com/liguopeng80/tokenzen-lite/server/internal/billing"
	"github.com/liguopeng80/tokenzen-lite/server/internal/config"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// TestBuildDeps_AllFieldsNonNil 断言 buildDeps 装配后全部字段非 nil。
// 覆盖「某组件空指针」故障形态：漏装配任一字段都会在此暴露。
func TestBuildDeps_AllFieldsNonNil(t *testing.T) {
	cfg := &config.Config{
		Env:                 config.EnvDev,
		EncryptKey:          "tzl-builddeps-test-key",
		UpstreamTimeoutSec:  30,
		SessionCookieSecure: false,
	}
	users := store.NewUserRepo(nil)
	settings := store.NewSettingsRepo(nil)
	billingSvc := billing.NewService(nil)

	deps := buildDeps(cfg, nil, nil, users, settings, billingSvc)

	// 透传字段
	if deps.Cfg == nil {
		t.Fatal("Cfg 为 nil")
	}
	if deps.Users == nil {
		t.Fatal("Users 为 nil")
	}
	if deps.Settings == nil {
		t.Fatal("Settings 为 nil")
	}
	if deps.Billing == nil {
		t.Fatal("Billing 为 nil")
	}
	// buildDeps 构造的字段
	if deps.Keys == nil {
		t.Fatal("Keys 为 nil")
	}
	if deps.Models == nil {
		t.Fatal("Models 为 nil")
	}
	if deps.Ledger == nil {
		t.Fatal("Ledger 为 nil")
	}
	if deps.Redemptions == nil {
		t.Fatal("Redemptions 为 nil")
	}
	if deps.Channels == nil {
		t.Fatal("Channels 为 nil")
	}
	if deps.Costs == nil {
		t.Fatal("Costs 为 nil")
	}
	if deps.UsageLogs == nil {
		t.Fatal("UsageLogs 为 nil")
	}
	if deps.Secrets == nil {
		t.Fatal("Secrets 为 nil")
	}
	if deps.Stats == nil {
		t.Fatal("Stats 为 nil")
	}
	if deps.Limiter == nil {
		t.Fatal("Limiter 为 nil")
	}
	if deps.Gate == nil {
		t.Fatal("Gate 为 nil")
	}
	if deps.LoginLock == nil {
		t.Fatal("LoginLock 为 nil")
	}
	if deps.Departments == nil {
		t.Fatal("Departments 为 nil")
	}
	if deps.Projects == nil {
		t.Fatal("Projects 为 nil")
	}
	if deps.AuditLogs == nil {
		t.Fatal("AuditLogs 为 nil")
	}
	if deps.Audit == nil {
		t.Fatal("Audit 为 nil")
	}
	if deps.AlertEvents == nil {
		t.Fatal("AlertEvents 为 nil")
	}
	if deps.Alerts == nil {
		t.Fatal("Alerts 为 nil")
	}
	if deps.Spend == nil {
		t.Fatal("Spend 为 nil")
	}
	if deps.Rollup == nil {
		t.Fatal("Rollup 为 nil")
	}
	if deps.Integrations == nil {
		t.Fatal("Integrations 为 nil")
	}
	if deps.ServiceTokens == nil {
		t.Fatal("ServiceTokens 为 nil")
	}
	if deps.Idempotency == nil {
		t.Fatal("Idempotency 为 nil")
	}
	if deps.Relay == nil {
		t.Fatal("Relay 为 nil")
	}
	if deps.Relay.Usage == nil {
		t.Fatal("Relay.Usage 为 nil")
	}
	if deps.Relay.Selector == nil {
		t.Fatal("Relay.Selector 为 nil")
	}
	if deps.Relay.Health == nil {
		t.Fatal("Relay.Health 为 nil")
	}
	// Sessions 由 auth.NewSessionManager 构造（postgresstore.New(nil) 只持有 nil 不 panic）
	if deps.Sessions == nil {
		t.Fatal("Sessions 为 nil")
	}
}

// TestBuildDeps_ReferenceConsistency 断言 Deps 与 Relay.Engine 共用同一份实例。
// 漏共用会导致状态分叉：例如 Relay 用一份 Billing、Deps 用另一份，扣费与查询口径就会不一致。
// 这是抽取 buildDeps 时最易犯的错（复制粘贴时漏改字段来源）。
func TestBuildDeps_ReferenceConsistency(t *testing.T) {
	cfg := &config.Config{
		Env:                config.EnvDev,
		EncryptKey:         "tzl-builddeps-test-key",
		UpstreamTimeoutSec: 30,
	}
	users := store.NewUserRepo(nil)
	settings := store.NewSettingsRepo(nil)
	billingSvc := billing.NewService(nil)

	deps := buildDeps(cfg, nil, nil, users, settings, billingSvc)

	// Deps 与 Relay.Engine 之间必须共用的实例
	cases := []struct {
		name string
		want any // deps 侧的期望值
		got  any // relay 侧的实际值
	}{
		{"Billing", deps.Billing, deps.Relay.Billing},
		{"Channels", deps.Channels, deps.Relay.Channels},
		{"Costs", deps.Costs, deps.Relay.Costs},
		{"Models", deps.Models, deps.Relay.Models},
		{"UsageLogs", deps.UsageLogs, deps.Relay.UsageLogs},
		{"Settings", deps.Settings, deps.Relay.Settings},
		{"Secrets", deps.Secrets, deps.Relay.Secrets},
		{"Spend", deps.Spend, deps.Relay.Spend},
		{"Alerts", deps.Alerts, deps.Relay.Alerts},
	}
	for _, c := range cases {
		if c.want != c.got {
			t.Errorf("%s 引用不一致：Deps 与 Relay.Engine 应共用同一实例，实际为不同指针", c.name)
		}
	}
	// 透传的 cfg/users 也应一致
	if deps.Cfg != cfg {
		t.Error("Cfg 应透传输入的 cfg")
	}
	if deps.Users != users {
		t.Error("Users 应透传输入的 users")
	}
	if deps.Billing != billingSvc {
		t.Error("Billing 应透传输入的 billingSvc")
	}
	if deps.Settings != settings {
		t.Error("Settings 应透传输入的 settings")
	}
}
