package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/liguopeng80/tokenzen-lite/server/internal/api/respond"
	"github.com/liguopeng80/tokenzen-lite/server/internal/audit"
	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/obs"
	"github.com/liguopeng80/tokenzen-lite/server/internal/relay"
	"github.com/liguopeng80/tokenzen-lite/server/internal/secrets"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// catalogAdminController 承载运营桶的目录与配额端点（admin 角色）：
// 渠道 CRUD/状态/测试/成本、模型 CRUD/价格/时段规则/导入、项目 CRUD。
// 跨 admin_channels / admin_models / admin_models_import / admin_projects 四文件共享。
type catalogAdminController struct {
	Audit       *audit.Recorder
	Channels    *store.ChannelRepo
	Costs       *store.ChannelCostRepo
	Models      *store.ModelRepo
	Relay       *relay.Engine
	Secrets     *secrets.Box
	Settings    *store.SettingsRepo
	Projects    *store.ProjectRepo
	Idempotency *store.IdempotencyRepo
}

func (c *catalogAdminController) handleAdminListChannels(w http.ResponseWriter, r *http.Request) {
	page, pageSize := PageParams(r)
	q := r.URL.Query()
	channels, total, err := c.Channels.List(r.Context(), store.ChannelListFilter{
		Keyword: q.Get("keyword"),
		Status:  domain.ChannelStatus(q.Get("status")),
		Model:   q.Get("model"),
		Page:    page, PageSize: pageSize,
	})
	if err != nil {
		respond.Fail(w, http.StatusInternalServerError, "查询渠道列表失败")
		return
	}
	respond.OK(w, respond.NewPage(page, pageSize, total, channels))
}

type channelPayload struct {
	Name           string                 `json:"name"`
	Provider       domain.Provider        `json:"provider"`
	Protocol       domain.ChannelProtocol `json:"protocol"`
	BaseURL        string                 `json:"base_url"`
	APIKey         string                 `json:"api_key"` // 明文入参，落库前加密
	Models         []string               `json:"models"`
	ModelMapping   map[string]string      `json:"model_mapping"`
	Priority       *int                   `json:"priority"`
	Weight         *int                   `json:"weight"`
	ParamOverride  *map[string]any        `json:"param_override"`  // 指针语义：字段缺席保持原值
	HeaderOverride *map[string]string     `json:"header_override"` // 指针语义：字段缺席保持原值
	TestModel      string                 `json:"test_model"`
}

func validateChannelPayload(p *channelPayload) string {
	if p.Name == "" {
		return "渠道名称必填"
	}
	if !domain.ValidProvider(p.Provider) {
		return "厂商取值不合法"
	}
	if !domain.ValidProtocol(p.Protocol) {
		return "协议取值不合法"
	}
	if p.BaseURL == "" {
		return "base_url 必填"
	}
	if len(p.Models) == 0 {
		return "渠道至少配置一个模型"
	}
	return ""
}

// validateChannelModelProtocols 校验渠道协议能否承载模型清单中各模型的形态：
// 向量/图像模型仅 openai_compat 协议渠道可承载（支持矩阵见 docs/glossary.md
// 的 ChannelProtocol 节）。未进入模型目录的名称跳过（允许预先配置未上架模型）。
// 返回非空字符串表示校验失败的提示消息。
func (c *catalogAdminController) validateChannelModelProtocols(ctx context.Context, protocol domain.ChannelProtocol, models []string) string {
	for _, name := range models {
		m, err := c.Models.GetByName(ctx, name)
		if err != nil {
			continue
		}
		if !domain.ProtocolSupportsModality(protocol, m.Modality) {
			return fmt.Sprintf("模型 %s 的形态为 %s，仅支持 OpenAI 兼容协议（openai_compat）渠道，不能配置在 %s 协议渠道上",
				name, m.Modality, protocol)
		}
	}
	return ""
}

