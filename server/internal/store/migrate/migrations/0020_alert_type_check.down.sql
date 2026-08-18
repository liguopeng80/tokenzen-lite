-- 回滚 0020：移除 alert_type 的 CHECK 约束。

ALTER TABLE alert_events DROP CONSTRAINT alert_events_alert_type_check;
