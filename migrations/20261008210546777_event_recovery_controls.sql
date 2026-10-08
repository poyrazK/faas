-- +goose Up
-- +goose StatementBegin
DO $$ DECLARE constraint_row record; BEGIN
 FOR constraint_row IN
  SELECT c.conname FROM pg_constraint c WHERE c.conrelid='event_recovery_jobs'::regclass AND c.contype='c'
   AND c.conkey && ARRAY(SELECT attnum FROM pg_attribute WHERE attrelid=c.conrelid AND attname IN ('state','window_count','completed_at'))
 LOOP EXECUTE format('ALTER TABLE event_recovery_jobs DROP CONSTRAINT IF EXISTS %I',constraint_row.conname); END LOOP;
END $$;
ALTER TABLE event_recovery_jobs
    ADD COLUMN IF NOT EXISTS paused_at timestamptz;
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='event_recovery_jobs'::regclass AND conname='event_recovery_jobs_lifecycle_chk') THEN
        ALTER TABLE event_recovery_jobs ADD CONSTRAINT event_recovery_jobs_lifecycle_chk CHECK (state IN ('running','paused','completed','cancelled'));
    END IF;
END $$;
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='event_recovery_jobs'::regclass AND conname='event_recovery_jobs_completion_chk') THEN
        ALTER TABLE event_recovery_jobs ADD CONSTRAINT event_recovery_jobs_completion_chk CHECK ((state IN ('running','paused')) = (completed_at IS NULL));
    END IF;
END $$;
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='event_recovery_jobs'::regclass AND conname='event_recovery_jobs_pause_chk') THEN
        ALTER TABLE event_recovery_jobs ADD CONSTRAINT event_recovery_jobs_pause_chk CHECK ((state='paused') = (paused_at IS NOT NULL));
    END IF;
END $$;
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='event_recovery_jobs'::regclass AND conname='event_recovery_jobs_window_budget_chk') THEN
        ALTER TABLE event_recovery_jobs ADD CONSTRAINT event_recovery_jobs_window_budget_chk CHECK (window_count>=0 AND window_count<=100);
    END IF;
END $$;
CREATE INDEX IF NOT EXISTS event_recovery_jobs_expiry_idx ON event_recovery_jobs(expires_at,id) WHERE state IN ('running','paused');
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM event_recovery_jobs WHERE state='paused') THEN RAISE EXCEPTION 'resume or cancel paused recovery jobs before rollback'; END IF;
 IF EXISTS (SELECT 1 FROM event_recovery_jobs WHERE window_count>rate_per_second AND window_started_at+interval '1 second'>clock_timestamp()) THEN RAISE EXCEPTION 'wait for the recovery pacing window to end before rollback'; END IF;
END $$;
UPDATE event_recovery_jobs SET window_count=0 WHERE window_count>rate_per_second AND window_started_at+interval '1 second'<=clock_timestamp();
DROP INDEX event_recovery_jobs_expiry_idx;
ALTER TABLE event_recovery_jobs DROP CONSTRAINT event_recovery_jobs_lifecycle_chk,
 DROP CONSTRAINT event_recovery_jobs_completion_chk, DROP CONSTRAINT event_recovery_jobs_pause_chk,
 DROP CONSTRAINT event_recovery_jobs_window_budget_chk, DROP COLUMN paused_at,
 ADD CONSTRAINT event_recovery_jobs_state_check CHECK (state IN ('running','completed','cancelled')),
 ADD CONSTRAINT event_recovery_jobs_check CHECK ((state='running') = (completed_at IS NULL)),
 ADD CONSTRAINT event_recovery_jobs_window_count_check CHECK (window_count>=0 AND window_count<=rate_per_second);
-- +goose StatementEnd
