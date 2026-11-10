-- name: GitHubProjectBindingsForInstallation :many
SELECT id::text AS project_id, account_id::text AS account_id,
       coalesce(repo_full_name, '') AS repo_full_name,
       coalesce(production_branch, '') AS production_branch
FROM projects
WHERE install_id = $1
ORDER BY account_id, id;

-- name: RefreshGitHubConnectionToken :execrows
UPDATE github_installations
SET sealed_install_token = sqlc.arg(token), token_expires_at = sqlc.arg(expires), sealed_at = now()
WHERE account_id = sqlc.arg(account_id) AND installation_id = sqlc.arg(installation_id)
  AND sealed_install_token = sqlc.arg(previous_token);

-- name: GitHubConnectionApps :many
SELECT a.id::text FROM apps a
LEFT JOIN projects p ON p.id = a.project_id
WHERE a.account_id = $1 AND (a.github_install_id = $2 OR p.install_id = $2)
ORDER BY a.id FOR UPDATE OF a;

-- name: DisconnectGitHubConnectionProjects :exec
UPDATE projects SET install_id = NULL, updated_at = now()
WHERE account_id = $1 AND install_id = $2;

-- name: DisconnectGitHubConnectionApps :exec
UPDATE apps SET github_install_id = NULL, github_repo_full_name = NULL,
 github_owner_id = NULL, github_repo_id = NULL, github_production_branch = NULL,
 github_install_binding_id = NULL, github_install_account_id = NULL,
 github_install_linked_at = NULL, github_production_trigger = NULL, github_preview_enabled = NULL
WHERE account_id = $1 AND github_install_id = $2;

-- name: DisconnectGitHubConnection :exec
DELETE FROM github_installations WHERE account_id = $1 AND installation_id = $2;

-- name: ClaimGitHubTransportRecovery :execrows
INSERT INTO github_transport_recoveries(delivery_guid, attempts, next_attempt_at)
SELECT sqlc.arg(guid), 1, sqlc.arg(attempt_at)::timestamptz + interval '5 minutes'
WHERE NOT EXISTS (SELECT 1 FROM github_webhook_deliveries WHERE delivery_id=sqlc.arg(guid))
AND NOT EXISTS (SELECT 1 FROM github_transport_recoveries WHERE delivery_guid='' AND next_attempt_at>sqlc.arg(attempt_at))
ON CONFLICT(delivery_guid) DO UPDATE SET attempts=github_transport_recoveries.attempts+1,
 next_attempt_at=sqlc.arg(attempt_at)::timestamptz + interval '5 minutes' * (github_transport_recoveries.attempts+1)
WHERE github_transport_recoveries.attempts<3 AND github_transport_recoveries.next_attempt_at<=sqlc.arg(attempt_at)
AND NOT EXISTS (SELECT 1 FROM github_webhook_deliveries WHERE delivery_id=sqlc.arg(guid))
AND NOT EXISTS (SELECT 1 FROM github_transport_recoveries WHERE delivery_guid='' AND next_attempt_at>sqlc.arg(attempt_at));

-- name: DelayGitHubTransportRecovery :exec
INSERT INTO github_transport_recoveries(delivery_guid,attempts,next_attempt_at) VALUES (sqlc.arg(guid),3,sqlc.arg(retry_at))
ON CONFLICT(delivery_guid) DO UPDATE SET next_attempt_at=greatest(github_transport_recoveries.next_attempt_at,excluded.next_attempt_at);

-- name: PruneGitHubTransportRecoveries :exec
DELETE FROM github_transport_recoveries WHERE created_at < now() - interval '7 days';

-- name: RecordGitHubDeliveryTarget :exec
INSERT INTO github_delivery_targets(delivery_id, account_id, status, skip_reason)
VALUES ($1,$2,$3,$4) ON CONFLICT(delivery_id,account_id)
DO UPDATE SET status=excluded.status,skip_reason=excluded.skip_reason,updated_at=now();

-- name: RecordGitHubConnectionVerification :execrows
INSERT INTO github_connection_verifications(account_id,installation_id,verified_at,access)
SELECT sqlc.arg(account_id),sqlc.arg(installation_id),sqlc.arg(verified_at),sqlc.arg(access)
WHERE EXISTS(SELECT 1 FROM github_installations WHERE account_id=sqlc.arg(account_id) AND installation_id=sqlc.arg(installation_id))
ON CONFLICT(account_id,installation_id) DO UPDATE SET verified_at=excluded.verified_at,access=excluded.access;

-- name: GitHubConnectionVerifications :many
SELECT installation_id,verified_at,access FROM github_connection_verifications WHERE account_id=$1;
