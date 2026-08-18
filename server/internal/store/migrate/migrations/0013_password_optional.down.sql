-- 回滚 0013：恢复 password_hash NOT NULL。
-- 仅当无空口令账号时才能成功（存量托管账号会阻止恢复）。

ALTER TABLE users ALTER COLUMN password_hash SET NOT NULL;
