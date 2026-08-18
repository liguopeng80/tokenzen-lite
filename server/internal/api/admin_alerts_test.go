package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/liguopeng80/tokenzen-lite/server/internal/alerting"
	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// webhookSink 是记录收到的 Webhook 报文的假接收端。
type webhookSink struct {
	*httptest.Server
	mu       sync.Mutex
	payloads []map[string]any
}

func newWebhookSink(t *testing.T, status int) *webhookSink {
	t.Helper()
	sink := &webhookSink{}
	sink.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		sink.mu.Lock()
		sink.payloads = append(sink.payloads, body)
		sink.mu.Unlock()
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(sink.Close)
	return sink
}

func (s *webhookSink) received() []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]map[string]any(nil), s.payloads...)
}

// setSetting 直接写系统设置（绕过接口的密文处理）。
func setSetting(t *testing.T, e *testEnv, key string, value any) {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("编码设置值失败: %v", err)
	}
	if err := e.deps.Settings.Set(t.Context(), key, raw); err != nil {
		t.Fatalf("写入设置 %s 失败: %v", key, err)
	}
}

// 告警投递成功后事件状态为已发送，报文按所选格式构造。
func TestAlertDeliveredToWebhook(t *testing.T) {
	e := newTestEnv(t)
	sink := newWebhookSink(t, http.StatusOK)
	setSetting(t, e, "alert_webhook_url", sink.URL)
	setSetting(t, e, "alert_webhook_format", string(domain.WebhookGeneric))

	event, err := e.deps.Alerts.RaiseSync(t.Context(), alerting.Event{
		Type: domain.AlertReconcileFailed, Severity: domain.AlertCritical,
		DedupKey: "reconcile_failed", Title: "积分对账未通过", Message: "3 个用户余额与流水不一致",
	})
	if err != nil {
		t.Fatalf("投递应成功: %v", err)
	}
	if event.Status != domain.AlertSent {
		t.Errorf("状态应为已发送，实际 %q", event.Status)
	}
	got := sink.received()
	if len(got) != 1 {
		t.Fatalf("接收端应收到 1 条，实际 %d 条", len(got))
	}
	if got[0]["alert_type"] != string(domain.AlertReconcileFailed) {
		t.Errorf("报文事件类型不符：%v", got[0])
	}
}

// 抑制窗口内的同类告警只投递一次：持续故障不应把通道刷成噪声。
func TestAlertDedupWithinWindow(t *testing.T) {
	e := newTestEnv(t)
	sink := newWebhookSink(t, http.StatusOK)
	setSetting(t, e, "alert_webhook_url", sink.URL)
	setSetting(t, e, "alert_dedup_window_sec", int64(3600))

	ev := alerting.Event{
		Type: domain.AlertChannelAutoDisabled, Severity: domain.AlertCritical,
		DedupKey: "channel_auto_disabled:1", Title: "渠道已自动禁用", Message: "连续致命错误",
	}
	for i := 0; i < 3; i++ {
		if _, err := e.deps.Alerts.RaiseSync(t.Context(), ev); err != nil {
			t.Fatalf("第 %d 次投递失败: %v", i+1, err)
		}
	}
	if got := sink.received(); len(got) != 1 {
		t.Errorf("抑制窗口内应只投递 1 次，实际 %d 次", len(got))
	}

	var suppressed int64
	if err := e.db.Model(&store.AlertEvent{}).
		Where("status = ?", domain.AlertSuppressed).Count(&suppressed).Error; err != nil {
		t.Fatalf("统计被抑制事件失败: %v", err)
	}
	if suppressed != 2 {
		t.Errorf("被抑制的事件应落库留痕，期望 2 条，实际 %d 条", suppressed)
	}
}

// 去重键不同的告警各自投递，不会被彼此抑制。
func TestAlertDifferentDedupKeysNotSuppressed(t *testing.T) {
	e := newTestEnv(t)
	sink := newWebhookSink(t, http.StatusOK)
	setSetting(t, e, "alert_webhook_url", sink.URL)
	setSetting(t, e, "alert_dedup_window_sec", int64(3600))

	for _, key := range []string{"channel_auto_disabled:1", "channel_auto_disabled:2"} {
		if _, err := e.deps.Alerts.RaiseSync(t.Context(), alerting.Event{
			Type: domain.AlertChannelAutoDisabled, DedupKey: key, Title: "渠道已自动禁用",
		}); err != nil {
			t.Fatalf("投递 %s 失败: %v", key, err)
		}
	}
	if got := sink.received(); len(got) != 2 {
		t.Errorf("不同去重键应各自投递，期望 2 次，实际 %d 次", len(got))
	}
}

