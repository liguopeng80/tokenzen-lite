package store

import (
	"reflect"
	"testing"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
)

// 白名单必须放行投递结果回写所需的全部字段。漏放任一字段会让告警状态停留在 pending，
// 表现为「管理员永远收不到告警也看不到失败原因」，与「投递失败」的轻微表象不匹配。
func TestAlertUpdatableFieldsAllowsDeliveryWriteback(t *testing.T) {
	required := []string{"status", "attempts", "last_error", "sent_at", "channels_sent"}
	for _, f := range required {
		if _, ok := alertUpdatableFields[f]; !ok {
			t.Errorf("投递回写所需的字段 %s 必须在白名单内", f)
		}
	}
}

// 落库时的不可变快照字段（alert_type、severity、dedup_key、created_at）禁止出现在白名单内。
// 这些字段记录「这条告警是什么、何时产生」，事后改写会让审计与去重窗口失效。
func TestAlertUpdatableFieldsExcludesImmutableSnapshot(t *testing.T) {
	immutable := []string{"id", "alert_type", "severity", "dedup_key", "title",
		"message", "payload", "created_at"}
	for _, f := range immutable {
		if _, ok := alertUpdatableFields[f]; ok {
			t.Errorf("不可变字段 %s 不应在白名单内", f)
		}
	}
}

// filterUpdatableAlertFields 必须保留白名单内字段、丢弃白名单外字段。
// 用 map 而非切片做断言：调用方传字段顺序无关，结果应是字段集合的交集。
func TestFilterUpdatableAlertFieldsKeepsWhitelistedDropsRest(t *testing.T) {
	in := map[string]any{
		"status":        domain.AlertSent,
		"attempts":      3,
		"last_error":    "boom",
		"sent_at":       nil,
		"channels_sent": nil,
		// 以下应被丢弃
		"alert_type": "channel_auto_disabled",
		"created_at": "2026-01-01",
		"id":         int64(7),
		"severity":   "critical",
	}
	want := map[string]any{
		"status":        domain.AlertSent,
		"attempts":      3,
		"last_error":    "boom",
		"sent_at":       nil,
		"channels_sent": nil,
	}
	got := filterUpdatableAlertFields(in)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("白名单过滤结果不符\nwant=%v\ngot =%v", want, got)
	}
}

// 全部入参都在白名单外时返回空 map（而非 nil），让 GORM Updates 成为 no-op
// 而不是 nil-map 触发的零值写入。
func TestFilterUpdatableAlertFieldsEmptyWhenAllRejected(t *testing.T) {
	in := map[string]any{
		"alert_type": "x",
		"created_at": "y",
	}
	got := filterUpdatableAlertFields(in)
	if len(got) != 0 {
		t.Errorf("全部字段被拒时应返回空 map，实际 %v", got)
	}
}
