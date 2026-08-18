-- 0002 定价与积分账本：models / model_prices / model_peak_rules / credit_ledger / redemptions / settings

CREATE TABLE models (
    id           BIGSERIAL PRIMARY KEY,
    name         TEXT        NOT NULL UNIQUE,
    display_name TEXT        NOT NULL DEFAULT '',
    description  TEXT        NOT NULL DEFAULT '',
    modality     TEXT        NOT NULL DEFAULT 'text'
                 CHECK (modality IN ('text', 'embedding', 'image')),
    billing_mode TEXT        NOT NULL DEFAULT 'per_token'
                 CHECK (billing_mode IN ('per_token', 'per_call')),
    status       TEXT        NOT NULL DEFAULT 'enabled'
                 CHECK (status IN ('enabled', 'disabled')),
    tags         TEXT        NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 模型单价，全部为"积分"计价：token 类单价按 积分/1M tokens，按次单价按 积分/次。
CREATE TABLE model_prices (
    model_id           BIGINT      PRIMARY KEY REFERENCES models (id) ON DELETE CASCADE,
    input_price        BIGINT      NOT NULL DEFAULT 0 CHECK (input_price >= 0),
    output_price       BIGINT      NOT NULL DEFAULT 0 CHECK (output_price >= 0),
    cache_read_price   BIGINT      NOT NULL DEFAULT 0 CHECK (cache_read_price >= 0),
    cache_write_price  BIGINT      NOT NULL DEFAULT 0 CHECK (cache_write_price >= 0),
    audio_input_price  BIGINT      NOT NULL DEFAULT 0 CHECK (audio_input_price >= 0),
    audio_output_price BIGINT      NOT NULL DEFAULT 0 CHECK (audio_output_price >= 0),
    per_call_price     BIGINT      NOT NULL DEFAULT 0 CHECK (per_call_price >= 0),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 特殊时段消耗倍率：multiplier_percent 为百分数（150 = 1.5 倍），命中多条取最大。
CREATE TABLE model_peak_rules (
    id                 BIGSERIAL PRIMARY KEY,
    model_id           BIGINT      NOT NULL REFERENCES models (id) ON DELETE CASCADE,
    timezone           TEXT        NOT NULL DEFAULT 'Asia/Shanghai',
    start_minute       INT         NOT NULL CHECK (start_minute >= 0 AND start_minute < 1440),
    end_minute         INT         NOT NULL CHECK (end_minute > 0 AND end_minute <= 1440),
    days_of_week       JSONB       NOT NULL DEFAULT '[1,2,3,4,5,6,7]',
    multiplier_percent INT         NOT NULL CHECK (multiplier_percent >= 100),
    enabled            BOOLEAN     NOT NULL DEFAULT true,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (start_minute < end_minute)
);
CREATE INDEX model_peak_rules_model_id_idx ON model_peak_rules (model_id);

-- 积分流水：对账唯一事实源。
CREATE TABLE credit_ledger (
    id            BIGSERIAL PRIMARY KEY,
    user_id       BIGINT      NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    entry_type    TEXT        NOT NULL
                  CHECK (entry_type IN ('grant', 'revoke', 'redeem', 'consume', 'refund', 'settle_adjust')),
    amount        BIGINT      NOT NULL,
    balance_after BIGINT      NOT NULL,
    ref_type      TEXT        NOT NULL DEFAULT '',
    ref_id        BIGINT      NOT NULL DEFAULT 0,
    request_id    TEXT        NOT NULL DEFAULT '',
    note          TEXT        NOT NULL DEFAULT '',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX credit_ledger_user_id_idx ON credit_ledger (user_id, created_at);
-- 同一请求同一类型的流水只允许一条，保证结算/退款幂等
CREATE UNIQUE INDEX credit_ledger_request_entry_uniq
    ON credit_ledger (request_id, entry_type) WHERE request_id <> '';

CREATE TABLE redemptions (
    id              BIGSERIAL PRIMARY KEY,
    batch_id        TEXT        NOT NULL DEFAULT '',
    code_hash       TEXT        NOT NULL UNIQUE,
    name            TEXT        NOT NULL DEFAULT '',
    credits         BIGINT      NOT NULL CHECK (credits > 0),
    status          TEXT        NOT NULL DEFAULT 'unused'
                    CHECK (status IN ('unused', 'used', 'disabled')),
    used_by_user_id BIGINT,
    redeemed_at     TIMESTAMPTZ,
    expires_at      TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX redemptions_batch_idx ON redemptions (batch_id);

-- 系统设置：键白名单与取值校验在 Go 侧（settings 包）强制。
CREATE TABLE settings (
    key        TEXT        PRIMARY KEY,
    value      JSONB       NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
