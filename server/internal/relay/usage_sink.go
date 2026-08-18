package relay

import (
	"context"
	"encoding/json"
	"math/rand"
	"net/http"
	"sync"
	"time"

	"github.com/liguopeng80/tokenzen-lite/server/internal/obs"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// UsageSink 用量日志与请求录制的统一落库入口（C2 设计 step 4）。
//
// 持有异步写入器（用量日志 + 录制文件）、录制目录、时钟，并把 finishLog
// 收口（埋点 + 异步入库 + 录制 flush）收敛到一处。Engine 不再直接持有
// logWriter/recWriter/RecordDir/Now 等写入状态与时钟字段（Engine 减 6 字段）。
//
// 设计要点：
//   - finishLog 是中继路径埋点与用量日志的唯一收口，放在 sink 才能保证
//     「指标与用量日志同口径」的不变式不依赖调用方约定。
//   - 写入器懒初始化（首次使用时构造），停机时 Close 刷盘。
//   - maybeNewRecorder 读 Settings 判定采样与开关，热路径上不录制时返回 nil（零分配）。
type UsageSink struct {
	usageLogs *store.UsageLogRepo // 用量日志仓储（构造 logWriter 用）
	settings  *store.SettingsRepo // 录制开关与采样率读取
	recordDir string              // 录制根目录，空表示禁用录制
	nowFn     func() time.Time    // 可注入时钟，nil 用 time.Now

	logOnce   sync.Once
	logWriter *usageLogWriter
	recOnce   sync.Once
	recWriter *recordingWriter
}

// NewUsageSink 创建用量日志/录制落库入口。nowFn 为 nil 时用 time.Now。
func NewUsageSink(usageLogs *store.UsageLogRepo, settings *store.SettingsRepo,
	recordDir string, nowFn func() time.Time) *UsageSink {
	return &UsageSink{
		usageLogs: usageLogs, settings: settings,
		recordDir: recordDir, nowFn: nowFn,
	}
}

// Now 返回当前时刻（可注入时钟，供时段倍率、录制时间戳、日志耗时等共用）。
func (s *UsageSink) Now() time.Time {
	if s.nowFn != nil {
		return s.nowFn()
	}
	return time.Now()
}

// logs 返回用量日志写入器（懒初始化，兼容测试以字面量构造 Engine 时 sink 未预置）。
func (s *UsageSink) logs() *usageLogWriter {
	s.logOnce.Do(func() { s.logWriter = newUsageLogWriter(s.usageLogs) })
	return s.logWriter
}

// recordings 返回录制写入器（懒初始化）；recordDir 空时返回 nil（禁用录制）。
func (s *UsageSink) recordings() *recordingWriter {
	if s.recordDir == "" {
		return nil
	}
	s.recOnce.Do(func() { s.recWriter = newRecordingWriter(s.recordDir) })
	return s.recWriter
}

// Close 停机前刷盘用量日志队列与录制队列；ctx 到期则放弃等待。
func (s *UsageSink) Close(ctx context.Context) {
	s.logs().close(ctx)
	if rw := s.recordings(); rw != nil {
		rw.close(ctx)
	}
}

// DroppedCount 返回用量日志累计丢弃条数（健康接口暴露，运维告警依据）。
func (s *UsageSink) DroppedCount() int64 {
	return s.logs().droppedCount()
}

// finishLog 补全耗时并交由有界队列异步落库（队列满时丢弃并计数告警），
// 同时把该次请求的终态计入指标。这里是全部中继路径的唯一收口，
// 埋点放在此处才能保证指标与用量日志的口径一致。
// rec 非 nil 时一并入队录制队列异步落盘。
func (s *UsageSink) finishLog(ctx context.Context, log *store.UsageLog, start time.Time, rec *recorder) {
	log.LatencyMS = time.Since(start).Milliseconds()
	obs.RecordRelayRequest(log.ModelName, string(log.Status), string(log.ErrorClass), log.LatencyMS)
	s.logs().enqueue(ctx, log)
	if rec != nil {
		rec.flush(s.Now(), log, s.recordings())
	}
}

// maybeNewRecorder 读 setting 判定是否录制；不录时返回 nil（热路径零分配）。
// 返回 nil 的条件：recordDir 空、record_enabled=false、采样未命中。
func (s *UsageSink) maybeNewRecorder(ctx context.Context, r *http.Request,
	body map[string]any, isStream bool) *recorder {
	if s.recordDir == "" || s.settings == nil {
		return nil
	}
	if !s.settings.GetBool(ctx, "record_enabled") {
		return nil
	}
	rate := s.settings.GetInt64(ctx, "record_sample_rate_permyriad")
	if rate <= 0 || rand.Int63n(10000) >= rate {
		return nil
	}
	maxBody := s.settings.GetInt64(ctx, "record_max_body_bytes")
	if maxBody <= 0 {
		maxBody = defaultMaxBodyBytes
	}
	maxStream := s.settings.GetInt64(ctx, "record_max_stream_bytes")
	if maxStream <= 0 {
		maxStream = defaultMaxStreamBytes
	}

	// 重新序列化请求体（已解码为 map）；序列化失败时记空。
	var rawBody []byte
	if body != nil {
		rawBody, _ = json.Marshal(body)
	}
	bodyTrunc := false
	if int64(len(rawBody)) > maxBody {
		rawBody = rawBody[:maxBody]
		bodyTrunc = true
	}

	return &recorder{
		requestID:      obs.RequestID(ctx),
		method:         r.Method,
		path:           r.URL.Path,
		query:          r.URL.RawQuery,
		reqHeaders:     redactHeaders(r.Header, redactRequestHeaderNames),
		reqBody:        rawBody,
		reqBodyLen:     int64(len(rawBody)),
		bodyTrunc:      bodyTrunc,
		clientIP:       clientIP(r),
		startedAt:      s.Now(),
		redactBody:     s.settings.GetBool(ctx, "record_redact_request_body"),
		isStream:       isStream,
		maxStreamBytes: maxStream,
	}
}
