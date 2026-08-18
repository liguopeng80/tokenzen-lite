package relay

// 请求录制：把客户端侧的完整请求/响应（含 SSE 全流）异步落盘为本地 JSON 文件，
// 输出结构与 llm-proxy 的 testcase 外层同构，便于复用同一回放测试集。
//
// 设计要点（见 plans/cryptic-leaping-gadget.md）：
//   - 默认全关（record_enabled=false + TZL_RECORD_DIR 空双保险）
//   - 只录客户端侧请求（handler 入口的 body 与 r.Header），不录上游请求
//   - 强制脱敏敏感头（Authorization/X-API-Key/Cookie/Proxy-Authorization 与 Set-Cookie/WWW-Authenticate）
//   - 异步有界队列落盘，队列满丢弃计数，不阻塞转发
//   - recorder 所有方法在 receiver 为 nil 时 no-op，热路径零额外开销

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/liguopeng80/tokenzen-lite/server/internal/obs"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// 录制相关常量。
const (
	recordingQueueSize    = 4096
	recordingFilePerm     = 0o600
	recordingDirPerm      = 0o700
	recordingSubDir       = "recordings"
	defaultMaxBodyBytes   = 2 << 20 // 2 MiB
	defaultMaxStreamBytes = 4 << 20 // 4 MiB
	redactedValue         = "********"
)

// redactRequestHeaderNames 强制脱敏的请求头（小写比较）。
var redactRequestHeaderNames = map[string]bool{
	"authorization":       true,
	"x-api-key":           true,
	"cookie":              true,
	"proxy-authorization": true,
}

// redactResponseHeaderNames 强制脱敏的响应头（小写比较）。
var redactResponseHeaderNames = map[string]bool{
	"set-cookie":       true,
	"www-authenticate": true,
}

// recorder 一次请求的录制器。所有方法在 receiver 为 nil 时 no-op。
type recorder struct {
	// 请求侧（构造时捕获）
	requestID  string
	method     string
	path       string
	query      string
	reqHeaders map[string]string
	reqBody    []byte // 已截断到 maxBodyBytes
	reqBodyLen int64  // body 字节数（截断后）
	bodyTrunc  bool   // 请求体被截断
	clientIP   string
	startedAt  time.Time
	redactBody bool

	// 响应侧（转发过程中填）
	statusCode  int
	respHeaders map[string]string
	rawBody     []byte // 上游原始响应体（非流式）
	outBody     []byte // 下游改写响应体（非流式）
	streamBuf   []byte // SSE 全流累计（流式）
	streamTrunc bool   // 流式累计超上限
	isStream    bool

	// 配置（构造时快照）
	maxStreamBytes int64

	// 业务元数据（flush 时从 UsageLog 抄）
	finishedAt time.Time
	usageLog   *store.UsageLog
}

// captureResp 记录非流式响应的上游原始体（raw）与下游改写体（out）。
func (rec *recorder) captureResp(raw, out []byte) {
	if rec == nil {
		return
	}
	rec.rawBody = raw
	rec.outBody = out
}

// setResponseMeta 记录响应状态码与响应头（脱敏后）。
func (rec *recorder) setResponseMeta(statusCode int, header http.Header) {
	if rec == nil {
		return
	}
	rec.statusCode = statusCode
	rec.respHeaders = redactHeaders(header, redactResponseHeaderNames)
}

// appendStreamLine 累计上游原始 SSE 行；达 maxStreamBytes 停止累计并置 truncated。
// 不影响转发——转发逻辑独立于录制。
func (rec *recorder) appendStreamLine(line []byte) {
	if rec == nil || rec.streamTrunc {
		return
	}
	if int64(len(rec.streamBuf))+int64(len(line))+1 > rec.maxStreamBytes {
		rec.streamTrunc = true
		return
	}
	rec.streamBuf = append(rec.streamBuf, line...)
	// scanner 去掉了换行符，补回 \n 以便拼回完整 SSE 流
	rec.streamBuf = append(rec.streamBuf, '\n')
}

// flush 快照业务元数据并入队异步落盘。nil 时 no-op。
func (rec *recorder) flush(now time.Time, log *store.UsageLog, rw *recordingWriter) {
	if rec == nil || rw == nil {
		return
	}
	rec.finishedAt = now
	rec.usageLog = log
	rw.enqueue(rec)
}

