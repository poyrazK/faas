-- +goose Up
CREATE TABLE public.issue_tracker_sync_escalation_policies (
 app_id uuid PRIMARY KEY REFERENCES public.apps(id) ON DELETE CASCADE,
 account_id uuid NOT NULL,
 enabled boolean NOT NULL DEFAULT false,
 team_id uuid,
 delay_minutes integer NOT NULL CHECK(delay_minutes BETWEEN 15 AND 10080),
 revision bigint NOT NULL DEFAULT 1 CHECK(revision>0),
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 CHECK(NOT enabled OR team_id IS NOT NULL)
);
ALTER TABLE public.issue_tracker_sync_incidents
 ADD COLUMN first_reminded_at timestamptz,
 ADD COLUMN escalated_at timestamptz,
 ADD COLUMN escalation_team_id uuid;
UPDATE public.issue_tracker_sync_incidents SET first_reminded_at=last_reminded_at WHERE reminder_count>0;
ALTER TABLE public.issue_tracker_sync_incidents
 ADD CONSTRAINT issue_tracker_incident_first_reminder CHECK((reminder_count=0)=(first_reminded_at IS NULL)),
 ADD CONSTRAINT issue_tracker_incident_escalation_receipt CHECK((escalated_at IS NULL)=(escalation_team_id IS NULL) AND (escalated_at IS NULL OR first_reminded_at IS NOT NULL));
ALTER TABLE public.issue_tracker_sync_notifications
 DROP CONSTRAINT issue_tracker_sync_notifications_kind_check,
 ADD CONSTRAINT issue_tracker_sync_notifications_kind_check CHECK(kind IN ('paused','closure','failure','recovered','reminder','escalation'));

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'Escalation receipts and history must be preserved; rollback is not supported'; END $$;
-- +goose StatementEnd
