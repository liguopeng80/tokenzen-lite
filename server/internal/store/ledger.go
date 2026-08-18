package store

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"gorm.io/gorm"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
)

// LedgerEntry 对应 credit_ledger 表（积分流水，对账唯一事实源）。
type LedgerEntry struct {
	ID           int64                  `gorm:"primaryKey" json:"id"`
	UserID       int64                  `json:"user_id"`
	EntryType    domain.LedgerEntryType `json:"entry_type"`
	Amount       domain.Credits         `json:"amount"`
	BalanceAfter domain.Credits         `json:"balance_after"`
	RefType      string                 `json:"ref_type"`
	RefID        int64                  `json:"ref_id"`
	RequestID    string                 `json:"request_id"`
	Note         string                 `json:"note"`
	// OperatorID 发起该笔调整的管理员，0 表示消费/退款/兑换等非管理动作。
	OperatorID int64 `json:"operator_id"`
	// DepartmentID 记账时点用户所属部门的快照，0 表示未分配。
	DepartmentID int64 `json:"department_id"`
	// IntegrationID 记账时点用户所属接入方的快照，0 表示本机直管账号。
	IntegrationID int64     `gorm:"column:integration_id;default:0" json:"integration_id"`
	CreatedAt     time.Time `json:"created_at"`
}

func (LedgerEntry) TableName() string { return "credit_ledger" }

// LedgerRepo 封装流水查询（写入统一走 billing.Service 保证与余额同事务）。
type LedgerRepo struct{ db *gorm.DB }

func NewLedgerRepo(db *gorm.DB) *LedgerRepo { return &LedgerRepo{db: db} }

// LedgerListFilter 流水筛选。UserID 为 0 表示全站（管理端）。
type LedgerListFilter struct {
	UserID        int64
	EntryType     domain.LedgerEntryType
	StartTime     *time.Time
	EndTime       *time.Time
	IntegrationID *int64
	Page          int
	PageSize      int
}

