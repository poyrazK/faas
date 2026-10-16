-- +goose Up
CREATE TABLE IF NOT EXISTS issue_application_slack_policies (
 app_id uuid PRIMARY KEY REFERENCES apps(id) ON DELETE CASCADE,
 enabled boolean NOT NULL DEFAULT false,
 revision bigint NOT NULL CHECK(revision>0),
 events text[] NOT NULL CHECK(cardinality(events)<=4 AND events <@ ARRAY['created','regressed','impact_threshold_reached','frequency_threshold_reached']::text[]),
 fallback_team_id uuid REFERENCES app_issue_teams(id) ON DELETE SET NULL,
 skipped_deliveries bigint NOT NULL DEFAULT 0 CHECK(skipped_deliveries>=0),
 unrouted_alerts bigint NOT NULL DEFAULT 0 CHECK(unrouted_alerts>=0),
 CHECK(NOT enabled OR cardinality(events)>0)
);

ALTER TABLE issue_team_slack_deliveries ALTER COLUMN notification_id DROP NOT NULL;

ALTER TABLE issue_team_slack_deliveries ADD COLUMN IF NOT EXISTS activity_id uuid REFERENCES issue_activity(id) ON DELETE CASCADE;

ALTER TABLE issue_team_slack_deliveries ADD COLUMN IF NOT EXISTS application_policy_revision bigint;

ALTER TABLE issue_team_slack_deliveries ADD COLUMN IF NOT EXISTS recipient_account_ids uuid[];

ALTER TABLE issue_team_slack_deliveries ADD COLUMN IF NOT EXISTS assigned_team_id uuid;

ALTER TABLE issue_team_slack_deliveries ADD COLUMN IF NOT EXISTS assigned_account_id uuid;
-- +goose StatementBegin

DO $issue_replay$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='issue_team_slack_deliveries'::regclass AND conname='issue_slack_source_check') THEN
  ALTER TABLE issue_team_slack_deliveries ADD CONSTRAINT issue_slack_source_check CHECK(
 (notification_id IS NOT NULL AND activity_id IS NULL AND application_policy_revision IS NULL AND recipient_account_ids IS NULL AND assigned_team_id IS NULL AND assigned_account_id IS NULL)
 OR (notification_id IS NULL AND activity_id IS NOT NULL AND application_policy_revision>0 AND application_policy_revision IS NOT NULL AND recipient_account_ids IS NOT NULL AND cardinality(recipient_account_ids)>0)
);
 END IF;
END $issue_replay$;
-- +goose StatementEnd

CREATE UNIQUE INDEX IF NOT EXISTS issue_slack_activity_delivery ON issue_team_slack_deliveries(activity_id) WHERE activity_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS issue_slack_application_history ON issue_team_slack_deliveries(created_at,delivery_seq) WHERE activity_id IS NOT NULL;


CREATE OR REPLACE VIEW issue_slack_delivery_sources AS
SELECT d.id AS delivery_id,COALESCE(n.issue_id,i.id)::uuid AS issue_id,n.link_id,
 COALESCE(n.kind,act.action,'')::text AS kind,COALESCE(n.reason,'')::text AS reason,(COALESCE(act.details,'{}'::jsonb) || CASE WHEN i.id IS NOT NULL THEN jsonb_build_object('environment',i.environment) ELSE '{}'::jsonb END)::jsonb AS details,
 COALESCE(CASE WHEN d.notification_id IS NOT NULL THEN
   a.account_id=n.account_id AND p.enabled AND n.team_id=dst.team_id
   AND EXISTS(SELECT 1 FROM app_issue_team_members tm WHERE tm.team_id=dst.team_id AND tm.account_id=ANY(n.recipient_account_ids)
     AND (tm.account_id=a.account_id OR EXISTS(SELECT 1 FROM org_memberships m WHERE m.org_id=a.org_id AND m.account_id=tm.account_id AND m.removed_at IS NULL)))
 ELSE
   ap.enabled AND ap.revision=d.application_policy_revision AND act.action=ANY(ap.events)
   AND i.id IS NOT NULL AND i.app_id=a.id AND i.account_id=a.account_id
   AND i.assignee_team_id IS NOT DISTINCT FROM d.assigned_team_id AND i.assignee_account_id IS NOT DISTINCT FROM d.assigned_account_id
   AND NOT (i.state='ignored' AND (i.ignored_until IS NULL OR i.ignored_until>clock_timestamp()))
   AND NOT EXISTS(SELECT 1 FROM issue_group_links gl WHERE gl.issue_id=i.id)
   AND EXISTS(SELECT 1 FROM app_issue_team_members tm WHERE tm.team_id=dst.team_id AND tm.account_id=ANY(d.recipient_account_ids)
     AND (tm.account_id=a.account_id OR EXISTS(SELECT 1 FROM org_memberships m WHERE m.org_id=a.org_id AND m.account_id=tm.account_id AND m.removed_at IS NULL)))
 END,false)::boolean AS eligible,
 COALESCE(d.activity_id IS NULL OR (ap.enabled AND ap.revision=d.application_policy_revision AND act.action=ANY(ap.events)),false)::boolean AS application_retry_allowed
FROM issue_team_slack_deliveries d
JOIN issue_team_slack_destinations dst ON dst.id=d.destination_id
JOIN apps a ON a.id=dst.app_id
LEFT JOIN issue_tracker_sync_notifications n ON n.id=d.notification_id
LEFT JOIN issue_tracker_sync_notification_policies p ON p.app_id=a.id AND p.account_id=a.account_id
LEFT JOIN issue_activity act ON act.id=d.activity_id
LEFT JOIN app_issues i ON i.id=act.issue_id
LEFT JOIN issue_application_slack_policies ap ON ap.app_id=a.id;

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'Application Slack delivery evidence must be preserved; rollback is not supported'; END $$;
-- +goose StatementEnd
