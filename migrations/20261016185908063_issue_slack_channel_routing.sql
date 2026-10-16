-- +goose Up
CREATE TABLE IF NOT EXISTS issue_slack_routing_policies (
 app_id uuid PRIMARY KEY REFERENCES apps(id) ON DELETE CASCADE,
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 revision bigint NOT NULL CHECK(revision>0),
 installation_revision bigint NOT NULL CHECK(installation_revision>0),
 rules jsonb NOT NULL CHECK(jsonb_typeof(rules)='array' AND jsonb_array_length(rules)<=20)
);

ALTER TABLE issue_team_slack_deliveries ADD COLUMN IF NOT EXISTS routing_revision bigint NOT NULL DEFAULT 0 CHECK(routing_revision>=0);

ALTER TABLE issue_team_slack_deliveries ADD COLUMN IF NOT EXISTS routing_rule_key text NOT NULL DEFAULT '' CHECK(routing_rule_key='' OR routing_rule_key ~ '^[a-z0-9][a-z0-9-]{0,47}$');
-- +goose StatementBegin

DO $issue_replay$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='issue_team_slack_deliveries'::regclass AND conname='issue_slack_routing_snapshot') THEN
  ALTER TABLE issue_team_slack_deliveries ADD CONSTRAINT issue_slack_routing_snapshot CHECK((activity_id IS NOT NULL OR (routing_revision=0 AND routing_rule_key='')) AND (routing_rule_key='' OR routing_revision>0));
 END IF;
END $issue_replay$;
-- +goose StatementEnd

ALTER TABLE issue_team_slack_threads ADD COLUMN IF NOT EXISTS channel_id text;

UPDATE issue_team_slack_threads t SET channel_id=COALESCE(
 (SELECT d.channel_id FROM issue_team_slack_deliveries d WHERE d.destination_id=t.destination_id AND d.destination_revision=t.destination_revision AND d.incident_id=t.incident_id ORDER BY d.delivery_seq LIMIT 1),
 (SELECT dst.channel_id FROM issue_team_slack_destinations dst WHERE dst.id=t.destination_id)) WHERE t.channel_id IS NULL;

ALTER TABLE issue_team_slack_threads ALTER COLUMN channel_id SET NOT NULL;
-- +goose StatementBegin

DO $issue_replay$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='issue_team_slack_threads'::regclass AND conname='issue_slack_thread_channel') THEN
  ALTER TABLE issue_team_slack_threads ADD CONSTRAINT issue_slack_thread_channel CHECK(channel_id ~ '^[CG][A-Z0-9]{8,31}$');
 END IF;
END $issue_replay$;
-- +goose StatementEnd
-- +goose StatementBegin

DO $issue_replay$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='issue_team_slack_threads'::regclass AND conname='issue_team_slack_threads_pkey' AND position('channel_id' in pg_get_constraintdef(oid))>0) THEN
  ALTER TABLE issue_team_slack_threads DROP CONSTRAINT IF EXISTS issue_team_slack_threads_pkey;
 END IF;
END $issue_replay$;
-- +goose StatementEnd
-- +goose StatementBegin

DO $issue_replay$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='issue_team_slack_threads'::regclass AND contype='p') THEN
  ALTER TABLE issue_team_slack_threads ADD PRIMARY KEY(destination_id,destination_revision,incident_id,channel_id);
 END IF;
END $issue_replay$;
-- +goose StatementEnd

DROP INDEX IF EXISTS issue_team_slack_delivery_thread;

CREATE INDEX IF NOT EXISTS issue_team_slack_delivery_thread ON issue_team_slack_deliveries(destination_id,destination_revision,incident_id,channel_id,delivery_seq);


