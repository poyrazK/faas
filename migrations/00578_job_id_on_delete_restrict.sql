-- +goose Up
-- +goose StatementBegin
-- The 00256 instances_kind_job migration set instances.job_id FK to
-- ON DELETE SET NULL — but the pair check requires kind='job_task'
-- AND job_id IS NOT NULL. Hard-deleting a job would violate the
-- check on its surviving instance rows.
--
-- Resolution: ON DELETE RESTRICT (the soft-delete path via
-- jobs.status='deleted' + the live-instances check from 00530 is
-- the customer-facing delete). Hard delete via the FK is now blocked.

-- Production repair for the same burned-slot history as 00571. The
-- 00256 instances_kind_job DDL was also replaced by an already-applied
-- reservation fence, so production can reach this migration with neither
-- `kind` nor `job_id`. Restore that base shape before changing the FK.
-- These statements are replay-safe and no-op where 00256 was applied.

ALTER TABLE instances
    ADD COLUMN IF NOT EXISTS kind text NOT NULL DEFAULT 'wake';

ALTER TABLE instances
    DROP CONSTRAINT IF EXISTS instances_kind_check;

ALTER TABLE instances
    ADD CONSTRAINT instances_kind_check
    CHECK (kind IN ('wake','build','job_task'));

ALTER TABLE instances
    ALTER COLUMN app_id DROP NOT NULL;

ALTER TABLE instances
    ADD COLUMN IF NOT EXISTS job_id uuid;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
         WHERE conname = 'instances_job_id_fk'
           AND conrelid = 'instances'::regclass
    ) THEN
        ALTER TABLE instances
            ADD CONSTRAINT instances_job_id_fk
            FOREIGN KEY (job_id) REFERENCES jobs(id)
            ON DELETE SET NULL;
    END IF;
END $$;

ALTER TABLE instances
    DROP CONSTRAINT IF EXISTS instances_app_or_job_chk;

ALTER TABLE instances
    ADD CONSTRAINT instances_app_or_job_chk
    CHECK (
        (kind IN ('wake','build') AND app_id IS NOT NULL AND job_id IS NULL)
     OR (kind = 'job_task' AND app_id IS NULL AND job_id IS NOT NULL)
    );

CREATE INDEX IF NOT EXISTS instances_job_id_idx
    ON instances (job_id)
    WHERE job_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS instances_kind_job_task_idx
    ON instances (job_id)
    WHERE kind = 'job_task';

DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'instances_job_id_fk'
    ) THEN
        ALTER TABLE instances DROP CONSTRAINT instances_job_id_fk;
    END IF;
END $$;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'instances_job_id_fk'
    ) THEN
        ALTER TABLE instances
            ADD CONSTRAINT instances_job_id_fk
            FOREIGN KEY (job_id) REFERENCES jobs(id) ON DELETE RESTRICT;
    END IF;
END $$;

CREATE INDEX IF NOT EXISTS instances_job_active_idx
    ON instances (job_id)
    WHERE kind = 'job_task'
      AND state NOT IN ('parked', 'destroyed');
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd
