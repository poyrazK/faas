-- name: IssueTrackerAutoSyncGet :one
SELECT * FROM issue_tracker_auto_sync WHERE link_id=$1;
-- name: IssueTrackerAutoSyncEnable :one
INSERT INTO issue_tracker_auto_sync(link_id,app_id,account_id,source_issue_id,binding_fingerprint,baseline,source_state,linear_open_state_id,linear_resolved_state_id)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)
ON CONFLICT(link_id) DO UPDATE SET app_id=excluded.app_id,account_id=excluded.account_id,enabled=true,revision=issue_tracker_auto_sync.revision+1,source_issue_id=excluded.source_issue_id,
 linear_open_state_id=excluded.linear_open_state_id,linear_resolved_state_id=excluded.linear_resolved_state_id, binding_fingerprint=excluded.binding_fingerprint,baseline=excluded.baseline,source_state=excluded.source_state,pending_state='',outcome='matched',reason='',claim_id=NULL,claim_until=NULL,next_check_at=now(),updated_at=now()
RETURNING *;
-- name: IssueTrackerAutoSyncDisable :one
UPDATE issue_tracker_auto_sync SET enabled=false,revision=revision+1,outcome='disabled',reason='',pending_state='',claim_id=NULL,claim_until=NULL,updated_at=now() WHERE link_id=$1 RETURNING *;
-- name: IssueTrackerAutoSyncClaim :one
WITH due AS (
 SELECT link_id FROM issue_tracker_auto_sync WHERE enabled AND outcome IN ('matched','pending','applied','error')
 AND next_check_at<=now() AND (claim_until IS NULL OR claim_until<=now())
 ORDER BY next_check_at,link_id FOR UPDATE SKIP LOCKED LIMIT 1
)
UPDATE issue_tracker_auto_sync p SET claim_id=sqlc.arg(claim_id),claim_until=sqlc.arg(claim_until),next_check_at=sqlc.arg(claim_until)
FROM due WHERE p.link_id=due.link_id RETURNING p.*;
-- name: IssueTrackerAutoSyncPending :one
UPDATE issue_tracker_auto_sync SET pending_state=sqlc.arg(pending_state),outcome='pending',updated_at=now()
WHERE link_id=sqlc.arg(link_id) AND enabled AND revision=sqlc.arg(revision) AND claim_id=sqlc.arg(claim_id) AND claim_until>now() RETURNING *;
-- name: IssueTrackerAutoSyncFinish :one
UPDATE issue_tracker_auto_sync SET baseline=sqlc.arg(baseline),source_state=sqlc.arg(source_state),pending_state=sqlc.arg(pending_state),
 outcome=sqlc.arg(outcome),reason=sqlc.arg(reason),last_checked_at=now(),next_check_at=sqlc.arg(next_check_at),claim_id=NULL,claim_until=NULL,updated_at=now()
WHERE link_id=sqlc.arg(link_id) AND enabled AND revision=sqlc.arg(revision) AND claim_id=sqlc.arg(claim_id) RETURNING *;

-- name: IssueTrackerInboundGuard :one
SELECT g.revision FROM issue_tracker_inbound_guards g JOIN apps a ON a.id=g.app_id
WHERE g.app_id=$1 AND a.account_id=$2 AND a.deleted_at IS NULL AND a.status<>'deleted';
-- name: IssueTrackerInboundGuardLock :one
SELECT revision FROM issue_tracker_inbound_guards WHERE app_id=$1 FOR UPDATE;
-- name: IssueTrackerInboundPolicyLock :one
SELECT * FROM issue_tracker_auto_sync WHERE link_id=$1 FOR UPDATE;
-- name: IssueTrackerInboundApplied :exec
UPDATE issue_tracker_auto_sync SET enabled=false,revision=revision+1,baseline=$2,source_state=$3,outcome='disabled',reason='',pending_state='',claim_id=NULL,claim_until=NULL,last_checked_at=now(),updated_at=now() WHERE link_id=$1;
-- name: IssueTrackerInboundDeployments :many
SELECT d.id,d.commit_sha,d.created_at FROM deployments d JOIN apps a ON a.id=d.app_id
JOIN issue_group_rollups i ON i.app_id=a.id AND i.id=sqlc.arg(issue_id)
WHERE a.id=sqlc.arg(app_id) AND a.account_id=sqlc.arg(account_id) AND a.deleted_at IS NULL AND a.status<>'deleted'
AND (i.environment='application' OR EXISTS (
 SELECT 1 FROM project_release_members rm JOIN project_release_sets rs ON rs.id=rm.release_id
 WHERE rm.app_id=a.id AND rm.deployment_id=d.id AND rs.environment_slug=i.environment AND rs.account_id=a.account_id))
ORDER BY d.created_at DESC,d.id DESC LIMIT sqlc.arg(page_limit);
