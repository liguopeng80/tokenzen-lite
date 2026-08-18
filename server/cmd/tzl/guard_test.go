package main

import (
	"strings"
	"testing"
	"time"

	"github.com/liguopeng80/tokenzen-lite/server/internal/relay"
)

// checkOrphanTimeoutGuard 的表驱动单测：覆盖默认安全配置、扣除结算窗口后的边界、
// 等于阈值与大于阈值的违例场景。DB 无关。
//
// 守卫语义：upstreamTimeout + settlementWindow >= orphanThreshold 即违例。
// settlementWindow 取 relay.BackgroundWriteTimeout（当前 8s），与生产事实源一致。
func TestCheckOrphanTimeoutGuard(t *testing.T) {
	threshold := 15 * time.Minute
	settlementWindow := relay.BackgroundWriteTimeout // 8s

	tests := []struct {
		name               string
		upstreamTimeoutSec int
		wantErr            bool
		errContains        []string // 期望错误文本包含的子串（违例时校验可操作性）
	}{
		{
			name:               "默认配置安全：600s + 8s = 608s 严格小于 900s",
			upstreamTimeoutSec: 600,
			wantErr:            false,
		},
		{
			name:               "边界：891s + 8s = 899s 严格小于 900s，通过",
			upstreamTimeoutSec: 891,
			wantErr:            false,
		},
		{
			name:               "扣除结算窗口后违例：892s + 8s = 900s 等于阈值",
			upstreamTimeoutSec: 892,
			wantErr:            true,
			errContains:        []string{"892", "8s", "15m0s", "TZL_UPSTREAM_TIMEOUT_SEC", "结算写入", "14m52s"},
		},
		{
			name:               "极端配置违例：899s + 8s = 907s 大于 900s",
			upstreamTimeoutSec: 899,
			wantErr:            true,
			errContains:        []string{"899", "8s", "15m0s", "结算写入", "15m7s"},
		},
		{
			name:               "等于阈值：900s + 8s 违例",
			upstreamTimeoutSec: 900,
			wantErr:            true,
			errContains:        []string{"900", "15m0s"},
		},
		{
			name:               "远大于阈值：1200s 违例",
			upstreamTimeoutSec: 1200,
			wantErr:            true,
			errContains:        []string{"1200", "20m0s", "15m0s"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkOrphanTimeoutGuard(tt.upstreamTimeoutSec, threshold, settlementWindow)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("期望返回错误，实际为 nil")
				}
				for _, sub := range tt.errContains {
					if !strings.Contains(err.Error(), sub) {
						t.Errorf("错误信息缺少子串 %q，完整错误：%v", sub, err)
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("期望 nil，实际返回错误：%v", err)
			}
		})
	}
}
