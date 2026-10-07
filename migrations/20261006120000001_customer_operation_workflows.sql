-- +goose Up
-- +goose StatementBegin
-- ADR-595. An operation owns exactly one execution family. The workflow ledger
-- remains authoritative and cannot be pruned while its operation retains it.
ALTER TABLE customer_operations ALTER COLUMN current_invocation_id DROP NOT NULL;
ALTER TABLE customer_operations ADD COLUMN workflow_run_id uuid UNIQUE
    REFERENCES workflow_runs(id) ON DELETE RESTRICT;
ALTER TABLE customer_operations ADD CONSTRAINT customer_operations_execution_family_check
    CHECK (num_nonnulls(current_invocation_id, workflow_run_id) = 1);
ALTER TABLE customer_operations ADD CONSTRAINT customer_operations_workflow_record_check
    CHECK (workflow_run_id IS NULL OR (record->>'workflow_run_id') IS NOT DISTINCT FROM workflow_run_id::text);
CREATE TABLE customer_operation_workflow_executions (
    operation_id uuid NOT NULL REFERENCES customer_operations(id) ON DELETE CASCADE,
    generation integer NOT NULL CHECK (generation > 0),
    run_id uuid NOT NULL REFERENCES workflow_runs(id) ON DELETE RESTRICT,
    resume_count integer NOT NULL CHECK (resume_count >= 0 AND generation = resume_count + 1),
    record jsonb NOT NULL CHECK (jsonb_typeof(record) = 'object'),
    CHECK ((record->>'workflow_run_id') IS NOT DISTINCT FROM run_id::text),
    CHECK ((record->>'generation') IS NOT DISTINCT FROM generation::text),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (operation_id, generation),
    UNIQUE (run_id, resume_count)
);
-- +goose StatementEnd

-- +goose Down
-- Forward-only: rollback must preserve admitted work and recovery receipts.
SELECT 1;
