package relay

import (
	"testing"

	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// system 提示词在 Anthropic 协议里独立于 messages，必须计入输入估算，
// 否则带长系统提示词的会话会被显著低估。
func TestEstimateAnthropicInputTokensCountsSystem(t *testing.T) {
	messagesOnly := map[string]any{
		"messages": []any{
			map[string]any{"role": "user", "content": "0123456789ABCDEF"},
		},
	}
	base := estimateAnthropicInputTokens(messagesOnly)
	if base <= 0 {
		t.Fatalf("仅有 messages 时估算应为正数，实际 %d", base)
	}

	cases := []struct {
		name   string
		system any
	}{
		{"字符串形式的 system", "01234567890123456789012345678901"},
		{"内容块数组形式的 system", []any{
			map[string]any{"type": "text", "text": "0123456789012345"},
			map[string]any{"type": "text", "text": "0123456789012345"},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := map[string]any{
				"messages": messagesOnly["messages"],
				"system":   tc.system,
			}
			got := estimateAnthropicInputTokens(body)
			if got <= base {
				t.Errorf("计入 system 后估算应增加：base %d，实际 %d", base, got)
			}
		})
	}
}

// 无法识别的 system 取值不应让估算出错或归零。
func TestEstimateAnthropicInputTokensIgnoresUnknownSystemShape(t *testing.T) {
	body := map[string]any{
		"messages": []any{map[string]any{"role": "user", "content": "0123456789ABCDEF"}},
		"system":   map[string]any{"unexpected": true},
	}
	if got := estimateAnthropicInputTokens(body); got <= 0 {
		t.Errorf("system 形态无法识别时应回落到 messages 估算，实际 %d", got)
	}
}

// 计数请求不接受流式与生成上限参数，转发前必须剔除，否则上游判为非法请求。
func TestBuildCountTokensRequestStripsUnsupportedFields(t *testing.T) {
	ch := &store.Channel{BaseURL: "https://upstream.example.com"}
	body := map[string]any{
		"model": "public", "stream": true, "max_tokens": float64(100),
		"messages": []any{},
	}
	req, err := buildCountTokensRequest(t.Context(), ch, "sk-test", body, "upstream-model")
	if err != nil {
		t.Fatalf("构建计数请求失败: %v", err)
	}
	if got := req.URL.Path; got != "/v1/messages/count_tokens" {
		t.Errorf("请求路径错误: %s", got)
	}
	if _, exists := body["stream"]; exists {
		t.Error("stream 字段应被剔除")
	}
	if _, exists := body["max_tokens"]; exists {
		t.Error("max_tokens 字段应被剔除")
	}
	if body["model"] != "upstream-model" {
		t.Errorf("模型名应改写为上游名，实际 %v", body["model"])
	}
}
