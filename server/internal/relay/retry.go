package relay

// 渠道重试循环：relayWithRetry（pipeline.go）的逐渠道尝试子函数。
// 拆自 relayWithRetry 以满足 M1（函数 ≤50 行）——relayWithRetry 保留循环骨架
// 与亲和指标计数，单次渠道尝试「解密 → conduit → 发请求 → 处理响应/错误」
// 由本文件的 tryChannel 承载。

import (
	"context"
	"errors"
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

// relayCtx 承载一次 relayWithRetry 调用中跨渠道重试不变的上下文。
// tryChannel 接收它 + 当前选中的渠道，做一次渠道尝试；把上下文打包收敛签名，
// 避免逐渠道重试的循环体塌缩成超长函数（M1 拆分）。
type relayCtx struct {
	ctx         context.Context // 请求上下文（日志、退款）
	upCtx       context.Context // 上游请求上下文（与下游断连解耦、带独立超时）
	w           http.ResponseWriter
	r           *http.Request
	ds          downstream
	body        map[string]any
	publicModel string
	stream      bool
	session     *BillingSession
	price       pricing.Price
	multiplier  int
	start       time.Time
	log         *store.UsageLog
	writeErr    errWriter
	rec         *recorder
	selector    *ChannelSelector
	affinityKey string
	affinitySrc affinitySource
}

// tryChannel 在单次渠道尝试中执行「解密密钥 → 构建 conduit → 发请求 → 处理响应/错误」。
//
// 返回 (done, lastErr)：
//   - done=true 表示请求已终结（成功输出响应，或已写出终态错误并落日志），调用方应直接 return；
//   - done=false 表示本次渠道失败、lastErr 携带错误，调用方可换下一个渠道重试。
//
// 行为与原 relayWithRetry 循环体内联实现完全等价（纯结构重构，控制流不变）。
// 调用方负责 selectWithAffinity 选渠道、亲和指标计数、tried 预标记。
func (e *Engine) tryChannel(a *relayCtx, ch *store.Channel, affOutcome affinityOutcome,
	attempt int) (done bool, lastErr relayError) {

	upstreamModel := ch.MappedModel(a.publicModel)
	apiKey, err := e.Secrets.Decrypt(ch.APIKeyEncrypted)
	if err != nil {
		obs.Logger(a.ctx).Error("渠道密钥解密失败", "channel_id", ch.ID, "error", err)
		return false, relayError{http.StatusServiceUnavailable, "channel_error", "渠道配置异常"}
	}
	cd, err := newConduit(a.ds, ch, a.body, a.publicModel, a.stream)
	if err != nil {
		_ = a.session.Refund(a.ctx)
		a.writeErr(a.w, http.StatusBadRequest, "invalid_request_error", err.Error())
		a.log.Status = domain.UsageRefunded
		a.log.ErrorMessage = err.Error()
		e.usage().finishLog(a.ctx, a.log, a.start, a.rec)
		return true, relayError{}
	}
	req, err := cd.BuildRequest(a.upCtx, ch, apiKey, upstreamModel)
	if err != nil {
		// ErrUnsupportedFeature：跨协议路由下目标上游协议无法表达请求字段（如
		// logprobs→Anthropic、top_k→OpenAI chat），属请求语义错误，400 返回且不重试。
		if errors.Is(err, ErrUnsupportedFeature) {
			_ = a.session.Refund(a.ctx)
			a.writeErr(a.w, http.StatusBadRequest, domain.ErrCodeUnsupportedFeature, err.Error())
			a.log.Status = domain.UsageRefunded
			a.log.ErrorMessage = err.Error()
			e.usage().finishLog(a.ctx, a.log, a.start, a.rec)
			return true, relayError{}
		}
		return false, relayError{http.StatusInternalServerError, "internal_error", "构建上游请求失败"}
	}

	obs.Logger(a.ctx).Info("转发上游请求",
		"channel_id", ch.ID, "upstream_model", upstreamModel,
		"protocol", ch.Protocol, "stream", a.stream, "attempt", attempt+1,
		"affinity_source", string(a.affinitySrc), "affinity_hit", affOutcome == affinityHit)
	resp, err := e.Client.Do(req)
	if err != nil {
		obs.Logger(a.ctx).Warn("上游网络错误", "channel_id", ch.ID, "error", err)
		a.log.ErrorClass = domain.ErrClassTransient
		return false, relayError{http.StatusBadGateway, "upstream_error", "上游连接失败"}
	}

	if resp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		resp.Body.Close()
		class := ClassifyUpstreamError(resp.StatusCode, errBody)
		obs.Logger(a.ctx).Warn("上游返回错误",
			"channel_id", ch.ID, "status", resp.StatusCode, "class", class,
			"body_excerpt", strutil.Truncate(string(errBody), 300))
		a.log.ErrorClass = class
		e.health().NoteFailure(a.ctx, ch, class, resp.StatusCode)
		if !ShouldRetryNextChannel(class) {
			_ = a.session.Refund(a.ctx)
			passUpstreamError(a.w, resp.StatusCode, errBody, a.ds)
			a.log.Status = domain.UsageRefunded
			a.log.ChannelID = ch.ID
			a.log.ErrorMessage = strutil.Truncate(string(errBody), 500)
			e.usage().finishLog(a.ctx, a.log, a.start, a.rec)
			return true, relayError{}
		}
		return false, relayError{http.StatusBadGateway, "upstream_error",
			fmt.Sprintf("上游错误（HTTP %d）", resp.StatusCode)}
	}

	// 成功拿到 200：清零连续失败计数，进入计费闭环，不再重试。
	// 亲和重绑（含首次绑定）：把当前亲和键绑到成功渠道，续期 TTL。
	// 漂移场景下此处即为「重绑到新渠道」的落点；首次（miss）此处建立绑定。
	e.health().NoteSuccess(ch.ID)
	a.selector.Bind(a.publicModel, a.affinityKey, ch.ID)
	a.log.ChannelID = ch.ID
	a.log.UpstreamModel = upstreamModel
	a.log.Protocol = ch.Protocol
	a.log.FirstByteMS = time.Since(a.start).Milliseconds()
	if a.stream {
		e.streamResponse(a.w, a.r, resp, cd, a.session, a.price, a.multiplier, a.start, a.log, a.writeErr, a.rec)
	} else {
		e.jsonResponse(a.w, a.r, resp, cd, a.session, a.price, a.multiplier, a.start, a.log, a.writeErr, a.rec)
	}
	return true, relayError{}
}
