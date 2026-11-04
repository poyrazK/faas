-- filename: 20261007212424439_operation_workflow_claim_replay_identity.sql
-- migration-compat: contract
-- migration-lint: allow index-build customer_operation_executions: 0 rows / 48 kB on production, measured 2026-10-17 23:19 UTC
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';
-- Bind workflow custody directly to the execution ledger's workflow identity.
-- The existing exactly-one-backend check and generated execution_kind prove
-- that a non-null workflow_run_id is a workflow, while the claim's own kind
-- check remains workflow-only. Ownership and generation stay in the FK.
-- This separate key leaves the original current-execution index replayable
-- without rewriting its already-shipped migration or dropping custody.
CREATE UNIQUE INDEX IF NOT EXISTS customer_operation_executions_workflow_custody_idx
 ON customer_operation_executions(operation_id,generation,workflow_run_id,execution_kind);
-- The replacement key is added NOT VALID under a new name, validated without
-- blocking writes, and only then replaces the old key (ADR-202610171900).
-- Each statement commits on its own; every step is idempotent.
-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'customer_operation_workflow_claims'::regclass AND conname = 'customer_operation_workflow_claims_workflow_custody_fkey') THEN
  ALTER TABLE customer_operation_workflow_claims
   ADD CONSTRAINT customer_operation_workflow_claims_workflow_custody_fkey
   FOREIGN KEY(operation_id,generation,workflow_run_id,execution_kind)
   REFERENCES customer_operation_executions(operation_id,generation,workflow_run_id,execution_kind) ON DELETE CASCADE NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE customer_operation_workflow_claims VALIDATE CONSTRAINT customer_operation_workflow_claims_workflow_custody_fkey;
ALTER TABLE customer_operation_workflow_claims DROP CONSTRAINT IF EXISTS customer_operation_workflow_claims_execution_identity_fkey;

-- +goose Down
-- Forward-only: retain operation/workflow custody and replay fences.
SELECT 1;
