-- filename: 20260917131722793_usage_daily_job_meter_kind.sql

-- +goose Up
-- +goose StatementBegin

-- Jobs use the widened usage_minutes identity from migration 00257. Keep
-- usage_daily's existing app_id-based primary key for compatibility with
-- the v1 read API, but carry an explicit discriminator and the denormalised
-- job identity. The rollup writes app_id=job_id for job rows so the existing
-- primary key remains valid; meter_kind is the authoritative distinction.
ALTER TABLE usage_daily
    ADD COLUMN IF NOT EXISTS meter_kind text NOT NULL DEFAULT 'app',
    ADD COLUMN IF NOT EXISTS job_id uuid;

ALTER TABLE usage_daily
    DROP CONSTRAINT IF EXISTS usage_daily_meter_kind_check;

ALTER TABLE usage_daily
    ADD CONSTRAINT usage_daily_meter_kind_check
    CHECK (meter_kind IN ('app', 'job'));

ALTER TABLE usage_daily
    DROP CONSTRAINT IF EXISTS usage_daily_job_identity_check;

ALTER TABLE usage_daily
    ADD CONSTRAINT usage_daily_job_identity_check
    CHECK (
        (meter_kind = 'app' AND job_id IS NULL)
     OR (meter_kind = 'job' AND job_id IS NOT NULL)
    );

CREATE INDEX IF NOT EXISTS usage_daily_job_idx
    ON usage_daily (account_id, day DESC)
    WHERE meter_kind = 'job';

COMMENT ON COLUMN usage_daily.meter_kind IS
    'Meter discriminator: app or job. Job rows retain app_id=job_id for the legacy PK; see migration 20260917131722793.';

COMMENT ON COLUMN usage_daily.job_id IS
    'jobs.id for meter_kind=job rows; NULL for app rows. The job may be deleted after metering.';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS usage_daily_job_idx;
ALTER TABLE usage_daily
    DROP CONSTRAINT IF EXISTS usage_daily_job_identity_check,
    DROP CONSTRAINT IF EXISTS usage_daily_meter_kind_check,
    DROP COLUMN IF EXISTS job_id,
    DROP COLUMN IF EXISTS meter_kind;

-- +goose StatementEnd
