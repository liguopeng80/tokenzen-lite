package relay

import "testing"

// TestPingBodyNonEmptyMessages 回归：渠道测试体的 messages 曾用 []map[string]any 构造，
// canonical 解码按 body["messages"].([]any) 读取时类型断言失败、静默丢空消息，
// 跨协议（anthropic）渠道测试被上游以 "messages must not be empty" 拒绝
// （openai_compat 直通不解码故未暴露）。固定为：pingBody 经解码与编码后 messages 非空。
func TestPingBodyNonEmptyMessages(t *testing.T) {
	body := pingBody("test-model")
	canon, err := decodeOpenAIRequest(body)
	if err != nil {
		t.Fatalf("解码测试体失败: %v", err)
	}
	if len(canon.Messages) == 0 {
		t.Fatalf("canonical 解码后 messages 不应为空")
	}
	enc := encodeAnthropicRequest(canon, "test-model")
	msgs, _ := enc["messages"].([]map[string]any)
	if len(msgs) == 0 {
		t.Errorf("Anthropic 编码后 messages 不应为空: %v", enc["messages"])
	}
}
