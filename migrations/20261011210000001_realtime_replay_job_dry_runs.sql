-- +goose Up
ALTER TABLE managed_realtime_replay_jobs
 ADD COLUMN dry_run boolean NOT NULL DEFAULT false,
 ADD COLUMN reviewed_job_id uuid,
 ADD COLUMN started_job_id uuid;
ALTER TABLE managed_realtime_replay_jobs DROP CONSTRAINT managed_realtime_replay_jobs_state_check;
ALTER TABLE managed_realtime_replay_jobs ADD CONSTRAINT managed_realtime_replay_jobs_state_check
 CHECK(state IN ('running','paused','checking','check_paused','completed','cancelled'));
ALTER TABLE managed_realtime_replay_jobs ADD CONSTRAINT realtime_replay_job_mode_state_check
 CHECK((dry_run AND state IN ('checking','check_paused','completed','cancelled')) OR
 (NOT dry_run AND state IN ('running','paused','completed','cancelled')));

-- IDs link bounded history without retaining reports or cascading deletion of live jobs.
DROP TRIGGER realtime_replay_job_notification ON managed_realtime_replay_jobs;
CREATE TRIGGER realtime_replay_job_notification AFTER UPDATE ON managed_realtime_replay_jobs
 FOR EACH ROW WHEN (NOT NEW.dry_run) EXECUTE FUNCTION enqueue_realtime_replay_job_notification();
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
