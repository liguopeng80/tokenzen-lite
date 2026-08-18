DROP TABLE IF EXISTS usage_rollup_state;
DROP TABLE IF EXISTS usage_daily_rollup;
DROP TABLE IF EXISTS daily_spend;
DROP TABLE IF EXISTS alert_events;
DROP TABLE IF EXISTS audit_logs;

DROP INDEX IF EXISTS usage_logs_department_idx;
ALTER TABLE credit_ledger DROP COLUMN IF EXISTS operator_id;
ALTER TABLE credit_ledger DROP COLUMN IF EXISTS department_id;
ALTER TABLE usage_logs    DROP COLUMN IF EXISTS department_id;

ALTER TABLE users DROP COLUMN IF EXISTS daily_spend_limit;
ALTER TABLE users DROP COLUMN IF EXISTS allowed_models;
DROP INDEX IF EXISTS users_department_idx;
ALTER TABLE users DROP COLUMN IF EXISTS department_id;

DROP TABLE IF EXISTS departments;
