-- +goose Up
-- +goose StatementBegin
DO $issue_replay$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='issue_team_slack_deliveries'::regclass AND conname='issue_team_slack_deliveries_last_error_check' AND position('issue_snoozed' in pg_get_constraintdef(oid))>0) THEN
  ALTER TABLE issue_team_slack_deliveries DROP CONSTRAINT IF EXISTS issue_team_slack_deliveries_last_error_check;
 END IF;
END $issue_replay$;
-- +goose StatementEnd
-- +goose StatementBegin

DO $issue_replay$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='issue_team_slack_deliveries'::regclass AND conname='issue_team_slack_deliveries_last_error_check') THEN
  ALTER TABLE issue_team_slack_deliveries ADD CONSTRAINT issue_team_slack_deliveries_last_error_check CHECK(last_error IN ('','issue_snoozed','destination_unavailable','delivery_expired','attempts_exhausted','token_unavailable','transport_error','rate_limited','slack_unavailable','slack_rejected','invalid_auth','channel_not_found','not_in_channel','missing_scope','thread_unavailable','invalid_response'));
 END IF;
END $issue_replay$;
-- +goose StatementEnd

CREATE TABLE IF NOT EXISTS issue_application_triage (
 issue_id uuid PRIMARY KEY REFERENCES app_issues(id) ON DELETE CASCADE,
 incident_id uuid NOT NULL DEFAULT gen_random_uuid(),
 revision bigint NOT NULL CHECK(revision>0),
 acknowledged_by uuid REFERENCES accounts(id) ON DELETE SET NULL,
 acknowledged_at timestamptz,
 snoozed_until timestamptz,
 snoozed_by uuid REFERENCES accounts(id) ON DELETE SET NULL,
 snooze_reason text NOT NULL DEFAULT '' CHECK(octet_length(snooze_reason)<=1024),
 CHECK((acknowledged_at IS NOT NULL) OR acknowledged_by IS NULL),
 CHECK(snoozed_until IS NOT NULL OR (snoozed_by IS NULL AND snooze_reason=''))
);

CREATE TABLE IF NOT EXISTS issue_application_triage_activity (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 issue_id uuid NOT NULL REFERENCES app_issues(id) ON DELETE CASCADE,
 incident_id uuid NOT NULL,
 revision bigint NOT NULL CHECK(revision>0),
 actor_id uuid REFERENCES accounts(id) ON DELETE SET NULL,
 action text NOT NULL CHECK(action IN ('acknowledge','snooze','unsnooze','reset')),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 snoozed_until timestamptz,
 reason text NOT NULL DEFAULT '' CHECK(octet_length(reason)<=1024),
 UNIQUE(issue_id,revision)
);

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
   AND NOT EXISTS(SELECT 1 FROM issue_application_triage tr WHERE tr.issue_id=i.id AND tr.snoozed_until>clock_timestamp())
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
DO $$ BEGIN RAISE EXCEPTION 'application issue triage is forward-only; preserve response history'; END $$;
-- +goose StatementEnd
