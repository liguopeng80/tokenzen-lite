package store

import (
	"context"
	"fmt"
	"os"
	"testing"

	"gorm.io/gorm"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store/migrate"
)

// 覆盖 P2-14：直接调用仓储列表方法传入非法分页参数时，
// 仓储层自身兜底必须生效——不产生负偏移量的数据库错误、
// 不发生不限条数的全表拉取。集成测试：需要 TZL_TEST_DATABASE_URL，未设置时跳过。

// seedRows 是各表种入的行数：超过上限 100，用于验证超限截断。
const seedRows = 105

func newStoreTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	url := os.Getenv("TZL_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("未设置 TZL_TEST_DATABASE_URL，跳过 store 集成测试")
	}
	if err := migrate.Up(url); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}
	db, err := Open(url)
	if err != nil {
		t.Fatalf("连接测试库失败: %v", err)
	}
	if sqlDB, dbErr := db.DB(); dbErr == nil {
		// 测试结束关闭连接池，防止累计空闲连接耗尽 PostgreSQL 连接上限。
		t.Cleanup(func() { _ = sqlDB.Close() })
	}
	if err := db.Exec("TRUNCATE users, api_keys, sessions, credit_ledger, redemptions, usage_logs RESTART IDENTITY CASCADE").Error; err != nil {
		t.Fatalf("清空测试表失败: %v", err)
	}
	return db
}

// seedPaginationData 向 users、api_keys、credit_ledger、usage_logs 各种入 seedRows 行。
func seedPaginationData(t *testing.T, db *gorm.DB) {
	t.Helper()
	users := make([]User, 0, seedRows)
	for i := 0; i < seedRows; i++ {
		users = append(users, User{
			Username: fmt.Sprintf("pguser-%03d", i), PasswordHash: "x",
			Role: domain.RoleUser, Status: domain.UserEnabled,
		})
	}
	if err := db.Create(&users).Error; err != nil {
		t.Fatalf("种入用户失败: %v", err)
	}

	keys := make([]APIKey, 0, seedRows)
	entries := make([]LedgerEntry, 0, seedRows)
	logs := make([]UsageLog, 0, seedRows)
	for i := 0; i < seedRows; i++ {
		keys = append(keys, APIKey{
			UserID: users[0].ID, Name: fmt.Sprintf("pgkey-%03d", i),
			KeyHash:   fmt.Sprintf("pghash-%03d", i),
			KeyPrefix: fmt.Sprintf("tzl-pg%03d", i),
			Status:    domain.KeyEnabled,
		})
		entries = append(entries, LedgerEntry{
			UserID: users[0].ID, EntryType: domain.LedgerGrant,
			Amount: 100, BalanceAfter: domain.Credits(100 * (i + 1)),
			RequestID: fmt.Sprintf("pgledger-%03d", i),
		})
		logs = append(logs, UsageLog{
			RequestID: fmt.Sprintf("pglog-%03d", i),
			UserID:    users[0].ID, APIKeyID: 1,
			ModelName: "pg-model", Status: domain.UsageSettled,
		})
	}
	if err := db.Create(&keys).Error; err != nil {
		t.Fatalf("种入密钥失败: %v", err)
	}
	if err := db.Create(&entries).Error; err != nil {
		t.Fatalf("种入流水失败: %v", err)
	}
	if err := db.Create(&logs).Error; err != nil {
		t.Fatalf("种入用量日志失败: %v", err)
	}
}

// listResult 是一次仓储列表调用的归一化结果，供四个仓储共用断言。
type listResult struct {
	count int
	total int64
	err   error
}

func TestRepoListPaginationFallback(t *testing.T) {
	db := newStoreTestDB(t)
	seedPaginationData(t, db)
	ctx := context.Background()

	userRepo := NewUserRepo(db)
	keyRepo := NewAPIKeyRepo(db)
	ledgerRepo := NewLedgerRepo(db)
	usageRepo := NewUsageLogRepo(db)

	repos := []struct {
		name string
		list func(page, pageSize int) listResult
	}{
		{"用户列表", func(page, pageSize int) listResult {
			items, total, err := userRepo.List(ctx, UserListFilter{Page: page, PageSize: pageSize})
			return listResult{len(items), total, err}
		}},
		{"密钥列表", func(page, pageSize int) listResult {
			items, total, err := keyRepo.List(ctx, APIKeyListFilter{Page: page, PageSize: pageSize})
			return listResult{len(items), total, err}
		}},
		{"积分流水", func(page, pageSize int) listResult {
			items, total, err := ledgerRepo.List(ctx, LedgerListFilter{Page: page, PageSize: pageSize})
			return listResult{len(items), total, err}
		}},
		{"用量日志", func(page, pageSize int) listResult {
			items, total, err := usageRepo.List(ctx, UsageLogFilter{Page: page, PageSize: pageSize})
			return listResult{len(items), total, err}
		}},
	}

	cases := []struct {
		name      string
		page      int
		pageSize  int
		wantCount int
	}{
		{"每页条数超限截断为 100", 1, 999, MaxPageSize},
		{"页码 0 每页条数 0 回落默认值", 0, 0, DefaultPageSize},
		{"页码负数归一为第一页", -3, 10, 10},
		{"每页条数负数回落默认值", 1, -5, DefaultPageSize},
		{"第二页取到剩余 5 条", 2, 100, seedRows - MaxPageSize},
	}
	for _, repo := range repos {
		for _, tc := range cases {
			t.Run(repo.name+"/"+tc.name, func(t *testing.T) {
				got := repo.list(tc.page, tc.pageSize)
				if got.err != nil {
					t.Fatalf("非法分页参数不应导致查询错误: %v", got.err)
				}
				if got.total != seedRows {
					t.Errorf("total 应为 %d，实际 %d", seedRows, got.total)
				}
				if got.count != tc.wantCount {
					t.Errorf("返回条数应为 %d，实际 %d", tc.wantCount, got.count)
				}
			})
		}
	}
}
