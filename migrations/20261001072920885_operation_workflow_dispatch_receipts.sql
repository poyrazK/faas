-- filename: 20261001072920885_operation_workflow_dispatch_receipts.sql

-- +goose Up
-- +goose StatementBegin
ALTER TABLE customer_operation_workflow_guest_claims
    ADD COLUMN IF NOT EXISTS dispatch_started_at timestamptz
    CHECK (dispatch_started_at IS NULL OR isfinite(dispatch_started_at));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE customer_operation_workflow_guest_claims
    DROP COLUMN IF EXISTS dispatch_started_at;
-- +goose StatementEnd
