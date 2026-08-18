package api

import (
	"net/http"
	"strings"

	"github.com/liguopeng80/tokenzen-lite/server/internal/api/respond"
	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/obs"
)

// setupCheckItem 是首次配置引导中的一条检查结果。
// Path 指向管理端的对应页面，使引导可以直接跳转，不必让管理员自行寻找入口。
type setupCheckItem struct {
	Key      domain.SetupCheck `json:"key"`
	Done     bool              `json:"done"`
	Required bool              `json:"required"`
	Title    string            `json:"title"`
	// Detail 说明未完成时的业务后果，而不是重复标题。
	Detail string `json:"detail"`
	Action string `json:"action"`
	Path   string `json:"path"`
}

// handleAdminSetupStatus 返回新装系统的配置完整性检查。
// 判定口径见 docs/glossary.md 的 SetupCheck 一节。
func (c *billingAdminController) handleAdminSetupStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	counts, err := c.Stats.SetupCounts(ctx)
	if err != nil {
		obs.Logger(ctx).Error("查询配置完整性失败", "error", err)
		respond.Fail(w, http.StatusInternalServerError, "查询配置完整性失败")
		return
	}
	serverAddress := strings.TrimSpace(c.Settings.GetString(ctx, "server_address"))
	alertConfigured := strings.TrimSpace(c.Settings.GetString(ctx, "alert_webhook_url")) != "" ||
		(strings.TrimSpace(c.Settings.GetString(ctx, "alert_smtp_host")) != "" &&
			strings.TrimSpace(c.Settings.GetString(ctx, "alert_email_to")) != "")

	checks := []setupCheckItem{
		{
			Key: domain.SetupCheckChannel, Required: true,
			Done:   counts.ChannelsEnabled > 0,
			Title:  "配置上游渠道",
			Detail: "没有启用的渠道时，全部调用无处路由，一律返回无可用渠道。",
			Action: "新建渠道", Path: "/channels",
		},
		{
			Key: domain.SetupCheckModel, Required: true,
			Done:   counts.ModelsEnabled > 0,
			Title:  "上架模型并设定价格",
			Detail: "模型目录为空时，用户端拉取不到任何可调用的模型；可用「导入预置价目」批量上架。",
			Action: "管理模型", Path: "/models",
		},
		{
			Key: domain.SetupCheckModelServable, Required: true,
			Done: counts.ModelsServable > 0,
			Detail: "上架的模型必须出现在某条启用渠道的模型清单中才能被路由，" +
				"否则用户在目录里看得到、调用时被拒。",
			Title:  "让上架模型至少被一条渠道承载",
			Action: "检查渠道模型清单", Path: "/channels",
		},
		{
			Key: domain.SetupCheckMember, Required: true,
			Done:   counts.MemberUsers > 0,
			Title:  "建立员工账号",
			Detail: "只有内置管理员时无人可用；支持逐个新建或按 CSV 批量导入。",
			Action: "管理用户", Path: "/users",
		},
		{
			Key: domain.SetupCheckCredits, Required: true,
			Done:   counts.UsersWithCredits > 0,
			Title:  "为员工发放积分",
			Detail: "余额为零的账号即使密钥正确也会被拒绝调用。",
			Action: "批量发放积分", Path: "/users",
		},
		{
			Key: domain.SetupCheckServerAddress, Required: true,
			Done:  serverAddress != "",
			Title: "填写对外 API 基址",
			Detail: "留空时门户的接入指引按员工浏览器当前地址推断 Base URL，" +
				"容器或反向代理部署下该地址通常不是真实的 API 入口，员工照指引配置会连不通。",
			Action: "填写 server_address", Path: "/settings",
		},
		{
			Key: domain.SetupCheckAlertChannel, Required: false,
			Done:   alertConfigured,
			Title:  "配置告警通道",
			Detail: "未配置时余额不足、渠道自动禁用等告警只落库，管理员不会收到通知。",
			Action: "配置 Webhook 或邮件", Path: "/settings",
		},
	}
	completed := true
	pending := 0
	for _, c := range checks {
		if c.Required && !c.Done {
			completed = false
			pending++
		}
	}
	respond.OK(w, map[string]any{
		"completed":      completed,
		"pending_count":  pending,
		"checks":         checks,
		"counts":         counts,
		"server_address": serverAddress,
	})
}
