package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/liguopeng80/tokenzen-lite/server/internal/api/respond"
	"github.com/liguopeng80/tokenzen-lite/server/internal/audit"
	"github.com/liguopeng80/tokenzen-lite/server/internal/auth"
	"github.com/liguopeng80/tokenzen-lite/server/internal/billing"
	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/obs"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// maxBatchUsers 单次批量导入的用户数上限。
const maxBatchUsers = 500

type userImportItem struct {
	Username string `json:"username"`
	// Password 可留空，留空时由系统生成一次性初始密码并在响应中返回。
	Password     string `json:"password"`
	DisplayName  string `json:"display_name"`
	Email        string `json:"email"`
	DepartmentID *int64 `json:"department_id"`
	// InitialCredits 建号即发放的积分，0 表示不发放。
	InitialCredits domain.Credits `json:"initial_credits"`
}

type userImportRequest struct {
	Items []userImportItem `json:"items"`
	// DefaultDepartmentID 未逐条指定部门时的默认归属。
	DefaultDepartmentID *int64 `json:"default_department_id"`
}

type userImportResult struct {
	Username string              `json:"username"`
	Action   domain.ImportAction `json:"action"`
	UserID   int64               `json:"user_id"`
	Message  string              `json:"message"`
	// InitialPassword 仅在系统代为生成时出现。明文只在本次响应中返回一次，
	// 不落库、不进日志与审计，管理员须在此时取走并转达本人。
	InitialPassword string `json:"initial_password,omitempty"`
}

type userImportSummary struct {
	Created int                `json:"created"`
	Skipped int                `json:"skipped"`
	Failed  int                `json:"failed"`
	Results []userImportResult `json:"results"`
}

// handleAdminImportUsers 批量创建用户。
//
// 处理粒度为单条：每条独立校验与写入，一条失败不影响其余记录，
// 响应逐条回报处理结果，便于导入方定位到具体是哪一条出了问题。
// 已存在的用户名按 skipped 处理而非覆盖——批量导入不应静默改动既有账号的密码与归属。
func (c *userAdminController) handleAdminImportUsers(w http.ResponseWriter, r *http.Request) {
	actor := auth.CurrentUser(r.Context())
	var req userImportRequest
	if !Bind(w, r, &req) {
		return
	}
	if len(req.Items) == 0 {
		respond.Fail(w, http.StatusBadRequest, "导入内容为空")
		return
	}
	if len(req.Items) > maxBatchUsers {
		respond.Fail(w, http.StatusBadRequest, "单次最多导入 500 个用户，请分批提交")
		return
	}
	defaultDept, ok := c.resolveDepartment(w, r, req.DefaultDepartmentID)
	if !ok {
		return
	}

	summary := userImportSummary{Results: make([]userImportResult, 0, len(req.Items))}
	seen := make(map[string]bool, len(req.Items))
	for i := range req.Items {
		result := c.importOneUser(r, &req.Items[i], defaultDept, actor.ID, seen)
		switch result.Action {
		case domain.ImportCreated:
			summary.Created++
		case domain.ImportSkipped:
			summary.Skipped++
		default:
			summary.Failed++
		}
		summary.Results = append(summary.Results, result)
	}

	obs.Logger(r.Context()).Info("用户批量导入完成", "total", len(req.Items),
		"created", summary.Created, "skipped", summary.Skipped, "failed", summary.Failed)
	result := domain.AuditSuccess
	if summary.Created == 0 {
		result = domain.AuditFailure
	}
	c.Audit.Record(r, audit.Entry{
		Action: domain.AuditUserImport, TargetType: domain.AuditTargetUser, Result: result,
		After: map[string]any{
			"total": len(req.Items), "created": summary.Created,
			"skipped": summary.Skipped, "failed": summary.Failed,
		},
	})
	respond.OK(w, summary)
}

