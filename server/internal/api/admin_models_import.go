package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/liguopeng80/tokenzen-lite/server/internal/api/respond"
	"github.com/liguopeng80/tokenzen-lite/server/internal/audit"
	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/obs"
	"github.com/liguopeng80/tokenzen-lite/server/internal/pricing"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// maxImportItems 单次批量导入的条数上限。
// 上限存在的意义是让一次导入的处理时长可预期；超出时应分批提交。
const maxImportItems = 200

// markupPercent 取值范围：100 表示按厂商官价平价折算，不加价；
// 上限 1000（十倍）用于拦掉误填（例如把"加价 30%"填成 3000）。
const (
	minMarkupPercent = 100
	maxMarkupPercent = 1000
)

// modelImportItem 是批量导入的一条记录。
// 定价必填：导入的模型直接进入对外目录，缺定价意味着上架即可被零扣费调用。
type modelImportItem struct {
	modelPayload
	Price *priceRequest `json:"price"`
}

type modelImportRequest struct {
	Items []modelImportItem `json:"items"`
	// Overwrite 为 true 时，已存在的同名模型按提交内容覆盖展示信息与定价；
	// 为 false 时跳过，保留站点已有配置。
	Overwrite bool `json:"overwrite"`
}

// modelImportResult 单条记录的处理结果。失败只影响该条，其余记录照常写入。
type modelImportResult struct {
	Name    string              `json:"name"`
	Action  domain.ImportAction `json:"action"`
	Message string              `json:"message"`
}

type modelImportSummary struct {
	Created int                 `json:"created"`
	Updated int                 `json:"updated"`
	Skipped int                 `json:"skipped"`
	Failed  int                 `json:"failed"`
	Results []modelImportResult `json:"results"`
}

// handleAdminImportModels 批量导入模型目录与定价。
//
// 处理粒度为单条：每条记录独立校验、独立事务写入，一条失败不影响其余记录，
// 响应逐条回报处理结果，便于导入方定位到具体是哪一条出了问题。
func (c *catalogAdminController) handleAdminImportModels(w http.ResponseWriter, r *http.Request) {
	var req modelImportRequest
	if !Bind(w, r, &req) {
		return
	}
	if len(req.Items) == 0 {
		respond.Fail(w, http.StatusBadRequest, "导入内容为空")
		return
	}
	if len(req.Items) > maxImportItems {
		respond.Fail(w, http.StatusBadRequest,
			"单次最多导入 "+strconv.Itoa(maxImportItems)+" 个模型，请分批提交")
		return
	}

	ctx := r.Context()
	log := obs.Logger(ctx)
	summary := modelImportSummary{Results: make([]modelImportResult, 0, len(req.Items))}
	seen := make(map[string]bool, len(req.Items))

	for i := range req.Items {
		item := req.Items[i]
		result := c.importOneModel(ctx, &item, req.Overwrite, seen)
		switch result.Action {
		case domain.ImportCreated:
			summary.Created++
		case domain.ImportUpdated:
			summary.Updated++
		case domain.ImportSkipped:
			summary.Skipped++
		default:
			summary.Failed++
		}
		summary.Results = append(summary.Results, result)
	}

	log.Info("模型批量导入完成", "total", len(req.Items), "created", summary.Created,
		"updated", summary.Updated, "skipped", summary.Skipped, "failed", summary.Failed,
		"overwrite", req.Overwrite)
	// 审计记批次统计而非逐条明细：逐条会把一次导入放大成上百条审计记录，
	// 淹没其余管理动作；单条的处理结果已在响应中逐条回报。
	result := domain.AuditSuccess
	if summary.Created+summary.Updated == 0 {
		result = domain.AuditFailure
	}
	c.Audit.Record(r, audit.Entry{
		Action: domain.AuditModelImport, TargetType: domain.AuditTargetModel,
		Result: result,
		After: map[string]any{
			"total": len(req.Items), "created": summary.Created, "updated": summary.Updated,
			"skipped": summary.Skipped, "failed": summary.Failed, "overwrite": req.Overwrite,
		},
	})
	respond.OK(w, summary)
}

