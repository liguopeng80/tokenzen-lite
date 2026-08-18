package relay

import (
	"testing"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
)

// U1：endpointForModality 表驱动——各模型形态映射到正确的下游端点提示。
// 提示文案会直接出现在 model_endpoint_mismatch 的错误消息里，
// 写错会把调用方引导到错误端点。未知形态必须回落到对话端点提示。
func TestEndpointForModality(t *testing.T) {
	cases := []struct {
		name string
		in   domain.Modality
		want string
	}{
		{"文本形态指向对话端点", domain.ModalityText, "/v1/chat/completions 或 /v1/messages"},
		{"向量形态指向 embeddings", domain.ModalityEmbedding, "/v1/embeddings"},
		{"图像形态指向 images", domain.ModalityImage, "/v1/images/generations"},
		{"未知形态回落对话端点", domain.Modality("video"), "/v1/chat/completions 或 /v1/messages"},
		{"空形态回落对话端点", domain.Modality(""), "/v1/chat/completions 或 /v1/messages"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := endpointForModality(c.in); got != c.want {
				t.Errorf("endpointForModality(%q) = %q，期望 %q", c.in, got, c.want)
			}
		})
	}
}
