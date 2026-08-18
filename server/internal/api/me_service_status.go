package api

import (
	"net/http"
	"sort"
	"time"

	"github.com/liguopeng80/tokenzen-lite/server/internal/api/respond"
	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/obs"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// 门户「服务状态」反映的是本站网关自己的可用性，不是厂商官方状态页。
//
// 员工调用失败时来查服务状态，要能看出「是本站的上游被禁用了」还是「本站一切正常，
// 问题在我这边」。厂商官方状态页与本站实际配置的渠道无关：本站可能只接了其中一家，
// 也可能接了官方状态页上根本没有的厂商；而且那些数据由员工浏览器直连公网获取，
// 企业内网部署时通常取不到。
//
// 返回口径按员工可见范围裁剪：只给厂商级的通道可用数量与本站近期的成功率、耗时，
// 不含渠道名称、地址、优先级等配置细节。

// serviceStatusLevel 是页面展示用的三档状态。
type serviceStatusLevel string

const (
	serviceOperational serviceStatusLevel = "operational"
	serviceDegraded    serviceStatusLevel = "degraded"
	serviceOutage      serviceStatusLevel = "outage"
)

// providerStatus 是一家上游厂商在本站的通道可用情况。
type providerStatus struct {
	Provider domain.Provider `json:"provider"`
	Total    int64           `json:"total"`
	Enabled  int64           `json:"enabled"`
	// AutoDisabled 因连续失败被系统自动禁用的通道数，是「上游出问题」的直接信号。
	AutoDisabled int64              `json:"auto_disabled"`
	Status       serviceStatusLevel `json:"status"`
}

// relayWindow 是一个时间窗口内本站中继的健康度。
type relayWindow struct {
	WindowMinutes      int   `json:"window_minutes"`
	Requests           int64 `json:"requests"`
	Failed             int64 `json:"failed"`
	FailureRatePercent int64 `json:"failure_rate_percent"`
	P95LatencyMS       int64 `json:"p95_latency_ms"`
}

type serviceStatusResponse struct {
	Status    serviceStatusLevel `json:"status"`
	CheckedAt time.Time          `json:"checked_at"`
	// ModelsTotal / ModelsAvailable 是已上架模型中当前有渠道承载的数量。
	ModelsTotal       int              `json:"models_total"`
	ModelsAvailable   int              `json:"models_available"`
	UnavailableModels []string         `json:"unavailable_models"`
	Providers         []providerStatus `json:"providers"`
	RecentHour        relayWindow      `json:"recent_hour"`
	RecentDay         relayWindow      `json:"recent_day"`
}

func (c *meStatsController) handleMeServiceStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	channels, _, err := c.Channels.List(ctx, store.ChannelListFilter{Page: 1, PageSize: maxStatusChannels})
	if err != nil {
		obs.Logger(ctx).Error("查询渠道状态失败", "error", err)
		respond.Fail(w, http.StatusInternalServerError, "查询服务状态失败")
		return
	}
	models, _, err := c.Models.List(ctx, store.ModelListFilter{Status: domain.ModelEnabled})
	if err != nil {
		obs.Logger(ctx).Error("查询已上架模型失败", "error", err)
		respond.Fail(w, http.StatusInternalServerError, "查询服务状态失败")
		return
	}
	carriers, err := c.Channels.CountEnabledByModel(ctx)
	if err != nil {
		obs.Logger(ctx).Error("统计模型承载渠道失败", "error", err)
		respond.Fail(w, http.StatusInternalServerError, "查询服务状态失败")
		return
	}

	now := time.Now()
	hour, err := c.UsageLogs.WindowHealth(ctx, now.Add(-time.Hour))
	if err != nil {
		obs.Logger(ctx).Error("统计中继健康度失败", "error", err)
		respond.Fail(w, http.StatusInternalServerError, "查询服务状态失败")
		return
	}
	day, err := c.UsageLogs.WindowHealth(ctx, now.Add(-24*time.Hour))
	if err != nil {
		obs.Logger(ctx).Error("统计中继健康度失败", "error", err)
		respond.Fail(w, http.StatusInternalServerError, "查询服务状态失败")
		return
	}

	resp := serviceStatusResponse{
		CheckedAt:   now,
		ModelsTotal: len(models),
		Providers:   summarizeProviders(channels),
		RecentHour:  toRelayWindow(60, hour),
		RecentDay:   toRelayWindow(24*60, day),
	}
	for _, m := range models {
		if carriers[m.Name] > 0 {
			resp.ModelsAvailable++
			continue
		}
		resp.UnavailableModels = append(resp.UnavailableModels, m.Name)
	}
	if resp.UnavailableModels == nil {
		resp.UnavailableModels = []string{}
	}
	errorRateThreshold := c.Settings.GetInt64(ctx, "alert_error_rate_percent")
	minRequests := c.Settings.GetInt64(ctx, "alert_error_rate_min_requests")
	resp.Status = overallServiceStatus(resp, hour, errorRateThreshold, minRequests)
	respond.OK(w, resp)
}

// maxStatusChannels 是状态页统计的渠道条数上限。内部网关的渠道数是两位数量级，
// 取一页足够；超出部分不参与统计，宁可少算也不做无上限查询。
const maxStatusChannels = 500

// summarizeProviders 把渠道按厂商聚合。只输出数量与状态，不含渠道名称与地址。
func summarizeProviders(channels []store.Channel) []providerStatus {
	byProvider := map[domain.Provider]*providerStatus{}
	for i := range channels {
		c := channels[i]
		row, ok := byProvider[c.Provider]
		if !ok {
			row = &providerStatus{Provider: c.Provider}
			byProvider[c.Provider] = row
		}
		row.Total++
		switch c.Status {
		case domain.ChannelEnabled:
			row.Enabled++
		case domain.ChannelAutoDisabled:
			row.AutoDisabled++
		}
	}
	out := make([]providerStatus, 0, len(byProvider))
	for _, row := range byProvider {
		switch {
		case row.Enabled == 0:
			row.Status = serviceOutage
		case row.AutoDisabled > 0:
			row.Status = serviceDegraded
		default:
			row.Status = serviceOperational
		}
		out = append(out, *row)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Provider < out[j].Provider })
	return out
}

func toRelayWindow(minutes int, h store.RelayHealth) relayWindow {
	return relayWindow{
		WindowMinutes: minutes, Requests: h.Total, Failed: h.Failed,
		FailureRatePercent: h.FailureRatePercent(), P95LatencyMS: h.P95LatencyMS,
	}
}

// overallServiceStatus 汇总总体状态。判定顺序即严重程度：一个模型都用不了是中断；
// 部分模型不可用、有通道被自动禁用、近一小时失败率超过告警阈值都算受影响。
// 失败率只在请求数达到告警口径的最小样本量时才参与判定，避免两三次调用失败就报警。
func overallServiceStatus(resp serviceStatusResponse, hour store.RelayHealth,
	errorRateThreshold, minRequests int64) serviceStatusLevel {

	if resp.ModelsTotal == 0 || resp.ModelsAvailable == 0 {
		return serviceOutage
	}
	if len(resp.UnavailableModels) > 0 {
		return serviceDegraded
	}
	for _, p := range resp.Providers {
		if p.Status != serviceOperational {
			return serviceDegraded
		}
	}
	if errorRateThreshold > 0 && hour.Total >= minRequests &&
		hour.FailureRatePercent() >= errorRateThreshold {
		return serviceDegraded
	}
	return serviceOperational
}
