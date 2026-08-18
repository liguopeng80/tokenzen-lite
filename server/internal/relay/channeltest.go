package relay

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/liguopeng80/tokenzen-lite/server/internal/obs"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// pingBody 构造连通性测试的最小对话请求体（OpenAI 风格）。
// messages 必须用 []any 构造：canonical 解码按 body["messages"].([]any) 读取，
// 用 []map[string]any 会因切片类型不匹配而断言失败、静默丢空消息（仅跨协议渠道暴露）。
func pingBody(model string) map[string]any {
	return map[string]any{
		"model": model,
		"messages": []any{
			map[string]any{"role": "user", "content": "ping"},
		},
		"max_tokens": 8,
	}
}

// TestChannel 向渠道发送一次最小对话请求验证连通性，返回耗时。
// 按渠道协议构建请求（openai_compat 直通，anthropic/gemini 经 canonical 转换），
// 管理端手工测试与自动禁用渠道的半开探测共用。
func TestChannel(ctx context.Context, client *http.Client, ch *store.Channel, apiKey, model string) (int64, error) {
	if model == "" {
		model = ch.TestModel
	}
	if model == "" {
		models := ch.ModelList()
		if len(models) == 0 {
			return 0, fmt.Errorf("渠道未配置模型，无法测试")
		}
		model = models[0]
	}
	body := pingBody(model)
	cd, err := newConduit(dsOpenAI, ch, body, model, false)
	if err != nil {
		return 0, fmt.Errorf("构建测试请求失败: %w", err)
	}
	upstreamModel := ch.MappedModel(model)
	req, err := cd.BuildRequest(ctx, ch, apiKey, upstreamModel)
	if err != nil {
		return 0, err
	}
	// 只记目标主机与路径，禁止记 apiKey 或完整 URL（密钥在请求头，但完整 URL 仍可能含敏感片段）。
	logger := obs.Logger(ctx)
	logger.Info("渠道测试调用上游", "channel_id", ch.ID, "base_url", ch.BaseURL, "path", req.URL.Path)
	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		logger.Warn("渠道测试上游网络错误", "channel_id", ch.ID, "base_url", ch.BaseURL, "error", err)
		return 0, fmt.Errorf("连接失败: %w", err)
	}
	defer resp.Body.Close()
	latency := time.Since(start).Milliseconds()
	if resp.StatusCode != http.StatusOK {
		excerpt, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		logger.Warn("渠道测试上游返回错误", "channel_id", ch.ID, "status", resp.StatusCode, "latency_ms", latency)
		return latency, fmt.Errorf("上游返回 HTTP %d: %s", resp.StatusCode, string(excerpt))
	}
	logger.Info("渠道测试上游响应", "channel_id", ch.ID, "status", resp.StatusCode, "latency_ms", latency)
	return latency, nil
}
