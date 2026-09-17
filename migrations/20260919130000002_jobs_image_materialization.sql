-- filename: 20260919130000002_jobs_image_materialization.sql
-- +goose Up
-- +goose StatementBegin

-- Jobs retain the customer-facing OCI reference, but vmmd must boot a
-- materialized ext4 artifact. Keep the two identities separate so a mutable
-- tag can never be mistaken for the immutable storage object.
ALTER TABLE jobs
    ADD COLUMN IF NOT EXISTS image_resolved_digest text NULL,
    ADD COLUMN IF NOT EXISTS image_storage_key text NULL,
    ADD COLUMN IF NOT EXISTS image_materialization_status text NOT NULL DEFAULT 'pending',
    ADD COLUMN IF NOT EXISTS image_materialization_error text NULL,
    ADD COLUMN IF NOT EXISTS image_materialized_at timestamptz NULL;

-- The status is deliberately a text CHECK rather than a PostgreSQL enum so
-- future worker states can be widened with the same replay-safe pattern.
ALTER TABLE jobs
    DROP CONSTRAINT IF EXISTS jobs_image_materialization_status_check;

ALTER TABLE jobs
    ADD CONSTRAINT jobs_image_materialization_status_check
    CHECK (image_materialization_status IN ('pending', 'ready', 'failed'));

-- Rows created before this migration have no artifact. Reset any partially
-- populated values to the fail-closed state; the worker will publish them
-- under the deterministic jobs/<job-id>.ext4 key.
UPDATE jobs
   SET image_materialization_status = 'pending',
       image_resolved_digest = NULL,
       image_storage_key = NULL,
       image_materialization_error = NULL,
       image_materialized_at = NULL
 WHERE image_materialization_status IS NULL
    OR image_materialization_status NOT IN ('pending', 'ready', 'failed');

CREATE INDEX IF NOT EXISTS jobs_image_materialization_pending_idx
    ON jobs (updated_at, id)
    WHERE status <> 'deleted'
      AND image_materialization_status = 'pending';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS jobs_image_materialization_pending_idx;
ALTER TABLE jobs DROP CONSTRAINT IF EXISTS jobs_image_materialization_status_check;
ALTER TABLE jobs
    DROP COLUMN IF EXISTS image_materialized_at,
    DROP COLUMN IF EXISTS image_materialization_error,
    DROP COLUMN IF EXISTS image_materialization_status,
    DROP COLUMN IF EXISTS image_storage_key,
    DROP COLUMN IF EXISTS image_resolved_digest;
-- +goose StatementEnd
