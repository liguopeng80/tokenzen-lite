package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/datatypes"

	"github.com/liguopeng80/tokenzen-lite/server/internal/api/respond"
	"github.com/liguopeng80/tokenzen-lite/server/internal/audit"
	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/obs"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// isUniqueViolationErr 判断是否为 PG 唯一约束违例（SQLSTATE 23505），
// 用于区分模型名冲突与别名冲突等唯一性错误。GORM 未开启 TranslateError，
// 故直接 errors.As 到底层 pgx 驱动错误。
func isUniqueViolationErr(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23505"
	}
	return false
}

// capabilitiesJSON 把能力标签列表转为 JSONB 字段值，空列表返回 "[]" 而非 nil，
// 因为 models.capabilities 列为 NOT NULL。toJSONField 对 nil 返回 nil，
// 直接用于 capabilities 会让 NOT NULL 约束违例。
func capabilitiesJSON(caps []string) datatypes.JSON {
	if len(caps) == 0 {
		return datatypes.JSON("[]")
	}
	return toJSONField(caps)
}

// adminModelItem 管理端模型列表条目：附带启用渠道承载数，
// 承载数为 0 说明模型上架后用户调用必然失败，便于上架前自检。
type adminModelItem struct {
	store.Model
	ChannelCount int64 `json:"channel_count"`
}

func (c *catalogAdminController) handleAdminListModels(w http.ResponseWriter, r *http.Request) {
	page, pageSize := PageParams(r)
	q := r.URL.Query()
	models, total, err := c.Models.List(r.Context(), store.ModelListFilter{
		Keyword:     q.Get("keyword"),
		Status:      domain.ModelStatus(q.Get("status")),
		Modality:    domain.Modality(q.Get("modality")),
		Provider:    domain.Provider(q.Get("provider")),
		Page:        page,
		PageSize:    pageSize,
		WithDetails: true,
	})
	if err != nil {
		obs.Logger(r.Context()).Error("查询模型列表失败", "error", err)
		respond.Fail(w, http.StatusInternalServerError, "查询模型列表失败")
		return
	}
	counts, err := c.Channels.CountEnabledByModel(r.Context())
	if err != nil {
		obs.Logger(r.Context()).Error("统计模型承载渠道失败", "error", err)
		respond.Fail(w, http.StatusInternalServerError, "查询模型列表失败")
		return
	}
	items := make([]adminModelItem, 0, len(models))
	for _, m := range models {
		items = append(items, adminModelItem{Model: m, ChannelCount: counts[m.Name]})
	}
	respond.OK(w, respond.NewPage(page, pageSize, total, items))
}

type modelPayload struct {
	Name          string             `json:"name"`
	DisplayName   string             `json:"display_name"`
	Description   string             `json:"description"`
	Modality      domain.Modality    `json:"modality"`
	BillingMode   domain.BillingMode `json:"billing_mode"`
	Status        domain.ModelStatus `json:"status"`
	Tags          string             `json:"tags"`
	Provider      string             `json:"provider"`
	ContextWindow int64              `json:"context_window"`
	MaxOutput     int64              `json:"max_output"`
	Capabilities  []string           `json:"capabilities"`
	Alias         string             `json:"alias"`
}

// validCapabilities 受控的能力标签取值（docs/glossary.md「模型能力属性」节）。
var validCapabilities = map[string]bool{
	"vision":    true,
	"video":     true,
	"audio":     true,
	"reasoning": true,
}

func validateModelPayload(p *modelPayload) string {
	if p.Name == "" || len(p.Name) > 128 {
		return "模型名称必填且不超过 128 字符"
	}
	if p.Modality == "" {
		p.Modality = domain.ModalityText
	}
	if p.Modality != domain.ModalityText && p.Modality != domain.ModalityEmbedding && p.Modality != domain.ModalityImage {
		return "模型形态取值不合法"
	}
	if p.BillingMode == "" {
		p.BillingMode = domain.BillPerToken
	}
	if p.BillingMode != domain.BillPerToken && p.BillingMode != domain.BillPerCall {
		return "计费方式取值不合法"
	}
	if p.Status == "" {
		p.Status = domain.ModelEnabled
	}
	if p.Status != domain.ModelEnabled && p.Status != domain.ModelDisabled {
		return "状态取值不合法"
	}
	// provider 非空时须是合法厂商；空合法（旧模型迁移后未填归属）。
	if p.Provider != "" && !domain.ValidProvider(domain.Provider(p.Provider)) {
		return "厂商取值不合法"
	}
	if p.ContextWindow < 0 || p.MaxOutput < 0 {
		return "上下文窗口与最大输出不能为负数"
	}
	for _, c := range p.Capabilities {
		if !validCapabilities[c] {
			return "能力标签取值不合法: " + c
		}
	}
	return ""
}

