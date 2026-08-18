package store

import (
	"context"
	"testing"

	"gorm.io/gorm"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
)

// CountEnabledByModel 集成测试（P3-10）：模型可用性统计只计入启用渠道。
// 需要 TZL_TEST_DATABASE_URL，未设置时跳过。

func newChannelCountTestRepo(t *testing.T) (*gorm.DB, *ChannelRepo) {
	t.Helper()
	db := newStoreTestDB(t)
	if err := db.Exec("TRUNCATE channels RESTART IDENTITY CASCADE").Error; err != nil {
		t.Fatalf("清空 channels 表失败: %v", err)
	}
	return db, NewChannelRepo(db)
}

func seedCountChannel(t *testing.T, db *gorm.DB, name string,
	status domain.ChannelStatus, modelsJSON string) {
	t.Helper()
	ch := &Channel{
		Name: name, Provider: domain.ProviderZhipu, Protocol: domain.ProtocolOpenAICompat,
		BaseURL: "http://unused.example", APIKeyEncrypted: "enc",
		Models: []byte(modelsJSON), ModelMapping: []byte("{}"),
		Status: status, Priority: 0, Weight: 1,
		ParamOverride: []byte("{}"), HeaderOverride: []byte("{}"),
	}
	if err := db.Create(ch).Error; err != nil {
		t.Fatalf("种入渠道 %s 失败: %v", name, err)
	}
}

// S1：启用渠道1承载[A,B]、启用渠道2承载[B]、manual_disabled 渠道承载[C]
// → {A:1, B:2}，C 不出现。
func TestCountEnabledByModelBasic(t *testing.T) {
	db, repo := newChannelCountTestRepo(t)
	seedCountChannel(t, db, "ch-1", domain.ChannelEnabled, `["model-a","model-b"]`)
	seedCountChannel(t, db, "ch-2", domain.ChannelEnabled, `["model-b"]`)
	seedCountChannel(t, db, "ch-manual-off", domain.ChannelManualDisabled, `["model-c"]`)

	counts, err := repo.CountEnabledByModel(context.Background())
	if err != nil {
		t.Fatalf("CountEnabledByModel 失败: %v", err)
	}
	if len(counts) != 2 {
		t.Errorf("应只统计 2 个模型，实际: %v", counts)
	}
	if counts["model-a"] != 1 {
		t.Errorf("model-a 承载数应为 1，实际 %d", counts["model-a"])
	}
	if counts["model-b"] != 2 {
		t.Errorf("model-b 承载数应为 2，实际 %d", counts["model-b"])
	}
	if _, ok := counts["model-c"]; ok {
		t.Errorf("manual_disabled 渠道承载的 model-c 不应出现在计数中: %v", counts)
	}
}

// S2：auto_disabled 渠道承载的模型不计入计数。
func TestCountEnabledByModelExcludesAutoDisabled(t *testing.T) {
	db, repo := newChannelCountTestRepo(t)
	seedCountChannel(t, db, "ch-auto-off", domain.ChannelAutoDisabled, `["model-d"]`)
	seedCountChannel(t, db, "ch-live", domain.ChannelEnabled, `["model-e"]`)

	counts, err := repo.CountEnabledByModel(context.Background())
	if err != nil {
		t.Fatalf("CountEnabledByModel 失败: %v", err)
	}
	if _, ok := counts["model-d"]; ok {
		t.Errorf("auto_disabled 渠道承载的 model-d 不应出现在计数中: %v", counts)
	}
	if counts["model-e"] != 1 {
		t.Errorf("model-e 承载数应为 1，实际 %d", counts["model-e"])
	}
}

// S3：无任何渠道 → 返回空 map，不报错。
func TestCountEnabledByModelEmpty(t *testing.T) {
	_, repo := newChannelCountTestRepo(t)

	counts, err := repo.CountEnabledByModel(context.Background())
	if err != nil {
		t.Fatalf("无渠道时不应报错: %v", err)
	}
	if len(counts) != 0 {
		t.Errorf("无渠道时应返回空 map，实际: %v", counts)
	}
}

// S4：启用渠道 models 为空数组 → 不产生任何计数行。
func TestCountEnabledByModelEmptyModels(t *testing.T) {
	db, repo := newChannelCountTestRepo(t)
	seedCountChannel(t, db, "ch-empty-models", domain.ChannelEnabled, `[]`)

	counts, err := repo.CountEnabledByModel(context.Background())
	if err != nil {
		t.Fatalf("CountEnabledByModel 失败: %v", err)
	}
	if len(counts) != 0 {
		t.Errorf("空模型清单的启用渠道不应产生计数行，实际: %v", counts)
	}
}
