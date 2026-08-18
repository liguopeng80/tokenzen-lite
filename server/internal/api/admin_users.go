package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/alexedwards/scs/v2"

	"github.com/liguopeng80/tokenzen-lite/server/internal/api/respond"
	"github.com/liguopeng80/tokenzen-lite/server/internal/audit"
	"github.com/liguopeng80/tokenzen-lite/server/internal/auth"
	"github.com/liguopeng80/tokenzen-lite/server/internal/billing"
	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/obs"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// userAdminController 承载管理端用户与代管密钥端点（托管桶 /admin/users/*）：
// 用户增删改查、状态/密码重置、批量导入与状态变更、批量发放积分、代管用户密钥
// 的签发/改/删。跨 admin_users / admin_users_batch / admin_keys 三文件共享。
type userAdminController struct {
	Audit       *audit.Recorder
	Billing     *billing.Service
	Departments *store.DepartmentRepo
	Keys        *store.APIKeyRepo
	Ledger      *store.LedgerRepo
	Sessions    *scs.SessionManager
	UsageLogs   *store.UsageLogRepo
	Users       *store.UserRepo
	Settings    *store.SettingsRepo
	Projects    *store.ProjectRepo
	Idempotency *store.IdempotencyRepo
}

// userWithMoney 包装 store.User，旁置余额/已用/每日上限的货币串。
type userWithMoney struct {
	store.User
	CreditBalanceMoney   string `json:"credit_balance_money"`
	CreditUsedMoney      string `json:"credit_used_money"`
	DailySpendLimitMoney string `json:"daily_spend_limit_money"`
}

func wrapUser(u store.User, mc moneyCtx) userWithMoney {
	return userWithMoney{
		User:                 u,
		CreditBalanceMoney:   mc.money(u.CreditBalance),
		CreditUsedMoney:      mc.money(u.CreditUsed),
		DailySpendLimitMoney: mc.money(u.DailySpendLimit),
	}
}

// canManage 判断操作者能否管理目标用户。
//   - root 可管理所有人（删除自己的拦截在各自处理函数里）。
//   - 运营 admin 只管理普通用户（不含其他管理员与托管服务账号）。
//   - 托管 managed 只管理本接入方作用域内的普通用户（跨接入方一律不可管）。
func canManage(actor *store.User, target *store.User) bool {
	if actor == nil || target == nil {
		return false
	}
	if actor.Role == domain.RoleRoot {
		return true
	}
	if actor.Role == domain.RoleAdmin {
		return target.Role == domain.RoleUser
	}
	if actor.Role == domain.RoleManaged {
		return target.Role == domain.RoleUser &&
			actor.IntegrationID != nil && target.IntegrationID != nil &&
			*actor.IntegrationID == *target.IntegrationID
	}
	return false
}

func (c *userAdminController) handleAdminListUsers(w http.ResponseWriter, r *http.Request) {
	page, pageSize := PageParams(r)
	q := r.URL.Query()
	f := store.UserListFilter{
		Keyword:  q.Get("keyword"),
		Role:     domain.Role(q.Get("role")),
		Status:   domain.UserStatus(q.Get("status")),
		Page:     page,
		PageSize: pageSize,
	}
	// department_id=0 表示只看未分配部门的用户，与「不按部门筛选」是两回事。
	if raw := q.Get("department_id"); raw != "" {
		if id, ok := parseInt64(raw); ok {
			f.DepartmentID = &id
		}
	}
	// 托管视角：只看本接入方作用域内的普通用户（顺带隐藏自己的 svc: 服务账号与其他角色）。
	// 运营 admin/root 不受限，看得见全部接入方与内部对象。
	if iid := auth.ScopeIntegrationID(r.Context()); iid != nil {
		f.IntegrationID = iid
		f.Role = domain.RoleUser
	}
	users, total, err := c.Users.List(r.Context(), f)
	if err != nil {
		obs.Logger(r.Context()).Error("查询用户列表失败", "error", err)
		respond.Fail(w, http.StatusInternalServerError, "查询用户列表失败")
		return
	}
	rate := c.Settings.GetInt64(r.Context(), "exchange_rate_credits_per_cny")
	mc := newMoneyCtx(rate)
	respond.OK(w, respond.NewPage(page, pageSize, total,
		wrapList(users, func(u store.User) userWithMoney { return wrapUser(u, mc) })))
}