func (c *catalogAdminController) handleAdminCreateModel(w http.ResponseWriter, r *http.Request) {
	var req modelPayload
	if !Bind(w, r, &req) {
		return
	}
	if msg := validateModelPayload(&req); msg != "" {
		respond.Fail(w, http.StatusBadRequest, msg)
		return
	}
	m := &store.Model{
		Name: req.Name, DisplayName: req.DisplayName, Description: req.Description,
		Modality: req.Modality, BillingMode: req.BillingMode, Status: req.Status, Tags: req.Tags,
		Provider: req.Provider, ContextWindow: req.ContextWindow, MaxOutput: req.MaxOutput,
		Capabilities: capabilitiesJSON(req.Capabilities), Alias: req.Alias,
	}
	if err := c.Models.Create(r.Context(), m); err != nil {
		if isUniqueViolationErr(err) {
			respond.Fail(w, http.StatusConflict, "模型名称或别名已存在")
			return
		}
		respond.Fail(w, http.StatusConflict, "模型名称已存在")
		return
	}
	c.Audit.Record(r, audit.Entry{
		Action: domain.AuditModelCreate, TargetType: domain.AuditTargetModel,
		TargetID: m.ID, TargetName: m.Name,
		After: map[string]any{
			"name": m.Name, "display_name": m.DisplayName, "modality": m.Modality,
			"billing_mode": m.BillingMode, "status": m.Status,
			"provider": m.Provider, "context_window": m.ContextWindow,
			"max_output": m.MaxOutput, "capabilities": m.Capabilities, "alias": m.Alias,
		},
	})
	respond.Created(w, m)
}

func (c *catalogAdminController) handleAdminGetModel(w http.ResponseWriter, r *http.Request) {
	id, ok := IDParam(w, r, "id")
	if !ok {
		return
	}
	m, err := c.Models.GetByID(r.Context(), id)
	if err != nil {
		respond.Fail(w, http.StatusNotFound, "模型不存在")
		return
	}
	respond.OK(w, m)
}

func (c *catalogAdminController) handleAdminUpdateModel(w http.ResponseWriter, r *http.Request) {
	id, ok := IDParam(w, r, "id")
	if !ok {
		return
	}
	var req modelPayload
	if !Bind(w, r, &req) {
		return
	}
	if msg := validateModelPayload(&req); msg != "" {
		respond.Fail(w, http.StatusBadRequest, msg)
		return
	}
	m, err := c.Models.GetByID(r.Context(), id)
	if err != nil {
		respond.Fail(w, http.StatusNotFound, "模型不存在")
		return
	}
	// 模型名称是渠道路由、渠道成本、密钥白名单与用量日志的关联键（按名称字符串关联），
	// 改名会使这些关联静默断链，因此禁止修改；如需改名应新建模型并下架旧模型。
	if req.Name != m.Name {
		respond.Fail(w, http.StatusBadRequest,
			"模型名称不可修改：渠道路由、成本、密钥白名单与用量日志均按名称关联，改名会导致关联失效；请新建模型并下架旧模型")
		return
	}
	// 已配置定价的模型变更计费方式时，定价必须与新计费方式一致，
	// 防止改完计费方式后模型带着全零单价被免费调用。
	if m.Price != nil {
		if msg := validatePriceForBillingMode(req.BillingMode, m.Price); msg != "" {
			respond.Fail(w, http.StatusBadRequest, msg+"（请先调整定价再变更计费方式）")
			return
		}
	}
	fields := map[string]any{
		"display_name": req.DisplayName, "description": req.Description,
		"modality": req.Modality, "billing_mode": req.BillingMode,
		"status": req.Status, "tags": req.Tags,
		"provider": req.Provider, "context_window": req.ContextWindow,
		"max_output": req.MaxOutput, "capabilities": capabilitiesJSON(req.Capabilities),
		"alias": req.Alias,
	}
	if err = c.Models.UpdateFields(r.Context(), id, fields); err != nil {
		if isUniqueViolationErr(err) {
			respond.Fail(w, http.StatusConflict, "模型别名已存在：对外别名全局唯一")
			return
		}
		respond.Fail(w, http.StatusNotFound, "模型不存在")
		return
	}
	c.Audit.Record(r, audit.Entry{
		Action: domain.AuditModelUpdate, TargetType: domain.AuditTargetModel,
		TargetID: id, TargetName: m.Name,
		Before: map[string]any{
			"display_name": m.DisplayName, "description": m.Description,
			"modality": m.Modality, "billing_mode": m.BillingMode,
			"status": m.Status, "tags": m.Tags,
			"provider": m.Provider, "context_window": m.ContextWindow,
			"max_output": m.MaxOutput, "capabilities": m.Capabilities, "alias": m.Alias,
		},
		After: fields,
	})
	respond.OK(w, nil)
}

