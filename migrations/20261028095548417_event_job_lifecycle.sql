-- migration-compat: expand
-- filename: 20261028095548417_event_job_lifecycle.sql
--
-- ADR-20261028095548: a backfill can end as cancelled, expired or failed, a
-- failing recovery or backfill job backs off, and a running backfill holds
-- only the receipts its scan has not reached. Columns are added and checks
-- only widen, each replaced in its own transactions. The trigger keeps an
-- ended backfill ended whichever release updates it.

-- +goose NO TRANSACTION

-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
ALTER TABLE event_replay_jobs
    ADD COLUMN IF NOT EXISTS failure_count integer NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS next_attempt_at timestamptz,
    ADD COLUMN IF NOT EXISTS last_failure text NOT NULL DEFAULT '';
ALTER TABLE event_recovery_jobs
    ADD COLUMN IF NOT EXISTS failure_count integer NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS last_failure text NOT NULL DEFAULT '';
DO $$ BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'event_replay_jobs'::regclass AND conname = 'event_replay_jobs_failure_check') THEN
        ALTER TABLE event_replay_jobs ADD CONSTRAINT event_replay_jobs_failure_check
            CHECK (failure_count BETWEEN 0 AND 10 AND last_failure IN ('', 'conflict', 'invalid_job', 'timeout', 'internal')) NOT VALID;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'event_recovery_jobs'::regclass AND conname = 'event_recovery_jobs_failure_check') THEN
        ALTER TABLE event_recovery_jobs ADD CONSTRAINT event_recovery_jobs_failure_check
            CHECK (failure_count BETWEEN 0 AND 10 AND last_failure IN ('', 'conflict', 'invalid_job', 'timeout', 'internal')) NOT VALID;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'event_replay_jobs'::regclass AND conname = 'event_replay_jobs_lifecycle_check') THEN
        ALTER TABLE event_replay_jobs ADD CONSTRAINT event_replay_jobs_lifecycle_check
            CHECK (state IN ('running', 'completed', 'completed_with_failures', 'cancelled', 'expired', 'failed')) NOT VALID;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'event_replay_job_items'::regclass AND conname = 'event_replay_job_items_lifecycle_check') THEN
        ALTER TABLE event_replay_job_items ADD CONSTRAINT event_replay_job_items_lifecycle_check
            CHECK (state IN ('pending', 'processing', 'enqueued', 'filtered', 'failed', 'skipped_captured', 'skipped_unknown', 'skipped_existing', 'skipped_unsettled', 'cancelled')) NOT VALID;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
ALTER TABLE event_replay_jobs VALIDATE CONSTRAINT event_replay_jobs_failure_check;
ALTER TABLE event_recovery_jobs VALIDATE CONSTRAINT event_recovery_jobs_failure_check;
ALTER TABLE event_replay_jobs VALIDATE CONSTRAINT event_replay_jobs_lifecycle_check;
-- +goose StatementEnd

-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
ALTER TABLE event_replay_job_items VALIDATE CONSTRAINT event_replay_job_items_lifecycle_check;
-- +goose StatementEnd

-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
ALTER TABLE event_replay_jobs DROP CONSTRAINT IF EXISTS event_replay_jobs_state_check;
ALTER TABLE event_replay_job_items DROP CONSTRAINT IF EXISTS event_replay_job_items_state_check;
-- +goose StatementEnd

-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
-- An ended backfill stays ended: an older release's finalize step has no
-- state guard and would otherwise reopen one.
CREATE OR REPLACE FUNCTION event_replay_job_keep_ended() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RETURN NULL;
END $$;
DROP TRIGGER IF EXISTS event_replay_job_keep_ended ON event_replay_jobs;
CREATE TRIGGER event_replay_job_keep_ended BEFORE UPDATE ON event_replay_jobs
    FOR EACH ROW WHEN (OLD.state IN ('cancelled', 'expired', 'failed'))
    EXECUTE FUNCTION event_replay_job_keep_ended();
