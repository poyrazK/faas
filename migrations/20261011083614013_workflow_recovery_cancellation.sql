-- +goose Up
-- +goose StatementBegin
-- The complete feature marker survives later migrations. Reconciliation must
-- preserve retained evidence and must not restore an earlier, narrower constraint.
DO $replay_guard$
BEGIN
 IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='event_recovery_history'::regclass AND conname='event_recovery_history_target_check') THEN
  RETURN;
 END IF;
ALTER TABLE event_recovery_history DROP CONSTRAINT event_recovery_history_action_check;
ALTER TABLE event_recovery_history ADD CONSTRAINT event_recovery_history_action_check CHECK(action IN ('created','paused','resumed','rate_changed','cancelled','expired','workflow_cancelled'));
ALTER TABLE event_recovery_history ADD COLUMN target_position bigint, ADD COLUMN target_run_id uuid, ADD COLUMN target_resume_count integer;
ALTER TABLE event_recovery_history ADD CONSTRAINT event_recovery_history_target_check CHECK ((action='workflow_cancelled' AND target_position BETWEEN 1 AND 25 AND target_run_id IS NOT NULL AND target_resume_count BETWEEN 1 AND 16 AND target_position IS NOT NULL AND target_resume_count IS NOT NULL) OR (action<>'workflow_cancelled' AND target_position IS NULL AND target_run_id IS NULL AND target_resume_count IS NULL));
END
$replay_guard$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM event_recovery_history WHERE action='workflow_cancelled') THEN RAISE EXCEPTION 'cannot downgrade while workflow cancellation history is retained'; END IF;
END $$;
ALTER TABLE event_recovery_history DROP CONSTRAINT event_recovery_history_target_check;
ALTER TABLE event_recovery_history DROP COLUMN target_position, DROP COLUMN target_run_id, DROP COLUMN target_resume_count;
ALTER TABLE event_recovery_history DROP CONSTRAINT event_recovery_history_action_check;
ALTER TABLE event_recovery_history ADD CONSTRAINT event_recovery_history_action_check CHECK(action IN ('created','paused','resumed','rate_changed','cancelled','expired'));
-- +goose StatementEnd
