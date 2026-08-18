-- 0015 按日汇总表增加 api_key_id 列，主键扩展为含 integration_id 与 api_key_id。
-- 纯 schema 变更，不做回填：迁移前已汇总的日期在表中以 api_key_id=0 存在，
-- 按「密钥」维度报表里标记为「历史汇总（按密钥不可拆）」；迁移后新汇总的日期
-- 由 RollDay 按 api_key_id 分组写入。回填需按服务器时区重算历史日志的日期分桶，
-- 存在时区口径错配、静默 corrupt 报表数据的风险，故不做（见 docs/tbd.md）。

ALTER TABLE usage_daily_rollup ADD COLUMN api_key_id BIGINT NOT NULL DEFAULT 0;

-- 原主键 (day, user_id, department_id, model_name, channel_id) 不含 integration_id
-- （0011 增列时未改主键）。本迁移一并补齐 integration_id 并纳入 api_key_id：
-- 同一 (day,user,dept,model,channel) 在不同密钥/接入方下应为不同行。
ALTER TABLE usage_daily_rollup DROP CONSTRAINT usage_daily_rollup_pkey;
ALTER TABLE usage_daily_rollup ADD PRIMARY KEY (day, user_id, department_id, model_name, channel_id, integration_id, api_key_id);
