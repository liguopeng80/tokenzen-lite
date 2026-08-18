package maintenance

import (
	"strings"
	"testing"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
)

// computeMonthlyGrantDelta 的判定逻辑是按月发放的核心纯逻辑，
// 不依赖 Scheduler 或 DB——补足口径与增发口径的差异完全由入参决定。

func TestComputeMonthlyGrantDeltaTopUp(t *testing.T) {
	tests := []struct {
		name      string
		amount    domain.Credits
		balance   domain.Credits
		wantDelta domain.Credits
		wantSkip  bool
	}{
		{"余额为零补到额度", 100_000, 0, 100_000, false},
		{"余额低于额度补差额", 100_000, 30_000, 70_000, false},
		{"余额等于额度不发放", 100_000, 100_000, 0, true},
		{"余额高于额度不发放", 100_000, 200_000, 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			delta, skip := computeMonthlyGrantDelta(domain.MonthlyGrantTopUp, tt.amount, tt.balance)
			if delta != tt.wantDelta || skip != tt.wantSkip {
				t.Errorf("delta=%d skip=%v；期望 delta=%d skip=%v",
					delta, skip, tt.wantDelta, tt.wantSkip)
			}
		})
	}
}

func TestComputeMonthlyGrantDeltaAdd(t *testing.T) {
	// 增发口径：不看余额，固定增发；未用完的部分累积。
	delta, skip := computeMonthlyGrantDelta(domain.MonthlyGrantAdd, 50_000, 200_000)
	if delta != 50_000 || skip {
		t.Errorf("增发口径应固定增发额度，实际 delta=%d skip=%v", delta, skip)
	}
	// 余额为零时同样增发。
	delta, skip = computeMonthlyGrantDelta(domain.MonthlyGrantAdd, 50_000, 0)
	if delta != 50_000 || skip {
		t.Errorf("增发口径下余额为零也应增发，实际 delta=%d skip=%v", delta, skip)
	}
}

func TestBuildMonthlyGrantFailureEvent(t *testing.T) {
	ev := buildMonthlyGrantFailureEvent("2026-08", domain.MonthlyGrantTopUp, 95, 5)
	if !strings.Contains(ev.Message, "2026-08") {
		t.Errorf("正文应含月份：%s", ev.Message)
	}
	if !strings.Contains(ev.Message, "5 个账号发放失败") {
		t.Errorf("正文应含失败人数：%s", ev.Message)
	}
	if !strings.Contains(ev.Message, "成功 95 个") {
		t.Errorf("正文应含成功人数：%s", ev.Message)
	}
	if got := ev.Payload["failed"]; got != 5 {
		t.Errorf("Payload failed 应为 5，实际 %v", got)
	}
}

func TestMonthlyGrantKey(t *testing.T) {
	got := monthlyGrantKey("2026-08", 42)
	if got != "monthly-grant:2026-08:42" {
		t.Errorf("幂等键格式错误：%s", got)
	}
}
