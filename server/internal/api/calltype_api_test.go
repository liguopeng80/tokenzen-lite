package api

import (
	"fmt"
	"testing"
	"time"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// TestAdminCostByCallType 覆盖管理端调用类型分布端点：
// 200、rows 携带枚举内的 call_type 与扣费字段，且只统计 settled。
func TestAdminCostByCallType(t *testing.T) {
	e := newTestEnv(t)
	adminC := e.seedAndLogin(t, "ctadmin", domain.RoleAdmin)
	e.seedAndLogin(t, "ctuser", domain.RoleUser)
	uid := e.userIDByName(t, "ctuser")

	// 种入不同形态的模型，使调用类型可派生。
	if err := e.db.Create(&store.Model{
		Name: "ct-api-text", Modality: domain.ModalityText,
		BillingMode: domain.BillPerToken, Status: domain.ModelEnabled,
	}).Error; err != nil {
		t.Fatalf("种入文本模型失败: %v", err)
	}
	if err := e.db.Create(&store.Model{
		Name: "ct-api-embed", Modality: domain.ModalityEmbedding,
		BillingMode: domain.BillPerToken, Status: domain.ModelEnabled,
	}).Error; err != nil {
		t.Fatalf("种入向量模型失败: %v", err)
	}

	anchor := time.Now().Add(-1 * time.Hour)
	e.seedUsageLog(t, store.UsageLog{
		RequestID: "ct-api-1", UserID: uid, APIKeyID: 1, ModelName: "ct-api-text",
		IsStream: true, CreditsCharged: 100, CreditsCost: 30, CreatedAt: anchor,
	})
	e.seedUsageLog(t, store.UsageLog{
		RequestID: "ct-api-2", UserID: uid, APIKeyID: 1, ModelName: "ct-api-text",
		IsStream: false, CreditsCharged: 50, CreditsCost: 10, CreatedAt: anchor,
	})
	e.seedUsageLog(t, store.UsageLog{
		RequestID: "ct-api-3", UserID: uid, APIKeyID: 1, ModelName: "ct-api-embed",
		CreditsCharged: 200, CreditsCost: 80, CreatedAt: anchor,
	})
	// 模型不在目录 → other
	e.seedUsageLog(t, store.UsageLog{
		RequestID: "ct-api-4", UserID: uid, APIKeyID: 1, ModelName: "ct-api-ghost",
		CreditsCharged: 70, CreditsCost: 0, CreatedAt: anchor,
	})
	// 非 settled：不计入
	e.seedUsageLog(t, store.UsageLog{
		RequestID: "ct-api-failed", UserID: uid, APIKeyID: 1, ModelName: "ct-api-text",
		IsStream: true, CreditsCharged: 9999, Status: domain.UsageFailed, CreatedAt: anchor,
	})

	start := time.Now().AddDate(0, 0, -2).Unix()
	end := time.Now().AddDate(0, 0, 1).Unix()
	resp, env := e.do(t, adminC, "GET",
		fmt.Sprintf("/api/admin/stats/cost-by-calltype?start_timestamp=%d&end_timestamp=%d", start, end), nil)
	if resp.StatusCode != 200 {
		t.Fatalf("cost-by-calltype 应 200，实际 %d %v", resp.StatusCode, env)
	}
	data, ok := env["data"].(map[string]any)
	if !ok {
		t.Fatalf("响应应为信封: %v", env)
	}
	rows, ok := data["rows"].([]any)
	if !ok {
		t.Fatalf("rows 应为数组: %v", data)
	}

	allowed := map[string]bool{
		store.CallTypeEmbedding: true, store.CallTypeImage: true, store.CallTypeStream: true,
		store.CallTypeNonStream: true, store.CallTypeOther: true,
	}
	var totalCharged float64
	byType := map[string]float64{}
	for _, r := range rows {
		row := r.(map[string]any)
		ct, _ := row["call_type"].(string)
		if !allowed[ct] {
			t.Errorf("出现非法调用类型 %q", ct)
		}
		charged, _ := row["credits_charged"].(float64)
		totalCharged += charged
		byType[ct] += charged
	}
	// 合计扣费 = 100 + 50 + 200 + 70 = 420（failed 不计入）
	if totalCharged != 420 {
		t.Errorf("合计扣费应为 420，实际 %v", totalCharged)
	}
	if byType[store.CallTypeStream] != 100 {
		t.Errorf("stream 扣费应为 100，实际 %v", byType[store.CallTypeStream])
	}
	if byType[store.CallTypeNonStream] != 50 {
		t.Errorf("non_stream 扣费应为 50，实际 %v", byType[store.CallTypeNonStream])
	}
	if byType[store.CallTypeEmbedding] != 200 {
		t.Errorf("embedding 扣费应为 200，实际 %v", byType[store.CallTypeEmbedding])
	}
	if byType[store.CallTypeOther] != 70 {
		t.Errorf("other 扣费应为 70，实际 %v", byType[store.CallTypeOther])
	}
}
