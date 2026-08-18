package domain

import "testing"

// 下游端点 × 上游协议支持矩阵：文本模型三协议均可承载，向量/图像模型仅 openai_compat。
func TestProtocolSupportsModality(t *testing.T) {
	cases := []struct {
		protocol ChannelProtocol
		modality Modality
		want     bool
	}{
		{ProtocolOpenAICompat, ModalityText, true},
		{ProtocolAnthropic, ModalityText, true},
		{ProtocolGemini, ModalityText, true},
		{ProtocolOpenAICompat, ModalityEmbedding, true},
		{ProtocolAnthropic, ModalityEmbedding, false},
		{ProtocolGemini, ModalityEmbedding, false},
		{ProtocolOpenAICompat, ModalityImage, true},
		{ProtocolAnthropic, ModalityImage, false},
		{ProtocolGemini, ModalityImage, false},
		{ChannelProtocol("grpc"), ModalityText, false}, // 非法协议不承载任何形态
	}
	for _, c := range cases {
		if got := ProtocolSupportsModality(c.protocol, c.modality); got != c.want {
			t.Errorf("ProtocolSupportsModality(%s, %s) = %v，期望 %v",
				c.protocol, c.modality, got, c.want)
		}
	}
}
