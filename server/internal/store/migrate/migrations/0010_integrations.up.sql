-- 0010 接入方托管：integrations（接入方）与 service_tokens（服务令牌），
-- 以及 users/departments/api_keys 的 integration_id 作用域列。
-- integration_id 语义：NULL = 运营方内部对象；非空 = 归属某接入方。
-- 配套新增角色 managed（托管管理员），rank 介于 user 与 admin 之间。
-- 设计依据 docs/glossary.md 的「接入方与服务令牌」「Role」节。

CREATE TABLE integrations (
    id         BIGSERIAL    PRIMARY KEY,
    name       TEXT         NOT NULL,
    -- slug 是接入方的不可变标识，服务账号用户名 svc:<slug> 与令牌归属都引用它。
    slug       TEXT         NOT NULL UNIQUE,
    status     TEXT         NOT NULL DEFAULT 'enabled'
               CHECK (status IN ('enabled', 'disabled')),
    created_at TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ  NOT NULL DEFAULT now()
);

-- 服务令牌：接入方后端进程的管理端机器凭据，哈希存储，
-- 与 /v1 下游的 API Key（tzl-）彻底分离（tzs- 前缀）。
-- 每个令牌认证一个 role=managed 的服务账号用户（见应用层 admin_integrations）。
CREATE TABLE service_tokens (
    id             BIGSERIAL   PRIMARY KEY,
    integration_id BIGINT      NOT NULL REFERENCES integrations (id) ON DELETE CASCADE,
    name           TEXT        NOT NULL,
    token_hash     TEXT        NOT NULL,
    token_prefix   TEXT        NOT NULL,
    status         TEXT        NOT NULL DEFAULT 'enabled'
                   CHECK (status IN ('enabled', 'disabled')),
    last_used_at   TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at     TIMESTAMPTZ
);
CREATE INDEX service_tokens_integration_idx ON service_tokens (integration_id);
-- 软删除：哈希唯一只在存活记录间生效，便于停用后重建同名令牌（与 api_keys 0008 同构）。
CREATE UNIQUE INDEX service_tokens_token_hash_alive
    ON service_tokens (token_hash) WHERE deleted_at IS NULL;

-- 作用域列。SET NULL：integration 极少硬删（运营只停用），即便发生其对象回落为内部。
ALTER TABLE users       ADD COLUMN integration_id BIGINT REFERENCES integrations (id) ON DELETE SET NULL;
ALTER TABLE departments ADD COLUMN integration_id BIGINT REFERENCES integrations (id) ON DELETE SET NULL;
ALTER TABLE api_keys    ADD COLUMN integration_id BIGINT REFERENCES integrations (id) ON DELETE SET NULL;
CREATE INDEX users_integration_idx       ON users (integration_id);
CREATE INDEX departments_integration_idx ON departments (integration_id);
CREATE INDEX api_keys_integration_idx    ON api_keys (integration_id);

-- 角色受控枚举补 managed。
ALTER TABLE users DROP CONSTRAINT users_role_check;
ALTER TABLE users ADD CONSTRAINT users_role_check CHECK (role IN ('user', 'managed', 'admin', 'root'));
