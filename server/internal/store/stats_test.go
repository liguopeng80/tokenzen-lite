package store

import (
	"testing"
	"time"
)

// SpendDay 是仪表盘「今天」与每日花费计数共用的本地日界分桶键。
// time.Truncate(24h) 按 UTC epoch 取整，会在非 UTC 时区把本地凌晨算进昨天，
// 从而系统性少报——这里固定 SpendDay 走本地日界的语义。
func TestSpendDayUsesLocalMidnight(t *testing.T) {
	// 断言「本地午夜 ≠ UTC 午夜」要求本地时区确实非 UTC，而 CI runner 的
	// 本地时区就是 UTC（其 time.Local 与 time.UTC 指针不同，无法用同一性
	// 比较识别）。因此用例内固定为 UTC+8 使语义在任意时区的机器上一致；
	// time.Local 是进程级全局，替换后 defer 还原（本包测试串行，无并发干扰）。
	orig := time.Local
	time.Local = time.FixedZone("UTC+8", 8*60*60)
	defer func() { time.Local = orig }()

	// 固定一个本地时刻：凌晨 03:30（东八区下对应 UTC 的前一天 19:30，本地日与 UTC 日不同）。
	now := time.Date(2026, 8, 7, 3, 30, 0, 0, time.Local)
	day := SpendDay(now)

	want := time.Date(2026, 8, 7, 0, 0, 0, 0, time.Local)
	if !day.Equal(want) {
		t.Fatalf("SpendDay 应返回本地午夜 %v，实际 %v", want, day)
	}
	// 与 Truncate(24h) 的关键差异：本地午夜与 UTC 午夜不等。
	utcMidnight := now.Truncate(24 * time.Hour)
	if day.Equal(utcMidnight) {
		t.Fatalf("SpendDay 不应等于 UTC 午夜截断 %v——这会把本地凌晨算进昨天", utcMidnight)
	}
	// 分桶键自身位于所属日的起点，且零分零秒。
	if day.Hour() != 0 || day.Minute() != 0 || day.Second() != 0 {
		t.Errorf("SpendDay 应截断到日界零点，实际 %v", day)
	}
	if day.Location() != time.Local {
		t.Errorf("SpendDay 应保留本地时区，实际 %v", day.Location())
	}
}
