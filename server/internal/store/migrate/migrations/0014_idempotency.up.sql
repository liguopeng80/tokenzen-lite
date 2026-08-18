-- 0014 通用幂等记录：建用户、建部门、签发 Key 等非账务写操作的幂等。
-- 与 credit_ledger 的 (request_id, entry_type) 账务幂等并存，命名空间独立：
-- 账务幂等键编码进流水 request_id（billing.AdminRequestID），本表不碰那个命名空间。
-- 唯一键按 (idempotency_key, scope, integration_id) 复合；
-- COALESCE(integration_id, 0) 让运营方侧（integration_id NULL）的幂等同样生效。

CREATE TABLE idempotency_records (
    id              BIGSERIAL    PRIMARY KEY,
    idempotency_key TEXT         NOT NULL,
    -- scope 区分用途，如 user.create / department.create / api_key.issue。
    scope           TEXT         NOT NULL,
    integration_id  BIGINT,
    response_status INTEGER      NOT NULL,
    response_body   JSONB,
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX idempotency_records_uniq
    ON idempotency_records (idempotency_key, scope, COALESCE(integration_id, 0));
