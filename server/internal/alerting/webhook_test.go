package alerting

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

func testEvent() *store.AlertEvent {
	return &store.AlertEvent{
		AlertType: domain.AlertChannelAutoDisabled,
		Severity:  domain.AlertCritical,
		DedupKey:  "channel_auto_disabled:7",
		Title:     "渠道已自动禁用：智谱主线",
		Message:   "连续 3 次致命错误",
		CreatedAt: time.Unix(1_780_000_000, 0),
	}
}

// 各平台的报文结构不同，发错结构接收端会静默丢弃消息。
func TestBuildWebhookBodyPerPlatform(t *testing.T) {
	ev := testEvent()
	now := time.Unix(1_780_000_000, 0)

	cases := []struct {
		format    domain.WebhookFormat
		wantKey   string
		wantValue string
	}{
		{domain.WebhookDingTalk, "msgtype", "markdown"},
		{domain.WebhookWeCom, "msgtype", "markdown"},
		{domain.WebhookFeishu, "msg_type", "post"},
	}
	for _, c := range cases {
		t.Run(string(c.format), func(t *testing.T) {
			raw, err := BuildWebhookBody(Config{WebhookFormat: c.format}, ev, now)
			if err != nil {
				t.Fatalf("构造报文失败: %v", err)
			}
			var body map[string]any
			if err := json.Unmarshal(raw, &body); err != nil {
				t.Fatalf("报文不是合法 JSON: %v", err)
			}
			if body[c.wantKey] != c.wantValue {
				t.Errorf("期望 %s=%s，实际 %v", c.wantKey, c.wantValue, body[c.wantKey])
			}
			if !strings.Contains(string(raw), ev.Message) {
				t.Errorf("报文应含告警正文: %s", raw)
			}
		})
	}

	t.Run("slack", func(t *testing.T) {
		raw, err := BuildWebhookBody(Config{WebhookFormat: domain.WebhookSlack}, ev, now)
		if err != nil {
			t.Fatalf("构造报文失败: %v", err)
		}
		var body struct {
			Text   string           `json:"text"`
			Blocks []map[string]any `json:"blocks"`
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Fatalf("报文不是合法 JSON: %v", err)
		}
		if body.Text == "" || len(body.Blocks) != 2 {
			t.Errorf("Slack 报文应含 text 与两个 block: %s", raw)
		}
	})

	t.Run("generic", func(t *testing.T) {
		raw, err := BuildWebhookBody(Config{WebhookFormat: domain.WebhookGeneric}, ev, now)
		if err != nil {
			t.Fatalf("构造报文失败: %v", err)
		}
		var body map[string]any
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Fatalf("报文不是合法 JSON: %v", err)
		}
		for _, key := range []string{"alert_type", "severity", "title", "message", "dedup_key", "created_at"} {
			if _, ok := body[key]; !ok {
				t.Errorf("通用报文缺少字段 %s: %s", key, raw)
			}
		}
		if body["alert_type"] != string(domain.AlertChannelAutoDisabled) {
			t.Errorf("事件类型应原样下发，实际 %v", body["alert_type"])
		}
	})
}

// 严重度体现在标题前缀上：管理员在群消息列表里一眼能分出轻重。
func TestHeadingMarksSeverity(t *testing.T) {
	critical := testEvent()
	if got := Heading(critical); !strings.HasPrefix(got, "[严重]") {
		t.Errorf("严重告警标题应带严重标记，实际 %q", got)
	}
	warning := testEvent()
	warning.Severity = domain.AlertWarning
	if got := Heading(warning); !strings.HasPrefix(got, "[提醒]") {
		t.Errorf("提醒级告警标题应带提醒标记，实际 %q", got)
	}
	noTitle := testEvent()
	noTitle.Title = ""
	if got := Heading(noTitle); !strings.Contains(got, string(domain.AlertChannelAutoDisabled)) {
		t.Errorf("无标题时应回退到事件类型，实际 %q", got)
	}
}

