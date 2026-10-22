-- +goose Up
ALTER TABLE managed_realtime_replay_jobs ADD COLUMN IF NOT EXISTS dry_run boolean NOT NULL DEFAULT false;
ALTER TABLE managed_realtime_replay_jobs ADD COLUMN IF NOT EXISTS reviewed_job_id uuid;
ALTER TABLE managed_realtime_replay_jobs ADD COLUMN IF NOT EXISTS started_job_id uuid;
-- +goose StatementBegin
DO $realtime_replay$
BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid=to_regclass('managed_realtime_replay_jobs') AND conname='managed_realtime_replay_jobs_state_check' AND position('''checking''' in pg_get_constraintdef(oid))>0) THEN
ALTER TABLE managed_realtime_replay_jobs DROP CONSTRAINT IF EXISTS managed_realtime_replay_jobs_state_check;
ALTER TABLE managed_realtime_replay_jobs ADD CONSTRAINT managed_realtime_replay_jobs_state_check
 CHECK(state IN ('running','paused','checking','check_paused','completed','cancelled'));
 END IF;
END $realtime_replay$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $realtime_replay$
BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid=to_regclass('managed_realtime_replay_jobs') AND conname='realtime_replay_job_mode_state_check') THEN
ALTER TABLE managed_realtime_replay_jobs ADD CONSTRAINT realtime_replay_job_mode_state_check
 CHECK((dry_run AND state IN ('checking','check_paused','completed','cancelled')) OR
 (NOT dry_run AND state IN ('running','paused','completed','cancelled')));
 END IF;
END $realtime_replay$;
-- +goose StatementEnd

-- IDs link bounded history without retaining reports or cascading deletion of live jobs.
DROP TRIGGER IF EXISTS realtime_replay_job_notification ON managed_realtime_replay_jobs;
-- +goose StatementBegin
DO $realtime_replay$
BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgrelid=to_regclass('managed_realtime_replay_jobs') AND tgname='realtime_replay_job_notification') THEN
CREATE TRIGGER realtime_replay_job_notification AFTER UPDATE ON managed_realtime_replay_jobs
 FOR EACH ROW WHEN (NOT NEW.dry_run) EXECUTE FUNCTION enqueue_realtime_replay_job_notification();
 END IF;
END $realtime_replay$;
-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM managed_realtime_replay_jobs WHERE dry_run AND state IN ('checking','check_paused')) THEN
 RAISE EXCEPTION 'Cancel active dry-run jobs before downgrade'; END IF;
END $$;
-- +goose StatementEnd
-- Terminal reports are discarded; real replays and their progress remain.
DELETE FROM managed_realtime_replay_jobs WHERE dry_run;
DROP TRIGGER realtime_replay_job_notification ON managed_realtime_replay_jobs;
CREATE TRIGGER realtime_replay_job_notification AFTER UPDATE ON managed_realtime_replay_jobs
 FOR EACH ROW EXECUTE FUNCTION enqueue_realtime_replay_job_notification();
ALTER TABLE managed_realtime_replay_jobs DROP CONSTRAINT realtime_replay_job_mode_state_check;
ALTER TABLE managed_realtime_replay_jobs DROP CONSTRAINT managed_realtime_replay_jobs_state_check;
ALTER TABLE managed_realtime_replay_jobs ADD CONSTRAINT managed_realtime_replay_jobs_state_check CHECK(state IN ('running','paused','completed','cancelled'));
ALTER TABLE managed_realtime_replay_jobs DROP COLUMN dry_run,DROP COLUMN reviewed_job_id,DROP COLUMN started_job_id;
