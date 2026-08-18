package migrate

import (
	"database/sql"
	"os"
	"testing"

	_ "github.com/lib/pq"
)

// 集成测试：需要 TZL_TEST_DATABASE_URL 指向可清空的测试库，未设置时跳过。
func testDatabaseURL(t *testing.T) string {
	t.Helper()
	url := os.Getenv("TZL_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("未设置 TZL_TEST_DATABASE_URL，跳过迁移集成测试")
	}
	return url
}

func tableExists(t *testing.T, db *sql.DB, name string) bool {
	t.Helper()
	var exists bool
	err := db.QueryRow(
		`SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema='public' AND table_name=$1)`,
		name,
	).Scan(&exists)
	if err != nil {
		t.Fatalf("查询表存在性失败: %v", err)
	}
	return exists
}

func TestUpDownIdempotent(t *testing.T) {
	url := testDatabaseURL(t)
	db, err := sql.Open("postgres", url)
	if err != nil {
		t.Fatalf("连接测试库失败: %v", err)
	}
	defer db.Close()

	// 起点归零：重置 public schema 后再 Down。共享测试库可能残留上一轮 api 测试种入的
	// 数据（如托管服务账号 role=managed、无口令用户），这些数据会让回滚 0010/0013 时
	// 恢复的更严格约束（三值 role CHECK、password_hash NOT NULL）无法重建。重置 schema
	// 清掉残留数据与 schema_migrations，使 Down/Up 从干净起点开始。
	if _, err := db.Exec(`DROP SCHEMA public CASCADE; CREATE SCHEMA public;`); err != nil {
		t.Fatalf("重置 schema 失败: %v", err)
	}
	if err := Down(url); err != nil {
		t.Fatalf("初始 Down 失败: %v", err)
	}

	if err := Up(url); err != nil {
		t.Fatalf("Up 失败: %v", err)
	}
	for _, tbl := range []string{"users", "sessions", "api_keys"} {
		if !tableExists(t, db, tbl) {
			t.Errorf("Up 后表 %s 应存在", tbl)
		}
	}

	// 重复 Up 应为无变更且不报错
	if err := Up(url); err != nil {
		t.Fatalf("重复 Up 应幂等，实际报错: %v", err)
	}

	if err := Down(url); err != nil {
		t.Fatalf("Down 失败: %v", err)
	}
	for _, tbl := range []string{"users", "sessions", "api_keys"} {
		if tableExists(t, db, tbl) {
			t.Errorf("Down 后表 %s 应不存在", tbl)
		}
	}

	// 回滚后再次 Up，保证 down 脚本完整可逆
	if err := Up(url); err != nil {
		t.Fatalf("Down 后再次 Up 失败: %v", err)
	}
}
