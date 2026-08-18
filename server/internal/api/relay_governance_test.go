package api

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// 部门级模型策略对成员生效：策略外的模型即使密钥未限制也调不通。
func TestDepartmentModelPolicyNarrowsAccess(t *testing.T) {
	e := newTestEnv(t)
	upstream := newOKChatUpstream(t)
	userID, key := e.seedRelayUser(t, "deptpolicy", 1_000_000, nil)
	e.seedModel(t, "glm-5")
	e.seedModel(t, "glm-5-pro")
	e.seedChannel(t, "ch-policy", upstream.URL, 0, []string{"glm-5", "glm-5-pro"}, nil)

	dept := &store.Department{
		Name: "只能用基础模型的部门", Status: domain.DepartmentEnabled,
		AllowedModels: toJSONField([]string{"glm-5"}),
	}
	if err := e.db.Create(dept).Error; err != nil {
		t.Fatalf("建部门失败: %v", err)
	}
	if err := e.db.Model(&store.User{}).Where("id = ?", userID).
		Update("department_id", dept.ID).Error; err != nil {
		t.Fatalf("分配部门失败: %v", err)
	}

	resp, raw := e.relayPost(t, key, chatBody("glm-5"))
	if resp.StatusCode != 200 {
		t.Fatalf("部门策略内的模型应可调用：%d %s", resp.StatusCode, raw)
	}
	resp, raw = e.relayPost(t, key, chatBody("glm-5-pro"))
	if resp.StatusCode != 403 {
		t.Fatalf("部门策略外的模型应 403，实际 %d %s", resp.StatusCode, raw)
	}
	if code := errorCodeOf(t, raw); code != "model_not_allowed" {
		t.Errorf("错误码应为 model_not_allowed，实际 %q", code)
	}
}

// 用户级策略只能收窄不能放宽：密钥列出但用户策略未列出的模型仍被拒绝。
func TestUserModelPolicyCannotWidenKeyWhitelist(t *testing.T) {
	e := newTestEnv(t)
	upstream := newOKChatUpstream(t)
	userID, key := e.seedRelayUser(t, "userpolicy", 1_000_000, nil)
	e.seedModel(t, "glm-5")
	e.seedModel(t, "glm-5-pro")
	e.seedChannel(t, "ch-user-policy", upstream.URL, 0, []string{"glm-5", "glm-5-pro"}, nil)

	if err := e.db.Model(&store.User{}).Where("id = ?", userID).
		Update("allowed_models", toJSONField([]string{"glm-5"})).Error; err != nil {
		t.Fatalf("设置用户策略失败: %v", err)
	}
	if err := e.db.Model(&store.APIKey{}).Where("user_id = ?", userID).
		Update("allowed_models", toJSONField([]string{"glm-5", "glm-5-pro"})).Error; err != nil {
		t.Fatalf("设置密钥白名单失败: %v", err)
	}

	resp, raw := e.relayPost(t, key, chatBody("glm-5-pro"))
	if resp.StatusCode != 403 {
		t.Fatalf("用户策略外的模型应 403，实际 %d %s", resp.StatusCode, raw)
	}
}

// 策略内容写坏时拒绝调用而非放行：放行等于配置静默失效。
func TestMalformedModelPolicyRejectsCall(t *testing.T) {
	e := newTestEnv(t)
	upstream := newOKChatUpstream(t)
	userID, key := e.seedRelayUser(t, "badpolicy", 1_000_000, nil)
	e.seedModel(t, "glm-5")
	e.seedChannel(t, "ch-bad-policy", upstream.URL, 0, []string{"glm-5"}, nil)

	if err := e.db.Model(&store.User{}).Where("id = ?", userID).
		Update("allowed_models", []byte(`{"models":["glm-5"]}`)).Error; err != nil {
		t.Fatalf("写入非法策略失败: %v", err)
	}
	resp, raw := e.relayPost(t, key, chatBody("glm-5"))
	if resp.StatusCode != 403 {
		t.Fatalf("策略无法解析时应拒绝，实际 %d %s", resp.StatusCode, raw)
	}
}

