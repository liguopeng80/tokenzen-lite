package alerting

import (
	"bufio"
	"context"
	"fmt"
	"mime"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// 报文的行结束一律是 CRLF，且不得出现裸 CR：头行用 CRLF 拼、正文按行转义，
// 两步各自处理不当会拼出裸 CR，那是不合法的报文，接收方拒收或显示为乱码。
func TestBuildMessageUsesCleanCRLF(t *testing.T) {
	cfg := SMTPConfig{Host: "smtp.example.com", From: "gw@example.com",
		To: []string{"ops@example.com"}}
	msg := buildMessage(cfg, "subject", "line1\nline2")

	if strings.Contains(msg, "\r\r") {
		t.Fatalf("报文中出现裸 CR：%q", msg)
	}
	for _, line := range strings.Split(msg, "\r\n") {
		if strings.ContainsAny(line, "\r\n") {
			t.Fatalf("行内不应残留换行字符：%q", line)
		}
	}
}

// 中文主题必须做 MIME 编码，否则部分客户端显示为乱码。
func TestBuildMessageEncodesChineseSubject(t *testing.T) {
	cfg := SMTPConfig{Host: "smtp.example.com", From: "gw@example.com",
		To: []string{"ops@example.com"}}
	msg := buildMessage(cfg, "渠道自动禁用", "正文")

	if strings.Contains(msg, "Subject: 渠道自动禁用") {
		t.Fatal("中文主题应做 MIME 编码而非原样写入")
	}
	subject := headerValue(t, msg, "Subject")
	decoded, err := new(mime.WordDecoder).DecodeHeader(subject)
	if err != nil {
		t.Fatalf("主题应可被标准解码器还原: %v", err)
	}
	if decoded != "渠道自动禁用" {
		t.Errorf("解码后应还原原主题，实际 %q", decoded)
	}
}

// 报文头齐备：缺 Date 或 MIME 声明会被部分服务器判为垃圾邮件。
func TestBuildMessageCarriesRequiredHeaders(t *testing.T) {
	cfg := SMTPConfig{Host: "smtp.example.com", Username: "gw@example.com",
		To: []string{"a@example.com", "b@example.com"}}
	msg := buildMessage(cfg, "subject", "body")

	// From 未单独配置时取登录账号。
	if got := headerValue(t, msg, "From"); got != "gw@example.com" {
		t.Errorf("发件人应回退到登录账号，实际 %q", got)
	}
	if got := headerValue(t, msg, "To"); got != "a@example.com, b@example.com" {
		t.Errorf("多个收件人应以逗号分隔，实际 %q", got)
	}
	if got := headerValue(t, msg, "Content-Type"); !strings.Contains(got, "charset=UTF-8") {
		t.Errorf("应声明 UTF-8 字符集，实际 %q", got)
	}
	if headerValue(t, msg, "Date") == "" {
		t.Error("应带 Date 头")
	}
	if headerValue(t, msg, "MIME-Version") != "1.0" {
		t.Error("应带 MIME-Version 头")
	}
	// 头与正文之间必须是一个空行。
	if !strings.Contains(msg, "\r\n\r\nbody") {
		t.Errorf("头与正文之间应有空行分隔：%q", msg)
	}
}

// 正文行首的单个点会被 SMTP 解释为报文结束标记，必须转义，
// 否则邮件在该行被截断，收件人只看到半封信。
func TestDotStuffEscapesLeadingDots(t *testing.T) {
	got := dotStuff("first\n.hidden\n..double\nnormal.line")
	lines := strings.Split(got, "\r\n")
	want := []string{"first", "..hidden", "...double", "normal.line"}
	if len(lines) != len(want) {
		t.Fatalf("行数应保持不变，实际 %d 行：%q", len(lines), got)
	}
	for i := range want {
		if lines[i] != want[i] {
			t.Errorf("第 %d 行应为 %q，实际 %q", i+1, want[i], lines[i])
		}
	}
	if !strings.Contains(got, "\r\n") {
		t.Error("SMTP 报文的行分隔应为 CRLF")
	}
}

// 正文里的行首点经完整报文构造后同样被转义。
func TestBuildMessageEscapesDotsInBody(t *testing.T) {
	cfg := SMTPConfig{Host: "smtp.example.com", From: "gw@example.com",
		To: []string{"ops@example.com"}}
	msg := buildMessage(cfg, "s", "line1\n.\nline3")
	if !strings.Contains(msg, "\r\n..\r\n") {
		t.Errorf("正文中的单点行应被转义为双点：%q", msg)
	}
}

// 未配置的通道直接拒绝，不去尝试连接。
func TestSendMailRejectsUnconfigured(t *testing.T) {
	if err := SendMail(SMTPConfig{}, "s", "b"); err == nil {
		t.Fatal("未配置邮件通道时应返回错误")
	}
	if err := SendMail(SMTPConfig{Host: "smtp.example.com"}, "s", "b"); err == nil {
		t.Fatal("没有收件人时应返回错误")
	}
}

// 完整走一遍 SMTP 会话：信封的发件人、收件人与报文内容都应正确送达。
func TestSendMailDeliversThroughSMTPSession(t *testing.T) {
	srv := newFakeSMTP(t)
	defer srv.close()

	cfg := SMTPConfig{
		Host: srv.host, Port: srv.port, From: "gw@example.com",
		To:       []string{"ops@example.com", "boss@example.com"},
		Security: domain.SMTPNone,
	}
	if err := SendMail(cfg, "余额不足", "你的积分余额已低于预警线。"); err != nil {
		t.Fatalf("投递失败: %v", err)
	}

	session := srv.wait(t)
	if session.from != "gw@example.com" {
		t.Errorf("信封发件人应为 %q，实际 %q", "gw@example.com", session.from)
	}
	if len(session.rcpt) != 2 || session.rcpt[0] != "ops@example.com" {
		t.Errorf("信封收件人不符：%v", session.rcpt)
	}
	if !strings.Contains(session.data, "你的积分余额已低于预警线。") {
		t.Errorf("正文未送达：%q", session.data)
	}
	subject := headerValue(t, session.data, "Subject")
	decoded, _ := new(mime.WordDecoder).DecodeHeader(subject)
	if decoded != "余额不足" {
		t.Errorf("主题未正确送达，实际 %q", decoded)
	}
}

// 服务器拒收时把失败原因带回来，而不是静默当作成功。
func TestSendMailSurfacesServerRejection(t *testing.T) {
	srv := newFakeSMTP(t)
	srv.rejectRcpt = true
	defer srv.close()

	cfg := SMTPConfig{Host: srv.host, Port: srv.port, From: "gw@example.com",
		To: []string{"nobody@example.com"}, Security: domain.SMTPNone}
	err := SendMail(cfg, "s", "b")
	if err == nil {
		t.Fatal("服务器拒收时应返回错误")
	}
	if !strings.Contains(err.Error(), "nobody@example.com") {
		t.Errorf("错误信息应指出被拒的收件人，实际 %v", err)
	}
}

// 告警事件走邮件通道时，主题取事件标题、正文取事件正文。
func TestSendEmailUsesEventHeadingAndMessage(t *testing.T) {
	var gotSubject, gotBody string
	s := &Service{SendMail: func(_ SMTPConfig, subject, body string) error {
		gotSubject, gotBody = subject, body
		return nil
	}}
	ev := &store.AlertEvent{
		AlertType: domain.AlertUserLowBalance, Severity: domain.AlertWarning,
		Title: "用户余额不足", Message: "alice 余额 100 积分",
	}
	cfg := Config{SMTP: SMTPConfig{Host: "h", To: []string{"a@example.com"}}}
	if err := s.sendEmail(context.Background(), cfg, ev); err != nil {
		t.Fatalf("投递失败: %v", err)
	}
	if !strings.Contains(gotSubject, "用户余额不足") {
		t.Errorf("主题应含事件标题，实际 %q", gotSubject)
	}
	if gotBody != "alice 余额 100 积分" {
		t.Errorf("正文应取事件正文，实际 %q", gotBody)
	}
}

// 事件正文为空时以标题兜底，避免发出一封空信。
func TestSendEmailFallsBackToSubjectWhenBodyEmpty(t *testing.T) {
	var gotBody string
	s := &Service{SendMail: func(_ SMTPConfig, _, body string) error {
		gotBody = body
		return nil
	}}
	ev := &store.AlertEvent{AlertType: domain.AlertTest, Title: "通道测试"}
	if err := s.sendEmail(context.Background(), Config{}, ev); err != nil {
		t.Fatalf("投递失败: %v", err)
	}
	if gotBody == "" {
		t.Error("正文为空时应以标题兜底")
	}
}

// headerValue 从报文中取指定头的值。
func headerValue(t *testing.T, msg, name string) string {
	t.Helper()
	for _, line := range strings.Split(msg, "\r\n") {
		if line == "" {
			break // 头结束
		}
		if strings.HasPrefix(line, name+": ") {
			return strings.TrimPrefix(line, name+": ")
		}
	}
	return ""
}

// --- 假 SMTP 服务器 ---

type smtpSession struct {
	from string
	rcpt []string
	data string
}

type fakeSMTP struct {
	ln         net.Listener
	host       string
	port       int64
	rejectRcpt bool
	done       chan smtpSession
	once       sync.Once
}

// newFakeSMTP 起一个只说明文 SMTP 的假服务器，接受一次会话后把收到的内容送出。
func newFakeSMTP(t *testing.T) *fakeSMTP {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("监听失败: %v", err)
	}
	host, portStr, _ := net.SplitHostPort(ln.Addr().String())
	var port int64
	fmt.Sscanf(portStr, "%d", &port)
	s := &fakeSMTP{ln: ln, host: host, port: port, done: make(chan smtpSession, 1)}
	go s.serve()
	return s
}

