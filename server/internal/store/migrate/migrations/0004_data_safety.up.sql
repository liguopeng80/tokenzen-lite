-- 0004 数据安全：积分流水不再随用户级联删除
-- 积分流水是对账唯一事实源（见 docs/glossary.md 的积分流水条目）。原先的
-- ON DELETE CASCADE 会在删除用户时一并销毁其全部流水，使已发生的消费无法复算，
-- 且 usage_logs 无外键会留下悬空 user_id，两侧对不上账。
-- 改为 RESTRICT：有流水的账号无法被物理删除，账号退役一律走禁用。
-- api_keys 的级联删除刻意保留：密钥不是账务记录，账号消失时密钥必须同时失效，
-- 否则会留下仍可通过认证的悬空密钥。
ALTER TABLE credit_ledger DROP CONSTRAINT credit_ledger_user_id_fkey;
ALTER TABLE credit_ledger ADD CONSTRAINT credit_ledger_user_id_fkey
    FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE RESTRICT;
