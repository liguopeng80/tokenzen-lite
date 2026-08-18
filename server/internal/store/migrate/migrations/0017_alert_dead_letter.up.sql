-- 0017 告警投递新增 dead_letter 状态：后台指数退避重试耗尽后标记，
-- 与 failed（一次失败 / 重试过程中的失败）区分，便于管理员在告警记录页
-- 单独筛选需要人工介入的事件。语义见 docs/glossary.md 的 AlertStatus。
-- 纯 CHECK 约束扩展，不新增列、不回填数据。

ALTER TABLE alert_events DROP CONSTRAINT alert_events_status_check;
ALTER TABLE alert_events ADD CONSTRAINT alert_events_status_check
    CHECK (status IN ('pending', 'sent', 'failed', 'suppressed', 'dead_letter'));
