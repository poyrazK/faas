-- +goose Up
ALTER TABLE public.issue_tracker_sync_triage
 ADD COLUMN snoozed_until timestamptz,
 ADD COLUMN snoozed_by uuid,
 ADD COLUMN snooze_reason text NOT NULL DEFAULT '',
 ADD CONSTRAINT issue_tracker_triage_snooze_fields CHECK ((snoozed_until IS NULL)=(snoozed_by IS NULL) AND (snoozed_until IS NOT NULL OR snooze_reason='') AND octet_length(snooze_reason)<=512);
ALTER TABLE public.issue_tracker_sync_triage_activity
 DROP CONSTRAINT issue_tracker_sync_triage_activity_action_check,
 ADD CONSTRAINT issue_tracker_sync_triage_activity_action_check CHECK(action IN ('acknowledge','claim','release','snooze','unsnooze')),
 ADD COLUMN snoozed_until timestamptz,
 ADD COLUMN reason text NOT NULL DEFAULT '',
 ADD CONSTRAINT issue_tracker_triage_activity_snooze_fields CHECK ((action='snooze')=(snoozed_until IS NOT NULL) AND (action='snooze' OR reason='') AND octet_length(reason)<=512);

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'Incident snooze history must be preserved; rollback is not supported'; END $$;
-- +goose StatementEnd
