-- name: IssueTrackerAutoSyncGet :one
SELECT * FROM issue_tracker_auto_sync WHERE link_id=$1;
-- name: IssueTrackerAutoSyncEnable :one
INSERT INTO issue_tracker_auto_sync(link_id,app_id,account_id,source_issue_id,binding_fingerprint,baseline,source_state,linear_open_state_id,linear_resolved_state_id,direction,source_revision,observed_state)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
ON CONFLICT(link_id) DO UPDATE SET direction=excluded.direction,source_revision=excluded.source_revision,pending_source_revision=0,observed_state=excluded.observed_state,app_id=excluded.app_id,account_id=excluded.account_id,enabled=true,revision=issue_tracker_auto_sync.revision+1,source_issue_id=excluded.source_issue_id,
 linear_open_state_id=excluded.linear_open_state_id,linear_resolved_state_id=excluded.linear_resolved_state_id, binding_fingerprint=excluded.binding_fingerprint,baseline=excluded.baseline,source_state=excluded.source_state,pending_state='',outcome='matched',reason='',claim_id=NULL,claim_until=NULL,next_check_at=now(),updated_at=now()
RETURNING *;
-- name: IssueTrackerAutoSyncDisable :one
UPDATE issue_tracker_auto_sync SET enabled=false,revision=revision+1,outcome='disabled',reason='',pending_state='',pending_source_revision=0,claim_id=NULL,claim_until=NULL,updated_at=now() WHERE link_id=$1 RETURNING *;
-- name: IssueTrackerAutoSyncClaim :one
WITH due AS (
 SELECT link_id FROM issue_tracker_auto_sync WHERE enabled AND outcome IN ('matched','pending','applied','error')
 AND next_check_at<=now() AND (claim_until IS NULL OR claim_until<=now())
 ORDER BY next_check_at,link_id FOR UPDATE SKIP LOCKED LIMIT 1
)
UPDATE issue_tracker_auto_sync p SET claim_id=sqlc.arg(claim_id),claim_until=sqlc.arg(claim_until),next_check_at=sqlc.arg(claim_until)
FROM due WHERE p.link_id=due.link_id RETURNING p.*;
-- name: IssueTrackerAutoSyncPending :one
UPDATE issue_tracker_auto_sync SET pending_source_revision=sqlc.arg(pending_source_revision),pending_state=sqlc.arg(pending_state),outcome='pending',updated_at=now()
WHERE link_id=sqlc.arg(link_id) AND enabled AND revision=sqlc.arg(revision) AND claim_id=sqlc.arg(claim_id) AND claim_until>now() RETURNING *;
-- name: IssueTrackerAutoSyncFinish :one
UPDATE issue_tracker_auto_sync SET source_revision=sqlc.arg(source_revision),pending_source_revision=sqlc.arg(pending_source_revision),observed_state=sqlc.arg(observed_state),baseline=sqlc.arg(baseline),source_state=sqlc.arg(source_state),pending_state=sqlc.arg(pending_state),
 last_successful_at=CASE WHEN sqlc.arg(outcome)::text IN ('matched','applied') AND sqlc.arg(pending_state)::text='' THEN clock_timestamp() ELSE last_successful_at END,
 outcome=sqlc.arg(outcome),reason=sqlc.arg(reason),last_checked_at=now(),next_check_at=sqlc.arg(next_check_at),claim_id=NULL,claim_until=NULL,updated_at=now()
WHERE link_id=sqlc.arg(link_id) AND enabled AND revision=sqlc.arg(revision) AND claim_id=sqlc.arg(claim_id) AND claim_until>clock_timestamp() RETURNING *;

-- name: IssueTrackerInboundGuard :one
SELECT g.revision FROM issue_tracker_inbound_guards g JOIN apps a ON a.id=g.app_id
WHERE g.app_id=$1 AND a.account_id=$2 AND a.deleted_at IS NULL AND a.status<>'deleted';
-- name: IssueTrackerInboundGuardLock :one
SELECT revision FROM issue_tracker_inbound_guards WHERE app_id=$1 FOR UPDATE;
-- name: IssueTrackerInboundPolicyLock :one
SELECT * FROM issue_tracker_auto_sync WHERE link_id=$1 FOR UPDATE;
-- name: IssueTrackerInboundApplied :exec
UPDATE issue_tracker_auto_sync SET enabled=false,revision=revision+1,baseline=$2,source_state=$3,outcome='disabled',reason='',pending_state='',pending_source_revision=0,claim_id=NULL,claim_until=NULL,last_checked_at=now(),updated_at=now() WHERE link_id=$1;
-- name: IssueTrackerInboundDeployments :many
SELECT d.id,d.commit_sha,d.created_at,d.status,
 COALESCE(i.last_seen_at,'epoch'::timestamptz)::timestamptz AS last_seen_at,
 COALESCE((SELECT sum(ir.event_count)::bigint FROM issue_releases ir
 WHERE ir.deployment_id=d.id AND ir.issue_id IN (SELECT i.id UNION ALL SELECT gl.issue_id FROM issue_group_links gl WHERE gl.canonical_issue_id=i.id)),0)::bigint AS observed_events
