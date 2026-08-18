package api

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// 本文件覆盖 P2-13：资金相关输入校验的 400 分支与正向边界值。
// 校验写反（把拒绝写成放行、把放行写成拒绝）会直接造成积分凭空发放或计费打折，
// 因此每个 400 用例断言提示文案，每个边界值用例断言落库结果。

// assertFail400 断言响应为 400 且提示文案包含预期子串。
func assertFail400(t *testing.T, status int, env map[string]any, wantMsg string) {
	t.Helper()
	if status != 400 {
		t.Fatalf("应 400，实际 %d，响应: %v", status, env)
	}
	if msg, _ := env["message"].(string); !strings.Contains(msg, wantMsg) {
		t.Errorf("提示文案应包含 %q，实际: %q", wantMsg, env["message"])
	}
}

// 模型单价校验：任一单价字段为负必须 400——负单价会让每次调用给用户加积分。
func TestAdminSetModelPriceValidation(t *testing.T) {
	e := newTestEnv(t)
	adminC := e.seedAndLogin(t, "priceadmin", domain.RoleAdmin)
	tokenModelID := e.seedModel(t, "price-token-model")
	pricePath := fmt.Sprintf("/api/admin/models/%d/price", tokenModelID)

	priceFields := []string{"input_price", "output_price", "cache_read_price",
		"cache_write_price", "audio_input_price", "audio_output_price", "per_call_price"}
	for _, field := range priceFields {
		t.Run("负单价/"+field, func(t *testing.T) {
			body := map[string]any{"input_price": 1000, field: -1}
			resp, env := e.do(t, adminC, "PUT", pricePath, body)
			assertFail400(t, resp.StatusCode, env, "单价不能为负数")
		})
	}

	// 按 token 计费的模型全零单价应 400（否则模型可被零扣费调用）
	resp, env := e.do(t, adminC, "PUT", pricePath, map[string]any{})
	assertFail400(t, resp.StatusCode, env, "至少需要一项非零的 token 单价")

	// 按次计费的模型按次单价为零应 400
	perCallModel := &store.Model{Name: "price-percall-model", Modality: domain.ModalityText,
		BillingMode: domain.BillPerCall, Status: domain.ModelEnabled}
	if err := e.db.Create(perCallModel).Error; err != nil {
		t.Fatalf("建按次计费模型失败: %v", err)
	}
	perCallPath := fmt.Sprintf("/api/admin/models/%d/price", perCallModel.ID)
	resp, env = e.do(t, adminC, "PUT", perCallPath, map[string]any{"input_price": 1000})
	assertFail400(t, resp.StatusCode, env, "非零的按次单价")

	// 正向：单项为 0 允许（input=0, output=1），仅全零被计费方式一致性校验拦截
	resp, env = e.do(t, adminC, "PUT", pricePath,
		map[string]any{"input_price": 0, "output_price": 1})
	if resp.StatusCode != 200 {
		t.Fatalf("input=0/output=1 应 200，实际 %d %v", resp.StatusCode, env)
	}

	// 正向：最小正向边界 input=1、其余 0，落库后回读一致
	resp, env = e.do(t, adminC, "PUT", pricePath, map[string]any{"input_price": 1})
	if resp.StatusCode != 200 {
		t.Fatalf("input=1 应 200，实际 %d %v", resp.StatusCode, env)
	}
	var minSaved store.ModelPrice
	if err := e.db.Where("model_id = ?", tokenModelID).First(&minSaved).Error; err != nil {
		t.Fatalf("查落库价格失败: %v", err)
	}
	if minSaved.InputPrice != 1 || minSaved.OutputPrice != 0 {
		t.Errorf("落库价格应为 1/0，实际 %d/%d", minSaved.InputPrice, minSaved.OutputPrice)
	}

	// 正向：合法单价 200 且落库值一致
	resp, env = e.do(t, adminC, "PUT", pricePath,
		map[string]any{"input_price": 720_000, "output_price": 2_300_000})
	if resp.StatusCode != 200 {
		t.Fatalf("合法单价应 200，实际 %d %v", resp.StatusCode, env)
	}
	var saved store.ModelPrice
	if err := e.db.Where("model_id = ?", tokenModelID).First(&saved).Error; err != nil {
		t.Fatalf("查落库价格失败: %v", err)
	}
	if saved.InputPrice != 720_000 || saved.OutputPrice != 2_300_000 {
		t.Errorf("落库价格应为 720000/2300000，实际 %d/%d", saved.InputPrice, saved.OutputPrice)
	}
}

