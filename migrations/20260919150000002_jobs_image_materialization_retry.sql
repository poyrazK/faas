-- filename: 20260919150000002_jobs_image_materialization_retry.sql
-- +goose Up
-- +goose StatementBegin

-- Materialization is a durable queue, not a best-effort notification. These
-- fields let multiple imaged nodes claim work without duplicate builds and
-- let a crashed worker's lease expire for safe recovery.
ALTER TABLE jobs
    ADD COLUMN IF NOT EXISTS image_materialization_attempts integer NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS image_materialization_next_attempt_at timestamptz NULL,
    ADD COLUMN IF NOT EXISTS image_materialization_lease_owner text NULL,
    ADD COLUMN IF NOT EXISTS image_materialization_lease_until timestamptz NULL;

ALTER TABLE jobs
    DROP CONSTRAINT IF EXISTS jobs_image_materialization_attempts_check;

ALTER TABLE jobs
    ADD CONSTRAINT jobs_image_materialization_attempts_check
    CHECK (image_materialization_attempts >= 0);

CREATE INDEX IF NOT EXISTS jobs_image_materialization_claim_idx
    ON jobs (updated_at, id)
    WHERE status <> 'deleted'
      AND image_materialization_status = 'pending';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS jobs_image_materialization_claim_idx;
ALTER TABLE jobs DROP CONSTRAINT IF EXISTS jobs_image_materialization_attempts_check;
ALTER TABLE jobs
    DROP COLUMN IF EXISTS image_materialization_lease_until,
    DROP COLUMN IF EXISTS image_materialization_lease_owner,
    DROP COLUMN IF EXISTS image_materialization_next_attempt_at,
    DROP COLUMN IF EXISTS image_materialization_attempts;
-- +goose StatementEnd
