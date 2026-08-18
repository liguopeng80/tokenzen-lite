-- 0003 中继：channels / channel_costs / usage_logs

CREATE TABLE channels (
    id                   BIGSERIAL PRIMARY KEY,
    name                 TEXT        NOT NULL,
    provider             TEXT        NOT NULL
                         CHECK (provider IN ('openai', 'anthropic', 'gemini', 'zhipu', 'qwen',
                                             'deepseek', 'minimax', 'xai', 'custom')),
    protocol             TEXT        NOT NULL
                         CHECK (protocol IN ('openai_compat', 'anthropic', 'gemini')),
    base_url             TEXT        NOT NULL,
    api_key_encrypted    TEXT        NOT NULL DEFAULT '',
    models               JSONB       NOT NULL DEFAULT '[]',
    model_mapping        JSONB       NOT NULL DEFAULT '{}',
    status               TEXT        NOT NULL DEFAULT 'enabled'
                         CHECK (status IN ('enabled', 'manual_disabled', 'auto_disabled')),
    priority             INT         NOT NULL DEFAULT 0,
    weight               INT         NOT NULL DEFAULT 1 CHECK (weight >= 0),
    param_override       JSONB       NOT NULL DEFAULT '{}',
    header_override      JSONB       NOT NULL DEFAULT '{}',
    test_model           TEXT        NOT NULL DEFAULT '',
    last_test_at         TIMESTAMPTZ,
    last_test_latency_ms BIGINT,
    disabled_reason      TEXT        NOT NULL DEFAULT '',
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX channels_models_gin_idx ON channels USING GIN (models);
CREATE INDEX channels_status_idx ON channels (status);

-- 渠道 × 模型成本单价（利润分析）。currency=usd 时单位为 微美元/1M tokens，
-- credits 时与售价同单位（积分/1M tokens 或 积分/次）。
CREATE TABLE channel_costs (
    id                BIGSERIAL PRIMARY KEY,
    channel_id        BIGINT      NOT NULL REFERENCES channels (id) ON DELETE CASCADE,
    model_name        TEXT        NOT NULL,
    currency          TEXT        NOT NULL DEFAULT 'credits'
                      CHECK (currency IN ('credits', 'usd')),
    input_cost        BIGINT      NOT NULL DEFAULT 0 CHECK (input_cost >= 0),
    output_cost       BIGINT      NOT NULL DEFAULT 0 CHECK (output_cost >= 0),
    cache_read_cost   BIGINT      NOT NULL DEFAULT 0 CHECK (cache_read_cost >= 0),
    cache_write_cost  BIGINT      NOT NULL DEFAULT 0 CHECK (cache_write_cost >= 0),
    per_call_cost     BIGINT      NOT NULL DEFAULT 0 CHECK (per_call_cost >= 0),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (channel_id, model_name)
);

CREATE TABLE usage_logs (
    id                      BIGSERIAL PRIMARY KEY,
    request_id              TEXT        NOT NULL UNIQUE,
    user_id                 BIGINT      NOT NULL,
    api_key_id              BIGINT      NOT NULL,
    model_name              TEXT        NOT NULL,
    upstream_model          TEXT        NOT NULL DEFAULT '',
    channel_id              BIGINT      NOT NULL DEFAULT 0,
    protocol                TEXT        NOT NULL DEFAULT '',
    is_stream               BOOLEAN     NOT NULL DEFAULT false,
    prompt_tokens           BIGINT      NOT NULL DEFAULT 0,
    completion_tokens       BIGINT      NOT NULL DEFAULT 0,
    cache_read_tokens       BIGINT      NOT NULL DEFAULT 0,
    cache_write_tokens      BIGINT      NOT NULL DEFAULT 0,
    audio_input_tokens      BIGINT      NOT NULL DEFAULT 0,
    audio_output_tokens     BIGINT      NOT NULL DEFAULT 0,
    call_count              BIGINT      NOT NULL DEFAULT 0,
    usage_semantic          TEXT        NOT NULL DEFAULT '',
    usage_estimated         BOOLEAN     NOT NULL DEFAULT false,
    peak_multiplier_percent INT         NOT NULL DEFAULT 100,
    credits_precharged      BIGINT      NOT NULL DEFAULT 0,
    credits_charged         BIGINT      NOT NULL DEFAULT 0,
    credits_cost            BIGINT      NOT NULL DEFAULT 0,
    status                  TEXT        NOT NULL DEFAULT 'settled'
                            CHECK (status IN ('settled', 'refunded', 'failed')),
    error_class             TEXT        NOT NULL DEFAULT '',
    error_message           TEXT        NOT NULL DEFAULT '',
    latency_ms              BIGINT      NOT NULL DEFAULT 0,
    first_byte_ms           BIGINT      NOT NULL DEFAULT 0,
    client_ip               TEXT        NOT NULL DEFAULT '',
    price_snapshot          JSONB,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX usage_logs_user_idx ON usage_logs (user_id, created_at);
CREATE INDEX usage_logs_channel_idx ON usage_logs (channel_id, created_at);
CREATE INDEX usage_logs_model_idx ON usage_logs (model_name, created_at);
