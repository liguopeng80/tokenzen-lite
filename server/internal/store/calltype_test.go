package store

import (
	"testing"
	"time"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
)

// TestCostByCallTypeBuckets 覆盖调用类型的派生分桶与扣费聚合：
// modality + is_stream 派生 embedding/image/stream/non_stream，
// model_name 不在 models 表时落入 other；只统计 settled；按扣费降序返回。
func TestCostByCallTypeBuckets(t *testing.T) {
	db := newStoreTestDB(t)
	repo := NewStatsRepo(db)

	// 清空 models（newStoreTestDB 默认不truncate models），保证断言基于本用例的数据。
	if err := db.Exec("TRUNCATE models, model_prices RESTART IDENTITY CASCADE").Error; err != nil {
		t.Fatalf("清空 models 失败: %v", err)
	}

	models := []Model{
		{Name: "ct-text-a", Modality: domain.ModalityText, BillingMode: domain.BillPerToken, Status: domain.ModelEnabled},
		{Name: "ct-embed-a", Modality: domain.ModalityEmbedding, BillingMode: domain.BillPerToken, Status: domain.ModelEnabled},
		{Name: "ct-image-a", Modality: domain.ModalityImage, BillingMode: domain.BillPerCall, Status: domain.ModelEnabled},
	}
	for i := range models {
		if err := db.Create(&models[i]).Error; err != nil {
			t.Fatalf("种入模型失败: %v", err)
		}
	}

	anchor := time.Date(2026, 8, 3, 10, 0, 0, 0, time.Local)
	logs := []UsageLog{
		// 文本流式：2 条，扣费 300 / 成本 100 → 毛利 200
		{RequestID: "ct-s-1", UserID: 1, APIKeyID: 1, ModelName: "ct-text-a",
			IsStream: true, PromptTokens: 10, CompletionTokens: 20,
			CreditsCharged: 100, CreditsCost: 30, Status: domain.UsageSettled, CreatedAt: anchor},
		{RequestID: "ct-s-2", UserID: 1, APIKeyID: 1, ModelName: "ct-text-a",
			IsStream: true, PromptTokens: 5, CompletionTokens: 5,
			CreditsCharged: 200, CreditsCost: 70, Status: domain.UsageSettled, CreatedAt: anchor},
		// 文本非流式：1 条，扣费 50
		{RequestID: "ct-ns-1", UserID: 1, APIKeyID: 1, ModelName: "ct-text-a",
			IsStream: false, PromptTokens: 3, CompletionTokens: 1,
			CreditsCharged: 50, CreditsCost: 10, Status: domain.UsageSettled, CreatedAt: anchor},
		// 向量嵌入：1 条，扣费 400
		{RequestID: "ct-e-1", UserID: 1, APIKeyID: 1, ModelName: "ct-embed-a",
			PromptTokens: 100, CreditsCharged: 400, CreditsCost: 150,
			Status: domain.UsageSettled, CreatedAt: anchor},
		// 图像：1 条，扣费 600
		{RequestID: "ct-i-1", UserID: 1, APIKeyID: 1, ModelName: "ct-image-a",
			CreditsCharged: 600, CreditsCost: 200, Status: domain.UsageSettled, CreatedAt: anchor},
		// 模型不在目录 → other：1 条，扣费 80
		{RequestID: "ct-o-1", UserID: 1, APIKeyID: 1, ModelName: "ct-ghost",
			CreditsCharged: 80, CreditsCost: 0, Status: domain.UsageSettled, CreatedAt: anchor},
		// 非 settled：不计入任何桶
		{RequestID: "ct-f-1", UserID: 1, APIKeyID: 1, ModelName: "ct-text-a",
			IsStream: true, CreditsCharged: 9999, Status: domain.UsageFailed, CreatedAt: anchor},
	}
	for i := range logs {
		if err := db.Create(&logs[i]).Error; err != nil {
			t.Fatalf("种入用量日志失败: %v", err)
		}
	}

	from := anchor.AddDate(0, 0, -1)
	to := anchor.AddDate(0, 0, 1)
	rows, err := repo.CostByCallType(t.Context(), from, to, nil)
	if err != nil {
		t.Fatalf("CostByCallType 查询失败: %v", err)
	}

	byType := map[string]CallTypeRow{}
	var seen []string
	for _, r := range rows {
		byType[r.CallType] = r
		seen = append(seen, r.CallType)
	}

	// 维度合法性：只允许枚举内的调用类型
	allowed := map[string]bool{
		CallTypeEmbedding: true, CallTypeImage: true, CallTypeStream: true,
		CallTypeNonStream: true, CallTypeOther: true,
	}
	for k := range byType {
		if !allowed[k] {
			t.Errorf("出现非法调用类型 %q", k)
		}
	}

	// 各桶断言
	cases := []struct {
		callType       string
		wantReqs       int64
		wantCharged    int64
		wantCost       int64
		wantMargin     int64
		wantPrompt     int64
		wantCompletion int64
	}{
		{CallTypeStream, 2, 300, 100, 200, 15, 25},
		{CallTypeNonStream, 1, 50, 10, 40, 3, 1},
		{CallTypeEmbedding, 1, 400, 150, 250, 100, 0},
		{CallTypeImage, 1, 600, 200, 400, 0, 0},
		{CallTypeOther, 1, 80, 0, 80, 0, 0},
	}
	for _, c := range cases {
		row, ok := byType[c.callType]
		if !ok {
			t.Errorf("调用类型 %s 缺失，已有: %v", c.callType, seen)
			continue
		}
		if row.Requests != c.wantReqs {
			t.Errorf("%s 请求数应为 %d，实际 %d", c.callType, c.wantReqs, row.Requests)
		}
		if row.CreditsCharged != c.wantCharged {
			t.Errorf("%s 扣费应为 %d，实际 %d", c.callType, c.wantCharged, row.CreditsCharged)
		}
		if row.CreditsCost != c.wantCost {
			t.Errorf("%s 成本应为 %d，实际 %d", c.callType, c.wantCost, row.CreditsCost)
		}
		if row.Margin != c.wantMargin {
			t.Errorf("%s 毛利应为 %d，实际 %d", c.callType, c.wantMargin, row.Margin)
		}
		if row.PromptTokens != c.wantPrompt {
			t.Errorf("%s prompt_tokens 应为 %d，实际 %d", c.callType, c.wantPrompt, row.PromptTokens)
		}
		if row.CompletionTokens != c.wantCompletion {
			t.Errorf("%s completion_tokens 应为 %d，实际 %d", c.callType, c.wantCompletion, row.CompletionTokens)
		}
	}

	// 排序断言：按 credits_charged 降序 → image(600) > embedding(400) > stream(300) > other(80) > non_stream(50)
	wantOrder := []string{CallTypeImage, CallTypeEmbedding, CallTypeStream, CallTypeOther, CallTypeNonStream}
	if len(rows) != len(wantOrder) {
		t.Fatalf("返回行数应为 %d，实际 %d", len(wantOrder), len(rows))
	}
	for i, w := range wantOrder {
		if rows[i].CallType != w {
			t.Errorf("第 %d 行调用类型应为 %s（扣费 %d），实际 %s（扣费 %d）；完整顺序: %v",
				i, w, byType[w].CreditsCharged, rows[i].CallType, rows[i].CreditsCharged, seen)
			break
		}
	}
}

