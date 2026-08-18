package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// importItem 构造一条合法的导入记录。
func importItem(name string, inputPrice int64) map[string]any {
	return map[string]any{
		"name": name, "display_name": name + " 展示名",
		"modality": "text", "billing_mode": "per_token",
		"provider": "openai", "context_window": 200000, "max_output": 8192,
		"capabilities": []string{"vision"},
		"price":        map[string]any{"input_price": inputPrice, "output_price": inputPrice * 3},
	}
}

// importSummary 发起导入并返回汇总计数与逐条结果。
func (e *testEnv) importModels(t *testing.T, c *http.Client, body map[string]any) (int, map[string]any) {
	t.Helper()
	resp, env := e.do(t, c, "POST", "/api/admin/models/import", body)
	data, _ := env["data"].(map[string]any)
	return resp.StatusCode, data
}

func counts(data map[string]any) (created, updated, skipped, failed int) {
	get := func(k string) int {
		v, _ := data[k].(float64)
		return int(v)
	}
	return get("created"), get("updated"), get("skipped"), get("failed")
}

func resultAt(t *testing.T, data map[string]any, i int) map[string]any {
	t.Helper()
	list, _ := data["results"].([]any)
	if i >= len(list) {
		t.Fatalf("结果条数不足：期望至少 %d 条，实际 %d 条", i+1, len(list))
	}
	item, _ := list[i].(map[string]any)
	return item
}

// TestImportModelsCreatesCatalogWithPrice 批量导入把模型与定价一并落库，
// 用户随后即可按该单价被扣费；这是新装系统上架模型的主路径。
func TestImportModelsCreatesCatalogWithPrice(t *testing.T) {
	e := newTestEnv(t)
	c := e.seedAndLogin(t, "import-admin", domain.RoleAdmin)

	status, data := e.importModels(t, c, map[string]any{
		"items": []any{importItem("import-a", 1000), importItem("import-b", 2000)},
	})
	if status != http.StatusOK {
		t.Fatalf("导入应 200，实际 %d", status)
	}
	created, updated, skipped, failed := counts(data)
	if created != 2 || updated != 0 || skipped != 0 || failed != 0 {
		t.Fatalf("期望新建 2 条，实际 created=%d updated=%d skipped=%d failed=%d",
			created, updated, skipped, failed)
	}
	if action, _ := resultAt(t, data, 0)["action"].(string); action != string(domain.ImportCreated) {
		t.Errorf("首条结果动作应为 created，实际 %q", action)
	}

	var m store.Model
	if err := e.db.Preload("Price").Where("name = ?", "import-a").First(&m).Error; err != nil {
		t.Fatalf("导入的模型未落库: %v", err)
	}
	if m.Price == nil {
		t.Fatal("导入的模型没有定价：上架后调用会零扣费")
	}
	if m.Price.InputPrice != 1000 || m.Price.OutputPrice != 3000 {
		t.Errorf("单价落库不符：输入 %d 输出 %d", m.Price.InputPrice, m.Price.OutputPrice)
	}
	if m.Status != domain.ModelEnabled || m.Modality != domain.ModalityText {
		t.Errorf("模型默认状态或形态不符：status=%s modality=%s", m.Status, m.Modality)
	}
}

// TestImportModelsSkipsExistingWithoutOverwrite 未选择覆盖时，
// 已存在的同名模型保持原有定价不变——防止重复导入把站点自定的价格改回预置价。
func TestImportModelsSkipsExistingWithoutOverwrite(t *testing.T) {
	e := newTestEnv(t)
	c := e.seedAndLogin(t, "import-admin", domain.RoleAdmin)

	e.importModels(t, c, map[string]any{"items": []any{importItem("import-keep", 1000)}})
	status, data := e.importModels(t, c, map[string]any{"items": []any{importItem("import-keep", 9999)}})
	if status != http.StatusOK {
		t.Fatalf("重复导入应 200，实际 %d", status)
	}
	created, updated, skipped, failed := counts(data)
	if created != 0 || updated != 0 || skipped != 1 || failed != 0 {
		t.Fatalf("期望跳过 1 条，实际 created=%d updated=%d skipped=%d failed=%d",
			created, updated, skipped, failed)
	}

	var p store.ModelPrice
	e.db.Joins("JOIN models ON models.id = model_prices.model_id").
		Where("models.name = ?", "import-keep").First(&p)
	if p.InputPrice != 1000 {
		t.Errorf("跳过的模型单价被改动：期望 1000，实际 %d", p.InputPrice)
	}
}