// importOneUser 处理单条导入记录。初始积分的发放与建号不在同一事务：
// 建号成功而发放失败时账号保留，结果标为 failed 并说明原因，管理员补发即可；
// 反之若整体回滚，一条发放失败会连带撤销已建好的账号。
func (c *userAdminController) importOneUser(r *http.Request, item *userImportItem, defaultDept *int64,
	actorID int64, seen map[string]bool) userImportResult {

	username := strings.TrimSpace(item.Username)
	res := userImportResult{Username: username}
	if !usernameRe.MatchString(username) {
		res.Action = domain.ImportFailed
		res.Message = "用户名须为 3-32 位字母、数字、下划线或连字符"
		return res
	}
	if seen[username] {
		res.Action = domain.ImportFailed
		res.Message = "同一批次内用户名重复"
		return res
	}
	seen[username] = true
	if item.InitialCredits < 0 {
		res.Action = domain.ImportFailed
		res.Message = "初始积分不能为负数"
		return res
	}

	ctx := r.Context()
	if _, err := c.Users.GetByUsername(ctx, username); err == nil {
		res.Action = domain.ImportSkipped
		res.Message = "用户名已存在，未改动既有账号"
		return res
	}
	// 密码列留空时由系统生成，管理员不必为每人拟定并保管明文密码。
	plain, generated := strings.TrimSpace(item.Password), false
	if plain == "" {
		var err error
		if plain, err = auth.GenerateInitialPassword(); err != nil {
			res.Action = domain.ImportFailed
			res.Message = "生成初始密码失败"
			return res
		}
		generated = true
	}
	hash, err := auth.HashPassword(plain)
	if err != nil {
		res.Action = domain.ImportFailed
		res.Message = err.Error()
		return res
	}
	dept := defaultDept
	if item.DepartmentID != nil {
		resolved, msg := c.lookupAssignableDepartment(ctx, item.DepartmentID)
		if msg != "" {
			res.Action = domain.ImportFailed
			res.Message = msg
			return res
		}
		dept = resolved
	}

	u := &store.User{
		Username: username, PasswordHash: hash,
		DisplayName: item.DisplayName, Email: item.Email,
		Role: domain.RoleUser, Status: domain.UserEnabled, DepartmentID: dept,
		MustChangePassword: true,
	}
	if err := c.Users.Create(ctx, u); err != nil {
		res.Action = domain.ImportFailed
		res.Message = "创建失败：用户名可能已被占用"
		return res
	}
	res.UserID = u.ID
	res.Action = domain.ImportCreated
	if generated {
		res.InitialPassword = plain
	}

	if item.InitialCredits > 0 {
		_, err := c.Billing.Grant(ctx, u.ID, item.InitialCredits, actorID, "批量导入初始额度", "")
		if err != nil {
			obs.Logger(ctx).Error("批量导入发放初始积分失败",
				"user_id", u.ID, "credits", item.InitialCredits, "error", err)
			res.Action = domain.ImportFailed
			res.Message = "账号已创建，但初始积分发放失败，请单独补发"
		}
	}
	return res
}

// lookupAssignableDepartment 校验单条导入指定的部门是否可接收新成员。
// 返回的错误信息为空表示通过。
func (c *userAdminController) lookupAssignableDepartment(ctx context.Context, departmentID *int64) (*int64, string) {
	if departmentID == nil || *departmentID == 0 {
		return nil, ""
	}
	dept, err := c.Departments.GetByID(ctx, *departmentID)
	if err != nil {
		return nil, "指定的部门不存在"
	}
	if dept.Status != domain.DepartmentEnabled {
		return nil, "指定的部门已停用，不能再分配新成员"
	}
	id := dept.ID
	return &id, ""
}

type batchGrantRequest struct {
	UserIDs []int64        `json:"user_ids"`
	Amount  domain.Credits `json:"amount"`
	Note    string         `json:"note"`
	// DepartmentID 指定时，对该部门全部成员发放（与 UserIDs 二选一）。
	DepartmentID *int64 `json:"department_id"`
	// IdempotencyKey 可选。携带后重复提交只记一次账。
	IdempotencyKey string `json:"idempotency_key"`
}

type batchGrantResult struct {
	UserID  int64  `json:"user_id"`
	OK      bool   `json:"ok"`
	Replay  bool   `json:"replay"`
	Message string `json:"message"`
}

type batchGrantSummary struct {
	Succeeded int                `json:"succeeded"`
	Replayed  int                `json:"replayed"`
	Failed    int                `json:"failed"`
	Results   []batchGrantResult `json:"results"`
}

