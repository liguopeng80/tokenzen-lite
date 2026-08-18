package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"gorm.io/datatypes"
	"gorm.io/gorm"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
)

// Channel 对应 channels 表。APIKeyEncrypted 不出现在 JSON 中。
type Channel struct {
	ID                int64                  `gorm:"primaryKey" json:"id"`
	Name              string                 `json:"name"`
	Provider          domain.Provider        `json:"provider"`
	Protocol          domain.ChannelProtocol `json:"protocol"`
	BaseURL           string                 `json:"base_url"`
	APIKeyEncrypted   string                 `json:"-"`
	Models            datatypes.JSON         `json:"models"`
	ModelMapping      datatypes.JSON         `json:"model_mapping"`
	Status            domain.ChannelStatus   `json:"status"`
	Priority          int                    `json:"priority"`
	Weight            int                    `json:"weight"`
	ParamOverride     datatypes.JSON         `json:"param_override"`
	HeaderOverride    datatypes.JSON         `json:"header_override"`
	TestModel         string                 `json:"test_model"`
	LastTestAt        *time.Time             `json:"last_test_at"`
	LastTestLatencyMS *int64                 `json:"last_test_latency_ms"`
	LastTestStatus    *string                `gorm:"column:last_test_status" json:"last_test_status"`
	DisabledReason    string                 `json:"disabled_reason"`
	CreatedAt         time.Time              `json:"created_at"`
	UpdatedAt         time.Time              `json:"updated_at"`
}

// ModelList 解析渠道支持的模型清单。
func (c *Channel) ModelList() []string {
	var models []string
	_ = json.Unmarshal(c.Models, &models)
	return models
}

// MappedModel 返回公开模型名对应的上游模型名；无映射时原样返回。
func (c *Channel) MappedModel(publicName string) string {
	var mapping map[string]string
	if err := json.Unmarshal(c.ModelMapping, &mapping); err == nil {
		if upstream, ok := mapping[publicName]; ok && upstream != "" {
			return upstream
		}
	}
	return publicName
}

// HeaderOverrides 解析附加请求头。
func (c *Channel) HeaderOverrides() map[string]string {
	var h map[string]string
	_ = json.Unmarshal(c.HeaderOverride, &h)
	return h
}

// ParamOverrides 解析请求体覆盖参数。
func (c *Channel) ParamOverrides() map[string]any {
	var p map[string]any
	_ = json.Unmarshal(c.ParamOverride, &p)
	return p
}

// ChannelRepo 封装渠道访问。
type ChannelRepo struct{ db *gorm.DB }

func NewChannelRepo(db *gorm.DB) *ChannelRepo { return &ChannelRepo{db: db} }

func (r *ChannelRepo) Create(ctx context.Context, c *Channel) error {
	return r.db.WithContext(ctx).Create(c).Error
}

