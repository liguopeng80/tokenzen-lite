package strutil

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTruncateKeepsShortStringIntact(t *testing.T) {
	if got := Truncate("上游拒绝", 500); got != "上游拒绝" {
		t.Fatalf("未超长时应原样返回，得到 %q", got)
	}
	if got := Truncate("", 500); got != "" {
		t.Fatalf("空串应原样返回，得到 %q", got)
	}
}

// 中文按字节截断会切出半个字符，这正是用量日志写不进库的成因。
func TestTruncateCutsAtRuneBoundary(t *testing.T) {
	// 每个汉字三字节，切在 500 字节处必然落在字符中间。
	src := strings.Repeat("模型调用失败，上游返回错误。", 100)
	if len(src) <= 500 {
		t.Fatalf("测试数据需超过 500 字节，实际 %d", len(src))
	}
	got := Truncate(src, 500)
	if len(got) > 500 {
		t.Fatalf("截断后应不超过 500 字节，实际 %d", len(got))
	}
	if !utf8.ValidString(got) {
		t.Fatal("截断结果必须是合法 UTF-8，否则 PostgreSQL 拒绝整条写入")
	}
	if !strings.HasPrefix(src, got) {
		t.Fatal("截断结果应为原串前缀")
	}
	// 与硬切对比，确认确实回退了切点。
	if utf8.ValidString(src[:500]) {
		t.Skip("该数据的硬切点恰好在边界上，换用例才有区分度")
	}
	if len(got) == 500 {
		t.Fatal("硬切点非法时应回退到上一个字符边界")
	}
}

func TestTruncateCleansInvalidUTF8(t *testing.T) {
	// 上游返回的响应体未必是 UTF-8：压缩残片、非 UTF-8 编码都可能出现。
	src := "错误：" + string([]byte{0xff, 0xfe, 0x80}) + "详情"
	got := Truncate(src, 500)
	if !utf8.ValidString(got) {
		t.Fatalf("非法字节应被清除，得到 %q", got)
	}
	if !strings.Contains(got, "错误：") || !strings.Contains(got, "详情") {
		t.Fatalf("合法部分应保留，得到 %q", got)
	}
}

func TestTruncateASCIIExactLength(t *testing.T) {
	if got := Truncate("abcdef", 3); got != "abc" {
		t.Fatalf("ASCII 应精确截到上限，得到 %q", got)
	}
}

func TestTruncateNonPositiveLimit(t *testing.T) {
	if got := Truncate("任意内容", 0); got != "" {
		t.Fatalf("上限为 0 时应返回空串，得到 %q", got)
	}
	if got := Truncate("任意内容", -1); got != "" {
		t.Fatalf("上限为负时应返回空串，得到 %q", got)
	}
}

// 单个字符就超过上限时只能整体丢弃，不能吐出半个字符。
func TestTruncateSingleRuneLongerThanLimit(t *testing.T) {
	got := Truncate("模", 2)
	if got != "" {
		t.Fatalf("单字符超限时应返回空串，得到 %q", got)
	}
}
