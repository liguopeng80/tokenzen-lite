package api

import (
	"net/http"

	"github.com/liguopeng80/tokenzen-lite/server/internal/api/respond"
	"github.com/liguopeng80/tokenzen-lite/server/internal/auth"
	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/obs"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// handleAdminListAuditLogs 查询操作审计。审计只提供读取，
// 没有更新与删除端点：能被修改的审计不构成审计。
func (c *orgAdminController) handleAdminListAuditLogs(w http.ResponseWriter, r *http.Request) {
	page, pageSize := PageParams(r)
	q := r.URL.Query()
	f := store.AuditListFilter{
		Action:     domain.AuditAction(q.Get("action")),
		TargetType: domain.AuditTargetType(q.Get("target_type")),
		Result:     domain.AuditResult(q.Get("result")),
		Keyword:    q.Get("keyword"),
		Page:       page, PageSize: pageSize,
		// 托管视角只看本接入方触发的审计；系统自动操作（integration_id 为 NULL）不归属
		// 任何接入方，对托管视角不可见。运营 admin/root 无作用域，看得见全部。
		IntegrationID: auth.ScopeIntegrationID(r.Context()),
	}
	if id, ok := parseInt64(q.Get("operator_id")); ok {
		f.OperatorID = id
	}
	if id, ok := parseInt64(q.Get("target_id")); ok {
		f.TargetID = id
	}
	f.StartTime, f.EndTime = timeRangeParams(r)

	rows, total, err := c.AuditLogs.List(r.Context(), f)
	if err != nil {
		obs.Logger(r.Context()).Error("查询审计记录失败", "error", err)
		respond.Fail(w, http.StatusInternalServerError, "查询审计记录失败")
		return
	}
	respond.OK(w, respond.NewPage(page, pageSize, total, rows))
}

// handleAdminAuditActions 下发全部审计动作取值，供管理端筛选下拉直接使用，
// 避免前端硬编码一份随后端漂移的枚举。
func (c *orgAdminController) handleAdminAuditActions(w http.ResponseWriter, _ *http.Request) {
	respond.OK(w, domain.AuditActions)
}
