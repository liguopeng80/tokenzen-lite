package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/datatypes"
	"gorm.io/gorm"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
)

// User 对应 users 表。
type User struct {
	ID            int64             `gorm:"primaryKey" json:"id"`
	Username      string            `json:"username"`
	PasswordHash  string            `json:"-"`
	DisplayName   string            `json:"display_name"`
	Email         string            `json:"email"`
	Role          domain.Role       `json:"role"`
	Status        domain.UserStatus `json:"status"`
	CreditBalance domain.Credits    `json:"credit_balance"`
	CreditUsed    domain.Credits    `json:"credit_used"`
	RequestCount  int64             `json:"request_count"`
	// DepartmentID 所属部门，nil 表示未分配。
	DepartmentID *int64 `json:"department_id"`
	// IntegrationID 所属接入方，nil 表示本机直接管理的账号。
	IntegrationID *int64 `json:"integration_id"`
	// ExternalRef 接入方侧的用户标识，本机直管账号为空串。
	ExternalRef string `gorm:"column:external_ref;default:''" json:"external_ref"`
	// AllowedModels 管理员维护的用户级模型策略，空表示该层不施加限制。
	AllowedModels datatypes.JSON `json:"allowed_models"`
	// DailySpendLimit 单自然日累计扣费积分上限，0 表示不限制。
	DailySpendLimit domain.Credits `json:"daily_spend_limit"`
	// MustChangePassword 密码由管理员设定，本人尚未改过。为真时除改密外的
	// 会话接口一律拒绝；清除该标志的唯一途径是用户自己调用改密接口。
	MustChangePassword bool      `json:"must_change_password"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

// ErrNotFound 表示目标记录不存在。
var ErrNotFound = errors.New("记录不存在")

// UserRepo 封装 users 表访问。
type UserRepo struct{ db *gorm.DB }

func NewUserRepo(db *gorm.DB) *UserRepo { return &UserRepo{db: db} }

func (r *UserRepo) Create(ctx context.Context, u *User) error {
	return r.db.WithContext(ctx).Create(u).Error
}

func (r *UserRepo) GetByID(ctx context.Context, id int64) (*User, error) {
	var u User
	err := r.db.WithContext(ctx).First(&u, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	return &u, err
}

func (r *UserRepo) GetByUsername(ctx context.Context, username string) (*User, error) {
	var u User
	err := r.db.WithContext(ctx).Where("username = ?", username).First(&u).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	return &u, err
}

// GetServiceAccount 返回接入方的服务账号用户（role=managed）。
// 每个接入方在创建时配套一个服务账号，服务令牌认证经它解析身份与作用域。
func (r *UserRepo) GetServiceAccount(ctx context.Context, integrationID int64) (*User, error) {
	var u User
	err := r.db.WithContext(ctx).
		Where("integration_id = ? AND role = ?", integrationID, domain.RoleManaged).
		First(&u).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	return &u, err
}

// GetByExternalRef 按接入方外部标识检索用户。integrationID 非 nil 时限定作用域（托管视角），
// 跨作用域或不存在一律返回 ErrNotFound，不暴露存在性。
func (r *UserRepo) GetByExternalRef(ctx context.Context, ref string, integrationID *int64) (*User, error) {
	q := r.db.WithContext(ctx).Where("external_ref = ?", ref)
	if integrationID != nil {
		q = q.Where("integration_id = ?", *integrationID)
	}
	var u User
	err := q.First(&u).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	return &u, err
}

// UserListFilter 用户列表筛选条件。
// DepartmentID 为 nil 表示不按部门筛选；指向 0 表示只看未分配部门的用户。
type UserListFilter struct {
	Keyword       string
	Role          domain.Role
	Status        domain.UserStatus
	DepartmentID  *int64
	IntegrationID *int64
	Page          int
	PageSize      int
}

func (r *UserRepo) List(ctx context.Context, f UserListFilter) ([]User, int64, error) {
	q := r.db.WithContext(ctx).Model(&User{})
	if f.Keyword != "" {
		kw := "%" + f.Keyword + "%"
		q = q.Where("username ILIKE ? OR display_name ILIKE ? OR email ILIKE ?", kw, kw, kw)
	}
	if f.Role != "" {
		q = q.Where("role = ?", f.Role)
	}
	if f.Status != "" {
		q = q.Where("status = ?", f.Status)
	}
	if f.DepartmentID != nil {
		if *f.DepartmentID == 0 {
			q = q.Where("department_id IS NULL")
		} else {
			q = q.Where("department_id = ?", *f.DepartmentID)
		}
	}
	if f.IntegrationID != nil {
		q = q.Where("integration_id = ?", *f.IntegrationID)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	page, pageSize := NormalizePagination(f.Page, f.PageSize)
	var users []User
	err := q.Order("id").Offset((page - 1) * pageSize).Limit(pageSize).Find(&users).Error
	return users, total, err
}

// UpdateFields 按白名单字段更新用户。
func (r *UserRepo) UpdateFields(ctx context.Context, id int64, fields map[string]any) error {
	fields["updated_at"] = time.Now()
	res := r.db.WithContext(ctx).Model(&User{}).Where("id = ?", id).Updates(fields)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *UserRepo) Delete(ctx context.Context, id int64) error {
	res := r.db.WithContext(ctx).Delete(&User{}, id)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// LowBalanceUser 余额低于预警阈值的用户摘要，供告警正文列名单。
// Email 只用于向本人投递提醒，不进告警载荷——管理员告警里没有用它的场景。
type LowBalanceUser struct {
	ID            int64          `json:"id"`
	Username      string         `json:"username"`
	CreditBalance domain.Credits `json:"credit_balance"`
	Email         string         `json:"-"`
}

// ListLowBalance 返回余额低于阈值的启用用户（按余额升序，最多 limit 条）
// 以及符合条件的总人数。
//
// 两类用户不计入：已禁用的用户，其调用本就被拒绝，余额高低无影响；
// 从未获得过积分也从未消费过的账号（余额与累计消费均为 0），
// 它们没有正在进行的调用可被中断——新装系统的初始管理员即属此类，
// 计入会让每台新装机器立刻收到一条无意义的低余额告警。
func (r *UserRepo) ListLowBalance(ctx context.Context, threshold domain.Credits, limit int) ([]LowBalanceUser, int64, error) {
	q := r.db.WithContext(ctx).Model(&User{}).
		Where("status = ?", domain.UserEnabled).
		Where("credit_balance < ?", threshold).
		Where("credit_balance > 0 OR credit_used > 0")
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if total == 0 {
		return nil, 0, nil
	}
	var users []LowBalanceUser
	err := q.Select("id", "username", "credit_balance", "email").
		Order("credit_balance, id").Limit(limit).Find(&users).Error
	return users, total, err
}

// GrantTarget 按月自动发放的目标账号摘要。
type GrantTarget struct {
	ID            int64
	Username      string
	CreditBalance domain.Credits
}

// ListGrantTargets 按 ID 递增分页返回启用的普通用户，供按月自动发放遍历。
// 用键集分页而非页码分页：发放过程中余额与账号数都在变，页码分页会漏发或重发。
// afterID 传 0 表示从头开始。
func (r *UserRepo) ListGrantTargets(ctx context.Context, afterID int64, limit int) ([]GrantTarget, error) {
	var targets []GrantTarget
	err := r.db.WithContext(ctx).Model(&User{}).
		Where("role = ? AND status = ? AND id > ?", domain.RoleUser, domain.UserEnabled, afterID).
		Select("id", "username", "credit_balance").
		Order("id").Limit(limit).Find(&targets).Error
	return targets, err
}

func (r *UserRepo) Count(ctx context.Context) (int64, error) {
	var n int64
	err := r.db.WithContext(ctx).Model(&User{}).Count(&n).Error
	return n, err
}

// EnsureRootUser 在系统无任何用户时创建初始 root 账号（首次启动引导）。
func (r *UserRepo) EnsureRootUser(ctx context.Context, username, passwordHash string) (created bool, err error) {
	n, err := r.Count(ctx)
	if err != nil {
		return false, fmt.Errorf("统计用户数失败: %w", err)
	}
	if n > 0 {
		return false, nil
	}
	u := &User{
		Username:     username,
		PasswordHash: passwordHash,
		DisplayName:  "Root",
		Role:         domain.RoleRoot,
		Status:       domain.UserEnabled,
	}
	return true, r.Create(ctx, u)
}