func (r *LedgerRepo) List(ctx context.Context, f LedgerListFilter) ([]LedgerEntry, int64, error) {
	q := r.db.WithContext(ctx).Model(&LedgerEntry{})
	if f.UserID > 0 {
		q = q.Where("user_id = ?", f.UserID)
	}
	if f.EntryType != "" {
		q = q.Where("entry_type = ?", f.EntryType)
	}
	if f.StartTime != nil {
		q = q.Where("created_at >= ?", *f.StartTime)
	}
	if f.EndTime != nil {
		q = q.Where("created_at < ?", *f.EndTime)
	}
	if f.IntegrationID != nil {
		q = q.Where("integration_id = ?", *f.IntegrationID)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	page, pageSize := NormalizePagination(f.Page, f.PageSize)
	var entries []LedgerEntry
	err := q.Order("id DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&entries).Error
	return entries, total, err
}

// callEntryTypes 是一次调用产生的流水类型。同一次调用先预扣（consume），
// 结算时补差额（settle_adjust），失败时退款（refund），三者共用 request_id。
var callEntryTypes = []domain.LedgerEntryType{
	domain.LedgerConsume, domain.LedgerSettleAdjust, domain.LedgerRefund,
}

// mergedGroupKey 是合并流水的分组键表达式：调用类流水按 request_id 归并，
// 其余每条自成一组。取值从枚举拼出，不在 SQL 里硬写字面量。
var mergedGroupKey = fmt.Sprintf(
	`CASE WHEN entry_type IN ('%s','%s','%s') AND request_id <> ''
		THEN 'req:' || request_id ELSE 'entry:' || id::text END`,
	domain.LedgerConsume, domain.LedgerSettleAdjust, domain.LedgerRefund)

// MergedLedgerRow 是员工视角的一条账目。
//
// 一次调用在流水里是两到三条记录：先按最大输出 token 预扣一笔大额，结算时把多扣的
// 退回。员工看到的首屏因此全是「结算差额 +19,046」这样的加号记录，既读不出这次调用
// 花了多少，也容易误以为账户在进账。这里把同一 request_id 的记录合并为一条净额，
// 内部记账过程放进 Entries 供按需展开。
type MergedLedgerRow struct {
	// ID 取组内最后一条流水的 ID，用于排序与行键。
	ID int64 `json:"id"`
	// RequestID 非空表示这是一次调用；管理员发放、兑换码充值等单条账目为空。
	RequestID string `json:"request_id"`
	// EntryType 单条账目沿用原类型；合并出来的调用记为 consume。
	EntryType domain.LedgerEntryType `json:"entry_type"`
	// Amount 净额：预扣与结算差额相加后本次调用实际扣掉的积分。
	Amount       domain.Credits `json:"amount"`
	BalanceAfter domain.Credits `json:"balance_after"`
	Note         string         `json:"note"`
	CreatedAt    time.Time      `json:"created_at"`
	// Entries 构成本行的原始流水，按时间升序。
	Entries []LedgerEntry `json:"entries"`
}

// MergedLedgerFilter 合并流水筛选。OnlyCalls 为真时只看调用扣费，
// 否则 EntryType 非空时按原始类型筛选单条账目。
type MergedLedgerFilter struct {
	UserID    int64
	EntryType domain.LedgerEntryType
	OnlyCalls bool
	Page      int
	PageSize  int
}

// ListMerged 返回按调用合并后的流水。总数与分页都按合并后的行数计算，
// 避免同一次调用的两条记录被分页切开。
func (r *LedgerRepo) ListMerged(ctx context.Context, f MergedLedgerFilter) ([]MergedLedgerRow, int64, error) {
	base := func() *gorm.DB {
		q := r.db.WithContext(ctx).Model(&LedgerEntry{}).Where("user_id = ?", f.UserID)
		switch {
		case f.OnlyCalls:
			q = q.Where("entry_type IN ?", callEntryTypes)
		case f.EntryType != "":
			q = q.Where("entry_type = ?", f.EntryType)
		}
		return q
	}

	var total int64
	if err := base().Select("COUNT(DISTINCT " + mergedGroupKey + ")").
		Row().Scan(&total); err != nil {
		return nil, 0, err
	}
	if total == 0 {
		return []MergedLedgerRow{}, 0, nil
	}

	page, pageSize := NormalizePagination(f.Page, f.PageSize)
	type groupRow struct {
		LastID   int64
		GroupKey string
	}
	var groups []groupRow
	if err := base().
		Select("MAX(id) AS last_id, " + mergedGroupKey + " AS group_key").
		Group(mergedGroupKey).
		Order("last_id DESC").
		Offset((page - 1) * pageSize).Limit(pageSize).
		Scan(&groups).Error; err != nil {
		return nil, 0, err
	}
	if len(groups) == 0 {
		return []MergedLedgerRow{}, total, nil
	}

	// 取回这些分组的全部原始流水，在内存里合并——每页至多几十条，无需再压给数据库。
	keys := make([]string, 0, len(groups))
	for _, g := range groups {
		keys = append(keys, g.GroupKey)
	}
	var entries []LedgerEntry
	if err := r.db.WithContext(ctx).
		Where("user_id = ?", f.UserID).
		Where(mergedGroupKey+" IN ?", keys).
		Order("id").Find(&entries).Error; err != nil {
		return nil, 0, err
	}
	return mergeLedgerGroups(keys, entries), total, nil
}

// mergeLedgerGroups 按 order 给出的分组顺序合并原始流水。
func mergeLedgerGroups(order []string, entries []LedgerEntry) []MergedLedgerRow {
	byKey := make(map[string][]LedgerEntry, len(order))
	for _, e := range entries {
		key := ledgerGroupKeyOf(e)
		byKey[key] = append(byKey[key], e)
	}
	rows := make([]MergedLedgerRow, 0, len(order))
	for _, key := range order {
		group := byKey[key]
		if len(group) == 0 {
			continue
		}
		first, last := group[0], group[len(group)-1]
		row := MergedLedgerRow{
			ID: last.ID, EntryType: first.EntryType,
			BalanceAfter: last.BalanceAfter, Note: first.Note,
			CreatedAt: first.CreatedAt, Entries: group,
		}
		for _, e := range group {
			row.Amount += e.Amount
		}
		if isCallEntry(first.EntryType) && first.RequestID != "" {
			row.RequestID = first.RequestID
			row.EntryType = domain.LedgerConsume
		}
		rows = append(rows, row)
	}
	return rows
}

// ledgerGroupKeyOf 与 SQL 侧的 mergedGroupKey 同口径，用于把取回的流水归到分组。
func ledgerGroupKeyOf(e LedgerEntry) string {
	if isCallEntry(e.EntryType) && e.RequestID != "" {
		return "req:" + e.RequestID
	}
	return "entry:" + strconv.FormatInt(e.ID, 10)
}

func isCallEntry(t domain.LedgerEntryType) bool {
	for _, known := range callEntryTypes {
		if known == t {
			return true
		}
	}
	return false
}

// GetByRequestEntry 按 (request_id, entry_type) 查询流水，用于幂等重放时
// 回查首次记账的结果。
func (r *LedgerRepo) GetByRequestEntry(ctx context.Context, requestID string,
	entryType domain.LedgerEntryType) (*LedgerEntry, error) {

	var e LedgerEntry
	err := r.db.WithContext(ctx).
		Where("request_id = ? AND entry_type = ?", requestID, entryType).First(&e).Error
	if err == gorm.ErrRecordNotFound {
		return nil, ErrNotFound
	}
	return &e, err
}

// CountByUser 返回指定用户的流水条数，用于判断账号是否已产生账务记录。
func (r *LedgerRepo) CountByUser(ctx context.Context, userID int64) (int64, error) {
	var total int64
	err := r.db.WithContext(ctx).Model(&LedgerEntry{}).
		Where("user_id = ?", userID).Count(&total).Error
	return total, err
}

// ReconcileMismatch 是对账不一致的记录。
type ReconcileMismatch struct {
	UserID     int64          `json:"user_id"`
	Username   string         `json:"username"`
	Balance    domain.Credits `json:"balance"`
	LedgerSum  domain.Credits `json:"ledger_sum"`
	Difference domain.Credits `json:"difference"`
}

// Reconcile 校验全部用户的不变式：credit_balance == SUM(ledger.amount)。
// 返回不一致的用户清单；空清单即对账通过。
func (r *LedgerRepo) Reconcile(ctx context.Context) ([]ReconcileMismatch, error) {
	var mismatches []ReconcileMismatch
	err := r.db.WithContext(ctx).Raw(`
		SELECT u.id AS user_id, u.username, u.credit_balance AS balance,
		       COALESCE(l.total, 0) AS ledger_sum,
		       u.credit_balance - COALESCE(l.total, 0) AS difference
		FROM users u
		LEFT JOIN (
			SELECT user_id, SUM(amount) AS total FROM credit_ledger GROUP BY user_id
		) l ON l.user_id = u.id
		WHERE u.credit_balance <> COALESCE(l.total, 0)
		ORDER BY u.id`).Scan(&mismatches).Error
	return mismatches, err
}

// APIKeyReconcileMismatch 是密钥已用额度与流水不一致的记录。
//
// 不变式：api_keys.credit_used == -SUM(credit_ledger.amount)
// WHERE ref_type='api_key' AND ref_id=密钥ID。consume 流水计负、refund 计正、
// settle_adjust 多退少补，故流水求和取反应等于累计已用额度。Difference 为 0 即一致，
// 正值表示 credit_used 偏高（多扣或少退），负值表示偏低。
type APIKeyReconcileMismatch struct {
	KeyID      int64          `json:"key_id"`
	Name       string         `json:"name"`
	CreditUsed domain.Credits `json:"credit_used"`
	LedgerSum  domain.Credits `json:"ledger_sum"`
	Difference domain.Credits `json:"difference"`
}

// ReconcileAPIKeys 校验全部密钥的已用额度不变式：
//
//	credit_used == -SUM(credit_ledger.amount WHERE ref_type='api_key' AND ref_id=key.id)
//
// C1 后 credit_used 已由 billing.applyTx 同事务维护（预扣条件 UPDATE / 结算退款
// GREATEST UPDATE / 命中上限时同事务标记 depleted），不再有脱离事务的裸 SQL。
// 本校验降级为防御性巡检：理论上不应再出现偏离，若发现偏离即表明存在绕过
// billing.Service 的写路径或历史脏数据，需定位根因。单校用户余额对账仍发现不了
// 密钥账漂移，故保留独立扫描。
//
// 纳入全部行（含已软删密钥）：deleted_at 仅用于认证与列表裁剪，已删密钥的
// credit_used 同样应为 0 或与历史流水一致。Raw 查询不经 GORM 软删过滤，自然覆盖。
// 返回不一致的密钥清单；空清单即对账通过。
func (r *LedgerRepo) ReconcileAPIKeys(ctx context.Context) ([]APIKeyReconcileMismatch, error) {
	var mismatches []APIKeyReconcileMismatch
	err := r.db.WithContext(ctx).Raw(`
		SELECT k.id AS key_id, k.name, k.credit_used AS credit_used,
		       COALESCE(SUM(l.amount), 0) AS ledger_sum,
		       k.credit_used + COALESCE(SUM(l.amount), 0) AS difference
		FROM api_keys k
		LEFT JOIN credit_ledger l
		       ON l.ref_type = 'api_key' AND l.ref_id = k.id
		GROUP BY k.id, k.name, k.credit_used
		HAVING k.credit_used <> -COALESCE(SUM(l.amount), 0)
		ORDER BY k.id`).Scan(&mismatches).Error
	return mismatches, err
}
