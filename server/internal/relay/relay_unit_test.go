package relay

import (
	"encoding/json"
	"testing"

	"gorm.io/datatypes"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

func mkChannel(id int64, priority, weight int) store.Channel {
	return store.Channel{ID: id, Priority: priority, Weight: weight}
}

func TestSelectChannelPriorityTiers(t *testing.T) {
	// 高优先级层未耗尽时不应选到低优先级
	channels := []store.Channel{mkChannel(1, 10, 1), mkChannel(2, 10, 1), mkChannel(3, 0, 100)}
	for i := 0; i < 50; i++ {
		c := SelectChannel(channels, nil)
		if c == nil || c.Priority != 10 {
			t.Fatalf("应始终从高优先级层选择，实际选到 %+v", c)
		}
	}
	// 高层全部排除后降层
	c := SelectChannel(channels, map[int64]bool{1: true, 2: true})
	if c == nil || c.ID != 3 {
		t.Fatalf("高层耗尽应降层选 3，实际 %+v", c)
	}
	// 全部排除
	if c := SelectChannel(channels, map[int64]bool{1: true, 2: true, 3: true}); c != nil {
		t.Fatalf("全部排除应返回 nil，实际 %+v", c)
	}
}

func TestSelectChannelWeightDistribution(t *testing.T) {
	// weight 9 vs 0 → 期望约 (9+1):(0+1) = 10:1
	channels := []store.Channel{mkChannel(1, 0, 9), mkChannel(2, 0, 0)}
	counts := map[int64]int{}
	const rounds = 5000
	for i := 0; i < rounds; i++ {
		counts[SelectChannel(channels, nil).ID]++
	}
	ratio := float64(counts[1]) / float64(counts[2])
	if ratio < 7 || ratio > 14 {
		t.Errorf("加权比例应接近 10:1，实际 %d:%d (%.1f)", counts[1], counts[2], ratio)
	}
}

func TestClassifyUpstreamError(t *testing.T) {
	cases := []struct {
		status int
		body   string
		want   domain.ErrorClass
	}{
		{401, `{"error":{"message":"bad key"}}`, domain.ErrClassAuthFatal},
		{403, `forbidden`, domain.ErrClassAuthFatal},
		{402, `payment required`, domain.ErrClassQuotaFatal},
		{429, `{"error":{"message":"rate limit exceeded"}}`, domain.ErrClassRateLimited},
		// 429 一律归为限流，即使报文含余额/额度字样（2026-08-05 决策 1）
		{429, `{"error":{"message":"insufficient balance"}}`, domain.ErrClassRateLimited},
		{429, `{"error":{"message":"quota exceeded"}}`, domain.ErrClassRateLimited},
		{500, `internal`, domain.ErrClassTransient},
		{503, `overloaded`, domain.ErrClassTransient},
		{400, `{"error":{"message":"invalid_api_key"}}`, domain.ErrClassAuthFatal},
		{400, `{"error":{"message":"missing field"}}`, domain.ErrClassBadRequest},
		// 非 429 的 4xx 含明确余额类文案 → 上游欠费
		{400, `{"error":{"message":"insufficient balance"}}`, domain.ErrClassQuotaFatal},
		{400, `{"error":{"message":"账户余额不足"}}`, domain.ErrClassQuotaFatal},
		// 真实生产语料（proxy.db 4xx response_body 取证，脱敏改写）：
		// 令牌额度耗尽的中英文标准文案，此前漏判为 bad_request，导致渠道连续失败不计、不换渠。
		{400, `{"error":{"code":"","message":"该令牌额度已用尽 [sk-XXX]","type":"quota_error"}}`, domain.ErrClassQuotaFatal},
		{400, `{"error":{"type":"quota_error","message":"token quota is not enough, token remain quota: ＄0.04, need quota: ＄0.18"}}`, domain.ErrClassQuotaFatal},
		// 边界补齐（测试计划 U2）
		{404, `{"error":{"message":"model not found"}}`, domain.ErrClassBadRequest},
		{200, `ok`, domain.ErrClassNone},
		{0, ``, domain.ErrClassNone},
		{400, `{"error":{"message":"账户欠费，请充值后重试"}}`, domain.ErrClassQuotaFatal},
		{400, `{"error":{"message":"account_deactivated"}}`, domain.ErrClassAuthFatal},
	}
	for _, tc := range cases {
		if got := ClassifyUpstreamError(tc.status, []byte(tc.body)); got != tc.want {
			t.Errorf("HTTP %d %q 应分类为 %s，实际 %s", tc.status, tc.body, tc.want, got)
		}
	}
	if !CountsTowardAutoDisable(domain.ErrClassAuthFatal) || !CountsTowardAutoDisable(domain.ErrClassQuotaFatal) {
		t.Error("计数决策错误：auth_fatal/quota_fatal 应计入连续失败")
	}
	if CountsTowardAutoDisable(domain.ErrClassRateLimited) || CountsTowardAutoDisable(domain.ErrClassTransient) {
		t.Error("计数决策错误：rate_limited/transient 不应计入连续失败")
	}
	if ShouldRetryNextChannel(domain.ErrClassBadRequest) || !ShouldRetryNextChannel(domain.ErrClassTransient) {
		t.Error("重试决策错误：bad_request 不重试，transient 重试")
	}
	// 补充断言（测试计划 U3）
	if CountsTowardAutoDisable(domain.ErrClassBadRequest) || CountsTowardAutoDisable(domain.ErrClassNone) {
		t.Error("计数决策错误：bad_request/none 不应计入连续失败")
	}
	if !ShouldRetryNextChannel(domain.ErrClassRateLimited) {
		t.Error("重试决策错误：rate_limited 应换渠道重试")
	}
}

func TestChannelHealthCounter(t *testing.T) {
	var h channelHealth
	if got := h.fail(1); got != 1 {
		t.Errorf("首次失败计数应为 1，实际 %d", got)
	}
	if got := h.fail(1); got != 2 {
		t.Errorf("连续失败计数应为 2，实际 %d", got)
	}
	if got := h.fail(2); got != 1 {
		t.Errorf("不同渠道计数应独立，实际 %d", got)
	}
	h.reset(1)
	if got := h.fail(1); got != 1 {
		t.Errorf("清零后计数应重新从 1 开始，实际 %d", got)
	}
	// 多渠道独立性：reset(1) 不影响渠道 2 的既有计数（测试计划 U4）
	if got := h.fail(2); got != 2 {
		t.Errorf("渠道 2 的计数不应受渠道 1 清零影响，期望 2，实际 %d", got)
	}
}

func TestNormalizeOpenAIUsage(t *testing.T) {
	raw := `{"prompt_tokens":1200,"completion_tokens":300,
		"prompt_tokens_details":{"cached_tokens":1000,"audio_tokens":50},
		"completion_tokens_details":{"audio_tokens":20}}`
	var u openaiUsage
	if err := json.Unmarshal([]byte(raw), &u); err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	n := normalizeOpenAIUsage(&u)
	// base = 1200 - 1000(缓存) - 50(音频) = 150
	if n.BaseInput != 150 || n.CacheRead != 1000 || n.AudioInput != 50 {
		t.Errorf("输入侧归一化错误: %+v", n)
	}
	// output = 300 - 20(音频) = 280
	if n.Output != 280 || n.AudioOutput != 20 {
		t.Errorf("输出侧归一化错误: %+v", n)
	}
}

func TestRewriteOpenAIChunk(t *testing.T) {
	chunk := []byte(`{"id":"c1","model":"glm-5-upstream","choices":[{"delta":{"content":"你好"}}],"usage":null}`)
	out, _, found := rewriteOpenAIChunk(chunk, "claude-sonnet-4-6")
	if found {
		t.Error("usage 为 null 不应视为找到")
	}
	var parsed map[string]any
	_ = json.Unmarshal(out, &parsed)
	if parsed["model"] != "claude-sonnet-4-6" {
		t.Errorf("model 应改写回公开名，实际 %v", parsed["model"])
	}

	final := []byte(`{"id":"c1","model":"glm-5-upstream","choices":[],"usage":{"prompt_tokens":10,"completion_tokens":5}}`)
	_, usage, found := rewriteOpenAIChunk(final, "claude-sonnet-4-6")
	if !found || usage.BaseInput != 10 || usage.Output != 5 {
		t.Errorf("末 chunk usage 应被嗅探: found=%v %+v", found, usage)
	}
}

func TestChannelMappedModel(t *testing.T) {
	ch := store.Channel{ModelMapping: datatypes.JSON(`{"claude-sonnet-4-6":"glm-5-turbo"}`)}
	if got := ch.MappedModel("claude-sonnet-4-6"); got != "glm-5-turbo" {
		t.Errorf("映射应生效，实际 %s", got)
	}
	if got := ch.MappedModel("gpt-5.4"); got != "gpt-5.4" {
		t.Errorf("无映射应原样返回，实际 %s", got)
	}
}
