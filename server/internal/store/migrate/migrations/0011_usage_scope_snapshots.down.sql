-- 回滚 0011：移除 usage/ledger/rollup/audit 的 integration_id 快照列与索引。

DROP INDEX IF EXISTS audit_logs_integration_idx;
DROP INDEX IF EXISTS credit_ledger_integration_idx;
DROP INDEX IF EXISTS usage_logs_integration_idx;
ALTER TABLE audit_logs         DROP COLUMN IF EXISTS integration_id;
ALTER TABLE usage_daily_rollup DROP COLUMN IF EXISTS integration_id;
ALTER TABLE credit_ledger      DROP COLUMN IF EXISTS integration_id;
ALTER TABLE usage_logs         DROP COLUMN IF EXISTS integration_id;