// TestImportModelsOverwriteUpdatesPrice 选择覆盖时按提交内容更新定价，
// 用于跟随厂商调价重新导入。
func TestImportModelsOverwriteUpdatesPrice(t *testing.T) {
	e := newTestEnv(t)
	c := e.seedAndLogin(t, "import-admin", domain.RoleAdmin)

	e.importModels(t, c, map[string]any{"items": []any{importItem("import-ow", 1000)}})
	status, data := e.importModels(t, c, map[string]any{
		"items": []any{importItem("import-ow", 5000)}, "overwrite": true,
	})
	if status != http.StatusOK {
		t.Fatalf("覆盖导入应 200，实际 %d", status)
	}
	created, updated, skipped, failed := counts(data)
	if created != 0 || updated != 1 || skipped != 0 || failed != 0 {
		t.Fatalf("期望覆盖 1 条，实际 created=%d updated=%d skipped=%d failed=%d",
			created, updated, skipped, failed)
	}

	var p store.ModelPrice
	e.db.Joins("JOIN models ON models.id = model_prices.model_id").
		Where("models.name = ?", "import-ow").First(&p)
	if p.InputPrice != 5000 || p.OutputPrice != 15000 {
		t.Errorf("覆盖后单价不符：输入 %d 输出 %d", p.InputPrice, p.OutputPrice)
	}

	var count int64
	e.db.Model(&store.Model{}).Where("name = ?", "import-ow").Count(&count)
	if count != 1 {
		t.Errorf("覆盖不应产生重复模型，实际 %d 条", count)
	}
}

// TestImportModelsPerItemFailureIsolated 单条记录不合法时只该条失败，
// 同批次其余记录照常写入——一次导入几十条时不因一条错误全批回退。
func TestImportModelsPerItemFailureIsolated(t *testing.T) {
	e := newTestEnv(t)
	c := e.seedAndLogin(t, "import-admin", domain.RoleAdmin)

	noPrice := importItem("import-nopricce", 1000)
	delete(noPrice, "price")
	negative := importItem("import-negative", 1000)
	negative["price"] = map[string]any{"input_price": -1}
	zeroPrice := importItem("import-zero", 0)
	zeroPrice["price"] = map[string]any{"input_price": 0, "output_price": 0}

	status, data := e.importModels(t, c, map[string]any{"items": []any{
		importItem("import-good", 1000),
		noPrice,
		negative,
		zeroPrice,
		map[string]any{"name": "", "price": map[string]any{"input_price": 1}},
		importItem("import-good", 2000), // 同批次内重名
	}})
	if status != http.StatusOK {
		t.Fatalf("含失败条目的导入仍应 200（逐条回报），实际 %d", status)
	}
	created, _, _, failed := counts(data)
	if created != 1 || failed != 5 {
		t.Fatalf("期望成功 1 条、失败 5 条，实际 created=%d failed=%d", created, failed)
	}
	for i, wantHint := range map[int]string{1: "定价", 2: "负数", 3: "单价", 4: "模型名称", 5: "重复"} {
		res := resultAt(t, data, i)
		if action, _ := res["action"].(string); action != string(domain.ImportFailed) {
			t.Errorf("第 %d 条应失败，实际 %q", i, action)
		}
		if msg, _ := res["message"].(string); !strings.Contains(msg, wantHint) {
			t.Errorf("第 %d 条失败原因应提到 %q，实际 %q", i, wantHint, msg)
		}
	}
	var count int64
	e.db.Model(&store.Model{}).Count(&count)
	if count != 1 {
		t.Errorf("失败条目不应落库，期望库中 1 个模型，实际 %d", count)
	}
}

// TestImportModelsRejectsEmptyAndOversized 空导入与超量导入在入口即被拒绝。
func TestImportModelsRejectsEmptyAndOversized(t *testing.T) {
	e := newTestEnv(t)
	c := e.seedAndLogin(t, "import-admin", domain.RoleAdmin)

	resp, env := e.do(t, c, "POST", "/api/admin/models/import", map[string]any{"items": []any{}})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("空导入应 400，实际 %d %v", resp.StatusCode, env)
	}

	items := make([]any, maxImportItems+1)
	for i := range items {
		items[i] = importItem("bulk-"+string(rune('a'+i%26))+string(rune('a'+i/26)), 1000)
	}
	resp, env = e.do(t, c, "POST", "/api/admin/models/import", map[string]any{"items": items})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("超量导入应 400，实际 %d %v", resp.StatusCode, env)
	}
	var count int64
	e.db.Model(&store.Model{}).Count(&count)
	if count != 0 {
		t.Errorf("被拒绝的导入不应写入任何模型，实际 %d 条", count)
	}
}