func (c *catalogAdminController) handleAdminCreateChannel(w http.ResponseWriter, r *http.Request) {
	var req channelPayload
	if !Bind(w, r, &req) {
		return
	}
	if msg := validateChannelPayload(&req); msg != "" {
		respond.Fail(w, http.StatusBadRequest, msg)
		return
	}
	if req.APIKey == "" {
		respond.Fail(w, http.StatusBadRequest, "上游 API Key 必填")
		return
	}
	if msg := c.validateChannelModelProtocols(r.Context(), req.Protocol, req.Models); msg != "" {
		respond.Fail(w, http.StatusBadRequest, msg)
		return
	}
	encrypted, err := c.Secrets.Encrypt(req.APIKey)
	if err != nil {
		respond.Fail(w, http.StatusInternalServerError, "密钥加密失败")
		return
	}
	priority, weight := 0, 1
	if req.Priority != nil {
		priority = *req.Priority
	}
	if req.Weight != nil {
		weight = *req.Weight
	}
	ch := &store.Channel{
		Name: req.Name, Provider: req.Provider, Protocol: req.Protocol,
		BaseURL: req.BaseURL, APIKeyEncrypted: encrypted,
		Models:       toJSONField(req.Models),
		ModelMapping: orEmptyObject(req.ModelMapping),
		Status:       domain.ChannelEnabled,
		Priority:     priority, Weight: weight,
		ParamOverride:  orEmptyObjectPtr(req.ParamOverride),
		HeaderOverride: orEmptyObjectPtr(req.HeaderOverride),
		TestModel:      req.TestModel,
	}
	if err := c.Channels.Create(r.Context(), ch); err != nil {
		obs.Logger(r.Context()).Error("创建渠道失败", "error", err)
		respond.Fail(w, http.StatusInternalServerError, "创建渠道失败")
		return
	}
	obs.Logger(r.Context()).Info("创建渠道", "channel_id", ch.ID, "provider", ch.Provider)
	c.Audit.Record(r, audit.Entry{
		Action: domain.AuditChannelCreate, TargetType: domain.AuditTargetChannel,
		TargetID: ch.ID, TargetName: ch.Name,
		After: map[string]any{
			"name": ch.Name, "provider": ch.Provider, "protocol": ch.Protocol,
			"base_url": ch.BaseURL, "models": req.Models,
			"priority": ch.Priority, "weight": ch.Weight,
			"api_key": domain.AuditRedacted,
		},
	})
	respond.Created(w, ch)
}

func orEmptyObject(m map[string]string) []byte {
	if m == nil {
		return []byte("{}")
	}
	return toJSONField(m)
}

// orEmptyObjectPtr 序列化指针指向的映射；指针或映射为 nil 时落空对象（创建路径的缺省值）。
func orEmptyObjectPtr[M map[string]string | map[string]any](m *M) []byte {
	if m == nil || *m == nil {
		return []byte("{}")
	}
	return toJSONField(*m)
}

func (c *catalogAdminController) handleAdminGetChannel(w http.ResponseWriter, r *http.Request) {
	id, ok := IDParam(w, r, "id")
	if !ok {
		return
	}
	ch, err := c.Channels.GetByID(r.Context(), id)
	if err != nil {
		respond.Fail(w, http.StatusNotFound, "渠道不存在")
		return
	}
	respond.OK(w, ch)
}

func (c *catalogAdminController) handleAdminUpdateChannel(w http.ResponseWriter, r *http.Request) {
	id, ok := IDParam(w, r, "id")
	if !ok {
		return
	}
	var req channelPayload
	if !Bind(w, r, &req) {
		return
	}
	if msg := validateChannelPayload(&req); msg != "" {
		respond.Fail(w, http.StatusBadRequest, msg)
		return
	}
	if msg := c.validateChannelModelProtocols(r.Context(), req.Protocol, req.Models); msg != "" {
		respond.Fail(w, http.StatusBadRequest, msg)
		return
	}
	fields := map[string]any{
		"name": req.Name, "provider": req.Provider, "protocol": req.Protocol,
		"base_url": req.BaseURL,
		"models":   toJSONField(req.Models), "model_mapping": orEmptyObject(req.ModelMapping),
		"test_model": req.TestModel,
	}
	if req.Priority != nil {
		fields["priority"] = *req.Priority
	}
	if req.Weight != nil {
		fields["weight"] = *req.Weight
	}
	// 覆盖配置与优先级/权重同款指针语义：字段缺席（或 null）保持原值，传入对象才更新，传 {} 即清空
	if req.ParamOverride != nil {
		fields["param_override"] = orEmptyObjectPtr(req.ParamOverride)
	}
	if req.HeaderOverride != nil {
		fields["header_override"] = orEmptyObjectPtr(req.HeaderOverride)
	}
	if req.APIKey != "" { // 留空表示不更换密钥
		encrypted, err := c.Secrets.Encrypt(req.APIKey)
		if err != nil {
			respond.Fail(w, http.StatusInternalServerError, "密钥加密失败")
			return
		}
		fields["api_key_encrypted"] = encrypted
	}
	before, _ := c.Channels.GetByID(r.Context(), id)
	if err := c.Channels.UpdateFields(r.Context(), id, fields); err != nil {
		respond.Fail(w, http.StatusNotFound, "渠道不存在")
		return
	}
	// 上游密钥只记「是否被更换」，密文与明文都不入审计。
	after := map[string]any{
		"name": req.Name, "provider": req.Provider, "protocol": req.Protocol,
		"base_url": req.BaseURL, "models": req.Models,
	}
	if req.APIKey != "" {
		after["api_key"] = domain.AuditRedacted
	}
	c.Audit.Record(r, audit.Entry{
		Action: domain.AuditChannelUpdate, TargetType: domain.AuditTargetChannel,
		TargetID: id, TargetName: req.Name,
		Before: channelSnapshot(before), After: after,
	})
	respond.OK(w, nil)
}

