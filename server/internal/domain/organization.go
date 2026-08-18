package domain

// DepartmentStatus 部门状态。
type DepartmentStatus string

const (
	// DepartmentEnabled 正常，可作为归属选项分配成员。
	DepartmentEnabled DepartmentStatus = "enabled"
	// DepartmentDisabled 已停用，不能再分配新成员；现有成员归属与调用能力不变。
	DepartmentDisabled DepartmentStatus = "disabled"
)

// Valid 判断部门状态取值是否合法。
func (s DepartmentStatus) Valid() bool {
	return s == DepartmentEnabled || s == DepartmentDisabled
}

// ProjectStatus 项目状态。项目与部门同构：扁平单层、不持余额、不参与扣费，
// 仅作与部门正交的第二层成本归属维度。
type ProjectStatus string

const (
	// ProjectEnabled 正常，可作为归属选项分配密钥。
	ProjectEnabled ProjectStatus = "enabled"
	// ProjectDisabled 已停用，不能再分配新密钥；现有密钥归属与调用能力不变。
	ProjectDisabled ProjectStatus = "disabled"
)

// Valid 判断项目状态取值是否合法。
func (s ProjectStatus) Valid() bool {
	return s == ProjectEnabled || s == ProjectDisabled
}

// IntegrationStatus 接入方状态。
type IntegrationStatus string

const (
	// IntegrationEnabled 正常，其服务令牌可用、其用户可调用。
	IntegrationEnabled IntegrationStatus = "enabled"
	// IntegrationDisabled 已停用：全部服务令牌停用、全部用户禁用，账务历史保留。
	IntegrationDisabled IntegrationStatus = "disabled"
)

// Valid 判断接入方状态取值是否合法。
func (s IntegrationStatus) Valid() bool {
	return s == IntegrationEnabled || s == IntegrationDisabled
}

// ServiceTokenStatus 服务令牌状态。与 KeyStatus 对齐，仅取启用/停用——
// 过期与耗尽由其承载的服务账号生命周期决定，不在令牌本身表达。
type ServiceTokenStatus string

const (
	ServiceTokenEnabled  ServiceTokenStatus = "enabled"
	ServiceTokenDisabled ServiceTokenStatus = "disabled"
)

// AuditAction 审计动作，命名为「对象.动作」，与 AuditTargetType 配套。
type AuditAction string

const (
	AuditAuthLogin          AuditAction = "auth.login"
	AuditAuthLogout         AuditAction = "auth.logout"
	AuditAuthPasswordChange AuditAction = "auth.password_change"
	AuditAuthRegister       AuditAction = "auth.register"

	AuditUserCreate        AuditAction = "user.create"
	AuditUserUpdate        AuditAction = "user.update"
	AuditUserDelete        AuditAction = "user.delete"
	AuditUserStatusChange  AuditAction = "user.status_change"
	AuditUserPasswordReset AuditAction = "user.password_reset"
	AuditUserCreditGrant   AuditAction = "user.credit_grant"
	AuditUserImport        AuditAction = "user.import"
	AuditUserCreditBatch   AuditAction = "user.credit_batch_grant"
	AuditUserPolicyChange  AuditAction = "user.policy_change"
	// AuditUserStatusBatch 批量启用或禁用用户（离职、调岗等批量处置）。
	AuditUserStatusBatch   AuditAction = "user.status_batch_change"
	AuditDepartmentCreate  AuditAction = "department.create"
	AuditDepartmentUpdate  AuditAction = "department.update"
	AuditDepartmentDelete  AuditAction = "department.delete"
	AuditDepartmentMembers AuditAction = "department.member_change"
	// AuditProject* 是项目（与部门正交的成本归属维度）的运营动作。
	AuditProjectCreate     AuditAction = "project.create"
	AuditProjectUpdate     AuditAction = "project.update"
	AuditProjectDelete     AuditAction = "project.delete"
	AuditModelCreate       AuditAction = "model.create"
	AuditModelUpdate       AuditAction = "model.update"
	AuditModelDelete       AuditAction = "model.delete"
	AuditModelPriceChange  AuditAction = "model.price_change"
	AuditModelPeakRules    AuditAction = "model.peak_rules_change"
	AuditModelImport       AuditAction = "model.import"
	AuditChannelCreate     AuditAction = "channel.create"
	AuditChannelUpdate     AuditAction = "channel.update"
	AuditChannelDelete     AuditAction = "channel.delete"
	AuditChannelStatus     AuditAction = "channel.status_change"
	AuditChannelCostChange AuditAction = "channel.cost_change"
	AuditChannelTest       AuditAction = "channel.test"
	AuditRedemptionBatch   AuditAction = "redemption.batch_create"
	AuditRedemptionStatus  AuditAction = "redemption.status_change"
	// AuditAPIKey* 是员工自助维护 API Key 的动作。密钥泄漏或出现异常调用时，
	// 事后追查要能查出这个密钥何时由谁创建、改过什么、何时删除。
	AuditAPIKeyCreate  AuditAction = "api_key.create"
	AuditAPIKeyUpdate  AuditAction = "api_key.update"
	AuditAPIKeyDelete  AuditAction = "api_key.delete"
	AuditSettingUpdate AuditAction = "setting.update"
	AuditAlertTest     AuditAction = "alert.test"
	AuditPurge         AuditAction = "audit.purge"
	// AuditIntegration* / AuditServiceToken* 是 root 运营接入方与其服务令牌的动作。
	AuditIntegrationCreate  AuditAction = "integration.create"
	AuditIntegrationUpdate  AuditAction = "integration.update"
	AuditIntegrationDisable AuditAction = "integration.disable"
	AuditServiceTokenCreate AuditAction = "service_token.create"
	AuditServiceTokenStatus AuditAction = "service_token.status_change"
	AuditServiceTokenDelete AuditAction = "service_token.delete"
)

