-- name: IssueApplicationReminderPolicy :one
SELECT COALESCE(p.enabled,false)::boolean AS enabled,COALESCE(p.revision,0)::bigint AS revision,
 COALESCE(p.interval_minutes,60)::integer AS interval_minutes,COALESCE(p.repeat_limit,3)::integer AS repeat_limit
FROM apps a LEFT JOIN issue_application_reminder_policies p ON p.app_id=a.id WHERE a.id=$1 AND a.account_id=$2 AND a.deleted_at IS NULL AND a.status<>'deleted';
-- name: IssueApplicationReminderPolicyPut :exec
INSERT INTO issue_application_reminder_policies(app_id,enabled,revision,interval_minutes,repeat_limit) VALUES($1,$2,$3,$4,$5)
ON CONFLICT(app_id) DO UPDATE SET enabled=excluded.enabled,revision=excluded.revision,interval_minutes=excluded.interval_minutes,repeat_limit=excluded.repeat_limit,updated_at=clock_timestamp();
-- name: IssueApplicationReminderStart :exec
INSERT INTO issue_application_triage(issue_id,revision,reminder_started_at)
SELECT sqlc.arg(issue_id),1,clock_timestamp() FROM issue_application_reminder_policies p WHERE p.app_id=sqlc.arg(app_id) AND p.enabled
ON CONFLICT(issue_id) DO UPDATE SET reminder_started_at=COALESCE(issue_application_triage.reminder_started_at,excluded.reminder_started_at);
-- name: IssueApplicationReminderApp :one
SELECT a.id FROM apps a JOIN accounts owner ON owner.id=a.account_id
JOIN issue_application_reminder_policies p ON p.app_id=a.id AND p.enabled
JOIN issue_application_slack_policies ap ON ap.app_id=a.id AND ap.enabled
JOIN LATERAL(SELECT GREATEST(tr.reminder_started_at,tr.last_reminded_at,tr.snoozed_until,p.updated_at)+p.interval_minutes*interval '1 minute' AS due_at
 FROM issue_application_triage tr JOIN app_issues i ON i.id=tr.issue_id WHERE i.app_id=a.id AND i.state='open'
 AND tr.reminder_started_at IS NOT NULL AND tr.acknowledged_at IS NULL AND tr.reminder_count<p.repeat_limit
 AND NOT EXISTS(SELECT 1 FROM issue_group_links gl WHERE gl.issue_id=i.id)
 AND GREATEST(tr.reminder_started_at,tr.last_reminded_at,tr.snoozed_until,p.updated_at)+p.interval_minutes*interval '1 minute'<=statement_timestamp()
 ORDER BY due_at,i.id LIMIT 1) due ON true
