-- filename: 20261001063124137_operation_workflow_guest_claims.sql

-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS customer_operation_workflow_guest_claims (
    workflow_run_id uuid NOT NULL,
    step_name text NOT NULL,
    step_attempt integer NOT NULL CHECK (step_attempt>0),
    operation_id uuid NOT NULL,
    generation integer NOT NULL CHECK (generation>0),
    execution_kind text NOT NULL DEFAULT 'workflow' CHECK (execution_kind='workflow'),
    coordinator_attempt integer NOT NULL CHECK (coordinator_attempt>0),
    instance_id uuid REFERENCES instances(id) ON DELETE SET NULL,
    capability_digest text NOT NULL CHECK (capability_digest ~ '^[0-9a-f]{64}$'),
    deadline_at timestamptz NOT NULL CHECK (isfinite(deadline_at)),
    bound_at timestamptz NOT NULL DEFAULT now() CHECK (isfinite(bound_at)),
    PRIMARY KEY(workflow_run_id,step_name,step_attempt),
    FOREIGN KEY(workflow_run_id,step_name,step_attempt)
        REFERENCES workflow_step_attempts(run_id,step_name,attempt) ON DELETE CASCADE,
    FOREIGN KEY(operation_id,generation,workflow_run_id,execution_kind)
        REFERENCES customer_operation_executions(operation_id,generation,execution_id,execution_kind) ON DELETE CASCADE
);
-- Preserve a consumed binding when an instance is deleted: deletion must not
-- make an unresolved native attempt eligible for another guest dispatch.
CREATE INDEX IF NOT EXISTS customer_operation_workflow_guest_instance_idx
    ON customer_operation_workflow_guest_claims(instance_id) WHERE instance_id IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS customer_operation_workflow_guest_claims;
-- +goose StatementEnd
