-- 0021 渠道连通测试结果状态：管理端「最近测试」列展示成功/失败，便于管理员一眼定位异常渠道。
-- nullable：未做过测试的渠道保持 NULL，列表显示「-」。

ALTER TABLE channels ADD COLUMN last_test_status text;
