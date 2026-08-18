package store

import (
	"context"
	"time"

	"gorm.io/gorm"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
)

// 调用类型是面向运营报表的派生维度，由模型形态与是否流式组合得出，
// 不是 usage_logs 上的物理列。权威定义见 docs/glossary.md 的 CallType 节，
// 枚举值已上移至 domain.CallType*。
//
// 下列 untyped string 常量为历史兼容别名，供尚未迁移到 domain.CallType* 的调用方继续引用，
// 单一事实源是 domain 包；新增调用类型不应在此再开桶，应扩展 domain.CallType*。
const (
	CallTypeEmbedding = string(domain.CallTypeEmbedding)
	CallTypeImage     = string(domain.CallTypeImage)
	CallTypeStream    = string(domain.CallTypeStream)
	CallTypeNonStream = string(domain.CallTypeNonStream)
	CallTypeOther     = string(domain.CallTypeOther)
)

// callTypeExpr 按 modality + is_stream 派生调用类型。
// models 表被 LEFT JOIN：模型已删除或不在目录中时 modality 为 NULL，
// 统一落入 'other' 桶，与 glossary 的 CallType 定义一致。
const callTypeExpr = `CASE
	WHEN m.modality = ? THEN ?
	WHEN m.modality = ? THEN ?
	WHEN m.modality = ? AND usage_logs.is_stream THEN ?
	WHEN m.modality = ? AND NOT usage_logs.is_stream THEN ?
	ELSE ?
END`

// callTypeExprArgs 返回 callTypeExpr 的占位符实参，与 CASE 分支顺序对齐。
// 拆出避免调用处维护一个易错的 6 元素字面量切片。
func callTypeExprArgs() []any {
	return []any{
		string(domain.ModalityEmbedding), string(domain.CallTypeEmbedding),
		string(domain.ModalityImage), string(domain.CallTypeImage),
		string(domain.ModalityText), string(domain.CallTypeStream),
		string(domain.ModalityText), string(domain.CallTypeNonStream),
		string(domain.CallTypeOther),
	}
}

// CallTypeRow 是调用类型分布的一行：单个派生调用类型上的请求量、token、扣费、成本与毛利。
type CallTypeRow struct {
	CallType         string         `json:"call_type"`
	Requests         int64          `json:"requests"`
	PromptTokens     int64          `json:"prompt_tokens"`
	CompletionTokens int64          `json:"completion_tokens"`
	CreditsCharged   domain.Credits `json:"credits_charged"`
	CreditsCost      domain.Credits `json:"credits_cost"`
	Margin           domain.Credits `json:"margin"`
}

// CostByCallType 按派生调用类型聚合扣费分布。
//
// 数据来源是原始 usage_logs（按日汇总表不含 is_stream 与 modality 维度），
// 与 models 表 LEFT JOIN 后由 modality + is_stream 派生调用类型，因此受用量日志保留期
// 约束——这是近期窗口（典型 30 天）的视图，落在保留窗口内。
//
//   - integrationID 非 nil 时按接入方作用域收窄（托管视角），与 heatmap/cost-report 同口径；
//   - 只统计 status='settled' 的日志，与其他统计口径一致；
//   - from/to 由调用方按自然日对齐（SpendDay），避免日界歧义。
//
// 返回值只含产生了数据的桶，按 credits_charged 降序；空结果返回长度为 0 的切片。
func (r *StatsRepo) CostByCallType(
	ctx context.Context,
	from, to time.Time,
	integrationID *int64,
) ([]CallTypeRow, error) {
	expr := callTypeExpr
	q := rawLogStatsQuery{
		from: from, to: to, status: domain.UsageSettled,
		integrationID: integrationID,
		tablePrefix:   "usage_logs.",
	}.apply(r.db.WithContext(ctx)).
		Joins("LEFT JOIN models m ON m.name = usage_logs.model_name").
		Select(expr+` AS call_type,
			COUNT(*) AS requests,
			COALESCE(SUM(usage_logs.prompt_tokens),0) AS prompt_tokens,
			COALESCE(SUM(usage_logs.completion_tokens),0) AS completion_tokens,
			COALESCE(SUM(usage_logs.credits_charged),0) AS credits_charged,
			COALESCE(SUM(usage_logs.credits_cost),0) AS credits_cost,
			COALESCE(SUM(usage_logs.credits_charged - usage_logs.credits_cost),0) AS margin`,
			callTypeExprArgs()...)

	var rows []CallTypeRow
	err := q.Group("call_type").Order("credits_charged DESC").Scan(&rows).Error
	if err == gorm.ErrRecordNotFound {
		return []CallTypeRow{}, nil
	}
	if err != nil {
		return nil, err
	}
	if rows == nil {
		rows = []CallTypeRow{}
	}
	return rows, nil
}
