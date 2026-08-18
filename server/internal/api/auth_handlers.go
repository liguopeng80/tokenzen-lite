package api

import (
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/alexedwards/scs/v2"

	"github.com/liguopeng80/tokenzen-lite/server/internal/api/respond"
	"github.com/liguopeng80/tokenzen-lite/server/internal/audit"
	"github.com/liguopeng80/tokenzen-lite/server/internal/auth"
	"github.com/liguopeng80/tokenzen-lite/server/internal/config"
	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/obs"
	"github.com/liguopeng80/tokenzen-lite/server/internal/ratelimit"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// authController 承载认证与会话相关端点：登录/登出/注册、当前用户、改密、更新资料。
// 仅持本 feature 所需依赖，handler 级编排不再触达渠道/计费等无关句柄。
type authController struct {
	Audit       *audit.Recorder
	Cfg         *config.Config
	Departments *store.DepartmentRepo
	Limiter     ratelimit.Limiter
	LoginLock   *ratelimit.FailureLocker
	Sessions    *scs.SessionManager
	Settings    *store.SettingsRepo
	Users       *store.UserRepo
}

var usernameRe = regexp.MustCompile(`^[a-zA-Z0-9_-]{3,32}$`)

// emailRe 是邮箱的形态校验，只判断「像不像一个可投递的地址」，不做完整 RFC 解析。
// 邮箱是余额不足提醒的收件地址，写错的直接后果是提醒发不出去且没人察觉。
var emailRe = regexp.MustCompile(`^[^\s@]+@[^\s@.]+(\.[^\s@.]+)+$`)

// externalRefRe 是接入方外部标识的形态校验：URL 安全字符，1-64 位。
// external_ref 会出现在检索路径 /external/{ref} 中，故禁止斜杠等路径分隔字符。
var externalRefRe = regexp.MustCompile(`^[A-Za-z0-9_\-.:]{1,64}$`)

// maxEmailLength 与数据库列宽一致。
const maxEmailLength = 254

// validateEmail 校验邮箱形态；空串表示不填写，视为通过。
// 返回面向使用者的错误信息，空串表示通过。
func validateEmail(email string) string {
	email = strings.TrimSpace(email)
	if email == "" {
		return ""
	}
	if len(email) > maxEmailLength {
		return "邮箱地址过长"
	}
	if !emailRe.MatchString(email) {
		return "邮箱格式不正确，示例：name@example.com"
	}
	return ""
}

// 登录与注册接口的防爆破限制（编译期常量，进程内存态计数；说明见 docs/deployment.md）。
// 有意不做成系统设置：这些是安全下限，不应暴露为可在线调低的运行参数。
const (
	loginIPRatePerMin     = 30               // 单来源 IP 每分钟登录尝试上限
	loginUserRatePerMin   = 10               // 单用户名每分钟登录尝试上限
	loginFailureThreshold = 5                // 连续失败达到该次数后锁定账号
	loginLockDuration     = 10 * time.Minute // 账号锁定时长，同时是连续失败计数的观察窗口
	registerIPRatePerHour = 10               // 单来源 IP 每小时注册尝试上限
)

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (c *authController) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if !Bind(w, r, &req) {
		return
	}
	ip := obs.ClientIP(r)
	// 来源 IP 与用户名双维度限流：任一维度超限即拒绝，统计口径为全部尝试（不分成败）。
	if c.Limiter != nil {
		if !c.Limiter.Allow("login:ip:"+ip, loginIPRatePerMin, time.Minute) ||
			!c.Limiter.Allow("login:user:"+req.Username, loginUserRatePerMin, time.Minute) {
			obs.Logger(r.Context()).Warn("登录限流拦截", "ip", ip, "username", req.Username)
			respond.Fail(w, http.StatusTooManyRequests, "登录尝试过于频繁，请稍后重试")
			return
		}
	}
	// 连续失败锁定：锁定期内即使密码正确也拒绝。对不存在的用户名同样计数，
	// 拒绝话术与限流一致，不泄露用户是否存在。
	if c.LoginLock != nil && c.LoginLock.Locked(req.Username) {
		obs.Logger(r.Context()).Warn("账号处于登录锁定期", "ip", ip, "username", req.Username)
		respond.Fail(w, http.StatusTooManyRequests, "登录尝试过于频繁，请稍后重试")
		return
	}
	// 无口令托管账号（PasswordHash 为空）在此一并被拒：bcrypt 校验空哈希必然失败，
	// 与密码错误走同一话术，不泄露账号存在性，也不泄露该账号是否为托管账号。
	u, err := c.Users.GetByUsername(r.Context(), req.Username)
	if err != nil || !auth.VerifyPassword(u.PasswordHash, req.Password) {
		if c.LoginLock != nil {
			c.LoginLock.RecordFailure(req.Username, loginFailureThreshold, loginLockDuration)
		}
		// 统一话术，不泄露用户是否存在；审计里如实记录尝试的用户名，
		// 使「谁在爆破哪个账号」可追溯。
		c.Audit.Record(r, audit.Entry{
			Action: domain.AuditAuthLogin, TargetType: domain.AuditTargetSession,
			TargetName: req.Username, Result: domain.AuditFailure,
			Message: "用户名或密码错误",
		})
		respond.Fail(w, http.StatusUnauthorized, "用户名或密码错误")
		return
	}
	if c.LoginLock != nil {
		c.LoginLock.Reset(req.Username)
	}
	if u.Status != domain.UserEnabled {
		c.Audit.Record(r, audit.Entry{
			Action: domain.AuditAuthLogin, TargetType: domain.AuditTargetSession,
			TargetID: u.ID, TargetName: u.Username, Result: domain.AuditFailure,
			Message: "账号已被禁用", Operator: u,
		})
		respond.Fail(w, http.StatusForbidden, "账号已被禁用")
		return
	}
	if err := auth.LoginSession(c.Sessions, r, u.ID); err != nil {
		obs.Logger(r.Context()).Error("写入 session 失败", "error", err)
		respond.Fail(w, http.StatusInternalServerError, "登录失败，请稍后重试")
		return
	}
	obs.Logger(r.Context()).Info("用户登录", "user_id", u.ID, "username", u.Username)
	// 登录发生在会话建立之前，上下文里还没有当前用户，须显式指定操作人。
	c.Audit.Record(r, audit.Entry{
		Action: domain.AuditAuthLogin, TargetType: domain.AuditTargetSession,
		TargetID: u.ID, TargetName: u.Username, Operator: u,
	})
	respond.OK(w, u)
}

