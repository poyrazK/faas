-- filename: 20260924040027291_verify_legacy_job_artifacts.sql
-- +goose Up
-- +goose StatementBegin

-- The 20260919200000001 repair recognized old apps/...ext4 job references,
-- but SQL could not check the artifact store and marked every matching row
-- ready. Fence those rows before new imaged workers start: old workers only
-- claim status=pending, and schedulers only dispatch status=ready.
ALTER TABLE jobs DROP CONSTRAINT IF EXISTS jobs_image_materialization_status_check;
ALTER TABLE jobs ADD CONSTRAINT jobs_image_materialization_status_check
    CHECK (image_materialization_status IN ('pending', 'verifying_legacy', 'ready', 'failed'));

UPDATE jobs
   SET image_materialization_status = 'verifying_legacy',
       image_materialization_error = NULL,
       image_materialized_at = NULL,
       image_materialization_attempts = 0,
       image_materialization_next_attempt_at = NULL,
       image_materialization_lease_owner = NULL,
       image_materialization_lease_until = NULL,
       updated_at = now()
 WHERE status <> 'deleted'
   AND image_materialization_status = 'ready'
   AND image_storage_key = image_ref
   AND image_resolved_digest IS NULL
   AND image_ref ~ '^apps/[a-z0-9]([a-z0-9-]{1,38})[a-z0-9]/[A-Za-z0-9._-]+\.ext4$'
   AND image_ref !~ '(^|/)\.\.?(/|$)';

CREATE INDEX IF NOT EXISTS jobs_legacy_artifact_verification_idx
    ON jobs (updated_at, id)
    WHERE status <> 'deleted' AND image_materialization_status = 'verifying_legacy';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Never restore an unverified artifact to ready on rollback.
UPDATE jobs
   SET image_materialization_status = 'failed',
       image_storage_key = NULL,
       image_materialization_error = 'legacy artifact verification was interrupted by rollback',
       image_materialization_next_attempt_at = NULL,
       image_materialization_lease_owner = NULL,
       image_materialization_lease_until = NULL,
       updated_at = now()
 WHERE image_materialization_status = 'verifying_legacy';

DROP INDEX IF EXISTS jobs_legacy_artifact_verification_idx;
ALTER TABLE jobs DROP CONSTRAINT IF EXISTS jobs_image_materialization_status_check;
ALTER TABLE jobs ADD CONSTRAINT jobs_image_materialization_status_check
    CHECK (image_materialization_status IN ('pending', 'ready', 'failed'));
-- +goose StatementEnd
