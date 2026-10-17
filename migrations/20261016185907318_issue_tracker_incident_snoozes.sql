-- +goose Up
ALTER TABLE issue_tracker_sync_triage ADD COLUMN IF NOT EXISTS snoozed_until timestamptz;
ALTER TABLE issue_tracker_sync_triage ADD COLUMN IF NOT EXISTS snoozed_by uuid;
ALTER TABLE issue_tracker_sync_triage ADD COLUMN IF NOT EXISTS snooze_reason text NOT NULL DEFAULT '';
-- +goose StatementBegin
DO $issue_replay$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='issue_tracker_sync_triage'::regclass AND conname='issue_tracker_triage_snooze_fields') THEN
  ALTER TABLE issue_tracker_sync_triage ADD CONSTRAINT issue_tracker_triage_snooze_fields CHECK ((snoozed_until IS NULL)=(snoozed_by IS NULL) AND (snoozed_until IS NOT NULL OR snooze_reason='') AND octet_length(snooze_reason)<=512);
 END IF;
END $issue_replay$;
-- +goose StatementEnd
-- +goose StatementBegin

DO $issue_replay$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='issue_tracker_sync_triage_activity'::regclass AND conname='issue_tracker_sync_triage_activity_action_check' AND position('unsnooze' in pg_get_constraintdef(oid))>0) THEN
  ALTER TABLE issue_tracker_sync_triage_activity DROP CONSTRAINT IF EXISTS issue_tracker_sync_triage_activity_action_check;
 END IF;
END $issue_replay$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $issue_replay$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='issue_tracker_sync_triage_activity'::regclass AND conname='issue_tracker_sync_triage_activity_action_check') THEN
  ALTER TABLE issue_tracker_sync_triage_activity ADD CONSTRAINT issue_tracker_sync_triage_activity_action_check CHECK(action IN ('acknowledge','claim','release','snooze','unsnooze'));
 END IF;
END $issue_replay$;
-- +goose StatementEnd
ALTER TABLE issue_tracker_sync_triage_activity ADD COLUMN IF NOT EXISTS snoozed_until timestamptz;
ALTER TABLE issue_tracker_sync_triage_activity ADD COLUMN IF NOT EXISTS reason text NOT NULL DEFAULT '';
-- +goose StatementBegin
DO $issue_replay$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='issue_tracker_sync_triage_activity'::regclass AND conname='issue_tracker_triage_activity_snooze_fields') THEN
  ALTER TABLE issue_tracker_sync_triage_activity ADD CONSTRAINT issue_tracker_triage_activity_snooze_fields CHECK ((action='snooze')=(snoozed_until IS NOT NULL) AND (action='snooze' OR reason='') AND octet_length(reason)<=512);
 END IF;
END $issue_replay$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'Incident snooze history must be preserved; rollback is not supported'; END $$;
-- +goose StatementEnd
