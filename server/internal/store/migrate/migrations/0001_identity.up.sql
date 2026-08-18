-- 0001 身份与访问：users / sessions / api_keys
-- 枚举取值以 docs/glossary.md 为权威定义，数据库用 text + CHECK 落地。

CREATE TABLE users (
    id             BIGSERIAL PRIMARY KEY,
    username       TEXT        NOT NULL UNIQUE,
    password_hash  TEXT        NOT NULL,
    display_name   TEXT        NOT NULL DEFAULT '',
    email          TEXT        NOT NULL DEFAULT '',
    role           TEXT        NOT NULL DEFAULT 'user'
                   CHECK (role IN ('user', 'admin', 'root')),
    status         TEXT        NOT NULL DEFAULT 'enabled'
                   CHECK (status IN ('enabled', 'disabled')),
    credit_balance BIGINT      NOT NULL DEFAULT 0 CHECK (credit_balance >= 0),
    credit_used    BIGINT      NOT NULL DEFAULT 0,
    request_count  BIGINT      NOT NULL DEFAULT 0,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- alexedwards/scs 的 postgres store 标准表结构
CREATE TABLE sessions (
    token  TEXT PRIMARY KEY,
    data   BYTEA       NOT NULL,
    expiry TIMESTAMPTZ NOT NULL
);
CREATE INDEX sessions_expiry_idx ON sessions (expiry);

CREATE TABLE api_keys (
    id             BIGSERIAL PRIMARY KEY,
    user_id        BIGINT      NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name           TEXT        NOT NULL,
    key_hash       TEXT        NOT NULL UNIQUE,
    key_prefix     TEXT        NOT NULL,
    status         TEXT        NOT NULL DEFAULT 'enabled'
                   CHECK (status IN ('enabled', 'disabled', 'expired', 'depleted')),
    credit_limit   BIGINT      CHECK (credit_limit IS NULL OR credit_limit >= 0),
    credit_used    BIGINT      NOT NULL DEFAULT 0,
    expires_at     TIMESTAMPTZ,
    allowed_models JSONB,
    allowed_ips    JSONB,
    last_used_at   TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX api_keys_user_id_idx ON api_keys (user_id);