func (c *authController) handleLogout(w http.ResponseWriter, r *http.Request) {
	// 操作人须在会话销毁前取出，之后上下文中的当前用户即不可用。
	operator := auth.CurrentUser(r.Context())
	if err := auth.LogoutSession(c.Sessions, r); err != nil {
		obs.Logger(r.Context()).Error("销毁 session 失败", "error", err)
	}
	if operator != nil {
		c.Audit.Record(r, audit.Entry{
			Action: domain.AuditAuthLogout, TargetType: domain.AuditTargetSession,
			TargetID: operator.ID, TargetName: operator.Username, Operator: operator,
		})
	}
	respond.OK(w, nil)
}

// handleMe 返回当前登录用户，并附带其负责的部门 ID。
// 附带该字段是为了让前端在一次请求内决定是否显示部门费用入口；
// 缺它则每次进入门户都要额外请求一次部门列表，或对全部用户显示入口后再报 403。
func (c *authController) handleMe(w http.ResponseWriter, r *http.Request) {
	u := auth.CurrentUser(r.Context())
	managed := make([]int64, 0)
	if c.Departments != nil && u != nil {
		owned, err := c.Departments.ListByOwner(r.Context(), u.ID)
		if err != nil {
			// 查询失败不阻断登录态获取：部门费用入口不显示，其余功能照常。
			obs.Logger(r.Context()).Error("查询负责部门失败", "user_id", u.ID, "error", err)
		}
		for i := range owned {
			managed = append(managed, owned[i].ID)
		}
	}
	// 内嵌 User 使其字段在 JSON 中平铺，保持既有响应结构不变，只做新增。
	respond.OK(w, struct {
		*store.User
		ManagedDepartmentIDs []int64 `json:"managed_department_ids"`
	}{User: u, ManagedDepartmentIDs: managed})
}

type registerRequest struct {
	Username    string `json:"username"`
	Password    string `json:"password"`
	DisplayName string `json:"display_name"`
}

