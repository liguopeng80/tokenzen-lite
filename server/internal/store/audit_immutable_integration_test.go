package store

import (
	"context"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
)

// newAuditEnv 提供清空后的审计表。
func newAuditEnv(t *testing.T) (*gorm.DB, *AuditLogRepo) {
	t.Helper()
	db := newStoreTestDB(t)
	if err := db.Exec("TRUNCATE audit_logs RESTART IDENTITY").Error; err != nil {
		t.Fatalf("清空审计表失败: %v", err)
	}
	return db, NewAuditLogRepo(db)
}

// seedAuditLog 写一条审计记录，createdAt 指定写入时点。
// 用原生 INSERT 而非 GORM：审计表不可更新，无法先建后改时间。
func seedAuditLog(t *testing.T, db *gorm.DB, action string, createdAt time.Time) int64 {
	t.Helper()
	var id int64
	err := db.Raw(`INSERT INTO audit_logs
		(operator_id, operator_name, operator_role, action, result, created_at)
		VALUES (?, ?, ?, ?, ?, ?) RETURNING id`,
		1, "root", domain.RoleRoot, action, domain.AuditSuccess, createdAt).
		Scan(&id).Error
	if err != nil {
		t.Fatalf("写入审计记录失败: %v", err)
	}
	return id
}

// 审计记录一经写入即为事实，数据库层拒绝任何修改——
// 仅靠应用层「没有更新代码路径」无法约束具备库权限的内部人员。
func TestAuditLogUpdateRejectedByDatabase(t *testing.T) {
	db, _ := newAuditEnv(t)
	id := seedAuditLog(t, db, "user.delete", time.Now().Add(-365*24*time.Hour))

	err := db.Exec("UPDATE audit_logs SET action = ? WHERE id = ?", "user.create", id).Error
	if err == nil {
		t.Fatal("修改审计记录应被数据库拒绝，实际成功")
	}
	if !strings.Contains(err.Error(), "审计记录不可修改") {
		t.Errorf("拒绝原因应为审计记录不可修改，实际 %v", err)
	}

	var got AuditLog
	if err := db.First(&got, id).Error; err != nil {
		t.Fatalf("查询审计记录失败: %v", err)
	}
	if got.Action != domain.AuditAction("user.delete") {
		t.Errorf("记录内容不应被改动，实际 action=%s", got.Action)
	}
}

// 近期审计记录不可删除：删除刚写入的记录等同于抹除操作痕迹。
func TestAuditLogRecentDeleteRejectedByDatabase(t *testing.T) {
	db, _ := newAuditEnv(t)
	id := seedAuditLog(t, db, "user.status_change", time.Now())

	err := db.Exec("DELETE FROM audit_logs WHERE id = ?", id).Error
	if err == nil {
		t.Fatal("删除近期审计记录应被数据库拒绝，实际成功")
	}
	if !strings.Contains(err.Error(), "30 天内不可删除") {
		t.Errorf("拒绝原因应为保护期内不可删除，实际 %v", err)
	}

	var count int64
	if err := db.Model(&AuditLog{}).Where("id = ?", id).Count(&count).Error; err != nil {
		t.Fatalf("查询审计记录失败: %v", err)
	}
	if count != 1 {
		t.Error("记录不应被删除")
	}
}

// 保留期清理是正常运维动作：超出保护期的记录仍可删除，且不波及保护期内的记录。
func TestAuditLogRetentionPurgeStillWorks(t *testing.T) {
	db, repo := newAuditEnv(t)
	oldID := seedAuditLog(t, db, "user.create", time.Now().Add(-200*24*time.Hour))
	recentID := seedAuditLog(t, db, "user.update", time.Now().Add(-1*time.Hour))

	deleted, err := repo.PurgeOlderThan(context.Background(),
		time.Now().Add(-180*24*time.Hour))
	if err != nil {
		t.Fatalf("按保留期清理失败: %v", err)
	}
	if deleted != 1 {
		t.Errorf("应清理 1 条过期记录，实际 %d 条", deleted)
	}

	var oldCount, recentCount int64
	db.Model(&AuditLog{}).Where("id = ?", oldID).Count(&oldCount)
	db.Model(&AuditLog{}).Where("id = ?", recentID).Count(&recentCount)
	if oldCount != 0 {
		t.Error("过期记录应被清理")
	}
	if recentCount != 1 {
		t.Error("保护期内的记录不应被清理")
	}
}

// 保留期设置的下限与数据库保护期一致：设得更短会让清理被数据库拒绝，
// 因此在写入设置时就挡掉，而不是等到清理任务失败才暴露。
func TestAuditRetentionSettingRejectsBelowProtectedWindow(t *testing.T) {
	def := settingDef("audit_log_retention_days")
	if def == nil || def.Validate == nil {
		t.Fatal("审计保留期设置项缺少校验")
	}
	cases := []struct {
		days    int64
		wantErr bool
	}{
		{0, false},   // 不清理
		{29, true},   // 短于数据库保护期
		{30, false},  // 恰好等于保护期
		{180, false}, // 默认值
		{3651, true}, // 超出上限
	}
	for _, tc := range cases {
		err := def.Validate(tc.days)
		if tc.wantErr && err == nil {
			t.Errorf("保留期 %d 天应被拒绝", tc.days)
		}
		if !tc.wantErr && err != nil {
			t.Errorf("保留期 %d 天应被接受，实际 %v", tc.days, err)
		}
	}
}
