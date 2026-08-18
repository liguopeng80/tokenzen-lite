-- 0018 Key 级每日花费上限：与用户级 daily_spend_limit 并行的按 Key 维度限额。
-- api_keys.daily_spend_limit 为 0 表示该 Key 不限制（与 users.daily_spend_limit 同口径）；
-- 当日累计计数由 daily_spend_by_key 承载，写入与用户级 daily_spend 同在余额调整事务内，
-- 由 maintenance 按同一保留期清理，口径与用户级完全一致。

ALTER TABLE api_keys ADD COLUMN daily_spend_limit BIGINT NOT NULL DEFAULT 0
    CHECK (daily_spend_limit >= 0);

CREATE TABLE daily_spend_by_key (
    api_key_id BIGINT      NOT NULL REFERENCES api_keys (id) ON DELETE CASCADE,
    day        DATE        NOT NULL,
    credits    BIGINT      NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (api_key_id, day)
);
