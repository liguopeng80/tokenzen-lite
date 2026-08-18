package main

// startBackground 的集成测试（需 TZL_TEST_DATABASE_URL）。
//
// 后台任务的 goroutine 启动后立即读 settings（SettingsRepo.GetInt64），
// nil db 会触发空指针 panic——因此无法用 nil db 做纯逻辑测试。
// 此处用真实测试库构造完整 Deps，验证：
//   - startBackground 返回的 cancel 立即返回（不阻塞调用方）
//   - cancel 后进程不崩溃（各 goroutine 收到 ctx 取消后退出）

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/liguopeng80/tokenzen-lite/server/internal/billing"
	"github.com/liguopeng80/tokenzen-lite/server/internal/config"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store/migrate"
)

// TestStartBackground_CancelDoesNotBlock 验证 startBackground 立即返回 cancel，
// 且调用 cancel 不阻塞。后台任务的实际退出语义由各调度器自身的测试覆盖。
func TestStartBackground_CancelDoesNotBlock(t *testing.T) {
	url := os.Getenv("TZL_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("未设置 TZL_TEST_DATABASE_URL，跳过 startBackground 集成测试")
	}
	if err := migrate.Up(url); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}
	db, err := store.Open(url)
	if err != nil {
		t.Fatalf("连接测试库失败: %v", err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })

	cfg := &config.Config{
		Env: config.EnvDev, EncryptKey: "tzl-bg-test-key",
		UpstreamTimeoutSec: 30,
	}
	users := store.NewUserRepo(db)
	settings := store.NewSettingsRepo(db)
	billingSvc := billing.NewService(db)
	deps := buildDeps(cfg, db, sqlDB, users, settings, billingSvc)

	start := time.Now()
	cancel := startBackground(context.Background(), deps)
	if cancel == nil {
		t.Fatal("startBackground 应返回非 nil 的 cancel 函数")
	}
	cancel()
	// startBackground + cancel 不应阻塞调用方（后台任务在独立 goroutine）
	if elapsed := time.Since(start); elapsed > 1*time.Second {
		t.Errorf("startBackground + cancel 应立即返回，实际耗时 %v", elapsed)
	}
	// 给后台 goroutine 一点时间观察 ctx 取消；进程不应崩溃
	time.Sleep(200 * time.Millisecond)
}
