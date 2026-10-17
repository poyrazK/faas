-- filename: 20261007212424439_operation_workflow_claim_replay_identity.sql

-- +goose Up
SET LOCAL lock_timeout = '5s';
-- migration-lint: allow index-build customer_operation_executions: 0 rows / 48 kB on production, measured 2026-10-17 23:19 UTC
-- +goose StatementBegin
-- Bind workflow custody directly to the execution ledger's workflow identity.
-- The existing exactly-one-backend check and generated execution_kind prove
-- that a non-null workflow_run_id is a workflow, while the claim's own kind
-- check remains workflow-only. Ownership and generation stay in the FK.
-- This separate key leaves the original current-execution index replayable
-- without rewriting its already-shipped migration or dropping custody.
CREATE UNIQUE INDEX IF NOT EXISTS customer_operation_executions_workflow_custody_idx
 ON customer_operation_executions(operation_id,generation,workflow_run_id,execution_kind);
ALTER TABLE customer_operation_workflow_claims
 DROP CONSTRAINT IF EXISTS customer_operation_workflow_claims_execution_identity_fkey;
ALTER TABLE customer_operation_workflow_claims
 ADD CONSTRAINT customer_operation_workflow_claims_execution_identity_fkey
 FOREIGN KEY(operation_id,generation,workflow_run_id,execution_kind)
 REFERENCES customer_operation_executions(operation_id,generation,workflow_run_id,execution_kind) ON DELETE CASCADE;
-- +goose StatementEnd

-- +goose Down
-- Forward-only: retain operation/workflow custody and replay fences.
SELECT 1;