// --- recordingWriter：有界队列 + 单 worker + 丢弃计数（仿 usageLogWriter） ---

// recordingWriter 异步把 recorder 序列化为 JSON 文件落盘。
type recordingWriter struct {
	dir     string
	queue   chan *recorder
	stopCh  chan struct{}
	done    chan struct{}
	closed  atomic.Bool
	dropped atomic.Int64
}

// newRecordingWriter 创建写入器并启动工作协程。
func newRecordingWriter(dir string) *recordingWriter {
	w := &recordingWriter{
		dir:    dir,
		queue:  make(chan *recorder, recordingQueueSize),
		stopCh: make(chan struct{}),
		done:   make(chan struct{}),
	}
	go w.run()
	return w
}

func (w *recordingWriter) run() {
	defer close(w.done)
	for {
		select {
		case rec := <-w.queue:
			w.write(rec)
		case <-w.stopCh:
			for {
				select {
				case rec := <-w.queue:
					w.write(rec)
				default:
					return
				}
			}
		}
	}
}

func (w *recordingWriter) write(rec *recorder) {
	if err := rec.persist(w.dir); err != nil {
		obs.Logger(context.Background()).Warn("录制文件写入失败",
			"request_id", rec.requestID, "error", err)
	}
}

func (w *recordingWriter) enqueue(rec *recorder) {
	if w.closed.Load() {
		w.dropped.Add(1)
		return
	}
	select {
	case w.queue <- rec:
	default:
		w.dropped.Add(1)
	}
}

func (w *recordingWriter) droppedCount() int64 { return w.dropped.Load() }

func (w *recordingWriter) close(ctx context.Context) {
	if !w.closed.CompareAndSwap(false, true) {
		return
	}
	close(w.stopCh)
	select {
	case <-w.done:
	case <-ctx.Done():
	}
}

// --- JSON 序列化与文件落盘 ---

// recordingFile 输出的外层 JSON 结构，与 llm-proxy testcase 同构（超集）。
type recordingFile struct {
	ID       string             `json:"id"`
	Meta     recordingMeta      `json:"meta"`
	Request  recordingRequest   `json:"request"`
	Response recordingResponse  `json:"response"`
	TZL      *recordingTZLBlock `json:"tzl,omitempty"`
	Error    string             `json:"error,omitempty"`
}

type recordingMeta struct {
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at"`
	DurationMS int64     `json:"duration_ms"`
	ClientIP   string    `json:"client_ip"`
	BodyHash   string    `json:"body_hash"`
}

type recordingRequest struct {
	Method      string            `json:"method"`
	Path        string            `json:"path"`
	QueryString string            `json:"query_string"`
	Headers     map[string]string `json:"headers"`
	Body        json.RawMessage   `json:"body"`
	BodySize    int64             `json:"body_size"`
}

type recordingResponse struct {
	StatusCode int               `json:"status_code"`
	Headers    map[string]string `json:"headers"`
	Body       json.RawMessage   `json:"body"`
	BodySize   int64             `json:"body_size"`
	RawBody    json.RawMessage   `json:"raw_body,omitempty"`
	IsStream   bool              `json:"is_stream"`
	Truncated  bool              `json:"truncated"`
}

type recordingTZLBlock struct {
	UserID           int64  `json:"user_id"`
	ChannelID        int64  `json:"channel_id"`
	Model            string `json:"model"`
	UpstreamModel    string `json:"upstream_model"`
	Protocol         string `json:"protocol"`
	IsStream         bool   `json:"is_stream"`
	Status           string `json:"status"`
	CreditsCharged   int64  `json:"credits_charged"`
	CreditsCost      int64  `json:"credits_cost"`
	PromptTokens     int64  `json:"prompt_tokens"`
	CompletionTokens int64  `json:"completion_tokens"`
}

