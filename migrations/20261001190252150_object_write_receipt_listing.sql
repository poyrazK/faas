-- filename: 20261001190252150_object_write_receipt_listing.sql

-- +goose Up
-- +goose StatementBegin
-- Read-only bucket diagnostics, including pending writes after a lost response.
CREATE INDEX object_upload_completions_bucket_receipts_idx
 ON object_upload_completions(bucket_id, created_at DESC, id DESC)
 WHERE write_phase <> 'untracked';
CREATE INDEX object_upload_completions_bucket_receipt_status_idx
 ON object_upload_completions(bucket_id, status, created_at DESC, id DESC)
 WHERE write_phase <> 'untracked';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX object_upload_completions_bucket_receipt_status_idx;
DROP INDEX object_upload_completions_bucket_receipts_idx;
-- +goose StatementEnd
