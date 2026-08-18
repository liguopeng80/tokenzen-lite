package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"gorm.io/datatypes"
	"gorm.io/gorm"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/pricing"
)

// Model 对应 models 表（对外模型目录）。
type Model struct {
	ID            int64              `gorm:"primaryKey" json:"id"`
	Name          string             `json:"name"`
	DisplayName   string             `json:"display_name"`
	Description   string             `json:"description"`
	Modality      domain.Modality    `json:"modality"`
	BillingMode   domain.BillingMode `json:"billing_mode"`
	Status        domain.ModelStatus `json:"status"`
	Tags          string             `json:"tags"`
	Provider      string             `json:"provider"`
	ContextWindow int64              `json:"context_window"`
	MaxOutput     int64              `json:"max_output"`
	Capabilities  datatypes.JSON     `gorm:"default:'[]'" json:"capabilities"`
	Alias         string             `json:"alias"`
	CreatedAt     time.Time          `json:"created_at"`
	UpdatedAt     time.Time          `json:"updated_at"`

	Price     *ModelPrice     `gorm:"foreignKey:ModelID" json:"price,omitempty"`
	PeakRules []ModelPeakRule `gorm:"foreignKey:ModelID" json:"peak_rules,omitempty"`
}

// ModelPrice 对应 model_prices 表，单价含义见 docs/glossary.md。
type ModelPrice struct {
	ModelID          int64     `gorm:"primaryKey" json:"model_id"`
	InputPrice       int64     `json:"input_price"`
	OutputPrice      int64     `json:"output_price"`
	CacheReadPrice   int64     `json:"cache_read_price"`
	CacheWritePrice  int64     `json:"cache_write_price"`
	AudioInputPrice  int64     `json:"audio_input_price"`
	AudioOutputPrice int64     `json:"audio_output_price"`
	PerCallPrice     int64     `json:"per_call_price"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// ToPricing 转为计费引擎的 Price 值类型。
func (p *ModelPrice) ToPricing() pricing.Price {
	if p == nil {
		return pricing.Price{}
	}
	return pricing.Price{
		InputPrice: p.InputPrice, OutputPrice: p.OutputPrice,
		CacheReadPrice: p.CacheReadPrice, CacheWritePrice: p.CacheWritePrice,
		AudioInputPrice: p.AudioInputPrice, AudioOutputPrice: p.AudioOutputPrice,
		PerCallPrice: p.PerCallPrice,
	}
}

// ModelPeakRule 对应 model_peak_rules 表。
type ModelPeakRule struct {
	ID                int64          `gorm:"primaryKey" json:"id"`
	ModelID           int64          `json:"model_id"`
	Timezone          string         `json:"timezone"`
	StartMinute       int            `json:"start_minute"`
	EndMinute         int            `json:"end_minute"`
	DaysOfWeek        datatypes.JSON `json:"days_of_week"`
	MultiplierPercent int            `json:"multiplier_percent"`
	Enabled           bool           `json:"enabled"`
	CreatedAt         time.Time      `json:"created_at"`
}

// ToPricing 转为计费引擎的 PeakRule 值类型。
func (r *ModelPeakRule) ToPricing() pricing.PeakRule {
	var days []int
	_ = json.Unmarshal(r.DaysOfWeek, &days)
	return pricing.PeakRule{
		Timezone: r.Timezone, StartMinute: r.StartMinute, EndMinute: r.EndMinute,
		DaysOfWeek: days, MultiplierPercent: r.MultiplierPercent, Enabled: r.Enabled,
	}
}

// ModelRepo 封装模型目录访问。
type ModelRepo struct{ db *gorm.DB }

func NewModelRepo(db *gorm.DB) *ModelRepo { return &ModelRepo{db: db} }

func (r *ModelRepo) Create(ctx context.Context, m *Model) error {
	return r.db.WithContext(ctx).Create(m).Error
}

func (r *ModelRepo) GetByID(ctx context.Context, id int64) (*Model, error) {
	var m Model
	err := r.db.WithContext(ctx).Preload("Price").Preload("PeakRules").First(&m, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	return &m, err
}

// GetByName 按对外模型名查询（中继计费路径）。
func (r *ModelRepo) GetByName(ctx context.Context, name string) (*Model, error) {
	var m Model
	err := r.db.WithContext(ctx).Preload("Price").Preload("PeakRules").
		Where("name = ?", name).First(&m).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	return &m, err
}

// ResolveAlias 把对外别名解析为真实模型名。命中返回真实名，
// 未命中（无模型占用该别名）返回空串与 nil error——别名解析是尽力而为的增强，
// 未命中时调用方按原始 model 名继续，不构成错误。查询走部分唯一索引 models_alias_uniq。
func (r *ModelRepo) ResolveAlias(ctx context.Context, alias string) (string, error) {
	if alias == "" {
		return "", nil
	}
	var name string
	err := r.db.WithContext(ctx).Model(&Model{}).
		Where("alias = ?", alias).Limit(1).Pluck("name", &name).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return name, nil
}

// ResolveNameFold 大小写不敏感地把请求模型名解析为目录规范名。
// 客户端可能传入与目录仅大小写不同的名字；归一到目录大小写后，白名单、
// 存在性与渠道匹配均按规范名精确比对。找不到时返回空串，由调用方按原名继续。
func (r *ModelRepo) ResolveNameFold(ctx context.Context, name string) (string, error) {
	if name == "" {
		return "", nil
	}
	var canonical string
	err := r.db.WithContext(ctx).Model(&Model{}).
		Where("LOWER(name) = LOWER(?)", name).Limit(1).Pluck("name", &canonical).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return canonical, nil
}

// ModelListFilter 模型列表筛选。
type ModelListFilter struct {
	Keyword     string
	Status      domain.ModelStatus
	Modality    domain.Modality
	Provider    domain.Provider
	Page        int
	PageSize    int
	WithDetails bool // 是否带出价格与时段规则
}

func (r *ModelRepo) List(ctx context.Context, f ModelListFilter) ([]Model, int64, error) {
	q := r.db.WithContext(ctx).Model(&Model{})
	if f.Keyword != "" {
		kw := "%" + f.Keyword + "%"
		q = q.Where("name ILIKE ? OR display_name ILIKE ? OR tags ILIKE ?", kw, kw, kw)
	}
	if f.Status != "" {
		q = q.Where("status = ?", f.Status)
	}
	if f.Modality != "" {
		q = q.Where("modality = ?", f.Modality)
	}
	if f.Provider != "" {
		q = q.Where("provider = ?", f.Provider)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if f.WithDetails {
		q = q.Preload("Price").Preload("PeakRules")
	}
	var models []Model
	if f.PageSize > 0 {
		q = q.Offset((f.Page - 1) * f.PageSize).Limit(f.PageSize)
	}
	err := q.Order("name").Find(&models).Error
	return models, total, err
}

func (r *ModelRepo) UpdateFields(ctx context.Context, id int64, fields map[string]any) error {
	fields["updated_at"] = time.Now()
	res := r.db.WithContext(ctx).Model(&Model{}).Where("id = ?", id).Updates(fields)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *ModelRepo) Delete(ctx context.Context, id int64) error {
	res := r.db.WithContext(ctx).Delete(&Model{}, id)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// CreateWithPrice 在同一事务内新建模型及其单价（批量导入用）。
// 两者必须同生共死：只建模型不落单价会得到一个可被零扣费调用的已上架模型。
func (r *ModelRepo) CreateWithPrice(ctx context.Context, m *Model, p *ModelPrice) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(m).Error; err != nil {
			return err
		}
		p.ModelID = m.ID
		p.UpdatedAt = time.Now()
		return tx.Create(p).Error
	})
}

// UpdateWithPrice 在同一事务内覆盖模型的展示信息与单价（批量导入的覆盖分支）。
func (r *ModelRepo) UpdateWithPrice(ctx context.Context, id int64, fields map[string]any, p *ModelPrice) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		fields["updated_at"] = time.Now()
		res := tx.Model(&Model{}).Where("id = ?", id).Updates(fields)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return ErrNotFound
		}
		p.ModelID = id
		p.UpdatedAt = time.Now()
		return tx.Save(p).Error
	})
}

// UpsertPrice 写入或覆盖模型单价。
func (r *ModelRepo) UpsertPrice(ctx context.Context, p *ModelPrice) error {
	p.UpdatedAt = time.Now()
	return r.db.WithContext(ctx).Save(p).Error
}

// ReplacePeakRules 全量替换模型的时段倍率规则。
func (r *ModelRepo) ReplacePeakRules(ctx context.Context, modelID int64, rules []ModelPeakRule) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("model_id = ?", modelID).Delete(&ModelPeakRule{}).Error; err != nil {
			return err
		}
		for i := range rules {
			rules[i].ID = 0
			rules[i].ModelID = modelID
			if err := tx.Create(&rules[i]).Error; err != nil {
				return err
			}
		}
		return nil
	})
}
