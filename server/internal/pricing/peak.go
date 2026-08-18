package pricing

import (
	"time"
)

// PeakRule 是一条时段倍率规则（业务定义见 docs/glossary.md）。
type PeakRule struct {
	Timezone          string
	StartMinute       int   // 当日分钟数 [0, 1440)
	EndMinute         int   // 当日分钟数 (0, 1440]，区间为 [start, end)
	DaysOfWeek        []int // ISO 星期（1=周一 ... 7=周日）
	MultiplierPercent int
	Enabled           bool
}

// EvaluatePeakMultiplier 求给定时刻命中的最大倍率百分数；无命中返回 100。
// 时区解析失败的规则跳过（数据在写入时已校验，此处防御）。
func EvaluatePeakMultiplier(rules []PeakRule, at time.Time) int {
	best := BaseMultiplierPercent
	for _, r := range rules {
		if !r.Enabled || r.MultiplierPercent <= best {
			continue
		}
		loc, err := time.LoadLocation(r.Timezone)
		if err != nil {
			continue
		}
		local := at.In(loc)
		weekday := int(local.Weekday())
		if weekday == 0 {
			weekday = 7 // time.Sunday=0 → ISO 7
		}
		if !containsInt(r.DaysOfWeek, weekday) {
			continue
		}
		minute := local.Hour()*60 + local.Minute()
		if minute >= r.StartMinute && minute < r.EndMinute {
			best = r.MultiplierPercent
		}
	}
	return best
}

func containsInt(list []int, v int) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
