package store

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"gorm.io/datatypes"
	"gorm.io/gorm"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
)

// Setting 对应 settings 表。
type Setting struct {
	Key       string         `gorm:"primaryKey" json:"key"`
	Value     datatypes.JSON `json:"value"`
	UpdatedAt time.Time      `json:"updated_at"`
}

// SettingKind 设置项的取值类型。
type SettingKind string

const (
	SettingInt64  SettingKind = "int64"
	SettingBool   SettingKind = "bool"
	SettingString SettingKind = "string"
)

// SettingDef 定义一个受支持的设置项（键白名单）。
type SettingDef struct {
	Key      string      `json:"key"`
	Kind     SettingKind `json:"kind"`
	Default  any         `json:"default"`
	Describe string      `json:"describe"`
	// Validate 可选的取值校验；返回用户可读的错误信息。
	Validate func(v any) error `json:"-"`
	// Options 枚举型设置项的全部合法取值，随读取接口下发。
	// 管理端据此渲染下拉选择，而不是让管理员往自由文本框里手打枚举值再由后端拒绝。
	Options []string `json:"options,omitempty"`
}

// auditLogProtectedDays 审计记录在数据库层的不可删除保护期天数。
// 与迁移 0006 中触发器的判定窗口必须一致：保留期短于该值时，
// 保留期清理会被数据库拒绝，因此设置项在写入时就把这类取值挡掉。
const auditLogProtectedDays = 30