FROM deployments d JOIN apps a ON a.id=d.app_id
JOIN issue_group_rollups i ON i.app_id=a.id AND i.id=sqlc.arg(issue_id)
WHERE a.id=sqlc.arg(app_id) AND a.account_id=sqlc.arg(account_id) AND a.deleted_at IS NULL AND a.status<>'deleted'
AND (i.environment='application' OR EXISTS (
 SELECT 1 FROM project_release_members rm JOIN project_release_sets rs ON rs.id=rm.release_id
 WHERE rm.app_id=a.id AND rm.deployment_id=d.id AND rs.environment_slug=i.environment AND rs.account_id=a.account_id))
ORDER BY d.created_at DESC,d.id DESC LIMIT sqlc.arg(page_limit);

-- name: IssueTrackerSourceRevision :one
SELECT GREATEST(g.source_revision,COALESCE((SELECT max(s.revision) FROM issue_tracker_source_guards s
 WHERE s.issue_id IN (SELECT i.id UNION ALL SELECT gl.issue_id FROM issue_group_links gl WHERE gl.canonical_issue_id=i.id)),0))::bigint AS source_revision FROM issue_tracker_inbound_guards g JOIN apps a ON a.id=g.app_id
JOIN app_issues i ON i.app_id=a.id AND i.id=sqlc.arg(issue_id)
WHERE g.app_id=sqlc.arg(app_id) AND a.account_id=sqlc.arg(account_id) AND a.deleted_at IS NULL AND a.status<>'deleted';
-- name: IssueTrackerSourceRevisionLock :one
-- Acquire the fence only. Read the effective group revision in a new statement
-- after this lock returns, so a wait cannot preserve an older subquery snapshot.
SELECT g.source_revision FROM issue_tracker_inbound_guards g JOIN app_issues i ON i.app_id=g.app_id AND i.id=sqlc.arg(issue_id)
WHERE g.app_id=sqlc.arg(app_id) FOR UPDATE OF g;
-- name: IssueTrackerAutoReopenComplete :one
UPDATE issue_tracker_auto_sync SET baseline=sqlc.arg(baseline),source_state='open',source_revision=sqlc.arg(source_revision),
 last_successful_at=clock_timestamp(),observed_state='open',pending_state='',pending_source_revision=0,outcome='applied',reason='',claim_id=NULL,claim_until=NULL,last_checked_at=clock_timestamp(),next_check_at=sqlc.arg(next_check_at),updated_at=clock_timestamp()
WHERE link_id=sqlc.arg(link_id) AND enabled AND revision=sqlc.arg(revision) AND claim_id=sqlc.arg(claim_id) AND claim_until>clock_timestamp() RETURNING *;

-- name: IssueTrackerReviewQueue :many
SELECT p.link_id,p.direction,p.outcome,p.reason,p.pending_state,p.observed_state,p.source_state,p.revision,
 p.last_checked_at,p.last_successful_at,p.next_check_at,p.claim_until,p.updated_at,
 l.provider,l.external_key,l.title AS ticket_title,l.created_at,
 i.id AS issue_id,i.title AS issue_title,i.environment,i.state AS issue_state,
 (p.source_issue_id<>i.id)::boolean AS regrouped
FROM issue_tracker_auto_sync p JOIN apps a ON a.id=p.app_id AND a.account_id=p.account_id
JOIN issue_tracker_links l ON l.id=p.link_id
JOIN issue_group_assignments ga ON ga.original_issue_id=l.issue_id
JOIN app_issues i ON i.id=ga.canonical_issue_id AND i.app_id=a.id
WHERE a.id=sqlc.arg(app_id) AND a.account_id=sqlc.arg(account_id) AND a.deleted_at IS NULL AND a.status<>'deleted'
 AND p.enabled AND (p.reason='fixing_deployment_required' OR p.outcome IN ('blocked','conflict','error') OR p.pending_state<>'')
 AND (sqlc.arg(category)::text='all'
  OR (sqlc.arg(category)='closures' AND p.reason='fixing_deployment_required')
  OR (sqlc.arg(category)='conflicts' AND p.outcome='conflict')
  OR (sqlc.arg(category)='pending' AND p.pending_state<>'')
  OR (sqlc.arg(category)='paused' AND p.outcome IN ('blocked','error') AND p.reason<>'fixing_deployment_required'))
 AND (sqlc.narg(cursor_time)::timestamptz IS NULL OR (l.created_at,l.id)<(sqlc.narg(cursor_time),sqlc.narg(cursor_id)::uuid))
ORDER BY l.created_at DESC,l.id DESC LIMIT sqlc.arg(page_limit);

