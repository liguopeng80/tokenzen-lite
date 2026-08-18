package api

import (
	"net/http"

	"github.com/liguopeng80/tokenzen-lite/server/internal/alerting"
	"github.com/liguopeng80/tokenzen-lite/server/internal/api/respond"
	"github.com/liguopeng80/tokenzen-lite/server/internal/audit"
	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/obs"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// handleAdminListAlerts 查询告警事件。管理员报告「没收到告警」时，
// 该列表用于区分「未触发」与「触发了但投递失败」。
func (c *billingAdminController) handleAdminListAlerts(w http.ResponseWriter, r *http.Request) {
	page, pageSize := PageParams(r)
	q := r.URL.Query()
	f := store.AlertListFilter{
		AlertType: domain.AlertType(q.Get("alert_type")),
		Severity:  domain.AlertSeverity(q.Get("severity")),
		Status:    domain.AlertStatus(q.Get("status")),
		Page:      page, PageSize: pageSize,
	}
	f.StartTime, f.EndTime = timeRangeParams(r)

	rows, total, err := c.AlertEvents.List(r.Context(), f)
	if err != nil {
		obs.Logger(r.Context()).Error("查询告警事件失败", "error", err)
		respond.Fail(w, http.StatusInternalServerError, "查询告警事件失败")
		return
	}
	respond.OK(w, respond.NewPage(page, pageSize, total, rows))
}

// handleAdminTestAlert 向当前配置的告警通道发一条测试消息。
// 同步投递并回报每条通道的结果——「保存了配置但发不出去」必须在配置时暴露，
// 而不是等到真出故障时才发现。
func (c *billingAdminController) handleAdminTestAlert(w http.ResponseWriter, r *http.Request) {
	if c.Alerts == nil {
		respond.Fail(w, http.StatusServiceUnavailable, "告警组件未就绪")
		return
	}
	cfg := c.Alerts.LoadConfig(r.Context())
	if cfg.WebhookURL == "" && !cfg.SMTP.Configured() {
		respond.Fail(w, http.StatusBadRequest, "尚未配置任何告警通道，请先填写 Webhook 地址或 SMTP 参数")
		return
	}
	event, err := c.Alerts.RaiseSync(r.Context(), alerting.Event{
		Type:     domain.AlertTest,
		Severity: domain.AlertWarning,
		Title:    "告警通道测试",
		Message:  "这是一条来自 Token Zen Lite 的测试消息。收到即表示告警通道配置正确。",
	})
	auditResult := domain.AuditSuccess
	message := ""
	if err != nil {
		auditResult = domain.AuditFailure
		message = err.Error()
	}
	c.Audit.Record(r, audit.Entry{
		Action: domain.AuditAlertTest, TargetType: domain.AuditTargetSetting,
		TargetName: "alert_channels", Result: auditResult, Message: message,
	})
	if err != nil {
		respond.Fail(w, http.StatusBadGateway, "测试消息发送失败："+err.Error())
		return
	}
	respond.OKMessage(w, "测试消息已发送，请到接收端确认", event)
}
