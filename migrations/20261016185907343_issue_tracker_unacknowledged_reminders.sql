-- +goose Up
CREATE TABLE IF NOT EXISTS issue_tracker_sync_reminder_policies (
 app_id uuid PRIMARY KEY REFERENCES apps(id) ON DELETE CASCADE,
 account_id uuid NOT NULL,
 enabled boolean NOT NULL DEFAULT false,
 interval_minutes integer NOT NULL CHECK(interval_minutes BETWEEN 15 AND 10080),
 revision bigint NOT NULL DEFAULT 1 CHECK(revision>0),
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

ALTER TABLE issue_tracker_sync_incidents ADD COLUMN IF NOT EXISTS opened_at timestamptz;
ALTER TABLE issue_tracker_sync_incidents ADD COLUMN IF NOT EXISTS reminder_count integer NOT NULL DEFAULT 0 CHECK(reminder_count BETWEEN 0 AND 3);
ALTER TABLE issue_tracker_sync_incidents ADD COLUMN IF NOT EXISTS last_reminded_at timestamptz;
-- +goose StatementBegin
DO $issue_replay$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='issue_tracker_sync_incidents'::regclass AND conname='issue_tracker_incident_reminder_receipt') THEN
  ALTER TABLE issue_tracker_sync_incidents ADD CONSTRAINT issue_tracker_incident_reminder_receipt CHECK((reminder_count=0)=(last_reminded_at IS NULL));
 END IF;
END $issue_replay$;
-- +goose StatementEnd

UPDATE issue_tracker_sync_incidents SET opened_at=clock_timestamp() WHERE incident_id IS NOT NULL AND opened_at IS NULL;
-- +goose StatementBegin

DO $issue_replay$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='issue_tracker_sync_notifications'::regclass AND conname='issue_tracker_sync_notifications_kind_check' AND position('reminder' in pg_get_constraintdef(oid))>0) THEN
  ALTER TABLE issue_tracker_sync_notifications DROP CONSTRAINT IF EXISTS issue_tracker_sync_notifications_kind_check;
 END IF;
END $issue_replay$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $issue_replay$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='issue_tracker_sync_notifications'::regclass AND conname='issue_tracker_sync_notifications_kind_check') THEN
  ALTER TABLE issue_tracker_sync_notifications ADD CONSTRAINT issue_tracker_sync_notifications_kind_check CHECK(kind IN ('paused','closure','failure','recovered','reminder'));
 END IF;
END $issue_replay$;
-- +goose StatementEnd
ALTER TABLE issue_tracker_sync_notifications DROP CONSTRAINT IF EXISTS issue_tracker_sync_notifications_incident_id_kind_key;
ALTER TABLE issue_tracker_sync_notifications ADD COLUMN IF NOT EXISTS reminder_sequence integer NOT NULL DEFAULT 0;
-- +goose StatementBegin
DO $issue_replay$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='issue_tracker_sync_notifications'::regclass AND conname='issue_tracker_notification_reminder_sequence') THEN
  ALTER TABLE issue_tracker_sync_notifications ADD CONSTRAINT issue_tracker_notification_reminder_sequence CHECK((kind='reminder' AND reminder_sequence BETWEEN 1 AND 3) OR (kind<>'reminder' AND reminder_sequence=0));
 END IF;
END $issue_replay$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $issue_replay$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='issue_tracker_sync_notifications'::regclass AND conname='issue_tracker_notification_incident_kind_sequence') THEN
  ALTER TABLE issue_tracker_sync_notifications ADD CONSTRAINT issue_tracker_notification_incident_kind_sequence UNIQUE(incident_id,kind,reminder_sequence);
 END IF;
END $issue_replay$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'Reminder receipts and history must be preserved; rollback is not supported'; END $$;
-- +goose StatementEnd
