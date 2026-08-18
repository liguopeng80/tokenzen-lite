-- 回滚 0009：恢复 channels.provider 原始 CHECK，移除 models 新增列与别名索引。

ALTER TABLE channels DROP CONSTRAINT channels_provider_check;
ALTER TABLE channels ADD CONSTRAINT channels_provider_check CHECK (provider IN (
    'openai', 'anthropic', 'gemini', 'zhipu', 'qwen',
    'deepseek', 'minimax', 'xai', 'custom'
));

DROP INDEX IF EXISTS models_alias_uniq;

ALTER TABLE models
    DROP COLUMN IF EXISTS alias,
    DROP COLUMN IF EXISTS capabilities,
    DROP COLUMN IF EXISTS max_output,
    DROP COLUMN IF EXISTS context_window,
    DROP COLUMN IF EXISTS provider;
