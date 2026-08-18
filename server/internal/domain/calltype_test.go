package domain

import "testing"

// TestCallTypeValues 固化调用类型枚举取值，
// 与 docs/glossary.md 的 CallType 条目（embedding/image/stream/non_stream/other）保持一致，
// 防止文档与代码脱钩的无意回改。
func TestCallTypeValues(t *testing.T) {
	cases := []struct {
		name CallType
		want string
	}{
		{CallTypeEmbedding, "embedding"},
		{CallTypeImage, "image"},
		{CallTypeStream, "stream"},
		{CallTypeNonStream, "non_stream"},
		{CallTypeOther, "other"},
	}
	for _, c := range cases {
		if string(c.name) != c.want {
			t.Errorf("%s 应为 %q，实际 %q", c.name, c.want, c.name)
		}
	}
}
