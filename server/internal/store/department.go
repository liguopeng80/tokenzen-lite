package store

import (
	"context"
	"errors"
	"time"

	"gorm.io/datatypes"
	"gorm.io/gorm"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
)

// Department 对应 departments 表：成本归属单元，单层无上下级。
// 部门不持有积分余额、不参与扣费，仅作费用分摊与预算对比的维度。
type Department struct {
	ID   int64  `gorm:"primaryKey" json:"id"`
	Name string `json:"name"`
	// Code 成本中心编码，对接财务系统用；非空时唯一。
	Code string `json:"code"`
	// OwnerUserID 部门负责人，nil 表示未指定。
	OwnerUserID *int64 `json:"owner_user_id"`
	// IntegrationID 所属接入方，nil 表示本机直接管理的部门。
	IntegrationID *int64 `json:"integration_id"`
	// ExternalRef 接入方侧的部门标识，本机直管部门为空串。
	ExternalRef string `gorm:"column:external_ref;default:''" json:"external_ref"`
	// MonthlyBudgetCredits 月度预算积分，0 表示未设预算，不做超预算告警。
	MonthlyBudgetCredits domain.Credits `json:"monthly_budget_credits"`
	// AllowedModels 部门级模型策略，空表示该层不施加限制。
	AllowedModels datatypes.JSON          `json:"allowed_models"`
	Status        domain.DepartmentStatus `json:"status"`
	Note          string                  `json:"note"`
	CreatedAt     time.Time               `json:"created_at"`
	UpdatedAt     time.Time               `json:"updated_at"`
}

// ErrDepartmentHasMembers 表示部门仍有成员，不允许删除。
var ErrDepartmentHasMembers = errors.New("部门仍有成员")

// DepartmentRepo 封装 departments 表访问。
type DepartmentRepo struct{ db *gorm.DB }

func NewDepartmentRepo(db *gorm.DB) *DepartmentRepo { return &DepartmentRepo{db: db} }

func (r *DepartmentRepo) Create(ctx context.Context, d *Department) error {
	return r.db.WithContext(ctx).Create(d).Error
}

func (r *DepartmentRepo) GetByID(ctx context.Context, id int64) (*Department, error) {
	var d Department
	err := r.db.WithContext(ctx).First(&d, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	return &d, err
}

// GetByExternalRef 按接入方外部标识检索部门。integrationID 非 nil 时限定作用域（托管视角），
// 跨作用域或不存在一律返回 ErrNotFound。
func (r *DepartmentRepo) GetByExternalRef(ctx context.Context, ref string, integrationID *int64) (*Department, error) {
	q := r.db.WithContext(ctx).Where("external_ref = ?", ref)
	if integrationID != nil {
		q = q.Where("integration_id = ?", *integrationID)
	}
	var d Department
	err := q.First(&d).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	return &d, err
}

// DepartmentListFilter 部门列表筛选条件。
type DepartmentListFilter struct {
	Keyword       string
	Status        domain.DepartmentStatus
	IntegrationID *int64
	Page          int
	PageSize      int
}

// DepartmentWithStats 是部门列表行，附带成员数与负责人用户名以便界面直接显示。
type DepartmentWithStats struct {
	Department
	MemberCount int64 `json:"member_count"`
	// OwnerUsername 负责人用户名；负责人未指定、或已不是本部门成员时为空串，
	// 与 ListByOwner 的生效口径一致（见该方法的说明）。
	OwnerUsername string `json:"owner_username"`
}

func (r *DepartmentRepo) List(ctx context.Context, f DepartmentListFilter) ([]DepartmentWithStats, int64, error) {
	q := r.db.WithContext(ctx).Model(&Department{})
	if f.Keyword != "" {
		kw := "%" + f.Keyword + "%"
		q = q.Where("name ILIKE ? OR code ILIKE ?", kw, kw)
	}
	if f.Status != "" {
		q = q.Where("status = ?", f.Status)
	}
	if f.IntegrationID != nil {
		q = q.Where("integration_id = ?", *f.IntegrationID)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	page, pageSize := NormalizePagination(f.Page, f.PageSize)
	var rows []DepartmentWithStats
	err := q.Select(`departments.*,
			(SELECT COUNT(*) FROM users u WHERE u.department_id = departments.id) AS member_count,
			COALESCE((SELECT u.username FROM users u
				WHERE u.id = departments.owner_user_id AND u.department_id = departments.id), '') AS owner_username`).
		Order("departments.id").Offset((page - 1) * pageSize).Limit(pageSize).Scan(&rows).Error
	return rows, total, err
}

// ListByOwner 返回指定用户担任负责人的全部部门，按 ID 排序。
// 部门负责人的查账范围由这一层归属关系决定，不由用户角色决定：
// 授予角色会让权限脱离部门归属，转岗后仍保留原部门的查账能力。
//
// 除负责人字段外还要求该用户当前仍是本部门成员：负责人被转出部门后，
// departments.owner_user_id 可能来不及清理，仅凭该字段判定会让已转岗的人
// 继续看到原部门的消费明细与成员余额。这里是负责人查账权限的唯一入口，
// 把成员条件放在这一层，成员关系无论从哪条路径变更都即时生效。
func (r *DepartmentRepo) ListByOwner(ctx context.Context, userID int64) ([]Department, error) {
	var rows []Department
	err := r.db.WithContext(ctx).
		Where("owner_user_id = ?", userID).
		Where("EXISTS (SELECT 1 FROM users u WHERE u.id = ? AND u.department_id = departments.id)", userID).
		Order("id").Find(&rows).Error
	return rows, err
}

// ListAll 返回全部部门，供下拉选择与报表补名使用。
func (r *DepartmentRepo) ListAll(ctx context.Context) ([]Department, error) {
	var rows []Department
	err := r.db.WithContext(ctx).Order("id").Find(&rows).Error
	return rows, err
}

// UpdateFields 按白名单字段更新部门。
func (r *DepartmentRepo) UpdateFields(ctx context.Context, id int64, fields map[string]any) error {
	fields["updated_at"] = time.Now()
	res := r.db.WithContext(ctx).Model(&Department{}).Where("id = ?", id).Updates(fields)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// MemberCount 返回部门成员数。
func (r *DepartmentRepo) MemberCount(ctx context.Context, id int64) (int64, error) {
	var n int64
	err := r.db.WithContext(ctx).Model(&User{}).Where("department_id = ?", id).Count(&n).Error
	return n, err
}

// Delete 删除部门。仍有成员时返回 ErrDepartmentHasMembers，需先转出成员。
// 已产生的用量日志与流水中的部门快照保留原 ID，报表显示为「已删除部门 #N」。
func (r *DepartmentRepo) Delete(ctx context.Context, id int64) error {
	n, err := r.MemberCount(ctx, id)
	if err != nil {
		return err
	}
	if n > 0 {
		return ErrDepartmentHasMembers
	}
	res := r.db.WithContext(ctx).Delete(&Department{}, id)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// AssignMembers 把指定用户批量划入部门（departmentID 为 nil 表示转为未分配）。
// 返回实际改动的用户数。
func (r *DepartmentRepo) AssignMembers(ctx context.Context, userIDs []int64, departmentID *int64) (int64, error) {
	if len(userIDs) == 0 {
		return 0, nil
	}
	res := r.db.WithContext(ctx).Model(&User{}).Where("id IN ?", userIDs).
		Updates(map[string]any{"department_id": departmentID, "updated_at": time.Now()})
	return res.RowsAffected, res.Error
}
