-- 0020 给 alert_events.alert_type 加 CHECK 约束。
-- alert_type 此前是裸 TEXT，拼写错误的类型值（如 'chanel_auto_disabled'）会被 DB 静默接受，
-- 而域层无对应枚举，事后查询永远查不到这条记录。约束取值与 domain.AlertType 全集对齐，
-- 与 domain.AlertType 的同步由 internal/alerting 的枚举覆盖测试钉死。
-- 纯 CHECK 约束扩展，不新增列、不回填数据。若存量历史数据中出现未登记的非法 alert_type，
-- ADD CONSTRAINT 会失败——此时应先清洗历史数据，而不是放宽约束。

ALTER TABLE alert_events ADD CONSTRAINT alert_events_alert_type_check
    CHECK (alert_type IN (
        'channel_auto_disabled',
        'reconcile_failed',
        'usage_log_dropped',
        'orphan_precharge_found',
        'department_over_budget',
        'backup_failed',
        'user_low_balance',
        'user_balance_notice',
        'monthly_grant_failed',
        'error_rate_high',
        'latency_degraded',
        'policy_malformed',
        'alert_test'
    ));
