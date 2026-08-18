DROP INDEX IF EXISTS idx_api_keys_user_alive;
DROP INDEX IF EXISTS idx_api_keys_key_hash_alive;

ALTER TABLE api_keys
    DROP COLUMN deleted_at;