func (s *fakeSMTP) serve() {
	conn, err := s.ln.Accept()
	if err != nil {
		return
	}
	defer conn.Close()
	var session smtpSession
	r := bufio.NewReader(conn)
	w := func(line string) { fmt.Fprintf(conn, "%s\r\n", line) }

	w("220 fake ESMTP")
	inData := false
	var body strings.Builder
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			break
		}
		line = strings.TrimRight(line, "\r\n")
		if inData {
			if line == "." {
				inData = false
				session.data = body.String()
				w("250 OK")
				continue
			}
			body.WriteString(line + "\r\n")
			continue
		}
		switch {
		case strings.HasPrefix(line, "EHLO"), strings.HasPrefix(line, "HELO"):
			w("250-fake")
			w("250 AUTH PLAIN")
		case strings.HasPrefix(line, "MAIL FROM:"):
			session.from = addrOf(line)
			w("250 OK")
		case strings.HasPrefix(line, "RCPT TO:"):
			if s.rejectRcpt {
				w("550 no such user")
				continue
			}
			session.rcpt = append(session.rcpt, addrOf(line))
			w("250 OK")
		case line == "DATA":
			inData = true
			w("354 send it")
		case line == "QUIT":
			w("221 bye")
			s.finish(session)
			return
		default:
			w("250 OK")
		}
	}
	s.finish(session)
}

func (s *fakeSMTP) finish(session smtpSession) {
	s.once.Do(func() { s.done <- session })
}

func (s *fakeSMTP) wait(t *testing.T) smtpSession {
	t.Helper()
	select {
	case session := <-s.done:
		return session
	case <-time.After(5 * time.Second):
		t.Fatal("等待 SMTP 会话超时")
		return smtpSession{}
	}
}

func (s *fakeSMTP) close() { _ = s.ln.Close() }

// addrOf 从 "MAIL FROM:<a@b>" 这类命令里取出地址。
func addrOf(line string) string {
	start := strings.Index(line, "<")
	end := strings.Index(line, ">")
	if start < 0 || end < start {
		return ""
	}
	return line[start+1 : end]
}
