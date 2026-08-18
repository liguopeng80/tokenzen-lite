package api

import (
	"testing"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// TestCanManageMatrix 固化 canManage 的权限矩阵语义（U1，无 DB）：
// root 可管理所有角色；admin 只能管理普通用户；admin 对同级 admin 与 root 均无权。
func TestCanManageMatrix(t *testing.T) {
	cases := []struct {
		name   string
		actor  domain.Role
		target domain.Role
		want   bool
	}{
		{"root 管理 user", domain.RoleRoot, domain.RoleUser, true},
		{"root 管理 admin", domain.RoleRoot, domain.RoleAdmin, true},
		{"root 管理 root", domain.RoleRoot, domain.RoleRoot, true},
		{"admin 管理 user", domain.RoleAdmin, domain.RoleUser, true},
		{"admin 管理同级 admin", domain.RoleAdmin, domain.RoleAdmin, false},
		{"admin 管理 root", domain.RoleAdmin, domain.RoleRoot, false},
		{"user 管理 user", domain.RoleUser, domain.RoleUser, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			actor := &store.User{Role: tc.actor}
			target := &store.User{Role: tc.target}
			if got := canManage(actor, target); got != tc.want {
				t.Errorf("canManage(%s, %s) = %v，期望 %v", tc.actor, tc.target, got, tc.want)
			}
		})
	}
}