// persist 序列化 JSON 并写入 recordings/YYYY/MM/DD/<request_id>.json。
func (rec *recorder) persist(baseDir string) error {
	doc := rec.buildDocument()
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化录制文件失败: %w", err)
	}
	dateDir := filepath.Join(baseDir, recordingSubDir,
		rec.startedAt.Format("2006/01/02"))
	if err := os.MkdirAll(dateDir, recordingDirPerm); err != nil {
		return fmt.Errorf("创建录制目录失败: %w", err)
	}
	filePath := filepath.Join(dateDir, rec.requestID+".json")
	if err := os.WriteFile(filePath, data, recordingFilePerm); err != nil {
		return fmt.Errorf("写入录制文件失败: %w", err)
	}
	return nil
}

// buildDocument 把 recorder 状态组装为输出 JSON 结构。
func (rec *recorder) buildDocument() recordingFile {
	// 请求体：开启 redactBody 时只保留占位；截断的 body 可能不再是合法 JSON，
	// 用字符串编码避免 MarshalJSON 失败；未截断时作为 RawMessage 保留原始 JSON 结构。
	var reqBody json.RawMessage
	if len(rec.reqBody) > 0 {
		if rec.redactBody {
			reqBody = json.RawMessage(`"<redacted>"`)
		} else if rec.bodyTrunc {
			encoded, _ := json.Marshal(string(rec.reqBody))
			reqBody = encoded
		} else {
			reqBody = json.RawMessage(rec.reqBody)
		}
	}

	// 响应体：流式取 streamBuf（原始文本，需 JSON 字符串编码）；
	// 非流式取 out（已是 JSON 字节），另存 raw_body（上游原始）。
	var respBody json.RawMessage
	var respBodySize int64
	if rec.isStream {
		encoded, _ := json.Marshal(string(rec.streamBuf))
		respBody = encoded
		respBodySize = int64(len(rec.streamBuf))
	} else if len(rec.outBody) > 0 {
		respBody = json.RawMessage(rec.outBody)
		respBodySize = int64(len(rec.outBody))
	}

	doc := recordingFile{
		ID: rec.requestID,
		Meta: recordingMeta{
			StartedAt:  rec.startedAt,
			FinishedAt: rec.finishedAt,
			DurationMS: rec.finishedAt.Sub(rec.startedAt).Milliseconds(),
			ClientIP:   rec.clientIP,
			BodyHash:   hashBody(rec.reqBody),
		},
		Request: recordingRequest{
			Method:      rec.method,
			Path:        rec.path,
			QueryString: rec.query,
			Headers:     rec.reqHeaders,
			Body:        reqBody,
			BodySize:    rec.reqBodyLen,
		},
		Response: recordingResponse{
			StatusCode: rec.statusCode,
			Headers:    rec.respHeaders,
			Body:       respBody,
			BodySize:   respBodySize,
			RawBody:    rawBodyField(rec),
			IsStream:   rec.isStream,
			Truncated:  rec.streamTrunc || rec.bodyTrunc,
		},
	}

	if rec.usageLog != nil {
		l := rec.usageLog
		doc.TZL = &recordingTZLBlock{
			UserID:           l.UserID,
			ChannelID:        l.ChannelID,
			Model:            l.ModelName,
			UpstreamModel:    l.UpstreamModel,
			Protocol:         string(l.Protocol),
			IsStream:         l.IsStream,
			Status:           string(l.Status),
			CreditsCharged:   int64(l.CreditsCharged),
			CreditsCost:      int64(l.CreditsCost),
			PromptTokens:     l.PromptTokens,
			CompletionTokens: l.CompletionTokens,
		}
	}
	return doc
}

// rawBodyField 非流式时返回上游原始体作为 JSON RawMessage。
func rawBodyField(rec *recorder) json.RawMessage {
	if rec.isStream || len(rec.rawBody) == 0 {
		return nil
	}
	return json.RawMessage(rec.rawBody)
}

// hashBody 返回 body 的 SHA-256 哈希十六串（空 body 返回空串）。
func hashBody(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	h := sha256.Sum256(body)
	return hex.EncodeToString(h[:])
}

// redactHeaders 克隆 header 并把敏感头值替换为 "********"。
func redactHeaders(h http.Header, sensitive map[string]bool) map[string]string {
	out := make(map[string]string, len(h))
	for k, vs := range h {
		if sensitive[strings.ToLower(k)] {
			out[k] = redactedValue
		} else {
			out[k] = strings.Join(vs, ", ")
		}
	}
	return out
}
