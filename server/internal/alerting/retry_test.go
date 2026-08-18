package alerting

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// nextRetry 的边界：前 len(retryBackoffs) 次失败各有一次退避，之后没有。
// 重试节奏的退化会导致重试次数错算或死信提前/延后，必须钉死。
func TestNextRetry(t *testing.T) {
	want := []time.Duration{2 * time.Second, 8 * time.Second, 30 * time.Second}
	for attempt := 1; attempt <= len(want); attempt++ {
		got, ok := nextRetry(attempt)
		if !ok {
			t.Fatalf("第 %d 次失败后应仍有重试，实际 ok=false", attempt)
		}
		if got != want[attempt-1] {
			t.Errorf("第 %d 次失败后的退避应为 %v，实际 %v", attempt, want[attempt-1], got)
		}
	}
	if _, ok := nextRetry(len(want) + 1); ok {
		t.Errorf("最后一次尝试（第 %d 次）之后不应再有重试", len(want)+1)
	}
	if _, ok := nextRetry(0); ok {
		t.Error("attempt=0 不应产生重试")
	}
}

// outcome 记录一次回写的状态与尝试序号，供断言重试轨迹。
type outcome struct {
	attempt int
	sent    []string
	failed  []string
	status  domain.AlertStatus
}

type outcomeLog struct{ items []outcome }

func (l *outcomeLog) record(_ context.Context, _ *store.AlertEvent, attempt int, sent, failures []string, status domain.AlertStatus) {
	l.items = append(l.items, outcome{attempt: attempt, sent: sent, failed: failures, status: status})
}

// scriptedDeliver 按脚本依次返回预设结果，超出脚本后恒返回「失败且已尝试」。
type scriptedDeliver struct {
	results []struct {
		sent, failures []string
		attempted      bool
	}
	calls int
}

func (d *scriptedDeliver) deliver(_ context.Context) (sent, failures []string, attempted bool) {
	i := d.calls
	d.calls++
	if i < len(d.results) {
		r := d.results[i]
		return r.sent, r.failures, r.attempted
	}
	return nil, []string{"webhook: 持续失败"}, true
}

type sleepLog struct {
	durs  []time.Duration
	stops int // 模拟取消：第 N 次睡眠返回 false
}

// noCancelSleeper 立即返回 true，记录退避时长但不真睡眠。
func (s *sleepLog) noCancel(_ context.Context, d time.Duration) bool {
	s.durs = append(s.durs, d)
	return true
}

func newRecord() *store.AlertEvent {
	return &store.AlertEvent{ID: 42, AlertType: domain.AlertChannelAutoDisabled, Title: "t"}
}

// 投递首次即成功：只回写一次 sent，不重试、不睡眠。
func TestDeliverWithRetrySuccessNoRetry(t *testing.T) {
	deliver := &scriptedDeliver{results: []struct {
		sent, failures []string
		attempted      bool
	}{{sent: []string{"webhook"}, attempted: true}}}
	log := &outcomeLog{}
	sleep := &sleepLog{}

	deliverWithRetry(context.Background(), newRecord(), deliver.deliver, log.record, sleep.noCancel)

	if deliver.calls != 1 {
		t.Errorf("应只尝试 1 次，实际 %d 次", deliver.calls)
	}
	if len(log.items) != 1 || log.items[0].status != domain.AlertSent {
		t.Fatalf("应只回写一次 sent，实际 %+v", log.items)
	}
	if log.items[0].attempt != 1 {
		t.Errorf("成功回写的尝试序号应为 1，实际 %d", log.items[0].attempt)
	}
	if len(sleep.durs) != 0 {
		t.Errorf("成功后不应睡眠，实际睡眠 %v", sleep.durs)
	}
}