// AuditActions 是全部合法审计动作，新增管理侧写操作时须同步登记 docs/glossary.md。
var AuditActions = []AuditAction{
	AuditAuthLogin, AuditAuthLogout, AuditAuthPasswordChange, AuditAuthRegister,
	AuditUserCreate, AuditUserUpdate, AuditUserDelete, AuditUserStatusChange,
	AuditUserPasswordReset, AuditUserCreditGrant, AuditUserImport, AuditUserCreditBatch,
	AuditUserPolicyChange, AuditUserStatusBatch,
	AuditDepartmentCreate, AuditDepartmentUpdate, AuditDepartmentDelete, AuditDepartmentMembers,
	AuditProjectCreate, AuditProjectUpdate, AuditProjectDelete,
	AuditModelCreate, AuditModelUpdate, AuditModelDelete, AuditModelPriceChange,
	AuditModelPeakRules, AuditModelImport,
	AuditChannelCreate, AuditChannelUpdate, AuditChannelDelete, AuditChannelStatus,
	AuditChannelCostChange, AuditChannelTest,
	AuditRedemptionBatch, AuditRedemptionStatus,
	AuditAPIKeyCreate, AuditAPIKeyUpdate, AuditAPIKeyDelete,
	AuditSettingUpdate, AuditAlertTest, AuditPurge,
	AuditIntegrationCreate, AuditIntegrationUpdate, AuditIntegrationDisable,
	AuditServiceTokenCreate, AuditServiceTokenStatus, AuditServiceTokenDelete,
}

// Valid 判断审计动作是否已登记。
func (a AuditAction) Valid() bool {
	for _, known := range AuditActions {
		if known == a {
			return true
		}
	}
	return false
}

// AuditTargetType 审计对象类型。
type AuditTargetType string

const (
	AuditTargetUser         AuditTargetType = "user"
	AuditTargetDepartment   AuditTargetType = "department"
	AuditTargetProject      AuditTargetType = "project"
	AuditTargetModel        AuditTargetType = "model"
	AuditTargetChannel      AuditTargetType = "channel"
	AuditTargetRedemption   AuditTargetType = "redemption"
	AuditTargetSetting      AuditTargetType = "setting"
	AuditTargetAPIKey       AuditTargetType = "api_key"
	AuditTargetSession      AuditTargetType = "session"
	AuditTargetAudit        AuditTargetType = "audit"
	AuditTargetIntegration  AuditTargetType = "integration"
	AuditTargetServiceToken AuditTargetType = "service_token"
)

// AuditResult 审计结果。
type AuditResult string

const (
	// AuditSuccess 操作已生效。
	AuditSuccess AuditResult = "success"
	// AuditFailure 操作被拒绝或执行失败，原因记入 message。
	AuditFailure AuditResult = "failure"
)

// AuditRedacted 是敏感字段在审计快照中的占位值：只表达该字段被修改过，
// 不记录任何值（含旧值）。
const AuditRedacted = "***"

// AlertType 告警事件类型。
type AlertType string

