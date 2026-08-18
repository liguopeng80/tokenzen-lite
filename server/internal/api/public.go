package api

import (
	"net/http"

	"github.com/liguopeng80/tokenzen-lite/server/internal/api/respond"
	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// publicController 承载无需登录的只读端点：站点配置（门户未登录页所需）与
// 已登录用户可见的模型目录。仅持本 feature 所需依赖。
type publicController struct {
	Channels *store.ChannelRepo
	Models   *store.ModelRepo
	Settings *store.SettingsRepo
}

// handleSiteConfig 公开的站点配置（Portal 未登录页也需要）。
func (c *publicController) handleSiteConfig(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	respond.OK(w, map[string]any{
		"site_name":                     c.Settings.GetString(ctx, "site_name"),
		"exchange_rate_credits_per_cny": c.Settings.GetInt64(ctx, "exchange_rate_credits_per_cny"),
		"currency_symbol":               c.Settings.GetString(ctx, "currency_symbol"),
		"register_enabled":              c.Settings.GetBool(ctx, "register_enabled"),
		// 门户余额预警阈值，与管理员侧低余额告警使用同一取值。
		"low_balance_threshold_credits": c.Settings.GetInt64(ctx, "low_balance_threshold_credits"),
		// 用户端接入指引展示的 Base URL。留空表示未配置，由前端按当前站点地址推断。
		"server_address": c.Settings.GetString(ctx, "server_address"),
		// 用户自助修改资料的字段级开关：关闭时门户对应输入框置灰，PUT /auth/profile 拦截。
		"profile_display_name_editable": c.Settings.GetBool(ctx, "profile_display_name_editable"),
		"profile_email_editable":        c.Settings.GetBool(ctx, "profile_email_editable"),
	})
}

// publicModel 公开目录条目：模型信息附加可用性标记。
// available 表示该模型当前至少被一条启用渠道承载，调用可被路由；
// false 时目录仍展示（定价可见），但前端应标注"暂不可用"。
type publicModel struct {
	store.Model
	Available bool `json:"available"`
}

// handlePublicModels 公开模型目录（含单价、时段倍率与可用性标记），仅返回已启用模型。
func (c *publicController) handlePublicModels(w http.ResponseWriter, r *http.Request) {
	models, _, err := c.Models.List(r.Context(), store.ModelListFilter{
		Status:      domain.ModelEnabled,
		WithDetails: true,
	})
	if err != nil {
		respond.Fail(w, http.StatusInternalServerError, "查询模型列表失败")
		return
	}
	counts, err := c.Channels.CountEnabledByModel(r.Context())
	if err != nil {
		respond.Fail(w, http.StatusInternalServerError, "查询模型列表失败")
		return
	}
	items := make([]publicModel, 0, len(models))
	for _, m := range models {
		items = append(items, publicModel{Model: m, Available: counts[m.Name] > 0})
	}
	respond.OK(w, items)
}