// channelSnapshot 把渠道压成审计用的字段快照，刻意不含上游密钥。
func channelSnapshot(ch *store.Channel) map[string]any {
	if ch == nil {
		return nil
	}
	return map[string]any{
		"name": ch.Name, "provider": ch.Provider, "protocol": ch.Protocol,
		"base_url": ch.BaseURL, "models": json.RawMessage(nullIfEmptyJSON(ch.Models)),
		"status": ch.Status, "priority": ch.Priority, "weight": ch.Weight,
	}
}

type channelStatusRequest struct {
	Status domain.ChannelStatus `json:"status"`
}

func (c *catalogAdminController) handleAdminSetChannelStatus(w http.ResponseWriter, r *http.Request) {
	id, ok := IDParam(w, r, "id")
	if !ok {
		return
	}
	var req channelStatusRequest
	if !Bind(w, r, &req) {
		return
	}
	if req.Status != domain.ChannelEnabled && req.Status != domain.ChannelManualDisabled {
		respond.Fail(w, http.StatusBadRequest, "状态只能设置为 enabled 或 manual_disabled")
		return
	}
	fields := map[string]any{"status": req.Status}
	if req.Status == domain.ChannelEnabled {
		fields["disabled_reason"] = ""
	}
	before, _ := c.Channels.GetByID(r.Context(), id)
	if err := c.Channels.UpdateFields(r.Context(), id, fields); err != nil {
		respond.Fail(w, http.StatusNotFound, "渠道不存在")
		return
	}
	entry := audit.Entry{
		Action: domain.AuditChannelStatus, TargetType: domain.AuditTargetChannel,
		TargetID: id, After: map[string]any{"status": req.Status},
	}
	if before != nil {
		entry.TargetName = before.Name
		entry.Before = map[string]any{
			"status": before.Status, "disabled_reason": before.DisabledReason,
		}
	}
	c.Audit.Record(r, entry)
	respond.OK(w, nil)
}

func (c *catalogAdminController) handleAdminDeleteChannel(w http.ResponseWriter, r *http.Request) {
	id, ok := IDParam(w, r, "id")
	if !ok {
		return
	}
	// 先取快照：删除后审计里只剩 ID，无法回答「删掉的是哪个渠道」。
	before, _ := c.Channels.GetByID(r.Context(), id)
	if err := c.Channels.Delete(r.Context(), id); err != nil {
		respond.Fail(w, http.StatusNotFound, "渠道不存在")
		return
	}
	entry := audit.Entry{
		Action: domain.AuditChannelDelete, TargetType: domain.AuditTargetChannel,
		TargetID: id, Before: channelSnapshot(before),
	}
	if before != nil {
		entry.TargetName = before.Name
	}
	c.Audit.Record(r, entry)
	respond.OK(w, nil)
}

