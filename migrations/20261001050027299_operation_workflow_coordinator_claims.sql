-- filename: 20261001050027299_operation_workflow_coordinator_claims.sql

-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS customer_operation_workflow_claims (
    workflow_run_id uuid PRIMARY KEY REFERENCES workflow_runs(id) ON DELETE CASCADE,
    operation_id uuid NOT NULL,
    generation integer NOT NULL CHECK (generation>0),
    execution_kind text NOT NULL DEFAULT 'workflow' CHECK (execution_kind='workflow'),
    attempt integer NOT NULL CHECK (attempt>0),
    capability_digest text NOT NULL CHECK (capability_digest ~ '^[0-9a-f]{64}$'),
    lease_until timestamptz NOT NULL CHECK (isfinite(lease_until)),
    FOREIGN KEY(operation_id,generation,workflow_run_id,execution_kind)
        REFERENCES customer_operation_executions(operation_id,generation,execution_id,execution_kind) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS customer_operation_workflow_claim_expiry_idx ON customer_operation_workflow_claims(lease_until,workflow_run_id);
CREATE INDEX IF NOT EXISTS workflow_runs_operation_due_idx ON workflow_runs(operation_id,status,scheduled_for) WHERE operation_id IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS customer_operation_workflow_claims;
DROP INDEX IF EXISTS workflow_runs_operation_due_idx;
-- +goose StatementEnd
