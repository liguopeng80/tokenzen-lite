package alerting

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/obs"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
	"github.com/liguopeng80/tokenzen-lite/server/internal/strutil"
)

// webhookTimeout 单次 Webhook 请求的超时。
const webhookTimeout = 10 * time.Second

// webhookBuilder 按目标平台报文格式构造 payload。
// 每种 domain.WebhookFormat 必须在 webhookBuilders 注册表里登记 builder，
// 否则 BuildWebhookBody 拒绝构造——避免新增格式时漏写 case 落入默认分支被静默当作 generic。
type webhookBuilder func(cfg Config, ev *store.AlertEvent, now time.Time) (map[string]any, error)

// webhookBuilders 是全部支持的 Webhook 报文格式与其构造器的映射。
// 与 domain.WebhookFormats 对齐，由 TestWebhookBuildersCoverAllFormats 钉死。
var webhookBuilders = map[domain.WebhookFormat]webhookBuilder{
	domain.WebhookGeneric:  buildGenericWebhook,
	domain.WebhookDingTalk: buildDingTalkWebhook,
	domain.WebhookFeishu:   buildFeishuWebhook,
	domain.WebhookWeCom:    buildWeComWebhook,
	domain.WebhookSlack:    buildSlackWebhook,
}

func (s *Service) httpClient() *http.Client {
	if s.HTTPClient != nil {
		return s.HTTPClient
	}
	return &http.Client{Timeout: webhookTimeout}
}

// sendWebhook 按配置的报文格式投递告警。
// 外部依赖调用按 obs 规范在退出前打一条 INFO 日志，含目标、格式、耗时与状态——
// 「Webhook 发出去了但没收到」这类问题只能靠这条日志定位是网络失败还是业务错误码。
func (s *Service) sendWebhook(ctx context.Context, cfg Config, ev *store.AlertEvent) (err error) {
	start := time.Now()
	target := webhookTarget(cfg.WebhookURL)
	defer func() {
		status := "ok"
		if err != nil {
			status = "error"
		}
		obs.Logger(ctx).Info("alert_webhook",
			"target", target,
			"format", string(cfg.WebhookFormat),
			"duration_ms", time.Since(start).Milliseconds(),
			"status", status,
			"error", errOrNil(err),
		)
	}()

	body, err := BuildWebhookBody(cfg, ev, start)
	if err != nil {
		return err
	}
	endpoint, err := WebhookEndpoint(cfg, start)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("构造 Webhook 请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")

	resp, err := s.httpClient().Do(req)
	if err != nil {
		return fmt.Errorf("请求失败: %w", err)
	}
	defer resp.Body.Close()
	preview, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("接收端返回 %d: %s", resp.StatusCode, strutil.Truncate(string(preview), 200))
	}
	// 钉钉、飞书、企业微信在 HTTP 200 的报文体里回报业务错误码，
	// 只看状态码会把「机器人被移出群」这类失败当成投递成功。
	if err := checkPlatformError(cfg.WebhookFormat, preview); err != nil {
		return err
	}
	return nil
}

// webhookTarget 从原始 URL 中提取 host+path 作为日志中的目标标识，
// 避免把查询参数（access_token、签名）写进日志——这些是凭据级敏感信息。
func webhookTarget(rawURL string) string {
	if rawURL == "" {
		return "(unconfigured)"
	}
	if u, err := url.Parse(rawURL); err == nil && u.Host != "" {
		return u.Host + u.Path
	}
	return strutil.Truncate(rawURL, 80)
}

// errOrNil 把非空 error 转为截断后的字符串、nil error 转为空串，供 slog 字段使用。
func errOrNil(err error) string {
	if err == nil {
		return ""
	}
	return strutil.Truncate(err.Error(), 200)
}

// Heading 返回告警的单行标题，供各通道复用。
func Heading(ev *store.AlertEvent) string {
	title := ev.Title
	if title == "" {
		title = string(ev.AlertType)
	}
	marker := "提醒"
	if ev.Severity == domain.AlertCritical {
		marker = "严重"
	}
	return fmt.Sprintf("[%s] %s", marker, title)
}

// BuildWebhookBody 按目标平台的报文格式序列化告警。
// 飞书的签名在报文体内，因此需要配置与时间戳。
// 未在 webhookBuilders 登记的格式直接返回错误——避免新增格式时漏写 case
// 落入默认分支被静默当作 generic，等问题暴露时已无法收回告警。
func BuildWebhookBody(cfg Config, ev *store.AlertEvent, now time.Time) ([]byte, error) {
	builder, ok := webhookBuilders[cfg.WebhookFormat]
	if !ok {
		return nil, fmt.Errorf("不支持的 Webhook 报文格式: %s", cfg.WebhookFormat)
	}
	payload, err := builder(cfg, ev, now)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("序列化 Webhook 报文失败: %w", err)
	}
	return raw, nil
}

func buildDingTalkWebhook(_ Config, ev *store.AlertEvent, _ time.Time) (map[string]any, error) {
	heading := Heading(ev)
	return map[string]any{
		"msgtype":  "markdown",
		"markdown": map[string]any{"title": heading, "text": "### " + heading + "\n\n" + ev.Message},
	}, nil
}