-- name: IssueTrackerSyncHistoryAdd :exec
INSERT INTO issue_tracker_sync_history(link_id,app_id,account_id,source_issue_id,policy_revision,claim_id,event_type,direction,outcome,reason,source_state,observed_state,pending_state)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13);
-- name: IssueTrackerSyncHistoryPrune :exec
DELETE FROM issue_tracker_sync_history h WHERE h.link_id=sqlc.arg(link_id) AND h.id IN (
 SELECT old.id FROM issue_tracker_sync_history old WHERE old.link_id=sqlc.arg(link_id)
 ORDER BY old.created_at DESC,old.id DESC OFFSET sqlc.arg(retained_count));
-- name: IssueTrackerSyncHistoryList :many
SELECT * FROM issue_tracker_sync_history WHERE link_id=$1 AND account_id=$2
ORDER BY created_at DESC,id DESC LIMIT $3;

-- name: IssueTrackerNotificationPolicyGet :one
SELECT p.* FROM issue_tracker_sync_notification_policies p JOIN apps a ON a.id=p.app_id AND a.account_id=p.account_id
WHERE p.app_id=$1 AND a.account_id=$2 AND a.deleted_at IS NULL AND a.status<>'deleted';
-- name: IssueTrackerNotificationPolicyPut :one
INSERT INTO issue_tracker_sync_notification_policies(app_id,account_id,enabled,failure_threshold)
VALUES($1,$2,$3,$4) ON CONFLICT(app_id) DO UPDATE SET account_id=excluded.account_id,enabled=excluded.enabled,failure_threshold=excluded.failure_threshold,revision=issue_tracker_sync_notification_policies.revision+1,updated_at=clock_timestamp() RETURNING *;
-- name: IssueTrackerIncidentGet :one
SELECT * FROM issue_tracker_sync_incidents WHERE link_id=$1;
-- name: IssueTrackerIncidentPut :exec
INSERT INTO issue_tracker_sync_incidents(link_id,settings_revision,failures,incident_id,announced_kind,issue_id,team_id,recipient_account_ids,opened_at)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,CASE WHEN $4::uuid IS NOT NULL THEN clock_timestamp() ELSE NULL END) ON CONFLICT(link_id) DO UPDATE SET settings_revision=excluded.settings_revision,failures=excluded.failures,incident_id=excluded.incident_id,announced_kind=excluded.announced_kind,issue_id=excluded.issue_id,team_id=excluded.team_id,recipient_account_ids=excluded.recipient_account_ids,
 opened_at=CASE WHEN issue_tracker_sync_incidents.incident_id=excluded.incident_id THEN issue_tracker_sync_incidents.opened_at WHEN excluded.incident_id IS NOT NULL THEN clock_timestamp() ELSE NULL END,
 reminder_count=CASE WHEN issue_tracker_sync_incidents.incident_id=excluded.incident_id THEN issue_tracker_sync_incidents.reminder_count ELSE 0 END,
 last_reminded_at=CASE WHEN issue_tracker_sync_incidents.incident_id=excluded.incident_id THEN issue_tracker_sync_incidents.last_reminded_at ELSE NULL END,
 first_reminded_at=CASE WHEN issue_tracker_sync_incidents.incident_id=excluded.incident_id THEN issue_tracker_sync_incidents.first_reminded_at ELSE NULL END,
 escalated_at=CASE WHEN issue_tracker_sync_incidents.incident_id=excluded.incident_id THEN issue_tracker_sync_incidents.escalated_at ELSE NULL END,
 escalation_team_id=CASE WHEN issue_tracker_sync_incidents.incident_id=excluded.incident_id THEN issue_tracker_sync_incidents.escalation_team_id ELSE NULL END;
-- name: IssueTrackerNotificationTarget :one
SELECT i.id,i.assignee_account_id,i.assignee_team_id,a.slug FROM issue_tracker_links l
JOIN issue_group_assignments ga ON ga.original_issue_id=l.issue_id JOIN app_issues i ON i.id=ga.canonical_issue_id
JOIN apps a ON a.id=i.app_id WHERE l.id=sqlc.arg(link_id) AND a.id=sqlc.arg(app_id) AND a.account_id=sqlc.arg(account_id) AND a.deleted_at IS NULL AND a.status<>'deleted';
-- name: IssueTrackerNotificationRecipients :many
SELECT c.id FROM accounts c JOIN apps a ON a.id=sqlc.arg(app_id)
WHERE (c.id=a.account_id OR EXISTS(SELECT 1 FROM org_memberships m WHERE m.org_id=a.org_id AND m.account_id=c.id AND m.removed_at IS NULL))
AND (c.id=sqlc.narg(assignee_account_id)::uuid OR EXISTS(SELECT 1 FROM app_issue_team_members tm WHERE tm.team_id=sqlc.narg(team_id)::uuid AND tm.account_id=c.id))
ORDER BY c.id;
-- name: IssueTrackerNotificationInsert :execrows
INSERT INTO issue_tracker_sync_notifications(id,link_id,app_id,account_id,issue_id,incident_id,kind,outcome,reason,team_id,recipient_account_ids,payload,reminder_sequence)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13) ON CONFLICT(incident_id,kind,reminder_sequence) DO NOTHING;
-- name: IssueTrackerNotificationPrune :exec
DELETE FROM issue_tracker_sync_notifications n WHERE n.link_id=sqlc.arg(link_id) AND n.id IN (
 SELECT old.id FROM issue_tracker_sync_notifications old WHERE old.link_id=sqlc.arg(link_id) ORDER BY old.created_at DESC,old.id DESC OFFSET sqlc.arg(retained_count));
