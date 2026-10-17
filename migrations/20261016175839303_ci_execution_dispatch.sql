-- +goose Up
-- ADR-1654: bounded apid reconciliation; no native instance ownership changes.
CREATE TABLE IF NOT EXISTS ci_dispatch_budget (lock_id boolean PRIMARY KEY CHECK(lock_id));
INSERT INTO ci_dispatch_budget VALUES(true) ON CONFLICT (lock_id) DO NOTHING;
ALTER TABLE ci_executions
 ADD COLUMN IF NOT EXISTS next_reconcile_at timestamptz NOT NULL DEFAULT now(),
 ADD COLUMN IF NOT EXISTS reconcile_failures integer NOT NULL DEFAULT 0 CHECK(reconcile_failures BETWEEN 0 AND 16);
CREATE INDEX IF NOT EXISTS ci_executions_dispatch_idx ON ci_executions(next_reconcile_at,id)
 WHERE mode='jobs' AND status IN ('queued','running','cancelling');

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM ci_executions WHERE mode='jobs' AND status IN ('queued','running','cancelling')) THEN
  RAISE EXCEPTION 'reconcile active CI executions before removing dispatcher recovery state';
 END IF;
END $$;
-- +goose StatementEnd
DROP INDEX ci_executions_dispatch_idx;
ALTER TABLE ci_executions DROP COLUMN reconcile_failures, DROP COLUMN next_reconcile_at;
DROP TABLE ci_dispatch_budget;
