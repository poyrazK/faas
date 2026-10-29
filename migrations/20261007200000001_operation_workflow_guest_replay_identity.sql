-- filename: 20261007200000001_operation_workflow_guest_replay_identity.sql
-- migration-compat: contract
-- migration-lint: allow index-build customer_operation_executions: 0 rows / 48 kB on production, measured 2026-10-17 23:19 UTC
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';
-- ADR-521. Keep native guest custody independent of the replayed current
-- execution index, as the coordinator custody ledger already is. The backend
-- exactly-one identity check and both workflow-kind checks retain the same
-- operation, generation, workflow-run and execution-kind authority.
CREATE UNIQUE INDEX IF NOT EXISTS customer_operation_executions_workflow_custody_idx
 ON customer_operation_executions(operation_id,generation,workflow_run_id,execution_kind);
-- The replacement key is added NOT VALID under a new name, validated without
-- blocking writes, and only then replaces the old key (ADR-202610171900).
-- Each statement commits on its own; every step is idempotent.
-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'customer_operation_workflow_guest_claims'::regclass AND conname = 'customer_operation_workflow_guest_claims_workflow_custody_fkey') THEN
  ALTER TABLE customer_operation_workflow_guest_claims
   ADD CONSTRAINT customer_operation_workflow_guest_claims_workflow_custody_fkey
   FOREIGN KEY(operation_id,generation,workflow_run_id,execution_kind)
   REFERENCES customer_operation_executions(operation_id,generation,workflow_run_id,execution_kind) ON DELETE CASCADE NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE customer_operation_workflow_guest_claims VALIDATE CONSTRAINT customer_operation_workflow_guest_claims_workflow_custody_fkey;
ALTER TABLE customer_operation_workflow_guest_claims DROP CONSTRAINT IF EXISTS customer_operation_workflow_guest_claims_execution_identity_fkey;

-- +goose Down
-- Forward-only: retain consumed native bindings and durable replay fences.
SELECT 1;