// handleAdminBatchGrantCredits 按用户列表或部门批量发放积分。
//
// 每个用户一条独立流水，逐个记账而非合并成一笔：合并会让「谁拿到了多少」
// 无法从流水复算，也无法对单个用户单独冲正。
func (c *userAdminController) handleAdminBatchGrantCredits(w http.ResponseWriter, r *http.Request) {
	actor := auth.CurrentUser(r.Context())
	var req batchGrantRequest
	if !Bind(w, r, &req) {
		return
	}
	if req.Amount == 0 {
		respond.Fail(w, http.StatusBadRequest, "调整金额不能为 0")
		return
	}
	if !validIdempotencyKey(req.IdempotencyKey) {
		respond.Fail(w, http.StatusBadRequest, "幂等键须为 1-64 位字母、数字、下划线或连字符")
		return
	}
	userIDs, ok := c.resolveBatchTargets(w, r, req.UserIDs, req.DepartmentID,
		"单次最多向 500 个用户发放，请分批提交")
	if !ok {
		return
	}

	summary := batchGrantSummary{Results: make([]batchGrantResult, 0, len(userIDs))}
	for _, userID := range userIDs {
		item := batchGrantResult{UserID: userID}
		// 每个用户用独立的幂等键，否则同一批次的第二个用户会命中第一个的流水而被判为重放。
		key := req.IdempotencyKey
		if key != "" {
			key = key + "-" + strconv.FormatInt(userID, 10)
		}
		entry, replayed, err := grantCredits(r, c.Billing, c.Ledger, userID, req.Amount, actor.ID, req.Note, key)
		switch {
		case err == nil && replayed:
			item.OK, item.Replay = true, true
			item.Message = "该发放已记账，本次未重复调整"
			summary.Replayed++
		case err == nil:
			item.OK = true
			item.Message = "已发放，余额 " + strconv.FormatInt(entry.BalanceAfter, 10)
			summary.Succeeded++
		case errors.Is(err, billing.ErrInsufficientCredits):
			item.Message = "扣回金额超过该用户当前余额"
			summary.Failed++
		case errors.Is(err, store.ErrNotFound):
			item.Message = "用户不存在"
			summary.Failed++
		default:
			obs.Logger(r.Context()).Error("批量发放积分失败", "user_id", userID, "error", err)
			item.Message = "发放失败"
			summary.Failed++
		}
		summary.Results = append(summary.Results, item)
	}

	obs.Logger(r.Context()).Info("批量发放积分完成", "actor_id", actor.ID,
		"total", len(userIDs), "succeeded", summary.Succeeded,
		"replayed", summary.Replayed, "failed", summary.Failed)
	result := domain.AuditSuccess
	if summary.Succeeded == 0 && summary.Replayed == 0 {
		result = domain.AuditFailure
	}
	c.Audit.Record(r, audit.Entry{
		Action: domain.AuditUserCreditBatch, TargetType: domain.AuditTargetUser, Result: result,
		After: map[string]any{
			"amount": req.Amount, "note": req.Note, "total": len(userIDs),
			"succeeded": summary.Succeeded, "replayed": summary.Replayed,
			"failed": summary.Failed, "department_id": req.DepartmentID,
		},
	})
	respond.OK(w, summary)
}

// resolveBatchTargets 确定批量操作的目标用户集合：按部门时展开为部门全部成员，
// 否则采用显式提交的用户列表。overLimitMsg 为超出单批上限时的提示。失败时已写出响应。
func (c *userAdminController) resolveBatchTargets(w http.ResponseWriter, r *http.Request,
	userIDs []int64, departmentID *int64, overLimitMsg string) ([]int64, bool) {

	if departmentID != nil && *departmentID > 0 {
		if _, err := c.Departments.GetByID(r.Context(), *departmentID); err != nil {
			respond.Fail(w, http.StatusBadRequest, "部门不存在")
			return nil, false
		}
		members, _, err := c.Users.List(r.Context(), store.UserListFilter{
			DepartmentID: departmentID, Page: 1, PageSize: maxBatchUsers,
		})
		if err != nil {
			respond.Fail(w, http.StatusInternalServerError, "查询部门成员失败")
			return nil, false
		}
		if len(members) == 0 {
			respond.Fail(w, http.StatusBadRequest, "该部门没有成员")
			return nil, false
		}
		ids := make([]int64, 0, len(members))
		for i := range members {
			ids = append(ids, members[i].ID)
		}
		return ids, true
	}
	if len(userIDs) == 0 {
		respond.Fail(w, http.StatusBadRequest, "请指定用户列表或部门")
		return nil, false
	}
	if len(userIDs) > maxBatchUsers {
		respond.Fail(w, http.StatusBadRequest, overLimitMsg)
		return nil, false
	}
	return userIDs, true
}

