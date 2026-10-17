-- ADR-1451: config revisions and config guards.

-- name: LockAppConfigScope :exec
-- Serializes revision numbering and guard bookkeeping for one app scope.
SELECT pg_advisory_xact_lock(hashtextextended('app_config:' || sqlc.arg(app_id)::uuid::text || ':' || sqlc.arg(scope)::text, 0));

-- name: ReadAppConfigVariables :many
SELECT key, value FROM app_envs
WHERE account_id = sqlc.arg(account_id)::uuid AND app_id = sqlc.arg(app_id)::uuid AND scope = sqlc.arg(scope)::text
ORDER BY key;

-- name: ReadAppConfigSecretFingerprints :many
SELECT key, COALESCE(secret_version, 0)::bigint AS secret_version, COALESCE(value_hash, '')::text AS value_hash
FROM app_secrets
WHERE account_id = sqlc.arg(account_id)::uuid AND app_id = sqlc.arg(app_id)::uuid AND scope = sqlc.arg(scope)::text
ORDER BY key;

-- name: LatestAppConfigRevision :one
SELECT * FROM app_config_revisions
WHERE app_id = sqlc.arg(app_id)::uuid AND scope = sqlc.arg(scope)::text
ORDER BY revision DESC LIMIT 1;

-- name: InsertAppConfigRevision :one
INSERT INTO app_config_revisions (id, account_id, app_id, scope, revision, source, config_key, actor,
    variables, secrets, guard_id, restored_revision, created_at)
VALUES (sqlc.arg(id)::uuid, sqlc.arg(account_id)::uuid, sqlc.arg(app_id)::uuid, sqlc.arg(scope)::text,
    sqlc.arg(revision)::bigint, sqlc.arg(source)::text, sqlc.arg(config_key)::text, sqlc.arg(actor)::text,
    sqlc.arg(variables)::jsonb, sqlc.arg(secrets)::jsonb, sqlc.narg(guard_id)::uuid,
    sqlc.narg(restored_revision)::bigint, sqlc.arg(created_at)::timestamptz)
RETURNING *;

-- name: PruneAppConfigRevisions :exec
-- Keeps the newest revisions and every watching guard's base revision.
DELETE FROM app_config_revisions r
WHERE r.app_id = sqlc.arg(app_id)::uuid AND r.scope = sqlc.arg(scope)::text
  AND r.revision <= sqlc.arg(keep_after)::bigint
  AND NOT EXISTS (
      SELECT 1 FROM app_config_guards g
      WHERE g.app_id = r.app_id AND g.scope = r.scope AND g.status = 'watching' AND g.base_revision = r.revision);

-- name: ListAppConfigRevisions :many
SELECT sqlc.embed(r), COALESCE(g.status, '')::text AS guard_status
FROM app_config_revisions r LEFT JOIN app_config_guards g ON g.id = r.guard_id
WHERE r.app_id = sqlc.arg(app_id)::uuid AND r.scope = sqlc.arg(scope)::text
ORDER BY r.revision DESC LIMIT sqlc.arg(row_limit)::int;

-- name: GetAppConfigRevision :one
SELECT sqlc.embed(r), COALESCE(g.status, '')::text AS guard_status
FROM app_config_revisions r LEFT JOIN app_config_guards g ON g.id = r.guard_id
WHERE r.app_id = sqlc.arg(app_id)::uuid AND r.scope = sqlc.arg(scope)::text AND r.revision = sqlc.arg(revision)::bigint;

-- name: LockWatchingAppConfigGuard :one
SELECT * FROM app_config_guards
WHERE app_id = sqlc.arg(app_id)::uuid AND scope = sqlc.arg(scope)::text AND status = 'watching'
FOR UPDATE;

-- name: InsertAppConfigGuard :one
INSERT INTO app_config_guards (id, account_id, app_id, scope, base_revision, head_revision, status, restorable,
    opened_at, last_change_at)
VALUES (sqlc.arg(id)::uuid, sqlc.arg(account_id)::uuid, sqlc.arg(app_id)::uuid, sqlc.arg(scope)::text,
    sqlc.arg(base_revision)::bigint, sqlc.arg(base_revision)::bigint, 'watching', true,
    sqlc.arg(opened_at)::timestamptz, sqlc.arg(opened_at)::timestamptz)
RETURNING *;

-- name: RetainAppConfigGuardSecret :exec
-- The first write of a key in a guard captures the row the guard replaces.
INSERT INTO app_config_guard_secrets (guard_id, config_key, existed, ciphertext, kid, value_hash, secret_class)
SELECT sqlc.arg(guard_id)::uuid, sqlc.arg(config_key)::text, s.key IS NOT NULL, s.ciphertext, s.kid, s.value_hash,
    CASE WHEN s.key IS NULL THEN NULL ELSE s.secret_class END
