package relay

import (
	"bufio"
	"bytes"
	"net/http"
	"time"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/obs"
	"github.com/liguopeng80/tokenzen-lite/server/internal/pricing"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
	"github.com/liguopeng80/tokenzen-lite/server/internal/strutil"
)

// SSE 缓冲：初始 64KB，单事件上限 4MB（超限判定为异常流，终止而非无限吃内存）。
const (
	sseInitialBuffer = 64 * 1024
	sseMaxEventSize  = 4 * 1024 * 1024
	ssePrefixData    = "data: "
	sseDoneMarker    = "[DONE]"
)

// streamResponse 流式代理：上游行 → conduitStream 帧 → 客户端，流结束后结算。
// 客户端断连时继续读完上游以拿到真实 usage 结算（架构决策 2026-08-05 第 3 项，
// 低并发场景成本可接受）：上游请求在 relayWithRetry 中经 Engine.upstreamContext
// 构建，不随下游连接取消，读取时长受该上下文的独立超时约束。
// 字节估算仅在上游不返回 usage 时作兜底。
func (e *Engine) streamResponse(w http.ResponseWriter, r *http.Request, resp *http.Response,
	cd conduit, session *BillingSession, price pricing.Price, multiplier int,
	start time.Time, log *store.UsageLog, writeErr errWriter, rec *recorder) {

	ctx := r.Context()
	defer resp.Body.Close()

	flusher, ok := w.(http.Flusher)
	if !ok {
		_ = session.Refund(ctx)
		writeErr(w, http.StatusInternalServerError, "internal_error", "服务不支持流式输出")
		log.Status = domain.UsageRefunded
		e.usage().finishLog(ctx, log, start, rec)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	rec.setResponseMeta(http.StatusOK, resp.Header)

	cs := cd.NewStream()
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, sseInitialBuffer), sseMaxEventSize)

	clientGone := false
	outputBytes := 0
	doneSent := false

	writeFrames := func(frames [][]byte) {
		for _, f := range frames {
			outputBytes += len(f)
			if clientGone {
				continue
			}
			if _, err := w.Write(f); err != nil {
				clientGone = true
				obs.Logger(ctx).Warn("客户端断连，继续读取上游以完成计费")
				continue
			}
			flusher.Flush()
		}
	}

	for scanner.Scan() {
		line := scanner.Bytes()
		// 累计上游原始行到录制器（不影响转发；超上限只置 truncated）
		rec.appendStreamLine(line)
		if !bytes.HasPrefix(line, []byte(ssePrefixData)) {
			// event:/空行/注释行：由 conduitStream 重建输出帧，此处不透传
			continue
		}
		payload := bytes.TrimSpace(bytes.TrimPrefix(line, []byte(ssePrefixData)))
		if bytes.Equal(payload, []byte(sseDoneMarker)) {
			writeFrames(cs.ProcessDone())
			doneSent = true
			continue
		}
		writeFrames(cs.ProcessPayload(payload))
	}
	if err := scanner.Err(); err != nil {
		obs.Logger(ctx).Warn("上游流读取中断", "error", err)
		// 已产生的 token 照常结算，但要在用量日志里留下中断标记：否则这条调用
		// 与正常结束的调用在日志里完全一样，员工反映「回答被截断」时无从确认。
		log.ErrorClass = domain.ErrClassStreamAborted
		log.ErrorMessage = strutil.Truncate("上游流读取中断："+err.Error(), 500)
	}
	if !doneSent {
		// 上游未发 [DONE]（Anthropic/Gemini 风格）：触发收尾帧
		writeFrames(cs.ProcessDone())
	}

	usage, usageFound := cs.Usage()
	if !usageFound {
		usage.BaseInput = estimateTokensFromText(contentLengthOfBody(r))
		usage.Output = estimateTokensFromText(outputBytes)
		usage.Estimated = true
		obs.Logger(ctx).Warn("流式响应缺少 usage，按字节估算计费",
			"output_bytes", outputBytes, "channel_id", log.ChannelID)
	}

	final := pricing.CalcTokenCredits(usage, price, multiplier)
	e.settleAndLog(ctx, session, log, usage, final, price, multiplier, start, rec)
}
