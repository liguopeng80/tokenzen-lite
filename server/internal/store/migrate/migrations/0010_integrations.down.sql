-- 回滚 0010：恢复三值角色约束，移除作用域列与接入方两表。

ALTER TABLE users DROP CONSTRAINT users_role_check;
ALTER TABLE users ADD CONSTRAINT users_role_check CHECK (role IN ('user', 'admin', 'root'));

DROP INDEX IF EXISTS api_keys_integration_idx;
DROP INDEX IF EXISTS departments_integration_idx;
DROP INDEX IF EXISTS users_integration_idx;
ALTER TABLE api_keys    DROP COLUMN IF EXISTS integration_id;
ALTER TABLE departments DROP COLUMN IF EXISTS integration_id;
ALTER TABLE users       DROP COLUMN IF EXISTS integration_id;

DROP TABLE IF EXISTS service_tokens;
DROP TABLE IF EXISTS integrations;
