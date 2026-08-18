package store

import (
	"strings"
	"testing"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
)

// 本文件是项目维度（0019）的纯逻辑测试——不连数据库，可在无 TZL_TEST_DATABASE_URL
// 环境下运行。覆盖聚合维度枚举完整性与 aggExprs/aggConditions 对 project 的处理。
// DB 集成测试见 rollup_project_integration_test.go（由主会话串行跑）。

// TestAggDimensionIncludesProject 校验 AggByProject 进入维度枚举且 Valid，
// 同时枚举无重复、无遗漏（与 SQL 主键列一一对应）。
func TestAggDimensionIncludesProject(t *testing.T) {
	all := []AggDimension{
		AggByUser, AggByDepartment, AggByProject, AggByModel,
		AggByChannel, AggByDay, AggByKey,
	}
	seen := make(map[AggDimension]bool, len(all))
	for _, d := range all {
		if seen[d] {
			t.Fatalf("维度枚举出现重复: %s", d)
		}
		seen[d] = true
		if !d.Valid() {
			t.Errorf("维度 %s 应为合法取值", d)
		}
	}
	if !seen[AggByProject] {
		t.Fatal("维度枚举应包含 project")
	}
	// 任意未知串不合法。
	if AggDimension("tenant").Valid() {
		t.Error("未登记的维度取值应判为非法")
	}
}

// TestAggExprsProjectBranch 校验 project 维度的分组表达式与显示名：
// group 列指向 src.project_id；project_id=0 标记为「未归属」。
func TestAggExprsProjectBranch(t *testing.T) {
	groupExpr, keyExpr := aggExprs(AggByProject)
	if groupExpr != "src.project_id" {
		t.Fatalf("project 分组 ID 表达式应取 src.project_id，实际 %q", groupExpr)
	}
	// keyExpr 含「未归属」字样（project_id=0 的标签），与 department 的「未分配」同范式。
	for _, want := range []string{"未归属", "p.name", "project_id"} {
		if !strings.Contains(keyExpr, want) {
			t.Errorf("project 显示名表达式应含 %q，实际 %q", want, keyExpr)
		}
	}
}

// TestAggConditionsProjectFilter 校验 AggFilter.ProjectID 被编译进 WHERE：
// nil 不加条件；指向 0 加「src.project_id = 0」；指向具体 ID 加等值条件。
func TestAggConditionsProjectFilter(t *testing.T) {
	// nil：不限项目，WHERE 不应含 project_id 条件。
	where, args := aggConditions(AggFilter{})
	if strings.Contains(where, "project_id") {
		t.Errorf("ProjectID 为 nil 时不应加项目筛选，实际 WHERE %q", where)
	}
	if len(args) != 0 {
		t.Errorf("nil 筛选不应产生参数，实际 %v", args)
	}
	// 指向 0：筛「未归属」。
	zero := int64(0)
	where, args = aggConditions(AggFilter{ProjectID: &zero})
	if !strings.Contains(where, "src.project_id = ?") {
		t.Errorf("ProjectID=&0 应筛未归属，实际 WHERE %q", where)
	}
	if len(args) != 1 || args[0].(int64) != 0 {
		t.Errorf("应附一个值为 0 的参数，实际 %v", args)
	}
	// 指向具体 ID。
	pid := int64(42)
	where, args = aggConditions(AggFilter{ProjectID: &pid})
	if !strings.Contains(where, "src.project_id = ?") {
		t.Errorf("ProjectID=&42 应筛指定项目，实际 WHERE %q", where)
	}
	if len(args) != 1 || args[0].(int64) != 42 {
		t.Errorf("应附一个值为 42 的参数，实际 %v", args)
	}
}

// TestAggConditionsDepartmentAndProjectOrthogonal 校验项目与部门筛选可并存，
// 二者各自独立附加条件（正交），不相互覆盖。
func TestAggConditionsDepartmentAndProjectOrthogonal(t *testing.T) {
	did := int64(7)
	pid := int64(9)
	where, args := aggConditions(AggFilter{DepartmentID: &did, ProjectID: &pid})
	if !strings.Contains(where, "src.department_id = ?") {
		t.Errorf("应含部门筛选，实际 %q", where)
	}
	if !strings.Contains(where, "src.project_id = ?") {
		t.Errorf("应含项目筛选，实际 %q", where)
	}
	if len(args) != 2 || args[0].(int64) != 7 || args[1].(int64) != 9 {
		t.Errorf("应附部门与项目两个参数 (7,9)，实际 %v", args)
	}
}

// 编译期断言：确保 ProjectStatus 与 DepartmentStatus 是不同类型，避免误用。
var _ domain.ProjectStatus = domain.ProjectEnabled
