package relay

// 兜底估算与流式 usage 嗅探的单元测试（架构决策 2026-08-05 第 3 项）：
// 字节估算仅在上游不返回 usage 时作兜底，本文件固定其换算口径与判定依据。

import (
	"math"
	"net/http"
	"testing"
)

// U1: estimateTokensFromText 兜底估算换算口径（UTF-8 字节数 / 4，向上保底 1）。
func TestEstimateTokensFromText(t *testing.T) {
	cases := []struct {
		name    string
		byteLen int
		want    int64
	}{
		{"零字节返回 0", 0, 0},
		{"1 字节保底 1", 1, 1},
		{"2 字节保底 1", 2, 1},
		{"3 字节保底 1", 3, 1},
		{"4 字节精确除 4", 4, 1},
		{"8 字节精确除 4", 8, 2},
		{"4000 字节精确除 4", 4000, 1000},
		{"5 字节向下取整为 1", 5, 1},
		{"大输入无溢出", math.MaxInt32, int64(math.MaxInt32) / 4},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := estimateTokensFromText(c.byteLen); got != c.want {
				t.Errorf("estimateTokensFromText(%d) = %d，期望 %d", c.byteLen, got, c.want)
			}
		})
	}
}

// U3: contentLengthOfBody 在 ContentLength 缺失（0 或 -1 chunked）时返回 0，
// 防止估算兜底引入负数。
func TestContentLengthOfBody(t *testing.T) {
	cases := []struct {
		name          string
		contentLength int64
		want          int
	}{
		{"正常长度原样返回", 128, 128},
		{"零长度返回 0", 0, 0},
		{"chunked（-1）返回 0", -1, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := &http.Request{ContentLength: c.contentLength}
			if got := contentLengthOfBody(r); got != c.want {
				t.Errorf("contentLengthOfBody(ContentLength=%d) = %d，期望 %d",
					c.contentLength, got, c.want)
			}
		})
	}
}

// U2: OpenAI 直通流 usage 嗅探——断连后"真实用量 vs 兜底估算"分叉的判定依据。
// usage 位于 finish_reason 之后的末尾 chunk 时必须命中；整条流无 usage 时必须报告未命中。
func TestOpenAIPassStreamUsageSniff(t *testing.T) {
	t.Run("usage 在 finish_reason 之后的末尾 chunk", func(t *testing.T) {
		cs := (&openaiPassthrough{publicModel: "pub-model"}).NewStream()
		chunks := []string{
			`{"id":"c","model":"up-model","choices":[{"delta":{"content":"hi"}}]}`,
			`{"id":"c","model":"up-model","choices":[{"delta":{},"finish_reason":"stop"}]}`,
			`{"id":"c","model":"up-model","choices":[],"usage":{"prompt_tokens":77,"completion_tokens":33}}`,
		}
		for _, c := range chunks {
			cs.ProcessPayload([]byte(c))
		}
		cs.ProcessDone()
		usage, found := cs.Usage()
		if !found {
			t.Fatal("末尾 usage chunk 应被嗅探到（usageFound=true）")
		}
		if usage.BaseInput != 77 || usage.Output != 33 {
			t.Errorf("usage 数值错误，期望 77/33，实际 %d/%d", usage.BaseInput, usage.Output)
		}
	})
	t.Run("整条流无 usage 字段", func(t *testing.T) {
		cs := (&openaiPassthrough{publicModel: "pub-model"}).NewStream()
		chunks := []string{
			`{"id":"c","model":"up-model","choices":[{"delta":{"content":"hi"}}]}`,
			`{"id":"c","model":"up-model","choices":[{"delta":{},"finish_reason":"stop"}]}`,
		}
		for _, c := range chunks {
			cs.ProcessPayload([]byte(c))
		}
		cs.ProcessDone()
		if _, found := cs.Usage(); found {
			t.Error("无 usage 的流不应报告 usageFound=true")
		}
	})
}
