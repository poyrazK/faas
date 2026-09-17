-- filename: 20260917192441691_repair_usage_minutes_job_identity.sql

-- +goose Up
-- +goose StatementBegin

-- Production can retain the applied ledger row for legacy migration 00257
-- while missing its usage_minutes widening after a schema restore. Reapply
-- that additive contract under a fresh timestamp version. Every operation is
-- replay-safe so databases whose schema is already correct remain unchanged.
ALTER TABLE usage_minutes
    ALTER COLUMN app_id DROP NOT NULL;

ALTER TABLE usage_minutes
    ADD COLUMN IF NOT EXISTS meter_kind text NOT NULL DEFAULT 'app',
    ADD COLUMN IF NOT EXISTS job_id uuid;

ALTER TABLE usage_minutes
    DROP CONSTRAINT IF EXISTS usage_minutes_meter_kind_check,
    DROP CONSTRAINT IF EXISTS usage_minutes_app_or_job_chk;

ALTER TABLE usage_minutes
    ADD CONSTRAINT usage_minutes_meter_kind_check
        CHECK (meter_kind IN ('app', 'job')),
    ADD CONSTRAINT usage_minutes_app_or_job_chk
        CHECK (
            (meter_kind = 'app' AND app_id IS NOT NULL AND job_id IS NULL)
         OR (meter_kind = 'job' AND app_id IS NULL AND job_id IS NOT NULL)
        );

CREATE INDEX IF NOT EXISTS usage_minutes_job_idx
    ON usage_minutes (account_id, minute DESC)
    WHERE meter_kind = 'job';

COMMENT ON COLUMN usage_minutes.meter_kind IS
    'Meter discriminator restored by migration 20260917192441691: app or job.';

COMMENT ON COLUMN usage_minutes.job_id IS
    'jobs.id for meter_kind=job rows; NULL for app rows. The job may be deleted after metering.';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- Forward-only: narrowing app_id or removing job identity would invalidate
-- legitimate job metering rows after this repair reaches production.
SELECT 1;
-- +goose StatementEnd
