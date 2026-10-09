-- +goose Up
-- +goose StatementBegin
-- ADR-665: native Job file receipts survive owner/run deletion for cleanup.
ALTER TABLE customer_operation_result_blobs
    ADD COLUMN IF NOT EXISTS job_run_id uuid,
    DROP CONSTRAINT IF EXISTS operation_blob_execution_family;
ALTER TABLE customer_operation_result_blobs
    ADD CONSTRAINT operation_blob_execution_family CHECK (
        (execution_id IS NOT NULL AND workflow_run_id IS NULL AND workflow_step IS NULL AND job_run_id IS NULL)
        OR (execution_id IS NULL AND workflow_run_id IS NOT NULL AND workflow_step IS NOT NULL AND job_run_id IS NULL
            AND octet_length(workflow_step) BETWEEN 1 AND 128)
        OR (execution_id IS NULL AND workflow_run_id IS NULL AND workflow_step IS NULL AND job_run_id IS NOT NULL)
    );
-- +goose StatementEnd

-- +goose Down
-- Forward only: retained Job write intents must remain discoverable.
SELECT 1;