// 时段倍率校验：倍率低于 100 必须 400（低于 100 会把高峰加价变成打折），
// 时段起止越界、星期越界、时区非法同样 400；边界值恰好 100 与 0-1440 必须放行。
func TestAdminSetPeakRulesValidation(t *testing.T) {
	e := newTestEnv(t)
	adminC := e.seedAndLogin(t, "peakadmin", domain.RoleAdmin)
	modelID := e.seedModel(t, "peak-model")
	rulesPath := fmt.Sprintf("/api/admin/models/%d/peak-rules", modelID)

	rule := func(mutate func(m map[string]any)) map[string]any {
		m := map[string]any{
			"timezone": "Asia/Shanghai", "start_minute": 540, "end_minute": 1080,
			"days_of_week": []int{1, 2, 3, 4, 5}, "multiplier_percent": 200, "enabled": true,
		}
		if mutate != nil {
			mutate(m)
		}
		return m
	}

	badCases := []struct {
		name    string
		mutate  func(m map[string]any)
		wantMsg string
	}{
		{"倍率 99 低于下限", func(m map[string]any) { m["multiplier_percent"] = 99 }, "倍率百分数不能低于 100"},
		{"倍率为 0", func(m map[string]any) { m["multiplier_percent"] = 0 }, "倍率百分数不能低于 100"},
		{"倍率为负", func(m map[string]any) { m["multiplier_percent"] = -300 }, "倍率百分数不能低于 100"},
		{"起点为负", func(m map[string]any) { m["start_minute"] = -1 }, "时段范围不合法"},
		{"终点超 1440", func(m map[string]any) { m["end_minute"] = 1441 }, "时段范围不合法"},
		{"起点等于终点", func(m map[string]any) { m["start_minute"] = 600; m["end_minute"] = 600 }, "时段范围不合法"},
		{"起点晚于终点", func(m map[string]any) { m["start_minute"] = 1080; m["end_minute"] = 540 }, "时段范围不合法"},
		{"星期为 0", func(m map[string]any) { m["days_of_week"] = []int{0, 1} }, "星期取值须在 1-7 之间"},
		{"星期为 8", func(m map[string]any) { m["days_of_week"] = []int{7, 8} }, "星期取值须在 1-7 之间"},
		{"时区不合法", func(m map[string]any) { m["timezone"] = "Mars/Olympus" }, "时区不合法"},
	}
	for _, tc := range badCases {
		t.Run(tc.name, func(t *testing.T) {
			resp, env := e.do(t, adminC, "PUT", rulesPath,
				map[string]any{"rules": []map[string]any{rule(tc.mutate)}})
			assertFail400(t, resp.StatusCode, env, tc.wantMsg)
		})
	}

	// 非法规则被拒后不得留下任何落库规则（整批原子拒绝）
	var count int64
	e.db.Model(&store.ModelPeakRule{}).Where("model_id = ?", modelID).Count(&count)
	if count != 0 {
		t.Fatalf("非法规则被拒后不应有落库规则，实际 %d 条", count)
	}

	// 正向边界：倍率恰好 100、时段起止恰好 0 与 1440 必须放行并落库
	resp, env := e.do(t, adminC, "PUT", rulesPath,
		map[string]any{"rules": []map[string]any{rule(func(m map[string]any) {
			m["multiplier_percent"] = 100
			m["start_minute"] = 0
			m["end_minute"] = 1440
		})}})
	if resp.StatusCode != 200 {
		t.Fatalf("边界值规则应 200，实际 %d %v", resp.StatusCode, env)
	}
	var saved []store.ModelPeakRule
	if err := e.db.Where("model_id = ?", modelID).Find(&saved).Error; err != nil {
		t.Fatalf("查落库规则失败: %v", err)
	}
	if len(saved) != 1 {
		t.Fatalf("应落库 1 条规则，实际 %d", len(saved))
	}
	if saved[0].MultiplierPercent != 100 || saved[0].StartMinute != 0 || saved[0].EndMinute != 1440 {
		t.Errorf("落库规则应为倍率 100、时段 0-1440，实际 %d、%d-%d",
			saved[0].MultiplierPercent, saved[0].StartMinute, saved[0].EndMinute)
	}

	// 防反向：倍率 101 必须放行（校验若误写成 >100 拒绝会在此失败）
	resp, env = e.do(t, adminC, "PUT", rulesPath,
		map[string]any{"rules": []map[string]any{rule(func(m map[string]any) {
			m["multiplier_percent"] = 101
		})}})
	if resp.StatusCode != 200 {
		t.Fatalf("倍率 101 应放行，实际 %d %v", resp.StatusCode, env)
	}

	// timezone 缺省应回落为 Asia/Shanghai 并放行
	resp, env = e.do(t, adminC, "PUT", rulesPath,
		map[string]any{"rules": []map[string]any{rule(func(m map[string]any) {
			delete(m, "timezone")
		})}})
	if resp.StatusCode != 200 {
		t.Fatalf("timezone 缺省应放行，实际 %d %v", resp.StatusCode, env)
	}
	var tzSaved []store.ModelPeakRule
	if err := e.db.Where("model_id = ?", modelID).Find(&tzSaved).Error; err != nil {
		t.Fatalf("查落库规则失败: %v", err)
	}
	if len(tzSaved) != 1 || tzSaved[0].Timezone != "Asia/Shanghai" {
		t.Errorf("timezone 缺省应落库为 Asia/Shanghai，实际: %+v", tzSaved)
	}

	// 混合批次：[合法规则, 倍率 99 规则] 整批 400，且已落库规则保持不变（防部分写入）
	resp, env = e.do(t, adminC, "PUT", rulesPath,
		map[string]any{"rules": []map[string]any{
			rule(nil),
			rule(func(m map[string]any) { m["multiplier_percent"] = 99 }),
		}})
	assertFail400(t, resp.StatusCode, env, "倍率百分数不能低于 100")
	var afterMixed []store.ModelPeakRule
	if err := e.db.Where("model_id = ?", modelID).Find(&afterMixed).Error; err != nil {
		t.Fatalf("查落库规则失败: %v", err)
	}
	if len(afterMixed) != 1 || afterMixed[0].MultiplierPercent != 200 {
		t.Errorf("混合批次被拒后已有规则应保持不变（1 条、倍率 200），实际: %+v", afterMixed)
	}
}