CREATE OR REPLACE VIEW issue_slack_delivery_sources AS
SELECT d.id AS delivery_id,COALESCE(n.issue_id,i.id)::uuid AS issue_id,n.link_id,
 COALESCE(n.kind,act.action,CASE WHEN rem.kind='escalation' THEN 'application_escalation' WHEN rem.id IS NOT NULL THEN 'application_reminder' END,'')::text AS kind,COALESCE(n.reason,'')::text AS reason,(COALESCE(act.details,CASE WHEN rem.kind='escalation' THEN '{}'::jsonb WHEN rem.id IS NOT NULL THEN jsonb_build_object('reminder_sequence',rem.sequence::text) END,'{}'::jsonb) || CASE WHEN i.id IS NOT NULL THEN jsonb_build_object('environment',i.environment) ELSE '{}'::jsonb END)::jsonb AS details,
 COALESCE(CASE WHEN d.notification_id IS NOT NULL THEN
   a.account_id=n.account_id AND p.enabled AND n.team_id=dst.team_id
   AND EXISTS(SELECT 1 FROM app_issue_team_members tm WHERE tm.team_id=dst.team_id AND tm.account_id=ANY(n.recipient_account_ids)
     AND (tm.account_id=a.account_id OR EXISTS(SELECT 1 FROM org_memberships m WHERE m.org_id=a.org_id AND m.account_id=tm.account_id AND m.removed_at IS NULL)))
 ELSE
   ap.enabled AND (d.activity_id IS NULL OR (d.routing_revision=COALESCE((SELECT cr.revision FROM issue_slack_routing_policies cr WHERE cr.app_id=a.id),0) AND (d.routing_rule_key='' OR EXISTS(SELECT 1 FROM issue_slack_routing_policies cr JOIN issue_slack_installations si ON si.app_id=cr.app_id WHERE cr.app_id=a.id AND cr.account_id=a.account_id AND si.account_id=a.account_id AND si.status='connected' AND si.revision=cr.installation_revision AND si.revision=dst.oauth_revision)))) AND ap.revision=d.application_policy_revision AND ((d.reminder_id IS NULL AND act.action=ANY(ap.events)) OR (d.reminder_id IS NOT NULL AND rp.enabled AND rp.revision=rem.policy_revision AND tr.escalation_team_id IS NOT DISTINCT FROM rem.responsibility_team_id AND (rem.kind='reminder' OR (ep.enabled AND ep.revision=rem.escalation_policy_revision AND ep.team_id=dst.team_id)) AND tr.incident_id=rem.response_id AND tr.acknowledged_at IS NULL AND i.state='open' AND (tr.snoozed_until IS NULL OR tr.snoozed_until<=clock_timestamp())))
   AND i.id IS NOT NULL AND i.app_id=a.id AND i.account_id=a.account_id
   AND i.assignee_team_id IS NOT DISTINCT FROM d.assigned_team_id AND i.assignee_account_id IS NOT DISTINCT FROM d.assigned_account_id
   AND NOT (i.state='ignored' AND (i.ignored_until IS NULL OR i.ignored_until>clock_timestamp()))
   AND NOT EXISTS(SELECT 1 FROM issue_group_links gl WHERE gl.issue_id=i.id)
   AND NOT EXISTS(SELECT 1 FROM issue_application_triage tr WHERE tr.issue_id=i.id AND tr.snoozed_until>clock_timestamp())
   AND EXISTS(SELECT 1 FROM app_issue_team_members tm WHERE tm.team_id=dst.team_id AND tm.account_id=ANY(d.recipient_account_ids)
     AND (tm.account_id=a.account_id OR EXISTS(SELECT 1 FROM org_memberships m WHERE m.org_id=a.org_id AND m.account_id=tm.account_id AND m.removed_at IS NULL)))
 END,false)::boolean AS eligible,
 COALESCE(d.notification_id IS NOT NULL OR (ap.enabled AND (d.activity_id IS NULL OR (d.routing_revision=COALESCE((SELECT cr.revision FROM issue_slack_routing_policies cr WHERE cr.app_id=a.id),0) AND (d.routing_rule_key='' OR EXISTS(SELECT 1 FROM issue_slack_routing_policies cr JOIN issue_slack_installations si ON si.app_id=cr.app_id WHERE cr.app_id=a.id AND cr.account_id=a.account_id AND si.account_id=a.account_id AND si.status='connected' AND si.revision=cr.installation_revision AND si.revision=dst.oauth_revision)))) AND ap.revision=d.application_policy_revision AND ((d.reminder_id IS NULL AND act.action=ANY(ap.events)) OR (d.reminder_id IS NOT NULL AND rp.enabled AND rp.revision=rem.policy_revision AND tr.escalation_team_id IS NOT DISTINCT FROM rem.responsibility_team_id AND (rem.kind='reminder' OR (ep.enabled AND ep.revision=rem.escalation_policy_revision AND ep.team_id=dst.team_id)) AND tr.incident_id=rem.response_id AND tr.acknowledged_at IS NULL AND i.state='open' AND (tr.snoozed_until IS NULL OR tr.snoozed_until<=clock_timestamp())))),false)::boolean AS application_retry_allowed
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
DO $$ BEGIN RAISE EXCEPTION 'Slack routing snapshots are forward-only'; END $$;
-- +goose StatementEnd