// importOneModel 处理单条导入记录，返回该条的处理结果。
func (c *catalogAdminController) importOneModel(ctx context.Context, item *modelImportItem, overwrite bool, seen map[string]bool) modelImportResult {
	fail := func(msg string) modelImportResult {
		return modelImportResult{Name: item.Name, Action: domain.ImportFailed, Message: msg}
	}
	if msg := validateModelPayload(&item.modelPayload); msg != "" {
		return fail(msg)
	}
	if seen[item.Name] {
		return fail("同一批次内模型名重复")
	}
	seen[item.Name] = true

	if item.Price == nil {
		return fail("缺少定价：导入的模型直接进入对外目录，无定价会被零扣费调用")
	}
	price, msg := buildImportPrice(item)
	if msg != "" {
		return fail(msg)
	}

	existing, err := c.Models.GetByName(ctx, item.Name)
	switch {
	case errors.Is(err, store.ErrNotFound):
		m := &store.Model{
			Name: item.Name, DisplayName: item.DisplayName, Description: item.Description,
			Modality: item.Modality, BillingMode: item.BillingMode,
			Status: item.Status, Tags: item.Tags,
			Provider: item.Provider, ContextWindow: item.ContextWindow,
			MaxOutput: item.MaxOutput, Capabilities: capabilitiesJSON(item.Capabilities),
			Alias: item.Alias,
		}
		if err := c.Models.CreateWithPrice(ctx, m, price); err != nil {
			obs.Logger(ctx).Error("导入模型写入失败", "model", item.Name, "error", err)
			return fail("写入失败")
		}
		return modelImportResult{Name: item.Name, Action: domain.ImportCreated}
	case err != nil:
		obs.Logger(ctx).Error("导入模型查询失败", "model", item.Name, "error", err)
		return fail("查询已有模型失败")
	case !overwrite:
		return modelImportResult{Name: item.Name, Action: domain.ImportSkipped,
			Message: "模型已存在，未选择覆盖，原有配置保持不变"}
	}

	fields := map[string]any{
		"display_name": item.DisplayName, "description": item.Description,
		"modality": item.Modality, "billing_mode": item.BillingMode,
		"status": item.Status, "tags": item.Tags,
		"provider": item.Provider, "context_window": item.ContextWindow,
		"max_output": item.MaxOutput, "capabilities": capabilitiesJSON(item.Capabilities),
		"alias": item.Alias,
	}
	if err := c.Models.UpdateWithPrice(ctx, existing.ID, fields, price); err != nil {
		obs.Logger(ctx).Error("导入模型覆盖失败", "model", item.Name, "error", err)
		return fail("覆盖失败")
	}
	return modelImportResult{Name: item.Name, Action: domain.ImportUpdated}
}

// buildImportPrice 校验并构造单价；返回的第二个值非空表示校验未通过。
func buildImportPrice(item *modelImportItem) (*store.ModelPrice, string) {
	p := item.Price
	for _, v := range []int64{p.InputPrice, p.OutputPrice, p.CacheReadPrice,
		p.CacheWritePrice, p.AudioInputPrice, p.AudioOutputPrice, p.PerCallPrice} {
		if v < 0 {
			return nil, "单价不能为负数"
		}
	}
	price := &store.ModelPrice{
		InputPrice: p.InputPrice, OutputPrice: p.OutputPrice,
		CacheReadPrice: p.CacheReadPrice, CacheWritePrice: p.CacheWritePrice,
		AudioInputPrice: p.AudioInputPrice, AudioOutputPrice: p.AudioOutputPrice,
		PerCallPrice: p.PerCallPrice,
	}
	if msg := validatePriceForBillingMode(item.BillingMode, price); msg != "" {
		return nil, msg
	}
	return price, ""
}

// presetModelView 是下发给管理端的一条预置价目：既给出厂商美元官价，
// 也给出按当前汇率、兑换率与加价百分数折算后的积分单价，供导入前预览。
type presetModelView struct {
	pricing.PresetModel
	Price priceRequest `json:"price"`
}

type presetProviderView struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	PricingURL string            `json:"pricing_url"`
	Models     []presetModelView `json:"models"`
}

type presetCatalogView struct {
	PricedAt                  string               `json:"priced_at"`
	Note                      string               `json:"note"`
	MarkupPercent             int                  `json:"markup_percent"`
	UsdCnyRateMilli           int64                `json:"usd_cny_rate_milli"`
	ExchangeRateCreditsPerCNY int64                `json:"exchange_rate_credits_per_cny"`
	Providers                 []presetProviderView `json:"providers"`
}

