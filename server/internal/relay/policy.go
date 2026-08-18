package relay

import (
	"encoding/json"
	"fmt"

	"gorm.io/datatypes"
)

// parseModelPolicy 解析一层模型策略。
// 返回值 applies 为 false 表示该层不施加限制（未配置或配置为空数组）。
// 解析失败返回错误：策略写错时必须拒绝调用，放行等于配置静默失效，
// 与设置白名单的意图相反。
func parseModelPolicy(raw datatypes.JSON) (allowed map[string]bool, applies bool, err error) {
	if len(raw) == 0 {
		return nil, false, nil
	}
	var models []string
	if err := json.Unmarshal(raw, &models); err != nil {
		return nil, false, fmt.Errorf("模型策略不是字符串数组: %w", err)
	}
	if len(models) == 0 {
		return nil, false, nil
	}
	set := make(map[string]bool, len(models))
	for _, m := range models {
		set[m] = true
	}
	return set, true, nil
}

// AllowsModel 判断模型是否落在有效模型集合内。
// 有效集合 = 部门策略 ∩ 用户策略 ∩ 密钥白名单，各层都只能收窄不能放宽；
// 某层为空表示该层不施加限制，不参与取交集。
// 任一层解析失败即返回错误，由调用方拒绝请求并告警。
func AllowsModel(department, user, key datatypes.JSON, model string) (bool, error) {
	for _, layer := range []struct {
		name string
		raw  datatypes.JSON
	}{
		{"部门", department},
		{"用户", user},
		{"密钥", key},
	} {
		set, applies, err := parseModelPolicy(layer.raw)
		if err != nil {
			return false, fmt.Errorf("%s级模型策略配置有误: %w", layer.name, err)
		}
		if applies && !set[model] {
			return false, nil
		}
	}
	return true, nil
}
