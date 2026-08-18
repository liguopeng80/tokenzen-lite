package api

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
)

// 覆盖 P2-12 单元用例 U1-U3：daysParam 边界、usageLogFilterFromQuery
// 的用户隔离与字段映射。纯函数测试，不依赖数据库。

// TestDaysParamBoundary 覆盖 days 参数的缺省回落与上限截断。
func TestDaysParamBoundary(t *testing.T) {
	cases := []struct {
		name  string
		query string
		want  int
	}{
		{"参数缺省回落", "", 30},
		{"days=0 回落", "?days=0", 30},
		{"负数回落", "?days=-1", 30},
		{"非数字回落", "?days=abc", 30},
		{"下边界 1", "?days=1", 1},
		{"上边界 365", "?days=365", 365},
		{"366 截断为 365", "?days=366", 365},
		{"400 截断为 365", "?days=400", 365},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/api/me/usage-daily"+tc.query, nil)
			if got := daysParam(r, 30, 365); got != tc.want {
				t.Errorf("daysParam(%q) = %d，期望 %d", tc.query, got, tc.want)
			}
		})
	}
}

// TestUsageLogFilterUserIsolation 覆盖筛选构造的用户隔离语义：
// 登录用户视角（userID>0）下查询串中的 user_id 必须被忽略。
func TestUsageLogFilterUserIsolation(t *testing.T) {
	cases := []struct {
		name   string
		userID int64
		query  string
		want   int64
	}{
		{"用户视角忽略查询串 user_id", 7, "?user_id=9", 7},
		{"管理端视角 user_id 生效", 0, "?user_id=9", 9},
		{"管理端视角缺省为全站", 0, "", 0},
		{"用户视角无参数保持自身", 7, "", 7},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/api/me/usage-logs"+tc.query, nil)
			f := usageLogFilterFromQuery(r, tc.userID)
			if f.UserID != tc.want {
				t.Errorf("userID=%d query=%q 时 filter.UserID = %d，期望 %d",
					tc.userID, tc.query, f.UserID, tc.want)
			}
		})
	}
}

// TestUsageLogFilterFieldMapping 覆盖查询串到 UsageLogFilter 的字段映射，
// 以及非法数字参数被忽略的分支。
func TestUsageLogFilterFieldMapping(t *testing.T) {
	r := httptest.NewRequest("GET", "/api/admin/usage-logs"+
		"?model=glm-5&status=settled&request_id=req-1"+
		"&api_key_id=11&channel_id=22"+
		"&start_timestamp=1700000000&end_timestamp=1700003600"+
		"&page=2&page_size=5", nil)
	f := usageLogFilterFromQuery(r, 0)

	if f.ModelName != "glm-5" || f.Status != domain.UsageStatus("settled") ||
		f.RequestID != "req-1" {
		t.Errorf("字符串字段映射不符: %+v", f)
	}
	if f.APIKeyID != 11 || f.ChannelID != 22 {
		t.Errorf("数字字段映射不符: api_key_id=%d channel_id=%d", f.APIKeyID, f.ChannelID)
	}
	if f.Page != 2 || f.PageSize != 5 {
		t.Errorf("分页参数映射不符: page=%d page_size=%d", f.Page, f.PageSize)
	}
	if f.StartTime == nil || !f.StartTime.Equal(time.Unix(1700000000, 0)) {
		t.Errorf("start_timestamp 映射不符: %v", f.StartTime)
	}
	if f.EndTime == nil || !f.EndTime.Equal(time.Unix(1700003600, 0)) {
		t.Errorf("end_timestamp 映射不符: %v", f.EndTime)
	}

	// 非法数字参数走 parseInt64 ok=false 分支，全部忽略
	r = httptest.NewRequest("GET", "/api/admin/usage-logs"+
		"?api_key_id=abc&channel_id=&start_timestamp=xyz&end_timestamp=1.5", nil)
	f = usageLogFilterFromQuery(r, 0)
	if f.APIKeyID != 0 || f.ChannelID != 0 || f.StartTime != nil || f.EndTime != nil {
		t.Errorf("非法数字参数应被忽略: %+v", f)
	}
}