func (c *catalogAdminController) handleAdminDeleteModel(w http.ResponseWriter, r *http.Request) {
	id, ok := IDParam(w, r, "id")
	if !ok {
		return
	}
	// 先取名称快照：删除后审计里只剩 ID，无法回答「删掉的是哪个模型」。
	m, err := c.Models.GetByID(r.Context(), id)
	if err != nil {
		respond.Fail(w, http.StatusNotFound, "模型不存在")
		return
	}
	if err := c.Models.Delete(r.Context(), id); err != nil {
		respond.Fail(w, http.StatusNotFound, "模型不存在")
		return
	}
	c.Audit.Record(r, audit.Entry{
		Action: domain.AuditModelDelete, TargetType: domain.AuditTargetModel,
		TargetID: id, TargetName: m.Name,
		Before: map[string]any{"name": m.Name, "status": m.Status},
	})
	respond.OK(w, nil)
}

type priceRequest struct {
	InputPrice       int64 `json:"input_price"`
	OutputPrice      int64 `json:"output_price"`
	CacheReadPrice   int64 `json:"cache_read_price"`
	CacheWritePrice  int64 `json:"cache_write_price"`
	AudioInputPrice  int64 `json:"audio_input_price"`
	AudioOutputPrice int64 `json:"audio_output_price"`
	PerCallPrice     int64 `json:"per_call_price"`
}

// validatePriceForBillingMode 校验定价与计费方式一致：按 token 计费至少一项 token 单价非零，
// 按次计费必须配置非零按次单价。全零单价会让模型被零扣费调用。
func validatePriceForBillingMode(mode domain.BillingMode, p *store.ModelPrice) string {
	if mode == domain.BillPerCall {
		if p.PerCallPrice <= 0 {
			return "按次计费的模型必须配置非零的按次单价"
		}
		return ""
	}
	if p.InputPrice == 0 && p.OutputPrice == 0 &&
		p.CacheReadPrice == 0 && p.CacheWritePrice == 0 &&
		p.AudioInputPrice == 0 && p.AudioOutputPrice == 0 {
		return "按 token 计费的模型至少需要一项非零的 token 单价"
	}
	return ""
}

func (c *catalogAdminController) handleAdminSetModelPrice(w http.ResponseWriter, r *http.Request) {
	id, ok := IDParam(w, r, "id")
	if !ok {
		return
	}
	m, err := c.Models.GetByID(r.Context(), id)
	if err != nil {
		respond.Fail(w, http.StatusNotFound, "模型不存在")
		return
	}
	var req priceRequest
	if !Bind(w, r, &req) {
		return
	}
	for _, v := range []int64{req.InputPrice, req.OutputPrice, req.CacheReadPrice,
		req.CacheWritePrice, req.AudioInputPrice, req.AudioOutputPrice, req.PerCallPrice} {
		if v < 0 {
			respond.Fail(w, http.StatusBadRequest, "单价不能为负数")
			return
		}
	}
	p := &store.ModelPrice{
		ModelID: id, InputPrice: req.InputPrice, OutputPrice: req.OutputPrice,
		CacheReadPrice: req.CacheReadPrice, CacheWritePrice: req.CacheWritePrice,
		AudioInputPrice: req.AudioInputPrice, AudioOutputPrice: req.AudioOutputPrice,
		PerCallPrice: req.PerCallPrice,
	}
	if msg := validatePriceForBillingMode(m.BillingMode, p); msg != "" {
		respond.Fail(w, http.StatusBadRequest, msg)
		return
	}
	if err := c.Models.UpsertPrice(r.Context(), p); err != nil {
		respond.Fail(w, http.StatusInternalServerError, "保存价格失败")
		return
	}
	obs.Logger(r.Context()).Info("模型价格更新", "model_id", id)
	c.Audit.Record(r, audit.Entry{
		Action: domain.AuditModelPriceChange, TargetType: domain.AuditTargetModel,
		TargetID: id, TargetName: m.Name,
		Before: priceSnapshot(m.Price), After: priceSnapshot(p),
	})
	respond.OK(w, p)
}

