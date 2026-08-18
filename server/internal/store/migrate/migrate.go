// Package migrate 内嵌 SQL 迁移文件并提供 up/down 执行入口。
// 服务启动时自动执行 Up；down 仅供测试与手工回滚使用。
package migrate

import (
	"embed"
	"errors"
	"fmt"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

func newMigrator(databaseURL string) (*migrate.Migrate, error) {
	src, err := iofs.New(migrationFS, "migrations")
	if err != nil {
		return nil, fmt.Errorf("加载内嵌迁移文件失败: %w", err)
	}
	m, err := migrate.NewWithSourceInstance("iofs", src, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("初始化迁移器失败: %w", err)
	}
	return m, nil
}

// Up 应用全部未执行的迁移；无新迁移时静默成功。
func Up(databaseURL string) error {
	m, err := newMigrator(databaseURL)
	if err != nil {
		return err
	}
	defer m.Close()
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("执行迁移失败: %w", err)
	}
	return nil
}

// Down 回滚全部迁移，仅用于测试与手工操作。
func Down(databaseURL string) error {
	m, err := newMigrator(databaseURL)
	if err != nil {
		return err
	}
	defer m.Close()
	if err := m.Down(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("回滚迁移失败: %w", err)
	}
	return nil
}
