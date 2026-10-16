-- +goose Up
CREATE TABLE IF NOT EXISTS issue_application_escalation_policies (
 app_id uuid PRIMARY KEY REFERENCES apps(id) ON DELETE CASCADE,
 enabled boolean NOT NULL DEFAULT false,
 revision bigint NOT NULL CHECK(revision>0),
 delay_minutes integer NOT NULL CHECK(delay_minutes BETWEEN 15 AND 10080),
 team_id uuid REFERENCES app_issue_teams(id) ON DELETE SET NULL,
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

ALTER TABLE issue_application_triage ADD COLUMN IF NOT EXISTS first_reminded_at timestamptz;

ALTER TABLE issue_application_triage ADD COLUMN IF NOT EXISTS escalation_attempted_at timestamptz;

ALTER TABLE issue_application_triage ADD COLUMN IF NOT EXISTS escalated_at timestamptz;

ALTER TABLE issue_application_triage ADD COLUMN IF NOT EXISTS escalation_team_id uuid REFERENCES app_issue_teams(id) ON DELETE SET NULL;

ALTER TABLE issue_application_reminders ADD COLUMN IF NOT EXISTS kind text NOT NULL DEFAULT 'reminder' CHECK(kind IN ('reminder','escalation'));

ALTER TABLE issue_application_reminders ADD COLUMN IF NOT EXISTS escalation_policy_revision bigint CHECK(escalation_policy_revision>0);

ALTER TABLE issue_application_reminders ADD COLUMN IF NOT EXISTS responsibility_team_id uuid REFERENCES app_issue_teams(id) ON DELETE SET NULL;

ALTER TABLE issue_application_reminders ADD COLUMN IF NOT EXISTS target_team_name text NOT NULL DEFAULT '' CHECK(octet_length(target_team_name)<=64);
-- +goose StatementBegin

DO $issue_replay$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='issue_application_reminders'::regclass AND conname='issue_application_reminders_sequence_check' AND position('escalation' in pg_get_constraintdef(oid))>0) THEN
  ALTER TABLE issue_application_reminders DROP CONSTRAINT IF EXISTS issue_application_reminders_sequence_check;
 END IF;
END $issue_replay$;
-- +goose StatementEnd
-- +goose StatementBegin

DO $issue_replay$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='issue_application_reminders'::regclass AND conname='issue_application_reminders_sequence_check') THEN
  ALTER TABLE issue_application_reminders ADD CONSTRAINT issue_application_reminders_sequence_check CHECK((kind='reminder' AND sequence BETWEEN 1 AND 3 AND escalation_policy_revision IS NULL) OR (kind='escalation' AND sequence=0 AND escalation_policy_revision IS NOT NULL));
 END IF;
END $issue_replay$;
-- +goose StatementEnd
-- +goose StatementBegin

DO $issue_replay$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='issue_application_reminders'::regclass AND conname='issue_application_reminders_outcome_check' AND position('already_responsible' in pg_get_constraintdef(oid))>0) THEN
  ALTER TABLE issue_application_reminders DROP CONSTRAINT IF EXISTS issue_application_reminders_outcome_check;
 END IF;
END $issue_replay$;
-- +goose StatementEnd
-- +goose StatementBegin

DO $issue_replay$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='issue_application_reminders'::regclass AND conname='issue_application_reminders_outcome_check') THEN
  ALTER TABLE issue_application_reminders ADD CONSTRAINT issue_application_reminders_outcome_check CHECK(outcome IN ('queued','unrouted','queue_full','already_responsible') AND (kind='escalation' OR outcome<>'already_responsible'));
 END IF;
END $issue_replay$;
-- +goose StatementEnd

ALTER TABLE issue_application_reminders DROP CONSTRAINT IF EXISTS issue_application_reminders_issue_id_response_id_sequence_key;
-- +goose StatementBegin

DO $issue_replay$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='issue_application_reminders'::regclass AND contype='u' AND pg_get_constraintdef(oid)='UNIQUE (issue_id, response_id, kind, sequence)') THEN
  ALTER TABLE issue_application_reminders ADD UNIQUE(issue_id,response_id,kind,sequence);
 END IF;
END $issue_replay$;
-- +goose StatementEnd

UPDATE issue_application_triage tr SET first_reminded_at=(SELECT min(r.created_at) FROM issue_application_reminders r WHERE r.issue_id=tr.issue_id AND r.response_id=tr.incident_id AND r.outcome='queued') WHERE tr.first_reminded_at IS NULL;