func (c *catalogAdminController) handleAdminTestChannel(w http.ResponseWriter, r *http.Request) {
	id, ok := IDParam(w, r, "id")
	if !ok {
		return
	}
	ch, err := c.Channels.GetByID(r.Context(), id)
	if err != nil {
		respond.Fail(w, http.StatusNotFound, "渠道不存在")
		return
	}
	logger := obs.Logger(r.Context())
	logger.Info("渠道测试", "channel_id", id, "provider", ch.Provider, "protocol", ch.Protocol)
	apiKey, err := c.Secrets.Decrypt(ch.APIKeyEncrypted)
	if err != nil {
		logger.Warn("渠道测试失败：密钥解密失败", "channel_id", id)
		respond.Fail(w, http.StatusInternalServerError, "渠道密钥解密失败")
		return
	}
	latency, testErr := relay.TestChannel(r.Context(), c.Relay.Client, ch, apiKey,
		r.URL.Query().Get("model"))
	now := time.Now()
	if testErr != nil {
		_ = c.Channels.UpdateFields(r.Context(), id, map[string]any{
			"last_test_at": now, "last_test_latency_ms": latency,
			"last_test_status": string(domain.ChannelTestFailure),
		})
		logger.Warn("渠道测试未通过", "channel_id", id, "latency_ms", latency,
			"error", testErr.Error())
		c.Audit.Record(r, audit.Entry{
			Action: domain.AuditChannelTest, TargetType: domain.AuditTargetChannel,
			TargetID: id, TargetName: ch.Name, Result: domain.AuditFailure,
			Message: testErr.Error(),
		})
		respond.OK(w, map[string]any{"ok": false, "latency_ms": latency, "message": testErr.Error()})
		return
	}
	_ = c.Channels.UpdateFields(r.Context(), id, map[string]any{
		"last_test_at": now, "last_test_latency_ms": latency,
		"last_test_status": string(domain.ChannelTestSuccess),
	})
	logger.Info("渠道测试通过", "channel_id", id, "latency_ms", latency)
	c.Audit.Record(r, audit.Entry{
		Action: domain.AuditChannelTest, TargetType: domain.AuditTargetChannel,
		TargetID: id, TargetName: ch.Name,
		After: map[string]any{"latency_ms": latency},
	})
	respond.OK(w, map[string]any{"ok": true, "latency_ms": latency, "message": "连通正常"})
}

type channelCostPayload struct {
	ModelName      string `json:"model_name"`
	Currency       string `json:"currency"`
	InputCost      int64  `json:"input_cost"`
	OutputCost     int64  `json:"output_cost"`
	CacheReadCost  int64  `json:"cache_read_cost"`
	CacheWriteCost int64  `json:"cache_write_cost"`
	PerCallCost    int64  `json:"per_call_cost"`
}

func (c *catalogAdminController) handleAdminGetChannelCosts(w http.ResponseWriter, r *http.Request) {
	id, ok := IDParam(w, r, "id")
	if !ok {
		return
	}
	costs, err := c.Costs.ListByChannel(r.Context(), id)
	if err != nil {
		respond.Fail(w, http.StatusInternalServerError, "查询成本失败")
		return
	}
	respond.OK(w, costs)
}

func (c *catalogAdminController) handleAdminSetChannelCosts(w http.ResponseWriter, r *http.Request) {
	id, ok := IDParam(w, r, "id")
	if !ok {
		return
	}
	if _, err := c.Channels.GetByID(r.Context(), id); err != nil {
		respond.Fail(w, http.StatusNotFound, "渠道不存在")
		return
	}
	var req struct {
		Costs []channelCostPayload `json:"costs"`
	}
	if !Bind(w, r, &req) {
		return
	}
	costs := make([]store.ChannelCost, 0, len(req.Costs))
	for _, c := range req.Costs {
		if c.ModelName == "" {
			respond.Fail(w, http.StatusBadRequest, "成本记录缺少模型名")
			return
		}
		if c.Currency == "" {
			c.Currency = string(domain.CostCurrencyCredits)
		}
		if !domain.CostCurrency(c.Currency).Valid() {
			respond.Fail(w, http.StatusBadRequest, "成本币种只能是 credits 或 usd")
			return
		}
		for _, v := range []int64{c.InputCost, c.OutputCost, c.CacheReadCost, c.CacheWriteCost, c.PerCallCost} {
			if v < 0 {
				respond.Fail(w, http.StatusBadRequest, "成本单价不能为负数")
				return
			}
		}
		costs = append(costs, store.ChannelCost{
			ModelName: c.ModelName, Currency: c.Currency,
			InputCost: c.InputCost, OutputCost: c.OutputCost,
			CacheReadCost: c.CacheReadCost, CacheWriteCost: c.CacheWriteCost,
			PerCallCost: c.PerCallCost,
		})
	}
	if err := c.Costs.ReplaceForChannel(r.Context(), id, costs); err != nil {
		respond.Fail(w, http.StatusInternalServerError, "保存成本失败")
		return
	}
	obs.Logger(r.Context()).Info("渠道成本更新", "channel_id", id, "count", len(costs))
	c.Audit.Record(r, audit.Entry{
		Action: domain.AuditChannelCostChange, TargetType: domain.AuditTargetChannel,
		TargetID: id, After: map[string]any{"costs": req.Costs},
	})
	respond.OK(w, nil)
}
