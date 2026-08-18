package domain

// SetupCheck 是首次配置引导的检查项标识。新装系统在管理员配置完成前，
// 任何调用都必然被拒绝，引导按这些标识逐项指出未完成的配置。
type SetupCheck string

const (
	// SetupCheckChannel 至少有一条启用的上游渠道。
	SetupCheckChannel SetupCheck = "channel"
	// SetupCheckModel 至少上架一个模型。
	SetupCheckModel SetupCheck = "model"
	// SetupCheckModelServable 上架的模型中至少有一个被启用渠道承载。
	SetupCheckModelServable SetupCheck = "model_servable"
	// SetupCheckMember 至少建立一个普通用户账号。
	SetupCheckMember SetupCheck = "member"
	// SetupCheckCredits 至少有一个普通用户持有正余额。
	SetupCheckCredits SetupCheck = "credits"
	// SetupCheckServerAddress 已填写对外 API 基址。
	SetupCheckServerAddress SetupCheck = "server_address"
	// SetupCheckAlertChannel 已配置至少一个告警通道。非必需项：
	// 缺它系统照常运行，只是告警事件只落库不投递。
	SetupCheckAlertChannel SetupCheck = "alert_channel"
)
