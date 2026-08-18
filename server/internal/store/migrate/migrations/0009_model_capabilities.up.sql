-- 0009 模型能力字段与厂商扩展：models 加 provider/context_window/max_output/capabilities/alias，
-- channels.provider CHECK 增加 moonshot。
-- 设计依据 docs/模型与厂商能力需求分析.md 与 docs/glossary.md 的「模型能力属性」节。

ALTER TABLE models
    ADD COLUMN provider       TEXT        NOT NULL DEFAULT '',
    ADD COLUMN context_window BIGINT      NOT NULL DEFAULT 0 CHECK (context_window >= 0),
    ADD COLUMN max_output     BIGINT      NOT NULL DEFAULT 0 CHECK (max_output >= 0),
    ADD COLUMN capabilities   JSONB       NOT NULL DEFAULT '[]',
    ADD COLUMN alias          TEXT        NOT NULL DEFAULT '';

-- 对外别名全局唯一：空串不占唯一位（多模型允许 alias 为空）。
CREATE UNIQUE INDEX models_alias_uniq ON models (alias) WHERE alias <> '';

-- channels.provider 受控枚举补 moonshot（Kimi）。约束名为 PG 默认 channels_provider_check。
ALTER TABLE channels DROP CONSTRAINT channels_provider_check;
ALTER TABLE channels ADD CONSTRAINT channels_provider_check CHECK (provider IN (
    'openai', 'anthropic', 'gemini', 'zhipu', 'qwen',
    'deepseek', 'minimax', 'xai', 'moonshot', 'custom'
));