func (r *ChannelRepo) GetByID(ctx context.Context, id int64) (*Channel, error) {
	var c Channel
	err := r.db.WithContext(ctx).First(&c, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	return &c, err
}

// ChannelListFilter 渠道列表筛选。
type ChannelListFilter struct {
	Keyword  string
	Status   domain.ChannelStatus
	Model    string
	Page     int
	PageSize int
}

func (r *ChannelRepo) List(ctx context.Context, f ChannelListFilter) ([]Channel, int64, error) {
	q := r.db.WithContext(ctx).Model(&Channel{})
	if f.Keyword != "" {
		q = q.Where("name ILIKE ?", "%"+f.Keyword+"%")
	}
	if f.Status != "" {
		q = q.Where("status = ?", f.Status)
	}
	if f.Model != "" {
		q = q.Where("models @> ?", datatypes.JSON(`["`+f.Model+`"]`))
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var channels []Channel
	query := q.Order("priority DESC, id")
	if f.PageSize > 0 {
		query = query.Offset((f.Page - 1) * f.PageSize).Limit(f.PageSize)
	}
	err := query.Find(&channels).Error
	return channels, total, err
}

// CountEnabledByModel 统计每个公开模型名被多少条启用渠道承载。
// 返回 map 仅含至少被一条启用渠道承载的模型名；未出现的模型承载数为 0。
func (r *ChannelRepo) CountEnabledByModel(ctx context.Context) (map[string]int64, error) {
	type row struct {
		ModelName string
		Cnt       int64
	}
	var rows []row
	err := r.db.WithContext(ctx).Raw(`
		SELECT m.model_name AS model_name, COUNT(*) AS cnt
		FROM channels, jsonb_array_elements_text(channels.models) AS m(model_name)
		WHERE channels.status = ?
		GROUP BY m.model_name`, domain.ChannelEnabled).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	counts := make(map[string]int64, len(rows))
	for _, r := range rows {
		counts[r.ModelName] = r.Cnt
	}
	return counts, nil
}

// ListEnabledForModel 返回支持指定公开模型的全部启用渠道（路由候选集）。
func (r *ChannelRepo) ListEnabledForModel(ctx context.Context, model string) ([]Channel, error) {
	b, _ := json.Marshal([]string{model})
	var channels []Channel
	err := r.db.WithContext(ctx).
		Where("status = ? AND models @> ?", domain.ChannelEnabled, datatypes.JSON(b)).
		Order("priority DESC, id").Find(&channels).Error
	return channels, err
}

func (r *ChannelRepo) UpdateFields(ctx context.Context, id int64, fields map[string]any) error {
	fields["updated_at"] = time.Now()
	res := r.db.WithContext(ctx).Model(&Channel{}).Where("id = ?", id).Updates(fields)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *ChannelRepo) Delete(ctx context.Context, id int64) error {
	res := r.db.WithContext(ctx).Delete(&Channel{}, id)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// AutoDisable 因致命错误自动禁用渠道。
func (r *ChannelRepo) AutoDisable(ctx context.Context, id int64, reason string) error {
	return r.UpdateFields(ctx, id, map[string]any{
		"status": domain.ChannelAutoDisabled, "disabled_reason": reason,
	})
}

// ChannelCost 对应 channel_costs 表。
type ChannelCost struct {
	ID             int64     `gorm:"primaryKey" json:"id"`
	ChannelID      int64     `json:"channel_id"`
	ModelName      string    `json:"model_name"`
	Currency       string    `json:"currency"`
	InputCost      int64     `json:"input_cost"`
	OutputCost     int64     `json:"output_cost"`
	CacheReadCost  int64     `json:"cache_read_cost"`
	CacheWriteCost int64     `json:"cache_write_cost"`
	PerCallCost    int64     `json:"per_call_cost"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// ChannelCostRepo 封装渠道成本访问。
type ChannelCostRepo struct{ db *gorm.DB }

func NewChannelCostRepo(db *gorm.DB) *ChannelCostRepo { return &ChannelCostRepo{db: db} }

func (r *ChannelCostRepo) ListByChannel(ctx context.Context, channelID int64) ([]ChannelCost, error) {
	var costs []ChannelCost
	err := r.db.WithContext(ctx).Where("channel_id = ?", channelID).
		Order("model_name").Find(&costs).Error
	return costs, err
}

// ListByModel 跨渠道比价：某公开模型在各渠道的成本。
func (r *ChannelCostRepo) ListByModel(ctx context.Context, modelName string) ([]ChannelCost, error) {
	var costs []ChannelCost
	err := r.db.WithContext(ctx).Where("model_name = ?", modelName).
		Order("channel_id").Find(&costs).Error
	return costs, err
}

// Get 查询单条成本记录；无记录返回 nil（不视为错误）。
func (r *ChannelCostRepo) Get(ctx context.Context, channelID int64, modelName string) (*ChannelCost, error) {
	var c ChannelCost
	err := r.db.WithContext(ctx).
		Where("channel_id = ? AND model_name = ?", channelID, modelName).First(&c).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// ReplaceForChannel 全量替换某渠道的成本表。
func (r *ChannelCostRepo) ReplaceForChannel(ctx context.Context, channelID int64, costs []ChannelCost) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("channel_id = ?", channelID).Delete(&ChannelCost{}).Error; err != nil {
			return err
		}
		for i := range costs {
			costs[i].ID = 0
			costs[i].ChannelID = channelID
			costs[i].UpdatedAt = time.Now()
			if err := tx.Create(&costs[i]).Error; err != nil {
				return err
			}
		}
		return nil
	})
}