-- name: IssueTrackerNotificationList :many
WITH notices AS (SELECT n.id,n.link_id,n.kind,n.reminder_sequence,n.outcome,n.reason,n.created_at,n.team_id,COALESCE(t.name,'')::text AS team_name,
 i.id AS issue_id,i.title AS issue_title,
 COALESCE(tr.revision,0)::bigint AS triage_revision,tr.acknowledged_by,tr.claimed_by,
 COALESCE(ack.email::text,'')::text AS acknowledged_name, COALESCE(claim.email::text,'')::text AS claimed_name,
 tr.snoozed_until,tr.snoozed_by,tr.snooze_reason,COALESCE(sleeper.email::text,'')::text AS snoozed_name,
 COALESCE(c.incident_id=n.incident_id AND p.enabled AND p.revision=c.settings_revision,false)::boolean AS active
FROM issue_tracker_sync_notifications n JOIN apps a ON a.id=n.app_id AND a.account_id=n.account_id
JOIN issue_tracker_links l ON l.id=n.link_id JOIN issue_group_assignments ga ON ga.original_issue_id=l.issue_id JOIN app_issues i ON i.id=ga.canonical_issue_id AND i.app_id=a.id
LEFT JOIN app_issue_teams t ON t.id=n.team_id AND t.app_id=a.id
LEFT JOIN issue_tracker_sync_triage tr ON tr.incident_id=n.incident_id
LEFT JOIN issue_tracker_sync_incidents c ON c.link_id=n.link_id
LEFT JOIN issue_tracker_sync_notification_policies p ON p.app_id=a.id AND p.account_id=a.account_id
LEFT JOIN accounts sleeper ON sleeper.id=tr.snoozed_by
LEFT JOIN accounts ack ON ack.id=tr.acknowledged_by
LEFT JOIN accounts claim ON claim.id=tr.claimed_by
WHERE a.id=sqlc.arg(app_id) AND a.account_id=sqlc.arg(owner_account_id) AND a.deleted_at IS NULL AND a.status<>'deleted'
AND (sqlc.narg(team_filter)::uuid IS NULL OR n.team_id=sqlc.narg(team_filter))
AND (a.account_id=sqlc.arg(actor_account_id)::uuid OR (
 sqlc.arg(actor_account_id)::uuid=ANY(n.recipient_account_ids)
 AND EXISTS(SELECT 1 FROM org_memberships m WHERE m.org_id=a.org_id AND m.account_id=sqlc.arg(actor_account_id) AND m.removed_at IS NULL)
 AND (n.team_id IS NULL OR EXISTS(SELECT 1 FROM app_issue_team_members tm WHERE tm.team_id=n.team_id AND tm.account_id=sqlc.arg(actor_account_id)))))
), inbox AS (SELECT notices.*,
 COALESCE(active AND snoozed_until>statement_timestamp(),false)::boolean AS snoozed,
 COALESCE(active AND snoozed_until<=statement_timestamp(),false)::boolean AS resurfaced,
 (CASE WHEN active AND snoozed_until<=statement_timestamp() THEN GREATEST(created_at,snoozed_until) ELSE created_at END)::timestamptz AS sort_at
 FROM notices)