// 钉钉的签名在查询参数上，飞书的在报文体内；放错位置接收端一律拒收。
func TestWebhookSigning(t *testing.T) {
	now := time.Unix(1_780_000_000, 0)

	t.Run("钉钉签名进查询参数", func(t *testing.T) {
		cfg := Config{
			WebhookURL:    "https://oapi.dingtalk.com/robot/send?access_token=abc",
			WebhookFormat: domain.WebhookDingTalk,
			WebhookSecret: "SEC123",
		}
		endpoint, err := WebhookEndpoint(cfg, now)
		if err != nil {
			t.Fatalf("构造地址失败: %v", err)
		}
		u, err := url.Parse(endpoint)
		if err != nil {
			t.Fatalf("地址不合法: %v", err)
		}
		q := u.Query()
		if q.Get("access_token") != "abc" {
			t.Errorf("原有查询参数应保留: %s", endpoint)
		}
		if q.Get("timestamp") != "1780000000000" {
			t.Errorf("时间戳应为毫秒，实际 %q", q.Get("timestamp"))
		}
		if q.Get("sign") == "" {
			t.Error("签名不应为空")
		}
	})

	t.Run("飞书签名进报文体", func(t *testing.T) {
		cfg := Config{WebhookFormat: domain.WebhookFeishu, WebhookSecret: "SEC123"}
		raw, err := BuildWebhookBody(cfg, testEvent(), now)
		if err != nil {
			t.Fatalf("构造报文失败: %v", err)
		}
		var body map[string]any
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Fatalf("报文不是合法 JSON: %v", err)
		}
		if body["timestamp"] != "1780000000" {
			t.Errorf("飞书时间戳应为秒，实际 %v", body["timestamp"])
		}
		if body["sign"] != FeishuSign("SEC123", now.Unix()) {
			t.Errorf("签名与算法输出不一致: %v", body["sign"])
		}
	})

	t.Run("未配置密钥时地址与报文不带签名", func(t *testing.T) {
		cfg := Config{WebhookURL: "https://example.com/hook", WebhookFormat: domain.WebhookDingTalk}
		endpoint, err := WebhookEndpoint(cfg, now)
		if err != nil {
			t.Fatalf("构造地址失败: %v", err)
		}
		if endpoint != cfg.WebhookURL {
			t.Errorf("无密钥时地址应原样返回，实际 %s", endpoint)
		}
		raw, _ := BuildWebhookBody(Config{WebhookFormat: domain.WebhookFeishu}, testEvent(), now)
		if strings.Contains(string(raw), `"sign"`) {
			t.Errorf("无密钥时报文不应带签名: %s", raw)
		}
	})
}

// BuildWebhookBody 必须为 domain.WebhookFormats 中的每个格式登记 builder。
// 否则新增 WebhookFormat 时漏登记会被静默当作 generic 投递——这正是该注册表要消除的失败模式。
func TestWebhookBuildersCoverAllFormats(t *testing.T) {
	for _, format := range domain.WebhookFormats {
		if _, ok := webhookBuilders[format]; !ok {
			t.Errorf("domain.WebhookFormats 中的 %s 未在 webhookBuilders 登记", format)
		}
	}
	// 反向：注册表里不应出现 domain 未登记的格式，否则两边会逐渐失去对照意义。
	for format := range webhookBuilders {
		if !format.Valid() {
			t.Errorf("webhookBuilders 中出现 domain 未登记的格式: %s", format)
		}
	}
}

// 传给 BuildWebhookBody 的格式不在注册表中时必须显式报错，而不是回退 generic。
func TestBuildWebhookBodyRejectsUnregisteredFormat(t *testing.T) {
	_, err := BuildWebhookBody(Config{WebhookFormat: "unknown-format"}, testEvent(), time.Unix(1_780_000_000, 0))
	if err == nil {
		t.Fatal("未登记的格式应返回错误，避免静默回退到 generic")
	}
}

// platformErrorCheckers 必须只为「200 响应体里回报业务错误码」的国内群平台登记。
// 自建接收端格式（generic、slack）不应在表内——它们的响应形态各异，强行解析会误判。
func TestPlatformErrorCheckersScope(t *testing.T) {
	wantChecked := map[domain.WebhookFormat]bool{
		domain.WebhookDingTalk: true,
		domain.WebhookFeishu:   true,
		domain.WebhookWeCom:    true,
	}
	for format := range platformErrorCheckers {
		if !wantChecked[format] {
			t.Errorf("platformErrorCheckers 不应检查 %s 的响应体", format)
		}
	}
	for format, want := range wantChecked {
		if _, got := platformErrorCheckers[format]; got != want {
			t.Errorf("platformErrorCheckers 中 %s 的登记状态应为 %v", format, want)
		}
	}
}

// 国内群机器人在 HTTP 200 的报文体里回报业务错误码；
// 只看状态码会把「机器人被移出群」当成投递成功。
func TestCheckPlatformError(t *testing.T) {
	cases := []struct {
		name    string
		format  domain.WebhookFormat
		body    string
		wantErr bool
	}{
		{"钉钉成功", domain.WebhookDingTalk, `{"errcode":0,"errmsg":"ok"}`, false},
		{"钉钉失败", domain.WebhookDingTalk, `{"errcode":310000,"errmsg":"sign not match"}`, true},
		{"企业微信失败", domain.WebhookWeCom, `{"errcode":93000,"errmsg":"invalid webhook url"}`, true},
		{"飞书失败", domain.WebhookFeishu, `{"code":19021,"msg":"sign match fail"}`, true},
		{"飞书成功", domain.WebhookFeishu, `{"code":0,"msg":"success"}`, false},
		{"自建接收端返回任意内容不误判", domain.WebhookGeneric, `{"errcode":1}`, false},
		{"报文不是 JSON 时不误判", domain.WebhookDingTalk, `OK`, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := checkPlatformError(c.format, []byte(c.body))
			if c.wantErr && err == nil {
				t.Error("期望识别为投递失败，实际判定成功")
			}
			if !c.wantErr && err != nil {
				t.Errorf("期望判定成功，实际 %v", err)
			}
		})
	}
}