// 每日花费上限拦截当日累计扣费突破上限的请求，错误码可被客户端识别。
func TestDailySpendLimitBlocksRequest(t *testing.T) {
	e := newTestEnv(t)
	upstream := newOKChatUpstream(t)
	userID, key := e.seedRelayUser(t, "spendcap", 10_000_000, nil)
	e.seedModel(t, "glm-5")
	e.seedChannel(t, "ch-spend", upstream.URL, 0, []string{"glm-5"}, nil)

	// 先跑通一次，产生当日消费记录。
	resp, raw := e.relayPost(t, key, chatBody("glm-5"))
	if resp.StatusCode != 200 {
		t.Fatalf("首次调用应成功：%d %s", resp.StatusCode, raw)
	}
	spent, err := e.deps.Spend.TodaySpend(t.Context(), userID, time.Now())
	if err != nil {
		t.Fatalf("查询当日花费失败: %v", err)
	}
	if spent <= 0 {
		t.Fatalf("当日花费计数应随扣费累加，实际 %d", spent)
	}

	// 把上限压到已消费额度，下一次请求的预扣必然突破。
	if err := e.db.Model(&store.User{}).Where("id = ?", userID).
		Update("daily_spend_limit", spent).Error; err != nil {
		t.Fatalf("设置每日花费上限失败: %v", err)
	}
	resp, raw = e.relayPost(t, key, chatBody("glm-5"))
	if resp.StatusCode != 429 {
		t.Fatalf("触及每日花费上限应 429，实际 %d %s", resp.StatusCode, raw)
	}
	if code := errorCodeOf(t, raw); code != "daily_spend_limit_exceeded" {
		t.Errorf("错误码应为 daily_spend_limit_exceeded，实际 %q", code)
	}

	// 上限清零后恢复调用。
	if err := e.db.Model(&store.User{}).Where("id = ?", userID).
		Update("daily_spend_limit", 0).Error; err != nil {
		t.Fatalf("清除每日花费上限失败: %v", err)
	}
	resp, raw = e.relayPost(t, key, chatBody("glm-5"))
	if resp.StatusCode != 200 {
		t.Fatalf("清除上限后应恢复调用：%d %s", resp.StatusCode, raw)
	}
}

// 用量日志落记账时点的部门快照，供按部门分摊报表使用。
func TestUsageLogCarriesDepartmentSnapshot(t *testing.T) {
	e := newTestEnv(t)
	upstream := newOKChatUpstream(t)
	userID, key := e.seedRelayUser(t, "deptusage", 1_000_000, nil)
	e.seedModel(t, "glm-5")
	e.seedChannel(t, "ch-dept-usage", upstream.URL, 0, []string{"glm-5"}, nil)

	dept := &store.Department{Name: "成本归属部门", Status: domain.DepartmentEnabled}
	if err := e.db.Create(dept).Error; err != nil {
		t.Fatalf("建部门失败: %v", err)
	}
	if err := e.db.Model(&store.User{}).Where("id = ?", userID).
		Update("department_id", dept.ID).Error; err != nil {
		t.Fatalf("分配部门失败: %v", err)
	}

	resp, raw := e.relayPost(t, key, chatBody("glm-5"))
	if resp.StatusCode != 200 {
		t.Fatalf("调用应成功：%d %s", resp.StatusCode, raw)
	}
	e.deps.Relay.Close(t.Context()) // 刷盘异步日志队列

	var log store.UsageLog
	if err := e.db.Where("user_id = ?", userID).Order("id DESC").First(&log).Error; err != nil {
		t.Fatalf("查询用量日志失败: %v", err)
	}
	if log.DepartmentID != dept.ID {
		t.Errorf("用量日志应记部门快照 %d，实际 %d", dept.ID, log.DepartmentID)
	}
}

// chatBody 构造最小可用的对话请求体。
func chatBody(model string) map[string]any {
	return map[string]any{
		"model":    model,
		"messages": []map[string]any{{"role": "user", "content": "hi"}},
	}
}

// errorCodeOf 提取 OpenAI 格式错误响应中的错误码。
func errorCodeOf(t *testing.T, raw []byte) string {
	t.Helper()
	var body struct {
		Error struct {
			Code string `json:"code"`
			Type string `json:"type"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("错误响应解析失败: %v %s", err, raw)
	}
	if body.Error.Code != "" {
		return body.Error.Code
	}
	return body.Error.Type
}