SELECT * FROM inbox
WHERE (sqlc.arg(inbox_view)::text='all' OR (sqlc.arg(inbox_view)='snoozed' AND snoozed) OR (sqlc.arg(inbox_view)='active' AND NOT snoozed))
AND (sqlc.narg(cursor_time)::timestamptz IS NULL OR (sort_at,id)<(sqlc.narg(cursor_time),sqlc.narg(cursor_id)::uuid))
ORDER BY sort_at DESC,id DESC LIMIT sqlc.arg(page_limit);
-- name: IssueTrackerNotificationRecoveryRecipients :many
SELECT c.id FROM accounts c JOIN apps a ON a.id=sqlc.arg(app_id)
WHERE c.id=ANY(sqlc.arg(recipient_ids)::uuid[])
AND (c.id=a.account_id OR EXISTS(SELECT 1 FROM org_memberships m WHERE m.org_id=a.org_id AND m.account_id=c.id AND m.removed_at IS NULL))
AND (sqlc.narg(team_id)::uuid IS NULL OR EXISTS(SELECT 1 FROM app_issue_team_members tm JOIN app_issue_teams t ON t.id=tm.team_id AND t.app_id=a.id WHERE tm.team_id=sqlc.narg(team_id) AND tm.account_id=c.id)) ORDER BY c.id;
-- name: IssueTrackerNotificationCanRead :one
SELECT EXISTS(SELECT 1 FROM apps a WHERE a.id=sqlc.arg(app_id) AND a.account_id=sqlc.arg(owner_account_id)
AND a.deleted_at IS NULL AND a.status<>'deleted' AND (a.account_id=sqlc.arg(actor_account_id)::uuid OR EXISTS(
 SELECT 1 FROM issue_tracker_sync_notifications n WHERE n.app_id=a.id AND n.account_id=a.account_id
 AND (sqlc.narg(link_id)::uuid IS NULL OR n.link_id=sqlc.narg(link_id))
 AND sqlc.arg(actor_account_id)::uuid=ANY(n.recipient_account_ids)
 AND EXISTS(SELECT 1 FROM org_memberships m WHERE m.org_id=a.org_id AND m.account_id=sqlc.arg(actor_account_id) AND m.removed_at IS NULL)
 AND (n.team_id IS NULL OR EXISTS(SELECT 1 FROM app_issue_team_members tm WHERE tm.team_id=n.team_id AND tm.account_id=sqlc.arg(actor_account_id))))));

-- name: IssueTrackerTriageScope :one
SELECT n.incident_id,n.link_id,statement_timestamp()::timestamptz AS checked_at,
 COALESCE(c.incident_id=n.incident_id AND c.escalated_at IS NOT NULL,false)::boolean AS escalated,
 (SELECT t.id FROM app_issue_teams t WHERE t.id=c.escalation_team_id AND c.incident_id=n.incident_id AND t.app_id=a.id AND EXISTS(
 SELECT 1 FROM app_issue_team_members tm WHERE tm.team_id=t.id AND (tm.account_id=a.account_id OR EXISTS(SELECT 1 FROM org_memberships m WHERE m.org_id=a.org_id AND m.account_id=tm.account_id AND m.removed_at IS NULL))))::uuid AS responsible_team_id,
 COALESCE((SELECT t.name FROM app_issue_teams t WHERE t.id=c.escalation_team_id AND c.incident_id=n.incident_id AND t.app_id=a.id AND EXISTS(
 SELECT 1 FROM app_issue_team_members tm WHERE tm.team_id=t.id AND (tm.account_id=a.account_id OR EXISTS(SELECT 1 FROM org_memberships m WHERE m.org_id=a.org_id AND m.account_id=tm.account_id AND m.removed_at IS NULL)))), '')::text AS responsible_team_name,
 COALESCE(c.incident_id=n.incident_id AND p.enabled AND p.revision=c.settings_revision,false)::boolean AS active
FROM issue_tracker_sync_notifications n JOIN apps a ON a.id=n.app_id AND a.account_id=n.account_id
LEFT JOIN issue_tracker_sync_incidents c ON c.link_id=n.link_id
LEFT JOIN issue_tracker_sync_notification_policies p ON p.app_id=a.id AND p.account_id=a.account_id
WHERE n.id=sqlc.arg(notification_id) AND a.id=sqlc.arg(app_id) AND a.account_id=sqlc.arg(owner_account_id)
AND a.deleted_at IS NULL AND a.status<>'deleted'
AND (a.account_id=sqlc.arg(actor_account_id)::uuid OR (
 sqlc.arg(actor_account_id)::uuid=ANY(n.recipient_account_ids)
 AND EXISTS(SELECT 1 FROM org_memberships m WHERE m.org_id=a.org_id AND m.account_id=sqlc.arg(actor_account_id) AND m.removed_at IS NULL)
 AND (n.team_id IS NULL OR EXISTS(SELECT 1 FROM app_issue_team_members tm WHERE tm.team_id=n.team_id AND tm.account_id=sqlc.arg(actor_account_id)))));
-- name: IssueTrackerTriageGet :one
SELECT tr.*, COALESCE(ack.email::text,'')::text AS acknowledged_name, COALESCE(claim.email::text,'')::text AS claimed_name,COALESCE(sleeper.email::text,'')::text AS snoozed_name
FROM issue_tracker_sync_triage tr LEFT JOIN accounts ack ON ack.id=tr.acknowledged_by LEFT JOIN accounts claim ON claim.id=tr.claimed_by
LEFT JOIN accounts sleeper ON sleeper.id=tr.snoozed_by
WHERE tr.incident_id=$1;
-- name: IssueTrackerTriagePut :exec
INSERT INTO issue_tracker_sync_triage(incident_id,link_id,revision,acknowledged_by,claimed_by,snoozed_until,snoozed_by,snooze_reason) VALUES($1,$2,$3,$4,$5,$6,$7,$8)
ON CONFLICT(incident_id) DO UPDATE SET revision=excluded.revision,acknowledged_by=excluded.acknowledged_by,claimed_by=excluded.claimed_by,snoozed_until=excluded.snoozed_until,snoozed_by=excluded.snoozed_by,snooze_reason=excluded.snooze_reason;
-- name: IssueTrackerTriageActivityAdd :exec
INSERT INTO issue_tracker_sync_triage_activity(id,incident_id,actor_id,action,revision,snoozed_until,reason) VALUES($1,$2,$3,$4,$5,$6,$7);
-- name: IssueTrackerTriageActivityList :many
SELECT h.*,COALESCE(a.email::text,'')::text AS actor_name FROM issue_tracker_sync_triage_activity h LEFT JOIN accounts a ON a.id=h.actor_id
WHERE h.incident_id=$1 ORDER BY h.revision DESC LIMIT $2;
-- name: IssueTrackerTriageActivityPrune :exec
DELETE FROM issue_tracker_sync_triage_activity h WHERE h.incident_id=sqlc.arg(incident_id) AND h.id IN (
 SELECT old.id FROM issue_tracker_sync_triage_activity old WHERE old.incident_id=sqlc.arg(incident_id) ORDER BY old.revision DESC OFFSET sqlc.arg(retained_count));
