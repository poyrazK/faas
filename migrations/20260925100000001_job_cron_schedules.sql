-- +goose Up
-- +goose StatementBegin

ALTER TABLE jobs
    ADD COLUMN IF NOT EXISTS cron_schedule text,
    ADD COLUMN IF NOT EXISTS cron_timezone text NOT NULL DEFAULT 'UTC',
    ADD COLUMN IF NOT EXISTS last_scheduled_at timestamptz;

-- Older builds accepted kind='recurring' as a label without providing a
-- schedule. Normalize those inert rows before pinning the real contract.
UPDATE jobs
   SET kind = 'batch'
 WHERE kind = 'recurring'
   AND cron_schedule IS NULL;

ALTER TABLE jobs
    DROP CONSTRAINT IF EXISTS jobs_schedule_kind_check;
ALTER TABLE jobs
    ADD CONSTRAINT jobs_schedule_kind_check CHECK (
        (kind = 'batch' AND cron_schedule IS NULL)
        OR
        (kind = 'recurring' AND cron_schedule IS NOT NULL AND btrim(cron_schedule) <> '')
    );

CREATE INDEX IF NOT EXISTS jobs_cron_schedule_idx
    ON jobs (last_scheduled_at, created_at)
    WHERE status = 'active' AND kind = 'recurring' AND cron_schedule IS NOT NULL;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

UPDATE jobs SET kind = 'batch' WHERE kind = 'recurring';
DROP INDEX IF EXISTS jobs_cron_schedule_idx;
ALTER TABLE jobs DROP CONSTRAINT IF EXISTS jobs_schedule_kind_check;
ALTER TABLE jobs
    DROP COLUMN IF EXISTS last_scheduled_at,
    DROP COLUMN IF EXISTS cron_timezone,
    DROP COLUMN IF EXISTS cron_schedule;

-- +goose StatementEnd