// 时段倍率写入层归一化：days_of_week 空数组或整体缺省均展开为全周 1-7；
// 跨午夜拆分对 [1380,1440) + [0,60) 作为契约认可的替代路径必须整批落库。
func TestAdminPeakRulesNormalization(t *testing.T) {
	e := newTestEnv(t)
	adminC := e.seedAndLogin(t, "peaknorm", domain.RoleAdmin)
	modelID := e.seedModel(t, "peak-norm-model")
	rulesPath := fmt.Sprintf("/api/admin/models/%d/peak-rules", modelID)

	daysOf := func(t *testing.T, r store.ModelPeakRule) []int {
		t.Helper()
		var days []int
		if err := json.Unmarshal(r.DaysOfWeek, &days); err != nil {
			t.Fatalf("解析落库 DaysOfWeek 失败: %v", err)
		}
		return days
	}
	allWeek := []int{1, 2, 3, 4, 5, 6, 7}

	// I3：days_of_week 显式传空数组 → 200，落库展开为全周
	// I4：days_of_week 字段整体缺省 → 与 I3 相同归一化结果
	for _, tc := range []struct {
		name string
		rule map[string]any
	}{
		{"空数组", map[string]any{
			"timezone": "Asia/Shanghai", "start_minute": 540, "end_minute": 1080,
			"days_of_week": []int{}, "multiplier_percent": 200, "enabled": true,
		}},
		{"字段缺省", map[string]any{
			"timezone": "Asia/Shanghai", "start_minute": 540, "end_minute": 1080,
			"multiplier_percent": 200, "enabled": true,
		}},
	} {
		t.Run("days_of_week "+tc.name+"归一化为全周", func(t *testing.T) {
			resp, env := e.do(t, adminC, "PUT", rulesPath,
				map[string]any{"rules": []map[string]any{tc.rule}})
			if resp.StatusCode != 200 {
				t.Fatalf("应 200，实际 %d %v", resp.StatusCode, env)
			}
			var saved []store.ModelPeakRule
			if err := e.db.Where("model_id = ?", modelID).Find(&saved).Error; err != nil {
				t.Fatalf("查落库规则失败: %v", err)
			}
			if len(saved) != 1 {
				t.Fatalf("应落库 1 条规则，实际 %d", len(saved))
			}
			if got := daysOf(t, saved[0]); !slicesEqualInt(got, allWeek) {
				t.Errorf("落库 DaysOfWeek 应为 %v，实际 %v", allWeek, got)
			}
		})
	}

	// I5：跨午夜等效拆分对 [1380,1440) + [0,60) 两条规则整批放行且均落库
	splitRule := func(start, end int) map[string]any {
		return map[string]any{
			"timezone": "Asia/Shanghai", "start_minute": start, "end_minute": end,
			"days_of_week": []int{1, 2, 3, 4, 5, 6, 7}, "multiplier_percent": 300, "enabled": true,
		}
	}
	resp, env := e.do(t, adminC, "PUT", rulesPath,
		map[string]any{"rules": []map[string]any{splitRule(1380, 1440), splitRule(0, 60)}})
	if resp.StatusCode != 200 {
		t.Fatalf("跨午夜拆分对应 200，实际 %d %v", resp.StatusCode, env)
	}
	var saved []store.ModelPeakRule
	if err := e.db.Where("model_id = ?", modelID).Order("start_minute").Find(&saved).Error; err != nil {
		t.Fatalf("查落库规则失败: %v", err)
	}
	if len(saved) != 2 {
		t.Fatalf("拆分对应落库 2 条规则，实际 %d", len(saved))
	}
	if saved[0].StartMinute != 0 || saved[0].EndMinute != 60 ||
		saved[1].StartMinute != 1380 || saved[1].EndMinute != 1440 {
		t.Errorf("落库时段应为 [0,60) 与 [1380,1440)，实际 [%d,%d) 与 [%d,%d)",
			saved[0].StartMinute, saved[0].EndMinute, saved[1].StartMinute, saved[1].EndMinute)
	}
}

