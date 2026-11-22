-- migration-compat: expand
-- ObjectUsageBuckets' inventory_held now asks whether a bucket's multipart
-- completions observed native versions, beside its upload completions and
-- version references, which already have matching (bucket_id) partial
-- indexes. Completed sessions fall outside the uncompleted-bucket index, so
-- without this one the usage snapshot, read under the account lock of every
-- object write, would scan every multipart upload on the platform. The
-- previous release never uses it.
--
-- The index is built concurrently (NO TRANSACTION), so
-- object_storage_multipart_uploads keeps taking writes. Re-runnable.
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';

CREATE INDEX CONCURRENTLY IF NOT EXISTS object_storage_multipart_completion_versions_idx
    ON object_storage_multipart_uploads (bucket_id) WHERE completion_versions_observed;

-- A failed concurrent build can leave an invalid index. Do not record success
-- when IF NOT EXISTS skips one; require explicit cleanup before retrying.
-- +goose StatementBegin
DO $index$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_index WHERE indexrelid = to_regclass('object_storage_multipart_completion_versions_idx')
                 AND indisvalid AND indisready) THEN
    RAISE EXCEPTION 'object_storage_multipart_completion_versions_idx is invalid: DROP INDEX CONCURRENTLY object_storage_multipart_completion_versions_idx and retry the migration';
  END IF;
END
$index$;
-- +goose StatementEnd

RESET lock_timeout;

-- +goose Down
SET lock_timeout = '5s';
DROP INDEX CONCURRENTLY IF EXISTS object_storage_multipart_completion_versions_idx;
RESET lock_timeout;
