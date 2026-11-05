-- +goose Up
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION issue_actor_current_member(p_app uuid, p_actor uuid) RETURNS boolean
LANGUAGE sql STABLE AS $$
 SELECT EXISTS (
  SELECT 1 FROM apps a LEFT JOIN orgs o ON o.id=a.org_id
  WHERE a.id=p_app AND (
   (a.org_id IS NULL AND a.account_id=p_actor)
   OR (o.personal_org AND o.personal_owner_account_id=a.account_id AND a.account_id=p_actor)
   OR (NOT o.personal_org AND EXISTS (SELECT 1 FROM org_memberships m
      WHERE m.org_id=a.org_id AND m.account_id=p_actor AND m.removed_at IS NULL))
  )
 );
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION issue_actor_can_manage_tracker(p_app uuid, p_actor uuid) RETURNS boolean
LANGUAGE sql STABLE AS $$
 SELECT EXISTS (
  SELECT 1 FROM apps a LEFT JOIN orgs o ON o.id=a.org_id
  WHERE a.id=p_app AND (
   (a.org_id IS NULL AND a.account_id=p_actor)
   OR (o.personal_org AND o.personal_owner_account_id=a.account_id AND a.account_id=p_actor)
   OR (NOT o.personal_org AND EXISTS (SELECT 1 FROM org_memberships m
      WHERE m.org_id=a.org_id AND m.account_id=p_actor AND m.removed_at IS NULL AND m.role IN ('owner','admin')))
  )
 );
$$;

-- +goose StatementEnd

CREATE OR REPLACE VIEW issue_application_response_access AS
SELECT i.id AS issue_id,a.id AS app_id,a.account_id AS owner_account_id,actor.id AS actor_account_id,owner.plan AS owner_plan,
 CASE WHEN tr.escalated_at IS NOT NULL THEN tr.escalation_team_id ELSE i.assignee_team_id END::uuid AS responsible_team_id,route.team_id AS routed_team_id
FROM app_issues i JOIN apps a ON a.id=i.app_id AND a.account_id=i.account_id
JOIN app_resource_accounts owner ON owner.app_id=a.id
LEFT JOIN orgs issue_owner_org ON issue_owner_org.id=a.org_id
LEFT JOIN issue_application_triage tr ON tr.issue_id=i.id
LEFT JOIN issue_application_slack_policies p ON p.app_id=a.id AND p.enabled
LEFT JOIN LATERAL (
 SELECT dst.team_id FROM issue_team_slack_destinations dst
 WHERE p.app_id IS NOT NULL AND dst.app_id=a.id AND dst.account_id=a.account_id AND dst.enabled
 AND dst.team_id IN((CASE WHEN tr.escalated_at IS NOT NULL THEN tr.escalation_team_id ELSE i.assignee_team_id END),p.fallback_team_id)
 AND EXISTS(SELECT 1 FROM app_issue_team_members member WHERE member.team_id=dst.team_id AND issue_actor_current_member(a.id,member.account_id))
 ORDER BY CASE WHEN dst.team_id=(CASE WHEN tr.escalated_at IS NOT NULL THEN tr.escalation_team_id ELSE i.assignee_team_id END) THEN 0 ELSE 1 END LIMIT 1
) route ON true
JOIN accounts actor ON issue_actor_current_member(a.id,actor.id)
WHERE a.deleted_at IS NULL AND a.status<>'deleted' AND owner.status='active' AND owner.abuse_hold_at IS NULL
AND (issue_owner_org.id IS NULL OR (issue_owner_org.personal_org AND issue_owner_org.personal_owner_account_id=a.account_id) OR (NOT issue_owner_org.personal_org AND issue_owner_org.status='active' AND NOT issue_owner_org.deleted_pending AND issue_owner_org.abuse_hold_at IS NULL))
AND NOT EXISTS(SELECT 1 FROM issue_group_links gl WHERE gl.issue_id=i.id)
AND (issue_actor_can_manage_tracker(a.id,actor.id) OR actor.id=a.account_id OR actor.id=i.assignee_account_id OR EXISTS(
 SELECT 1 FROM app_issue_team_members tm WHERE tm.account_id=actor.id AND
 (tm.team_id=i.assignee_team_id OR (tr.escalated_at IS NOT NULL AND tm.team_id=tr.escalation_team_id) OR tm.team_id=route.team_id)));

