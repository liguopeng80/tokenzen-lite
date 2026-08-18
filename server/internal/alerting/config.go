package alerting

import (
	"context"
	"strings"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/obs"
)

// SMTPConfig 是告警邮件通道的配置。
type SMTPConfig struct {
	Host     string
	Port     int64
	Username string
	Password string
	From     string
	To       []string
	Security domain.SMTPSecurity
}

// Configured 判断邮件通道是否具备投递条件。
func (c SMTPConfig) Configured() bool {
	return c.Host != "" && len(c.To) > 0
}

// Sender 返回发件地址：未单独配置时取登录账号。
func (c SMTPConfig) Sender() string {
	if c.From != "" {
		return c.From
	}
	return c.Username
}

// Config 是告警投递所需的全部配置。
type Config struct {
	WebhookURL    string
	WebhookFormat domain.WebhookFormat
	WebhookSecret string
	SMTP          SMTPConfig
}

// SecretSettingKeys 是以密文存储、读取接口须返回掩码的设置键。
var SecretSettingKeys = map[string]bool{
	"alert_webhook_secret": true,
	"alert_smtp_password":  true,
}

// LoadConfig 读取告警配置并解密密文字段。
// 解密失败时该字段按空值处理并记 ERROR 日志：加密密钥变更后
// 应当明确暴露为「签名缺失/认证失败」，而不是拿密文当明文发出去。
func (s *Service) LoadConfig(ctx context.Context) Config {
	if s.Settings == nil {
		return Config{}
	}
	format := domain.WebhookFormat(s.Settings.GetString(ctx, "alert_webhook_format"))
	if !format.Valid() {
		format = domain.WebhookGeneric
	}
	security := domain.SMTPSecurity(s.Settings.GetString(ctx, "alert_smtp_tls"))
	if !security.Valid() {
		security = domain.SMTPStartTLS
	}
	return Config{
		WebhookURL:    strings.TrimSpace(s.Settings.GetString(ctx, "alert_webhook_url")),
		WebhookFormat: format,
		WebhookSecret: s.decrypt(ctx, "alert_webhook_secret"),
		SMTP: SMTPConfig{
			Host:     strings.TrimSpace(s.Settings.GetString(ctx, "alert_smtp_host")),
			Port:     s.Settings.GetInt64(ctx, "alert_smtp_port"),
			Username: s.Settings.GetString(ctx, "alert_smtp_username"),
			Password: s.decrypt(ctx, "alert_smtp_password"),
			From:     strings.TrimSpace(s.Settings.GetString(ctx, "alert_smtp_from")),
			To:       splitRecipients(s.Settings.GetString(ctx, "alert_email_to")),
			Security: security,
		},
	}
}

func (s *Service) decrypt(ctx context.Context, key string) string {
	stored := s.Settings.GetString(ctx, key)
	if stored == "" || s.Secrets == nil {
		return ""
	}
	plain, err := s.Secrets.Decrypt(stored)
	if err != nil {
		obs.Logger(ctx).Error("解密告警通道密钥失败", "key", key, "error", err)
		return ""
	}
	return plain
}

// splitRecipients 解析逗号分隔的收件地址。
func splitRecipients(raw string) []string {
	var out []string
	for _, part := range strings.Split(raw, ",") {
		if addr := strings.TrimSpace(part); addr != "" {
			out = append(out, addr)
		}
	}
	return out
}
