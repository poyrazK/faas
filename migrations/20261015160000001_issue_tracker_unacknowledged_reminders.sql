-- +goose Up
CREATE TABLE public.issue_tracker_sync_reminder_policies (
 app_id uuid PRIMARY KEY REFERENCES public.apps(id) ON DELETE CASCADE,
 account_id uuid NOT NULL,
 enabled boolean NOT NULL DEFAULT false,
 interval_minutes integer NOT NULL CHECK(interval_minutes BETWEEN 15 AND 10080),
 revision bigint NOT NULL DEFAULT 1 CHECK(revision>0),
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
ALTER TABLE public.issue_tracker_sync_incidents
 ADD COLUMN opened_at timestamptz,
 ADD COLUMN reminder_count integer NOT NULL DEFAULT 0 CHECK(reminder_count BETWEEN 0 AND 3),
 ADD COLUMN last_reminded_at timestamptz,
 ADD CONSTRAINT issue_tracker_incident_reminder_receipt CHECK((reminder_count=0)=(last_reminded_at IS NULL));
UPDATE public.issue_tracker_sync_incidents SET opened_at=clock_timestamp() WHERE incident_id IS NOT NULL;
ALTER TABLE public.issue_tracker_sync_notifications
 DROP CONSTRAINT issue_tracker_sync_notifications_kind_check,
 ADD CONSTRAINT issue_tracker_sync_notifications_kind_check CHECK(kind IN ('paused','closure','failure','recovered','reminder')),
 DROP CONSTRAINT issue_tracker_sync_notifications_incident_id_kind_key,
 ADD COLUMN reminder_sequence integer NOT NULL DEFAULT 0,
 ADD CONSTRAINT issue_tracker_notification_reminder_sequence CHECK((kind='reminder' AND reminder_sequence BETWEEN 1 AND 3) OR (kind<>'reminder' AND reminder_sequence=0)),
 ADD CONSTRAINT issue_tracker_notification_incident_kind_sequence UNIQUE(incident_id,kind,reminder_sequence);

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'Reminder receipts and history must be preserved; rollback is not supported'; END $$;
-- +goose StatementEnd
