package respond

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOKEnvelope(t *testing.T) {
	rec := httptest.NewRecorder()
	OK(rec, map[string]int{"n": 1})
	if rec.Code != 200 {
		t.Fatalf("状态码应为 200，实际 %d", rec.Code)
	}
	var env Envelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("响应不是合法 JSON: %v", err)
	}
	if !env.Success {
		t.Error("success 应为 true")
	}
}

func TestFailEnvelope(t *testing.T) {
	rec := httptest.NewRecorder()
	Fail(rec, 402, "积分余额不足")
	if rec.Code != 402 {
		t.Fatalf("状态码应为 402，实际 %d", rec.Code)
	}
	var env Envelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("响应不是合法 JSON: %v", err)
	}
	if env.Success || env.Message != "积分余额不足" {
		t.Errorf("信封内容不符: %+v", env)
	}
	if strings.Contains(rec.Body.String(), `"data"`) {
		t.Error("失败响应不应包含 data 字段")
	}
}

func TestNewPageNilItems(t *testing.T) {
	p := NewPage[string](1, 20, 0, nil)
	b, _ := json.Marshal(p)
	if !strings.Contains(string(b), `"items":[]`) {
		t.Errorf("nil items 应序列化为空数组，实际 %s", b)
	}
}

// TestOKNormalizesNilSlice 空列表必须序列化为 []，不能是 null。
// 缺陷成因：Go 的 nil 切片序列化成 null，前端对列表端点的返回直接遍历会抛异常——
// 新装系统尚无任何数据时，管理端概览页因此白屏（错误边界接管）。
func TestOKNormalizesNilSlice(t *testing.T) {
	type row struct {
		Day string `json:"day"`
	}
	cases := []struct {
		name string
		data any
		want string
	}{
		{"nil 切片归一为空数组", []row(nil), `"data":[]`},
		{"非空切片原样输出", []row{{Day: "2026-08-06"}}, `"data":[{"day":"2026-08-06"}]`},
		{"nil 入参仍为 null", nil, `"data":null`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			OK(w, c.data)
			if body := strings.TrimSpace(w.Body.String()); !strings.Contains(body, c.want) {
				t.Errorf("响应体应包含 %s，实际 %s", c.want, body)
			}
		})
	}
}
