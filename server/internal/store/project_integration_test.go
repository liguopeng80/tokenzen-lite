package store

import (
	"context"
	"fmt"
	"testing"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
)

// 本文件是 ProjectRepo（0019 项目维度）的 DB 集成测试，由主会话在
// TZL_TEST_DATABASE_URL 上串行执行（go test -p 1 ./...）。
// 覆盖项目 CRUD、密钥归属、删除规则（ON DELETE SET NULL）。

func newProjectTestEnv(t *testing.T) *ProjectRepo {
	t.Helper()
	db := newStoreTestDB(t)
	// newStoreTestDB 已 TRUNCATE users/api_keys/...；额外清 projects（被外键引用，CASCADE 安全）。
	if err := db.Exec(`TRUNCATE projects RESTART IDENTITY CASCADE`).Error; err != nil {
		t.Fatalf("清空 projects 失败: %v", err)
	}
	return NewProjectRepo(db)
}

func TestProjectCRUD(t *testing.T) {
	repo := newProjectTestEnv(t)
	ctx := context.Background()

	// Create。
	p := &Project{Name: "项目甲", Code: "P-A", MonthlyBudgetCredits: 500000, Status: domain.ProjectEnabled}
	if err := repo.Create(ctx, p); err != nil {
		t.Fatalf("创建项目失败: %v", err)
	}
	if p.ID == 0 {
		t.Fatal("创建后应取得 ID")
	}
	// 名称唯一约束。
	if err := repo.Create(ctx, &Project{Name: "项目甲"}); err == nil {
		t.Fatal("重名项目应创建失败")
	}

	// GetByID。
	got, err := repo.GetByID(ctx, p.ID)
	if err != nil {
		t.Fatalf("查询项目失败: %v", err)
	}
	if got.Name != "项目甲" || got.Code != "P-A" || got.MonthlyBudgetCredits != 500000 {
		t.Errorf("查询结果不符: %+v", got)
	}

	// GetByID 不存在。
	if _, err := repo.GetByID(ctx, 99999); err != ErrNotFound {
		t.Errorf("不存在的项目应返回 ErrNotFound，实际 %v", err)
	}

	// UpdateFields（白名单，external_ref 不可改）。
	if err := repo.UpdateFields(ctx, p.ID, map[string]any{
		"name": "项目甲改", "monthly_budget_credits": domain.Credits(800000),
	}); err != nil {
		t.Fatalf("更新项目失败: %v", err)
	}
	updated, _ := repo.GetByID(ctx, p.ID)
	if updated.Name != "项目甲改" || updated.MonthlyBudgetCredits != 800000 {
		t.Errorf("更新后字段不符: %+v", updated)
	}

	// UpdateFields 不存在。
	if err := repo.UpdateFields(ctx, 99999, map[string]any{"name": "x"}); err != ErrNotFound {
		t.Errorf("更新不存在的项目应返回 ErrNotFound，实际 %v", err)
	}

	// List（带 keyword）。
	list, total, err := repo.List(ctx, ProjectListFilter{Keyword: "甲改"})
	if err != nil {
		t.Fatalf("列表查询失败: %v", err)
	}
	if total != 1 || len(list) != 1 {
		t.Fatalf("按关键字应查出 1 行，实际 total=%d rows=%d", total, len(list))
	}

	// Delete。
	if err := repo.Delete(ctx, p.ID); err != nil {
		t.Fatalf("删除项目失败: %v", err)
	}
	if _, err := repo.GetByID(ctx, p.ID); err != ErrNotFound {
		t.Errorf("删除后查询应返回 ErrNotFound，实际 %v", err)
	}
	// Delete 不存在。
	if err := repo.Delete(ctx, 99999); err != ErrNotFound {
		t.Errorf("删除不存在的项目应返回 ErrNotFound，实际 %v", err)
	}
}

// TestProjectCodeUniqueOnNonEmpty 校验 code 非空时唯一、为空时可重复（部分唯一索引）。
func TestProjectCodeUniqueOnNonEmpty(t *testing.T) {
	repo := newProjectTestEnv(t)
	ctx := context.Background()
	if err := repo.Create(ctx, &Project{Name: "p1", Code: "CODE1"}); err != nil {
		t.Fatalf("创建 p1 失败: %v", err)
	}
	// 同 code 应失败。
	if err := repo.Create(ctx, &Project{Name: "p2", Code: "CODE1"}); err == nil {
		t.Fatal("code 重复应创建失败")
	}
	// 空 code 可重复（多个项目不填 code）。
	if err := repo.Create(ctx, &Project{Name: "p3", Code: ""}); err != nil {
		t.Fatalf("空 code 项目创建失败: %v", err)
	}
	if err := repo.Create(ctx, &Project{Name: "p4", Code: ""}); err != nil {
		t.Fatalf("第二个空 code 项目创建失败: %v", err)
	}
}

