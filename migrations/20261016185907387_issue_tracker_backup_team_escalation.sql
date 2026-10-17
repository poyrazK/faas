-- +goose Up
CREATE TABLE IF NOT EXISTS issue_tracker_sync_escalation_policies (
 app_id uuid PRIMARY KEY REFERENCES apps(id) ON DELETE CASCADE,
 account_id uuid NOT NULL,
 enabled boolean NOT NULL DEFAULT false,
 team_id uuid,
 delay_minutes integer NOT NULL CHECK(delay_minutes BETWEEN 15 AND 10080),
 revision bigint NOT NULL DEFAULT 1 CHECK(revision>0),
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 CHECK(NOT enabled OR team_id IS NOT NULL)
);

ALTER TABLE issue_tracker_sync_incidents ADD COLUMN IF NOT EXISTS first_reminded_at timestamptz;
ALTER TABLE issue_tracker_sync_incidents ADD COLUMN IF NOT EXISTS escalated_at timestamptz;
ALTER TABLE issue_tracker_sync_incidents ADD COLUMN IF NOT EXISTS escalation_team_id uuid;

UPDATE issue_tracker_sync_incidents SET first_reminded_at=last_reminded_at WHERE reminder_count>0 AND first_reminded_at IS NULL;
-- +goose StatementBegin

DO $issue_replay$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='issue_tracker_sync_incidents'::regclass AND conname='issue_tracker_incident_first_reminder') THEN
  ALTER TABLE issue_tracker_sync_incidents ADD CONSTRAINT issue_tracker_incident_first_reminder CHECK((reminder_count=0)=(first_reminded_at IS NULL));
 END IF;
END $issue_replay$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $issue_replay$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='issue_tracker_sync_incidents'::regclass AND conname='issue_tracker_incident_escalation_receipt') THEN
  ALTER TABLE issue_tracker_sync_incidents ADD CONSTRAINT issue_tracker_incident_escalation_receipt CHECK((escalated_at IS NULL)=(escalation_team_id IS NULL) AND (escalated_at IS NULL OR first_reminded_at IS NOT NULL));
 END IF;
END $issue_replay$;
-- +goose StatementEnd
-- +goose StatementBegin

DO $issue_replay$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='issue_tracker_sync_notifications'::regclass AND conname='issue_tracker_sync_notifications_kind_check' AND position('escalation' in pg_get_constraintdef(oid))>0) THEN
  ALTER TABLE issue_tracker_sync_notifications DROP CONSTRAINT IF EXISTS issue_tracker_sync_notifications_kind_check;
 END IF;
END $issue_replay$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $issue_replay$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='issue_tracker_sync_notifications'::regclass AND conname='issue_tracker_sync_notifications_kind_check') THEN
  ALTER TABLE issue_tracker_sync_notifications ADD CONSTRAINT issue_tracker_sync_notifications_kind_check CHECK(kind IN ('paused','closure','failure','recovered','reminder','escalation'));
 END IF;
END $issue_replay$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'Escalation receipts and history must be preserved; rollback is not supported'; END $$;
-- +goose StatementEnd
