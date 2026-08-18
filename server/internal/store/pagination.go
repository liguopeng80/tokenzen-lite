package store

// 分页兜底常量，与 api.PageParams 的统一解析保持一致：
// 默认每页 20 条，上限 100 条。
const (
	// DefaultPageSize 是每页条数缺省或非法（< 1）时的回落值。
	DefaultPageSize = 20
	// MaxPageSize 是每页条数的上限，超限时截断。
	MaxPageSize = 100
)

// NormalizePagination 归一化分页参数，作为仓储层自身的兜底：
// 页码 < 1 归一为 1；每页条数 < 1 回落为 DefaultPageSize，
// 超过 MaxPageSize 截断为 MaxPageSize。
// 调用方即使绕过 api.PageParams 直接传入非法值，
// 也不会产生负偏移量或不限条数的全表拉取。
func NormalizePagination(page, pageSize int) (int, int) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = DefaultPageSize
	}
	if pageSize > MaxPageSize {
		pageSize = MaxPageSize
	}
	return page, pageSize
}
