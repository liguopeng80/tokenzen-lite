-- 回滚 0015：恢复原始主键（不含 integration_id/api_key_id），移除 api_key_id 列。
-- 注意：若回滚前已有按 api_key_id 拆分的汇总行（同一 5 元组对应多行），
-- 恢复原主键会因重复键失败。仅在干净环境（无按密钥拆分数据）下回滚。

ALTER TABLE usage_daily_rollup DROP CONSTRAINT usage_daily_rollup_pkey;
ALTER TABLE usage_daily_rollup ADD PRIMARY KEY (day, user_id, department_id, model_name, channel_id);
ALTER TABLE usage_daily_rollup DROP COLUMN api_key_id;
