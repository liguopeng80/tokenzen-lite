package store

import (
	"context"
	"time"

	"gorm.io/datatypes"
	"gorm.io/gorm"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
)

// UsageLog 对应 usage_logs 表：一次 /v1 请求的完整计费与转发记录。
type UsageLog struct {
	ID        int64  `gorm:"primaryKey" json:"id"`
	RequestID string `json:"request_id"`
	UserID    int64  `json:"user_id"`
	APIKeyID  int64  `json:"api_key_id"`
	// DepartmentID 记账时点用户所属部门的快照，0 表示未分配。
	DepartmentID int64 `json:"department_id"`
	// ProjectID 记账时点该密钥所属项目的快照，0 表示未归属（密钥未挂项目或迁移前历史）。
	// 与 DepartmentID 同为记账时点快照，使报表口径不随密钥后续改挂项目而变。
	ProjectID int64 `json:"project_id"`
	// IntegrationID 记账时点用户所属接入方的快照，0 表示本机直管账号。
	IntegrationID         int64                  `gorm:"column:integration_id;default:0" json:"integration_id"`
	ModelName             string                 `json:"model_name"`
	UpstreamModel         string                 `json:"upstream_model"`
	ChannelID             int64                  `json:"channel_id"`
	Protocol              domain.ChannelProtocol `json:"protocol"`
	IsStream              bool                   `json:"is_stream"`
	PromptTokens          int64                  `json:"prompt_tokens"`
	CompletionTokens      int64                  `json:"completion_tokens"`
	CacheReadTokens       int64                  `json:"cache_read_tokens"`
	CacheWriteTokens      int64                  `json:"cache_write_tokens"`
	AudioInputTokens      int64                  `json:"audio_input_tokens"`
	AudioOutputTokens     int64                  `json:"audio_output_tokens"`
	CallCount             int64                  `json:"call_count"`
	UsageSemantic         domain.UsageSemantic   `json:"usage_semantic"`
	UsageEstimated        bool                   `json:"usage_estimated"`
	PeakMultiplierPercent int                    `json:"peak_multiplier_percent"`
	CreditsPrecharged     domain.Credits         `json:"credits_precharged"`
	CreditsCharged        domain.Credits         `json:"credits_charged"`
	CreditsCost           domain.Credits         `json:"credits_cost"`
	Status                domain.UsageStatus     `json:"status"`
	ErrorClass            domain.ErrorClass      `json:"error_class"`
	ErrorMessage          string                 `json:"error_message"`
	LatencyMS             int64                  `json:"latency_ms"`
	FirstByteMS           int64                  `json:"first_byte_ms"`
	ClientIP              string                 `json:"client_ip"`
	PriceSnapshot         datatypes.JSON         `json:"price_snapshot"`
	CreatedAt             time.Time              `json:"created_at"`
}

// UsageLogRepo 封装用量日志访问。
type UsageLogRepo struct{ db *gorm.DB }

func NewUsageLogRepo(db *gorm.DB) *UsageLogRepo { return &UsageLogRepo{db: db} }

func (r *UsageLogRepo) Create(ctx context.Context, l *UsageLog) error {
	return r.db.WithContext(ctx).Create(l).Error
}

// CountByUser 返回指定用户的用量日志条数，用于判断账号是否已产生调用记录。
func (r *UsageLogRepo) CountByUser(ctx context.Context, userID int64) (int64, error) {
	var total int64
	err := r.db.WithContext(ctx).Model(&UsageLog{}).
		Where("user_id = ?", userID).Count(&total).Error
	return total, err
}

// UsageLogFilter 用量日志筛选。
type UsageLogFilter struct {
	UserID int64
	// Username 按用户名模糊匹配（管理端视角）。日志只存 user_id，
	// 但管理员排查时手上有的是用户名，不该先去用户管理页把 ID 查出来。
	// 与 UserID 同时给出时两个条件叠加。
	Username  string
	APIKeyID  int64
	ModelName string
	ChannelID int64
	Status    domain.UsageStatus
	RequestID string
	StartTime *time.Time
	EndTime   *time.Time
	// IntegrationID 为 nil 表示不按接入方筛选；0 表示只看本机直管账号的记录。
	IntegrationID *int64
	Page          int
	PageSize      int
}