// TestPricingPresetsConvertsWithMarkup 预置价目按当前汇率与兑换率折算为积分单价，
// 加价百分数生效；折算在服务端完成，管理端预览值与导入后生效值同源。
func TestPricingPresetsConvertsWithMarkup(t *testing.T) {
	e := newTestEnv(t)
	c := e.seedAndLogin(t, "preset-admin", domain.RoleAdmin)

	resp, env := e.do(t, c, "GET", "/api/admin/models/pricing-presets", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("预置价目应 200，实际 %d %v", resp.StatusCode, env)
	}
	data, _ := env["data"].(map[string]any)
	if pricedAt, _ := data["priced_at"].(string); pricedAt == "" {
		t.Error("预置价目响应缺少采集月份，管理员无从判断价目是否过期")
	}
	providers, _ := data["providers"].([]any)
	if len(providers) == 0 {
		t.Fatal("预置价目响应无厂商")
	}
	first, _ := providers[0].(map[string]any)
	models, _ := first["models"].([]any)
	if len(models) == 0 {
		t.Fatal("首个厂商无预置模型")
	}
	m, _ := models[0].(map[string]any)
	price, _ := m["price"].(map[string]any)
	base, _ := price["input_price"].(float64)
	if base <= 0 {
		t.Fatalf("平价折算的输入单价应为正数，实际 %v", price["input_price"])
	}

	// 加价 200% 时单价应为平价的两倍（同一条模型）。
	resp, env = e.do(t, c, "GET", "/api/admin/models/pricing-presets?markup_percent=200", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("带加价的预置价目应 200，实际 %d %v", resp.StatusCode, env)
	}
	data, _ = env["data"].(map[string]any)
	providers, _ = data["providers"].([]any)
	first, _ = providers[0].(map[string]any)
	models, _ = first["models"].([]any)
	m, _ = models[0].(map[string]any)
	price, _ = m["price"].(map[string]any)
	doubled, _ := price["input_price"].(float64)
	if doubled != base*2 {
		t.Errorf("加价 200%% 的单价应为平价的两倍：平价 %v，实际 %v", base, doubled)
	}

	// 越界的加价百分数必须拒绝，防止把"加价 30%"误填成 3000。
	for _, bad := range []string{"99", "1001", "abc"} {
		resp, _ = e.do(t, c, "GET", "/api/admin/models/pricing-presets?markup_percent="+bad, nil)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("加价百分数 %q 应 400，实际 %d", bad, resp.StatusCode)
		}
	}
}

// TestImportPresetPricesRoundTrip 预置价目折算出的单价可直接提交导入并原样落库，
// 保证"预览什么价，导入后就是什么价"。
func TestImportPresetPricesRoundTrip(t *testing.T) {
	e := newTestEnv(t)
	c := e.seedAndLogin(t, "preset-admin", domain.RoleAdmin)

	_, env := e.do(t, c, "GET", "/api/admin/models/pricing-presets?markup_percent=130", nil)
	data, _ := env["data"].(map[string]any)
	providers, _ := data["providers"].([]any)
	first, _ := providers[0].(map[string]any)
	models, _ := first["models"].([]any)
	m, _ := models[0].(map[string]any)

	name, _ := m["name"].(string)
	price, _ := m["price"].(map[string]any)
	item := map[string]any{
		"name": name, "display_name": m["display_name"], "modality": m["modality"],
		"billing_mode": m["billing_mode"], "price": price,
	}
	status, summary := e.importModels(t, c, map[string]any{"items": []any{item}})
	if status != http.StatusOK {
		t.Fatalf("导入预置模型应 200，实际 %d", status)
	}
	if created, _, _, failed := counts(summary); created != 1 || failed != 0 {
		t.Fatalf("预置模型导入应成功，实际 created=%d failed=%d", created, failed)
	}

	var stored store.ModelPrice
	e.db.Joins("JOIN models ON models.id = model_prices.model_id").
		Where("models.name = ?", name).First(&stored)
	want, _ := price["input_price"].(float64)
	if stored.InputPrice != int64(want) {
		t.Errorf("落库单价与预览值不一致：预览 %v，落库 %d", want, stored.InputPrice)
	}
}
