package pricing

import (
	"testing"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
)

// TestPresetsWellFormed 内置预置价目的结构约束。
// 业务后果：预置价目是新装系统上架模型的起点，一条模型形态或计费方式写错，
// 导入后该模型要么零扣费，要么按错误维度计费。
func TestPresetsWellFormed(t *testing.T) {
	catalog, err := Presets()
	if err != nil {
		t.Fatalf("加载预置价目失败: %v", err)
	}
	if catalog.PricedAt == "" {
		t.Error("预置价目必须标注采集月份，否则无法判断价目是否过期")
	}
	if len(catalog.Providers) == 0 {
		t.Fatal("预置价目为空")
	}
	seen := map[string]string{}
	for _, p := range catalog.Providers {
		if p.ID == "" || p.Name == "" {
			t.Errorf("厂商 %+v 缺少 id 或 name", p)
		}
		if p.PricingURL == "" {
			t.Errorf("厂商 %s 缺少定价页地址，运维无从核对价目", p.ID)
		}
		if len(p.Models) == 0 {
			t.Errorf("厂商 %s 没有任何预置模型", p.ID)
		}
		for _, m := range p.Models {
			if m.Name == "" {
				t.Errorf("厂商 %s 存在无名模型", p.ID)
				continue
			}
			if prev, dup := seen[m.Name]; dup {
				t.Errorf("模型名 %s 在 %s 与 %s 下重复：模型名是全局唯一键，导入时后者必然失败",
					m.Name, prev, p.ID)
			}
			seen[m.Name] = p.ID

			switch domain.Modality(m.Modality) {
			case domain.ModalityText, domain.ModalityEmbedding, domain.ModalityImage:
			default:
				t.Errorf("模型 %s 的形态 %q 不是合法枚举", m.Name, m.Modality)
			}
			switch domain.BillingMode(m.BillingMode) {
			case domain.BillPerToken:
				if m.InputUSD <= 0 && m.OutputUSD <= 0 {
					t.Errorf("按 token 计费的 %s 输入与输出官价均为零，导入后调用零扣费", m.Name)
				}
			case domain.BillPerCall:
				if m.PerCallUSD <= 0 {
					t.Errorf("按次计费的 %s 缺少按次官价，导入后调用零扣费", m.Name)
				}
			default:
				t.Errorf("模型 %s 的计费方式 %q 不是合法枚举", m.Name, m.BillingMode)
			}
		}
	}
}

// TestPresetToCreditPrice 预置价目折算为积分单价时逐项换算，未计价项保持为零。
func TestPresetToCreditPrice(t *testing.T) {
	m := PresetModel{
		Name: "demo", Modality: string(domain.ModalityText), BillingMode: string(domain.BillPerToken),
		InputUSD: 3 * microUSDPerUSD, OutputUSD: 15 * microUSDPerUSD, CacheReadUSD: 300_000,
	}
	got := m.ToCreditPrice(7200, 1_000_000, 100)
	if got.InputPrice != 21_600_000 {
		t.Errorf("输入单价 = %d，期望 21600000", got.InputPrice)
	}
	if got.OutputPrice != 108_000_000 {
		t.Errorf("输出单价 = %d，期望 108000000", got.OutputPrice)
	}
	if got.CacheReadPrice != 2_160_000 {
		t.Errorf("缓存读单价 = %d，期望 2160000", got.CacheReadPrice)
	}
	if got.CacheWritePrice != 0 || got.PerCallPrice != 0 {
		t.Errorf("厂商未计价的项应保持为零，实际缓存写 %d、按次 %d",
			got.CacheWritePrice, got.PerCallPrice)
	}
}
