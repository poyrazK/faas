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
-- name: IssueTrackerCallbackHealth :one
SELECT count(*)::bigint AS retained_deliveries,
 count(*) FILTER (WHERE cb.processed_at IS NULL)::bigint AS pending_hints,
 max(cb.received_at)::timestamptz AS last_received_at,
 max(cb.processed_at)::timestamptz AS last_processed_at
FROM issue_tracker_callbacks cb
JOIN issue_tracker_auto_sync p ON p.link_id=sqlc.arg(link_id)
JOIN apps a ON a.id=p.app_id AND a.account_id=p.account_id
LEFT JOIN issue_tracker_linear_connections c ON c.app_id=a.id AND c.account_id=a.account_id
WHERE a.id=sqlc.arg(app_id) AND a.account_id=sqlc.arg(account_id)
 AND a.deleted_at IS NULL AND a.status<>'deleted'
 AND cb.scope_key=CASE WHEN p.baseline->>'provider'='github' THEN 'github/'||a.github_install_id::text ELSE 'linear/'||a.id::text||'/'||c.revision::text END
 AND cb.provider=p.baseline->>'provider' AND cb.ticket_id=p.baseline->>'id'
 AND ((cb.provider='github' AND cb.installation_id=a.github_install_id AND cb.repository_id=a.github_repo_id
  AND cb.ticket_url=p.baseline->>'url' AND a.github_install_account_id=a.account_id
  AND EXISTS(SELECT 1 FROM github_installations gi WHERE gi.account_id=a.account_id AND gi.installation_id=a.github_install_id))
 OR (cb.provider='linear' AND cb.app_id=a.id AND c.connected AND c.webhook_enabled
  AND cb.connection_revision=c.revision AND cb.workspace_id=c.workspace_id AND cb.team_id=c.team_id
  AND p.baseline->>'team_id'=c.team_id::text));

-- name: IssueTrackerCallbackDiagnosticRecord :exec
INSERT INTO issue_tracker_callback_diagnostics AS d(app_id,account_id,connection_revision,invalid_signature,wrong_scope,stale_delivery,invalid_payload,delivery_conflict,last_verified_at,last_rejected_at)
SELECT c.app_id,c.account_id,c.revision,CASE WHEN sqlc.arg(reason)::text='invalid_signature' THEN 1 ELSE 0 END,CASE WHEN sqlc.arg(reason)::text='wrong_scope' THEN 1 ELSE 0 END,CASE WHEN sqlc.arg(reason)::text='stale_delivery' THEN 1 ELSE 0 END,CASE WHEN sqlc.arg(reason)::text='invalid_payload' THEN 1 ELSE 0 END,CASE WHEN sqlc.arg(reason)::text='delivery_conflict' THEN 1 ELSE 0 END,CASE WHEN sqlc.arg(reason)::text='verified' THEN now() END,CASE WHEN sqlc.arg(reason)::text<>'verified' THEN now() END
FROM issue_tracker_linear_connections c JOIN apps a ON a.id=c.app_id AND a.account_id=c.account_id
WHERE c.app_id=sqlc.arg(app_id) AND c.account_id=sqlc.arg(account_id) AND c.revision=sqlc.arg(connection_revision) AND c.connected AND c.webhook_enabled AND a.deleted_at IS NULL AND a.status<>'deleted'
ON CONFLICT(app_id) DO UPDATE SET account_id=excluded.account_id,connection_revision=excluded.connection_revision,
invalid_signature=CASE WHEN d.connection_revision<>excluded.connection_revision OR d.account_id<>excluded.account_id THEN excluded.invalid_signature WHEN d.invalid_signature<9223372036854775807 THEN d.invalid_signature+excluded.invalid_signature ELSE d.invalid_signature END,
wrong_scope=CASE WHEN d.connection_revision<>excluded.connection_revision OR d.account_id<>excluded.account_id THEN excluded.wrong_scope WHEN d.wrong_scope<9223372036854775807 THEN d.wrong_scope+excluded.wrong_scope ELSE d.wrong_scope END,
stale_delivery=CASE WHEN d.connection_revision<>excluded.connection_revision OR d.account_id<>excluded.account_id THEN excluded.stale_delivery WHEN d.stale_delivery<9223372036854775807 THEN d.stale_delivery+excluded.stale_delivery ELSE d.stale_delivery END,
invalid_payload=CASE WHEN d.connection_revision<>excluded.connection_revision OR d.account_id<>excluded.account_id THEN excluded.invalid_payload WHEN d.invalid_payload<9223372036854775807 THEN d.invalid_payload+excluded.invalid_payload ELSE d.invalid_payload END,
delivery_conflict=CASE WHEN d.connection_revision<>excluded.connection_revision OR d.account_id<>excluded.account_id THEN excluded.delivery_conflict WHEN d.delivery_conflict<9223372036854775807 THEN d.delivery_conflict+excluded.delivery_conflict ELSE d.delivery_conflict END,
last_verified_at=CASE WHEN d.connection_revision<>excluded.connection_revision OR d.account_id<>excluded.account_id THEN excluded.last_verified_at ELSE COALESCE(excluded.last_verified_at,d.last_verified_at) END,
last_rejected_at=CASE WHEN d.connection_revision<>excluded.connection_revision OR d.account_id<>excluded.account_id THEN excluded.last_rejected_at ELSE COALESCE(excluded.last_rejected_at,d.last_rejected_at) END
WHERE d.connection_revision<=excluded.connection_revision;
-- name: IssueTrackerCallbackSetup :one
SELECT l.provider,
 CASE WHEN l.provider='linear' THEN COALESCE(c.connected AND c.webhook_enabled,false)
 ELSE COALESCE(a.github_install_id IS NOT NULL AND a.github_repo_id IS NOT NULL AND a.github_install_account_id=a.account_id
 AND EXISTS(SELECT 1 FROM github_installations gi WHERE gi.account_id=a.account_id AND gi.installation_id=a.github_install_id),false) END::boolean AS configured,
 d.last_verified_at,d.last_rejected_at,
COALESCE(d.invalid_signature,0)::bigint AS invalid_signature,COALESCE(d.wrong_scope,0)::bigint AS wrong_scope,COALESCE(d.stale_delivery,0)::bigint AS stale_delivery,COALESCE(d.invalid_payload,0)::bigint AS invalid_payload,COALESCE(d.delivery_conflict,0)::bigint AS delivery_conflict
FROM apps a JOIN issue_tracker_links l ON l.id=sqlc.arg(link_id)
LEFT JOIN issue_tracker_linear_connections c ON c.app_id=a.id AND c.account_id=a.account_id AND c.connected AND c.webhook_enabled
LEFT JOIN issue_tracker_callback_diagnostics d ON d.app_id=c.app_id AND d.account_id=c.account_id AND d.connection_revision=c.revision AND l.provider='linear'
WHERE a.id=sqlc.arg(app_id) AND a.account_id=sqlc.arg(account_id) AND a.deleted_at IS NULL AND a.status<>'deleted';