type batchStatusRequest struct {
	UserIDs []int64 `json:"user_ids"`
	// DepartmentID 指定时，对该部门全部成员操作（与 UserIDs 二选一）。
	DepartmentID *int64            `json:"department_id"`
	Status       domain.UserStatus `json:"status"`
}

type batchStatusResult struct {
	UserID   int64  `json:"user_id"`
	Username string `json:"username"`
	OK       bool   `json:"ok"`
	Message  string `json:"message"`
}

type batchStatusSummary struct {
	Succeeded int                 `json:"succeeded"`
	Unchanged int                 `json:"unchanged"`
	Failed    int                 `json:"failed"`
	Results   []batchStatusResult `json:"results"`
}

// handleAdminBatchSetUserStatus 批量启用或禁用用户，用于离职与调岗的集中处置。
//
// 处理粒度为单条：越权、目标不存在等只让该条失败，其余照常执行，
// 响应逐条回报结果——批量禁用时哪个账号没禁掉必须能看出来，否则会留下仍可调用的账号。
func (c *userAdminController) handleAdminBatchSetUserStatus(w http.ResponseWriter, r *http.Request) {
	actor := auth.CurrentUser(r.Context())
	var req batchStatusRequest
	if !Bind(w, r, &req) {
		return
	}
	if req.Status != domain.UserEnabled && req.Status != domain.UserDisabled {
		respond.Fail(w, http.StatusBadRequest, "状态取值不合法")
		return
	}
	userIDs, ok := c.resolveBatchTargets(w, r, req.UserIDs, req.DepartmentID,
		"单次最多处理 500 个用户，请分批提交")
	if !ok {
		return
	}

	summary := batchStatusSummary{Results: make([]batchStatusResult, 0, len(userIDs))}
	changed := make([]int64, 0, len(userIDs))
	for _, userID := range userIDs {
		item := c.setOneUserStatus(r, userID, req.Status, actor)
		switch {
		case item.OK && item.Message == "":
			summary.Succeeded++
			changed = append(changed, userID)
		case item.OK:
			summary.Unchanged++
		default:
			summary.Failed++
		}
		summary.Results = append(summary.Results, item)
	}

	obs.Logger(r.Context()).Info("批量变更用户状态", "actor_id", actor.ID,
		"status", req.Status, "total", len(userIDs), "succeeded", summary.Succeeded,
		"unchanged", summary.Unchanged, "failed", summary.Failed)
	result := domain.AuditSuccess
	if summary.Succeeded == 0 {
		result = domain.AuditFailure
	}
	c.Audit.Record(r, audit.Entry{
		Action: domain.AuditUserStatusBatch, TargetType: domain.AuditTargetUser, Result: result,
		After: map[string]any{
			"status": req.Status, "total": len(userIDs),
			"succeeded": summary.Succeeded, "unchanged": summary.Unchanged,
			"failed": summary.Failed, "department_id": req.DepartmentID,
			// 记录实际被改动的账号，使「某账号是哪次批量操作被禁用的」可追溯。
			"changed_user_ids": changed,
		},
	})
	respond.OK(w, summary)
}

// setOneUserStatus 处理单个用户的状态变更。返回项的 OK 为 true 且 Message 为空
// 表示状态确实发生了改变；OK 为 true 但 Message 非空表示本就是目标状态，未改动。
func (c *userAdminController) setOneUserStatus(r *http.Request, userID int64,
	status domain.UserStatus, actor *store.User) batchStatusResult {

	item := batchStatusResult{UserID: userID}
	ctx := r.Context()
	target, err := c.Users.GetByID(ctx, userID)
	if err != nil {
		item.Message = "用户不存在"
		return item
	}
	item.Username = target.Username
	if !canManage(actor, target) {
		item.Message = "无权管理该用户"
		return item
	}
	// 禁用自己会让当前管理员立刻失去后续操作能力，批量场景更容易误伤，一律拒绝。
	if target.ID == actor.ID && status == domain.UserDisabled {
		item.Message = "不能禁用自己的账号"
		return item
	}
	if target.Status == status {
		item.OK = true
		item.Message = "已是目标状态，未改动"
		return item
	}
	if err := c.Users.UpdateFields(ctx, target.ID, map[string]any{"status": status}); err != nil {
		obs.Logger(ctx).Error("批量变更用户状态失败", "user_id", target.ID, "error", err)
		item.Message = "更新状态失败"
		return item
	}
	item.OK = true
	return item
}