// SettingDefs 是全部合法设置项的注册表。新增键必须先登记 docs/glossary.md。
var SettingDefs = []SettingDef{
	{Key: "site_name", Kind: SettingString, Default: "Token Zen",
		Describe: "站点名称"},
	{Key: "exchange_rate_credits_per_cny", Kind: SettingInt64, Default: int64(1_000_000),
		Describe: "1 货币单位兑换的积分数（全局兑换率，与货币符号联用决定界面金额刻度）",
		Validate: func(v any) error {
			if v.(int64) <= 0 {
				return fmt.Errorf("兑换率必须为正数")
			}
			return nil
		}},
	{Key: "currency_symbol", Kind: SettingString, Default: "¥",
		Describe: "界面金额展示符号（默认 ¥）。仅影响展示，不进行汇率换算；" +
			"积分始终为内部计费单位，对外统一以此符号呈现",
		Validate: func(v any) error {
			s, ok := v.(string)
			if !ok || strings.TrimSpace(s) == "" {
				return fmt.Errorf("货币符号不能为空")
			}
			if len(s) > 8 {
				return fmt.Errorf("货币符号过长（≤8 字符）")
			}
			return nil
		}},
	{Key: "usd_cny_rate_milli", Kind: SettingInt64, Default: int64(7200),
		Describe: "美元兑人民币汇率（千分数，7200 = 7.200），用于渠道成本折算",
		Validate: func(v any) error {
			if v.(int64) <= 0 {
				return fmt.Errorf("汇率必须为正数")
			}
			return nil
		}},
	{Key: "register_enabled", Kind: SettingBool, Default: false,
		Describe: "是否开放用户自助注册。默认关闭：企业内部部署的账号由管理员建立，" +
			"开放注册会让能访问到站点的任何人自助建号"},
	{Key: "low_balance_threshold_credits", Kind: SettingInt64, Default: int64(100_000),
		Describe: "余额预警阈值（积分）：用户余额低于该值时，门户展示预警并向管理员告警（0 = 关闭预警）",
		Validate: func(v any) error {
			if v.(int64) < 0 {
				return fmt.Errorf("余额预警阈值不得为负数（0 = 关闭预警）")
			}
			return nil
		}},
	{Key: "user_balance_notice_enabled", Kind: SettingBool, Default: true,
		Describe: "余额低于预警阈值时，是否额外向用户本人邮箱发送提醒（需已配置告警邮件通道，" +
			"且该用户填了邮箱）。管理员侧的低余额告警不受此项影响"},
	{Key: "profile_display_name_editable", Kind: SettingBool, Default: true,
		Describe: "是否允许用户自助修改显示名称。关闭后 PUT /auth/profile 携带 display_name 返回 403；" +
			"管理员侧仍可改"},
	{Key: "profile_email_editable", Kind: SettingBool, Default: true,
		Describe: "是否允许用户自助修改邮箱。关闭后 PUT /auth/profile 携带 email 返回 403；" +
			"管理员侧仍可改"},
	{Key: "monthly_grant_credits", Kind: SettingInt64, Default: int64(0),
		Describe: "按月自动发放给每个启用普通用户的积分额度（0 = 关闭自动发放）。" +
			"每月首次维护轮次执行，按「月份 + 用户」幂等，补发不会重复记账",
		Validate: func(v any) error {
			if v.(int64) < 0 {
				return fmt.Errorf("按月发放额度不得为负数（0 = 关闭）")
			}
			return nil
		}},
	{Key: "monthly_grant_mode", Kind: SettingString, Default: string(domain.MonthlyGrantTopUp),
		Options: stringsOf(domain.MonthlyGrantModes),
		Describe: "按月发放的口径：topup = 补足到额度（余额已达额度的账号本月不再发放，未用完不累积）；" +
			"add = 增发固定额度（不看当前余额，未用完的部分累积）",
		Validate: func(v any) error {
			if !domain.MonthlyGrantMode(v.(string)).Valid() {
				return fmt.Errorf("按月发放口径须为 topup 或 add")
			}
			return nil
		}},
	{Key: "relay_max_retries", Kind: SettingInt64, Default: int64(3),
		Describe: "中继失败时最多尝试的渠道数",
		Validate: func(v any) error {
			n := v.(int64)
			if n < 1 || n > 10 {
				return fmt.Errorf("重试渠道数须在 1-10 之间")
			}
			return nil
		}},
	{Key: "precharge_min_tokens", Kind: SettingInt64, Default: int64(500),
		Describe: "预扣费的最小估算 token 数"},
	{Key: "precharge_default_max_tokens", Kind: SettingInt64, Default: int64(8192),
		Describe: "请求未携带 max_tokens 时预扣费采用的输出 token 上限"},
	{Key: "rate_limit_per_key_rpm", Kind: SettingInt64, Default: int64(120),
		Describe: "单个 API Key 每分钟请求上限（取值 0-100000，0 = 不限流）",
		Validate: func(v any) error {
			n := v.(int64)
			if n < 0 || n > 100000 {
				return fmt.Errorf("按密钥限流须在 0-100000 之间（0 = 不限流）")
			}
			return nil
		}},
	{Key: "rate_limit_per_user_rpm", Kind: SettingInt64, Default: int64(240),
		Describe: "单个用户每分钟请求上限（全部 API Key 合并计数，与按密钥上限同时生效取更严者；取值 0-100000，0 = 不限流）",
		Validate: func(v any) error {
			n := v.(int64)
			if n < 0 || n > 100000 {
				return fmt.Errorf("按用户限流须在 0-100000 之间（0 = 不限流）")
			}
			return nil
		}},
	{Key: "max_concurrent_requests_per_user", Kind: SettingInt64, Default: int64(10),
		Describe: "/v1 单个用户并发请求子配额（一级闸门，防单一用户占满全站槽位；取值 0-1000，0 = 不限制）",
		Validate: func(v any) error {
			n := v.(int64)
			if n < 0 || n > 1000 {
				return fmt.Errorf("单用户并发子配额须在 0-1000 之间（0 = 不限制）")
			}
			return nil
		}},
	{Key: "max_keys_per_user", Kind: SettingInt64, Default: int64(20),
		Describe: "单个用户可持有的 API Key 数量上限（创建时校验；取值 0-1000，0 = 不限制）",
		Validate: func(v any) error {
			n := v.(int64)
			if n < 0 || n > 1000 {
				return fmt.Errorf("单用户密钥数量上限须在 0-1000 之间（0 = 不限制）")
			}
			return nil
		}},
	{Key: "max_concurrent_requests", Kind: SettingInt64, Default: int64(40),
		Describe: "/v1 并发请求总上限（二级保护，约束全部在途请求；取值 0-1000，0 = 不限制；与内存预算的换算依据见 docs/deployment.md）",
		Validate: func(v any) error {
			n := v.(int64)
			if n < 0 || n > 1000 {
				return fmt.Errorf("并发请求总上限须在 0-1000 之间（0 = 不限制）")
			}
			return nil
		}},
	{Key: "max_concurrent_large_requests", Kind: SettingInt64, Default: int64(2),
		Describe: "/v1 大请求（请求体超过 1 MiB 或长度未知）并发上限，计入总并发（取值 0-1000，0 = 不限制）",
		Validate: func(v any) error {
			n := v.(int64)
			if n < 0 || n > 1000 {
				return fmt.Errorf("大请求并发上限须在 0-1000 之间（0 = 不限制）")
			}
			return nil
		}},
	{Key: "channel_disable_failure_threshold", Kind: SettingInt64, Default: int64(3),
		Describe: "渠道连续致命错误达到该次数时自动禁用（0 = 不自动禁用；计数为进程内存态）",
		Validate: func(v any) error {
			n := v.(int64)
			if n < 0 || n > 100 {
				return fmt.Errorf("连续失败阈值须在 0-100 之间")
			}
			return nil
		}},
	{Key: "channel_probe_interval_sec", Kind: SettingInt64, Default: int64(60),
		Describe: "自动禁用渠道的半开探测间隔秒数（0 = 关闭探测）",
		Validate: func(v any) error {
			n := v.(int64)
			if n < 0 || n > 3600 {
				return fmt.Errorf("探测间隔须在 0-3600 秒之间")
			}
			return nil
		}},
	{Key: "server_address", Kind: SettingString, Default: "",
		Describe: "对外的 API 基址（如 https://api.example.com），下发给用户端接入指引作为 Base URL；留空时用户端按浏览器当前站点地址推断",
		Validate: func(v any) error {
			s := strings.TrimSpace(v.(string))
			if s == "" {
				return nil
			}
			u, err := url.Parse(s)
			if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
				return fmt.Errorf("API 基址须是完整的 http/https 地址，例如 https://api.example.com")
			}
			if strings.HasSuffix(s, "/") {
				return fmt.Errorf("API 基址末尾不要带斜杠")
			}
			return nil
		}},
	{Key: "orphan_cleanup_interval_sec", Kind: SettingInt64, Default: int64(300),
		Describe: "孤儿预扣回收的执行间隔秒数（0 = 关闭定时回收，仅在服务启动时执行一次）",
		Validate: func(v any) error {
			n := v.(int64)
			if n < 0 || n > 86400 {
				return fmt.Errorf("回收间隔须在 0-86400 秒之间")
			}
			return nil
		}},
	{Key: "audit_log_retention_days", Kind: SettingInt64, Default: int64(180),
		Describe: "操作审计记录保留天数（0 = 不清理，其余取值不少于 30 天）。" +
			"默认值覆盖等保二级对安全审计记录留存六个月的要求；下限 30 天与数据库层的" +
			"不可删除保护期一致，设得更短会让保留期清理被数据库拒绝",
		Validate: func(v any) error {
			n := v.(int64)
			if n != 0 && n < auditLogProtectedDays {
				return fmt.Errorf("审计保留天数为 0（不清理）或不少于 %d 天", auditLogProtectedDays)
			}
			if n > 3650 {
				return fmt.Errorf("审计保留天数不得超过 3650")
			}
			return nil
		}},
	{Key: "usage_log_retention_days", Kind: SettingInt64, Default: int64(90),
		Describe: "原始用量日志保留天数（0 = 不清理）。清理前校验该日期已完成按日汇总，报表不受影响，" +
			"仅明细查询与导出受限于保留期。默认 90 天：单机部署的磁盘容量有限，不设保留期时" +
			"原始日志会无上限增长",
		Validate: func(v any) error {
			n := v.(int64)
			if n != 0 && n < 30 {
				return fmt.Errorf("用量日志保留天数为 0（不清理）或不少于 30 天")
			}
			if n > 3650 {
				return fmt.Errorf("用量日志保留天数不得超过 3650")
			}
			return nil
		}},
	{Key: "usage_rollup_enabled", Kind: SettingBool, Default: true,
		Describe: "是否启用用量按日汇总任务（关闭后报表退回直接聚合原始日志）"},
	{Key: "alert_dedup_window_sec", Kind: SettingInt64, Default: int64(3600),
		Describe: "告警抑制窗口秒数：同一去重键在窗口内只投递一次（0 = 不抑制）",
		Validate: func(v any) error {
			n := v.(int64)
			if n < 0 || n > 86400 {
				return fmt.Errorf("告警抑制窗口须在 0-86400 秒之间")
			}
			return nil
		}},
	{Key: "alert_error_rate_percent", Kind: SettingInt64, Default: int64(20),
		Describe: "中继失败率告警阈值（百分数）：最近一小时的失败请求占比超过该值时告警（0 = 关闭）。" +
			"与渠道自动禁用互补——后者针对单个渠道的致命错误，本项针对全局的失败比例上升",
		Validate: func(v any) error {
			n := v.(int64)
			if n < 0 || n > 100 {
				return fmt.Errorf("失败率阈值须在 0-100 之间（0 = 关闭）")
			}
			return nil
		}},
	{Key: "alert_error_rate_min_requests", Kind: SettingInt64, Default: int64(50),
		Describe: "触发失败率告警所需的最小请求数：窗口内请求数不足该值时不判定，" +
			"避免夜间几次调用全失败就报出 100% 失败率",
		Validate: func(v any) error {
			if v.(int64) < 0 {
				return fmt.Errorf("最小请求数不得为负数")
			}
			return nil
		}},
	{Key: "alert_latency_p95_ms", Kind: SettingInt64, Default: int64(0),
		Describe: "中继耗时告警阈值：最近一小时总耗时的 95 分位超过该毫秒数时告警（0 = 关闭）。" +
			"默认关闭：合理的耗时随模型与请求长度差异巨大，阈值须按本站实际分布设定",
		Validate: func(v any) error {
			if v.(int64) < 0 {
				return fmt.Errorf("耗时阈值不得为负数（0 = 关闭）")
			}
			return nil
		}},
	{Key: "alert_webhook_url", Kind: SettingString, Default: "",
		Describe: "告警 Webhook 地址（http/https），留空表示不启用该通道",
		Validate: validateOptionalHTTPURL("告警 Webhook 地址")},
	{Key: "alert_webhook_format", Kind: SettingString, Default: "generic",
		Options:  stringsOf(domain.WebhookFormats),
		Describe: "Webhook 报文格式：generic / dingtalk / feishu / wecom / slack",
		Validate: func(v any) error {
			if !domain.WebhookFormat(v.(string)).Valid() {
				return fmt.Errorf("报文格式须为 generic、dingtalk、feishu、wecom 或 slack 之一")
			}
			return nil
		}},
	{Key: "alert_webhook_secret", Kind: SettingString, Default: "",
		Describe: "Webhook 加签密钥（钉钉、飞书），加密存储，读取接口返回掩码"},
	{Key: "alert_smtp_host", Kind: SettingString, Default: "",
		Describe: "告警邮件 SMTP 服务器地址，留空表示不启用该通道"},
	{Key: "alert_smtp_port", Kind: SettingInt64, Default: int64(587),
		Describe: "SMTP 端口（取值 1-65535）",
		Validate: func(v any) error {
			n := v.(int64)
			if n < 1 || n > 65535 {
				return fmt.Errorf("SMTP 端口须在 1-65535 之间")
			}
			return nil
		}},
	{Key: "alert_smtp_username", Kind: SettingString, Default: "",
		Describe: "SMTP 登录账号"},
	{Key: "alert_smtp_password", Kind: SettingString, Default: "",
		Describe: "SMTP 登录密码，加密存储，读取接口返回掩码"},
	{Key: "alert_smtp_tls", Kind: SettingString, Default: "starttls",
		Options:  stringsOf(domain.SMTPSecurities),
		Describe: "SMTP 加密方式：starttls / tls / none",
		Validate: func(v any) error {
			if !domain.SMTPSecurity(v.(string)).Valid() {
				return fmt.Errorf("加密方式须为 starttls、tls 或 none 之一")
			}
			return nil
		}},
	{Key: "alert_smtp_from", Kind: SettingString, Default: "",
		Describe: "告警邮件发件地址，留空时取 SMTP 登录账号"},
	{Key: "alert_email_to", Kind: SettingString, Default: "",
		Describe: "告警邮件收件地址，多个以逗号分隔"},
	// --- 请求录制（plans/cryptic-leaping-gadget.md）---
	// 默认全关：录制开销仅在需要积累回放样本时承担。
	{Key: "record_enabled", Kind: SettingBool, Default: false,
		Describe: "是否启用中继请求录制（落盘完整请求/响应 JSON，含 SSE 全流）。" +
			"默认关闭：录制有磁盘与性能开销，仅在需要积累回放样本时开启"},
	{Key: "record_sample_rate_permyriad", Kind: SettingInt64, Default: int64(0),
		Describe: "录制采样率（万分位）：0 = 不录，10000 = 全录。" +
			"建议从 100（1%）开始按需上调；全录只在低流量调试时使用",
		Validate: func(v any) error {
			n := v.(int64)
			if n < 0 || n > 10000 {
				return fmt.Errorf("录制采样率须在 0-10000 之间（0 = 不录，10000 = 全录）")
			}
			return nil
		}},
	{Key: "record_max_body_bytes", Kind: SettingInt64, Default: int64(2 << 20),
		Describe: "单条录制请求体的最大字节数（超出截断并置 truncated 标记）。" +
			"默认 2 MiB：覆盖常规文本请求，图像 base64 请求会被截断",
		Validate: func(v any) error {
			if v.(int64) < 0 {
				return fmt.Errorf("请求体上限不得为负数")
			}
			return nil
		}},
	{Key: "record_max_stream_bytes", Kind: SettingInt64, Default: int64(4 << 20),
		Describe: "流式录制累计的最大字节数（超出停止累计并置 truncated，不影响转发）。" +
			"默认 4 MiB：覆盖常规流式响应",
		Validate: func(v any) error {
			if v.(int64) < 0 {
				return fmt.Errorf("流式累计上限不得为负数")
			}
			return nil
		}},
	{Key: "record_redact_request_body", Kind: SettingBool, Default: false,
		Describe: "录制时是否脱敏请求体（置 true 时不保存 messages 内容，只留占位）。" +
			"默认关闭：录制目的是积累回放样本，需保留请求体内容"},
	{Key: "record_retention_days", Kind: SettingInt64, Default: int64(7),
		Describe: "录制文件保留天数（0 = 不自动清理）。每小时维护轮次按 mtime 清理过期文件",
		Validate: func(v any) error {
			if v.(int64) < 0 {
				return fmt.Errorf("录制保留天数不得为负数（0 = 不清理）")
			}
			return nil
		}},
}

