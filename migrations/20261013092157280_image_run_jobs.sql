-- filename: 20261013092157280_image_run_jobs.sql
-- ADR-1161: image runs execute one-task Job runs against a platform-managed
-- runner job per account and image. Runner jobs use kind 'image_run', are
-- never scheduled, and record the image's own ENTRYPOINT and CMD so apid can
-- resolve Docker-style commands.
-- +goose Up
-- +goose StatementBegin
ALTER TABLE jobs DROP CONSTRAINT IF EXISTS jobs_kind_check;
ALTER TABLE jobs
    ADD CONSTRAINT jobs_kind_check CHECK (kind IN ('batch', 'recurring', 'image_run'));

ALTER TABLE jobs DROP CONSTRAINT IF EXISTS jobs_schedule_kind_check;
ALTER TABLE jobs
    ADD CONSTRAINT jobs_schedule_kind_check CHECK (
        (kind IN ('batch', 'image_run') AND cron_schedule IS NULL)
        OR
        (kind = 'recurring' AND cron_schedule IS NOT NULL AND btrim(cron_schedule) <> '')
    );

-- NULL until imaged publishes a ready artifact; NULL on a ready row means the
-- artifact predates this migration and apid re-queues it before use.
ALTER TABLE jobs
    ADD COLUMN IF NOT EXISTS image_entrypoint text[],
    ADD COLUMN IF NOT EXISTS image_cmd text[];

ALTER TABLE jobs
    DROP CONSTRAINT IF EXISTS jobs_image_entrypoint_chk,
    DROP CONSTRAINT IF EXISTS jobs_image_cmd_chk;
ALTER TABLE jobs
    ADD CONSTRAINT jobs_image_entrypoint_chk
    CHECK (image_entrypoint IS NULL OR cardinality(image_entrypoint) <= 64),
    ADD CONSTRAINT jobs_image_cmd_chk
    CHECK (image_cmd IS NULL OR cardinality(image_cmd) <= 64);

-- Eviction picks the least recently used live runner job per account.
CREATE INDEX IF NOT EXISTS jobs_image_run_lru_idx
    ON jobs (account_id, updated_at)
    WHERE kind = 'image_run' AND status <> 'deleted';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
UPDATE jobs SET status = 'deleted', kind = 'batch' WHERE kind = 'image_run';
DROP INDEX IF EXISTS jobs_image_run_lru_idx;
ALTER TABLE jobs DROP CONSTRAINT IF EXISTS jobs_image_cmd_chk;
ALTER TABLE jobs DROP CONSTRAINT IF EXISTS jobs_image_entrypoint_chk;
ALTER TABLE jobs DROP COLUMN IF EXISTS image_cmd;
ALTER TABLE jobs DROP COLUMN IF EXISTS image_entrypoint;
ALTER TABLE jobs DROP CONSTRAINT IF EXISTS jobs_schedule_kind_check;
ALTER TABLE jobs
    ADD CONSTRAINT jobs_schedule_kind_check CHECK (
        (kind = 'batch' AND cron_schedule IS NULL)
        OR
        (kind = 'recurring' AND cron_schedule IS NOT NULL AND btrim(cron_schedule) <> '')
    );
ALTER TABLE jobs DROP CONSTRAINT IF EXISTS jobs_kind_check;
ALTER TABLE jobs
    ADD CONSTRAINT jobs_kind_check CHECK (kind IN ('batch', 'recurring'));
-- +goose StatementEnd