type adminCreateUserRequest struct {
	Username     string      `json:"username"`
	Password     string      `json:"password"`
	DisplayName  string      `json:"display_name"`
	Email        string      `json:"email"`
	Role         domain.Role `json:"role"`
	DepartmentID *int64      `json:"department_id"`
	// Passwordless 建无口令托管账号：不生成口令、不置「首次改密」标志。
	// 这类账号不用于登录，由接入方经服务令牌代管，其调用走 API Key、与口令无关。
	Passwordless bool `json:"passwordless"`
	// ExternalRef 接入方侧的对象标识，写入后不可变更，供接入方按它精确反查归属。
	ExternalRef string `json:"external_ref"`
	// IdempotencyKey 携带后，重复提交只生效一次，第二次返回首次结果并标明重放（R4）。
	IdempotencyKey string `json:"idempotency_key"`
	// InitialCredits 建号即发放的积分，0 表示不发放。与批量导入的同名字段口径一致：
	// 余额为零的账号即使密钥正确也会被拒绝调用，逐个建号时同样需要这一步。
	InitialCredits domain.Credits `json:"initial_credits"`
}

func (c *userAdminController) handleAdminCreateUser(w http.ResponseWriter, r *http.Request) {
	actor := auth.CurrentUser(r.Context())
	var req adminCreateUserRequest
	if !Bind(w, r, &req) {
		return
	}
	if !usernameRe.MatchString(req.Username) {
		respond.Fail(w, http.StatusBadRequest, "用户名须为 3-32 位字母、数字、下划线或连字符")
		return
	}
	if msg := validateEmail(req.Email); msg != "" {
		respond.Fail(w, http.StatusBadRequest, msg)
		return
	}
	req.Email = strings.TrimSpace(req.Email)
	req.ExternalRef = strings.TrimSpace(req.ExternalRef)
	if req.ExternalRef != "" && !externalRefRe.MatchString(req.ExternalRef) {
		respond.Fail(w, http.StatusBadRequest, "外部标识须为 1-64 位字母、数字、下划线、连字符、小数点或冒号")
		return
	}
	if req.InitialCredits < 0 {
		respond.Fail(w, http.StatusBadRequest, "初始积分不能为负数")
		return
	}
	if req.Role == "" {
		req.Role = domain.RoleUser
	}
	if !req.Role.Valid() {
		respond.Fail(w, http.StatusBadRequest, "角色取值不合法")
		return
	}
	// 创建高于普通用户的角色是 root 独占操作
	if req.Role != domain.RoleUser && actor.Role != domain.RoleRoot {
		respond.Fail(w, http.StatusForbidden, "仅超级管理员可创建管理员账号")
		return
	}
	// 幂等（R4）：同一 idempotency_key 重复提交只生效一次，第二次返回首次结果并标明重放。
	if req.IdempotencyKey != "" && !idempotencyKeyRe.MatchString(req.IdempotencyKey) {
		respond.Fail(w, http.StatusBadRequest, "幂等键须为 1-64 位字母、数字、下划线或连字符")
		return
	}
	if firstID, ok := idempotencyLookupReplay(r.Context(), c.Idempotency, req.IdempotencyKey, "user.create"); ok {
		if prior, err := c.Users.GetByID(r.Context(), firstID); err == nil {
			respond.OKMessage(w, "该用户已创建，本次未重复执行（重放）", prior)
			return
		}
	}
	// 托管管理员只能建本接入方作用域内的无口令托管账号：接入方用户不在网关登录，
	// 由接入方经服务令牌代管。运营 admin/root 建的是内部账号，不归属任何接入方。
	managedScope := auth.ScopeIntegrationID(r.Context())
	if managedScope != nil {
		req.Passwordless = true
	}
	// 密码留空时由系统生成一次性初始密码，省去管理员逐人拟定密码这一步；
	// 明文只在本次响应中返回，不落库也不进审计。
	// Passwordless 走另一条路：不生成口令、不置「首次改密」，落库空哈希。
	if req.Passwordless && strings.TrimSpace(req.Password) != "" {
		respond.Fail(w, http.StatusBadRequest, "无口令账号不能同时指定密码")
		return
	}
	plain, generated := strings.TrimSpace(req.Password), false
	var hash string
	if req.Passwordless {
		hash = ""
	} else {
		if plain == "" {
			var err error
			if plain, err = auth.GenerateInitialPassword(); err != nil {
				obs.Logger(r.Context()).Error("生成初始密码失败", "error", err)
				respond.Fail(w, http.StatusInternalServerError, "生成初始密码失败")
				return
			}
			generated = true
		}
		var err error
		hash, err = auth.HashPassword(plain)
		if err != nil {
			respond.Fail(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	department, ok := c.resolveDepartment(w, r, req.DepartmentID)
	if !ok {
		return
	}
	u := &store.User{
		Username:     req.Username,
		PasswordHash: hash,
		DisplayName:  req.DisplayName,
		Email:        req.Email,
		Role:         req.Role,
		Status:       domain.UserEnabled,
		DepartmentID: department,
		ExternalRef:  req.ExternalRef,
		// 普通账号的初始密码在本人改掉之前，同时存在于管理员与转达渠道上，
		// 故置「首次改密」；无口令账号不登录，不需要这个门槛。
		MustChangePassword: !req.Passwordless,
	}
	if managedScope != nil {
		u.IntegrationID = managedScope
	}
	if err := c.Users.Create(r.Context(), u); err != nil {
		respond.Fail(w, http.StatusConflict, "用户名已存在")
		return
	}
	if req.IdempotencyKey != "" {
		idempotencyRemember(r.Context(), c.Idempotency, req.IdempotencyKey, "user.create", u.ID)
	}
	obs.Logger(r.Context()).Info("管理员创建用户",
		"actor_id", actor.ID, "user_id", u.ID, "role", u.Role)
	// 初始积分发放失败不回滚建号：账号已经可用，补一次发放即可；
	// 反过来把已建好的账号删掉，反而会让管理员以为整个操作没发生。
	if req.InitialCredits > 0 {
		if _, err := c.Billing.Grant(r.Context(), u.ID, req.InitialCredits,
			actor.ID, "建号初始额度", ""); err != nil {
			obs.Logger(r.Context()).Error("发放初始积分失败",
				"user_id", u.ID, "credits", req.InitialCredits, "error", err)
		} else {
			u.CreditBalance += req.InitialCredits
			c.Audit.Record(r, audit.Entry{
				Action: domain.AuditUserCreditGrant, TargetType: domain.AuditTargetUser,
				TargetID: u.ID, TargetName: u.Username,
				After: map[string]any{"amount": req.InitialCredits, "note": "建号初始额度"},
			})
		}
	}
	c.Audit.Record(r, audit.Entry{
		Action: domain.AuditUserCreate, TargetType: domain.AuditTargetUser,
		TargetID: u.ID, TargetName: u.Username,
		After: map[string]any{
			"username": u.Username, "display_name": u.DisplayName, "email": u.Email,
			"role": u.Role, "status": u.Status, "department_id": u.DepartmentID,
		},
	})
	// 内嵌 userWithMoney（其内嵌 store.User）使字段在 JSON 中平铺，保持既有响应结构不变，
	// 只做新增（_money 货币串）。initial_password 仅在系统代为生成时出现，管理员自行指定密码时不回显。
	rate := c.Settings.GetInt64(r.Context(), "exchange_rate_credits_per_cny")
	resp := struct {
		userWithMoney
		InitialPassword string `json:"initial_password,omitempty"`
	}{userWithMoney: wrapUser(*u, newMoneyCtx(rate))}
	if generated {
		resp.InitialPassword = plain
	}
	respond.Created(w, resp)
}

func (c *userAdminController) handleAdminGetUser(w http.ResponseWriter, r *http.Request) {
	target, ok := loadManagedUser(w, r, c.Users)
	if !ok {
		return
	}
	rate := c.Settings.GetInt64(r.Context(), "exchange_rate_credits_per_cny")
	respond.OK(w, wrapUser(*target, newMoneyCtx(rate)))
}

// loadManagedUser 加载目标用户并做管理权限检查；失败时已写出响应。
// 托管管理员越权（含跨作用域）一律按「用户不存在」处理，避免借端点探测对象 ID；
// 运营 admin/root 维持 403（同级互管的既有语义，由既有测试固化）。
//
// 跨 userAdmin / billingAdmin 两组 controller 共用：发放积分、改密、删除等
// 均需先确认操作者对目标用户的管理边界，故为包级自由函数。
func loadManagedUser(w http.ResponseWriter, r *http.Request, users *store.UserRepo) (*store.User, bool) {
	id, ok := IDParam(w, r, "id")
	if !ok {
		return nil, false
	}
	target, err := users.GetByID(r.Context(), id)
	if err != nil {
		respond.Fail(w, http.StatusNotFound, "用户不存在")
		return nil, false
	}
	actor := auth.CurrentUser(r.Context())
	if !canManage(actor, target) {
		if actor != nil && actor.Role == domain.RoleManaged {
			respond.Fail(w, http.StatusNotFound, "用户不存在")
		} else {
			respond.Fail(w, http.StatusForbidden, "无权管理该用户")
		}
		return nil, false
	}
	return target, true
}

type adminUpdateUserRequest struct {
	DisplayName *string      `json:"display_name"`
	Email       *string      `json:"email"`
	Role        *domain.Role `json:"role"`
	// DepartmentID 传 0 表示转为未分配部门，不传表示不变更归属。
	DepartmentID *int64 `json:"department_id"`
	// AllowedModels 管理员维护的用户级模型策略。传空数组表示该层不施加限制。
	AllowedModels []string `json:"allowed_models"`
	// DailySpendLimit 单自然日累计扣费积分上限，0 表示不限制。
	DailySpendLimit *domain.Credits `json:"daily_spend_limit"`
}

func (c *userAdminController) handleAdminUpdateUser(w http.ResponseWriter, r *http.Request) {
	actor := auth.CurrentUser(r.Context())
	target, ok := loadManagedUser(w, r, c.Users)
	if !ok {
		return
	}
	var req adminUpdateUserRequest
	if !Bind(w, r, &req) {
		return
	}
	fields := map[string]any{}
	before := map[string]any{}
	if req.DisplayName != nil {
		fields["display_name"] = *req.DisplayName
		before["display_name"] = target.DisplayName
	}
	if req.Email != nil {
		if msg := validateEmail(*req.Email); msg != "" {
			respond.Fail(w, http.StatusBadRequest, msg)
			return
		}
		fields["email"] = strings.TrimSpace(*req.Email)
		before["email"] = target.Email
	}
	if req.DepartmentID != nil {
		department, ok := c.resolveDepartment(w, r, req.DepartmentID)
		if !ok {
			return
		}
		fields["department_id"] = department
		before["department_id"] = target.DepartmentID
	}
	if req.AllowedModels != nil {
		fields["allowed_models"] = toJSONField(req.AllowedModels)
		before["allowed_models"] = json.RawMessage(nullIfEmptyJSON(target.AllowedModels))
	}
	if req.DailySpendLimit != nil {
		if *req.DailySpendLimit < 0 {
			respond.Fail(w, http.StatusBadRequest, "每日花费上限不能为负数")
			return
		}
		fields["daily_spend_limit"] = *req.DailySpendLimit
		before["daily_spend_limit"] = target.DailySpendLimit
	}
	if req.Role != nil {
		if actor.Role != domain.RoleRoot {
			respond.Fail(w, http.StatusForbidden, "仅超级管理员可变更角色")
			return
		}
		if !req.Role.Valid() {
			respond.Fail(w, http.StatusBadRequest, "角色取值不合法")
			return
		}
		if target.ID == actor.ID {
			respond.Fail(w, http.StatusBadRequest, "不能变更自己的角色")
			return
		}
		fields["role"] = *req.Role
		before["role"] = target.Role
	}
	if len(fields) == 0 {
		respond.Fail(w, http.StatusBadRequest, "没有可更新的字段")
		return
	}
	if err := c.Users.UpdateFields(r.Context(), target.ID, fields); err != nil {
		respond.Fail(w, http.StatusInternalServerError, "更新用户失败")
		return
	}
	// 模型策略与花费上限属于成本管控，与展示信息变更分开记，便于按动作检索。
	action := domain.AuditUserUpdate
	if req.AllowedModels != nil || req.DailySpendLimit != nil {
		action = domain.AuditUserPolicyChange
	}
	c.Audit.Record(r, audit.Entry{
		Action: action, TargetType: domain.AuditTargetUser,
		TargetID: target.ID, TargetName: target.Username,
		Before: before, After: fields,
	})
	respond.OK(w, nil)
}

// resolveDepartment 校验部门归属：nil 或 0 表示未分配；指定部门时要求存在，
// 且已停用的部门不能再接收新成员。失败时已写出响应。
func (c *userAdminController) resolveDepartment(w http.ResponseWriter, r *http.Request,
	departmentID *int64) (*int64, bool) {

	if departmentID == nil || *departmentID == 0 {
		return nil, true
	}
	dept, err := c.Departments.GetByID(r.Context(), *departmentID)
	if err != nil {
		respond.Fail(w, http.StatusBadRequest, "部门不存在")
		return nil, false
	}
	// 托管视角只能把用户分配到本接入方作用域内的部门。
	if !canAccessDepartment(auth.CurrentUser(r.Context()), dept) {
		respond.Fail(w, http.StatusBadRequest, "部门不存在")
		return nil, false
	}
	if dept.Status != domain.DepartmentEnabled {
		respond.Fail(w, http.StatusBadRequest, "该部门已停用，不能再分配新成员")
		return nil, false
	}
	id := dept.ID
	return &id, true
}

// nullIfEmptyJSON 把空的 JSONB 值规范为 JSON null，供审计快照序列化。
func nullIfEmptyJSON(raw []byte) []byte {
	if len(raw) == 0 {
		return []byte("null")
	}
	return raw
}

type adminSetStatusRequest struct {
	Status domain.UserStatus `json:"status"`
}

func (c *userAdminController) handleAdminSetUserStatus(w http.ResponseWriter, r *http.Request) {
	target, ok := loadManagedUser(w, r, c.Users)
	if !ok {
		return
	}
	var req adminSetStatusRequest
	if !Bind(w, r, &req) {
		return
	}
	if req.Status != domain.UserEnabled && req.Status != domain.UserDisabled {
		respond.Fail(w, http.StatusBadRequest, "状态取值不合法")
		return
	}
	if target.ID == auth.CurrentUser(r.Context()).ID {
		respond.Fail(w, http.StatusBadRequest, "不能禁用自己的账号")
		return
	}
	if err := c.Users.UpdateFields(r.Context(), target.ID, map[string]any{"status": req.Status}); err != nil {
		respond.Fail(w, http.StatusInternalServerError, "更新状态失败")
		return
	}
	obs.Logger(r.Context()).Info("管理员变更用户状态",
		"actor_id", auth.CurrentUser(r.Context()).ID, "user_id", target.ID, "status", req.Status)
	c.Audit.Record(r, audit.Entry{
		Action: domain.AuditUserStatusChange, TargetType: domain.AuditTargetUser,
		TargetID: target.ID, TargetName: target.Username,
		Before: map[string]any{"status": target.Status},
		After:  map[string]any{"status": req.Status},
	})
	respond.OK(w, nil)
}

type adminResetPasswordRequest struct {
	Password string `json:"password"`
}

func (c *userAdminController) handleAdminResetPassword(w http.ResponseWriter, r *http.Request) {
	target, ok := loadManagedUser(w, r, c.Users)
	if !ok {
		return
	}
	// 无口令托管账号不用于登录，给它们设密码会破坏这一不变式；如需登录账号请新建。
	if target.PasswordHash == "" {
		respond.Fail(w, http.StatusBadRequest, "该账号为无口令托管账号，不能重置密码")
		return
	}
	var req adminResetPasswordRequest
	if !Bind(w, r, &req) {
		return
	}
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		respond.Fail(w, http.StatusBadRequest, err.Error())
		return
	}
	// 管理员为他人重置的密码是一次性凭证，要求本人首次登录后改掉；
	// root 重置自己的密码属于本人操作，不置位。
	fields := map[string]any{"password_hash": hash}
	if actorID := auth.CurrentUser(r.Context()).ID; actorID != target.ID {
		fields["must_change_password"] = true
	}
	if err := c.Users.UpdateFields(r.Context(), target.ID, fields); err != nil {
		respond.Fail(w, http.StatusInternalServerError, "重置密码失败")
		return
	}
	// 密码已重置：作废目标用户全部登录会话；root 重置自己密码时重新签发当前会话
	if err := auth.DestroyUserSessions(r.Context(), c.Sessions, target.ID); err != nil {
		obs.Logger(r.Context()).Error("作废用户历史会话失败", "user_id", target.ID, "error", err)
	}
	actor := auth.CurrentUser(r.Context())
	if target.ID == actor.ID {
		if err := auth.LoginSession(c.Sessions, r, actor.ID); err != nil {
			obs.Logger(r.Context()).Error("改密后重新签发会话失败", "user_id", actor.ID, "error", err)
		}
	}
	obs.Logger(r.Context()).Info("管理员重置用户密码，该用户历史会话已作废",
		"actor_id", actor.ID, "user_id", target.ID)
	c.Audit.Record(r, audit.Entry{
		Action: domain.AuditUserPasswordReset, TargetType: domain.AuditTargetUser,
		TargetID: target.ID, TargetName: target.Username,
		After:   map[string]any{"password_hash": "changed"},
		Message: "该用户全部登录会话已作废",
	})
	respond.OKMessage(w, "密码已重置，该用户全部登录已失效", nil)
}

// handleAdminDeleteUser 删除账号。删除仅适用于尚未产生账务记录的误建账号：
// 一旦存在积分流水或用量日志，账号即承载了对账与成本分摊所需的历史，必须保留，
// 退役改用禁用（POST /admin/users/{id}/status）。数据库侧另有 RESTRICT 外键兜底。
func (c *userAdminController) handleAdminDeleteUser(w http.ResponseWriter, r *http.Request) {
	target, ok := loadManagedUser(w, r, c.Users)
	if !ok {
		return
	}
	if target.ID == auth.CurrentUser(r.Context()).ID {
		respond.Fail(w, http.StatusBadRequest, "不能删除自己的账号")
		return
	}
	if msg, ok := c.userDeletionBlocked(r, target.ID); !ok {
		c.Audit.Record(r, audit.Entry{
			Action: domain.AuditUserDelete, TargetType: domain.AuditTargetUser,
			TargetID: target.ID, TargetName: target.Username,
			Result: domain.AuditFailure, Message: msg,
		})
		respond.Fail(w, http.StatusConflict, msg)
		return
	}
	if err := c.Users.Delete(r.Context(), target.ID); err != nil {
		obs.Logger(r.Context()).Error("删除用户失败", "user_id", target.ID, "error", err)
		respond.Fail(w, http.StatusInternalServerError, "删除用户失败")
		return
	}
	// 账号已不存在：作废其全部登录会话，避免残留会话在会话表中自然过期前仍被携带。
	if err := auth.DestroyUserSessions(r.Context(), c.Sessions, target.ID); err != nil {
		obs.Logger(r.Context()).Error("作废已删除用户的会话失败", "user_id", target.ID, "error", err)
	}
	obs.Logger(r.Context()).Info("管理员删除用户",
		"actor_id", auth.CurrentUser(r.Context()).ID, "user_id", target.ID)
	c.Audit.Record(r, audit.Entry{
		Action: domain.AuditUserDelete, TargetType: domain.AuditTargetUser,
		TargetID: target.ID, TargetName: target.Username,
		Before: map[string]any{
			"username": target.Username, "role": target.Role, "status": target.Status,
			"department_id": target.DepartmentID,
		},
	})
	respond.OK(w, nil)
}

// userDeletionBlocked 检查账号是否已产生不可销毁的历史记录。
// 返回 false 时第一个返回值是面向管理员的拒绝原因。
func (c *userAdminController) userDeletionBlocked(r *http.Request, userID int64) (string, bool) {
	ledgerCount, err := c.Ledger.CountByUser(r.Context(), userID)
	if err != nil {
		obs.Logger(r.Context()).Error("统计用户流水失败", "user_id", userID, "error", err)
		return "无法确认该用户是否已产生积分流水，删除已中止", false
	}
	if ledgerCount > 0 {
		return "该用户已产生积分流水，删除会销毁对账所需的历史记录；请改用禁用账号", false
	}
	usageCount, err := c.UsageLogs.CountByUser(r.Context(), userID)
	if err != nil {
		obs.Logger(r.Context()).Error("统计用户用量日志失败", "user_id", userID, "error", err)
		return "无法确认该用户是否已产生调用记录，删除已中止", false
	}
	if usageCount > 0 {
		return "该用户已产生调用记录，删除会销毁成本分摊所需的历史；请改用禁用账号", false
	}
	return "", true
}

// handleAdminListUserKeys 管理员查看指定用户的全部 API Key（旧系统缺失能力）。
// 与重置密码、删除等操作共用 loadManagedUser 的管理边界：
// admin 只能查看普通用户，root 可查看所有人。
func (c *userAdminController) handleAdminListUserKeys(w http.ResponseWriter, r *http.Request) {
	target, ok := loadManagedUser(w, r, c.Users)
	if !ok {
		return
	}
	page, pageSize := PageParams(r)
	keys, total, err := c.Keys.List(r.Context(), store.APIKeyListFilter{
		UserID: target.ID, Page: page, PageSize: pageSize,
	})
	if err != nil {
		respond.Fail(w, http.StatusInternalServerError, "查询用户密钥失败")
		return
	}
	rate := c.Settings.GetInt64(r.Context(), "exchange_rate_credits_per_cny")
	mc := newMoneyCtx(rate)
	respond.OK(w, respond.NewPage(page, pageSize, total,
		wrapList(keys, func(k store.APIKey) apiKeyWithMoney { return wrapAPIKey(k, mc) })))
}
