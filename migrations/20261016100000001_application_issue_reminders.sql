-- +goose Up
CREATE TABLE IF NOT EXISTS issue_application_reminder_policies (
 app_id uuid PRIMARY KEY REFERENCES apps(id) ON DELETE CASCADE,
 enabled boolean NOT NULL DEFAULT false,
 revision bigint NOT NULL CHECK(revision>0),
 interval_minutes integer NOT NULL CHECK(interval_minutes BETWEEN 5 AND 1440),
 repeat_limit integer NOT NULL CHECK(repeat_limit BETWEEN 1 AND 3),
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

CREATE TABLE IF NOT EXISTS issue_application_reminders (
 id uuid PRIMARY KEY,
 issue_id uuid NOT NULL REFERENCES app_issues(id) ON DELETE CASCADE,
 response_id uuid NOT NULL,
 sequence integer NOT NULL CHECK(sequence BETWEEN 1 AND 3),
 policy_revision bigint NOT NULL CHECK(policy_revision>0),
 outcome text NOT NULL CHECK(outcome IN ('queued','unrouted','queue_full')),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(issue_id,response_id,sequence)
);

ALTER TABLE issue_application_triage ADD COLUMN IF NOT EXISTS reminder_started_at timestamptz;

ALTER TABLE issue_application_triage ADD COLUMN IF NOT EXISTS last_reminded_at timestamptz;

ALTER TABLE issue_application_triage ADD COLUMN IF NOT EXISTS reminder_count integer NOT NULL DEFAULT 0 CHECK(reminder_count BETWEEN 0 AND 3);

ALTER TABLE issue_team_slack_deliveries ADD COLUMN IF NOT EXISTS reminder_id uuid REFERENCES issue_application_reminders(id) ON DELETE CASCADE;
-- +goose StatementBegin

DO $issue_replay$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='issue_team_slack_deliveries'::regclass AND conname='issue_slack_source_check' AND position('reminder_id' in pg_get_constraintdef(oid))>0) THEN
  ALTER TABLE issue_team_slack_deliveries DROP CONSTRAINT IF EXISTS issue_slack_source_check;
 END IF;
END $issue_replay$;
-- +goose StatementEnd
-- +goose StatementBegin

DO $issue_replay$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='issue_team_slack_deliveries'::regclass AND conname='issue_slack_source_check') THEN
  ALTER TABLE issue_team_slack_deliveries ADD CONSTRAINT issue_slack_source_check CHECK((notification_id IS NOT NULL AND activity_id IS NULL AND reminder_id IS NULL AND application_policy_revision IS NULL AND recipient_account_ids IS NULL AND assigned_team_id IS NULL AND assigned_account_id IS NULL) OR (notification_id IS NULL AND num_nonnulls(activity_id,reminder_id)=1 AND application_policy_revision IS NOT NULL AND application_policy_revision>0 AND recipient_account_ids IS NOT NULL AND cardinality(recipient_account_ids)>0));
 END IF;
END $issue_replay$;
-- +goose StatementEnd

CREATE UNIQUE INDEX IF NOT EXISTS issue_slack_reminder_once ON issue_team_slack_deliveries(reminder_id) WHERE reminder_id IS NOT NULL;

CREATE OR REPLACE VIEW issue_slack_delivery_sources AS
SELECT d.id AS delivery_id,COALESCE(n.issue_id,i.id)::uuid AS issue_id,n.link_id,
 COALESCE(n.kind,act.action,CASE WHEN rem.id IS NOT NULL THEN 'application_reminder' END,'')::text AS kind,COALESCE(n.reason,'')::text AS reason,(COALESCE(act.details,CASE WHEN rem.id IS NOT NULL THEN jsonb_build_object('reminder_sequence',rem.sequence::text) END,'{}'::jsonb) || CASE WHEN i.id IS NOT NULL THEN jsonb_build_object('environment',i.environment) ELSE '{}'::jsonb END)::jsonb AS details,
 COALESCE(CASE WHEN d.notification_id IS NOT NULL THEN
   a.account_id=n.account_id AND p.enabled AND n.team_id=dst.team_id
   AND EXISTS(SELECT 1 FROM app_issue_team_members tm WHERE tm.team_id=dst.team_id AND tm.account_id=ANY(n.recipient_account_ids)
     AND (tm.account_id=a.account_id OR EXISTS(SELECT 1 FROM org_memberships m WHERE m.org_id=a.org_id AND m.account_id=tm.account_id AND m.removed_at IS NULL)))
 ELSE
   ap.enabled AND ap.revision=d.application_policy_revision AND ((d.reminder_id IS NULL AND act.action=ANY(ap.events)) OR (d.reminder_id IS NOT NULL AND rp.enabled AND rp.revision=rem.policy_revision AND tr.incident_id=rem.response_id AND tr.acknowledged_at IS NULL AND i.state='open' AND (tr.snoozed_until IS NULL OR tr.snoozed_until<=clock_timestamp())))
   AND i.id IS NOT NULL AND i.app_id=a.id AND i.account_id=a.account_id
   AND i.assignee_team_id IS NOT DISTINCT FROM d.assigned_team_id AND i.assignee_account_id IS NOT DISTINCT FROM d.assigned_account_id
   AND NOT (i.state='ignored' AND (i.ignored_until IS NULL OR i.ignored_until>clock_timestamp()))
   AND NOT EXISTS(SELECT 1 FROM issue_group_links gl WHERE gl.issue_id=i.id)
   AND NOT EXISTS(SELECT 1 FROM issue_application_triage tr WHERE tr.issue_id=i.id AND tr.snoozed_until>clock_timestamp())
   AND EXISTS(SELECT 1 FROM app_issue_team_members tm WHERE tm.team_id=dst.team_id AND tm.account_id=ANY(d.recipient_account_ids)
     AND (tm.account_id=a.account_id OR EXISTS(SELECT 1 FROM org_memberships m WHERE m.org_id=a.org_id AND m.account_id=tm.account_id AND m.removed_at IS NULL)))
 END,false)::boolean AS eligible,
 COALESCE(d.notification_id IS NOT NULL OR (ap.enabled AND ap.revision=d.application_policy_revision AND ((d.reminder_id IS NULL AND act.action=ANY(ap.events)) OR (d.reminder_id IS NOT NULL AND rp.enabled AND rp.revision=rem.policy_revision AND tr.incident_id=rem.response_id AND tr.acknowledged_at IS NULL AND i.state='open' AND (tr.snoozed_until IS NULL OR tr.snoozed_until<=clock_timestamp())))),false)::boolean AS application_retry_allowed
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
LEFT JOIN issue_application_triage tr ON tr.issue_id=i.id;

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'native reminders are forward-only; preserve delivery evidence'; END $$;
-- +goose StatementEnd
