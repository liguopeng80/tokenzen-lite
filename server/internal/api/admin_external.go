package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/liguopeng80/tokenzen-lite/server/internal/api/respond"
	"github.com/liguopeng80/tokenzen-lite/server/internal/auth"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// admin_external.go：按接入方外部标识精确检索用户与部门（R5）。
// external_ref 写入后不可变（迁移触发器强制），用于两侧对象长期对应。
// 托管视角自动叠加本接入方作用域：跨作用域的对象按不存在处理（404），不暴露存在性。

// externalLookupController 承载 /admin/{users,departments,projects}/external/{ref}
// 三个按接入方外部标识精确检索单个对象的端点（托管桶）。三端点同范式，独立成组。
type externalLookupController struct {
	Departments *store.DepartmentRepo
	Projects    *store.ProjectRepo
	Users       *store.UserRepo
	Settings    *store.SettingsRepo
}

// handleAdminGetUserByExternalRef 按外部标识取回单个用户。
func (c *externalLookupController) handleAdminGetUserByExternalRef(w http.ResponseWriter, r *http.Request) {
	ref := chi.URLParam(r, "ref")
	if ref == "" {
		respond.Fail(w, http.StatusBadRequest, "缺少外部标识")
		return
	}
	u, err := c.Users.GetByExternalRef(r.Context(), ref, auth.ScopeIntegrationID(r.Context()))
	if err != nil {
		respond.Fail(w, http.StatusNotFound, "用户不存在")
		return
	}
	rate := c.Settings.GetInt64(r.Context(), "exchange_rate_credits_per_cny")
	respond.OK(w, wrapUser(*u, newMoneyCtx(rate)))
}

// handleAdminGetDepartmentByExternalRef 按外部标识取回单个部门。
func (c *externalLookupController) handleAdminGetDepartmentByExternalRef(w http.ResponseWriter, r *http.Request) {
	ref := chi.URLParam(r, "ref")
	if ref == "" {
		respond.Fail(w, http.StatusBadRequest, "缺少外部标识")
		return
	}
	dept, err := c.Departments.GetByExternalRef(r.Context(), ref, auth.ScopeIntegrationID(r.Context()))
	if err != nil {
		respond.Fail(w, http.StatusNotFound, "部门不存在")
		return
	}
	rate := c.Settings.GetInt64(r.Context(), "exchange_rate_credits_per_cny")
	respond.OK(w, wrapDepartment(*dept, newMoneyCtx(rate)))
}

// handleAdminGetProjectByExternalRef 按外部标识取回单个项目（同部门范式）。
func (c *externalLookupController) handleAdminGetProjectByExternalRef(w http.ResponseWriter, r *http.Request) {
	ref := chi.URLParam(r, "ref")
	if ref == "" {
		respond.Fail(w, http.StatusBadRequest, "缺少外部标识")
		return
	}
	p, err := c.Projects.GetByExternalRef(r.Context(), ref, auth.ScopeIntegrationID(r.Context()))
	if err != nil {
		respond.Fail(w, http.StatusNotFound, "项目不存在")
		return
	}
	rate := c.Settings.GetInt64(r.Context(), "exchange_rate_credits_per_cny")
	respond.OK(w, wrapProject(*p, newMoneyCtx(rate)))
}
