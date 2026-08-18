-- 0006 审计记录的不可变性下沉到数据库层。
--
-- 此前不可变性只靠应用层保证（代码里没有更新与删除审计记录的路径），
-- 但审计的约束对象往往正是具备数据库权限的内部高权限人员——他们可以直接
-- 改表抹掉自己的操作痕迹，审计对其不构成约束。改为由触发器强制：
--   1. 任何 UPDATE 一律拒绝：审计记录一经写入即为事实，没有修正场景。
--   2. DELETE 只允许清理早于保护期的记录：保留期清理是正常运维动作，
--      但删除近期记录等同于抹除痕迹，一律拒绝。
--
-- 保护期取 30 天，与 audit_log_retention_days 的取值下限一致
-- （该设置项只接受 0（不清理）或不少于 30 天），因此正常的保留期清理不受影响。
--
-- 触发器不拦截 TRUNCATE：TRUNCATE 需要表属主权限，属于运维层面的整表重置
-- （测试库清理依赖此行为），与「改写单条记录掩盖操作」不是同一类动作。

CREATE OR REPLACE FUNCTION audit_logs_enforce_immutable() RETURNS TRIGGER AS $$
BEGIN
    IF TG_OP = 'UPDATE' THEN
        RAISE EXCEPTION '审计记录不可修改（audit_logs 为只追加表）';
    END IF;
    IF TG_OP = 'DELETE' AND OLD.created_at > now() - INTERVAL '30 days' THEN
        RAISE EXCEPTION '审计记录在写入后 30 天内不可删除（当前记录写入于 %）', OLD.created_at;
    END IF;
    RETURN OLD;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER audit_logs_immutable_update
    BEFORE UPDATE ON audit_logs
    FOR EACH ROW EXECUTE FUNCTION audit_logs_enforce_immutable();

CREATE TRIGGER audit_logs_immutable_delete
    BEFORE DELETE ON audit_logs
    FOR EACH ROW EXECUTE FUNCTION audit_logs_enforce_immutable();