// TestCostByCallTypeEmpty 空结果返回长度为 0 的切片（由 handler 兜底为 []）。
func TestCostByCallTypeEmpty(t *testing.T) {
	db := newStoreTestDB(t)
	repo := NewStatsRepo(db)

	from := time.Date(2026, 8, 1, 0, 0, 0, 0, time.Local)
	to := from.AddDate(0, 0, 1)
	rows, err := repo.CostByCallType(t.Context(), from, to, nil)
	if err != nil {
		t.Fatalf("CostByCallType 空查询失败: %v", err)
	}
	if rows == nil {
		t.Fatal("空结果应返回非 nil 切片")
	}
	if len(rows) != 0 {
		t.Errorf("空结果长度应为 0，实际 %d", len(rows))
	}
}

// TestCostByCallTypeIntegrationScope 覆盖接入方作用域收窄：
// 只统计本接入方记录，跨接入方的不计入。
func TestCostByCallTypeIntegrationScope(t *testing.T) {
	db := newStoreTestDB(t)
	repo := NewStatsRepo(db)

	if err := db.Exec("TRUNCATE models, model_prices RESTART IDENTITY CASCADE").Error; err != nil {
		t.Fatalf("清空 models 失败: %v", err)
	}
	if err := db.Create(&Model{
		Name: "ct-scope-text", Modality: domain.ModalityText,
		BillingMode: domain.BillPerToken, Status: domain.ModelEnabled,
	}).Error; err != nil {
		t.Fatalf("种入模型失败: %v", err)
	}

	anchor := time.Date(2026, 8, 3, 10, 0, 0, 0, time.Local)
	logs := []UsageLog{
		// integration_id=1：2 条，应被收窄后统计
		{RequestID: "ct-scope-in-1", UserID: 1, APIKeyID: 1, IntegrationID: 1,
			ModelName: "ct-scope-text", IsStream: true,
			CreditsCharged: 100, Status: domain.UsageSettled, CreatedAt: anchor},
		{RequestID: "ct-scope-in-2", UserID: 1, APIKeyID: 1, IntegrationID: 1,
			ModelName: "ct-scope-text", IsStream: false,
			CreditsCharged: 50, Status: domain.UsageSettled, CreatedAt: anchor},
		// integration_id=2：不应出现
		{RequestID: "ct-scope-out-1", UserID: 2, APIKeyID: 2, IntegrationID: 2,
			ModelName: "ct-scope-text", IsStream: true,
			CreditsCharged: 9999, Status: domain.UsageSettled, CreatedAt: anchor},
	}
	for i := range logs {
		if err := db.Create(&logs[i]).Error; err != nil {
			t.Fatalf("种入用量日志失败: %v", err)
		}
	}

	from := anchor.AddDate(0, 0, -1)
	to := anchor.AddDate(0, 0, 1)
	integID := int64(1)
	rows, err := repo.CostByCallType(t.Context(), from, to, &integID)
	if err != nil {
		t.Fatalf("CostByCallType 作用域查询失败: %v", err)
	}

	var totalReqs, totalCharged int64
	for _, r := range rows {
		totalReqs += r.Requests
		totalCharged += r.CreditsCharged
	}
	if totalReqs != 2 {
		t.Errorf("接入方作用域下请求数应为 2，实际 %d", totalReqs)
	}
	if totalCharged != 150 {
		t.Errorf("接入方作用域下扣费应为 150，实际 %d", totalCharged)
	}
}
