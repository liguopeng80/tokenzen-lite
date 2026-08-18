-- 回滚 0012：移除 external_ref 列、唯一索引与不可变触发器。

DROP TRIGGER IF EXISTS departments_external_ref_immutable ON departments;
DROP TRIGGER IF EXISTS users_external_ref_immutable ON users;
DROP FUNCTION IF EXISTS external_ref_immutable();

DROP INDEX IF EXISTS departments_external_ref_uniq;
DROP INDEX IF EXISTS users_external_ref_uniq;
ALTER TABLE departments DROP COLUMN IF EXISTS external_ref;
ALTER TABLE users       DROP COLUMN IF EXISTS external_ref;
