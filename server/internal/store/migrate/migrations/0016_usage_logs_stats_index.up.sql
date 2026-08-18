-- 0016 为管理端原始日志统计（heatmap/health-timeline/cost-by-calltype）补建索引。
-- 这些查询以 created_at 范围 + 可选 status 为过滤前置，无维度列收窄，
-- 现有索引均以维度列打头（user/channel/model/department/integration），无法命中，
-- 退化为整段保留窗口的顺序扫描。按 (status, created_at) 建索引覆盖该访问模式。

CREATE INDEX usage_logs_status_created_idx ON usage_logs (status, created_at);

-- health-timeline 仅按 created_at 范围过滤（不限定 status，需统计失败），
-- (status, created_at) 复合索引对其无导引作用，单独建 created_at 索引覆盖。
CREATE INDEX usage_logs_created_idx ON usage_logs (created_at);
