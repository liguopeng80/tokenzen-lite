// Package respond 定义统一的 API 响应信封与分页结构。
// 全部 /api 端点必须经由本包输出，禁止 handler 自行拼 JSON。
package respond

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"reflect"
)

// Envelope 是统一响应信封。
type Envelope struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// Page 是统一分页结构，作为 Envelope.Data 使用。
type Page[T any] struct {
	Page     int   `json:"page"`
	PageSize int   `json:"page_size"`
	Total    int64 `json:"total"`
	Items    []T   `json:"items"`
}

// NewPage 构造分页结构；items 为 nil 时归一为空切片，避免前端收到 null。
func NewPage[T any](page, pageSize int, total int64, items []T) Page[T] {
	if items == nil {
		items = []T{}
	}
	return Page[T]{Page: page, PageSize: pageSize, Total: total, Items: items}
}

// nullData 是 data 键的显式 null 值：契约声明成功信封恒为三个顶层键，
// 处理函数传 nil 时不能让 omitempty 把整个 data 键连同 success/message 一起裁掉。
var nullData = json.RawMessage("null")

// dataOrNull 让 data 字段在成功响应中恒定出现——nil 时序列化为 data:null 而非省略该键。
//
// 另外把 nil 切片归一为空切片：Go 的 nil 切片会序列化成 null，前端拿到列表端点的
// data 直接遍历就会抛异常（新装系统尚无任何数据时，全部列表端点都走这条路径）。
// 列表端点的"空结果"统一表达为 []，与 NewPage 对 items 的处理一致。
func dataOrNull(data any) any {
	if data == nil {
		return nullData
	}
	if v := reflect.ValueOf(data); v.Kind() == reflect.Slice && v.IsNil() {
		return reflect.MakeSlice(v.Type(), 0, 0).Interface()
	}
	return data
}

// OK 输出成功响应。
func OK(w http.ResponseWriter, data any) {
	write(w, http.StatusOK, Envelope{Success: true, Data: dataOrNull(data)})
}

// OKMessage 输出带提示文案的成功响应。message 面向用户，必须可读。
func OKMessage(w http.ResponseWriter, message string, data any) {
	write(w, http.StatusOK, Envelope{Success: true, Message: message, Data: dataOrNull(data)})
}

// Created 输出创建成功响应。
func Created(w http.ResponseWriter, data any) {
	write(w, http.StatusCreated, Envelope{Success: true, Data: dataOrNull(data)})
}

// Fail 输出业务失败响应。message 面向用户，必须可读；失败信封不含 data 字段。
func Fail(w http.ResponseWriter, status int, message string) {
	write(w, status, Envelope{Success: false, Message: message})
}

func write(w http.ResponseWriter, status int, env Envelope) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(env); err != nil {
		slog.Error("响应序列化失败", "error", err)
	}
}