-- name: IssueTrackerTriagePrune :exec
DELETE FROM issue_tracker_sync_triage tr WHERE tr.link_id=$1 AND NOT EXISTS(SELECT 1 FROM issue_tracker_sync_notifications n WHERE n.incident_id=tr.incident_id);

-- name: IssueTrackerReminderPolicyGet :one
SELECT rp.* FROM issue_tracker_sync_reminder_policies rp JOIN apps a ON a.id=rp.app_id AND a.account_id=rp.account_id
WHERE a.id=$1 AND a.account_id=$2 AND a.deleted_at IS NULL AND a.status<>'deleted';
-- name: IssueTrackerReminderPolicyPut :one
INSERT INTO issue_tracker_sync_reminder_policies(app_id,account_id,enabled,interval_minutes) VALUES($1,$2,$3,$4)
ON CONFLICT(app_id) DO UPDATE SET account_id=excluded.account_id,enabled=excluded.enabled,interval_minutes=excluded.interval_minutes,revision=issue_tracker_sync_reminder_policies.revision+1,updated_at=clock_timestamp() RETURNING *;
-- name: IssueTrackerReminderApp :one
SELECT a.id,a.account_id FROM apps a JOIN accounts owner ON owner.id=a.account_id
JOIN LATERAL (SELECT (GREATEST(c.opened_at,rp.updated_at,c.last_reminded_at,tr.snoozed_until)+rp.interval_minutes*interval '1 minute')::timestamptz AS due_at FROM issue_tracker_sync_incidents c
 JOIN issue_tracker_links l ON l.id=c.link_id
 JOIN app_issues original ON original.id=l.issue_id AND original.app_id=a.id
 JOIN issue_tracker_auto_sync s ON s.link_id=c.link_id AND s.account_id=a.account_id AND s.app_id=a.id
 JOIN issue_tracker_sync_notification_policies p ON p.app_id=a.id AND p.account_id=a.account_id AND p.enabled AND p.revision=c.settings_revision
 JOIN issue_tracker_sync_reminder_policies rp ON rp.app_id=a.id AND rp.account_id=a.account_id AND rp.enabled
 LEFT JOIN issue_tracker_sync_triage tr ON tr.incident_id=c.incident_id
 WHERE c.incident_id IS NOT NULL AND c.opened_at IS NOT NULL AND c.reminder_count<sqlc.arg(reminder_max)
 AND tr.acknowledged_by IS NULL
 AND (tr.snoozed_until IS NULL OR tr.snoozed_until<=statement_timestamp())
 AND GREATEST(c.opened_at,rp.updated_at,c.last_reminded_at,tr.snoozed_until)+rp.interval_minutes*interval '1 minute'<=statement_timestamp() ORDER BY due_at,c.link_id LIMIT 1) ready ON true
WHERE a.deleted_at IS NULL AND a.status<>'deleted' AND owner.status='active' AND owner.abuse_hold_at IS NULL
AND owner.plan=ANY(sqlc.arg(entitled_plans)::text[]) ORDER BY ready.due_at,a.id LIMIT 1 FOR UPDATE OF a SKIP LOCKED;
-- name: IssueTrackerReminderDue :one
SELECT c.* FROM apps a JOIN accounts owner ON owner.id=a.account_id
JOIN LATERAL (SELECT c.* FROM issue_tracker_sync_incidents c
 JOIN issue_tracker_links l ON l.id=c.link_id
 JOIN app_issues original ON original.id=l.issue_id AND original.app_id=a.id
 JOIN issue_tracker_auto_sync s ON s.link_id=c.link_id AND s.account_id=a.account_id AND s.app_id=a.id
 JOIN issue_tracker_sync_notification_policies p ON p.app_id=a.id AND p.account_id=a.account_id AND p.enabled AND p.revision=c.settings_revision
 JOIN issue_tracker_sync_reminder_policies rp ON rp.app_id=a.id AND rp.account_id=a.account_id AND rp.enabled
 LEFT JOIN issue_tracker_sync_triage tr ON tr.incident_id=c.incident_id
 WHERE c.incident_id IS NOT NULL AND c.opened_at IS NOT NULL AND c.reminder_count<sqlc.arg(reminder_max)
 AND tr.acknowledged_by IS NULL
 AND (tr.snoozed_until IS NULL OR tr.snoozed_until<=statement_timestamp())
 AND GREATEST(c.opened_at,rp.updated_at,c.last_reminded_at,tr.snoozed_until)+rp.interval_minutes*interval '1 minute'<=statement_timestamp() ORDER BY GREATEST(c.opened_at,rp.updated_at,c.last_reminded_at,tr.snoozed_until),c.link_id LIMIT 1) c ON true
