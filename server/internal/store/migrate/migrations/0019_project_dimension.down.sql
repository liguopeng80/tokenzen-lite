-- 0019 反向：还原 rollup 主键为 7 元组，移除 project_id 列，删 projects 表与 api_keys.project_id。

ALTER TABLE usage_daily_rollup DROP CONSTRAINT usage_daily_rollup_pkey;
ALTER TABLE usage_daily_rollup ADD PRIMARY KEY (
    day, user_id, department_id, model_name, channel_id, integration_id, api_key_id
);
ALTER TABLE usage_daily_rollup DROP COLUMN IF EXISTS project_id;

DROP INDEX IF EXISTS usage_logs_project_idx;
ALTER TABLE usage_logs DROP COLUMN IF EXISTS project_id;

DROP INDEX IF EXISTS api_keys_project_idx;
ALTER TABLE api_keys DROP COLUMN IF EXISTS project_id;

DROP TRIGGER IF EXISTS projects_external_ref_immutable ON projects;
DROP INDEX IF EXISTS projects_owner_idx;
DROP INDEX IF EXISTS projects_external_ref_uniq;
DROP INDEX IF EXISTS projects_code_uniq;
DROP TABLE IF EXISTS projects;