-- A running backfill holds only the receipts its scan has not reached.
CREATE OR REPLACE FUNCTION event_receipt_retention_hold(p_account uuid, p_outbox bigint, p_accepted timestamptz, p_job_cutoff timestamptz, p_now timestamptz)
RETURNS text LANGUAGE sql STABLE AS $fn$
 SELECT CASE
 WHEN EXISTS (SELECT 1 FROM event_replay_jobs j WHERE j.account_id=p_account AND j.state='running'
  AND p_accepted>=j.from_at AND p_accepted<j.cutoff_at
  AND (p_accepted,p_outbox)>(j.cursor_at,j.cursor_outbox_id)) THEN 'backfill_running'
 WHEN EXISTS (SELECT 1 FROM event_replay_jobs j WHERE j.account_id=p_account AND j.state='completed_with_failures'
  AND j.completed_at>=p_job_cutoff AND EXISTS (SELECT 1 FROM event_replay_job_items i
   WHERE i.job_id=j.id AND i.outbox_id=p_outbox AND i.state='failed' AND i.retryable)) THEN 'backfill_retryable'
 WHEN EXISTS (SELECT 1 FROM event_recovery_items i JOIN event_recovery_jobs j ON j.id=i.job_id
  WHERE i.outbox_id=p_outbox AND i.state='pending' AND j.account_id=p_account
   AND j.selection->'protect_receipts'='true'::jsonb AND j.state IN ('running','paused') AND j.expires_at>p_now) THEN 'recovery_pending'
 ELSE '' END;
$fn$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM event_replay_jobs WHERE state IN ('cancelled', 'expired', 'failed'))
       OR EXISTS (SELECT 1 FROM event_replay_job_items WHERE state = 'cancelled') THEN
        RAISE EXCEPTION 'ended event replay jobs must be pruned before rollback';
    END IF;
END $$;
CREATE OR REPLACE FUNCTION event_receipt_retention_hold(p_account uuid, p_outbox bigint, p_accepted timestamptz, p_job_cutoff timestamptz, p_now timestamptz)
RETURNS text LANGUAGE sql STABLE AS $fn$
 SELECT CASE
 WHEN EXISTS (SELECT 1 FROM event_replay_jobs j WHERE j.account_id=p_account AND j.state='running'
  AND p_accepted>=j.from_at AND p_accepted<j.cutoff_at) THEN 'backfill_running'
 WHEN EXISTS (SELECT 1 FROM event_replay_jobs j WHERE j.account_id=p_account AND j.state='completed_with_failures'
  AND j.completed_at>=p_job_cutoff AND EXISTS (SELECT 1 FROM event_replay_job_items i
   WHERE i.job_id=j.id AND i.outbox_id=p_outbox AND i.state='failed' AND i.retryable)) THEN 'backfill_retryable'
 WHEN EXISTS (SELECT 1 FROM event_recovery_items i JOIN event_recovery_jobs j ON j.id=i.job_id
  WHERE i.outbox_id=p_outbox AND i.state='pending' AND j.account_id=p_account
   AND j.selection->'protect_receipts'='true'::jsonb AND j.state IN ('running','paused') AND j.expires_at>p_now) THEN 'recovery_pending'
 ELSE '' END;
$fn$;
DROP TRIGGER IF EXISTS event_replay_job_keep_ended ON event_replay_jobs;
DROP FUNCTION IF EXISTS event_replay_job_keep_ended();
DO $$ BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'event_replay_jobs'::regclass AND conname = 'event_replay_jobs_state_check') THEN
        ALTER TABLE event_replay_jobs ADD CONSTRAINT event_replay_jobs_state_check
            CHECK (state IN ('running', 'completed', 'completed_with_failures')) NOT VALID;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'event_replay_job_items'::regclass AND conname = 'event_replay_job_items_state_check') THEN
        ALTER TABLE event_replay_job_items ADD CONSTRAINT event_replay_job_items_state_check
            CHECK (state IN ('pending', 'processing', 'enqueued', 'filtered', 'failed', 'skipped_captured', 'skipped_unknown', 'skipped_existing', 'skipped_unsettled')) NOT VALID;
    END IF;
END $$;
ALTER TABLE event_replay_job_items DROP CONSTRAINT IF EXISTS event_replay_job_items_lifecycle_check;
ALTER TABLE event_replay_jobs DROP CONSTRAINT IF EXISTS event_replay_jobs_lifecycle_check;
ALTER TABLE event_recovery_jobs DROP CONSTRAINT IF EXISTS event_recovery_jobs_failure_check;
ALTER TABLE event_replay_jobs DROP CONSTRAINT IF EXISTS event_replay_jobs_failure_check;
ALTER TABLE event_recovery_jobs DROP COLUMN IF EXISTS last_failure, DROP COLUMN IF EXISTS failure_count;
ALTER TABLE event_replay_jobs DROP COLUMN IF EXISTS last_failure, DROP COLUMN IF EXISTS next_attempt_at, DROP COLUMN IF EXISTS failure_count;
-- +goose StatementEnd