func slicesEqualInt(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// 兑换码批量生成校验：积分非正、数量越界必须 400——否则额度可被批量凭空发放；
// 数量 1 与 1000 为合法边界，必须放行且生成数量准确。
func TestAdminCreateRedemptionsValidation(t *testing.T) {
	e := newTestEnv(t)
	adminC := e.seedAndLogin(t, "redeemadmin", domain.RoleAdmin)
	const path = "/api/admin/redemptions/batch"

	badCases := []struct {
		name    string
		body    map[string]any
		wantMsg string
	}{
		{"积分为 0", map[string]any{"count": 1, "credits": 0}, "兑换积分必须为正数"},
		{"积分为负", map[string]any{"count": 1, "credits": -500_000}, "兑换积分必须为正数"},
		{"数量为 0", map[string]any{"count": 0, "credits": 1000}, "单批数量须在 1-1000 之间"},
		{"数量为负", map[string]any{"count": -5, "credits": 1000}, "单批数量须在 1-1000 之间"},
		{"数量超 1000", map[string]any{"count": 1001, "credits": 1000}, "单批数量须在 1-1000 之间"},
	}
	for _, tc := range badCases {
		t.Run(tc.name, func(t *testing.T) {
			resp, env := e.do(t, adminC, "POST", path, tc.body)
			assertFail400(t, resp.StatusCode, env, tc.wantMsg)
		})
	}

	// 非法请求被拒后不得留下任何兑换码
	var count int64
	e.db.Model(&store.Redemption{}).Count(&count)
	if count != 0 {
		t.Fatalf("非法请求被拒后不应有落库兑换码，实际 %d 条", count)
	}

	// 正向边界：数量 1 与 1000 均放行，返回明文数量与落库条数一致。
	// 批次号按秒生成，连续两批可能同秒同号，因此按表内总数增量断言而非按批次号查询。
	for _, n := range []int{1, 1000} {
		t.Run(fmt.Sprintf("数量边界 %d", n), func(t *testing.T) {
			var before int64
			e.db.Model(&store.Redemption{}).Count(&before)
			resp, env := e.do(t, adminC, "POST", path,
				map[string]any{"count": n, "credits": 1000, "name": "边界批次"})
			if resp.StatusCode != 201 {
				t.Fatalf("数量 %d 应 201，实际 %d %v", n, resp.StatusCode, env)
			}
			data, _ := env["data"].(map[string]any)
			codes, _ := data["codes"].([]any)
			if len(codes) != n {
				t.Errorf("应返回 %d 个明文兑换码，实际 %d", n, len(codes))
			}
			var after int64
			e.db.Model(&store.Redemption{}).Count(&after)
			if after-before != int64(n) {
				t.Errorf("应新增落库 %d 条，实际新增 %d", n, after-before)
			}
		})
	}
}
