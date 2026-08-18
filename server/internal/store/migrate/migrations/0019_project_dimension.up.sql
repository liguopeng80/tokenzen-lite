-- 0019 项目维度：与部门正交的第二层成本归属单元（扁平单层、不持余额、记账时点快照）。
-- 设计依据 docs/design/组织与审计模型.md（部门模型范式）与
-- .scratch/plans/2026-08-09-成本归集与装配测试方案.md（方案一）。
-- projects 与 departments 同构：单层、不持余额、不参与扣费、月度预算仅作对比。
-- 刻意不加 department_id：项目与部门是正交的两个成本维度（同一密钥可同时归属一个部门与一个项目）。
-- 刻意不加 allowed_models：模型策略保持部门/用户/密钥三层，项目不参与模型权限。

CREATE TABLE projects (
    id                     BIGSERIAL PRIMARY KEY,
    name                   TEXT        NOT NULL UNIQUE,
    code                   TEXT        NOT NULL DEFAULT '',
    owner_user_id          BIGINT      REFERENCES users (id) ON DELETE SET NULL,
    integration_id         BIGINT      REFERENCES integrations (id) ON DELETE CASCADE,
    external_ref           TEXT        NOT NULL DEFAULT '',
    monthly_budget_credits BIGINT      NOT NULL DEFAULT 0 CHECK (monthly_budget_credits >= 0),
    status                 TEXT        NOT NULL DEFAULT 'enabled'
                           CHECK (status IN ('enabled', 'disabled')),
    note                   TEXT        NOT NULL DEFAULT '',
    created_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at             TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- 成本中心编码非空时唯一（同部门范式）。
CREATE UNIQUE INDEX projects_code_uniq ON projects (code) WHERE code <> '';
-- 外部标识按接入方隔离唯一（同 departments_external_ref_uniq 范式，0012）。
CREATE UNIQUE INDEX projects_external_ref_uniq
    ON projects (integration_id, external_ref) WHERE external_ref <> '';
CREATE INDEX projects_owner_idx ON projects (owner_user_id) WHERE status = 'enabled';
-- external_ref 写入后不可变（仿 departments，0012）。
CREATE TRIGGER projects_external_ref_immutable
    BEFORE UPDATE ON projects
    FOR EACH ROW EXECUTE FUNCTION external_ref_immutable();

-- 密钥归属项目：live 外键，可空（未归属项目）。删除项目时密钥的 project_id 置 NULL，
-- 不级联删密钥（与 users.department_id 的归属语义类似，但用 SET NULL 而非 RESTRICT）。
ALTER TABLE api_keys ADD COLUMN project_id BIGINT
    REFERENCES projects (id) ON DELETE SET NULL;
CREATE INDEX api_keys_project_idx ON api_keys (project_id);

-- 项目快照：记账时点该密钥所属项目的 ID，0 = 未归属（含「密钥未挂项目」与「迁移前历史」两种）。
-- 不做快照会令密钥改挂项目后历史消费整体迁移到新项目，与已出账月份的分摊对不上（同部门快照范式）。
ALTER TABLE usage_logs ADD COLUMN project_id BIGINT NOT NULL DEFAULT 0;
CREATE INDEX usage_logs_project_idx ON usage_logs (project_id, created_at);

-- 按日汇总扩 project_id 进主键（同 0015 把 api_key_id 纳入主键的范式）：
-- project_id 必须进 rollup 才能保证「项目重指派后历史口径不变」。
-- 纯 schema 变更，不回填历史：迁移前已汇总日期在 rollup 中以 project_id=0 存在，
-- 报表按项目维度时合并显示为「未归属」；迁移后新汇总日期由 RollDay 按 project_id 分组写入。
ALTER TABLE usage_daily_rollup ADD COLUMN project_id BIGINT NOT NULL DEFAULT 0;
ALTER TABLE usage_daily_rollup DROP CONSTRAINT usage_daily_rollup_pkey;
ALTER TABLE usage_daily_rollup ADD PRIMARY KEY (
    day, user_id, department_id, project_id,
    model_name, channel_id, integration_id, api_key_id
);
