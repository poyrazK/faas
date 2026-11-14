-- name: UpsertSecretGrant :one
INSERT INTO secret_grants (app_id, node_id, granted_by_node_id, expires_at)
VALUES (sqlc.arg(app_id)::uuid, sqlc.arg(node_id)::uuid, sqlc.arg(granted_by_node_id)::uuid, sqlc.arg(expires_at)::timestamptz)
ON CONFLICT (app_id, node_id) DO UPDATE
   SET granted_by_node_id = excluded.granted_by_node_id,
       granted_at = now(),
       expires_at = excluded.expires_at
RETURNING expires_at;

-- name: SecretGrantValid :one
SELECT EXISTS (
    SELECT 1 FROM secret_grants
     WHERE app_id = sqlc.arg(app_id)::uuid
       AND node_id = sqlc.arg(node_id)::uuid
       AND expires_at > sqlc.arg(at)::timestamptz
) AS valid;

-- name: DeleteExpiredSecretGrants :execrows
DELETE FROM secret_grants WHERE expires_at <= sqlc.arg(before)::timestamptz;

-- name: InsertSecretOpenAudit :exec
INSERT INTO secret_open_audit (node_id, app_id, via, entries)
VALUES (sqlc.arg(node_id)::uuid, sqlc.arg(app_id)::uuid, sqlc.arg(via)::text, sqlc.arg(entries)::integer);

-- name: CrashCaptureSealedKey :one
-- A live capture's app and sealed key (ADR-20261110210000 slice 3d).
SELECT app_id, sealed_key FROM crash_captures
 WHERE id = sqlc.arg(id)::uuid AND status <> 'expired' AND sealed_key IS NOT NULL;

-- name: AppSecretCiphertext :one
-- The app's own row (ADR-20261110210000 slice 3d-4).
SELECT ciphertext FROM app_secrets
 WHERE app_id = sqlc.arg(app_id)::uuid AND scope = sqlc.arg(scope)::text AND key = sqlc.arg(key)::text;

-- name: LogDrainHeaderSealed :one
-- A drain of the named app only (ADR-20261110210000 slice 3f).
SELECT auth_header_sealed FROM app_log_drains
 WHERE id = sqlc.arg(id)::uuid AND app_id = sqlc.arg(app_id)::uuid;

-- name: EdgeHeaderSecretSealed :one
-- A header value of the named app that one of its headers rules still
-- references (ADR-20261110210000 slice 3i).
SELECT s.ciphertext FROM edge_rule_header_secrets s
 WHERE s.id = sqlc.arg(id)::uuid AND s.app_id = sqlc.arg(app_id)::uuid
   AND EXISTS (
     SELECT 1 FROM edge_rules r,
       jsonb_array_elements(CASE WHEN jsonb_typeof(r.action->'headers'->'request_headers') = 'array'
         THEN r.action->'headers'->'request_headers' ELSE '[]'::jsonb END) op
      WHERE r.app_id = s.app_id AND r.kind = 'headers' AND op->>'secret_id' = s.id::text);

-- name: RegistryCredentialSealed :one
-- The app's own credential for one registry host (ADR-20261110210000 slice 3j).
SELECT username, password_encrypted FROM app_registry_credentials
 WHERE app_id = sqlc.arg(app_id)::uuid AND registry = sqlc.arg(registry)::text;

-- name: NodePreparesAppImage :one
-- An unfinished image preparation of one of the app's deployments held by
-- the node (ADR-20261110210000 slice 3j-2).
SELECT EXISTS (
    SELECT 1 FROM deployment_image_preparations p
      JOIN deployments d ON d.id = p.deployment_id
     WHERE d.app_id = sqlc.arg(app_id)::uuid
       AND p.node_name = sqlc.arg(node_name)::text
       AND p.phase <> 'handed_off'
       AND p.updated_at > sqlc.arg(since)::timestamptz
) AS preparing;

-- name: JobRegistryCredentialSealed :one
-- The job's own credential for one registry host (slice 3j-2b).
SELECT username, password_encrypted FROM job_registry_credentials
 WHERE job_id = sqlc.arg(job_id)::uuid AND registry = sqlc.arg(registry)::text;

-- name: JobMaterializationLease :one
SELECT coalesce(image_materialization_lease_owner, '')::text AS owner,
       coalesce(image_materialization_lease_until, '-infinity'::timestamptz)::timestamptz AS until
  FROM jobs WHERE id = sqlc.arg(id)::uuid;

-- name: BuildRunner :one
-- The build's app, status, start and claiming node (slice 3j-3).
SELECT d.app_id, b.status, coalesce(b.started_at, '-infinity'::timestamptz)::timestamptz AS started_at,
       coalesce(b.builder_node_name, '')::text AS builder_node_name
  FROM builds b JOIN deployments d ON d.id = b.deployment_id
 WHERE b.id = sqlc.arg(id)::uuid;

-- name: InsertJobSecretOpenAudit :exec
INSERT INTO secret_open_audit (node_id, job_id, via, entries)
VALUES (sqlc.arg(node_id)::uuid, sqlc.arg(job_id)::uuid, sqlc.arg(via)::text, 1);

-- name: TriggerConfig :one
-- A trigger of the named app only (ADR-20261110210000 slice 3g).
SELECT kind, config FROM triggers
 WHERE id = sqlc.arg(id)::uuid AND app_id = sqlc.arg(app_id)::uuid;
