package api

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/liguopeng80/tokenzen-lite/server/internal/api/respond"
)

// maxBodyBytes 限制管理面请求体大小，防止异常大包。
const maxBodyBytes = 1 << 20 // 1 MiB

// Bind 解析 JSON 请求体到 dst；失败时已写出 400 响应并返回 false。
func Bind(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		respond.Fail(w, http.StatusBadRequest, "请求体不是合法 JSON")
		return false
	}
	return true
}

// IDParam 读取路径参数并转为 int64；非法时已写出 400 响应并返回 false。
func IDParam(w http.ResponseWriter, r *http.Request, name string) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, name), 10, 64)
	if err != nil || id <= 0 {
		respond.Fail(w, http.StatusBadRequest, "路径参数 "+name+" 不合法")
		return 0, false
	}
	return id, true
}

// jsonRawValue 是延迟解析的 JSON 原始值（设置项写入用）。
type jsonRawValue = json.RawMessage

// parseInt64 解析十进制整数；空串或非法返回 ok=false。
func parseInt64(s string) (int64, bool) {
	if s == "" {
		return 0, false
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

// PageParams 读取分页参数，带默认值与上限。
func PageParams(r *http.Request) (page, pageSize int) {
	page, _ = strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	pageSize, _ = strconv.Atoi(r.URL.Query().Get("page_size"))
	if pageSize < 1 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}
	return page, pageSize
}