// filtered 返回只带筛选条件的查询，供列表与带名称的列表复用。
func (r *UsageLogRepo) filtered(ctx context.Context, f UsageLogFilter) *gorm.DB {
	q := r.db.WithContext(ctx).Model(&UsageLog{})
	if f.UserID > 0 {
		q = q.Where("user_id = ?", f.UserID)
	}
	if f.Username != "" {
		// 用子查询而不是 JOIN：List 与 ListWithNames 共用本函数，
		// 前者不带 users 表，写成 JOIN 会让两条路径的筛选口径分叉。
		q = q.Where("usage_logs.user_id IN (?)",
			r.db.Model(&User{}).Select("id").Where("username ILIKE ?", "%"+f.Username+"%"))
	}
	if f.APIKeyID > 0 {
		q = q.Where("api_key_id = ?", f.APIKeyID)
	}
	if f.ModelName != "" {
		q = q.Where("model_name = ?", f.ModelName)
	}
	if f.ChannelID > 0 {
		q = q.Where("channel_id = ?", f.ChannelID)
	}
	if f.Status != "" {
		q = q.Where("status = ?", f.Status)
	}
	if f.RequestID != "" {
		q = q.Where("request_id = ?", f.RequestID)
	}
	if f.StartTime != nil {
		q = q.Where("created_at >= ?", *f.StartTime)
	}
	if f.EndTime != nil {
		q = q.Where("created_at < ?", *f.EndTime)
	}
	if f.IntegrationID != nil {
		// ListWithNames 会 JOIN users（也有 integration_id 列），必须限定表名，否则列引用歧义。
		q = q.Where("usage_logs.integration_id = ?", *f.IntegrationID)
	}
	return q
}

func (r *UsageLogRepo) List(ctx context.Context, f UsageLogFilter) ([]UsageLog, int64, error) {
	q := r.filtered(ctx, f)
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	page, pageSize := NormalizePagination(f.Page, f.PageSize)
	var logs []UsageLog
	err := q.Order("id DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&logs).Error
	return logs, total, err
}

// UsageLogRow 是管理端列表行，附带用户名与渠道名。日志本身只存 ID，
// 管理员排查某个员工的调用时不该先去用户管理页把 ID 查出来再对号。
// 用户或渠道已被删除时名称为空串，由界面显示为「已删除 #ID」。
type UsageLogRow struct {
	UsageLog
	Username    string `json:"username"`
	ChannelName string `json:"channel_name"`
}

// ListWithNames 与 List 同筛选口径，额外补上用户名与渠道名。
func (r *UsageLogRepo) ListWithNames(ctx context.Context, f UsageLogFilter) ([]UsageLogRow, int64, error) {
	q := r.filtered(ctx, f)
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	page, pageSize := NormalizePagination(f.Page, f.PageSize)
	var rows []UsageLogRow
	err := r.filtered(ctx, f).
		Select(`usage_logs.*,
			COALESCE(u.username, '') AS username,
			COALESCE(c.name, '') AS channel_name`).
		Joins("LEFT JOIN users u ON u.id = usage_logs.user_id").
		Joins("LEFT JOIN channels c ON c.id = usage_logs.channel_id").
		Order("usage_logs.id DESC").Offset((page - 1) * pageSize).Limit(pageSize).
		Scan(&rows).Error
	return rows, total, err
}

// GetByRequestID 按 request_id 精确查询（计费明细直达）。
func (r *UsageLogRepo) GetByRequestID(ctx context.Context, requestID string) (*UsageLog, error) {
	var l UsageLog
	err := r.db.WithContext(ctx).Where("request_id = ?", requestID).First(&l).Error
	if err == gorm.ErrRecordNotFound {
		return nil, ErrNotFound
	}
	return &l, err
}

// RelayHealth 是一个时间窗口内中继请求的健康度快照。
// 口径与用量日志一致：只统计已落库的请求，其中 Failed 为 status = 'failed'
// 的条数——那是上游失败或被拒绝的请求，退款已完成，用户侧表现为调用报错。
type RelayHealth struct {
	Total  int64
	Failed int64
	// P95LatencyMS 该窗口内总耗时的 95 分位。取分位数而非平均值：
	// 平均值会被大量快速失败的请求拉低，掩盖长尾劣化。
	P95LatencyMS int64
}

// FailureRatePercent 返回失败请求占比（百分数，向下取整）。窗口内无请求时为 0。
func (h RelayHealth) FailureRatePercent() int64 {
	if h.Total <= 0 {
		return 0
	}
	return h.Failed * 100 / h.Total
}

// WindowHealth 统计 since 之后的中继请求健康度。
func (r *UsageLogRepo) WindowHealth(ctx context.Context, since time.Time) (RelayHealth, error) {
	var h RelayHealth
	err := r.db.WithContext(ctx).Raw(`
		SELECT
			COUNT(*) AS total,
			COUNT(*) FILTER (WHERE status = ?) AS failed,
			COALESCE(percentile_disc(0.95) WITHIN GROUP (ORDER BY latency_ms), 0) AS p95_latency_ms
		FROM usage_logs
		WHERE created_at >= ?`, domain.UsageFailed, since).Scan(&h).Error
	return h, err
}
