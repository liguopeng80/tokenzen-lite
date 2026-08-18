package maintenance

import (
	"context"
	"fmt"
	"time"

	"github.com/liguopeng80/tokenzen-lite/server/internal/alerting"
	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/obs"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// userNoticeSuppressWindow 同一用户两次余额提醒之间的最短间隔。
// 维护循环每小时一轮，若沿用运维告警的抑制窗口，余额持续偏低的员工会每小时
// 收到一封同样内容的邮件；一天一封既足以让本人及时找管理员，也不至于被当成垃圾邮件。
const userNoticeSuppressWindow = 24 * time.Hour

// notifyLowBalanceUsers 向余额不足的用户本人投递提醒。
//
// 管理员侧的聚合告警解决的是「谁该补发积分」，本函数解决的是「本人事先知情」：
// 余额耗尽时该用户全部调用同时开始失败，没有预告的话，失败会先表现为客户端报错。
// 三个前置条件缺一不可——本项未关闭、邮件通道已配置、该用户填了邮箱；
// 任一不满足时静默跳过，不产生注定投递失败的事件记录。
func (s *Scheduler) notifyLowBalanceUsers(ctx context.Context,
	threshold domain.Credits, users []store.LowBalanceUser) {

	if s.Alerts == nil || !s.Settings.GetBool(ctx, "user_balance_notice_enabled") {
		return
	}
	if !s.Alerts.EmailReady(ctx) {
		obs.Logger(ctx).Info("邮件通道未配置，跳过用户本人的余额提醒", "users", len(users))
		return
	}
	sent := 0
	for _, u := range users {
		if u.Email == "" {
			continue
		}
		s.Alerts.Raise(ctx, alerting.Event{
			Type:     domain.AlertUserBalanceNotice,
			Severity: domain.AlertWarning,
			// 去重键不含日期：抑制窗口本身就是一天，含日期反而会在跨日时刻放行两封。
			DedupKey:    fmt.Sprintf("user_balance_notice:%d", u.ID),
			SuppressFor: userNoticeSuppressWindow,
			Title:       "你的积分余额不足",
			Message: fmt.Sprintf("账号 %s 当前余额 %d 积分，低于预警线 %d。"+
				"余额耗尽后，你的全部 API 调用会被拒绝，请联系管理员补发积分。",
				u.Username, u.CreditBalance, threshold),
			EmailTo: []string{u.Email},
			Payload: map[string]any{
				"user_id": u.ID, "username": u.Username,
				"credit_balance": u.CreditBalance, "threshold": threshold,
			},
		})
		sent++
	}
	if sent > 0 {
		obs.Logger(ctx).Info("已向余额不足的用户本人投递提醒", "users", sent)
	}
}