// handleAdminPricingPresets 返回内置预置价目，并按当前系统汇率与兑换率折算积分单价。
// 折算在服务端完成，管理端预览到的数字与导入后实际生效的单价同源。
func (c *catalogAdminController) handleAdminPricingPresets(w http.ResponseWriter, r *http.Request) {
	markup := minMarkupPercent
	if raw := r.URL.Query().Get("markup_percent"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < minMarkupPercent || n > maxMarkupPercent {
			respond.Fail(w, http.StatusBadRequest,
				"加价百分数须在 "+strconv.Itoa(minMarkupPercent)+"-"+strconv.Itoa(maxMarkupPercent)+
					" 之间（100 = 按官价平价折算）")
			return
		}
		markup = n
	}

	catalog, err := pricing.Presets()
	if err != nil {
		obs.Logger(r.Context()).Error("加载预置价目失败", "error", err)
		respond.Fail(w, http.StatusInternalServerError, "加载预置价目失败")
		return
	}
	ctx := r.Context()
	rateMilli := c.Settings.GetInt64(ctx, "usd_cny_rate_milli")
	creditsPerCNY := c.Settings.GetInt64(ctx, "exchange_rate_credits_per_cny")

	view := presetCatalogView{
		PricedAt: catalog.PricedAt, Note: catalog.Note, MarkupPercent: markup,
		UsdCnyRateMilli: rateMilli, ExchangeRateCreditsPerCNY: creditsPerCNY,
		Providers: make([]presetProviderView, 0, len(catalog.Providers)),
	}
	for _, p := range catalog.Providers {
		models := make([]presetModelView, 0, len(p.Models))
		for _, m := range p.Models {
			cp := m.ToCreditPrice(rateMilli, creditsPerCNY, markup)
			models = append(models, presetModelView{
				PresetModel: m,
				Price: priceRequest{
					InputPrice: cp.InputPrice, OutputPrice: cp.OutputPrice,
					CacheReadPrice: cp.CacheReadPrice, CacheWritePrice: cp.CacheWritePrice,
					AudioInputPrice: cp.AudioInputPrice, AudioOutputPrice: cp.AudioOutputPrice,
					PerCallPrice: cp.PerCallPrice,
				},
			})
		}
		view.Providers = append(view.Providers, presetProviderView{
			ID: p.ID, Name: p.Name, PricingURL: p.PricingURL, Models: models,
		})
	}
	respond.OK(w, view)
}

// importRemoteTimeout 远程预置拉取的 HTTP 超时。
// importRemoteMaxBytes 远程响应体大小上限，防止恶意端点撑爆内存。
const (
	importRemoteTimeout  = 15 * time.Second
	importRemoteMaxBytes = 10 << 20 // 10 MiB
)

type importRemoteRequest struct {
	SourceURL     string `json:"source_url"`
	MarkupPercent int    `json:"markup_percent"`
	Overwrite     bool   `json:"overwrite"`
}