FROM (SELECT 1) AS one
LEFT JOIN app_secrets s ON s.account_id = sqlc.arg(account_id)::uuid AND s.app_id = sqlc.arg(app_id)::uuid
    AND s.scope = sqlc.arg(scope)::text AND s.key = sqlc.arg(config_key)::text
ON CONFLICT (guard_id, config_key) DO NOTHING;

-- name: RecordAppConfigGuardChange :execrows
UPDATE app_config_guards
SET head_revision = sqlc.arg(head_revision)::bigint,
    last_change_at = GREATEST(last_change_at, sqlc.arg(changed_at)::timestamptz),
    window_ends_at = NULL,
    restorable = restorable AND NOT sqlc.arg(unrestorable)::boolean
WHERE id = sqlc.arg(id)::uuid AND status = 'watching';

-- name: OpenAppConfigGuardWindow :execrows
UPDATE app_config_guards SET window_ends_at = sqlc.arg(window_ends_at)::timestamptz
WHERE id = sqlc.arg(id)::uuid AND status = 'watching' AND window_ends_at IS NULL
  AND last_change_at = sqlc.arg(last_change_at)::timestamptz;

-- name: CloseAppConfigGuard :execrows
-- expect_head = 0 closes regardless of later changes; otherwise the guard
-- must still be judging the head the caller evaluated.
UPDATE app_config_guards
SET status = sqlc.arg(status)::text, decision = sqlc.arg(decision)::jsonb, closed_at = sqlc.arg(closed_at)::timestamptz
WHERE id = sqlc.arg(id)::uuid AND status = 'watching'
  AND (sqlc.arg(expect_head)::bigint = 0 OR head_revision = sqlc.arg(expect_head)::bigint);

-- name: DeleteAppConfigGuardSecrets :exec
DELETE FROM app_config_guard_secrets WHERE guard_id = sqlc.arg(guard_id)::uuid;

-- name: ListWatchingAppConfigGuards :many
SELECT * FROM app_config_guards WHERE status = 'watching'
ORDER BY last_change_at, id LIMIT sqlc.arg(row_limit)::int;

-- name: GetAppConfigGuard :one
SELECT * FROM app_config_guards WHERE id = sqlc.arg(id)::uuid;

-- name: LatestAppConfigGuard :one
SELECT * FROM app_config_guards
WHERE app_id = sqlc.arg(app_id)::uuid AND scope = sqlc.arg(scope)::text
ORDER BY opened_at DESC, id DESC LIMIT 1;

-- name: ListAppConfigGuardSecrets :many
SELECT * FROM app_config_guard_secrets WHERE guard_id = sqlc.arg(guard_id)::uuid ORDER BY config_key;

-- name: AppConfigGuardTelemetry :one
-- New config: requests from the minute of the latest change on, served by an
-- instance started after it or failing before any instance served them.
-- Old config: requests in the baseline window before the guard opened.
-- Telemetry is minute-bucketed, so instance start time attributes the change
-- minute. Only deployments that existed when the guard opened count: a
-- release deployed during the guard is judged by its own rollout, not
-- blamed on the config. Uses request_telemetry_app_dep_received_idx.
WITH scoped AS (
    SELECT d.id FROM deployments d
    WHERE d.app_id = sqlc.arg(app_id)::uuid AND COALESCE(NULLIF(d.scope, ''), 'default') = sqlc.arg(scope)::text
      AND d.created_at <= sqlc.arg(opened_at)::timestamptz
), rows AS (
    SELECT t.count::bigint AS weight, t.status,
        (t.received_at >= date_trunc('minute', sqlc.arg(changed_at)::timestamptz) AND (
            NULLIF(t.instance_id, '') IS NULL
            OR (i.id IS NOT NULL AND (i.started_at IS NULL OR i.started_at >= sqlc.arg(changed_at)::timestamptz)))) AS fresh,
        (t.received_at >= sqlc.arg(baseline_from)::timestamptz
            AND t.received_at < date_trunc('minute', sqlc.arg(opened_at)::timestamptz)) AS old
    FROM request_telemetry t
    LEFT JOIN instances i ON i.id = CASE WHEN t.instance_id ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
        THEN t.instance_id::uuid END
    WHERE t.app_id = sqlc.arg(app_id)::uuid AND t.deployment_id IN (SELECT id FROM scoped)
      AND t.received_at >= sqlc.arg(baseline_from)::timestamptz AND t.received_at < sqlc.arg(until)::timestamptz
)
SELECT
    COALESCE(SUM(weight) FILTER (WHERE fresh), 0)::bigint AS new_requests,
    COALESCE(SUM(weight) FILTER (WHERE fresh AND status >= 500 AND status < 600), 0)::bigint AS new_server_errors,
    COALESCE(SUM(weight) FILTER (WHERE old), 0)::bigint AS old_requests,
    COALESCE(SUM(weight) FILTER (WHERE old AND status >= 500 AND status < 600), 0)::bigint AS old_server_errors
FROM rows;
