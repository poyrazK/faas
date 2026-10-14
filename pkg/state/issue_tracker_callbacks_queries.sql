-- name: IssueTrackerCallbackScopeLock :exec
SELECT pg_advisory_xact_lock(hashtextextended(sqlc.arg(scope_key)::text, 731));
-- name: IssueTrackerCallbackFind :many
SELECT delivery_id,payload_hash FROM issue_tracker_callbacks WHERE scope_key=sqlc.arg(scope_key)
 AND (delivery_id=sqlc.arg(delivery_id) OR payload_hash=sqlc.arg(payload_hash));
-- name: IssueTrackerCallbackCount :one
SELECT count(*) FROM issue_tracker_callbacks WHERE scope_key=$1;
-- name: IssueTrackerCallbackInsert :exec
INSERT INTO issue_tracker_callbacks(scope_key,delivery_id,provider,app_id,connection_revision,installation_id,repository_id,workspace_id,team_id,ticket_id,ticket_url,payload_hash)
VALUES(sqlc.arg(scope_key),sqlc.arg(delivery_id),sqlc.arg(provider),sqlc.narg(app_id),sqlc.arg(connection_revision),sqlc.arg(installation_id),sqlc.arg(repository_id),sqlc.narg(workspace_id),sqlc.narg(team_id),sqlc.arg(ticket_id),sqlc.arg(ticket_url),sqlc.arg(payload_hash));
-- name: IssueTrackerCallbackNext :one
SELECT * FROM issue_tracker_callbacks WHERE processed_at IS NULL ORDER BY received_at,scope_key,delivery_id FOR UPDATE SKIP LOCKED LIMIT 1;
-- name: IssueTrackerCallbackPolicies :many
SELECT p.link_id,p.claim_until FROM issue_tracker_auto_sync p
JOIN apps a ON a.id=p.app_id AND a.account_id=p.account_id
LEFT JOIN issue_tracker_linear_connections c ON c.app_id=a.id AND c.account_id=a.account_id
WHERE p.enabled AND p.outcome IN ('matched','pending','applied','error') AND a.deleted_at IS NULL AND a.status<>'deleted'
 AND p.baseline->>'provider'=sqlc.arg(provider)::text AND p.baseline->>'id'=sqlc.arg(ticket_id)::text
 AND ((sqlc.arg(provider)::text='github' AND a.github_install_id=sqlc.arg(installation_id)::bigint AND a.github_repo_id=sqlc.arg(repository_id)::bigint
       AND a.github_install_account_id=a.account_id AND p.baseline->>'url'=sqlc.arg(ticket_url)::text
       AND EXISTS(SELECT 1 FROM github_installations gi WHERE gi.account_id=a.account_id AND gi.installation_id=a.github_install_id))
 OR (sqlc.arg(provider)::text='linear' AND a.id=sqlc.narg(app_id)::uuid AND c.connected AND c.webhook_enabled
       AND c.revision=sqlc.arg(connection_revision)::bigint AND c.workspace_id=sqlc.narg(workspace_id)::uuid AND c.team_id=sqlc.narg(team_id)::uuid
       AND p.baseline->>'team_id'=c.team_id::text))
ORDER BY p.link_id FOR UPDATE OF p;
-- name: IssueTrackerCallbackSchedule :exec
UPDATE issue_tracker_auto_sync SET next_check_at=LEAST(next_check_at,now()),claim_id=NULL,claim_until=NULL WHERE link_id=$1;
-- name: IssueTrackerCallbackComplete :exec
UPDATE issue_tracker_callbacks SET processed_at=now() WHERE scope_key=$1 AND delivery_id=$2;
-- name: IssueTrackerCallbackCleanup :exec
WITH expired AS (SELECT scope_key,delivery_id FROM issue_tracker_callbacks WHERE processed_at<sqlc.arg(before)::timestamptz ORDER BY processed_at LIMIT sqlc.arg(batch_size))
DELETE FROM issue_tracker_callbacks c USING expired e WHERE c.scope_key=e.scope_key AND c.delivery_id=e.delivery_id;
