package relay

// /v1/embeddings 与 /v1/images/generations：仅走 openai_compat 渠道的简化中继。

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/obs"
	"github.com/liguopeng80/tokenzen-lite/server/internal/pricing"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
	"github.com/liguopeng80/tokenzen-lite/server/internal/strutil"
)

// HandleEmbeddings 处理 POST /v1/embeddings（按 input token 计费）。
// 下游入口是 /v1/embeddings；上游 path 仅追加 /embeddings（base_url 已含版本根）。
func (e *Engine) HandleEmbeddings(w http.ResponseWriter, r *http.Request, ident Identity) {
	e.handleSimpleRelay(w, r, ident, "/embeddings", domain.ModalityEmbedding, false)
}

// HandleImages 处理 POST /v1/images/generations（按次计费）。
// 下游入口是 /v1/images/generations；上游 path 仅追加 /images/generations（base_url 已含版本根）。
func (e *Engine) HandleImages(w http.ResponseWriter, r *http.Request, ident Identity) {
	e.handleSimpleRelay(w, r, ident, "/images/generations", domain.ModalityImage, true)
}

// handleSimpleRelay 非流式、OpenAI 协议直通的简化中继（embeddings/images 共用）。
// modality 是本端点期望的模型形态；perCall 为 true 时按次计费，否则按 token 计费。
func (e *Engine) handleSimpleRelay(w http.ResponseWriter, r *http.Request, ident Identity,
	path string, modality domain.Modality, perCall bool) {

	ctx := r.Context()
	start := e.now()
	writeErr := WriteOpenAIError

	r.Body = http.MaxBytesReader(w, r.Body, maxRelayBodyBytes)
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_request_error", "请求体不是合法 JSON")
		return
	}
	publicModel, _ := body["model"].(string)
	if publicModel == "" {
		writeErr(w, http.StatusBadRequest, "invalid_request_error", "缺少 model 字段")
		return
	}
	wantBilling := domain.BillPerToken
	if perCall {
		wantBilling = domain.BillPerCall
	}
	_, price, multiplier, ok := e.prepareModel(ctx, w, ident, publicModel, modality, wantBilling, writeErr)
	if !ok {
		return
	}

	// 预扣：按次计费用 n×单价；token 计费按输入长度估算
	callCount := int64(1)
	if n, ok := body["n"].(float64); ok && n > 0 {
		callCount = int64(n)
	}
	var precharge domain.Credits
	if perCall {
		if price.PerCallPrice <= 0 {
			writeErr(w, http.StatusServiceUnavailable, "model_not_priced", "模型未配置按次单价")
			return
		}
		precharge = pricing.CalcPerCallCredits(callCount, price, multiplier)
	} else {
		est := estimateEmbeddingInputTokens(body)
		minTokens := e.Settings.GetInt64(ctx, "precharge_min_tokens")
		if est < minTokens {
			est = minTokens
		}
		precharge = pricing.CalcTokenCredits(domain.NormalizedUsage{BaseInput: est}, price, multiplier)
	}

	requestID := obs.RequestID(ctx)
	session := NewBillingSession(e.DB, e.Billing, requestID, ident.Key, ident.User.DailySpendLimit)
	if err := e.checkDailySpendLimit(ctx, ident, precharge); err != nil {
		e.rejectPrecharge(w, ctx, ident, publicModel, false, start, multiplier, err, writeErr)
		return
	}
	if err := session.Precharge(ctx, precharge); err != nil {
		e.rejectPrecharge(w, ctx, ident, publicModel, false, start, multiplier, err, writeErr)
		return
	}
	defer session.EnsureFinal(ctx) // 退款写入在 Refund 内部脱离取消并施加截止时间

	log := &store.UsageLog{
		RequestID: requestID, UserID: ident.User.ID, APIKeyID: ident.Key.ID,
		DepartmentID:  ident.DepartmentID(),
		ProjectID:     ident.ProjectID(),
		IntegrationID: ident.IntegrationID(),
		ModelName:     publicModel, PeakMultiplierPercent: multiplier,
		CreditsPrecharged: precharge, ClientIP: clientIP(r),
	}
	rec := e.usage().maybeNewRecorder(ctx, r, body, false)

	channels, err := e.Channels.ListEnabledForModel(ctx, publicModel)
	if err != nil {
		channels = nil
	}
	// provider 前缀路由：收窄到同 provider 渠道（为零值时直返原切片，行为不变）。
	channels = filterByProvider(channels, ident.Provider)
	// 支持矩阵（docs/glossary.md ChannelProtocol 节）：向量/图像端点仅支持 openai_compat 协议渠道
	var candidates []store.Channel
	for _, ch := range channels {
		if domain.ProtocolSupportsModality(ch.Protocol, modality) {
			candidates = append(candidates, ch)
		}
	}
	if len(candidates) == 0 {
		_ = session.Refund(ctx)
		if len(channels) > 0 {
			// 有启用渠道但协议均不支持当前端点，与完全无渠道区分
			writeErr(w, http.StatusServiceUnavailable, "channel_protocol_unsupported",
				"该模型的启用渠道协议均不支持当前端点（仅支持 OpenAI 兼容协议渠道）")
			log.ErrorMessage = "启用渠道的协议均不支持当前端点"
		} else {
			writeErr(w, http.StatusServiceUnavailable, "no_channel", "没有可用渠道承载该模型")
			log.ErrorMessage = "无可用渠道"
		}
		log.Status = domain.UsageRefunded
		e.usage().finishLog(ctx, log, start, rec)
		return
	}

	maxRetries := int(e.Settings.GetInt64(ctx, "relay_max_retries"))
	tried := map[int64]bool{}
	var lastErr relayError
	// 上游请求上下文与下游连接解耦并附独立超时（与 relayWithRetry 同口径，
	// 架构决策 2026-08-05 第 3 项）：客户端断连不取消上游请求，仍按真实响应结算。
	upCtx, cancelUpstream := e.upstreamContext(ctx)
	defer cancelUpstream()
	for attempt := 0; attempt < maxRetries; attempt++ {
		ch := SelectChannel(candidates, tried)
		if ch == nil {
			break
		}
		tried[ch.ID] = true
		upstreamModel := ch.MappedModel(publicModel)
		apiKey, err := e.Secrets.Decrypt(ch.APIKeyEncrypted)
		if err != nil {
			lastErr = relayError{http.StatusServiceUnavailable, "channel_error", "渠道配置异常"}
			continue
		}
		req, err := buildOpenAIRequest(upCtx, ch, apiKey, copyBody(body), upstreamModel, false, path)
		if err != nil {
			lastErr = relayError{http.StatusInternalServerError, "internal_error", "构建上游请求失败"}
			continue
		}
		resp, err := e.Client.Do(req)
		if err != nil {
			lastErr = relayError{http.StatusBadGateway, "upstream_error", "上游连接失败"}
			log.ErrorClass = domain.ErrClassTransient
			continue
		}
		if resp.StatusCode != http.StatusOK {
			errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
			resp.Body.Close()
			class := ClassifyUpstreamError(resp.StatusCode, errBody)
			log.ErrorClass = class
			e.health().NoteFailure(ctx, ch, class, resp.StatusCode)
			if !ShouldRetryNextChannel(class) {
				_ = session.Refund(ctx)
				passUpstreamError(w, resp.StatusCode, errBody, dsOpenAI)
				log.Status = domain.UsageRefunded
				log.ChannelID = ch.ID
				log.ErrorMessage = strutil.Truncate(string(errBody), 500)
				e.usage().finishLog(ctx, log, start, rec)
				return
			}
			lastErr = relayError{http.StatusBadGateway, "upstream_error",
				fmt.Sprintf("上游错误（HTTP %d）", resp.StatusCode)}
			continue
		}

		raw, err := io.ReadAll(io.LimitReader(resp.Body, maxRelayBodyBytes))
		resp.Body.Close()
		if err != nil {
			_ = session.Refund(ctx)
			writeErr(w, http.StatusBadGateway, "upstream_error", "读取上游响应失败")
			log.Status = domain.UsageRefunded
			e.usage().finishLog(ctx, log, start, rec)
			return
		}
		e.health().NoteSuccess(ch.ID)
		log.ChannelID = ch.ID
		log.UpstreamModel = upstreamModel
		log.Protocol = ch.Protocol
		log.FirstByteMS = time.Since(start).Milliseconds()

		out, usage, usageFound, err := (&openaiPassthrough{publicModel: publicModel}).TransformResponse(raw)
		if err != nil {
			// embeddings 响应也是 JSON（含 usage.prompt_tokens），解析失败按估算
			out = raw
			usageFound = false
		}
		var final domain.Credits
		if perCall {
			// 按实际返回张数结算：取请求张数与响应图片数的较小值计次；
			// 上游少返图（内容过滤、单次上限）时按实际张数扣费，差异记入日志备注。
			billedCalls := callCount
			if returned, counted := countImageItems(raw); counted && returned < billedCalls {
				log.ErrorMessage = fmt.Sprintf("上游实际返回 %d 张，少于请求的 %d 张，按实际张数计费", returned, callCount)
				billedCalls = returned
			}
			usage = domain.NormalizedUsage{CallCount: billedCalls, Semantic: domain.SemanticOpenAI}
			final = pricing.CalcPerCallCredits(billedCalls, price, multiplier)
		} else {
			if !usageFound {
				usage.BaseInput = estimateEmbeddingInputTokens(body)
				usage.Estimated = true
			}
			usage.Output = 0 // embeddings 无输出 token
			final = pricing.CalcTokenCredits(usage, price, multiplier)
		}
		rec.setResponseMeta(http.StatusOK, resp.Header)
		rec.captureResp(raw, out)
		e.settleAndLog(ctx, session, log, usage, final, price, multiplier, start, rec)
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(out)
		return
	}

	_ = session.Refund(ctx)
	if lastErr.status == 0 {
		lastErr = relayError{http.StatusServiceUnavailable, "no_channel", "没有可用渠道"}
	}
	writeErr(w, lastErr.status, lastErr.code, lastErr.message)
	log.Status = domain.UsageRefunded
	log.ErrorMessage = lastErr.message
	e.usage().finishLog(ctx, log, start, rec)
}

// countImageItems 解析图像生成响应中 data 数组的长度（实际返回张数）。
// 响应不含 data 数组时返回 counted=false，调用方回退到请求张数计费。
func countImageItems(raw []byte) (count int64, counted bool) {
	var resp struct {
		Data []json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil || resp.Data == nil {
		return 0, false
	}
	return int64(len(resp.Data)), true
}

// estimateEmbeddingInputTokens 估算 embeddings 输入 token。
func estimateEmbeddingInputTokens(body map[string]any) int64 {
	total := 0
	switch input := body["input"].(type) {
	case string:
		total = len(input)
	case []any:
		for _, item := range input {
			if s, ok := item.(string); ok {
				total += len(s)
			}
		}
	}
	return estimateTokensFromText(total)
}
