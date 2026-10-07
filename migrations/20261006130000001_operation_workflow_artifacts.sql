-- +goose Up
-- +goose StatementBegin
-- ADR-596: both execution families retain write intents through owner deletion.
ALTER TABLE customer_operation_result_blobs
    ALTER COLUMN execution_id DROP NOT NULL,
    ADD COLUMN workflow_run_id uuid,
    ADD COLUMN workflow_step text,
    ADD CONSTRAINT operation_blob_execution_family CHECK (
        (execution_id IS NOT NULL AND workflow_run_id IS NULL AND workflow_step IS NULL)
        OR (execution_id IS NULL AND workflow_run_id IS NOT NULL AND workflow_step IS NOT NULL
            AND octet_length(workflow_step) BETWEEN 1 AND 128)
    );
ALTER TABLE customer_operation_events DROP CONSTRAINT customer_operation_events_event_type_check;
ALTER TABLE customer_operation_events ADD CONSTRAINT customer_operation_events_event_type_check
 CHECK(event_type IN ('accepted','running','progress','artifact_prepared','artifact_attached','succeeded','failed','cancellation_requested','cancelled','reconciliation_required','recovery_requested','delivery_changed','result_expired'));
-- +goose StatementEnd

-- +goose Down
-- Forward only: retained workflow write intents must remain discoverable.
SELECT 1;
