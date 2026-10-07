-- +goose Up
-- +goose StatementBegin
-- ADR-602: a Job Operation owns one immutable, single-task run per generation.
ALTER TABLE customer_operations ADD COLUMN job_run_id uuid UNIQUE REFERENCES job_runs(id) ON DELETE RESTRICT;
ALTER TABLE customer_operations DROP CONSTRAINT customer_operations_execution_family_check;
ALTER TABLE customer_operations ADD CONSTRAINT customer_operations_execution_family_check
 CHECK (num_nonnulls(current_invocation_id, workflow_run_id, job_run_id) = 1);
ALTER TABLE customer_operations ADD CONSTRAINT customer_operations_job_record_check
 CHECK (job_run_id IS NULL OR (record->>'job_run_id') IS NOT DISTINCT FROM job_run_id::text);
CREATE TABLE customer_operation_job_executions (
 operation_id uuid NOT NULL REFERENCES customer_operations(id) ON DELETE CASCADE,
 generation integer NOT NULL CHECK (generation > 0),
 run_id uuid NOT NULL UNIQUE REFERENCES job_runs(id) ON DELETE RESTRICT,
 record jsonb NOT NULL CHECK (jsonb_typeof(record) = 'object'),
 CHECK ((record->>'job_run_id') IS NOT DISTINCT FROM run_id::text),
 CHECK ((record->>'generation') IS NOT DISTINCT FROM generation::text),
 created_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(operation_id, generation)
);
ALTER TABLE customer_operation_events DROP CONSTRAINT customer_operation_events_event_type_check;
ALTER TABLE customer_operation_events ADD CONSTRAINT customer_operation_events_event_type_check
 CHECK(event_type IN ('accepted','running','progress','result_prepared','artifact_prepared','artifact_attached','succeeded','failed','cancellation_requested','cancelled','reconciliation_required','recovery_requested','delivery_changed','result_expired'));
-- +goose StatementEnd

-- +goose Down
SELECT 1;
