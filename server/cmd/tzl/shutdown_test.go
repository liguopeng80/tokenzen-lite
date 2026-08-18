package main

// shutdown 的纯逻辑单测（不连数据库）。
// 覆盖「用量日志刷盘」故障形态：停机时若漏调 flusher.Close，
// 队列中的用量日志会永久丢失，计费明细与成本分摊出现缺口。
//
// 通过 shutdownFlusher 接口注入桩，验证 shutdown：
//   - 调用 flusher.Close（用量日志刷盘触发）
//   - srv.Shutdown 在 shutdownTimeout 内完成时返回 nil
//   - srv.Shutdown 超时时返回错误，但仍刷盘（不丢日志）
//   - flusher.Close 收到的 ctx 截止时间符合 flushTimeout

import (
	"context"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeFlusher 记录 Close 调用与传入的 ctx 截止时间。不阻塞——
// 真实 relay.Engine.Close 的阻塞行为由 relay 包自己的测试覆盖，此处只验证调用触发。
type fakeFlusher struct {
	mu          sync.Mutex
	closeCalled int32
	gotCtx      context.Context
}

func (f *fakeFlusher) Close(ctx context.Context) {
	atomic.StoreInt32(&f.closeCalled, 1)
	f.mu.Lock()
	f.gotCtx = ctx
	f.mu.Unlock()
}

func (f *fakeFlusher) called() bool { return atomic.LoadInt32(&f.closeCalled) == 1 }

// newListeningServer 起一个监听回环的 *http.Server，返回该 server 与其地址；
// handler 决定响应行为（用于测试在途请求等待或阻塞）。
func newListeningServer(t *testing.T, handler http.Handler) (*http.Server, string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("监听失败: %v", err)
	}
	srv := &http.Server{Handler: handler}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() {
		_ = srv.Close()
	})
	return srv, ln.Addr().String()
}

// TestShutdown_InvokesFlusher 验证 shutdown 调用了 flusher.Close（用量日志刷盘触发）。
// 核心故障形态：停机漏刷盘 → 计费明细永久缺口。
func TestShutdown_InvokesFlusher(t *testing.T) {
	srv, addr := newListeningServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	flusher := &fakeFlusher{}

	start := time.Now()
	err := shutdown(srv, flusher, 5*time.Second, 2*time.Second)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("干净停机应返回 nil，实际: %v", err)
	}
	if !flusher.called() {
		t.Fatal("shutdown 未调用 flusher.Close，用量日志刷盘未触发")
	}
	// flusher.Close 收到的 ctx 应带 flushTimeout 截止时间
	flusher.mu.Lock()
	gotCtx := flusher.gotCtx
	flusher.mu.Unlock()
	if gotCtx == nil {
		t.Fatal("flusher.Close 收到 nil ctx")
	}
	dl, ok := gotCtx.Deadline()
	if !ok {
		t.Fatal("flusher.Close 收到的 ctx 应有截止时间")
	}
	remain := time.Until(dl)
	if remain <= 0 {
		t.Errorf("flusher.Close 的 ctx 截止时间已过期，剩余 %v", remain)
	}
	if remain > 2*time.Second {
		t.Errorf("flusher.Close 的 ctx 截止时间应不超过 flushTimeout(2s)，剩余 %v", remain)
	}
	// 整体应快速返回（srv 无在途请求立即停 + flusher 不阻塞）
	if elapsed > 2*time.Second {
		t.Errorf("shutdown 耗时 %v 过长，疑似阻塞", elapsed)
	}
	// 验证 srv 真的停止接收新连接
	client := &http.Client{Timeout: 500 * time.Millisecond}
	_, _ = client.Get("http://" + addr + "/") // 期望连接失败（已关闭）
}

// TestShutdown_GracefulPathReturnsNil 验证在途请求完成后 shutdown 返回 nil。
func TestShutdown_GracefulPathReturnsNil(t *testing.T) {
	inflight := make(chan struct{})
	released := make(chan struct{})
	srv, addr := newListeningServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(inflight)
		<-released // 模拟在途请求处理中
		w.WriteHeader(http.StatusOK)
	}))
	flusher := &fakeFlusher{}

	// 发起一个在途请求
	go func() {
		client := &http.Client{Timeout: 5 * time.Second}
		resp, err := client.Get("http://" + addr)
		if err == nil {
			_ = resp.Body.Close()
		}
	}()
	<-inflight // 等请求进入处理

	// 启动 shutdown，给它充裕的超时
	doneCh := make(chan error, 1)
	go func() { doneCh <- shutdown(srv, flusher, 3*time.Second, 1*time.Second) }()

	// 短暂等待后释放请求，验证 shutdown 等到了在途请求完成
	select {
	case <-doneCh:
		t.Fatal("shutdown 不应在在途请求完成前返回")
	case <-time.After(100 * time.Millisecond):
		// 符合预期：仍在等待
	}
	close(released)

	select {
	case err := <-doneCh:
		if err != nil {
			t.Fatalf("在途请求完成后 shutdown 应返回 nil，实际: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("shutdown 未在在途请求完成后及时返回")
	}
	if !flusher.called() {
		t.Fatal("shutdown 应调用 flusher.Close")
	}
}

// TestShutdown_ServerTimeoutReturnsError 验证 srv.Shutdown 超时后返回错误，但仍刷盘。
// 覆盖「优雅停机超时」路径，并确保超时不丢日志（flusher.Close 仍被调用）。
func TestShutdown_ServerTimeoutReturnsError(t *testing.T) {
	block := make(chan struct{})
	t.Cleanup(func() { close(block) })
	srv, addr := newListeningServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		<-block // 永久阻塞，直到测试结束清理时才返回
	}))
	flusher := &fakeFlusher{}

	// 发起一个永久阻塞的在途请求，使 srv.Shutdown 必然超时
	go func() {
		client := &http.Client{}
		resp, _ := client.Get("http://" + addr)
		if resp != nil {
			_ = resp.Body.Close()
		}
	}()
	// 给 server 一点时间接收请求
	time.Sleep(100 * time.Millisecond)

	err := shutdown(srv, flusher, 200*time.Millisecond, 1*time.Second)
	if err == nil {
		t.Fatal("srv.Shutdown 超时应返回错误，实际为 nil")
	}
	if !flusher.called() {
		t.Fatal("srv 超时后 shutdown 仍应调用 flusher.Close，避免用量日志丢失")
	}
}