WHERE a.deleted_at IS NULL AND a.status<>'deleted' AND owner.status='active' AND owner.abuse_hold_at IS NULL AND owner.plan=ANY(sqlc.arg(entitled_plans)::text[])
ORDER BY due.due_at,a.id LIMIT 1 FOR UPDATE OF a SKIP LOCKED;
-- name: IssueApplicationReminderDue :one
SELECT i.*,tr.incident_id AS response_id,tr.reminder_count,p.revision AS reminder_policy_revision,tr.escalation_team_id AS responsibility_team_id,CASE WHEN tr.escalated_at IS NOT NULL THEN tr.escalation_team_id ELSE i.assignee_team_id END::uuid AS preferred_team_id
FROM apps a JOIN accounts owner ON owner.id=a.account_id
JOIN issue_application_reminder_policies p ON p.app_id=a.id AND p.enabled
JOIN issue_application_slack_policies ap ON ap.app_id=a.id AND ap.enabled
JOIN app_issues i ON i.app_id=a.id AND i.account_id=a.account_id JOIN issue_application_triage tr ON tr.issue_id=i.id
WHERE a.id=sqlc.arg(app_id) AND a.deleted_at IS NULL AND a.status<>'deleted' AND owner.status='active' AND owner.abuse_hold_at IS NULL AND owner.plan=ANY(sqlc.arg(entitled_plans)::text[])
AND i.state='open' AND tr.reminder_started_at IS NOT NULL AND tr.acknowledged_at IS NULL AND tr.reminder_count<p.repeat_limit
AND NOT EXISTS(SELECT 1 FROM issue_group_links gl WHERE gl.issue_id=i.id)
AND GREATEST(tr.reminder_started_at,tr.last_reminded_at,tr.snoozed_until,p.updated_at)+p.interval_minutes*interval '1 minute'<=statement_timestamp()
ORDER BY GREATEST(tr.reminder_started_at,tr.last_reminded_at,tr.snoozed_until,p.updated_at),i.id LIMIT 1;
-- name: IssueApplicationReminderRecord :exec
INSERT INTO issue_application_reminders(id,issue_id,response_id,sequence,policy_revision,outcome,responsibility_team_id) VALUES($1,$2,$3,$4,$5,$6,$7);
-- name: IssueApplicationReminderReceipt :exec
UPDATE issue_application_triage SET reminder_count=reminder_count+1,last_reminded_at=clock_timestamp(),first_reminded_at=CASE WHEN sqlc.arg(queued)::boolean THEN COALESCE(first_reminded_at,clock_timestamp()) ELSE first_reminded_at END WHERE issue_id=sqlc.arg(issue_id);
-- name: IssueApplicationReminderEnqueue :exec
INSERT INTO issue_team_slack_deliveries(id,destination_id,destination_revision,reminder_id,incident_id,channel_id,application_policy_revision,recipient_account_ids,assigned_team_id,assigned_account_id)
VALUES(sqlc.arg(id),sqlc.arg(destination_id),sqlc.arg(destination_revision),sqlc.arg(reminder_id),sqlc.arg(issue_id),sqlc.arg(channel_id),sqlc.arg(policy_revision),sqlc.arg(recipients)::uuid[],sqlc.narg(assigned_team_id),sqlc.narg(assigned_account_id));
-- name: IssueApplicationReminderRelease :exec
UPDATE issue_team_slack_destinations dst SET claim_id=NULL,claim_until=NULL WHERE dst.app_id=sqlc.arg(app_id)
AND EXISTS(SELECT 1 FROM issue_team_slack_deliveries d JOIN issue_application_reminders rem ON rem.id=d.reminder_id
 WHERE d.destination_id=dst.id AND d.claim_id=dst.claim_id AND (sqlc.narg(issue_id)::uuid IS NULL OR rem.issue_id=sqlc.narg(issue_id)));
-- name: IssueApplicationReminderCancel :exec
UPDATE issue_team_slack_deliveries d SET status='canceled',last_error='destination_unavailable',claim_id=NULL,claim_until=NULL,updated_at=clock_timestamp()
FROM issue_application_reminders rem JOIN app_issues i ON i.id=rem.issue_id
WHERE rem.id=d.reminder_id AND i.app_id=sqlc.arg(app_id) AND (sqlc.narg(issue_id)::uuid IS NULL OR i.id=sqlc.narg(issue_id)) AND d.status IN ('pending','failed','in_flight');
-- name: IssueApplicationReminderHistory :many
SELECT r.*,COALESCE(d.status,'')::text AS delivery_status,COALESCE(t.name,NULLIF(r.target_team_name,''),'')::text AS team_name
FROM issue_application_reminders r LEFT JOIN issue_team_slack_deliveries d ON d.reminder_id=r.id
LEFT JOIN issue_team_slack_destinations dst ON dst.id=d.destination_id LEFT JOIN app_issue_teams t ON t.id=dst.team_id
WHERE r.issue_id=$1 ORDER BY r.created_at DESC,r.id DESC LIMIT $2;
-- name: IssueApplicationReminderPrune :exec
DELETE FROM issue_application_reminders target WHERE target.issue_id=$1 AND target.id IN(SELECT old.id FROM issue_application_reminders old WHERE old.issue_id=$1 ORDER BY old.created_at DESC,old.id DESC OFFSET $2);
-- name: IssueApplicationReminderStopSchedules :exec
UPDATE issue_application_triage tr SET reminder_started_at=NULL,last_reminded_at=NULL,first_reminded_at=NULL FROM app_issues i WHERE i.id=tr.issue_id AND i.app_id=$1;