// 前两次失败、第三次成功：回写轨迹 failed/failed/sent，睡眠两次后停止。
func TestDeliverWithRetrySucceedsAfterRetries(t *testing.T) {
	deliver := &scriptedDeliver{results: []struct {
		sent, failures []string
		attempted      bool
	}{
		{failures: []string{"webhook: timeout"}, attempted: true},
		{failures: []string{"webhook: 503"}, attempted: true},
		{sent: []string{"webhook"}, attempted: true},
	}}
	log := &outcomeLog{}
	sleep := &sleepLog{}

	deliverWithRetry(context.Background(), newRecord(), deliver.deliver, log.record, sleep.noCancel)

	if deliver.calls != 3 {
		t.Errorf("应尝试 3 次，实际 %d 次", deliver.calls)
	}
	if len(log.items) != 3 {
		t.Fatalf("应回写 3 次，实际 %d 次：%+v", len(log.items), log.items)
	}
	for i, want := range []domain.AlertStatus{domain.AlertFailed, domain.AlertFailed, domain.AlertSent} {
		if log.items[i].status != want {
			t.Errorf("第 %d 次回写状态应为 %s，实际 %s", i+1, want, log.items[i].status)
		}
		if log.items[i].attempt != i+1 {
			t.Errorf("第 %d 次回写的尝试序号应为 %d，实际 %d", i+1, i+1, log.items[i].attempt)
		}
	}
	if len(sleep.durs) != 2 {
		t.Fatalf("应睡眠 2 次（成功前），实际 %d 次：%v", len(sleep.durs), sleep.durs)
	}
	if sleep.durs[0] != 2*time.Second || sleep.durs[1] != 8*time.Second {
		t.Errorf("退避应为 2s、8s，实际 %v", sleep.durs)
	}
}

// 全部重试耗尽：最后一次回写 dead_letter，尝试次数 = 1 + len(retryBackoffs)。
func TestDeliverWithRetryExhaustedToDeadLetter(t *testing.T) {
	deliver := &scriptedDeliver{results: []struct {
		sent, failures []string
		attempted      bool
	}{
		{failures: []string{"webhook: down"}, attempted: true}, // 永远失败
	}}
	log := &outcomeLog{}
	sleep := &sleepLog{}

	deliverWithRetry(context.Background(), newRecord(), deliver.deliver, log.record, sleep.noCancel)

	wantAttempts := 1 + len(retryBackoffs)
	if deliver.calls != wantAttempts {
		t.Errorf("应尝试 %d 次，实际 %d 次", wantAttempts, deliver.calls)
	}
	if len(log.items) != wantAttempts {
		t.Fatalf("应回写 %d 次，实际 %d 次", wantAttempts, len(log.items))
	}
	last := log.items[len(log.items)-1]
	if last.status != domain.AlertDeadLetter {
		t.Errorf("最后一次应回写 dead_letter，实际 %s（轨迹 %+v）", last.status, log.items)
	}
	if last.attempt != wantAttempts {
		t.Errorf("死信回写的尝试序号应为 %d，实际 %d", wantAttempts, last.attempt)
	}
	// 中间各次应为 failed。
	for i := 0; i < len(log.items)-1; i++ {
		if log.items[i].status != domain.AlertFailed {
			t.Errorf("第 %d 次回写应为 failed，实际 %s", i+1, log.items[i].status)
		}
	}
	if len(sleep.durs) != len(retryBackoffs) {
		t.Errorf("应睡眠 %d 次，实际 %d 次：%v", len(retryBackoffs), len(sleep.durs), sleep.durs)
	}
}

// 未配置任何通道（attempted=false）：记 failed 后立即停止，不重试、不睡眠。
// 配置缺失不会自行恢复，重试只会白白占用后台协程。
func TestDeliverWithRetryNoChannelsNoRetry(t *testing.T) {
	deliver := &scriptedDeliver{results: []struct {
		sent, failures []string
		attempted      bool
	}{
		{failures: nil, attempted: false},
	}}
	log := &outcomeLog{}
	sleep := &sleepLog{}

	deliverWithRetry(context.Background(), newRecord(), deliver.deliver, log.record, sleep.noCancel)

	if deliver.calls != 1 {
		t.Errorf("未配置通道应只尝试 1 次，实际 %d 次", deliver.calls)
	}
	if len(log.items) != 1 || log.items[0].status != domain.AlertFailed {
		t.Fatalf("应只回写一次 failed，实际 %+v", log.items)
	}
	if log.items[0].status == domain.AlertDeadLetter {
		t.Error("未配置通道不应转死信——它从未真正尝试过")
	}
	if len(sleep.durs) != 0 {
		t.Errorf("未配置通道不应睡眠，实际 %v", sleep.durs)
	}
}

// 退避期间上下文取消：保持最近一次 failed，不再继续重试。
func TestDeliverWithRetryCancelledDuringBackoff(t *testing.T) {
	deliver := &scriptedDeliver{results: []struct {
		sent, failures []string
		attempted      bool
	}{
		{failures: []string{"webhook: err"}, attempted: true},
	}}
	log := &outcomeLog{}
	cancelledSleep := func(_ context.Context, _ time.Duration) bool { return false }

	deliverWithRetry(context.Background(), newRecord(), deliver.deliver, log.record, cancelledSleep)

	if deliver.calls != 1 {
		t.Errorf("退避取消后不应再次尝试，实际 %d 次", deliver.calls)
	}
	if len(log.items) != 1 || log.items[0].status != domain.AlertFailed {
		t.Fatalf("取消时应只保留一次 failed，实际 %+v", log.items)
	}
}

