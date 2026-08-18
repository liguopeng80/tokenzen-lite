-- 0018 回滚：移除 Key 级每日花费上限的列与计数表。
DROP TABLE IF EXISTS daily_spend_by_key;
ALTER TABLE api_keys DROP COLUMN IF EXISTS daily_spend_limit;
