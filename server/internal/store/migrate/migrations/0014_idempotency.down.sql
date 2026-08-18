-- 回滚 0014：移除通用幂等记录表。

DROP TABLE IF EXISTS idempotency_records;
