package store

import (
	"context"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
)

// SetupCounts 是判定「新装系统是否已配置到可用状态」所需的计数。
// 各项的判定口径与前端引导文案一一对应，见 api/admin_setup.go。
type SetupCounts struct {
	// ChannelsEnabled 启用状态的上游渠道数。
	ChannelsEnabled int64 `json:"channels_enabled"`
	// ModelsEnabled 已上架（启用）的模型数。
	ModelsEnabled int64 `json:"models_enabled"`
	// ModelsServable 已上架且至少被一条启用渠道承载的模型数。
	// 与 ModelsEnabled 的差额即「上架了但没有渠道能路由」的模型。
	ModelsServable int64 `json:"models_servable"`
	// MemberUsers 普通角色的启用用户数（不含管理员与 root）。
	MemberUsers int64 `json:"member_users"`
	// UsersWithCredits 其中余额为正的用户数。
	UsersWithCredits int64 `json:"users_with_credits"`
}

// SetupCounts 汇总首次配置引导所需的计数。
func (r *StatsRepo) SetupCounts(ctx context.Context) (*SetupCounts, error) {
	c := &SetupCounts{}
	db := r.db.WithContext(ctx)
	if err := db.Model(&Channel{}).
		Where("status = ?", domain.ChannelEnabled).Count(&c.ChannelsEnabled).Error; err != nil {
		return nil, err
	}
	if err := db.Model(&Model{}).
		Where("status = ?", domain.ModelEnabled).Count(&c.ModelsEnabled).Error; err != nil {
		return nil, err
	}
	// 渠道的模型清单是 jsonb 数组，用 jsonb_exists 判断成员存在；
	// 不用 `?` 运算符是因为它与 SQL 占位符冲突。
	if err := db.Raw(`
		SELECT COUNT(*) FROM models m
		WHERE m.status = ? AND EXISTS (
			SELECT 1 FROM channels c
			WHERE c.status = ? AND jsonb_exists(c.models, m.name)
		)`, domain.ModelEnabled, domain.ChannelEnabled).
		Scan(&c.ModelsServable).Error; err != nil {
		return nil, err
	}
	if err := db.Model(&User{}).
		Where("role = ? AND status = ?", domain.RoleUser, domain.UserEnabled).
		Count(&c.MemberUsers).Error; err != nil {
		return nil, err
	}
	if err := db.Model(&User{}).
		Where("role = ? AND status = ? AND credit_balance > 0", domain.RoleUser, domain.UserEnabled).
		Count(&c.UsersWithCredits).Error; err != nil {
		return nil, err
	}
	return c, nil
}
