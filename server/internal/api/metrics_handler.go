package api

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/liguopeng80/tokenzen-lite/server/internal/api/respond"
	"github.com/liguopeng80/tokenzen-lite/server/internal/auth"
	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/obs"
)

// handleMetrics 以 Prometheus 文本格式导出运行指标。
//
// 该端点不套用 {success, message, data} 信封：消费者是抓取端，不经过前端的统一解析。
// 访问控制有两条路径——配置了 TZL_METRICS_TOKEN 时接受 Bearer 令牌（抓取端用），
// 或 root 会话（人工查看用）。指标中含模型名、渠道 ID 与各接口的调用量，
// 这些是内部运营信息，不匿名开放。
func (s *systemController) handleMetrics(w http.ResponseWriter, r *http.Request) {
	if !s.metricsAllowed(r) {
		respond.Fail(w, http.StatusUnauthorized, "需要指标访问令牌或超级管理员会话")
		return
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(obs.DefaultMetrics().Export()))
}

func (s *systemController) metricsAllowed(r *http.Request) bool {
	if s.Cfg != nil && s.Cfg.MetricsToken != "" {
		token := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer"))
		// 定长比较：令牌校验不应因比较提前返回而泄漏前缀信息。
		if subtle.ConstantTimeCompare([]byte(token), []byte(s.Cfg.MetricsToken)) == 1 {
			return true
		}
	}
	if s.Sessions == nil || s.Users == nil {
		return false
	}
	uid := auth.SessionUserID(s.Sessions, r)
	if uid == 0 {
		return false
	}
	u, err := s.Users.GetByID(r.Context(), uid)
	return err == nil && u.Status == domain.UserEnabled && u.Role.AtLeast(domain.RoleRoot)
}
