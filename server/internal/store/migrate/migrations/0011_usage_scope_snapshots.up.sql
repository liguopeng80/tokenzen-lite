-- 0011 用量与账务的作用域快照列。
-- 与 department_id 快照同源（0005）：部门可被删除，按 department_id join 不可靠，
-- 故在写入时点把 integration_id 冻结进流水与聚合，供按接入方隔离报表与审计。
-- 0 = 运营方内部对象（与 department_id=0「未分配」同口径）。

ALTER TABLE usage_logs         ADD COLUMN integration_id BIGINT NOT NULL DEFAULT 0;
ALTER TABLE credit_ledger      ADD COLUMN integration_id BIGINT NOT NULL DEFAULT 0;
ALTER TABLE usage_daily_rollup ADD COLUMN integration_id BIGINT NOT NULL DEFAULT 0;
-- 审计的 operator 可能为系统（无 integration），允许 NULL。
ALTER TABLE audit_logs         ADD COLUMN integration_id BIGINT;

CREATE INDEX usage_logs_integration_idx    ON usage_logs (integration_id, created_at);
CREATE INDEX credit_ledger_integration_idx ON credit_ledger (integration_id, created_at);
CREATE INDEX audit_logs_integration_idx    ON audit_logs (integration_id, created_at);