type peakRulePayload struct {
	Timezone          string `json:"timezone"`
	StartMinute       int    `json:"start_minute"`
	EndMinute         int    `json:"end_minute"`
	DaysOfWeek        []int  `json:"days_of_week"`
	MultiplierPercent int    `json:"multiplier_percent"`
	Enabled           bool   `json:"enabled"`
}

func (c *catalogAdminController) handleAdminSetPeakRules(w http.ResponseWriter, r *http.Request) {
	id, ok := IDParam(w, r, "id")
	if !ok {
		return
	}
	if _, err := c.Models.GetByID(r.Context(), id); err != nil {
		respond.Fail(w, http.StatusNotFound, "模型不存在")
		return
	}
	var req struct {
		Rules []peakRulePayload `json:"rules"`
	}
	if !Bind(w, r, &req) {
		return
	}
	rules := make([]store.ModelPeakRule, 0, len(req.Rules))
	for _, p := range req.Rules {
		if p.Timezone == "" {
			p.Timezone = "Asia/Shanghai"
		}
		if _, err := time.LoadLocation(p.Timezone); err != nil {
			respond.Fail(w, http.StatusBadRequest, "时区不合法: "+p.Timezone)
			return
		}
		if p.StartMinute < 0 || p.EndMinute > 1440 || p.StartMinute >= p.EndMinute {
			respond.Fail(w, http.StatusBadRequest, "时段范围不合法（跨午夜请拆成两条规则）")
			return
		}
		if p.MultiplierPercent < 100 {
			respond.Fail(w, http.StatusBadRequest, "倍率百分数不能低于 100")
			return
		}
		if len(p.DaysOfWeek) == 0 {
			p.DaysOfWeek = []int{1, 2, 3, 4, 5, 6, 7}
		}
		for _, day := range p.DaysOfWeek {
			if day < 1 || day > 7 {
				respond.Fail(w, http.StatusBadRequest, "星期取值须在 1-7 之间")
				return
			}
		}
		rules = append(rules, store.ModelPeakRule{
			Timezone: p.Timezone, StartMinute: p.StartMinute, EndMinute: p.EndMinute,
			DaysOfWeek: toJSONField(p.DaysOfWeek), MultiplierPercent: p.MultiplierPercent,
			Enabled: p.Enabled,
		})
	}
	if err := c.Models.ReplacePeakRules(r.Context(), id, rules); err != nil {
		respond.Fail(w, http.StatusInternalServerError, "保存时段规则失败")
		return
	}
	obs.Logger(r.Context()).Info("模型时段倍率更新", "model_id", id, "rule_count", len(rules))
	c.Audit.Record(r, audit.Entry{
		Action: domain.AuditModelPeakRules, TargetType: domain.AuditTargetModel,
		TargetID: id, After: map[string]any{"rules": req.Rules},
	})
	respond.OK(w, nil)
}

// priceSnapshot 把模型定价压成审计用的字段快照；未配置定价时返回 nil。
func priceSnapshot(p *store.ModelPrice) map[string]any {
	if p == nil {
		return nil
	}
	return map[string]any{
		"input_price": p.InputPrice, "output_price": p.OutputPrice,
		"cache_read_price": p.CacheReadPrice, "cache_write_price": p.CacheWritePrice,
		"audio_input_price": p.AudioInputPrice, "audio_output_price": p.AudioOutputPrice,
		"per_call_price": p.PerCallPrice,
	}
}
