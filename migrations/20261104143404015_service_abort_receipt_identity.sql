-- migration-compat: expand
-- ADR-20261104144000: receipt and intent share the existing immutable audit row.
-- The index is built online without holding an audit-table write lock.
-- +goose NO TRANSACTION
-- +goose Up
SET lock_timeout = '5s';
SET statement_timeout = 0;
-- A cancelled concurrent build leaves an invalid index. Remove only that
-- incomplete index before retrying; IF NOT EXISTS alone would retain it.
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM pg_index WHERE indexrelid=to_regclass('deployment_audit_service_abort_attempt_idx') AND NOT indisvalid) THEN
  DROP INDEX deployment_audit_service_abort_attempt_idx;
 END IF;
END $$;
-- +goose StatementEnd
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS deployment_audit_service_abort_attempt_idx
ON deployment_audit ((data->'service_abort_receipt'->>'app_id'), (data->'service_abort_receipt'->>'attempt_id'))
WHERE data ? 'service_abort_receipt';
-- Do not mark an interrupted concurrent build as successfully applied.
-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_index WHERE indexrelid=to_regclass('deployment_audit_service_abort_attempt_idx') AND indisvalid AND indisready AND indisunique) THEN
  RAISE EXCEPTION 'service abort receipt index is invalid: DROP INDEX CONCURRENTLY deployment_audit_service_abort_attempt_idx and retry migration';
 END IF;
END $$;
-- +goose StatementEnd
RESET lock_timeout;
RESET statement_timeout;

-- +goose Down
SET lock_timeout = '5s';
SET statement_timeout = 0;
DROP INDEX CONCURRENTLY IF EXISTS deployment_audit_service_abort_attempt_idx;
RESET lock_timeout;
RESET statement_timeout;
