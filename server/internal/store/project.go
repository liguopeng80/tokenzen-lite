package store

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
)

// Project 对应 projects 表：与部门正交的第二层成本归属单元，单层无上下级。
// 与部门同构——不持有积分余额、不参与扣费，仅作费用分摊与预算对比的维度；
// 刻意不加 department_id：项目与部门是两个正交维度，同一密钥可同时归属一个部门与一个项目。
type Project struct {
	ID   int64  `gorm:"primaryKey" json:"id"`
	Name string `json:"name"`
	// Code 项目编码，对接财务系统用；非空时唯一。
	Code string `json:"code"`
	// OwnerUserID 项目负责人，nil 表示未指定。仅作元数据，不派生查账资格
	// （项目无独立的 /api 负责人视图，与部门负责人的 /api/dept 范式不同）。
	OwnerUserID *int64 `json:"owner_user_id"`
	// IntegrationID 所属接入方，nil 表示本机直接管理的项目。
	IntegrationID *int64 `json:"integration_id"`
	// ExternalRef 接入方侧的项目标识，本机直管项目为空串，写入后不可变。
	ExternalRef string `gorm:"column:external_ref;default:''" json:"external_ref"`
	// MonthlyBudgetCredits 月度预算积分，0 表示未设预算，不做超预算告警（同部门）。
	MonthlyBudgetCredits domain.Credits       `json:"monthly_budget_credits"`
	Status               domain.ProjectStatus `json:"status"`
	Note                 string               `json:"note"`
	CreatedAt            time.Time            `json:"created_at"`
	UpdatedAt            time.Time            `json:"updated_at"`
}

// ProjectRepo 封装 projects 表访问。
type ProjectRepo struct{ db *gorm.DB }

func NewProjectRepo(db *gorm.DB) *ProjectRepo { return &ProjectRepo{db: db} }

func (r *ProjectRepo) Create(ctx context.Context, p *Project) error {
	// GORM 会把零值 "" 显式插入，覆盖迁移 DEFAULT 'enabled'，触发 projects_status_check。
	// 在此兜底为 enabled，使直接走 store 层的调用方（不经 api 层 projectPayload.validate）
	// 也满足 CHECK 约束，与 departments 表 status 同口径。
	if p.Status == "" {
		p.Status = domain.ProjectEnabled
	}
	return r.db.WithContext(ctx).Create(p).Error
}

func (r *ProjectRepo) GetByID(ctx context.Context, id int64) (*Project, error) {
	var p Project
	err := r.db.WithContext(ctx).First(&p, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	return &p, err
}

// GetByExternalRef 按接入方外部标识检索项目（同 DepartmentRepo.GetByExternalRef 范式）。
// integrationID 非 nil 时限定作用域（托管视角），跨作用域或不存在一律返回 ErrNotFound。
func (r *ProjectRepo) GetByExternalRef(ctx context.Context, ref string, integrationID *int64) (*Project, error) {
	q := r.db.WithContext(ctx).Where("external_ref = ?", ref)
	if integrationID != nil {
		q = q.Where("integration_id = ?", *integrationID)
	}
	var p Project
	err := q.First(&p).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	return &p, err
}

// ProjectListFilter 项目列表筛选条件。
type ProjectListFilter struct {
	Keyword       string
	Status        domain.ProjectStatus
	IntegrationID *int64
	Page          int
	PageSize      int
}

// ProjectWithStats 是项目列表行，附带密钥数与负责人用户名以便界面直接显示。
// 项目的「成员」是归属它的 API Key（密钥挂项目，而非用户挂项目）。
type ProjectWithStats struct {
	Project
	// KeyCount 归属该项目的密钥数（api_keys.project_id = projects.id）。
	KeyCount int64 `json:"key_count"`
	// OwnerUsername 负责人用户名；未指定负责人时为空串。
	OwnerUsername string `json:"owner_username"`
}

func (r *ProjectRepo) List(ctx context.Context, f ProjectListFilter) ([]ProjectWithStats, int64, error) {
	q := r.db.WithContext(ctx).Model(&Project{})
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
	var rows []ProjectWithStats
	err := q.Select(`projects.*,
			(SELECT COUNT(*) FROM api_keys k WHERE k.project_id = projects.id AND k.deleted_at IS NULL) AS key_count,
			COALESCE((SELECT u.username FROM users u WHERE u.id = projects.owner_user_id), '') AS owner_username`).
		Order("projects.id").Offset((page - 1) * pageSize).Limit(pageSize).Scan(&rows).Error
	return rows, total, err
}

// ListAll 返回全部项目，供下拉选择与报表补名使用。
func (r *ProjectRepo) ListAll(ctx context.Context) ([]Project, error) {
	var rows []Project
	err := r.db.WithContext(ctx).Order("id").Find(&rows).Error
	return rows, err
}

// UpdateFields 按白名单字段更新项目（external_ref 写入后不可变，不在白名单内）。
func (r *ProjectRepo) UpdateFields(ctx context.Context, id int64, fields map[string]any) error {
	fields["updated_at"] = time.Now()
	res := r.db.WithContext(ctx).Model(&Project{}).Where("id = ?", id).Updates(fields)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// KeyCount 返回归属该项目的密钥数。
func (r *ProjectRepo) KeyCount(ctx context.Context, id int64) (int64, error) {
	var n int64
	err := r.db.WithContext(ctx).Model(&APIKey{}).Where("project_id = ?", id).Count(&n).Error
	return n, err
}

// Delete 删除项目。api_keys.project_id 为 ON DELETE SET NULL，归属该项目的密钥
// 会被自动置为未归属；已产生的用量日志与按日汇总中的项目快照保留原 ID，
// 报表显示为「已删除项目 #N」（同部门删除的快照保留范式）。
func (r *ProjectRepo) Delete(ctx context.Context, id int64) error {
	res := r.db.WithContext(ctx).Delete(&Project{}, id)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}