// TestProjectDeleteNullifiesKeys 校验删除项目时归属它的密钥 project_id 被置 NULL
// （ON DELETE SET NULL 语义），密钥记录本身不删除。
func TestProjectDeleteNullifiesKeys(t *testing.T) {
	repo := newProjectTestEnv(t)
	ctx := context.Background()
	db := repo.db

	p := &Project{Name: "del-proj", Status: domain.ProjectEnabled}
	if err := repo.Create(ctx, p); err != nil {
		t.Fatalf("建项目失败: %v", err)
	}
	// 建用户 + 密钥，密钥挂该项目。
	user := &User{Username: "key-owner", DisplayName: "owner", Role: domain.RoleUser,
		Status: domain.UserEnabled, CreditBalance: 1000}
	if err := db.Create(user).Error; err != nil {
		t.Fatalf("建用户失败: %v", err)
	}
	key := &APIKey{UserID: user.ID, Name: "k1", KeyHash: "h1", KeyPrefix: "sk",
		Status: domain.KeyEnabled, ProjectID: &p.ID}
	if err := db.Create(key).Error; err != nil {
		t.Fatalf("建密钥失败: %v", err)
	}

	// KeyCount 计数。
	n, err := repo.KeyCount(ctx, p.ID)
	if err != nil {
		t.Fatalf("KeyCount 失败: %v", err)
	}
	if n != 1 {
		t.Errorf("归属该项目的密钥数期望 1，实际 %d", n)
	}

	// 删除项目 → 密钥 project_id 置 NULL。
	if err := repo.Delete(ctx, p.ID); err != nil {
		t.Fatalf("删除项目失败: %v", err)
	}
	var after APIKey
	if err := db.First(&after, key.ID).Error; err != nil {
		t.Fatalf("密钥应仍存在，查询失败: %v", err)
	}
	if after.ProjectID != nil {
		t.Errorf("删除项目后密钥 project_id 应为 NULL，实际 %v", *after.ProjectID)
	}
}

// TestProjectListWithStats 校验列表行附带 key_count 与 owner_username。
func TestProjectListWithStats(t *testing.T) {
	repo := newProjectTestEnv(t)
	ctx := context.Background()
	db := repo.db

	owner := &User{Username: "proj-owner", DisplayName: "翁", Role: domain.RoleUser,
		Status: domain.UserEnabled, CreditBalance: 0}
	if err := db.Create(owner).Error; err != nil {
		t.Fatalf("建负责人失败: %v", err)
	}
	p := &Project{Name: "stat-proj", OwnerUserID: &owner.ID, Status: domain.ProjectEnabled}
	if err := repo.Create(ctx, p); err != nil {
		t.Fatalf("建项目失败: %v", err)
	}
	user := &User{Username: "kuser", DisplayName: "u", Role: domain.RoleUser,
		Status: domain.UserEnabled, CreditBalance: 0}
	if err := db.Create(user).Error; err != nil {
		t.Fatalf("建用户失败: %v", err)
	}
	for i := 0; i < 3; i++ {
		if err := db.Create(&APIKey{UserID: user.ID, Name: "k", KeyHash: fmt.Sprintf("h%d", i), KeyPrefix: "sk",
			Status: domain.KeyEnabled, ProjectID: &p.ID}).Error; err != nil {
			t.Fatalf("建密钥失败: %v", err)
		}
	}

	rows, _, err := repo.List(ctx, ProjectListFilter{})
	if err != nil {
		t.Fatalf("列表查询失败: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("应查出 1 个项目，实际 %d", len(rows))
	}
	if rows[0].KeyCount != 3 {
		t.Errorf("key_count 期望 3，实际 %d", rows[0].KeyCount)
	}
	if rows[0].OwnerUsername != "proj-owner" {
		t.Errorf("owner_username 期望 proj-owner，实际 %q", rows[0].OwnerUsername)
	}
}
