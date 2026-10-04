-- +goose Up
ALTER TABLE object_upload_completions
 ADD COLUMN recovery_cursor text NOT NULL DEFAULT ''
  CHECK (octet_length(recovery_cursor) <= 8192),
 ADD COLUMN recovery_versions_observed boolean NOT NULL DEFAULT false;

-- A retained version is durable evidence that current-object inventory cannot
-- safely reclaim capacity. This latch survives completion and worker restarts.
CREATE INDEX object_upload_version_history_bucket_idx
 ON object_upload_completions(bucket_id) WHERE recovery_versions_observed;

-- +goose Down
DROP INDEX object_upload_version_history_bucket_idx;
ALTER TABLE object_upload_completions
 DROP COLUMN recovery_versions_observed,
 DROP COLUMN recovery_cursor;
