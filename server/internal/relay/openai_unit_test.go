package relay

import (
	"testing"

	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// openai_compat 渠道的 BaseURL 应含版本根路径（如 https://api.openai.com/v1、
// 智谱 Coding Plan 的 https://open.bigmodel.cn/api/coding/paas），codec 仅追加
// /chat/completions，不再硬编码 /v1，避免与 BaseURL 中的版本段重复拼成 /v1/v1。
func TestBuildOpenAIRequestAppendsChatCompletions(t *testing.T) {
	cases := []struct {
		name    string
		baseURL string
		want    string
	}{
		{"openai 标准版本根", "https://api.openai.com/v1", "https://api.openai.com/v1/chat/completions"},
		{"智谱 coding plan 端点", "https://open.bigmodel.cn/api/coding/paas", "https://open.bigmodel.cn/api/coding/paas/chat/completions"},
		{"根地址（无版本段）仍兼容", "https://api.deepseek.com", "https://api.deepseek.com/chat/completions"},
		{"尾部斜杠被裁剪", "https://api.openai.com/v1/", "https://api.openai.com/v1/chat/completions"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ch := &store.Channel{BaseURL: tc.baseURL}
			req, err := buildOpenAIRequest(
				t.Context(), ch, "sk-test",
				map[string]any{"model": "x", "messages": []any{}},
				"upstream", false, "/chat/completions",
			)
			if err != nil {
				t.Fatalf("构建请求失败: %v", err)
			}
			if got := req.URL.String(); got != tc.want {
				t.Errorf("上游 URL 错误: got %s, want %s", got, tc.want)
			}
		})
	}
}