// stringsOf 把字符串枚举切片转为普通字符串切片，供 Options 下发。
func stringsOf[T ~string](values []T) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		out = append(out, string(v))
	}
	return out
}

// validateOptionalHTTPURL 校验可留空的 http/https 地址。
func validateOptionalHTTPURL(label string) func(v any) error {
	return func(v any) error {
		s := strings.TrimSpace(v.(string))
		if s == "" {
			return nil
		}
		u, err := url.Parse(s)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fmt.Errorf("%s须是完整的 http/https 地址", label)
		}
		return nil
	}
}

func settingDef(key string) *SettingDef {
	for i := range SettingDefs {
		if SettingDefs[i].Key == key {
			return &SettingDefs[i]
		}
	}
	return nil
}

// SettingsRepo 提供带类型校验的设置读写。读路径带 30 秒进程内缓存。
type SettingsRepo struct {
	db    *gorm.DB
	cache *settingsCache
}

func NewSettingsRepo(db *gorm.DB) *SettingsRepo {
	return &SettingsRepo{db: db, cache: newSettingsCache(30 * time.Second)}
}

// get 读取原始值；未设置返回 nil。
func (r *SettingsRepo) get(ctx context.Context, key string) (any, error) {
	if v, ok := r.cache.get(key); ok {
		return v, nil
	}
	var s Setting
	err := r.db.WithContext(ctx).Where("key = ?", key).First(&s).Error
	if err == gorm.ErrRecordNotFound {
		r.cache.put(key, nil)
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	def := settingDef(key)
	if def == nil {
		return nil, nil
	}
	v, err := decodeSettingValue(def.Kind, s.Value)
	if err != nil {
		return nil, err
	}
	r.cache.put(key, v)
	return v, nil
}

func decodeSettingValue(kind SettingKind, raw datatypes.JSON) (any, error) {
	switch kind {
	case SettingInt64:
		var n int64
		err := json.Unmarshal(raw, &n)
		return n, err
	case SettingBool:
		var b bool
		err := json.Unmarshal(raw, &b)
		return b, err
	default:
		var s string
		err := json.Unmarshal(raw, &s)
		return s, err
	}
}

// GetInt64 读取整型设置，未设置时返回注册的默认值。
func (r *SettingsRepo) GetInt64(ctx context.Context, key string) int64 {
	def := settingDef(key)
	if def == nil || def.Kind != SettingInt64 {
		return 0
	}
	if v, err := r.get(ctx, key); err == nil && v != nil {
		return v.(int64)
	}
	return def.Default.(int64)
}

// GetBool 读取布尔设置，未设置时返回注册的默认值。
func (r *SettingsRepo) GetBool(ctx context.Context, key string) bool {
	def := settingDef(key)
	if def == nil || def.Kind != SettingBool {
		return false
	}
	if v, err := r.get(ctx, key); err == nil && v != nil {
		return v.(bool)
	}
	return def.Default.(bool)
}

// GetString 读取字符串设置，未设置时返回注册的默认值。
func (r *SettingsRepo) GetString(ctx context.Context, key string) string {
	def := settingDef(key)
	if def == nil || def.Kind != SettingString {
		return ""
	}
	if v, err := r.get(ctx, key); err == nil && v != nil {
		return v.(string)
	}
	return def.Default.(string)
}

// Set 校验并写入设置项。value 为 JSON 原始值。
func (r *SettingsRepo) Set(ctx context.Context, key string, rawValue json.RawMessage) error {
	def := settingDef(key)
	if def == nil {
		return fmt.Errorf("不支持的设置项: %s", key)
	}
	v, err := decodeSettingValue(def.Kind, datatypes.JSON(rawValue))
	if err != nil {
		return fmt.Errorf("设置项 %s 的取值类型应为 %s", key, def.Kind)
	}
	if def.Validate != nil {
		if err := def.Validate(v); err != nil {
			return err
		}
	}
	s := Setting{Key: key, Value: datatypes.JSON(rawValue), UpdatedAt: time.Now()}
	if err := r.db.WithContext(ctx).Save(&s).Error; err != nil {
		return fmt.Errorf("保存设置失败: %w", err)
	}
	r.cache.invalidate(key)
	return nil
}

// Effective 返回单个设置项的生效值（含默认值回退）。第二个返回值为 false 表示
// 该键未注册。供审计记录改动前的取值使用——设置项被误改后要能查出原值。
func (r *SettingsRepo) Effective(ctx context.Context, key string) (any, bool) {
	def := settingDef(key)
	if def == nil {
		return nil, false
	}
	if v, err := r.get(ctx, key); err == nil && v != nil {
		return v, true
	}
	return def.Default, true
}

// EffectiveAll 返回全部注册键的生效值（含默认值回退），供管理端展示。
func (r *SettingsRepo) EffectiveAll(ctx context.Context) []map[string]any {
	out := make([]map[string]any, 0, len(SettingDefs))
	for _, def := range SettingDefs {
		var value any = def.Default
		if v, err := r.get(ctx, def.Key); err == nil && v != nil {
			value = v
		}
		item := map[string]any{
			"key": def.Key, "kind": def.Kind, "value": value,
			"default": def.Default, "describe": def.Describe,
		}
		if len(def.Options) > 0 {
			item["options"] = def.Options
		}
		out = append(out, item)
	}
	return out
}