// 定向邮件（EmailTo 非空）只走邮件通道：Webhook 不应被调用，收件人改写为 EmailTo。
// 这是后台重试不得破坏的语义——把员工余额投进群里既无用也越权。
func TestAttemptChannelsDirectedEmailSkipsWebhook(t *testing.T) {
	transport := &recordingTransport{}
	s := &Service{
		HTTPClient: &http.Client{Transport: transport},
	}
	var gotTo []string
	s.SendMail = func(cfg SMTPConfig, _, _ string) error {
		gotTo = cfg.To
		return nil
	}
	cfg := Config{
		WebhookURL:    "https://hook.example/test", // 配了 webhook，验证它不被走
		WebhookFormat: domain.WebhookGeneric,       // 与 LoadConfig 的回退保持一致：空格式归一到 generic
		SMTP:          SMTPConfig{Host: "smtp.example.com", From: "gw@example.com", To: []string{"admin@example.com"}},
	}

	t.Run("定向邮件改写收件人且不发 Webhook", func(t *testing.T) {
		transport.mu.Lock()
		transport.calls = 0
		transport.mu.Unlock()
		gotTo = nil

		sent, failures, attempted := s.attemptChannels(
			context.Background(), cfg, newRecord(), []string{"alice@example.com"})

		if !attempted || len(sent) != 1 || sent[0] != "email" {
			t.Errorf("定向邮件应经邮件通道送达，实际 sent=%v failures=%v attempted=%v", sent, failures, attempted)
		}
		if len(failures) != 0 {
			t.Errorf("不应有失败，实际 %v", failures)
		}
		if len(gotTo) != 1 || gotTo[0] != "alice@example.com" {
			t.Errorf("收件人应改写为定向地址，实际 %v", gotTo)
		}
		if transport.calls != 0 {
			t.Errorf("定向邮件不应触发 Webhook，实际调用 %d 次", transport.calls)
		}
	})

	t.Run("未指定 EmailTo 时按配置投递 Webhook 与邮件", func(t *testing.T) {
		transport.mu.Lock()
		transport.calls = 0
		transport.mu.Unlock()

		sent, _, attempted := s.attemptChannels(context.Background(), cfg, newRecord(), nil)

		if !attempted {
			t.Error("配置了通道应标记 attempted=true")
		}
		if transport.calls != 1 {
			t.Errorf("应请求一次 Webhook，实际 %d 次", transport.calls)
		}
		if len(sent) != 2 {
			t.Errorf("Webhook 与邮件都成功时应记录两条通道，实际 %v", sent)
		}
	})
}

// 定向邮件但邮件通道未配置：返回未尝试，调用方据此跳过重试。
func TestAttemptChannelsDirectedEmailUnconfiguredNotAttempted(t *testing.T) {
	s := &Service{}
	cfg := Config{} // 无 SMTP

	sent, failures, attempted := s.attemptChannels(
		context.Background(), cfg, newRecord(), []string{"alice@example.com"})

	if attempted {
		t.Error("邮件通道未配置时不应标记 attempted")
	}
	if len(sent) != 0 {
		t.Errorf("不应有成功通道，实际 %v", sent)
	}
	if len(failures) != 1 {
		t.Errorf("应返回一条未配置失败原因，实际 %v", failures)
	}
}

// Service 未装配或未配置事件仓库时 Raise 必须静默返回，不 panic。
// 这是未配置告警通道的部署与单测中的 nil-safe 契约。
func TestRaiseNilSafe(t *testing.T) {
	var s *Service
	s.Raise(context.Background(), Event{Type: domain.AlertTest, Title: "nil receiver"}) // nil 接收者

	(&Service{}).Raise(context.Background(), Event{Type: domain.AlertTest, Title: "nil repo"}) // Events 为 nil
	// 能执行到这里即说明未 panic。
}

// recordingTransport 记录 Webhook 请求次数，按 200 成功响应。
type recordingTransport struct {
	mu    sync.Mutex
	calls int
}

func (t *recordingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	t.mu.Lock()
	t.calls++
	t.mu.Unlock()
	return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody}, nil
}
