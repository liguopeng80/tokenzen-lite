package relay

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// canonicalConduit.NewStream 在 codecFor 缺注册时 panic：NewStream 接口不返错，
// 缺注册属程序员错误（新增协议常量时漏补 init 注册）。HTTP 层 chimw.Recoverer 兜底为 500。
// 这是 P2-4 三层保障的运行期 fail-loud——绝不再静默回落 OpenAI 解码器产出乱码。
// 配合 TestCodecRegistryCoverage（启动期断言全注册）与 TestCodecForUnknownProtocolError
// （codecFor 返错路径），三层共同消除 P2-4。
func TestCanonicalConduitNewStreamPanicsOnUnknownProtocol(t *testing.T) {
	c := &canonicalConduit{
		up: domain.ChannelProtocol("bogus_protocol"),
		ds: dsOpenAI,
	}

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("未知协议应 panic，实际正常返回")
		}
		// panic 值可能是 error 或 string——统一以字符串形式校验。
		msg := fmt.Sprintf("%v", r)
		if !strings.Contains(msg, "bogus_protocol") {
			t.Errorf("panic 消息应含协议名 bogus_protocol，实际 %q", msg)
		}
		if !strings.Contains(msg, "未注册 codec") {
			t.Errorf("panic 消息应说明 codec 未注册，实际 %q", msg)
		}
	}()

	_ = c.NewStream()
	t.Error("NewStream 应 panic，未到达此行")
}

// TestCodecForUnknownProtocolError 验证 codecFor 对未知协议返错（非 panic、非静默）。
// 走 codecFor 路径的两个方法（BuildRequest / TransformResponse）应把 error 透传给调用方，
// 由 relayWithRetry 归为不可重试类返回客户端，而不是乱码或 panic。
func TestCodecForUnknownProtocolError(t *testing.T) {
	c := &canonicalConduit{
		up:          domain.ChannelProtocol("bogus_protocol"),
		ds:          dsOpenAI,
		canon:       &CanonRequest{Model: "m", MaxTokens: 1},
		publicModel: "m",
	}
	ch := &store.Channel{BaseURL: "http://example.invalid"}

	_, err := c.BuildRequest(context.Background(), ch, "key", "up-model")
	if err == nil {
		t.Fatal("未知协议 BuildRequest 应返错，实际 nil")
	}
	if !strings.Contains(err.Error(), "bogus_protocol") {
		t.Errorf("BuildRequest 错误应含协议名，实际 %q", err.Error())
	}

	_, _, _, err = c.TransformResponse([]byte(`{}`))
	if err == nil {
		t.Fatal("未知协议 TransformResponse 应返错，实际 nil")
	}
	if !strings.Contains(err.Error(), "bogus_protocol") {
		t.Errorf("TransformResponse 错误应含协议名，实际 %q", err.Error())
	}
}

// TestCodecRegistryCoverage 启动期覆盖检查：domain.AllProtocols() 的每个协议
// 都必须在 codecRegistry 注册。新增协议常量时遗漏 init 注册会立即使此测试红——
// 从源头消除 P2-4（新增协议静默回落乱码）。
func TestCodecRegistryCoverage(t *testing.T) {
	for _, p := range domain.AllProtocols() {
		c, err := codecFor(p)
		if err != nil {
			t.Errorf("协议 %q 未注册 codec: %v", p, err)
			continue
		}
		if c == nil {
			t.Errorf("协议 %q 注册了 nil codec", p)
		}
	}
}