func (c *authController) handleRegister(w http.ResponseWriter, r *http.Request) {
	// 系统设置优先；设置表不可用时回退到环境配置
	enabled := c.Cfg.RegisterEnabled
	if c.Settings != nil {
		enabled = c.Settings.GetBool(r.Context(), "register_enabled")
	}
	if !enabled {
		respond.Fail(w, http.StatusForbidden, "系统未开放注册")
		return
	}
	var req registerRequest
	if !Bind(w, r, &req) {
		return
	}
	// 按来源 IP 限流，防止批量灌入垃圾账号；统计口径为全部尝试（含校验失败）。
	if ip := obs.ClientIP(r); c.Limiter != nil &&
		!c.Limiter.Allow("register:ip:"+ip, registerIPRatePerHour, time.Hour) {
		obs.Logger(r.Context()).Warn("注册限流拦截", "ip", ip)
		respond.Fail(w, http.StatusTooManyRequests, "注册过于频繁，请稍后重试")
		return
	}
	if !usernameRe.MatchString(req.Username) {
		respond.Fail(w, http.StatusBadRequest, "用户名须为 3-32 位字母、数字、下划线或连字符")
		return
	}
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		respond.Fail(w, http.StatusBadRequest, err.Error())
		return
	}
	u := &store.User{
		Username:     req.Username,
		PasswordHash: hash,
		DisplayName:  req.DisplayName,
		Role:         domain.RoleUser,
		Status:       domain.UserEnabled,
	}
	if err := c.Users.Create(r.Context(), u); err != nil {
		respond.Fail(w, http.StatusConflict, "用户名已存在")
		return
	}
	obs.Logger(r.Context()).Info("用户注册", "user_id", u.ID, "username", u.Username)
	c.Audit.Record(r, audit.Entry{
		Action: domain.AuditAuthRegister, TargetType: domain.AuditTargetUser,
		TargetID: u.ID, TargetName: u.Username, Operator: u,
		After: map[string]any{"username": u.Username, "display_name": u.DisplayName},
	})
	respond.Created(w, u)
}

type changePasswordRequest struct {
	OriginalPassword string `json:"original_password"`
	Password         string `json:"password"`
}

func (c *authController) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	u := auth.CurrentUser(r.Context())
	var req changePasswordRequest
	if !Bind(w, r, &req) {
		return
	}
	if !auth.VerifyPassword(u.PasswordHash, req.OriginalPassword) {
		respond.Fail(w, http.StatusBadRequest, "原密码不正确")
		return
	}
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		respond.Fail(w, http.StatusBadRequest, err.Error())
		return
	}
	// 本人改密是清除「首次登录强制改密」标志的唯一途径。
	if err := c.Users.UpdateFields(r.Context(), u.ID, map[string]any{
		"password_hash": hash, "must_change_password": false,
	}); err != nil {
		respond.Fail(w, http.StatusInternalServerError, "修改密码失败")
		return
	}
	// 密码已变更：作废该用户全部登录会话，再为当前会话重新签发，保持本次登录有效
	if err := auth.DestroyUserSessions(r.Context(), c.Sessions, u.ID); err != nil {
		obs.Logger(r.Context()).Error("作废用户历史会话失败", "user_id", u.ID, "error", err)
	}
	if err := auth.LoginSession(c.Sessions, r, u.ID); err != nil {
		obs.Logger(r.Context()).Error("改密后重新签发会话失败", "user_id", u.ID, "error", err)
	}
	obs.Logger(r.Context()).Info("用户修改密码，历史会话已作废", "user_id", u.ID)
	c.Audit.Record(r, audit.Entry{
		Action: domain.AuditAuthPasswordChange, TargetType: domain.AuditTargetUser,
		TargetID: u.ID, TargetName: u.Username,
		Message: "该用户历史登录会话已作废",
	})
	respond.OK(w, nil)
}

type updateProfileRequest struct {
	DisplayName *string `json:"display_name"`
	Email       *string `json:"email"`
}

func (c *authController) handleUpdateProfile(w http.ResponseWriter, r *http.Request) {
	u := auth.CurrentUser(r.Context())
	var req updateProfileRequest
	if !Bind(w, r, &req) {
		return
	}
	// 字段级全局锁定：管理员关闭对应开关后，用户自助修改该字段一律拒绝；管理员侧不受影响。
	if req.DisplayName != nil && c.Settings != nil &&
		!c.Settings.GetBool(r.Context(), "profile_display_name_editable") {
		respond.Fail(w, http.StatusForbidden, "显示名称已由管理员锁定，不可自助修改")
		return
	}
	if req.Email != nil && c.Settings != nil &&
		!c.Settings.GetBool(r.Context(), "profile_email_editable") {
		respond.Fail(w, http.StatusForbidden, "邮箱已由管理员锁定，不可自助修改")
		return
	}
	fields := map[string]any{}
	if req.DisplayName != nil {
		fields["display_name"] = *req.DisplayName
	}
	if req.Email != nil {
		if msg := validateEmail(*req.Email); msg != "" {
			respond.Fail(w, http.StatusBadRequest, msg)
			return
		}
		fields["email"] = strings.TrimSpace(*req.Email)
	}
	if len(fields) == 0 {
		respond.Fail(w, http.StatusBadRequest, "没有可更新的字段")
		return
	}
	if err := c.Users.UpdateFields(r.Context(), u.ID, fields); err != nil {
		respond.Fail(w, http.StatusInternalServerError, "更新资料失败")
		return
	}
	respond.OK(w, nil)
}