const (
	AlertChannelAutoDisabled  AlertType = "channel_auto_disabled"
	AlertReconcileFailed      AlertType = "reconcile_failed"
	AlertUsageLogDropped      AlertType = "usage_log_dropped"
	AlertOrphanPrechargeFound AlertType = "orphan_precharge_found"
	AlertDepartmentOverBudget AlertType = "department_over_budget"
	AlertBackupFailed         AlertType = "backup_failed"
	// AlertUserLowBalance 有用户余额低于预警阈值，其调用即将开始被拒绝。
	AlertUserLowBalance AlertType = "user_low_balance"
	// AlertUserBalanceNotice 给用户本人的余额不足提醒，投递到其本人邮箱，
	// 不进群通道。与 AlertUserLowBalance 分开是因为收件人与措辞都不同：
	// 前者要求管理员发放积分，后者告知本人调用即将失败。
	AlertUserBalanceNotice AlertType = "user_balance_notice"
	// AlertMonthlyGrantFailed 按月自动发放积分时有账号发放失败。
	AlertMonthlyGrantFailed AlertType = "monthly_grant_failed"
	// AlertErrorRateHigh 最近一个窗口内中继失败率超过阈值。
	AlertErrorRateHigh AlertType = "error_rate_high"
	// AlertLatencyDegraded 最近一个窗口内中继耗时的 95 分位超过阈值。
	AlertLatencyDegraded AlertType = "latency_degraded"
	// AlertPolicyMalformed 模型策略或来源 IP 白名单无法解析，相关调用已被拒绝。
	AlertPolicyMalformed AlertType = "policy_malformed"
	// AlertTest 是通道连通性测试，不属于业务事件，不参与去重抑制。
	AlertTest AlertType = "alert_test"
)

// AlertTypes 是全部合法的告警事件类型，新增类型时同步登记 docs/glossary.md
// 与 alert_events.alert_type 的 CHECK 约束（见迁移 0020）。
var AlertTypes = []AlertType{
	AlertChannelAutoDisabled, AlertReconcileFailed, AlertUsageLogDropped,
	AlertOrphanPrechargeFound, AlertDepartmentOverBudget, AlertBackupFailed,
	AlertUserLowBalance, AlertUserBalanceNotice, AlertMonthlyGrantFailed,
	AlertErrorRateHigh, AlertLatencyDegraded, AlertPolicyMalformed, AlertTest,
}

// Valid 判断告警事件类型是否已登记。
func (a AlertType) Valid() bool {
	for _, known := range AlertTypes {
		if known == a {
			return true
		}
	}
	return false
}

// AlertSeverity 告警严重度。
type AlertSeverity string

const (
	// AlertCritical 需立即处理：影响计费正确性或服务可用性。
	AlertCritical AlertSeverity = "critical"
	// AlertWarning 需知晓并择机处理。
	AlertWarning AlertSeverity = "warning"
)

// AlertStatus 告警投递状态。
type AlertStatus string

const (
	AlertPending    AlertStatus = "pending"
	AlertSent       AlertStatus = "sent"
	AlertFailed     AlertStatus = "failed"
	AlertSuppressed AlertStatus = "suppressed"
	// AlertDeadLetter 后台重试耗尽仍未送达。需要管理员介入，
	// 与一次普通失败（failed）区分以便在告警记录页单独筛选。
	// 抑制查询只认 sent，故死信不会让后续同类告警继续静默。
	AlertDeadLetter AlertStatus = "dead_letter"
)

// WebhookFormat 告警 Webhook 的报文格式。
type WebhookFormat string

const (
	WebhookGeneric  WebhookFormat = "generic"
	WebhookDingTalk WebhookFormat = "dingtalk"
	WebhookFeishu   WebhookFormat = "feishu"
	WebhookWeCom    WebhookFormat = "wecom"
	WebhookSlack    WebhookFormat = "slack"
)

// WebhookFormats 是全部支持的 Webhook 报文格式。
var WebhookFormats = []WebhookFormat{
	WebhookGeneric, WebhookDingTalk, WebhookFeishu, WebhookWeCom, WebhookSlack,
}

// Valid 判断 Webhook 报文格式是否受支持。
func (f WebhookFormat) Valid() bool {
	for _, known := range WebhookFormats {
		if known == f {
			return true
		}
	}
	return false
}

// SMTPSecurity SMTP 连接的加密方式。
type SMTPSecurity string

const (
	SMTPStartTLS SMTPSecurity = "starttls"
	SMTPTLS      SMTPSecurity = "tls"
	SMTPNone     SMTPSecurity = "none"
)

// Valid 判断 SMTP 加密方式取值是否合法。
func (s SMTPSecurity) Valid() bool {
	return s == SMTPStartTLS || s == SMTPTLS || s == SMTPNone
}

// SMTPSecurities 是全部支持的 SMTP 加密方式。
var SMTPSecurities = []SMTPSecurity{SMTPStartTLS, SMTPTLS, SMTPNone}
