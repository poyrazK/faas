-- filename: 20261007200000001_operation_workflow_guest_replay_identity.sql

-- +goose Up
-- +goose StatementBegin
-- ADR-521. Keep native guest custody independent of the replayed current
-- execution index, as the coordinator custody ledger already is. The backend
-- exactly-one identity check and both workflow-kind checks retain the same
-- operation, generation, workflow-run and execution-kind authority.
CREATE UNIQUE INDEX IF NOT EXISTS customer_operation_executions_workflow_custody_idx
 ON customer_operation_executions(operation_id,generation,workflow_run_id,execution_kind);
ALTER TABLE customer_operation_workflow_guest_claims
 DROP CONSTRAINT IF EXISTS customer_operation_workflow_guest_claims_execution_identity_fkey;
ALTER TABLE customer_operation_workflow_guest_claims
 ADD CONSTRAINT customer_operation_workflow_guest_claims_execution_identity_fkey
 FOREIGN KEY(operation_id,generation,workflow_run_id,execution_kind)
 REFERENCES customer_operation_executions(operation_id,generation,workflow_run_id,execution_kind) ON DELETE CASCADE;
-- +goose StatementEnd

-- +goose Down
-- Forward-only: retain consumed native bindings and durable replay fences.
SELECT 1;
