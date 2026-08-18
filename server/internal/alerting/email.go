package alerting

import (
	"context"
	"crypto/tls"
	"fmt"
	"mime"
	"net"
	"net/smtp"
	"strconv"
	"strings"
	"time"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/obs"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// smtpDialTimeout SMTP 建连与握手的超时。
const smtpDialTimeout = 10 * time.Second

// sendEmail 投递告警邮件。
// 外部依赖调用按 obs 规范在退出前打一条 INFO 日志，含目标、耗时与状态——
// 邮件投递失败只会表现为「没收到」，必须凭日志定位是 SMTP 拒收还是网络不可达。
func (s *Service) sendEmail(ctx context.Context, cfg Config, ev *store.AlertEvent) (err error) {
	start := time.Now()
	target := smtpTarget(cfg.SMTP)
	defer func() {
		status := "ok"
		if err != nil {
			status = "error"
		}
		obs.Logger(ctx).Info("alert_email",
			"target", target,
			"duration_ms", time.Since(start).Milliseconds(),
			"status", status,
			"error", errOrNil(err),
		)
	}()

	subject := Heading(ev)
	body := ev.Message
	if body == "" {
		body = subject
	}
	send := s.SendMail
	if send == nil {
		send = SendMail
	}
	return send(cfg.SMTP, subject, body)
}

// smtpTarget 返回日志中标识 SMTP 目标的字符串。Host 为空（未配置）时返回占位符。
func smtpTarget(cfg SMTPConfig) string {
	if cfg.Host == "" {
		return "(unconfigured)"
	}
	return net.JoinHostPort(cfg.Host, strconv.FormatInt(cfg.Port, 10))
}

// SendMail 按配置发送一封纯文本邮件。
func SendMail(cfg SMTPConfig, subject, body string) error {
	if !cfg.Configured() {
		return fmt.Errorf("邮件通道未配置")
	}
	addr := net.JoinHostPort(cfg.Host, strconv.FormatInt(cfg.Port, 10))
	conn, err := dialSMTP(cfg, addr)
	if err != nil {
		return err
	}
	client, err := smtp.NewClient(conn, cfg.Host)
	if err != nil {
		conn.Close()
		return fmt.Errorf("建立 SMTP 会话失败: %w", err)
	}
	defer client.Close()

	if cfg.Security == domain.SMTPStartTLS {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return fmt.Errorf("服务器不支持 STARTTLS，请改选加密方式")
		}
		if err := client.StartTLS(&tls.Config{ServerName: cfg.Host, MinVersion: tls.VersionTLS12}); err != nil {
			return fmt.Errorf("STARTTLS 握手失败: %w", err)
		}
	}
	if cfg.Username != "" {
		auth := smtp.PlainAuth("", cfg.Username, cfg.Password, cfg.Host)
		if err := client.Auth(auth); err != nil {
			return fmt.Errorf("SMTP 认证失败: %w", err)
		}
	}
	if err := client.Mail(cfg.Sender()); err != nil {
		return fmt.Errorf("设置发件人失败: %w", err)
	}
	for _, to := range cfg.To {
		if err := client.Rcpt(to); err != nil {
			return fmt.Errorf("设置收件人 %s 失败: %w", to, err)
		}
	}
	wc, err := client.Data()
	if err != nil {
		return fmt.Errorf("开始写入邮件正文失败: %w", err)
	}
	if _, err := wc.Write([]byte(buildMessage(cfg, subject, body))); err != nil {
		wc.Close()
		return fmt.Errorf("写入邮件正文失败: %w", err)
	}
	if err := wc.Close(); err != nil {
		return fmt.Errorf("提交邮件失败: %w", err)
	}
	return client.Quit()
}

func dialSMTP(cfg SMTPConfig, addr string) (net.Conn, error) {
	dialer := &net.Dialer{Timeout: smtpDialTimeout}
	if cfg.Security == domain.SMTPTLS {
		conn, err := tls.DialWithDialer(dialer, "tcp", addr,
			&tls.Config{ServerName: cfg.Host, MinVersion: tls.VersionTLS12})
		if err != nil {
			return nil, fmt.Errorf("连接 SMTP 服务器失败: %w", err)
		}
		return conn, nil
	}
	conn, err := dialer.Dial("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("连接 SMTP 服务器失败: %w", err)
	}
	return conn, nil
}

// buildMessage 组装 RFC 5322 报文。主题用 MIME 编码，避免中文标题在
// 部分客户端显示为乱码。
func buildMessage(cfg SMTPConfig, subject, body string) string {
	var b strings.Builder
	b.WriteString("From: " + cfg.Sender() + "\r\n")
	b.WriteString("To: " + strings.Join(cfg.To, ", ") + "\r\n")
	b.WriteString("Subject: " + mime.QEncoding.Encode("UTF-8", subject) + "\r\n")
	b.WriteString("Date: " + time.Now().Format(time.RFC1123Z) + "\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
	b.WriteString("Content-Transfer-Encoding: 8bit\r\n")
	b.WriteString("\r\n")
	// 正文行首的单个点会被 SMTP 解释为报文结束标记，须转义。
	b.WriteString(body)
	return dotStuff(b.String())
}

// dotStuff 把报文统一为 CRLF 行结束，并转义行首的点。
//
// 先归一化再切分：报文头本身是用 CRLF 拼的，若只按 \n 切分再用 \r\n 拼回去，
// 每个头行都会多出一个裸 CR（`...\r\r\n`）。那是不合法的报文，接收方要么拒收，
// 要么把整封信显示成乱码——而这条路径出错的表现只是「邮件没收到」，很难追查。
func dotStuff(msg string) string {
	lines := strings.Split(strings.ReplaceAll(msg, "\r\n", "\n"), "\n")
	for i, line := range lines {
		if strings.HasPrefix(line, ".") {
			lines[i] = "." + line
		}
	}
	return strings.Join(lines, "\r\n")
}