WHERE a.id=sqlc.arg(app_id) AND a.account_id=sqlc.arg(account_id) AND a.deleted_at IS NULL AND a.status<>'deleted'
AND owner.status='active' AND owner.abuse_hold_at IS NULL AND owner.plan=ANY(sqlc.arg(entitled_plans)::text[]);
-- name: IssueTrackerReminderReceipt :exec
UPDATE issue_tracker_sync_incidents SET reminder_count=sqlc.arg(sequence),last_reminded_at=clock_timestamp(),first_reminded_at=COALESCE(first_reminded_at,clock_timestamp()),issue_id=sqlc.narg(issue_id),team_id=sqlc.narg(team_id),recipient_account_ids=sqlc.arg(recipient_ids)::uuid[]
WHERE link_id=sqlc.arg(link_id) AND incident_id=sqlc.arg(incident_id);
-- name: IssueTrackerReminderOrigin :one
SELECT outcome,reason FROM issue_tracker_sync_notifications WHERE incident_id=$1 AND kind IN ('paused','closure','failure') ORDER BY created_at DESC,id DESC LIMIT 1;
-- name: IssueTrackerReminderHistory :many
SELECT n.reminder_sequence,n.created_at,COALESCE(t.name,'')::text AS team_name FROM issue_tracker_sync_notifications n
JOIN apps a ON a.id=n.app_id AND a.account_id=n.account_id LEFT JOIN app_issue_teams t ON t.id=n.team_id AND t.app_id=a.id
WHERE n.incident_id=sqlc.arg(incident_id) AND n.kind='reminder' AND a.id=sqlc.arg(app_id) AND a.account_id=sqlc.arg(owner_account_id)
AND (a.account_id=sqlc.arg(actor_account_id)::uuid OR (
 sqlc.arg(actor_account_id)::uuid=ANY(n.recipient_account_ids)
 AND EXISTS(SELECT 1 FROM org_memberships m WHERE m.org_id=a.org_id AND m.account_id=sqlc.arg(actor_account_id) AND m.removed_at IS NULL)
 AND (n.team_id IS NULL OR EXISTS(SELECT 1 FROM app_issue_team_members tm WHERE tm.team_id=n.team_id AND tm.account_id=sqlc.arg(actor_account_id)))))
ORDER BY n.reminder_sequence DESC LIMIT sqlc.arg(history_limit);

-- name: IssueTrackerEscalationPolicyGet :one
SELECT ep.* FROM issue_tracker_sync_escalation_policies ep JOIN apps a ON a.id=ep.app_id AND a.account_id=ep.account_id
WHERE a.id=$1 AND a.account_id=$2 AND a.deleted_at IS NULL AND a.status<>'deleted';
-- name: IssueTrackerEscalationPolicyPut :one
INSERT INTO issue_tracker_sync_escalation_policies(app_id,account_id,enabled,team_id,delay_minutes) VALUES($1,$2,$3,$4,$5)
ON CONFLICT(app_id) DO UPDATE SET account_id=excluded.account_id,enabled=excluded.enabled,team_id=excluded.team_id,delay_minutes=excluded.delay_minutes,revision=issue_tracker_sync_escalation_policies.revision+1,updated_at=clock_timestamp() RETURNING *;
-- name: IssueTrackerEscalationApp :one
SELECT a.id,a.account_id FROM apps a JOIN accounts owner ON owner.id=a.account_id
JOIN LATERAL (SELECT (GREATEST(c.first_reminded_at,ep.updated_at,tr.snoozed_until)+ep.delay_minutes*interval '1 minute')::timestamptz AS due_at FROM issue_tracker_sync_incidents c
 JOIN issue_tracker_links l ON l.id=c.link_id
 JOIN app_issues original ON original.id=l.issue_id AND original.app_id=a.id
 JOIN issue_tracker_auto_sync s ON s.link_id=c.link_id AND s.account_id=a.account_id AND s.app_id=a.id
 JOIN issue_tracker_sync_notification_policies p ON p.app_id=a.id AND p.account_id=a.account_id AND p.enabled AND p.revision=c.settings_revision
 JOIN issue_tracker_sync_reminder_policies rp ON rp.app_id=a.id AND rp.account_id=a.account_id AND rp.enabled
 JOIN issue_tracker_sync_escalation_policies ep ON ep.app_id=a.id AND ep.account_id=a.account_id AND ep.enabled
 LEFT JOIN issue_tracker_sync_triage tr ON tr.incident_id=c.incident_id
 WHERE c.incident_id IS NOT NULL AND c.first_reminded_at IS NOT NULL AND c.escalated_at IS NULL
 AND tr.acknowledged_by IS NULL
 AND (tr.snoozed_until IS NULL OR tr.snoozed_until<=statement_timestamp())
 AND GREATEST(c.first_reminded_at,ep.updated_at,tr.snoozed_until)+ep.delay_minutes*interval '1 minute'<=statement_timestamp() ORDER BY due_at,c.link_id LIMIT 1) ready ON true