// handleAdminImportRemote 从远程 URL 拉取预置价目（PresetCatalog），
// 按当前汇率与兑换率折算为积分单价后复用 importOneModel 逐条导入。
// 用于站点在发版之间刷新预置价目。
//
// 安全：外部 HTTP 须 timeout + 大小上限 + 仅 http/https + 状态码校验，
// 防 SSRF 与内存暴涨（observability 规范的外部依赖三要素）。
func (c *catalogAdminController) handleAdminImportRemote(w http.ResponseWriter, r *http.Request) {
	var req importRemoteRequest
	if !Bind(w, r, &req) {
		return
	}
	if req.SourceURL == "" {
		respond.Fail(w, http.StatusBadRequest, "source_url 必填")
		return
	}
	// scheme 校验：仅允许 http/https，拦掉 file://、ftp:// 等可能读本地或非 HTTP 服务的 scheme。
	parsed, err := url.Parse(req.SourceURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		respond.Fail(w, http.StatusBadRequest, "source_url 必须是 http(s) 地址")
		return
	}
	markup := req.MarkupPercent
	if markup == 0 {
		markup = minMarkupPercent
	}
	if markup < minMarkupPercent || markup > maxMarkupPercent {
		respond.Fail(w, http.StatusBadRequest,
			"加价百分数须在 "+strconv.Itoa(minMarkupPercent)+"-"+strconv.Itoa(maxMarkupPercent)+
				" 之间（100 = 按官价平价折算）")
		return
	}

	ctx := r.Context()
	fetchStart := time.Now()
	// HTTP GET：timeout + LimitReader + 状态码校验（仿 alerting/webhook.go 的三要素）。
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, req.SourceURL, nil)
	if err != nil {
		respond.Fail(w, http.StatusBadRequest, "source_url 格式不合法")
		return
	}
	client := &http.Client{Timeout: importRemoteTimeout}
	resp, err := client.Do(httpReq)
	if err != nil {
		obs.Logger(ctx).Error("拉取远程预置价目失败", "url", req.SourceURL, "error", err)
		respond.Fail(w, http.StatusBadGateway, "拉取远程预置价目失败: "+err.Error())
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respond.Fail(w, http.StatusBadGateway,
			"远程返回非 2xx 状态码: "+strconv.Itoa(resp.StatusCode))
		return
	}
	var catalog pricing.PresetCatalog
	if err := json.NewDecoder(io.LimitReader(resp.Body, importRemoteMaxBytes)).Decode(&catalog); err != nil {
		obs.Logger(ctx).Error("解析远程预置价目失败", "url", req.SourceURL, "error", err)
		respond.Fail(w, http.StatusBadRequest, "远程响应不是合法的预置价目 JSON: "+err.Error())
		return
	}
	obs.Logger(ctx).Info("拉取远程预置价目完成",
		"url", req.SourceURL, "status", resp.StatusCode,
		"fetch_ms", time.Since(fetchStart).Milliseconds(),
		"providers", len(catalog.Providers))

	rateMilli := c.Settings.GetInt64(ctx, "usd_cny_rate_milli")
	creditsPerCNY := c.Settings.GetInt64(ctx, "exchange_rate_credits_per_cny")

	// 把 PresetCatalog 展平为 modelImportItem 列表，复用 importOneModel 逐条写入。
	totalModels := 0
	for _, p := range catalog.Providers {
		totalModels += len(p.Models)
	}
	items := make([]modelImportItem, 0, totalModels)
	for _, p := range catalog.Providers {
		for _, m := range p.Models {
			cp := m.ToCreditPrice(rateMilli, creditsPerCNY, markup)
			items = append(items, modelImportItem{
				modelPayload: modelPayload{
					Name: m.Name, DisplayName: m.DisplayName, Description: m.Description,
					Modality: domain.Modality(m.Modality), BillingMode: domain.BillingMode(m.BillingMode),
					Status: domain.ModelEnabled, Provider: m.Provider,
					ContextWindow: m.ContextWindow, MaxOutput: m.MaxOutput,
					Capabilities: m.Capabilities, Alias: m.Alias,
				},
				Price: &priceRequest{
					InputPrice: cp.InputPrice, OutputPrice: cp.OutputPrice,
					CacheReadPrice: cp.CacheReadPrice, CacheWritePrice: cp.CacheWritePrice,
					AudioInputPrice: cp.AudioInputPrice, AudioOutputPrice: cp.AudioOutputPrice,
					PerCallPrice: cp.PerCallPrice,
				},
			})
		}
	}
	if len(items) == 0 {
		respond.Fail(w, http.StatusBadRequest, "远程预置价目为空")
		return
	}
	if len(items) > maxImportItems {
		respond.Fail(w, http.StatusBadRequest,
			"单次最多导入 "+strconv.Itoa(maxImportItems)+" 个模型，远程价目含 "+strconv.Itoa(len(items))+
				" 个，请分批或缩减远程价目")
		return
	}

	summary := modelImportSummary{Results: make([]modelImportResult, 0, len(items))}
	seen := make(map[string]bool, len(items))
	for i := range items {
		item := items[i]
		result := c.importOneModel(ctx, &item, req.Overwrite, seen)
		switch result.Action {
		case domain.ImportCreated:
			summary.Created++
		case domain.ImportUpdated:
			summary.Updated++
		case domain.ImportSkipped:
			summary.Skipped++
		default:
			summary.Failed++
		}
		summary.Results = append(summary.Results, result)
	}

	obs.Logger(ctx).Info("远程预置价目导入完成", "total", len(items),
		"created", summary.Created, "updated", summary.Updated,
		"skipped", summary.Skipped, "failed", summary.Failed, "overwrite", req.Overwrite)
	result := domain.AuditSuccess
	if summary.Created+summary.Updated == 0 {
		result = domain.AuditFailure
	}
	c.Audit.Record(r, audit.Entry{
		Action: domain.AuditModelImport, TargetType: domain.AuditTargetModel,
		Result: result,
		After: map[string]any{
			"source_url": req.SourceURL, "total": len(items),
			"created": summary.Created, "updated": summary.Updated,
			"skipped": summary.Skipped, "failed": summary.Failed, "overwrite": req.Overwrite,
		},
	})
	respond.OK(w, summary)
}