// 投递失败的事件不参与抑制：一次通道故障不应永久吞掉该类告警。
func TestFailedDeliveryDoesNotSuppressNextAlert(t *testing.T) {
	e := newTestEnv(t)
	failing := newWebhookSink(t, http.StatusInternalServerError)
	setSetting(t, e, "alert_webhook_url", failing.URL)
	setSetting(t, e, "alert_dedup_window_sec", int64(3600))

	ev := alerting.Event{
		Type: domain.AlertUsageLogDropped, DedupKey: "usage_log_dropped", Title: "用量日志出现丢弃",
	}
	event, err := e.deps.Alerts.RaiseSync(t.Context(), ev)
	if err == nil {
		t.Fatal("接收端返回 500 时应回报投递失败")
	}
	if event.Status != domain.AlertFailed {
		t.Errorf("状态应为投递失败，实际 %q", event.Status)
	}

	// 再次触发：上一次没送达，本次必须继续尝试。
	if _, err := e.deps.Alerts.RaiseSync(t.Context(), ev); err == nil {
		t.Fatal("接收端仍失败时应继续回报失败")
	}
	if got := failing.received(); len(got) != 2 {
		t.Errorf("投递失败的事件不应抑制后续告警，期望尝试 2 次，实际 %d 次", len(got))
	}
}

// 未配置任何通道时事件仍落库：管理员能区分「未触发」与「触发了但发不出去」。
func TestAlertPersistedWithoutChannels(t *testing.T) {
	e := newTestEnv(t)
	event, err := e.deps.Alerts.RaiseSync(t.Context(), alerting.Event{
		Type: domain.AlertBackupFailed, Title: "备份失败",
	})
	if err != nil {
		t.Fatalf("未配置通道不应返回错误: %v", err)
	}
	if event.Status != domain.AlertFailed {
		t.Errorf("未配置通道时状态应为投递失败，实际 %q", event.Status)
	}
	var stored store.AlertEvent
	if err := e.db.First(&stored, event.ID).Error; err != nil {
		t.Fatalf("事件应已落库: %v", err)
	}
	if stored.LastError == "" {
		t.Error("应记录未配置通道的原因")
	}
}

// 通道测试端点在未配置任何通道时给出明确指引，而不是静默成功。
func TestAlertTestEndpointRequiresConfiguredChannel(t *testing.T) {
	e := newTestEnv(t)
	adminC := e.seedAndLogin(t, "alertadmin", domain.RoleAdmin)

	resp, env := e.do(t, adminC, "POST", "/api/admin/alerts/test", nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("未配置通道时应 400，实际 %d：%v", resp.StatusCode, env)
	}

	sink := newWebhookSink(t, http.StatusOK)
	setSetting(t, e, "alert_webhook_url", sink.URL)
	resp, env = e.do(t, adminC, "POST", "/api/admin/alerts/test", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("配置通道后应 200，实际 %d：%v", resp.StatusCode, env)
	}
	if len(sink.received()) != 1 {
		t.Errorf("测试消息应实际发出，实际收到 %d 条", len(sink.received()))
	}
}