CREATE OR REPLACE VIEW issue_slack_delivery_sources AS
SELECT d.id AS delivery_id,COALESCE(n.issue_id,i.id)::uuid AS issue_id,n.link_id,
 COALESCE(n.kind,act.action,CASE WHEN rem.kind='escalation' THEN 'application_escalation' WHEN rem.id IS NOT NULL THEN 'application_reminder' END,'')::text AS kind,COALESCE(n.reason,'')::text AS reason,(COALESCE(act.details,CASE WHEN rem.kind='escalation' THEN '{}'::jsonb WHEN rem.id IS NOT NULL THEN jsonb_build_object('reminder_sequence',rem.sequence::text) END,'{}'::jsonb) || CASE WHEN i.id IS NOT NULL THEN jsonb_build_object('environment',i.environment) ELSE '{}'::jsonb END)::jsonb AS details,
 COALESCE(CASE WHEN d.notification_id IS NOT NULL THEN
   a.account_id=n.account_id AND p.enabled AND n.team_id=dst.team_id
   AND EXISTS(SELECT 1 FROM app_issue_team_members tm WHERE tm.team_id=dst.team_id AND tm.account_id=ANY(n.recipient_account_ids)
     AND issue_actor_current_member(a.id,tm.account_id))
 ELSE
   ap.enabled AND (d.activity_id IS NULL OR (d.routing_revision=COALESCE((SELECT cr.revision FROM issue_slack_routing_policies cr WHERE cr.app_id=a.id),0) AND (d.routing_rule_key='' OR EXISTS(SELECT 1 FROM issue_slack_routing_policies cr JOIN issue_slack_installations si ON si.app_id=cr.app_id WHERE cr.app_id=a.id AND cr.account_id=a.account_id AND si.account_id=a.account_id AND si.status='connected' AND si.revision=cr.installation_revision AND si.revision=dst.oauth_revision)))) AND ap.revision=d.application_policy_revision AND ((d.reminder_id IS NULL AND act.action=ANY(ap.events)) OR (d.reminder_id IS NOT NULL AND rp.enabled AND rp.revision=rem.policy_revision AND tr.escalation_team_id IS NOT DISTINCT FROM rem.responsibility_team_id AND (rem.kind='reminder' OR (ep.enabled AND ep.revision=rem.escalation_policy_revision AND ep.team_id=dst.team_id)) AND tr.incident_id=rem.response_id AND tr.acknowledged_at IS NULL AND i.state='open' AND (tr.snoozed_until IS NULL OR tr.snoozed_until<=clock_timestamp())))
   AND i.id IS NOT NULL AND i.app_id=a.id AND i.account_id=a.account_id
   AND i.assignee_team_id IS NOT DISTINCT FROM d.assigned_team_id AND i.assignee_account_id IS NOT DISTINCT FROM d.assigned_account_id
   AND NOT (i.state='ignored' AND (i.ignored_until IS NULL OR i.ignored_until>clock_timestamp()))
   AND NOT EXISTS(SELECT 1 FROM issue_group_links gl WHERE gl.issue_id=i.id)
   AND NOT EXISTS(SELECT 1 FROM issue_application_triage tr WHERE tr.issue_id=i.id AND tr.snoozed_until>clock_timestamp())
   AND EXISTS(SELECT 1 FROM app_issue_team_members tm WHERE tm.team_id=dst.team_id AND tm.account_id=ANY(d.recipient_account_ids)
     AND issue_actor_current_member(a.id,tm.account_id))
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
DO $$ BEGIN RAISE EXCEPTION 'Issue response authorization is forward-only'; END $$;
-- +goose StatementEnd
