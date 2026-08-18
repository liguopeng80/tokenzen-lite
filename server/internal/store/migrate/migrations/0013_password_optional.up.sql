-- 0013 托管账号无口令：放开 users.password_hash 的 NOT NULL。
-- 托管账号不用于登录（由接入方经服务令牌代管），其调用走 API Key，与口令无关。
-- 登录路径对空口令账号显式拒绝，不泄露账号存在性。

ALTER TABLE users ALTER COLUMN password_hash DROP NOT NULL;