WHERE a.deleted_at IS NULL AND a.status<>'deleted' AND owner.status='active' AND owner.abuse_hold_at IS NULL
AND owner.plan=ANY(sqlc.arg(entitled_plans)::text[]) ORDER BY ready.due_at,a.id LIMIT 1 FOR UPDATE OF a SKIP LOCKED;
-- name: IssueTrackerEscalationDue :one
SELECT c.* FROM apps a JOIN accounts owner ON owner.id=a.account_id
JOIN LATERAL (SELECT c.* FROM issue_tracker_sync_incidents c
 JOIN issue_tracker_links l ON l.id=c.link_id
 JOIN app_issues original ON original.id=l.issue_id AND original.app_id=a.id
 JOIN issue_tracker_auto_sync s ON s.link_id=c.link_id AND s.account_id=a.account_id AND s.app_id=a.id
 JOIN issue_tracker_sync_notification_policies p ON p.app_id=a.id AND p.account_id=a.account_id AND p.enabled AND p.revision=c.settings_revision
 JOIN issue_tracker_sync_reminder_policies rp ON rp.app_id=a.id AND rp.account_id=a.account_id AND rp.enabled
 JOIN issue_tracker_sync_escalation_policies ep ON ep.app_id=a.id AND ep.account_id=a.account_id AND ep.enabled
 LEFT JOIN issue_tracker_sync_triage tr ON tr.incident_id=c.incident_id
 WHERE c.incident_id IS NOT NULL AND c.first_reminded_at IS NOT NULL AND c.escalated_at IS NULL
 AND tr.acknowledged_by IS NULL
 AND (tr.snoozed_until IS NULL OR tr.snoozed_until<=statement_timestamp())
 AND GREATEST(c.first_reminded_at,ep.updated_at,tr.snoozed_until)+ep.delay_minutes*interval '1 minute'<=statement_timestamp() ORDER BY GREATEST(c.first_reminded_at,ep.updated_at,tr.snoozed_until)+ep.delay_minutes*interval '1 minute',c.link_id LIMIT 1) c ON true
WHERE a.id=sqlc.arg(app_id) AND a.account_id=sqlc.arg(account_id) AND a.deleted_at IS NULL AND a.status<>'deleted'
AND owner.status='active' AND owner.abuse_hold_at IS NULL AND owner.plan=ANY(sqlc.arg(entitled_plans)::text[]);
-- name: IssueTrackerEscalationReceipt :exec
UPDATE issue_tracker_sync_incidents SET escalated_at=clock_timestamp(),escalation_team_id=sqlc.arg(escalation_team_id),issue_id=sqlc.narg(issue_id),team_id=sqlc.narg(team_id),recipient_account_ids=sqlc.arg(recipient_ids)::uuid[]
WHERE link_id=sqlc.arg(link_id) AND incident_id=sqlc.arg(incident_id) AND escalated_at IS NULL;
-- name: IssueTrackerEscalationHistory :many
SELECT n.team_id,n.created_at,COALESCE(t.name,'')::text AS team_name FROM issue_tracker_sync_notifications n
JOIN apps a ON a.id=n.app_id AND a.account_id=n.account_id LEFT JOIN app_issue_teams t ON t.id=n.team_id AND t.app_id=a.id
WHERE n.incident_id=sqlc.arg(incident_id) AND n.kind='escalation' AND a.id=sqlc.arg(app_id) AND a.account_id=sqlc.arg(owner_account_id)
AND (a.account_id=sqlc.arg(actor_account_id)::uuid OR (
 sqlc.arg(actor_account_id)::uuid=ANY(n.recipient_account_ids)
 AND EXISTS(SELECT 1 FROM org_memberships m WHERE m.org_id=a.org_id AND m.account_id=sqlc.arg(actor_account_id) AND m.removed_at IS NULL)
 AND (n.team_id IS NULL OR EXISTS(SELECT 1 FROM app_issue_team_members tm WHERE tm.team_id=n.team_id AND tm.account_id=sqlc.arg(actor_account_id)))))
ORDER BY n.created_at DESC LIMIT 1;
