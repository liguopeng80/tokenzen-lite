DROP TRIGGER IF EXISTS audit_logs_immutable_delete ON audit_logs;
DROP TRIGGER IF EXISTS audit_logs_immutable_update ON audit_logs;
DROP FUNCTION IF EXISTS audit_logs_enforce_immutable();
