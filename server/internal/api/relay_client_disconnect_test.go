package api

// 客户端提前断开场景（架构决策 2026-08-05 第 3 项）：
// 下游断连不取消上游请求，网关继续读完上游流，按上游返回的真实 usage 结算；
// 字节估算仅在上游不返回 usage 时作兜底。

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// streamChatRequest 以可取消的上下文发起流式对话请求（模拟可提前断开的客户端）。
func streamChatRequest(t *testing.T, baseURL, apiKey string) (*http.Response, context.CancelFunc) {
	t.Helper()
	payload, _ := json.Marshal(map[string]any{
		"model": "glm-5", "stream": true,
		"messages": []map[string]any{{"role": "user", "content": "hi"}},
	})
	reqCtx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(reqCtx, "POST",
		baseURL+"/v1/chat/completions", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		cancel()
		t.Fatalf("流式请求失败: %v", err)
	}
	return resp, cancel
}

// 客户端读到首帧后断开：网关继续读完上游流，按真实 usage 精确结算，不落入字节估算。
func TestRelayStreamClientDisconnectSettlesRealUsage(t *testing.T) {
	e := newTestEnv(t)

	// release 控制上游在客户端断开之后才发送剩余帧（含 usage），
	// 确保测试覆盖的是"下游已断、上游继续读"的窗口。
	release := make(chan struct{})
	upstreamDone := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(upstreamDone)
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		fmt.Fprint(w, "data: {\"id\":\"c\",\"model\":\"glm-5\",\"choices\":[{\"delta\":{\"content\":\"你\"}}]}\n\n")
		fl.Flush()
		<-release
		fmt.Fprint(w, "data: {\"id\":\"c\",\"model\":\"glm-5\",\"choices\":[{\"delta\":{\"content\":\"好\"}}]}\n\n")
		fl.Flush()
		fmt.Fprint(w, "data: {\"id\":\"c\",\"model\":\"glm-5\",\"choices\":[],\"usage\":{\"prompt_tokens\":200,\"completion_tokens\":50}}\n\n")
		fl.Flush()
		fmt.Fprint(w, "data: [DONE]\n\n")
		fl.Flush()
	}))
	defer upstream.Close()

	userID, key := e.seedRelayUser(t, "relay-disc", 1_000_000, nil)
	e.seedModel(t, "glm-5")
	e.seedChannel(t, "ch-disc", upstream.URL, 0, []string{"glm-5"}, nil)

	resp, cancel := streamChatRequest(t, e.srv.URL, key)
	if resp.StatusCode != 200 {
		t.Fatalf("流式请求应成功: %d", resp.StatusCode)
	}
	// 读到首个数据帧后模拟客户端提前断开（命令行工具中断输出的常见操作）
	sc := bufio.NewScanner(resp.Body)
	gotFirst := false
	for sc.Scan() {
		if strings.HasPrefix(sc.Text(), "data: ") {
			gotFirst = true
			break
		}
	}
	if !gotFirst {
		t.Fatal("未读到首个数据帧")
	}
	cancel()
	resp.Body.Close()
	// 等断连传播到网关的下游请求上下文，再放行上游剩余帧：
	// 若上游请求仍与下游上下文耦合，此时上游连接已被取消，读不到 usage。
	time.Sleep(200 * time.Millisecond)
	close(release)
	select {
	case <-upstreamDone:
	case <-time.After(5 * time.Second):
		t.Fatal("上游未完成发送（上游连接可能已随下游断连被取消）")
	}

	// 按上游真实 usage 结算：200×1 + 50×2 = 300 积分
	log := e.waitUsageLog(t, userID)
	if log.Status != domain.UsageSettled || log.CreditsCharged != 300 {
		t.Errorf("日志应 settled/300，实际 %s/%d", log.Status, log.CreditsCharged)
	}
	if log.UsageEstimated {
		t.Error("拿到真实 usage 后不应标记估算")
	}
	if log.PromptTokens != 200 || log.CompletionTokens != 50 {
		t.Errorf("token 记录应为上游真实用量 200/50，实际 %d/%d",
			log.PromptTokens, log.CompletionTokens)
	}
	if bal := e.userBalance(t, userID); bal != 1_000_000-300 {
		t.Errorf("期望余额 %d，实际 %d", 1_000_000-300, bal)
	}
	e.assertReconcile(t)
}

