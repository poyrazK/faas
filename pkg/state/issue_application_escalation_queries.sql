-- name: IssueApplicationEscalationPolicy :one
SELECT COALESCE(p.enabled,false)::boolean AS enabled,COALESCE(p.revision,0)::bigint AS revision,
 COALESCE(p.delay_minutes,1440)::integer AS delay_minutes,p.team_id,COALESCE(t.name,'')::text AS team_name
FROM apps a LEFT JOIN issue_application_escalation_policies p ON p.app_id=a.id LEFT JOIN app_issue_teams t ON t.id=p.team_id AND t.app_id=a.id
WHERE a.id=$1 AND a.account_id=$2 AND a.deleted_at IS NULL AND a.status<>'deleted';
-- name: IssueApplicationEscalationPolicyPut :exec
INSERT INTO issue_application_escalation_policies(app_id,enabled,revision,delay_minutes,team_id) VALUES($1,$2,$3,$4,$5)
ON CONFLICT(app_id) DO UPDATE SET enabled=excluded.enabled,revision=excluded.revision,delay_minutes=excluded.delay_minutes,team_id=excluded.team_id,updated_at=clock_timestamp();
-- name: IssueApplicationEscalationApp :one
SELECT a.id FROM apps a JOIN accounts owner ON owner.id=a.account_id
JOIN issue_application_reminder_policies rp ON rp.app_id=a.id AND rp.enabled
JOIN issue_application_slack_policies ap ON ap.app_id=a.id AND ap.enabled
JOIN issue_application_escalation_policies ep ON ep.app_id=a.id AND ep.enabled
JOIN LATERAL(SELECT GREATEST(tr.first_reminded_at,tr.snoozed_until,rp.updated_at,ep.updated_at)+ep.delay_minutes*interval '1 minute' AS due_at
 FROM issue_application_triage tr JOIN app_issues i ON i.id=tr.issue_id WHERE i.app_id=a.id AND i.account_id=a.account_id AND i.state='open'
 AND tr.first_reminded_at IS NOT NULL AND tr.acknowledged_at IS NULL AND tr.escalation_attempted_at IS NULL
 AND NOT EXISTS(SELECT 1 FROM issue_group_links gl WHERE gl.issue_id=i.id)
 AND GREATEST(tr.first_reminded_at,tr.snoozed_until,rp.updated_at,ep.updated_at)+ep.delay_minutes*interval '1 minute'<=statement_timestamp()
 ORDER BY due_at,i.id LIMIT 1) due ON true
WHERE a.deleted_at IS NULL AND a.status<>'deleted' AND owner.status='active' AND owner.abuse_hold_at IS NULL AND owner.plan=ANY(sqlc.arg(entitled_plans)::text[])
ORDER BY due.due_at,a.id LIMIT 1 FOR UPDATE OF a SKIP LOCKED;
-- name: IssueApplicationEscalationDue :one
SELECT i.*,tr.incident_id AS response_id,rp.revision AS reminder_policy_revision,ep.revision AS escalation_policy_revision,ep.team_id AS backup_team_id,COALESCE(t.name,'')::text AS backup_team_name
FROM apps a JOIN accounts owner ON owner.id=a.account_id
JOIN issue_application_reminder_policies rp ON rp.app_id=a.id AND rp.enabled
JOIN issue_application_slack_policies ap ON ap.app_id=a.id AND ap.enabled
JOIN issue_application_escalation_policies ep ON ep.app_id=a.id AND ep.enabled
JOIN app_issues i ON i.app_id=a.id AND i.account_id=a.account_id JOIN issue_application_triage tr ON tr.issue_id=i.id
LEFT JOIN app_issue_teams t ON t.id=ep.team_id AND t.app_id=a.id
WHERE a.id=sqlc.arg(app_id) AND a.deleted_at IS NULL AND a.status<>'deleted' AND owner.status='active' AND owner.abuse_hold_at IS NULL AND owner.plan=ANY(sqlc.arg(entitled_plans)::text[])
AND i.state='open' AND tr.first_reminded_at IS NOT NULL AND tr.acknowledged_at IS NULL AND tr.escalation_attempted_at IS NULL
AND NOT EXISTS(SELECT 1 FROM issue_group_links gl WHERE gl.issue_id=i.id)
AND GREATEST(tr.first_reminded_at,tr.snoozed_until,rp.updated_at,ep.updated_at)+ep.delay_minutes*interval '1 minute'<=statement_timestamp()
ORDER BY GREATEST(tr.first_reminded_at,tr.snoozed_until,rp.updated_at,ep.updated_at),i.id LIMIT 1;
-- name: IssueApplicationEscalationRecord :exec
INSERT INTO issue_application_reminders(id,issue_id,response_id,kind,sequence,policy_revision,escalation_policy_revision,responsibility_team_id,target_team_name,outcome)
VALUES($1,$2,$3,'escalation',0,$4,$5,$6,$7,$8);
-- name: IssueApplicationEscalationReceipt :exec
UPDATE issue_application_triage SET escalation_attempted_at=clock_timestamp(),
 escalated_at=CASE WHEN sqlc.arg(queued)::boolean THEN clock_timestamp() ELSE escalated_at END,
 last_reminded_at=CASE WHEN sqlc.arg(queued)::boolean THEN clock_timestamp() ELSE last_reminded_at END,
 escalation_team_id=CASE WHEN sqlc.arg(queued)::boolean THEN sqlc.narg(team_id)::uuid ELSE escalation_team_id END
WHERE issue_id=sqlc.arg(issue_id);
-- name: IssueApplicationEscalationRelease :exec
UPDATE issue_team_slack_destinations dst SET claim_id=NULL,claim_until=NULL WHERE dst.app_id=sqlc.arg(app_id)
AND EXISTS(SELECT 1 FROM issue_team_slack_deliveries d JOIN issue_application_reminders rem ON rem.id=d.reminder_id
 WHERE d.destination_id=dst.id AND d.claim_id=dst.claim_id AND rem.kind='escalation');
-- name: IssueApplicationEscalationCancel :exec
UPDATE issue_team_slack_deliveries d SET status='canceled',last_error='destination_unavailable',claim_id=NULL,claim_until=NULL,updated_at=clock_timestamp()
FROM issue_application_reminders rem JOIN app_issues i ON i.id=rem.issue_id
WHERE rem.id=d.reminder_id AND i.app_id=sqlc.arg(app_id) AND rem.kind='escalation' AND d.status IN ('pending','failed','in_flight');
