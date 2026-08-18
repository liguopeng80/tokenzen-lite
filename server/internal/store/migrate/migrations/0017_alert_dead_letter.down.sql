-- 回滚 0017：移除 dead_letter 状态。已有死信事件先归并回 failed，避免约束校验失败。

UPDATE alert_events SET status = 'failed' WHERE status = 'dead_letter';

ALTER TABLE alert_events DROP CONSTRAINT alert_events_status_check;
ALTER TABLE alert_events ADD CONSTRAINT alert_events_status_check
    CHECK (status IN ('pending', 'sent', 'failed', 'suppressed'));
