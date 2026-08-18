// Package obs 提供可观测性基础设施：结构化日志初始化、request_id 中间件、
// 请求访问日志。关键路径日志规范见项目根 CLAUDE.md。
package obs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net"
	"net/http"
	"os"
	"time"
)

// HeaderRealIP 是可信反向代理传递客户端真实 IP 的头。
const HeaderRealIP = "X-Real-IP"

type ctxKey int

const requestIDKey ctxKey = 0

// HeaderRequestID 是对外暴露与接受的 request_id 头。
const HeaderRequestID = "X-Request-ID"

// nowFn 与 randFn 是 time.Now / rand.Read 的可注入薄包装。
// 中间件计时与 request_id 生成原本直接调用标准库，测试无法控制时间推进与随机源；
// 改为包级 var 后，测试可在 t.Cleanup 还原的前提下临时替换，使 AccessLogMiddleware
// 的耗时断言与 newRequestID 的输出确定性可验。生产路径默认等同原调用。
var (
	nowFn  = time.Now
	randFn = rand.Read
)

// InitLogger 按级别初始化全局 slog JSON logger。
func InitLogger(level string) {
	var lv slog.Level
	switch level {
	case "debug":
		lv = slog.LevelDebug
	case "warn":
		lv = slog.LevelWarn
	case "error":
		lv = slog.LevelError
	default:
		lv = slog.LevelInfo
	}
	h := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lv})
	slog.SetDefault(slog.New(h))
}

// RequestID 返回 ctx 中的 request_id；不存在时返回空串。
func RequestID(ctx context.Context) string {
	if v, ok := ctx.Value(requestIDKey).(string); ok {
		return v
	}
	return ""
}

// Logger 返回带 request_id 字段的 logger，业务代码统一经由它输出日志。
func Logger(ctx context.Context) *slog.Logger {
	return slog.Default().With("request_id", RequestID(ctx))
}

// WithRequestID 把 request_id 写入 ctx。用于非 HTTP 入口（CLI、启动任务）
// 以既有请求的标识输出日志。
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey, id)
}

// RequestIDMiddleware 提取或生成 request_id，写入 context 与响应头。
func RequestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(HeaderRequestID)
		if id == "" || len(id) > 64 {
			id = newRequestID()
		}
		ctx := context.WithValue(r.Context(), requestIDKey, id)
		w.Header().Set(HeaderRequestID, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// AccessLogMiddleware 输出端点入口与结束日志（含总耗时与状态码）。
func AccessLogMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := nowFn()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		// 经 nowFn 而非 time.Since 计算耗时：这样替换 nowFn 即可同时控制起点与终点，
		// 使中间件的耗时埋点与日志断言确定性可验。
		elapsed := nowFn().Sub(start)
		RecordHTTPRequest(r.URL.Path, r.Method, sw.status, elapsed)
		Logger(r.Context()).Info("http_request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", sw.status,
			"duration_ms", elapsed.Milliseconds(),
			"client_ip", clientIP(r),
		)
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

// Flush 透传底层 Flusher，SSE 依赖此能力。
func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// RealIPMiddleware 按可信代理列表解析客户端真实 IP。
// 仅当连接的远端地址命中 trustedProxies（IP 或 CIDR）且 X-Real-IP 是合法 IP 时，
// 才把 r.RemoteAddr 改写为该头的值；其余情况删除该头，
// 保证后续环节（IP 白名单、用量日志、访问日志）一律以连接远端地址为准。
// trustedProxies 为空时不信任任何代理。非法条目在 config 校验阶段已拒绝。
func RealIPMiddleware(trustedProxies []string) func(http.Handler) http.Handler {
	var nets []*net.IPNet
	var ips []net.IP
	for _, p := range trustedProxies {
		if _, cidr, err := net.ParseCIDR(p); err == nil {
			nets = append(nets, cidr)
		} else if ip := net.ParseIP(p); ip != nil {
			ips = append(ips, ip)
		}
	}
	trusted := func(remote net.IP) bool {
		for _, cidr := range nets {
			if cidr.Contains(remote) {
				return true
			}
		}
		for _, ip := range ips {
			if ip.Equal(remote) {
				return true
			}
		}
		return false
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			remote := net.ParseIP(clientIP(r))
			if remote != nil && trusted(remote) {
				if real := net.ParseIP(r.Header.Get(HeaderRealIP)); real != nil {
					r.RemoteAddr = real.String()
					next.ServeHTTP(w, r)
					return
				}
			}
			r.Header.Del(HeaderRealIP)
			next.ServeHTTP(w, r)
		})
	}
}

// ClientIP 返回请求的客户端 IP（不含端口）。
// 经 RealIPMiddleware 处理后，RemoteAddr 已是可信的客户端地址。
func ClientIP(r *http.Request) string {
	return clientIP(r)
}

func clientIP(r *http.Request) string {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

func newRequestID() string {
	b := make([]byte, 16)
	if _, err := randFn(b); err != nil {
		return "rid-rand-fail"
	}
	return hex.EncodeToString(b)
}
