package ratelimit

import (
	"testing"
	"time"
)

func TestFailureLockerLockAfterThreshold(t *testing.T) {
	clock := newFakeClock()
	l := NewFailureLocker()
	l.Now = clock.now
	lockFor := 100 * time.Millisecond
	for i := 0; i < 2; i++ {
		l.RecordFailure("alice", 3, lockFor)
		if l.Locked("alice") {
			t.Fatalf("第 %d 次失败未达阈值，不应锁定", i+1)
		}
	}
	l.RecordFailure("alice", 3, lockFor)
	if !l.Locked("alice") {
		t.Fatal("连续失败达到阈值应锁定")
	}
	if l.Locked("bob") {
		t.Error("不同 key 互不影响")
	}
	clock.advance(lockFor + time.Millisecond)
	if l.Locked("alice") {
		t.Error("锁定期结束后应解锁")
	}
}

func TestFailureLockerResetClearsCount(t *testing.T) {
	l := NewFailureLocker()
	lockFor := time.Minute
	l.RecordFailure("carol", 3, lockFor)
	l.RecordFailure("carol", 3, lockFor)
	l.Reset("carol")
	// 清零后再失败 2 次仍未达阈值。
	l.RecordFailure("carol", 3, lockFor)
	l.RecordFailure("carol", 3, lockFor)
	if l.Locked("carol") {
		t.Error("成功登录清零后，失败计数应重新累计")
	}
	l.RecordFailure("carol", 3, lockFor)
	if !l.Locked("carol") {
		t.Error("清零后再次连续失败达到阈值应锁定")
	}
}

func TestFailureLockerStaleCountExpires(t *testing.T) {
	clock := newFakeClock()
	l := NewFailureLocker()
	l.Now = clock.now
	lockFor := 50 * time.Millisecond
	l.RecordFailure("dave", 2, lockFor)
	// 距上次失败超过 lockFor，旧计数过期，从 1 重新计起。
	clock.advance(lockFor + time.Millisecond)
	l.RecordFailure("dave", 2, lockFor)
	if l.Locked("dave") {
		t.Error("过期计数不应与新失败合并计数")
	}
}

func TestFailureLockerDisabled(t *testing.T) {
	l := NewFailureLocker()
	for i := 0; i < 10; i++ {
		l.RecordFailure("erin", 0, time.Minute)
	}
	if l.Locked("erin") {
		t.Error("threshold<=0 应不锁定")
	}
}