func buildWeComWebhook(_ Config, ev *store.AlertEvent, _ time.Time) (map[string]any, error) {
	heading := Heading(ev)
	return map[string]any{
		"msgtype":  "markdown",
		"markdown": map[string]any{"content": "### " + heading + "\n\n" + ev.Message},
	}, nil
}

func buildFeishuWebhook(cfg Config, ev *store.AlertEvent, now time.Time) (map[string]any, error) {
	heading := Heading(ev)
	payload := map[string]any{
		"msg_type": "post",
		"content": map[string]any{
			"post": map[string]any{
				"zh_cn": map[string]any{
					"title":   heading,
					"content": [][]map[string]any{{{"tag": "text", "text": ev.Message}}},
				},
			},
		},
	}
	if cfg.WebhookSecret != "" {
		ts := now.Unix()
		payload["timestamp"] = strconv.FormatInt(ts, 10)
		payload["sign"] = FeishuSign(cfg.WebhookSecret, ts)
	}
	return payload, nil
}

func buildSlackWebhook(_ Config, ev *store.AlertEvent, _ time.Time) (map[string]any, error) {
	heading := Heading(ev)
	return map[string]any{
		"text": heading + "\n" + ev.Message,
		"blocks": []map[string]any{
			{"type": "header", "text": map[string]any{"type": "plain_text", "text": heading}},
			{"type": "section", "text": map[string]any{"type": "mrkdwn", "text": ev.Message}},
		},
	}, nil
}

func buildGenericWebhook(_ Config, ev *store.AlertEvent, _ time.Time) (map[string]any, error) {
	heading := Heading(ev)
	return map[string]any{
		"alert_type": ev.AlertType,
		"severity":   ev.Severity,
		"title":      heading,
		"message":    ev.Message,
		"dedup_key":  ev.DedupKey,
		"payload":    json.RawMessage(nullIfEmpty(ev.Payload)),
		"created_at": ev.CreatedAt.Format(time.RFC3339),
	}, nil
}

// WebhookEndpoint 返回实际请求地址。钉钉的签名在查询参数上，其余格式原样返回。
func WebhookEndpoint(cfg Config, now time.Time) (string, error) {
	if cfg.WebhookSecret == "" || cfg.WebhookFormat != domain.WebhookDingTalk {
		return cfg.WebhookURL, nil
	}
	u, err := url.Parse(cfg.WebhookURL)
	if err != nil {
		return "", fmt.Errorf("Webhook 地址格式错误: %w", err)
	}
	ts := strconv.FormatInt(now.UnixMilli(), 10)
	mac := hmac.New(sha256.New, []byte(cfg.WebhookSecret))
	mac.Write([]byte(ts + "\n" + cfg.WebhookSecret))
	q := u.Query()
	q.Set("timestamp", ts)
	q.Set("sign", base64.StdEncoding.EncodeToString(mac.Sum(nil)))
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// FeishuSign 计算飞书自定义机器人的签名：以「时间戳\n密钥」作为 HMAC 密钥
// 对空消息求值，取 base64。
func FeishuSign(secret string, timestamp int64) string {
	mac := hmac.New(sha256.New, []byte(strconv.FormatInt(timestamp, 10)+"\n"+secret))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// platformErrorChecker 解析特定平台在 200 响应体里回报的业务错误码。
// 报文无法解析或不含错误码时返回 nil，避免误判自建接收端的响应。
type platformErrorChecker func(body []byte) error

// platformErrorCheckers 是「需要解析 200 响应体里业务错误码」的平台集合。
// 不在表内的格式不检查——自建接收端的响应形态各异，强行解析会误判。
// 与 domain.WebhookFormats 的覆盖关系由 TestPlatformErrorCheckersScope 钉死。
var platformErrorCheckers = map[domain.WebhookFormat]platformErrorChecker{
	domain.WebhookDingTalk: parseNativePlatformError,
	domain.WebhookWeCom:    parseNativePlatformError,
	domain.WebhookFeishu:   parseNativePlatformError,
}

// checkPlatformError 解析国内群机器人在 200 响应体里回报的业务错误码。
func checkPlatformError(format domain.WebhookFormat, body []byte) error {
	fn, ok := platformErrorCheckers[format]
	if !ok {
		return nil
	}
	return fn(body)
}

// parseNativePlatformError 解析钉钉/企业微信（errcode/errmsg）与飞书（code/msg）的错误码格式。
func parseNativePlatformError(body []byte) error {
	var resp struct {
		ErrCode int    `json:"errcode"`
		ErrMsg  string `json:"errmsg"`
		Code    int    `json:"code"`
		Msg     string `json:"msg"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil
	}
	if resp.ErrCode != 0 {
		return fmt.Errorf("接收端拒绝（errcode=%d）: %s", resp.ErrCode, resp.ErrMsg)
	}
	if resp.Code != 0 {
		return fmt.Errorf("接收端拒绝（code=%d）: %s", resp.Code, resp.Msg)
	}
	return nil
}

func nullIfEmpty(raw []byte) []byte {
	if len(raw) == 0 {
		return []byte("null")
	}
	return raw
}
