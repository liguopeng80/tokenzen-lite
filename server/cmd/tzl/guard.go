// serve 子命令的启动期不变式守卫。纯函数，便于单元测试。
package main

import (
	"fmt"
	"time"
)

// checkOrphanTimeoutGuard 校验上游超时严格小于孤儿预扣判定阈值。
// 违例时返回错误：流式请求可能在结算前被孤儿清理全额退款（写 refund 流水），
// 流结束后结算再补写 settle_adjust 流水；二者 entry_type 不同，账流唯一索引
// (request_id, entry_type) 不互斥，净流水会使该请求的用户余额异常增加
// （预扣 P、实际消耗 final<P 时，余额多出 P-final）。
//
// 判定时从阈值中扣除结算写入窗口 settlementWindow：上游在阈值前刚完成时，
// 结算（settle_adjust）仍在该窗口内写入。若阈值刚好在上游超时之后触发孤儿清理，
// 就会出现「先退款、再补 settle_adjust」的套利序列。因此有效判定为
// upstreamTimeout + settlementWindow >= orphanThreshold 即违例。
//
// 阈值当前为常量、非运维可配，故此处对超时取 fail-safe：宁可拒绝启动也不静默放行套利配置。
func checkOrphanTimeoutGuard(upstreamTimeoutSec int, orphanThreshold, settlementWindow time.Duration) error {
	upstreamTimeout := time.Duration(upstreamTimeoutSec) * time.Second
	if upstreamTimeout+settlementWindow >= orphanThreshold {
		return fmt.Errorf(
			"上游超时配置不安全：TZL_UPSTREAM_TIMEOUT_SEC=%d 秒（=%s）"+
				" 加上结算写入窗口 %s 后为 %s，必须严格小于孤儿预扣判定阈值 %s；"+
				"超时大于等于该阈值时，流式请求可能在结算前被孤儿清理全额退款，"+
				"随后结算再补写 settle_adjust 流水，因 entry_type 不同、唯一索引不互斥，"+
				"会使该请求的净流水让用户余额异常增加（套利）。"+
				"已为结算写入预留 %s（后台写入截止时间），请将 TZL_UPSTREAM_TIMEOUT_SEC 下调到"+
				"（阈值 − 结算窗口 = %s − %s = %s）以下"+
				"（孤儿阈值当前固定为 %s，不可在运维侧调整）",
			upstreamTimeoutSec, upstreamTimeout,
			settlementWindow, upstreamTimeout+settlementWindow, orphanThreshold,
			settlementWindow,
			orphanThreshold, settlementWindow, orphanThreshold-settlementWindow,
			orphanThreshold,
		)
	}
	return nil
}
