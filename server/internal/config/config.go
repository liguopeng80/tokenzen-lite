// Package config 加载并校验服务配置。全部配置来自环境变量（12-factor），
// 开发环境可由启动脚本从 .env 文件导出。
package config

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
)

// Env 标识运行环境。
type Env string

const (
	EnvDev  Env = "dev"
	EnvProd Env = "prod"
)

// Config 是服务的全量配置。
type Config struct {
	Env Env
	// BindAddr HTTP 服务绑定地址，默认仅监听回环；
	// 需要对外直接暴露时显式设为 0.0.0.0 或指定网卡地址。
	BindAddr    string
	Port        int
	DatabaseURL string
	// TrustedProxies 可信反向代理的来源地址列表（IP 或 CIDR）。
	// 仅当连接的远端地址命中该列表时才采信 X-Real-IP 头；
	// 列表为空表示不信任任何代理，一律使用连接远端地址作为客户端 IP。
	TrustedProxies []string
	// SessionSecret 用于 session cookie 签名，生产环境必填。
	SessionSecret string
	// SessionCookieSecure 决定会话 cookie 是否带 Secure 属性。带该属性的
	// cookie 只在安全上下文（HTTPS，或 localhost）中被浏览器保存，因此该值
	// 必须与站点实际对浏览器暴露的协议一致，而不是与运行环境一致：
	// 生产环境若以明文 HTTP 对内网提供服务，置 true 会让用户登录后立即掉登录态。
	// 默认取 Env == prod，可由 TZL_SESSION_COOKIE_SECURE 显式覆盖。
	SessionCookieSecure bool
	// LogLevel: debug/info/warn/error
	LogLevel string
	// ShutdownTimeoutSec 优雅停机等待秒数。
	ShutdownTimeoutSec int
	// RegisterEnabled 是否开放用户自助注册（M2 起迁入系统设置表，本项仅在
	// 设置表不可用时兜底）。默认关闭：企业内部部署的账号由管理员建立。
	RegisterEnabled bool
	// RootUsername/RootPassword 首次启动时创建的初始 root 账号；
	// 仅在系统尚无任何用户时生效。
	RootUsername string
	RootPassword string
	// EncryptKey 渠道上游密钥的加密密钥材料，生产环境必填。
	EncryptKey string
	// UpstreamTimeoutSec 上游请求总超时（含流式，流式按首字节+空闲另控）。
	UpstreamTimeoutSec int
	// MetricsToken 是 /metrics 的访问令牌（Authorization: Bearer）。留空表示
	// 不启用令牌访问，此时该端点只接受 root 会话——指标里含模型名、渠道 ID
	// 与各接口的调用量，不是可以匿名读取的信息。
	MetricsToken string
	// RecordDir 请求录制文件的根目录（TZL_RECORD_DIR）。
	// 留空表示禁用录制；设置后录制文件落盘到 <RecordDir>/recordings/YYYY/MM/DD/。
	RecordDir string
}

// Load 从环境变量读取配置并校验。校验失败返回聚合后的错误。
func Load() (*Config, error) {
	cfg := &Config{
		Env:                Env(getenv("TZL_ENV", string(EnvDev))),
		BindAddr:           getenv("TZL_BIND_ADDR", "127.0.0.1"),
		Port:               getenvInt("TZL_PORT", 19030),
		TrustedProxies:     splitCSV(os.Getenv("TZL_TRUSTED_PROXIES")),
		DatabaseURL:        os.Getenv("TZL_DATABASE_URL"),
		SessionSecret:      os.Getenv("TZL_SESSION_SECRET"),
		LogLevel:           getenv("TZL_LOG_LEVEL", "info"),
		ShutdownTimeoutSec: getenvInt("TZL_SHUTDOWN_TIMEOUT_SEC", 15),
		RegisterEnabled:    getenv("TZL_REGISTER_ENABLED", "false") == "true",
		RootUsername:       getenv("TZL_ROOT_USERNAME", "root"),
		RootPassword:       os.Getenv("TZL_ROOT_PASSWORD"),
		EncryptKey:         getenv("TZL_ENCRYPT_KEY", "tzl-dev-only-encrypt-key"),
		UpstreamTimeoutSec: getenvInt("TZL_UPSTREAM_TIMEOUT_SEC", 600),
		MetricsToken:       os.Getenv("TZL_METRICS_TOKEN"),
		RecordDir:          os.Getenv("TZL_RECORD_DIR"),
	}
	cfg.SessionCookieSecure = getenvBool("TZL_SESSION_COOKIE_SECURE", cfg.Env == EnvProd)
	if errs := cfg.validate(); len(errs) > 0 {
		return nil, fmt.Errorf("配置无效: %s", strings.Join(errs, "; "))
	}
	return cfg, nil
}

func (c *Config) validate() []string {
	var errs []string
	if c.Env != EnvDev && c.Env != EnvProd {
		errs = append(errs, fmt.Sprintf("TZL_ENV 必须是 dev 或 prod，当前为 %q", c.Env))
	}
	if net.ParseIP(c.BindAddr) == nil {
		errs = append(errs, fmt.Sprintf("TZL_BIND_ADDR 必须是合法 IP 地址，当前为 %q", c.BindAddr))
	}
	if c.Port < 1 || c.Port > 65535 {
		errs = append(errs, fmt.Sprintf("TZL_PORT 超出合法范围: %d", c.Port))
	}
	for _, p := range c.TrustedProxies {
		if _, _, err := net.ParseCIDR(p); err == nil {
			continue
		}
		if net.ParseIP(p) == nil {
			errs = append(errs, fmt.Sprintf("TZL_TRUSTED_PROXIES 含非法条目 %q（须为 IP 或 CIDR）", p))
		}
	}
	if c.DatabaseURL == "" {
		errs = append(errs, "TZL_DATABASE_URL 必填")
	}
	if c.Env == EnvProd && len(c.SessionSecret) < 32 {
		errs = append(errs, "生产环境 TZL_SESSION_SECRET 必填且不少于 32 字符")
	}
	if c.Env == EnvProd && (c.EncryptKey == "" || c.EncryptKey == "tzl-dev-only-encrypt-key") {
		errs = append(errs, "生产环境 TZL_ENCRYPT_KEY 必填且不能使用开发默认值")
	}
	switch c.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		errs = append(errs, fmt.Sprintf("TZL_LOG_LEVEL 不支持 %q", c.LogLevel))
	}
	return errs
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// splitCSV 按逗号拆分并去除空白，空串返回 nil。
func splitCSV(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	var out []string
	for _, part := range strings.Split(s, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// getenvBool 读取布尔型环境变量；未设置或取值无法识别时返回 fallback。
// 识别 true/false、1/0、yes/no、on/off，大小写不敏感。
func getenvBool(key string, fallback bool) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case "true", "1", "yes", "on":
		return true
	case "false", "0", "no", "off":
		return false
	default:
		return fallback
	}
}

func getenvInt(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}
