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

-- name: SnapshotSealedKey :one
-- A sealed park or warm snapshot's app and key (ADR-20261110210000 slice
-- 3d-3), through its deployment.
SELECT d.app_id, s.sealed_key FROM snapshots s JOIN deployments d ON d.id = s.deployment_id
 WHERE s.sealed_capture_id = sqlc.arg(id)::uuid AND s.sealed_key IS NOT NULL AND NOT s.delete_pending
 ORDER BY s.created_at DESC LIMIT 1;

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
-- The build's app, status, start and claiming node (slice 3j-3), and the
-- registry repositories its deployment may pull (slice 3j-4; not recorded
-- when NULL).
SELECT d.app_id, b.status, coalesce(b.started_at, '-infinity'::timestamptz)::timestamptz AS started_at,
       coalesce(b.builder_node_name, '')::text AS builder_node_name,
       (d.build_registry_scope IS NOT NULL)::boolean AS scope_recorded,
       coalesce(d.build_registry_scope, '{}'::text[])::text[] AS build_registry_scope
  FROM builds b JOIN deployments d ON d.id = b.deployment_id
 WHERE b.id = sqlc.arg(id)::uuid;

-- name: InsertJobSecretOpenAudit :exec
INSERT INTO secret_open_audit (node_id, job_id, via, entries)
VALUES (sqlc.arg(node_id)::uuid, sqlc.arg(job_id)::uuid, sqlc.arg(via)::text, 1);

-- name: TriggerConfig :one
-- A trigger of the named app only (ADR-20261110210000 slice 3g).
SELECT kind, config FROM triggers
 WHERE id = sqlc.arg(id)::uuid AND app_id = sqlc.arg(app_id)::uuid;

-- name: PostgresCutoverVerification :one
SELECT c.account_id, c.app_id, m.environment_key, m.access, d.postgres_major,
       m.provider_identity_id, m.credential_ref, m.ciphertext, m.kid, m.value_hash
FROM managed_postgres_cutovers c
JOIN managed_postgres_cutover_credentials m ON m.cutover_id=c.id
JOIN managed_postgres_databases d ON d.id=c.target_database_id AND d.account_id=c.account_id
WHERE c.id=sqlc.arg(cutover_id)::uuid AND c.account_id=sqlc.arg(account_id)::uuid
  AND m.id=sqlc.arg(credential_id)::uuid AND m.state='sealed'
  AND c.state='verifying' AND c.lease_token=sqlc.arg(lease_token)::text AND c.lease_until>clock_timestamp()
  AND d.state='ready' AND d.backend_id=c.target_backend_id
  AND d.backend_fingerprint=c.target_backend_fingerprint
  AND d.provider_resource_id=c.target_resource_id AND d.desired_generation=c.target_generation;

-- name: RecordPostgresVerification :exec
INSERT INTO secret_verification_audit(account_id,app_id,cutover_id,credential_id)
VALUES(sqlc.arg(account_id)::uuid,sqlc.arg(app_id)::uuid,sqlc.arg(cutover_id)::uuid,sqlc.arg(credential_id)::uuid);

-- name: DeploymentAppID :one
-- The app a deployment belongs to (ADR-20261117190000 slice 2).
SELECT app_id FROM deployments WHERE id = sqlc.arg(id)::uuid;

-- name: NodeRunsApp :one
-- An instance of the app on the node that is not over.
SELECT EXISTS (
    SELECT 1 FROM instances
     WHERE app_id = sqlc.arg(app_id)::uuid AND node_id = sqlc.arg(node_id)::uuid
       AND state NOT IN ('stopped', 'failed', 'evicting_account_deleting')
) AS runs;

-- name: NodeRunsJob :one
-- A task instance of the job on the node that is not over, or a live
-- sandbox session on the node that boots the job's image.
SELECT (EXISTS (
    SELECT 1 FROM instances
     WHERE job_id = sqlc.arg(job_id)::uuid AND node_id = sqlc.arg(node_id)::uuid
       AND state NOT IN ('stopped', 'failed', 'evicting_account_deleting')
) OR EXISTS (
    SELECT 1 FROM sandbox_sessions
     WHERE image_job_id = sqlc.arg(job_id)::uuid
       AND (node_id = sqlc.arg(node_id)::text OR placed_node_id = sqlc.arg(node_id)::text)
       AND state IN ('starting', 'ready', 'frozen')
))::boolean AS runs;

-- name: NodeRunsSandboxTemplateOf :one
-- A live sandbox session on the node whose template is built by the app:
-- the session boots that app's layer (reads only).
SELECT EXISTS (
    SELECT 1 FROM sandbox_sessions s JOIN sandbox_templates t ON t.id = s.template_id
     WHERE t.app_id = sqlc.arg(app_id)::uuid
       AND (s.node_id = sqlc.arg(node_id)::text OR s.placed_node_id = sqlc.arg(node_id)::text)
       AND s.state IN ('starting', 'ready', 'frozen')
) AS runs;

-- name: NodeBuildsDeployment :one
-- A running build of the deployment the node claimed, started since since.
SELECT EXISTS (
    SELECT 1 FROM builds
     WHERE deployment_id = sqlc.arg(deployment_id)::uuid AND builder_node_name = sqlc.arg(node_name)::text
       AND status = 'running' AND started_at > sqlc.arg(since)::timestamptz
) AS builds;

-- name: NodeRunsSandboxOf :one
-- A live sandbox session of the account placed on the node.
SELECT EXISTS (
    SELECT 1 FROM sandbox_sessions
     WHERE account_id = sqlc.arg(account_id)::uuid
       AND (node_id = sqlc.arg(node_id)::text OR placed_node_id = sqlc.arg(node_id)::text)
       AND state IN ('starting', 'ready', 'frozen')
) AS runs;

-- name: InsertStorageGrantAudit :exec
INSERT INTO storage_grant_audit (node_id, subject_class, subject_id, app_id, access, objects)
VALUES (sqlc.arg(node_id)::uuid, sqlc.arg(subject_class)::text, sqlc.arg(subject_id)::uuid,
        sqlc.narg(app_id)::uuid, sqlc.arg(access)::text, sqlc.arg(objects)::int);

-- name: VerificationLease :one
-- The node verifying a deployment, the deployment's status and scope, and the
-- app's stored verification contract (ADR-20261122220000 amendment 2).
SELECT l.app_id, l.node_name, l.expires_at, d.status, d.scope,
       c.contract::jsonb AS contract
  FROM deployment_verification_leases l
  JOIN deployments d ON d.id = l.deployment_id AND d.app_id = l.app_id
  LEFT JOIN app_verification_contracts c ON c.app_id = l.app_id
 WHERE l.deployment_id = sqlc.arg(deployment_id)::uuid;