// 密文设置项经加密存储，读取接口只返回掩码。
func TestSecretSettingsMasked(t *testing.T) {
	e := newTestEnv(t)
	rootC := e.seedAndLogin(t, "secretroot", domain.RoleRoot)
	const plaintext = "smtp-plaintext-password"

	resp, env := e.do(t, rootC, "PUT", "/api/admin/settings",
		map[string]any{"key": "alert_smtp_password", "value": plaintext})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("保存密文设置应 200，实际 %d：%v", resp.StatusCode, env)
	}

	// 库中不得留有明文。
	var stored store.Setting
	if err := e.db.Where("key = ?", "alert_smtp_password").First(&stored).Error; err != nil {
		t.Fatalf("查询设置失败: %v", err)
	}
	if string(stored.Value) == `"`+plaintext+`"` {
		t.Fatal("密文设置项不得以明文存储")
	}

	// 读取接口返回掩码。
	resp, env = e.do(t, rootC, "GET", "/api/admin/settings", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("读取设置应 200，实际 %d：%v", resp.StatusCode, env)
	}
	items := env["data"].([]any)
	var found bool
	for _, raw := range items {
		item := raw.(map[string]any)
		if item["key"] != "alert_smtp_password" {
			continue
		}
		found = true
		if item["value"] == plaintext {
			t.Error("读取接口不得回显密文设置项的明文")
		}
		if item["secret"] != true {
			t.Error("密文设置项应标记为 secret，供前端按密码框渲染")
		}
	}
	if !found {
		t.Fatal("设置列表应包含 alert_smtp_password")
	}

	// 服务端解密后可还原明文，保证投递时用的是真实密码。
	cfg := e.deps.Alerts.LoadConfig(t.Context())
	if cfg.SMTP.Password != plaintext {
		t.Errorf("服务端应能解密还原明文，实际 %q", cfg.SMTP.Password)
	}
}

// 定向通知（EmailTo 非空）只发给指定收件人，且不进 Webhook 群通道。
// 业务上这是给员工本人的余额提醒：把某个员工的余额发进管理员群里既无用也多余，
// 而管理员配置的收件地址收到本该发给员工的邮件同样是错的。
func TestDirectedNoticeGoesOnlyToItsRecipient(t *testing.T) {
	e := newTestEnv(t)
	sink := newWebhookSink(t, http.StatusOK)
	setSetting(t, e, "alert_webhook_url", sink.URL)
	setSetting(t, e, "alert_smtp_host", "smtp.example.com")
	setSetting(t, e, "alert_email_to", "ops@example.com")

	var gotTo []string
	var gotSubject string
	e.deps.Alerts.SendMail = func(cfg alerting.SMTPConfig, subject, _ string) error {
		gotTo, gotSubject = cfg.To, subject
		return nil
	}

	event, err := e.deps.Alerts.RaiseSync(t.Context(), alerting.Event{
		Type: domain.AlertUserBalanceNotice, Severity: domain.AlertWarning,
		DedupKey: "user_balance_notice:7", Title: "你的积分余额不足",
		Message: "账号 alice 当前余额 500 积分", EmailTo: []string{"alice@example.com"},
	})
	if err != nil {
		t.Fatalf("定向通知投递应成功: %v", err)
	}
	if event.Status != domain.AlertSent {
		t.Errorf("状态应为已发送，实际 %q", event.Status)
	}
	if len(gotTo) != 1 || gotTo[0] != "alice@example.com" {
		t.Errorf("收件人应为指定地址而非管理员地址，实际 %v", gotTo)
	}
	if gotSubject == "" {
		t.Error("邮件主题不应为空")
	}
	if got := sink.received(); len(got) != 0 {
		t.Errorf("定向通知不应投递到 Webhook，实际收到 %d 条", len(got))
	}
}

// 邮件通道未配置时，定向通知记为投递失败并写明原因——
// 事件已落库，「没收到」与「没触发」仍可区分。
func TestDirectedNoticeFailsWithoutEmailChannel(t *testing.T) {
	e := newTestEnv(t)
	sink := newWebhookSink(t, http.StatusOK)
	setSetting(t, e, "alert_webhook_url", sink.URL)

	event, err := e.deps.Alerts.RaiseSync(t.Context(), alerting.Event{
		Type: domain.AlertUserBalanceNotice, DedupKey: "user_balance_notice:8",
		Title: "你的积分余额不足", EmailTo: []string{"bob@example.com"},
	})
	if err == nil {
		t.Fatal("邮件通道未配置时应返回投递失败")
	}
	if event.Status != domain.AlertFailed {
		t.Errorf("状态应为投递失败，实际 %q", event.Status)
	}
	if got := sink.received(); len(got) != 0 {
		t.Errorf("定向通知不应改投 Webhook，实际收到 %d 条", len(got))
	}
}
