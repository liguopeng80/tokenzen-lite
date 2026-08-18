package relay

import "testing"

// countImageItems：图像生成响应 data 数组长度即实际返回张数（U1）。
func TestCountImageItemsValidResponses(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want int64
	}{
		{"空数组", `{"created":1,"data":[]}`, 0},
		{"单张", `{"created":1,"data":[{"b64_json":"aW1n"}]}`, 1},
		{"三张", `{"created":1,"data":[{"url":"a"},{"url":"b"},{"url":"c"}]}`, 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, counted := countImageItems([]byte(tc.raw))
			if !counted {
				t.Fatalf("合法响应应可判定张数，实际 counted=false")
			}
			if got != tc.want {
				t.Errorf("期望 %d 张，实际 %d", tc.want, got)
			}
		})
	}
}

// countImageItems：响应不可判定时必须返回 counted=false，
// 调用方回退到请求张数计费，禁止误判为 0 张导致全额退款（U2）。
func TestCountImageItemsUndeterminable(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"非 JSON", `PNG-binary-not-json`},
		{"缺少 data 字段", `{"created":1}`},
		{"data 非数组", `{"created":1,"data":"oops"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, counted := countImageItems([]byte(tc.raw)); counted {
				t.Errorf("不可判定响应应返回 counted=false，实际 true")
			}
		})
	}
}
