// Package domain 定义跨模块共享的业务枚举与核心值类型。
// 全部取值以 docs/glossary.md 为权威定义，先改文档再改代码。
package domain

// Role 用户角色。
type Role string

const (
	RoleUser    Role = "user"
	RoleManaged Role = "managed" // 托管管理员：接入方服务账号身份，介于 user 与 admin 之间。
	RoleAdmin   Role = "admin"
	RoleRoot    Role = "root"
)

var roleRank = map[Role]int{RoleUser: 1, RoleManaged: 5, RoleAdmin: 10, RoleRoot: 100}

// AtLeast 判断角色权限是否不低于 other。
func (r Role) AtLeast(other Role) bool {
	return roleRank[r] >= roleRank[other]
}

// Valid 判断角色取值是否合法。
func (r Role) Valid() bool {
	_, ok := roleRank[r]
	return ok
}

// UserStatus 用户状态。
type UserStatus string

const (
	UserEnabled  UserStatus = "enabled"
	UserDisabled UserStatus = "disabled"
)

// KeyStatus API Key 状态。
type KeyStatus string

const (
	KeyEnabled  KeyStatus = "enabled"
	KeyDisabled KeyStatus = "disabled"
	KeyExpired  KeyStatus = "expired"
	KeyDepleted KeyStatus = "depleted"
)

// Credits 积分数额（系统内部记账单位）。
type Credits = int64

// ImportAction 批量导入模型时，单条记录的处理结果。
type ImportAction string

const (
	// ImportCreated 模型不存在，已按提交内容新建（含定价）。
	ImportCreated ImportAction = "created"
	// ImportUpdated 模型已存在且请求要求覆盖，已更新展示信息与定价。
	ImportUpdated ImportAction = "updated"
	// ImportSkipped 模型已存在且请求未要求覆盖，原有配置保持不变。
	ImportSkipped ImportAction = "skipped"
	// ImportFailed 该条记录未通过校验或写入失败，其余记录不受影响。
	ImportFailed ImportAction = "failed"
)
