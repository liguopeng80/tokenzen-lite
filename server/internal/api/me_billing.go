package api

import (
	"errors"
	"net/http"

	"github.com/liguopeng80/tokenzen-lite/server/internal/api/respond"
	"github.com/liguopeng80/tokenzen-lite/server/internal/auth"
	"github.com/liguopeng80/tokenzen-lite/server/internal/billing"
	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/obs"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// meStatsController 承载员工自助的统计、用量、计费相关端点（/me 下除密钥外）：
// 余额/兑换/流水、用量日志列表/明细/导出、用量汇总/日趋势/缓存报告/Token 结构/
// 热力图、模型目录与渠道可用性构成的服务状态视图。跨 me_usage / me_billing /
// me_service_status / stats_me 四个文件共享同一 controller。
type meStatsController struct {
	Billing   *billing.Service
	Ledger    *store.LedgerRepo
	Settings  *store.SettingsRepo
	Users     *store.UserRepo
	Channels  *store.ChannelRepo
	Models    *store.ModelRepo
	UsageLogs *store.UsageLogRepo
	Rollup    *store.RollupRepo
	Stats     *store.StatsRepo
}

// handleMeBalance 用户余额/配额查询。
func (c *meStatsController) handleMeBalance(w http.ResponseWriter, r *http.Request) {
	u := auth.CurrentUser(r.Context())
	// 重新读库获取最新余额（session 中的 user 可能滞后）
	fresh, err := c.Users.GetByID(r.Context(), u.ID)
	if err != nil {
		respond.Fail(w, http.StatusInternalServerError, "查询余额失败")
		return
	}
	rate := c.Settings.GetInt64(r.Context(), "exchange_rate_credits_per_cny")
	mc := newMoneyCtx(rate)
	respond.OK(w, map[string]any{
		"credit_balance":                fresh.CreditBalance,
		"credit_balance_money":          mc.money(fresh.CreditBalance),
		"credit_used":                   fresh.CreditUsed,
		"credit_used_money":             mc.money(fresh.CreditUsed),
		"request_count":                 fresh.RequestCount,
		"exchange_rate_credits_per_cny": rate,
		"currency_symbol":               c.Settings.GetString(r.Context(), "currency_symbol"),
	})
}

type redeemRequest struct {
	Code string `json:"code"`
}

// redeemFailureMessage 把核销失败的具体原因翻译成员工能据以行动的提示：
// 抄错了可以自查，已过期或已作废只能找管理员，反复重试没有意义。
func redeemFailureMessage(err error) string {
	switch {
	case errors.Is(err, billing.ErrRedemptionExpired):
		return "兑换码已过期，请联系管理员重新发放"
	case errors.Is(err, billing.ErrRedemptionDisabled):
		return "兑换码已被作废，请联系管理员"
	case errors.Is(err, billing.ErrRedemptionUsed):
		return "兑换码已被使用"
	case errors.Is(err, billing.ErrRedemptionNotFound):
		return "兑换码不存在，请核对后重新输入"
	default:
		return "兑换码不可用，请联系管理员"
	}
}

func (c *meStatsController) handleMeRedeem(w http.ResponseWriter, r *http.Request) {
	u := auth.CurrentUser(r.Context())
	var req redeemRequest
	if !Bind(w, r, &req) {
		return
	}
	if req.Code == "" {
		respond.Fail(w, http.StatusBadRequest, "兑换码不能为空")
		return
	}
	entry, err := c.Billing.Redeem(r.Context(), u.ID, req.Code)
	if err != nil {
		if errors.Is(err, billing.ErrRedemptionUnavailable) {
			respond.Fail(w, http.StatusBadRequest, redeemFailureMessage(err))
			return
		}
		obs.Logger(r.Context()).Error("兑换失败", "error", err, "user_id", u.ID)
		respond.Fail(w, http.StatusInternalServerError, "兑换失败，请稍后重试")
		return
	}
	rate := c.Settings.GetInt64(r.Context(), "exchange_rate_credits_per_cny")
	respond.OK(w, wrapLedgerEntry(*entry, newMoneyCtx(rate)))
}

// ledgerViewRaw 是流水的原始视角：一次调用的预扣与结算差额各占一条。
// 默认视角把它们合并为一条净额，见 store.MergedLedgerRow。
const ledgerViewRaw = "raw"

// handleMeListLedger 返回员工自己的积分流水。默认按调用合并：员工要看的是
// 「这次调用花了多少」，而预扣加结算差额是系统的内部记账过程。
// mergedLedgerRowWithMoney 包装 store.MergedLedgerRow，旁置净额/变动后余额的货币串，
// 并把内部记账明细 Entries 也逐条旁置货币串。Entries 字段在此覆盖嵌入体的同名字段，
// 使 JSON 输出既保留原始字段（经嵌入提升）又新增 _money 兄弟字段。
type mergedLedgerRowWithMoney struct {
	store.MergedLedgerRow
	AmountMoney       string                 `json:"amount_money"`
	BalanceAfterMoney string                 `json:"balance_after_money"`
	Entries           []ledgerEntryWithMoney `json:"entries"`
}

// wrapMergedLedgerRow 把合并流水的净额、变动后余额与每条原始明细都换算为货币串。
func wrapMergedLedgerRow(r store.MergedLedgerRow, mc moneyCtx) mergedLedgerRowWithMoney {
	return mergedLedgerRowWithMoney{
		MergedLedgerRow:   r,
		AmountMoney:       mc.money(r.Amount),
		BalanceAfterMoney: mc.money(r.BalanceAfter),
		Entries: wrapList(r.Entries, func(e store.LedgerEntry) ledgerEntryWithMoney {
			return wrapLedgerEntry(e, mc)
		}),
	}
}

func (c *meStatsController) handleMeListLedger(w http.ResponseWriter, r *http.Request) {
	u := auth.CurrentUser(r.Context())
	page, pageSize := PageParams(r)
	q := r.URL.Query()
	entryType := domain.LedgerEntryType(q.Get("entry_type"))
	rate := c.Settings.GetInt64(r.Context(), "exchange_rate_credits_per_cny")
	mc := newMoneyCtx(rate)
	if q.Get("view") == ledgerViewRaw {
		entries, total, err := c.Ledger.List(r.Context(), store.LedgerListFilter{
			UserID: u.ID, EntryType: entryType,
			Page: page, PageSize: pageSize,
		})
		if err != nil {
			obs.Logger(r.Context()).Error("查询流水失败", "error", err, "user_id", u.ID)
			respond.Fail(w, http.StatusInternalServerError, "查询流水失败")
			return
		}
		respond.OK(w, respond.NewPage(page, pageSize, total,
			wrapList(entries, func(e store.LedgerEntry) ledgerEntryWithMoney {
				return wrapLedgerEntry(e, mc)
			})))
		return
	}
	// 合并视角下按 consume 筛选表示「只看调用扣费」，其余类型按原类型筛选。
	rows, total, err := c.Ledger.ListMerged(r.Context(), store.MergedLedgerFilter{
		UserID:    u.ID,
		EntryType: entryType,
		OnlyCalls: entryType == domain.LedgerConsume,
		Page:      page, PageSize: pageSize,
	})
	if err != nil {
		obs.Logger(r.Context()).Error("查询流水失败", "error", err, "user_id", u.ID)
		respond.Fail(w, http.StatusInternalServerError, "查询流水失败")
		return
	}
	respond.OK(w, respond.NewPage(page, pageSize, total,
		wrapList(rows, func(r store.MergedLedgerRow) mergedLedgerRowWithMoney {
			return wrapMergedLedgerRow(r, mc)
		})))
}
