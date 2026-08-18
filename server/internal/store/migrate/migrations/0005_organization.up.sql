-- 0005 组织化管理：departments / audit_logs / alert_events / daily_spend / usage_daily_rollup
-- 概念定义见 docs/design/组织与审计模型.md，枚举取值以 docs/glossary.md 为权威。

-- 部门：成本归属单元，单层无上下级。不持有积分余额、不参与扣费。
CREATE TABLE departments (
    id                     BIGSERIAL PRIMARY KEY,
    name                   TEXT        NOT NULL UNIQUE,
    code                   TEXT        NOT NULL DEFAULT '',
    owner_user_id          BIGINT      REFERENCES users (id) ON DELETE SET NULL,
    monthly_budget_credits BIGINT      NOT NULL DEFAULT 0 CHECK (monthly_budget_credits >= 0),
    allowed_models         JSONB,
    status                 TEXT        NOT NULL DEFAULT 'enabled'
                           CHECK (status IN ('enabled', 'disabled')),
    note                   TEXT        NOT NULL DEFAULT '',
    created_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at             TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- 成本中心编码非空时唯一，留空的部门不参与该约束
CREATE UNIQUE INDEX departments_code_uniq ON departments (code) WHERE code <> '';

-- 用户归属 0 或 1 个部门。RESTRICT：部门仍有成员时不允许删除，必须先转出成员。
ALTER TABLE users ADD COLUMN department_id BIGINT
    REFERENCES departments (id) ON DELETE RESTRICT;
CREATE INDEX users_department_idx ON users (department_id);

-- 管理员可控的用户级模型策略。NULL 或空数组 = 该层不施加限制。
ALTER TABLE users ADD COLUMN allowed_models JSONB;
-- 单个用户单个自然日的累计扣费积分上限，0 = 不限制。
ALTER TABLE users ADD COLUMN daily_spend_limit BIGINT NOT NULL DEFAULT 0
    CHECK (daily_spend_limit >= 0);

-- 部门快照：记账时点用户所属部门，0 = 未分配。
-- 不做快照会使用户转部门后其历史消费在报表中整体迁移到新部门，与已出账的分摊对不上。
ALTER TABLE usage_logs    ADD COLUMN department_id BIGINT NOT NULL DEFAULT 0;
ALTER TABLE credit_ledger ADD COLUMN department_id BIGINT NOT NULL DEFAULT 0;
-- 发起该笔调整的管理员，0 = 消费/退款/兑换等非管理动作。
ALTER TABLE credit_ledger ADD COLUMN operator_id BIGINT NOT NULL DEFAULT 0;
CREATE INDEX usage_logs_department_idx ON usage_logs (department_id, created_at);

-- 操作审计：只追加，无更新与删除路径。
CREATE TABLE audit_logs (
    id            BIGSERIAL PRIMARY KEY,
    operator_id   BIGINT      NOT NULL DEFAULT 0,
    operator_name TEXT        NOT NULL DEFAULT '',
    operator_role TEXT        NOT NULL DEFAULT '',
    action        TEXT        NOT NULL,
    target_type   TEXT        NOT NULL DEFAULT '',
    target_id     BIGINT      NOT NULL DEFAULT 0,
    target_name   TEXT        NOT NULL DEFAULT '',
    result        TEXT        NOT NULL DEFAULT 'success'
                  CHECK (result IN ('success', 'failure')),
    before_state  JSONB,
    after_state   JSONB,
    client_ip     TEXT        NOT NULL DEFAULT '',
    request_id    TEXT        NOT NULL DEFAULT '',
    message       TEXT        NOT NULL DEFAULT '',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX audit_logs_created_idx  ON audit_logs (created_at DESC);
CREATE INDEX audit_logs_operator_idx ON audit_logs (operator_id, created_at DESC);
CREATE INDEX audit_logs_target_idx   ON audit_logs (target_type, target_id, created_at DESC);
CREATE INDEX audit_logs_action_idx   ON audit_logs (action, created_at DESC);

-- 告警事件：既是投递队列，也是"没收到告警"时区分未触发与投递失败的依据。
CREATE TABLE alert_events (
    id            BIGSERIAL PRIMARY KEY,
    alert_type    TEXT        NOT NULL,
    severity      TEXT        NOT NULL DEFAULT 'warning'
                  CHECK (severity IN ('critical', 'warning')),
    dedup_key     TEXT        NOT NULL DEFAULT '',
    title         TEXT        NOT NULL DEFAULT '',
    message       TEXT        NOT NULL DEFAULT '',
    payload       JSONB,
    status        TEXT        NOT NULL DEFAULT 'pending'
                  CHECK (status IN ('pending', 'sent', 'failed', 'suppressed')),
    channels_sent JSONB,
    attempts      INT         NOT NULL DEFAULT 0,
    last_error    TEXT        NOT NULL DEFAULT '',
    sent_at       TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX alert_events_created_idx ON alert_events (created_at DESC);
CREATE INDEX alert_events_dedup_idx   ON alert_events (dedup_key, created_at DESC);
CREATE INDEX alert_events_status_idx  ON alert_events (status, created_at);

-- 每日花费计数：在余额调整的同一事务内维护，与积分流水同源。
CREATE TABLE daily_spend (
    user_id    BIGINT      NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    day        DATE        NOT NULL,
    credits    BIGINT      NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, day)
);

-- 用量按日聚合：报表数据源，只汇总已结算记录。
CREATE TABLE usage_daily_rollup (
    day                 DATE   NOT NULL,
    user_id             BIGINT NOT NULL,
    department_id       BIGINT NOT NULL DEFAULT 0,
    model_name          TEXT   NOT NULL,
    channel_id          BIGINT NOT NULL DEFAULT 0,
    requests            BIGINT NOT NULL DEFAULT 0,
    prompt_tokens       BIGINT NOT NULL DEFAULT 0,
    completion_tokens   BIGINT NOT NULL DEFAULT 0,
    cache_read_tokens   BIGINT NOT NULL DEFAULT 0,
    cache_write_tokens  BIGINT NOT NULL DEFAULT 0,
    credits_charged     BIGINT NOT NULL DEFAULT 0,
    credits_cost        BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (day, user_id, department_id, model_name, channel_id)
);
CREATE INDEX usage_daily_rollup_day_idx        ON usage_daily_rollup (day);
CREATE INDEX usage_daily_rollup_department_idx ON usage_daily_rollup (department_id, day);

-- 已完成汇总的日期水位，供报表判断某日读聚合表还是读原始日志，
-- 以及清理原始日志前校验该日期确已汇总。
CREATE TABLE usage_rollup_state (
    day          DATE        PRIMARY KEY,
    rows_rolled  BIGINT      NOT NULL DEFAULT 0,
    completed_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