// asyncV1Request 在后台 goroutine 中以可取消上下文发起 /v1 请求，
// 用于模拟在响应到达前后任意时刻断开的客户端。done 返回客户端侧的请求错误。
func asyncV1Request(baseURL, apiKey, path string, payload []byte) (context.CancelFunc, chan error) {
	reqCtx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		req, _ := http.NewRequestWithContext(reqCtx, "POST",
			baseURL+path, bytes.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+apiKey)
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			resp.Body.Close()
		}
		done <- err
	}()
	return cancel, done
}

// I2: 流式断连 + 上游全程不返回 usage：字节估算兜底在断连场景同样生效，
// 结算为 settled + usage_estimated=true 且计费金额大于 0。
func TestRelayStreamClientDisconnectNoUsageFallsBackToEstimate(t *testing.T) {
	e := newTestEnv(t)

	clientGone := make(chan struct{})
	upstreamDone := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(upstreamDone)
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		fmt.Fprint(w, "data: {\"id\":\"c\",\"model\":\"glm-5\",\"choices\":[{\"delta\":{\"content\":\"first\"}}]}\n\n")
		fl.Flush()
		<-clientGone
		time.Sleep(200 * time.Millisecond)
		fmt.Fprint(w, "data: {\"id\":\"c\",\"model\":\"glm-5\",\"choices\":[{\"delta\":{\"content\":\"rest\"}}]}\n\n")
		fl.Flush()
		fmt.Fprint(w, "data: [DONE]\n\n")
		fl.Flush()
	}))
	defer upstream.Close()

	userID, key := e.seedRelayUser(t, "disc-nousage", 1_000_000, nil)
	e.seedModel(t, "glm-5")
	e.seedChannel(t, "ch-nousage", upstream.URL, 0, []string{"glm-5"}, nil)

	resp, cancel := streamChatRequest(t, e.srv.URL, key)
	sc := bufio.NewScanner(resp.Body)
	gotFirst := false
	for sc.Scan() {
		if strings.HasPrefix(sc.Text(), "data: ") {
			gotFirst = true
			break
		}
	}
	if !gotFirst {
		t.Fatal("未读到首个数据帧")
	}
	cancel()
	resp.Body.Close()
	close(clientGone)
	select {
	case <-upstreamDone:
	case <-time.After(5 * time.Second):
		t.Fatal("上游未完成发送")
	}

	log := e.waitUsageLog(t, userID)
	if log.Status != domain.UsageSettled {
		t.Fatalf("断连 + 无 usage 应仍以估算结算为 settled，实际 %s（错误: %s）",
			log.Status, log.ErrorMessage)
	}
	if !log.UsageEstimated {
		t.Error("上游无 usage 时应标记估算计费")
	}
	if log.CreditsCharged <= 0 {
		t.Errorf("估算计费金额应大于 0，实际 %d", log.CreditsCharged)
	}
	e.assertReconcile(t)
}

