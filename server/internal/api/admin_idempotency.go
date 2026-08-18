package api

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/liguopeng80/tokenzen-lite/server/internal/auth"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// admin_idempotency.go：建用户、建部门、签发 Key 等非账务写操作的幂等（R4）。
//
// 与积分幂等（credit_ledger 的 (request_id, entry_type) 唯一索引）并存、命名空间独立：
// 这里只存「首次创建的对象 id」，重放时按 id 回查对象、附带重放标记返回，
// 避免对完整响应体做缓冲回放。覆盖需求关注的是「网络超时后串行重试」，不是并发；
// 极小并发竞态由 idempotency_records 的唯一索引兜底（第二次写入失败，但首次创建已落地）。
//
// 这两个 helper 跨 userAdmin / catalogAdmin / orgAdmin 三组 controller 共用，
// 故为包级自由函数，签名收 *store.IdempotencyRepo 而非绑定某个 controller。

// idempotencyFirstID 从幂等记录的 response_body 解出首次创建的对象 id。
func idempotencyFirstID(body []byte) (int64, bool) {
	if len(body) == 0 {
		return 0, false
	}
	var first struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(body, &first); err != nil {
		return 0, false
	}
	return first.ID, first.ID != 0
}

// idempotencyLookupReplay 查幂等键是否有首次记录，返回首次对象 id。
// scope 区分用途（user.create / department.create / api_key.issue）；
// 作用域取当前 actor 的 integration（托管）或 nil（运营），与记录表唯一键口径一致。
func idempotencyLookupReplay(ctx context.Context, repo *store.IdempotencyRepo, key, scope string) (int64, bool) {
	if key == "" || repo == nil {
		return 0, false
	}
	rec, err := repo.GetByKey(ctx, key, scope, auth.ScopeIntegrationID(ctx))
	if err != nil || rec == nil {
		return 0, false
	}
	return idempotencyFirstID(rec.ResponseBody)
}

// idempotencyRemember 记录首次创建的对象 id，供后续重放回查。
func idempotencyRemember(ctx context.Context, repo *store.IdempotencyRepo, key, scope string, id int64) {
	if key == "" || repo == nil {
		return
	}
	body, _ := json.Marshal(map[string]int64{"id": id})
	_ = repo.Record(ctx, key, scope, auth.ScopeIntegrationID(ctx), http.StatusCreated, body)
}
