-- 0007 首次登录强制改密：管理员建号、批量导入与重置密码后，初始密码只是一次性凭证，
-- 由本人首次登录时改掉。标志清除的唯一途径是用户自己调用改密接口。

ALTER TABLE users
    ADD COLUMN must_change_password BOOLEAN NOT NULL DEFAULT FALSE;

-- 存量账号不追溯置位：其密码由本人掌握，强制改密只会打断在用的账号。