CREATE OR REPLACE VIEW issue_slack_delivery_sources AS
SELECT d.id AS delivery_id,COALESCE(n.issue_id,i.id)::uuid AS issue_id,n.link_id,
 COALESCE(n.kind,act.action,CASE WHEN rem.kind='escalation' THEN 'application_escalation' WHEN rem.id IS NOT NULL THEN 'application_reminder' END,'')::text AS kind,COALESCE(n.reason,'')::text AS reason,(COALESCE(act.details,CASE WHEN rem.kind='escalation' THEN '{}'::jsonb WHEN rem.id IS NOT NULL THEN jsonb_build_object('reminder_sequence',rem.sequence::text) END,'{}'::jsonb) || CASE WHEN i.id IS NOT NULL THEN jsonb_build_object('environment',i.environment) ELSE '{}'::jsonb END)::jsonb AS details,
 COALESCE(CASE WHEN d.notification_id IS NOT NULL THEN
   a.account_id=n.account_id AND p.enabled AND n.team_id=dst.team_id
   AND EXISTS(SELECT 1 FROM app_issue_team_members tm WHERE tm.team_id=dst.team_id AND tm.account_id=ANY(n.recipient_account_ids)
     AND (tm.account_id=a.account_id OR EXISTS(SELECT 1 FROM org_memberships m WHERE m.org_id=a.org_id AND m.account_id=tm.account_id AND m.removed_at IS NULL)))
 ELSE
   ap.enabled AND ap.revision=d.application_policy_revision AND ((d.reminder_id IS NULL AND act.action=ANY(ap.events)) OR (d.reminder_id IS NOT NULL AND rp.enabled AND rp.revision=rem.policy_revision AND tr.escalation_team_id IS NOT DISTINCT FROM rem.responsibility_team_id AND (rem.kind='reminder' OR (ep.enabled AND ep.revision=rem.escalation_policy_revision AND ep.team_id=dst.team_id)) AND tr.incident_id=rem.response_id AND tr.acknowledged_at IS NULL AND i.state='open' AND (tr.snoozed_until IS NULL OR tr.snoozed_until<=clock_timestamp())))
   AND i.id IS NOT NULL AND i.app_id=a.id AND i.account_id=a.account_id
   AND i.assignee_team_id IS NOT DISTINCT FROM d.assigned_team_id AND i.assignee_account_id IS NOT DISTINCT FROM d.assigned_account_id
   AND NOT (i.state='ignored' AND (i.ignored_until IS NULL OR i.ignored_until>clock_timestamp()))
   AND NOT EXISTS(SELECT 1 FROM issue_group_links gl WHERE gl.issue_id=i.id)
   AND NOT EXISTS(SELECT 1 FROM issue_application_triage tr WHERE tr.issue_id=i.id AND tr.snoozed_until>clock_timestamp())
   AND EXISTS(SELECT 1 FROM app_issue_team_members tm WHERE tm.team_id=dst.team_id AND tm.account_id=ANY(d.recipient_account_ids)
     AND (tm.account_id=a.account_id OR EXISTS(SELECT 1 FROM org_memberships m WHERE m.org_id=a.org_id AND m.account_id=tm.account_id AND m.removed_at IS NULL)))
 END,false)::boolean AS eligible,
 COALESCE(d.notification_id IS NOT NULL OR (ap.enabled AND ap.revision=d.application_policy_revision AND ((d.reminder_id IS NULL AND act.action=ANY(ap.events)) OR (d.reminder_id IS NOT NULL AND rp.enabled AND rp.revision=rem.policy_revision AND tr.escalation_team_id IS NOT DISTINCT FROM rem.responsibility_team_id AND (rem.kind='reminder' OR (ep.enabled AND ep.revision=rem.escalation_policy_revision AND ep.team_id=dst.team_id)) AND tr.incident_id=rem.response_id AND tr.acknowledged_at IS NULL AND i.state='open' AND (tr.snoozed_until IS NULL OR tr.snoozed_until<=clock_timestamp())))),false)::boolean AS application_retry_allowed
FROM issue_team_slack_deliveries d
JOIN issue_team_slack_destinations dst ON dst.id=d.destination_id
JOIN apps a ON a.id=dst.app_id
LEFT JOIN issue_tracker_sync_notifications n ON n.id=d.notification_id
LEFT JOIN issue_tracker_sync_notification_policies p ON p.app_id=a.id AND p.account_id=a.account_id
LEFT JOIN issue_activity act ON act.id=d.activity_id
LEFT JOIN issue_application_reminders rem ON rem.id=d.reminder_id
LEFT JOIN app_issues i ON i.id=COALESCE(act.issue_id,rem.issue_id)
LEFT JOIN issue_application_slack_policies ap ON ap.app_id=a.id
LEFT JOIN issue_application_reminder_policies rp ON rp.app_id=a.id
LEFT JOIN issue_application_triage tr ON tr.issue_id=i.id
LEFT JOIN issue_application_escalation_policies ep ON ep.app_id=a.id;

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'native escalation is forward-only; preserve response evidence'; END $$;
-- +goose StatementEnd