// I3: 客户端在上游返回首字节前断连：上游请求上下文已解耦（WithoutCancel 在
// e.Client.Do 阶段即生效），上游 handler 完整执行且按真实 usage 结算。
func TestRelayClientDisconnectBeforeFirstByteStillRelaysUpstream(t *testing.T) {
	e := newTestEnv(t)

	reqReceived := make(chan struct{})
	clientGone := make(chan struct{})
	upstreamCtxErr := make(chan error, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(reqReceived)
		<-clientGone
		time.Sleep(300 * time.Millisecond)
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		fmt.Fprint(w, "data: {\"id\":\"c\",\"model\":\"glm-5\",\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n")
		fl.Flush()
		fmt.Fprint(w, "data: {\"id\":\"c\",\"model\":\"glm-5\",\"choices\":[],\"usage\":{\"prompt_tokens\":100,\"completion_tokens\":25}}\n\n")
		fl.Flush()
		fmt.Fprint(w, "data: [DONE]\n\n")
		fl.Flush()
		// handler 走到此处说明未被中途取消；回传上下文状态供断言
		upstreamCtxErr <- r.Context().Err()
	}))
	defer upstream.Close()

	userID, key := e.seedRelayUser(t, "disc-prebyte", 1_000_000, nil)
	e.seedModel(t, "glm-5")
	e.seedChannel(t, "ch-prebyte", upstream.URL, 0, []string{"glm-5"}, nil)

	payload, _ := json.Marshal(map[string]any{
		"model": "glm-5", "stream": true,
		"messages": []map[string]any{{"role": "user", "content": "hi"}},
	})
	cancel, done := asyncV1Request(e.srv.URL, key, "/v1/chat/completions", payload)
	select {
	case <-reqReceived:
	case <-time.After(5 * time.Second):
		t.Fatal("上游未收到请求")
	}
	cancel() // 客户端在收到任何响应字节前断开
	if err := <-done; err == nil {
		t.Fatal("客户端请求应因取消而失败（断连前置条件不成立）")
	}
	close(clientGone)

	select {
	case ctxErr := <-upstreamCtxErr:
		if ctxErr != nil {
			t.Errorf("上游 handler 的请求上下文不应被取消，实际 %v", ctxErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("上游 handler 未完整执行（上游请求可能已随客户端断连被取消）")
	}

	// 按上游真实 usage 结算：100×1 + 25×2 = 150 积分
	log := e.waitUsageLog(t, userID)
	if log.Status != domain.UsageSettled || log.CreditsCharged != 150 {
		t.Errorf("日志应 settled/150，实际 %s/%d（错误: %s）",
			log.Status, log.CreditsCharged, log.ErrorMessage)
	}
	if log.UsageEstimated {
		t.Error("拿到真实 usage 后不应标记估算")
	}
	if log.PromptTokens != 100 || log.CompletionTokens != 25 {
		t.Errorf("token 记录应为 100/25，实际 %d/%d", log.PromptTokens, log.CompletionTokens)
	}
	e.assertReconcile(t)
}

// I4: 非流式请求客户端中途断连：jsonResponse 路径下游写失败被忽略，计费完整。
func TestRelayNonStreamClientDisconnectSettlesRealUsage(t *testing.T) {
	e := newTestEnv(t)

	reqReceived := make(chan struct{})
	clientGone := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(reqReceived)
		<-clientGone
		time.Sleep(200 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"x","model":"glm-5","choices":[{"message":{"content":"ok"}}],
			"usage":{"prompt_tokens":40,"completion_tokens":10}}`)
	}))
	defer upstream.Close()

	userID, key := e.seedRelayUser(t, "disc-json", 1_000_000, nil)
	e.seedModel(t, "glm-5")
	e.seedChannel(t, "ch-json", upstream.URL, 0, []string{"glm-5"}, nil)

	payload, _ := json.Marshal(map[string]any{
		"model":    "glm-5",
		"messages": []map[string]any{{"role": "user", "content": "hi"}},
	})
	cancel, done := asyncV1Request(e.srv.URL, key, "/v1/chat/completions", payload)
	select {
	case <-reqReceived:
	case <-time.After(5 * time.Second):
		t.Fatal("上游未收到请求")
	}
	cancel()
	<-done
	close(clientGone)

	// 按上游真实 usage 结算：40×1 + 10×2 = 60 积分
	log := e.waitUsageLog(t, userID)
	if log.Status != domain.UsageSettled || log.CreditsCharged != 60 {
		t.Fatalf("日志应 settled/60，实际 %s/%d（错误: %s）",
			log.Status, log.CreditsCharged, log.ErrorMessage)
	}
	if log.UsageEstimated {
		t.Error("拿到真实 usage 后不应标记估算")
	}
	if log.PromptTokens != 40 || log.CompletionTokens != 10 {
		t.Errorf("token 记录应为 40/10，实际 %d/%d", log.PromptTokens, log.CompletionTokens)
	}
	if bal := e.userBalance(t, userID); bal != 1_000_000-60 {
		t.Errorf("期望余额 %d，实际 %d", 1_000_000-60, bal)
	}
	e.assertReconcile(t)
}

// I5: 独立超时兜底——上游流中途停滞：UpstreamTimeout 到期终止读取而非无限挂起，
// 已读内容按字节估算结算。
func TestRelayUpstreamStallTerminatedByIndependentTimeout(t *testing.T) {
	e := newTestEnv(t)
	e.deps.Relay.UpstreamTimeout = 1 * time.Second

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		fmt.Fprint(w, "data: {\"id\":\"c\",\"model\":\"glm-5\",\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n")
		fl.Flush()
		// 挂起不再输出也不关闭，直至网关放弃（独立超时取消上游连接）
		<-r.Context().Done()
	}))
	defer upstream.Close()

	userID, key := e.seedRelayUser(t, "stall-user", 1_000_000, nil)
	e.seedModel(t, "glm-5")
	e.seedChannel(t, "ch-stall", upstream.URL, 0, []string{"glm-5"}, nil)

	start := time.Now()
	resp, cancel := streamChatRequest(t, e.srv.URL, key)
	defer cancel()
	buf := new(bytes.Buffer)
	_, _ = buf.ReadFrom(resp.Body) // 读到网关因超时终止流为止
	resp.Body.Close()
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("停滞流应在独立超时（1s）附近终止，实际耗时 %v", elapsed)
	}
	if !strings.Contains(buf.String(), "partial") {
		t.Errorf("超时前已到达的内容帧应已转发给客户端: %q", buf.String())
	}

	log := e.waitUsageLog(t, userID)
	if log.Status != domain.UsageSettled {
		t.Fatalf("超时中断后应按估算结算为 settled，实际 %s（错误: %s）",
			log.Status, log.ErrorMessage)
	}
	if !log.UsageEstimated {
		t.Error("上游停滞未给出 usage，应标记估算")
	}
	if log.CreditsCharged <= 0 {
		t.Errorf("估算计费金额应大于 0，实际 %d", log.CreditsCharged)
	}
	e.assertReconcile(t)
}

// I6: 客户端断连 + 上游彻底无响应：渠道尝试耗尽后走退款闭环，
// 解耦上下文不破坏失败退款（余额恢复、refund 流水、key 已用额度回落）。
func TestRelayClientDisconnectUpstreamUnresponsiveRefunds(t *testing.T) {
	e := newTestEnv(t)
	e.deps.Relay.UpstreamTimeout = 1 * time.Second

	var reqOnce sync.Once
	reqReceived := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqOnce.Do(func() { close(reqReceived) })
		// 必须先读完请求体：net/http 服务端只在请求体读尽后才启动后台读，
		// 未读尽时对端关闭连接不会取消 r.Context()，handler 将永久阻塞，
		// 进而使 httptest.Server.Close 在测试收尾时挂起。
		_, _ = io.Copy(io.Discard, r.Body)
		// 响应头之前挂起，直至网关超时放弃；上限兜底防止 handler 无法退出。
		select {
		case <-r.Context().Done():
		case <-time.After(10 * time.Second):
		}
	}))
	defer upstream.Close()

	userID, key := e.seedRelayUser(t, "dead-upstream", 1_000_000, nil)
	e.seedModel(t, "glm-5")
	e.seedChannel(t, "ch-dead", upstream.URL, 0, []string{"glm-5"}, nil)

	payload, _ := json.Marshal(map[string]any{
		"model": "glm-5", "stream": true,
		"messages": []map[string]any{{"role": "user", "content": "hi"}},
	})
	cancel, done := asyncV1Request(e.srv.URL, key, "/v1/chat/completions", payload)
	select {
	case <-reqReceived:
	case <-time.After(5 * time.Second):
		t.Fatal("上游未收到请求")
	}
	time.Sleep(100 * time.Millisecond)
	cancel() // 客户端在等待期间断连
	<-done

	log := e.waitUsageLog(t, userID)
	if log.Status != domain.UsageRefunded {
		t.Fatalf("渠道尝试耗尽后应退款，实际 %s（错误: %s）", log.Status, log.ErrorMessage)
	}
	var refunds int64
	if err := e.db.Model(&store.LedgerEntry{}).
		Where("user_id = ? AND entry_type = ?", userID, domain.LedgerRefund).
		Count(&refunds).Error; err != nil {
		t.Fatalf("查退款流水失败: %v", err)
	}
	if refunds != 1 {
		t.Errorf("应存在 1 条 refund 流水，实际 %d", refunds)
	}
	if bal := e.userBalance(t, userID); bal != 1_000_000 {
		t.Errorf("退款后余额应恢复 1000000，实际 %d", bal)
	}
	var k store.APIKey
	if err := e.db.Where("user_id = ?", userID).First(&k).Error; err != nil {
		t.Fatalf("查密钥失败: %v", err)
	}
	if k.CreditUsed != 0 {
		t.Errorf("退款后 key 已用额度应回落为 0，实际 %d", k.CreditUsed)
	}
	e.assertReconcile(t)
}

// I7: /v1/embeddings 客户端断连：extra_endpoints 的独立解耦上下文路径，
// 按上游真实 usage.prompt_tokens 结算。
func TestRelayEmbeddingsClientDisconnectSettlesRealUsage(t *testing.T) {
	e := newTestEnv(t)

	reqReceived := make(chan struct{})
	clientGone := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(reqReceived)
		<-clientGone
		time.Sleep(200 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"object":"list","model":"emb-up",
			"data":[{"object":"embedding","index":0,"embedding":[0.1,0.2]}],
			"usage":{"prompt_tokens":64}}`)
	}))
	defer upstream.Close()

	userID, key := e.seedRelayUser(t, "disc-emb", 1_000_000, nil)
	m := &store.Model{Name: "emb-disc-model", Modality: domain.ModalityEmbedding,
		BillingMode: domain.BillPerToken, Status: domain.ModelEnabled}
	if err := e.db.Create(m).Error; err != nil {
		t.Fatalf("建模型失败: %v", err)
	}
	if err := e.db.Create(&store.ModelPrice{ModelID: m.ID, InputPrice: 1_000_000}).Error; err != nil {
		t.Fatalf("建价格失败: %v", err)
	}
	e.seedChannel(t, "ch-emb-disc", upstream.URL, 0, []string{"emb-disc-model"}, nil)

	payload, _ := json.Marshal(map[string]any{
		"model": "emb-disc-model", "input": "hello world",
	})
	cancel, done := asyncV1Request(e.srv.URL, key, "/v1/embeddings", payload)
	select {
	case <-reqReceived:
	case <-time.After(5 * time.Second):
		t.Fatal("上游未收到请求")
	}
	cancel()
	<-done
	close(clientGone)

	// 按上游真实 usage 结算：64×1 = 64 积分
	log := e.waitUsageLog(t, userID)
	if log.Status != domain.UsageSettled || log.CreditsCharged != 64 {
		t.Fatalf("日志应 settled/64，实际 %s/%d（错误: %s）",
			log.Status, log.CreditsCharged, log.ErrorMessage)
	}
	if log.UsageEstimated {
		t.Error("拿到真实 usage 后不应标记估算")
	}
	if log.PromptTokens != 64 || log.CompletionTokens != 0 {
		t.Errorf("token 记录应为 64/0，实际 %d/%d", log.PromptTokens, log.CompletionTokens)
	}
	e.assertReconcile(t)
}
