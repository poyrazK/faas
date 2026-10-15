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
