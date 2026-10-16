-- name: IssueApplicationTriageScope :one
SELECT i.id,i.state,i.assignee_team_id,i.assignee_account_id,i.environment,statement_timestamp()::timestamptz AS checked_at,
 COALESCE(t.name,'')::text AS team_name,COALESCE(ac.email::text,'')::text AS assignee_name
FROM app_issues i JOIN apps a ON a.id=i.app_id AND a.account_id=i.account_id
JOIN accounts owner ON owner.id=a.account_id
LEFT JOIN app_issue_teams t ON t.id=i.assignee_team_id AND t.app_id=a.id
LEFT JOIN accounts ac ON ac.id=i.assignee_account_id
LEFT JOIN issue_application_triage current_response ON current_response.issue_id=i.id
WHERE i.id=sqlc.arg(issue_id) AND a.id=sqlc.arg(app_id) AND a.account_id=sqlc.arg(owner_account_id)
AND a.deleted_at IS NULL AND a.status<>'deleted' AND owner.plan=ANY(sqlc.arg(entitled_plans)::text[]) AND owner.abuse_hold_at IS NULL AND owner.status='active'
AND NOT EXISTS(SELECT 1 FROM issue_group_links gl WHERE gl.issue_id=i.id)
AND (a.account_id=sqlc.arg(actor_account_id)::uuid OR (
 EXISTS(SELECT 1 FROM org_memberships m WHERE m.org_id=a.org_id AND m.account_id=sqlc.arg(actor_account_id) AND m.removed_at IS NULL)
 AND (EXISTS(SELECT 1 FROM issue_application_triage tr JOIN app_issue_team_members tm ON tm.team_id=tr.escalation_team_id WHERE tr.issue_id=i.id AND tr.escalated_at IS NOT NULL AND tm.account_id=sqlc.arg(actor_account_id)) OR i.assignee_account_id=sqlc.arg(actor_account_id)::uuid OR EXISTS(
 SELECT 1 FROM app_issue_team_members tm WHERE tm.team_id=i.assignee_team_id AND tm.account_id=sqlc.arg(actor_account_id)) OR EXISTS(
 SELECT 1 FROM issue_application_slack_policies p JOIN LATERAL (
 SELECT dst.team_id FROM issue_team_slack_destinations dst
 WHERE dst.app_id=a.id AND dst.account_id=a.account_id AND dst.enabled AND dst.team_id IN((CASE WHEN current_response.escalated_at IS NOT NULL THEN current_response.escalation_team_id ELSE i.assignee_team_id END),p.fallback_team_id)
 AND EXISTS(SELECT 1 FROM app_issue_team_members member WHERE member.team_id=dst.team_id AND (member.account_id=a.account_id OR EXISTS(
 SELECT 1 FROM org_memberships m WHERE m.org_id=a.org_id AND m.account_id=member.account_id AND m.removed_at IS NULL)))
 ORDER BY CASE WHEN dst.team_id=(CASE WHEN current_response.escalated_at IS NOT NULL THEN current_response.escalation_team_id ELSE i.assignee_team_id END) THEN 0 ELSE 1 END LIMIT 1
 ) route ON true JOIN app_issue_team_members tm ON tm.team_id=route.team_id AND tm.account_id=sqlc.arg(actor_account_id)
 WHERE p.app_id=a.id AND p.enabled))));
-- name: IssueApplicationTriageGet :one
SELECT tr.*,COALESCE(ack.email::text,'')::text AS acknowledged_name,COALESCE(sleeper.email::text,'')::text AS snoozed_name,COALESCE(backup.name,'')::text AS responsible_team_name
FROM issue_application_triage tr LEFT JOIN accounts ack ON ack.id=tr.acknowledged_by LEFT JOIN accounts sleeper ON sleeper.id=tr.snoozed_by LEFT JOIN app_issue_teams backup ON backup.id=tr.escalation_team_id WHERE tr.issue_id=$1;
-- name: IssueApplicationTriagePut :exec
INSERT INTO issue_application_triage(issue_id,incident_id,revision,acknowledged_by,acknowledged_at,snoozed_until,snoozed_by,snooze_reason)
VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT(issue_id) DO UPDATE SET revision=excluded.revision,acknowledged_by=excluded.acknowledged_by,acknowledged_at=excluded.acknowledged_at,snoozed_until=excluded.snoozed_until,snoozed_by=excluded.snoozed_by,snooze_reason=excluded.snooze_reason;
-- name: IssueApplicationTriageHistory :many
SELECT h.*,COALESCE(ac.email::text,'')::text AS actor_name FROM issue_application_triage_activity h LEFT JOIN accounts ac ON ac.id=h.actor_id
WHERE h.issue_id=$1 ORDER BY h.revision DESC LIMIT $2;
-- name: IssueApplicationTriageAudit :exec
INSERT INTO issue_application_triage_activity(issue_id,incident_id,revision,actor_id,action,snoozed_until,reason) VALUES($1,$2,$3,$4,$5,$6,$7);
-- name: IssueApplicationTriagePrune :exec
DELETE FROM issue_application_triage_activity h WHERE h.issue_id=$1 AND h.revision IN(SELECT old.revision FROM issue_application_triage_activity old WHERE old.issue_id=$1 ORDER BY old.revision DESC OFFSET $2);
-- name: IssueApplicationTriageReset :one
INSERT INTO issue_application_triage(issue_id,revision) VALUES($1,1) ON CONFLICT(issue_id) DO UPDATE SET incident_id=gen_random_uuid(),revision=issue_application_triage.revision+1,acknowledged_by=NULL,acknowledged_at=NULL,snoozed_until=NULL,snoozed_by=NULL,snooze_reason='',reminder_started_at=NULL,last_reminded_at=NULL,reminder_count=0,first_reminded_at=NULL,escalation_attempted_at=NULL,escalated_at=NULL,escalation_team_id=NULL RETURNING *;
-- name: IssueApplicationTriageSnoozed :one
SELECT EXISTS(SELECT 1 FROM issue_application_triage WHERE issue_id=$1 AND snoozed_until>statement_timestamp());
-- name: IssueApplicationTriageReleaseLease :exec
UPDATE issue_team_slack_destinations dst SET claim_id=NULL,claim_until=NULL WHERE EXISTS(
 SELECT 1 FROM issue_team_slack_deliveries d JOIN issue_slack_delivery_sources src ON src.delivery_id=d.id
 WHERE src.issue_id=$1 AND d.notification_id IS NULL AND d.destination_id=dst.id AND d.claim_id=dst.claim_id);
-- name: IssueApplicationTriageCancelSlack :exec
UPDATE issue_team_slack_deliveries d SET status='canceled',last_error='issue_snoozed',claim_id=NULL,claim_until=NULL,updated_at=clock_timestamp()
FROM issue_slack_delivery_sources src WHERE src.delivery_id=d.id AND src.issue_id=$1 AND d.notification_id IS NULL AND d.status IN ('pending','failed','in_flight');
