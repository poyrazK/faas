-- name: IssueTrackerLinearOwner :one
SELECT id FROM apps WHERE id=sqlc.arg(app_id) AND account_id=sqlc.arg(account_id) AND deleted_at IS NULL AND status<>'deleted' FOR UPDATE;
-- name: IssueTrackerLinearGet :one
SELECT * FROM issue_tracker_linear_connections WHERE app_id=sqlc.arg(app_id) AND account_id=sqlc.arg(account_id);
-- name: IssueTrackerLinearPut :one
INSERT INTO issue_tracker_linear_connections(app_id,account_id,revision,connected,workspace_id,team_id,viewer_id,sealed_credential,webhook_enabled,auth_method,oauth_status,access_token_expires_at)
VALUES(sqlc.arg(app_id),sqlc.arg(account_id),1,sqlc.arg(connected),sqlc.narg(workspace_id),sqlc.narg(team_id),sqlc.narg(viewer_id),sqlc.narg(sealed_credential),sqlc.arg(webhook_enabled),sqlc.arg(auth_method),sqlc.arg(oauth_status),sqlc.narg(access_token_expires_at))
ON CONFLICT(app_id) DO UPDATE SET account_id=excluded.account_id,revision=issue_tracker_linear_connections.revision+1,connected=excluded.connected,
workspace_id=excluded.workspace_id,team_id=excluded.team_id,viewer_id=excluded.viewer_id,sealed_credential=excluded.sealed_credential,webhook_enabled=excluded.webhook_enabled,auth_method=excluded.auth_method,oauth_status=excluded.oauth_status,access_token_expires_at=excluded.access_token_expires_at,credential_generation=issue_tracker_linear_connections.credential_generation+1,refresh_claim_id=NULL,refresh_claim_until=NULL,refresh_started_at=NULL,updated_at=now()
RETURNING *;
-- name: LinearOAuthFlowStart :exec
INSERT INTO issue_tracker_linear_oauth_flows(app_id,account_id,issue_id,state_hash,expected_revision,phase,sealed_state,expires_at)
VALUES($1,$2,$3,$4,$5,'authorizing',$6,$7)
ON CONFLICT(app_id) DO UPDATE SET account_id=excluded.account_id,issue_id=excluded.issue_id,state_hash=excluded.state_hash,expected_revision=excluded.expected_revision,phase='authorizing',sealed_state=excluded.sealed_state,expires_at=excluded.expires_at;
-- name: LinearOAuthFlowGet :one
SELECT f.* FROM issue_tracker_linear_oauth_flows f JOIN apps a ON a.id=f.app_id AND a.account_id=f.account_id
WHERE f.state_hash=$1 AND f.account_id=$2 AND f.expires_at>now() AND a.deleted_at IS NULL AND a.status<>'deleted';
-- name: LinearOAuthFlowClaim :one
UPDATE issue_tracker_linear_oauth_flows f SET phase=sqlc.arg(next_phase)
FROM apps a WHERE a.id=f.app_id AND a.account_id=f.account_id AND a.deleted_at IS NULL AND a.status<>'deleted'
AND f.state_hash=sqlc.arg(state_hash) AND f.account_id=sqlc.arg(account_id) AND f.phase=sqlc.arg(expected_phase) AND f.expires_at>now() RETURNING f.*;
-- name: LinearOAuthFlowSave :execrows
UPDATE issue_tracker_linear_oauth_flows SET sealed_state=$3,phase='choosing' WHERE state_hash=$1 AND account_id=$2 AND phase='exchanging' AND expires_at>now();
-- name: LinearOAuthFlowDelete :exec
DELETE FROM issue_tracker_linear_oauth_flows WHERE state_hash=$1 AND account_id=$2;
-- name: LinearOAuthFlowCleanup :exec
WITH expired AS (SELECT app_id FROM issue_tracker_linear_oauth_flows WHERE expires_at<now() ORDER BY expires_at LIMIT $1)
DELETE FROM issue_tracker_linear_oauth_flows f USING expired e WHERE f.app_id=e.app_id;
-- name: LinearOAuthRefreshClaim :one
UPDATE issue_tracker_linear_connections c SET oauth_status='refreshing',refresh_claim_id=sqlc.arg(claim_id),refresh_claim_until=sqlc.arg(claim_until),refresh_started_at=COALESCE(c.refresh_started_at,now())
FROM apps a WHERE a.id=c.app_id AND a.account_id=c.account_id AND a.deleted_at IS NULL AND a.status<>'deleted'
AND c.app_id=sqlc.arg(app_id) AND c.account_id=sqlc.arg(account_id) AND c.revision=sqlc.arg(revision) AND c.credential_generation=sqlc.arg(generation)
AND c.connected AND c.auth_method='oauth' AND c.oauth_status IN ('ready','refreshing') AND (c.refresh_claim_until IS NULL OR c.refresh_claim_until<=now())
AND c.access_token_expires_at<=sqlc.arg(refresh_before) AND (c.refresh_started_at IS NULL OR c.refresh_started_at>sqlc.arg(grace_after)) RETURNING c.*;
-- name: LinearOAuthRefreshFinish :one
UPDATE issue_tracker_linear_connections c SET sealed_credential=COALESCE(sqlc.narg(sealed_credential)::bytea,c.sealed_credential),access_token_expires_at=COALESCE(sqlc.narg(expires_at)::timestamptz,c.access_token_expires_at),
 credential_generation=c.credential_generation+CASE WHEN sqlc.narg(sealed_credential)::bytea IS NULL THEN 0 ELSE 1 END,
 oauth_status=sqlc.arg(oauth_status),refresh_claim_id=NULL,refresh_claim_until=NULL,refresh_started_at=CASE WHEN sqlc.arg(oauth_status)::text='ready' THEN NULL ELSE c.refresh_started_at END,updated_at=now()
FROM apps a WHERE a.id=c.app_id AND a.account_id=c.account_id AND a.deleted_at IS NULL AND a.status<>'deleted'
AND c.app_id=sqlc.arg(app_id) AND c.account_id=sqlc.arg(account_id) AND c.revision=sqlc.arg(revision) AND c.credential_generation=sqlc.arg(generation)
AND c.connected AND c.auth_method='oauth' AND c.refresh_claim_id=sqlc.arg(claim_id) AND c.refresh_claim_until>now() RETURNING c.*;
-- name: LinearOAuthSetStatus :one
UPDATE issue_tracker_linear_connections c SET oauth_status=sqlc.arg(oauth_status),updated_at=now()
FROM apps a WHERE a.id=c.app_id AND a.account_id=c.account_id AND a.deleted_at IS NULL AND a.status<>'deleted'
AND c.app_id=sqlc.arg(app_id) AND c.account_id=sqlc.arg(account_id) AND c.revision=sqlc.arg(revision) AND c.credential_generation=sqlc.arg(generation)
AND c.connected AND c.auth_method='oauth' AND (c.refresh_claim_until IS NULL OR c.refresh_claim_until<=now()) RETURNING c.*;
