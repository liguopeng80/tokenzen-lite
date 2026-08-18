// Package audit 记录管理侧写操作与认证事件。记录只追加，无更新与删除路径。
// 覆盖范围与动作枚举见 docs/glossary.md，设计依据见 docs/design/组织与审计模型.md。
package audit

import (
	"context"
	"encoding/json"
	"net/http"

	"gorm.io/datatypes"

	"github.com/liguopeng80/tokenzen-lite/server/internal/auth"
	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/obs"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
	"github.com/liguopeng80/tokenzen-lite/server/internal/strutil"
)

// SensitiveFields 是任何对象上都不得记录取值的字段名。审计只表达「该字段被修改过」，
// 连旧值也不记录——审计表的可读范围比这些字段的原始存储位置更宽。
var SensitiveFields = map[string]bool{
	"password":             true,
	"new_password":         true,
	"old_password":         true,
	"password_hash":        true,
	"api_key":              true,
	"api_key_encrypted":    true,
	"alert_smtp_password":  true,
	"alert_webhook_secret": true,
	"idempotency_key":      true,
	"session_token":        true,
	"key":                  true,
	"key_hash":             true,
}

// sensitiveByTarget 是只在特定对象类型上敏感的字段名。字段名会在不同对象上重名：
// 兑换码的 code 是凭据，泄漏即可被兑换；部门的 code 是成本中心编码，是对接财务
// 系统的关键标识，其变更必须能从审计里查出改成了什么。因此脱敏按「对象类型 +
// 字段名」判定，不按字段名一刀切。
var sensitiveByTarget = map[domain.AuditTargetType]map[string]bool{
	domain.AuditTargetRedemption: {"code": true, "codes": true},
}

// Recorder 写入审计记录。写入失败不影响主业务返回，但会输出 ERROR 日志——
// 静默失败会让「审计里查不到」与「操作没发生」无法区分。
type Recorder struct {
	repo *store.AuditLogRepo
}

func NewRecorder(repo *store.AuditLogRepo) *Recorder { return &Recorder{repo: repo} }

// Entry 描述一条待记录的审计事件。
type Entry struct {
	Action     domain.AuditAction
	TargetType domain.AuditTargetType
	TargetID   int64
	TargetName string
	Result     domain.AuditResult
	Before     map[string]any
	After      map[string]any
	Message    string
	// Operator 显式指定操作人。为 nil 时从请求上下文取当前登录用户；
	// 两者都没有时记为系统操作（operator_id = 0）。
	Operator *store.User
}

// Record 记录一条审计事件。r 用于提取操作人、来源 IP 与请求标识。
func (rec *Recorder) Record(r *http.Request, e Entry) {
	if rec == nil || rec.repo == nil {
		return
	}
	ctx := r.Context()
	operator := e.Operator
	if operator == nil {
		operator = auth.CurrentUser(ctx)
	}
	log := &store.AuditLog{
		Action:      e.Action,
		TargetType:  e.TargetType,
		TargetID:    e.TargetID,
		TargetName:  strutil.Truncate(e.TargetName, 200),
		Result:      e.Result,
		BeforeState: encodeState(e.TargetType, e.Before),
		AfterState:  encodeState(e.TargetType, e.After),
		ClientIP:    obs.ClientIP(r),
		RequestID:   obs.RequestID(ctx),
		Message:     strutil.Truncate(e.Message, 500),
	}
	if log.Result == "" {
		log.Result = domain.AuditSuccess
	}
	if operator != nil {
		log.OperatorID = operator.ID
		log.OperatorName = operator.Username
		log.OperatorRole = operator.Role
		// 托管服务令牌触发的操作带上 integration_id，使审计可按作用域筛选（R1）。
		log.IntegrationID = operator.IntegrationID
	}
	rec.write(ctx, log)
}

// RecordSystem 记录一条由系统自身发起的审计事件（定时任务、启动自检），
// 无 HTTP 请求上下文。
func (rec *Recorder) RecordSystem(ctx context.Context, e Entry) {
	if rec == nil || rec.repo == nil {
		return
	}
	log := &store.AuditLog{
		Action:      e.Action,
		TargetType:  e.TargetType,
		TargetID:    e.TargetID,
		TargetName:  strutil.Truncate(e.TargetName, 200),
		Result:      e.Result,
		BeforeState: encodeState(e.TargetType, e.Before),
		AfterState:  encodeState(e.TargetType, e.After),
		RequestID:   obs.RequestID(ctx),
		Message:     strutil.Truncate(e.Message, 500),
	}
	if log.Result == "" {
		log.Result = domain.AuditSuccess
	}
	// 系统侧操作也可能处于托管作用域（定时任务按接入方遍历时），取 ctx 的作用域。
	log.IntegrationID = auth.ScopeIntegrationID(ctx)
	rec.write(ctx, log)
}

func (rec *Recorder) write(ctx context.Context, log *store.AuditLog) {
	if err := rec.repo.Create(ctx, log); err != nil {
		obs.Logger(ctx).Error("写入审计记录失败",
			"action", log.Action, "target_type", log.TargetType,
			"target_id", log.TargetID, "error", err)
	}
}

// encodeState 序列化变更快照并对敏感字段脱敏。
func encodeState(targetType domain.AuditTargetType, state map[string]any) datatypes.JSON {
	if len(state) == 0 {
		return nil
	}
	raw, err := json.Marshal(Redact(targetType, state))
	if err != nil {
		return nil
	}
	return datatypes.JSON(raw)
}

// Redact 返回脱敏后的副本：敏感字段的值替换为占位符，其余原样保留。
// targetType 决定那些只在特定对象上敏感的字段是否脱敏，见 sensitiveByTarget。
// 不修改传入的 map。
func Redact(targetType domain.AuditTargetType, state map[string]any) map[string]any {
	scoped := sensitiveByTarget[targetType]
	out := make(map[string]any, len(state))
	for k, v := range state {
		if SensitiveFields[k] || scoped[k] {
			out[k] = domain.AuditRedacted
			continue
		}
		if nested, ok := v.(map[string]any); ok {
			out[k] = Redact(targetType, nested)
			continue
		}
		out[k] = v
	}
	return out
}

// Diff 返回两个字段快照中取值不同的部分，用于只记录本次实际改动的字段。
// 变更前缺失的键视为新增。
func Diff(before, after map[string]any) (map[string]any, map[string]any) {
	changedBefore := map[string]any{}
	changedAfter := map[string]any{}
	for k, newVal := range after {
		oldVal, existed := before[k]
		if existed && equalJSON(oldVal, newVal) {
			continue
		}
		if existed {
			changedBefore[k] = oldVal
		}
		changedAfter[k] = newVal
	}
	return changedBefore, changedAfter
}

// equalJSON 以 JSON 序列化结果比较两个值，避免切片、映射无法直接比较。
func equalJSON(a, b any) bool {
	ra, errA := json.Marshal(a)
	rb, errB := json.Marshal(b)
	if errA != nil || errB != nil {
		return false
	}
	return string(ra) == string(rb)
}
