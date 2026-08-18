ALTER TABLE credit_ledger DROP CONSTRAINT credit_ledger_user_id_fkey;
ALTER TABLE credit_ledger ADD CONSTRAINT credit_ledger_user_id_fkey
    FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE;
