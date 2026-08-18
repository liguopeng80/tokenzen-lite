-- 0008 API Key 改为软删除：密钥泄漏或出现异常调用时要能事后追查。物理删除会让
-- 用量日志里的 api_key_id 指向一条不存在的记录，追查时既查不出这个密钥曾经存在，
-- 也查不出它何时由谁删除。删除后的密钥不再参与认证与列表，只保留供追溯的档案。

ALTER TABLE api_keys
    ADD COLUMN deleted_at TIMESTAMPTZ;

-- 认证与列表查询一律带 deleted_at IS NULL，索引按该条件裁剪。
CREATE INDEX idx_api_keys_key_hash_alive ON api_keys (key_hash) WHERE deleted_at IS NULL;
CREATE INDEX idx_api_keys_user_alive ON api_keys (user_id) WHERE deleted_at IS NULL;
