-- name: IssueApplicationTriageScope :one
SELECT i.id,i.state,i.assignee_team_id,i.assignee_account_id,i.environment,statement_timestamp()::timestamptz AS checked_at,
 COALESCE(t.name,'')::text AS team_name,COALESCE(ac.email::text,'')::text AS assignee_name
FROM issue_application_response_access access JOIN app_issues i ON i.id=access.issue_id
LEFT JOIN app_issue_teams t ON t.id=i.assignee_team_id AND t.app_id=i.app_id
LEFT JOIN accounts ac ON ac.id=i.assignee_account_id
WHERE access.issue_id=sqlc.arg(issue_id) AND access.app_id=sqlc.arg(app_id) AND access.owner_account_id=sqlc.arg(owner_account_id)
AND access.actor_account_id=sqlc.arg(actor_account_id) AND access.owner_plan=ANY(sqlc.arg(entitled_plans)::text[]);
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
-- name: IssueApplicationResponseInbox :many
SELECT i.id,i.environment,COALESCE(tr.revision,0)::bigint AS revision,tr.acknowledged_at,tr.snoozed_until,tr.escalated_at,
 access.responsible_team_id::uuid AS responsible_team_id,COALESCE(team.name,'')::text AS responsible_team_name,
 access.routed_team_id,COALESCE(routed.name,'')::text AS routed_team_name,
 COALESCE(latest.outcome,'')::text AS outcome,COALESCE(latest.delivery_status,'')::text AS delivery_status,
 statement_timestamp()::timestamptz AS checked_at,
 CASE WHEN ap.enabled AND rp.enabled AND tr.acknowledged_at IS NULL AND tr.reminder_started_at IS NOT NULL AND tr.reminder_count<rp.repeat_limit
 THEN GREATEST(tr.reminder_started_at,tr.last_reminded_at,tr.snoozed_until,rp.updated_at)+rp.interval_minutes*interval '1 minute' END::timestamptz AS reminder_due_at,
 CASE WHEN ap.enabled AND rp.enabled AND ep.enabled AND tr.acknowledged_at IS NULL AND tr.first_reminded_at IS NOT NULL AND tr.escalation_attempted_at IS NULL
 THEN GREATEST(tr.first_reminded_at,tr.snoozed_until,rp.updated_at,ep.updated_at)+ep.delay_minutes*interval '1 minute' END::timestamptz AS escalation_due_at
FROM issue_application_response_access access JOIN app_issues i ON i.id=access.issue_id
LEFT JOIN issue_application_triage tr ON tr.issue_id=i.id
LEFT JOIN app_issue_teams team ON team.id=access.responsible_team_id
LEFT JOIN app_issue_teams routed ON routed.id=access.routed_team_id
LEFT JOIN issue_application_slack_policies ap ON ap.app_id=i.app_id
LEFT JOIN issue_application_reminder_policies rp ON rp.app_id=i.app_id
LEFT JOIN issue_application_escalation_policies ep ON ep.app_id=i.app_id
LEFT JOIN LATERAL (
 SELECT rem.outcome,COALESCE(d.status,'')::text AS delivery_status FROM issue_application_reminders rem
 LEFT JOIN issue_team_slack_deliveries d ON d.reminder_id=rem.id
 WHERE rem.issue_id=i.id AND rem.response_id=tr.incident_id ORDER BY rem.created_at DESC,rem.id DESC LIMIT 1
) latest ON true
WHERE access.app_id=sqlc.arg(app_id) AND access.owner_account_id=sqlc.arg(owner_account_id)
AND access.actor_account_id=sqlc.arg(actor_account_id) AND access.owner_plan=ANY(sqlc.arg(entitled_plans)::text[])
AND i.state='open' AND (sqlc.narg(after_id)::uuid IS NULL OR i.id>sqlc.narg(after_id))
AND (NOT sqlc.arg(my_teams)::boolean OR EXISTS(SELECT 1 FROM app_issue_team_members tm WHERE tm.account_id=access.actor_account_id AND tm.team_id IN(access.responsible_team_id,access.routed_team_id)))
AND (sqlc.arg(response_filter)::text='all'
 OR (sqlc.arg(response_filter)='unacknowledged' AND tr.acknowledged_at IS NULL AND (tr.snoozed_until IS NULL OR tr.snoozed_until<=statement_timestamp()))
 OR (sqlc.arg(response_filter)='snoozed' AND tr.snoozed_until>statement_timestamp())
 OR (sqlc.arg(response_filter)='resurfaced' AND tr.snoozed_until<=statement_timestamp() AND tr.acknowledged_at IS NULL)
 OR (sqlc.arg(response_filter)='escalated' AND tr.escalated_at IS NOT NULL)
 OR (sqlc.arg(response_filter)='delivery_failed' AND (latest.outcome IN('unrouted','queue_full') OR latest.delivery_status IN('failed','dead'))))
ORDER BY i.id LIMIT sqlc.arg(page_limit);
