-- filename: 20260919200000001_repair_legacy_job_artifacts.sql
-- +goose Up
-- +goose StatementBegin

-- Before OCI image materialization, jobs.image_ref was passed directly to
-- vmmd as a platform StorageBackend key. The materialization migration
-- defaulted every existing row to pending, so those legacy apps/...ext4 keys
-- were subsequently parsed as OCI references and their queued runs could
-- never dispatch. Preserve the already-built artifact as the runtime key.
--
-- Keep image_resolved_digest NULL: these rows predate OCI resolution and
-- inventing a manifest digest would make the customer-facing metadata lie.
UPDATE jobs
   SET image_storage_key = image_ref,
       image_resolved_digest = NULL,
       image_materialization_status = 'ready',
       image_materialization_error = NULL,
       image_materialized_at = now(),
       image_materialization_attempts = 0,
       image_materialization_next_attempt_at = NULL,
       image_materialization_lease_owner = NULL,
       image_materialization_lease_until = NULL,
       updated_at = now()
 WHERE status <> 'deleted'
   AND image_materialization_status IN ('pending', 'failed')
   AND image_storage_key IS NULL
   AND image_ref ~ '^apps/[a-z0-9]([a-z0-9-]{1,38})[a-z0-9]/[A-Za-z0-9._-]+\.ext4$'
   AND image_ref !~ '(^|/)\.\.?(/|$)';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Data repair is intentionally irreversible: returning an already-built
-- artifact to the OCI queue would strand the job again.
SELECT 1;
-- +goose StatementEnd
