-- name: ReadSnapshotPublicationDeployment :one
SELECT a.id::text AS app_id, a.account_id::text AS account_id
FROM apps a JOIN deployments d ON d.app_id = a.id
WHERE d.id = sqlc.arg(deployment_id)::uuid;

-- name: LockSnapshotPublicationApp :one
SELECT id FROM apps WHERE id = sqlc.arg(app_id)::uuid AND account_id = sqlc.arg(account_id)::uuid
FOR UPDATE;

-- name: LockSnapshotPublicationDeployment :one
SELECT id FROM deployments WHERE id = sqlc.arg(deployment_id)::uuid AND app_id = sqlc.arg(app_id)::uuid
FOR SHARE;

-- name: ReadSnapshotPublicationSource :one
SELECT coalesce(app_id::text, '')::text AS app_id, deployment_id::text AS deployment_id, started_at
FROM instances WHERE id = sqlc.arg(instance_id)::uuid FOR SHARE;

-- name: ReadSnapshotPublicationConfigChange :one
SELECT changed_at FROM app_runtime_config_changes WHERE app_id = sqlc.arg(app_id)::uuid;

-- name: ListWarmPoolReconciliationAppIDs :many
SELECT a.id::text AS app_id FROM apps a
WHERE a.status <> 'deleted'
  AND (sqlc.arg(node_id)::text = '' OR a.node_id = nullif(sqlc.arg(node_id)::text, '')::uuid)
  AND (a.warm_pool_size > 0
       OR EXISTS (SELECT 1 FROM instances i WHERE i.app_id = a.id AND i.state = 'warm')
       OR EXISTS (
           SELECT 1 FROM deployments d
           JOIN project_environment_workload_deployment_specs p ON p.deployment_id = d.id
           JOIN project_environment_workload_specs s ON s.id = p.spec_id AND s.app_id = d.app_id
           WHERE d.app_id = a.id AND d.status = 'live'
             AND (s.settings -> 'warm_pool_size')::jsonb > '0'::jsonb
       ))
ORDER BY a.id;

-- name: LockProjectEnvironmentCloneApps :many
SELECT id::text AS app_id FROM apps
WHERE account_id = sqlc.arg(account_id)::uuid
  AND project_id = sqlc.arg(project_id)::uuid
  AND status <> 'deleted' AND preview_of_slug IS NULL
ORDER BY id FOR UPDATE;

-- name: ReadProjectEnvironmentCloneObjectCopyProofs :many
SELECT m.source_bucket_id::text AS source_bucket_id, m.target_bucket_id::text AS target_bucket_id,
       m.manifest_hash, m.captured_at_exact, m.object_count,
       count(e.object_key)::integer AS entry_count,
       count(e.object_key) FILTER (WHERE e.copied_at IS NOT NULL AND e.target_etag <> ''
                                  AND e.verified_sha256 ~ '^[a-f0-9]{64}$')::integer AS verified_count
FROM project_environment_clone_object_manifests m
LEFT JOIN project_environment_clone_object_entries e
  ON e.operation_id = m.operation_id AND e.source_bucket_id = m.source_bucket_id
WHERE m.operation_id = sqlc.arg(operation_id)::uuid
GROUP BY m.source_bucket_id, m.target_bucket_id, m.manifest_hash, m.captured_at_exact, m.object_count
ORDER BY m.source_bucket_id;

-- name: RegisterLayerArtifactRetention :exec
INSERT INTO layer_artifact_retention(storage_key) VALUES (sqlc.arg(storage_key)::text) ON CONFLICT DO NOTHING;

-- name: LockLayerArtifactRetention :one
SELECT storage_key, state, coalesce(deletion_id::text, '')::text AS deletion_id
FROM layer_artifact_retention WHERE storage_key = sqlc.arg(storage_key)::text FOR UPDATE;

-- name: ClaimLayerArtifactDeletion :execrows
UPDATE layer_artifact_retention SET state = 'deleting', deletion_id = sqlc.arg(deletion_id)::uuid
WHERE storage_key = sqlc.arg(storage_key)::text AND state = 'retained';

-- name: RequestLayerArtifactDeletion :exec
UPDATE layer_artifact_retention SET delete_requested_at = coalesce(delete_requested_at, clock_timestamp())
WHERE storage_key = sqlc.arg(storage_key)::text;

-- name: CompleteLayerArtifactDeletion :execrows
UPDATE layer_artifact_retention SET state = 'deleted', deleted_at = coalesce(deleted_at, clock_timestamp())
WHERE storage_key = sqlc.arg(storage_key)::text AND deletion_id = sqlc.arg(deletion_id)::uuid
  AND state IN ('deleting', 'deleted');

-- name: PendingLayerArtifactDeletions :many
SELECT storage_key, state, coalesce(deletion_id::text, '')::text AS deletion_id FROM layer_artifact_retention
WHERE state = 'deleting' OR (state = 'retained' AND delete_requested_at IS NOT NULL) ORDER BY storage_key;

-- name: InsertProjectEnvironmentCloneLayerPin :exec
INSERT INTO project_environment_clone_layer_pins(operation_id, app_id, storage_key, bytes)
VALUES (sqlc.arg(operation_id)::uuid, sqlc.arg(app_id)::uuid, sqlc.arg(storage_key)::text, sqlc.arg(bytes)::bigint)
ON CONFLICT (operation_id, app_id, storage_key) DO NOTHING;

-- name: ReadDeploymentLayerArtifactKeys :many
SELECT key FROM (
    SELECT d.rootfs_key::text AS key FROM deployments d WHERE d.id = sqlc.arg(deployment_id)::uuid
    UNION SELECT l.storage_key::text FROM deployment_sidecar_layers l WHERE l.deployment_id = sqlc.arg(deployment_id)::uuid
) keys WHERE coalesce(key, '') <> '' ORDER BY key;

-- name: LayerArtifactHasReferences :one
SELECT EXISTS (
    SELECT 1 FROM deployments d JOIN apps a ON a.id = d.app_id
    WHERE a.status <> 'deleted'
      AND ((d.deleted_at IS NULL AND d.status IN ('pending', 'building', 'imaging', 'snapshotting', 'live'))
           OR EXISTS (SELECT 1 FROM snapshots sn WHERE sn.deployment_id = d.id AND NOT sn.stale)
           OR EXISTS (SELECT 1 FROM instances i WHERE i.deployment_id = d.id AND i.state IN ('pending', 'waking', 'cold_booting', 'running', 'snapshotting', 'migrating', 'warm', 'draining'))
           OR EXISTS (SELECT 1 FROM deployment_aliases al WHERE al.deployment_id = d.id)
           OR EXISTS (SELECT 1 FROM project_release_members rm JOIN project_release_sets rs ON rs.id = rm.release_id
                      WHERE rm.deployment_id = d.id AND (rs.active OR rs.expires_at > clock_timestamp())))
      AND (d.rootfs_key = sqlc.arg(storage_key)::text
           OR sqlc.arg(storage_key)::text = 'apps/' || a.slug || '/' || d.id::text || '.ext4'
           OR left(sqlc.arg(storage_key)::text, length('apps/' || a.slug || '/' || d.id::text || '/')) = 'apps/' || a.slug || '/' || d.id::text || '/'
           OR EXISTS (SELECT 1 FROM deployment_sidecar_layers l WHERE l.deployment_id = d.id AND l.storage_key = sqlc.arg(storage_key)::text))
) OR EXISTS (
    SELECT 1 FROM project_environment_clone_layer_pins p
    JOIN project_environment_clone_operations op ON op.id = p.operation_id
    JOIN apps a ON a.id = p.app_id
    WHERE p.storage_key = sqlc.arg(storage_key)::text AND a.status <> 'deleted'
      AND op.status NOT IN ('ready', 'compensated')
) AS referenced;

-- name: RetainedLayerBytesWithClonePins :one
WITH retained_deployments AS (
    SELECT d.* FROM deployments d JOIN apps a ON a.id = d.app_id
    WHERE d.app_id = sqlc.arg(app_id)::uuid AND a.status <> 'deleted'
      AND (d.deleted_at IS NULL
           OR EXISTS (SELECT 1 FROM snapshots sn WHERE sn.deployment_id = d.id AND NOT sn.stale)
           OR EXISTS (SELECT 1 FROM instances i WHERE i.deployment_id = d.id AND i.state IN ('pending', 'waking', 'cold_booting', 'running', 'snapshotting', 'migrating', 'warm', 'draining'))
           OR EXISTS (SELECT 1 FROM deployment_aliases al WHERE al.deployment_id = d.id)
           OR EXISTS (SELECT 1 FROM project_release_members rm JOIN project_release_sets rs ON rs.id = rm.release_id
                      WHERE rm.deployment_id = d.id AND (rs.active OR rs.expires_at > clock_timestamp())))
)
SELECT coalesce(sum(retained.bytes), 0)::bigint AS retained_bytes FROM (
    SELECT storage_key, max(bytes)::bigint AS bytes FROM (
        SELECT coalesce(nullif(d.rootfs_key, ''), nullif(d.rootfs_path, ''))::text AS storage_key,
               greatest(coalesce(d.rootfs_bytes, 0), 0)::bigint AS bytes
        FROM retained_deployments d WHERE coalesce(d.rootfs_bytes, 0) > 0
        UNION ALL
        SELECT l.storage_key::text, greatest(l.bytes, 0)::bigint
        FROM deployment_sidecar_layers l JOIN retained_deployments d ON d.id = l.deployment_id WHERE l.bytes > 0
        UNION ALL
        SELECT p.storage_key::text, p.bytes FROM project_environment_clone_layer_pins p
        JOIN project_environment_clone_operations op ON op.id = p.operation_id JOIN apps a ON a.id = p.app_id
        WHERE p.app_id = sqlc.arg(app_id)::uuid AND a.status <> 'deleted' AND op.status NOT IN ('ready', 'compensated')
    ) layers WHERE coalesce(storage_key, '') <> '' GROUP BY storage_key
) retained;

-- name: LockLayerArtifactDeployment :one
SELECT id::text FROM deployments WHERE id = sqlc.arg(deployment_id)::uuid FOR UPDATE;

-- name: LockLayerArtifactApp :one
SELECT id::text FROM apps WHERE id = sqlc.arg(app_id)::uuid AND status <> 'deleted' FOR UPDATE;

-- name: ReadProjectEnvironmentCloneVariables :many
SELECT e.app_id::text AS app_id, e.scope, e.key, e.value
FROM app_envs e JOIN apps a ON a.id = e.app_id
WHERE a.account_id = sqlc.arg(account_id)::uuid AND a.project_id = sqlc.arg(project_id)::uuid
  AND a.status <> 'deleted' AND a.preview_of_slug IS NULL
  AND e.scope = (sqlc.arg(value_scopes)::jsonb ->> a.id::text)
ORDER BY e.app_id, e.key;

-- name: ReadProjectEnvironmentCloneValueQuota :many
SELECT a.id::text AS app_id, a.slug,
       (SELECT count(*) FROM app_secrets s WHERE s.app_id = a.id)::bigint AS secret_count,
       (SELECT count(*) FROM app_envs e WHERE e.app_id = a.id)::bigint AS variable_count
FROM apps a
WHERE a.account_id = sqlc.arg(account_id)::uuid AND a.project_id = sqlc.arg(project_id)::uuid
  AND a.status <> 'deleted' AND a.preview_of_slug IS NULL
ORDER BY a.id;

-- name: InsertProjectEnvironmentCloneCapturedVariable :exec
INSERT INTO app_envs (account_id, app_id, scope, key, value)
VALUES (sqlc.arg(account_id)::uuid, sqlc.arg(app_id)::uuid, sqlc.arg(target_scope)::text,
        sqlc.arg(key)::text, sqlc.arg(value)::text);

-- name: InsertProjectEnvironmentCloneCapturedSecret :exec
-- Managed ownership columns are deliberately absent: provider credentials
-- must be recreated against isolated target bindings by their owner.
INSERT INTO app_secrets (account_id, app_id, scope, key, ciphertext, kid, value_hash, secret_version, secret_class)
VALUES (sqlc.arg(account_id)::uuid, sqlc.arg(app_id)::uuid, sqlc.arg(target_scope)::text,
        sqlc.arg(key)::text, sqlc.arg(ciphertext)::bytea, nullif(sqlc.arg(kid)::text, ''),
        nullif(sqlc.arg(value_hash)::text, ''), nullif(sqlc.arg(secret_version)::bigint, 0), sqlc.arg(secret_class)::text);

-- name: ReadProjectEnvironmentCloneSecrets :many
-- Decode the explicit configuration fields in Go; delivery observations do
-- not enter the fingerprint. No encrypted content leaves the store boundary.
SELECT to_jsonb(s)::jsonb AS secret
FROM app_secrets s JOIN apps a ON a.id = s.app_id
WHERE a.account_id = sqlc.arg(account_id)::uuid AND a.project_id = sqlc.arg(project_id)::uuid
  AND a.status <> 'deleted' AND a.preview_of_slug IS NULL
  AND s.scope = (sqlc.arg(value_scopes)::jsonb ->> a.id::text)
ORDER BY s.app_id, s.key;

-- name: ReadProjectEnvironmentCloneProductionValueScope :one
-- Empty scope means that an active graph has an invalid or missing member.
SELECT coalesce((CASE
  WHEN EXISTS (SELECT 1 FROM project_release_sets rs
      WHERE rs.project_id = sqlc.arg(project_id)::uuid
        AND rs.environment_slug = 'production' AND rs.active) THEN
    (SELECT d.scope FROM project_release_sets rs
       JOIN project_release_members rm ON rm.release_id = rs.id
         AND rm.app_id = sqlc.arg(app_id)::uuid
       JOIN deployments d ON d.id = rm.deployment_id AND d.app_id = sqlc.arg(app_id)::uuid
     WHERE rs.project_id = sqlc.arg(project_id)::uuid
       AND rs.environment_slug = 'production' AND rs.active
       AND d.status = 'live' AND d.scope = 'production')
  ELSE coalesce(
    (SELECT d.scope FROM deployments d
      WHERE d.app_id = sqlc.arg(app_id)::uuid AND d.status = 'live'
        AND d.scope IN ('production', 'default') AND d.traffic_percent > 0
      ORDER BY (d.scope = 'production') DESC, d.created_at DESC, d.id DESC LIMIT 1),
    CASE WHEN EXISTS (SELECT 1 FROM app_envs e
                       WHERE e.app_id = sqlc.arg(app_id)::uuid AND e.scope = 'production')
           OR EXISTS (SELECT 1 FROM app_secrets s
                       WHERE s.app_id = sqlc.arg(app_id)::uuid AND s.scope = 'production')
         THEN 'production' ELSE 'default' END)
END)::text, '')::text AS source_scope;
-- name: ClaimNotificationForNode :one
WITH candidate AS (
    SELECT id
    FROM notification_outbox
    WHERE channel = ANY(sqlc.arg(channels)::text[])
      -- A fixed-size index key accepts oversized poison events. The full
      -- identity check is required too; hash equality never grants ownership.
      AND (sqlc.arg(node_id)::text = '' OR
           (md5(notification_outbox_target_node(channel, payload)) IN (md5(''), md5(sqlc.arg(node_id)::text))
            AND notification_outbox_target_node(channel, payload) IN ('', sqlc.arg(node_id)::text)))
      AND ((state = 'pending' AND available_at <= now()) OR
           (state = 'processing' AND lease_until <= clock_timestamp()))
    ORDER BY id
    FOR UPDATE SKIP LOCKED
    LIMIT 1
)
UPDATE notification_outbox o
SET state = 'processing', attempts = o.attempts + 1,
    claimed_by = sqlc.arg(claim_token)::text, claimed_at = now(),
    lease_until = clock_timestamp() + sqlc.arg(lease_milliseconds)::bigint * interval '1 millisecond'
FROM candidate
WHERE o.id = candidate.id
RETURNING o.id, o.channel, o.payload, o.attempts,
          COALESCE(o.claimed_by, '')::text AS claim_token;

-- name: ClaimImmediateNotificationForNode :one
WITH candidate AS (
    SELECT id
    FROM notification_outbox
    WHERE id = sqlc.arg(id)::bigint AND channel = sqlc.arg(channel)::text
      AND (sqlc.arg(node_id)::text = '' OR
           notification_outbox_target_node(channel, payload) IN ('', sqlc.arg(node_id)::text))
      -- Only the first delivery bypasses the LISTEN grace period. A repeated
      -- notification must not bypass a failed handler's retry backoff.
      AND ((state = 'pending' AND (attempts = 0 OR available_at <= now())) OR
           (state = 'processing' AND lease_until <= clock_timestamp()))
    FOR UPDATE SKIP LOCKED
)
UPDATE notification_outbox o
SET state = 'processing', attempts = o.attempts + 1,
    claimed_by = sqlc.arg(claim_token)::text, claimed_at = now(),
    lease_until = clock_timestamp() + sqlc.arg(lease_milliseconds)::bigint * interval '1 millisecond'
FROM candidate
WHERE o.id = candidate.id
RETURNING o.id, o.channel, o.payload, o.attempts,
          COALESCE(o.claimed_by, '')::text AS claim_token;

-- name: RenewNotificationClaim :execrows
-- Materialize the locked row before evaluating expiry. A valid predicate
-- evaluated before waiting for a row lock must not resurrect an expired lease.
WITH owned AS MATERIALIZED (
    SELECT id, lease_until FROM notification_outbox
    WHERE id = sqlc.arg(id)::bigint AND state = 'processing'
      AND claimed_by = sqlc.arg(claim_token)::text
    FOR UPDATE
)
UPDATE notification_outbox o
SET lease_until = clock_timestamp() + sqlc.arg(lease_milliseconds)::bigint * interval '1 millisecond'
FROM owned
WHERE o.id = owned.id AND owned.lease_until > clock_timestamp();

-- name: CompleteNotificationClaim :execrows
WITH owned AS MATERIALIZED (
    SELECT id, lease_until FROM notification_outbox
    WHERE id = sqlc.arg(id)::bigint AND state = 'processing'
      AND claimed_by = sqlc.arg(claim_token)::text
    FOR UPDATE
)
UPDATE notification_outbox o
SET state = 'delivered', delivered_at = now(),
    claimed_by = NULL, claimed_at = NULL, lease_until = NULL, last_error = NULL
FROM owned
WHERE o.id = owned.id AND owned.lease_until > clock_timestamp();

-- name: AcknowledgePendingNotification :exec
-- Legacy scheduler subscribers cannot acknowledge another worker's lease.
UPDATE notification_outbox
SET state = 'delivered', delivered_at = now(),
    claimed_by = NULL, claimed_at = NULL, lease_until = NULL, last_error = NULL
WHERE id = sqlc.arg(id)::bigint AND state = 'pending';

-- name: NotificationClaimAttempts :one
SELECT attempts FROM notification_outbox
WHERE id = sqlc.arg(id)::bigint AND state = 'processing'
  AND claimed_by = sqlc.arg(claim_token)::text AND lease_until > clock_timestamp();

-- name: FailNotificationClaim :execrows
WITH owned AS MATERIALIZED (
    SELECT id, lease_until FROM notification_outbox
    WHERE id = sqlc.arg(id)::bigint AND state = 'processing'
      AND claimed_by = sqlc.arg(claim_token)::text
    FOR UPDATE
)
UPDATE notification_outbox o
SET state = CASE WHEN o.attempts >= sqlc.arg(max_attempts)::integer THEN 'dead_letter' ELSE 'pending' END,
    available_at = clock_timestamp() + sqlc.arg(retry_milliseconds)::bigint * interval '1 millisecond',
    claimed_by = NULL, claimed_at = NULL, lease_until = NULL,
    last_error = sqlc.arg(message)::text
FROM owned
WHERE o.id = owned.id AND owned.lease_until > clock_timestamp();

-- name: ReleaseUnownedNotification :execrows
WITH owned AS MATERIALIZED (
    SELECT id, lease_until FROM notification_outbox
    WHERE id = sqlc.arg(id)::bigint AND state = 'processing'
      AND claimed_by = sqlc.arg(claim_token)::text AND attempts > 0
    FOR UPDATE
)
UPDATE notification_outbox o
SET state = 'pending', attempts = o.attempts - 1,
    claimed_by = NULL, claimed_at = NULL, lease_until = NULL
FROM owned
WHERE o.id = owned.id AND owned.lease_until > clock_timestamp();

-- name: LockDeploymentHostingFailure :one
SELECT app_id, status FROM deployments
WHERE id = sqlc.arg(deployment_id)::uuid
FOR UPDATE;

-- name: LockDeploymentHostingVerification :one
SELECT status, stage_state FROM deployments
WHERE id = sqlc.arg(deployment_id)::uuid
FOR UPDATE;

-- name: WriteDeploymentHostingVerification :execrows
UPDATE deployments
SET stage_state = jsonb_set(stage_state, '{hosting_verification}', sqlc.arg(progress)::jsonb)
WHERE id = sqlc.arg(deployment_id)::uuid AND status = 'snapshotting';

-- name: WriteDeploymentHostingFailureReceipt :execrows
UPDATE deployments SET api_hosting_receipt = sqlc.arg(receipt)::jsonb
WHERE id = sqlc.arg(deployment_id)::uuid AND status = 'snapshotting';

-- name: PutTCPListenerTLSObservation :execrows
INSERT INTO app_tcp_listener_tls_observations
    (listener_id, edge_id, hostname, intent_updated_at, observed_at, ready, not_after)
SELECT l.id, sqlc.arg(edge_id)::text, sqlc.arg(hostname)::text,
       sqlc.arg(intent_updated_at)::timestamptz, sqlc.arg(observed_at)::timestamptz,
       sqlc.arg(ready)::boolean, sqlc.narg(not_after)::timestamptz
FROM app_tcp_listeners l
WHERE l.id = sqlc.arg(listener_id)::uuid AND l.enabled
  AND l.tls_mode = 'terminate' AND l.tls_hostname = sqlc.arg(hostname)::text
  AND l.updated_at = sqlc.arg(intent_updated_at)::timestamptz
ON CONFLICT (listener_id, edge_id) DO UPDATE
SET hostname = EXCLUDED.hostname, intent_updated_at = EXCLUDED.intent_updated_at,
    observed_at = EXCLUDED.observed_at, ready = EXCLUDED.ready, not_after = EXCLUDED.not_after
WHERE app_tcp_listener_tls_observations.observed_at < EXCLUDED.observed_at;

-- name: ListTCPListenerTLSObservations :many
SELECT listener_id, edge_id, hostname, intent_updated_at, observed_at, ready, not_after
FROM app_tcp_listener_tls_observations
WHERE listener_id = sqlc.arg(listener_id)::uuid
ORDER BY edge_id;

-- name: PruneTCPListenerTLSObservations :execrows
DELETE FROM app_tcp_listener_tls_observations
WHERE observed_at <= sqlc.arg(before_at)::timestamptz;

-- name: ReadAccountCreditConsumption :one
-- An unqualified legacy row blocks the whole key; guessing could double-debit.
SELECT coalesce(sum(-delta_cents) FILTER (WHERE provider = sqlc.arg(provider)::text), 0)::bigint AS consumed_cents,
       coalesce(bool_or(delta_cents < 0) FILTER (WHERE provider = sqlc.arg(provider)), false)::boolean AS has_prior,
       coalesce(bool_or(provider = ''), false)::boolean AS has_unqualified
FROM credit_ledger
WHERE account_id = sqlc.arg(account_id)::uuid
  AND provider_invoice_id = sqlc.arg(provider_invoice_id)::text;

-- name: ReverseAccountInvoiceCreditConsumption :execrows
WITH consumed AS (
    SELECT ledger.credit_id, sum(-ledger.delta_cents)::bigint AS cents
    FROM credit_ledger AS ledger
    JOIN account_credits AS credit
      ON credit.id = ledger.credit_id AND credit.account_id = ledger.account_id
    WHERE ledger.account_id = sqlc.arg(account_id)::uuid
      AND ledger.provider_invoice_id = sqlc.arg(provider_invoice_id)::text
      AND ledger.provider = sqlc.arg(provider)::text
    GROUP BY ledger.credit_id
    HAVING sum(-ledger.delta_cents) > 0
), inserted AS (
    INSERT INTO credit_ledger
        (account_id, credit_id, delta_cents, reason, actor, provider, provider_invoice_id, refund_reversal_id)
    SELECT sqlc.arg(account_id), credit_id, cents, 'provider refund failed',
           'apid-refund-reversal', sqlc.arg(provider), sqlc.arg(provider_invoice_id), sqlc.arg(refund_id)::uuid
    FROM consumed
    ON CONFLICT (refund_reversal_id, credit_id) WHERE refund_reversal_id IS NOT NULL
        DO NOTHING
    RETURNING credit_id, delta_cents
)
UPDATE account_credits AS credit
SET cents_remaining = credit.cents_remaining + inserted.delta_cents
FROM inserted
WHERE credit.id = inserted.credit_id AND credit.account_id = sqlc.arg(account_id);

-- name: ListAppSecretRuntimeReloadTargets :many
-- Build the complete active roster for each secret from the deployment's
-- persisted scope/allowlist and reload opt-in. A missing observation remains
-- a target with nullable outcome fields rather than disappearing from the
-- denominator.
SELECT s.scope,
       s.key,
       i.id::text AS instance_id,
       ''::text AS workload_name,
       i.state AS runtime_state,
       CASE
         WHEN d.secret_reload_signal IS NULL THEN 'unknown'
         WHEN d.secret_reload_signal = '' THEN 'disabled'
         ELSE 'enabled'
       END AS reload_support,
       o.secret_version,
       o.projection,
       o.signal,
       o.observed_at,
       o.error_code,
       o.application_ack_version,
       o.application_ack_status,
       o.application_ack_at,
       o.application_ack_error_code
  FROM instances i
  JOIN deployments d ON d.id = i.deployment_id AND d.app_id = i.app_id
  JOIN app_secrets s ON s.app_id = i.app_id AND s.scope = d.scope
  LEFT JOIN app_secret_runtime_reload_observations o
    ON o.app_id = s.app_id AND o.scope = s.scope AND o.key = s.key AND o.instance_id = i.id
   AND o.workload_name = ''
 WHERE s.account_id = sqlc.arg(account_id)::uuid
   AND i.app_id = sqlc.arg(app_id)::uuid
   AND (sqlc.arg(scope)::text = '' OR s.scope = sqlc.arg(scope)::text)
   AND (sqlc.arg(key)::text = '' OR s.key = sqlc.arg(key)::text)
   AND NOT EXISTS (SELECT 1 FROM managed_postgres_bindings b WHERE b.id = s.managed_postgres_binding_id AND b.access = 'migration')
   AND i.state IN ('waking','cold_booting','running','draining','snapshotting','migrating','warm')
   AND ((coalesce(d.override_env_secrets, '{}'::jsonb) = '{}'::jsonb
         AND jsonb_array_length(coalesce(d.sidecars, '[]'::jsonb)) = 0)
        OR d.override_env_secrets ? s.key)
UNION ALL
SELECT s.scope,
       s.key,
       i.id::text AS instance_id,
       sidecar.value->>'name' AS workload_name,
       i.state AS runtime_state,
       CASE
         WHEN reload.signal IS NULL THEN 'unknown'
         WHEN reload.signal = '' THEN 'disabled'
         ELSE 'enabled'
       END AS reload_support,
       o.secret_version,
       o.projection,
       o.signal,
       o.observed_at,
       o.error_code,
       o.application_ack_version,
       o.application_ack_status,
       o.application_ack_at,
       o.application_ack_error_code
  FROM instances i
  JOIN deployments d ON d.id = i.deployment_id AND d.app_id = i.app_id
  JOIN app_secrets s ON s.app_id = i.app_id AND s.scope = d.scope
 CROSS JOIN LATERAL jsonb_array_elements(coalesce(d.sidecars, '[]'::jsonb)) AS sidecar(value)
  LEFT JOIN deployment_sidecar_secret_reload_signals reload
    ON reload.deployment_id = d.id AND reload.sidecar_name = sidecar.value->>'name'
  LEFT JOIN app_secret_runtime_reload_observations o
    ON o.app_id = s.app_id AND o.scope = s.scope AND o.key = s.key AND o.instance_id = i.id
   AND o.workload_name = sidecar.value->>'name'
 WHERE s.account_id = sqlc.arg(account_id)::uuid
   AND i.app_id = sqlc.arg(app_id)::uuid
   AND (sqlc.arg(scope)::text = '' OR s.scope = sqlc.arg(scope)::text)
   AND (sqlc.arg(key)::text = '' OR s.key = sqlc.arg(key)::text)
   AND NOT EXISTS (SELECT 1 FROM managed_postgres_bindings b WHERE b.id = s.managed_postgres_binding_id AND b.access = 'migration')
   AND i.state IN ('waking','cold_booting','running','draining','snapshotting','migrating','warm')
   AND sidecar.value->>'type' = 'sidecar'
   AND coalesce(sidecar.value->'env_secrets', '{}'::jsonb) ? s.key
ORDER BY scope ASC, key ASC, instance_id ASC, workload_name ASC;
-- name: CreateAppSecretRevocation :one
INSERT INTO app_secret_revocations (id, account_id, app_id, scope, key, created_at)
VALUES (sqlc.arg(id)::uuid, sqlc.arg(account_id)::uuid, sqlc.arg(app_id)::uuid,
        sqlc.arg(scope)::text, sqlc.arg(key)::text, sqlc.arg(created_at)::timestamptz)
RETURNING id::text, account_id::text, app_id::text, scope, key, created_at;

-- name: GetCustomerAppSecretForDeletion :one
SELECT EXISTS (
           SELECT 1 FROM app_secrets
            WHERE account_id = sqlc.arg(account_id)::uuid
              AND app_id = sqlc.arg(app_id)::uuid
              AND scope = sqlc.arg(scope)::text
              AND key = sqlc.arg(key)::text
       ) AS present,
       EXISTS (
           SELECT 1 FROM app_secrets
            WHERE account_id = sqlc.arg(account_id)::uuid
              AND app_id = sqlc.arg(app_id)::uuid
              AND scope = sqlc.arg(scope)::text
              AND key = sqlc.arg(key)::text
              AND (managed_postgres_binding_id IS NOT NULL OR managed_object_storage_credential_id IS NOT NULL)
       ) AS managed;

-- name: CreateAppSecretRevocationTarget :exec
INSERT INTO app_secret_revocation_targets
    (revocation_id, instance_id, workload_name, runtime_state, reload_support)
VALUES (sqlc.arg(revocation_id)::uuid, sqlc.arg(instance_id)::uuid,
        sqlc.arg(workload_name)::text, sqlc.arg(runtime_state)::text,
        sqlc.arg(reload_support)::text);

-- name: GetAppSecretRevocation :one
SELECT id::text, account_id::text, app_id::text, scope, key, created_at
  FROM app_secret_revocations
 WHERE account_id = sqlc.arg(account_id)::uuid
   AND app_id = sqlc.arg(app_id)::uuid
   AND id = sqlc.arg(id)::uuid;

-- name: ListAppSecretRevocationTargets :many
SELECT instance_id::text, workload_name, runtime_state, reload_support,
       status, coalesce(ack_revision, ''), ack_at, coalesce(error_code, '')
  FROM app_secret_revocation_targets
 WHERE revocation_id = sqlc.arg(revocation_id)::uuid
 ORDER BY instance_id, workload_name;

-- name: RecordAppSecretRevocationAck :execrows
UPDATE app_secret_revocation_targets t
   SET status = sqlc.arg(status)::text,
       ack_revision = sqlc.arg(ack_revision)::text,
       ack_at = sqlc.arg(ack_at)::timestamptz,
       error_code = nullif(sqlc.arg(error_code)::text, '')
  FROM app_secret_revocations r
 WHERE t.revocation_id = r.id
   AND r.account_id = sqlc.arg(account_id)::uuid
   AND r.app_id = sqlc.arg(app_id)::uuid
   AND r.scope = sqlc.arg(scope)::text
   AND t.instance_id = sqlc.arg(instance_id)::uuid
   AND t.workload_name = sqlc.arg(workload_name)::text
   AND r.created_at <= sqlc.arg(ack_at)::timestamptz
   AND EXISTS (SELECT 1 FROM instances i WHERE i.id = t.instance_id AND i.app_id = r.app_id)
   AND NOT EXISTS (
       SELECT 1 FROM app_secrets s
        WHERE s.account_id = r.account_id AND s.app_id = r.app_id
          AND s.scope = r.scope AND s.key = r.key
   )
   AND t.status <> 'applied';

-- name: DeleteCustomerAppSecret :execrows
DELETE FROM app_secrets
 WHERE account_id = sqlc.arg(account_id)::uuid
   AND app_id = sqlc.arg(app_id)::uuid
   AND scope = sqlc.arg(scope)::text
   AND key = sqlc.arg(key)::text
   AND managed_postgres_binding_id IS NULL
   AND managed_object_storage_credential_id IS NULL;

-- name: SetDeploymentSecretReloadSignal :execrows
-- imaged persists the validated image opt-in on each newly built deployment;
-- the state query keeps legacy NULL rows distinct from explicit opt-outs.
UPDATE deployments
   SET secret_reload_signal = sqlc.arg(signal)::text
 WHERE id = sqlc.arg(id)::uuid;

-- name: SumAccountCreditRefundReversal :one
SELECT coalesce(sum(delta_cents), 0)::bigint AS reversed_cents
FROM credit_ledger
WHERE account_id = sqlc.arg(account_id)::uuid
  AND refund_reversal_id = sqlc.arg(refund_id)::uuid;

-- name: AppendAccountCreditLedgerEntry :exec
INSERT INTO credit_ledger (account_id, credit_id, delta_cents, reason, actor, provider, provider_invoice_id)
VALUES ($1, $2, $3, $4, $5, $6, $7);

-- name: ReserveAccountCreditConsumption :one
INSERT INTO credit_ledger (account_id, credit_id, delta_cents, reason, actor, provider, provider_invoice_id)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (provider, provider_invoice_id, credit_id)
    WHERE provider_invoice_id IS NOT NULL AND delta_cents < 0 DO NOTHING
RETURNING id;

-- name: LockInvoiceForRefund :one
SELECT account_id, provider, provider_invoice_id, amount_paid_cents,
       total_cents, amount_refunded_cents, amount_refund_pending_cents, credits_applied_cents
FROM invoices WHERE id = $1 FOR UPDATE;

-- name: LockCreditConsumption :exec
-- Keep the historical broad lock key, also shared with refund compensation.
SELECT pg_advisory_xact_lock(hashtextextended('consume-account-credit:' || sqlc.arg(provider_invoice_id)::text, 0));

-- name: FindInvoiceIDsByProviderKey :many
-- Two matches mean an invoice ID collides with another invoice's charge ID.
SELECT id FROM invoices
WHERE account_id = sqlc.arg(account_id)::uuid
  AND provider = sqlc.arg(provider)::text
  AND (provider_invoice_id = sqlc.arg(provider_key)::text
       OR provider_charge_id = sqlc.arg(provider_key))
LIMIT 2;

-- name: ListInvoiceSnapshots :many
SELECT id, account_id, provider, provider_invoice_id, provider_charge_id, number, status,
       period_start, period_end, subtotal_cents, tax_cents, total_cents, amount_paid_cents,
       plan, amount_refunded_cents, amount_refund_pending_cents, credits_applied_cents,
       currency, pdf_available, created_at, updated_at, details, detail_lifecycle FROM invoices
WHERE account_id = sqlc.arg(account_id)::uuid
  AND (sqlc.narg(month_start)::timestamptz IS NULL OR period_end >= sqlc.narg(month_start))
  AND (sqlc.narg(month_end)::timestamptz IS NULL OR period_end < sqlc.narg(month_end))
  AND (sqlc.narg(before_time)::timestamptz IS NULL OR period_end < sqlc.narg(before_time))
ORDER BY period_end DESC, id DESC
LIMIT sqlc.arg(row_limit);

-- name: GetInvoiceSnapshot :one
SELECT id, account_id, provider, provider_invoice_id, provider_charge_id, number, status,
       period_start, period_end, subtotal_cents, tax_cents, total_cents, amount_paid_cents,
       plan, amount_refunded_cents, amount_refund_pending_cents, credits_applied_cents,
       currency, pdf_available, created_at, updated_at, details, detail_lifecycle FROM invoices WHERE id = $1;

-- name: LockOwnedInvoiceSnapshot :one
SELECT id, account_id, provider, provider_invoice_id, provider_charge_id, number, status,
       period_start, period_end, subtotal_cents, tax_cents, total_cents, amount_paid_cents,
       plan, amount_refunded_cents, amount_refund_pending_cents, credits_applied_cents,
       currency, pdf_available, created_at, updated_at, details, detail_lifecycle FROM invoices WHERE id = $1 AND account_id = $2 FOR UPDATE;

-- name: InvoiceRefreshTime :one
SELECT clock_timestamp()::timestamptz;

-- name: SetInvoiceEnrichment :exec
UPDATE invoices SET details = $2, detail_lifecycle = $3, updated_at = $4 WHERE id = $1;

-- name: UpsertInvoiceSnapshot :one
INSERT INTO invoices (
  account_id, provider, provider_invoice_id, provider_charge_id, number, status,
  period_start, period_end, subtotal_cents, tax_cents, total_cents,
  amount_paid_cents, plan, currency, pdf_available, details, detail_lifecycle, updated_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, clock_timestamp())
ON CONFLICT (account_id, provider, provider_invoice_id) DO UPDATE SET
  provider_charge_id = coalesce(nullif(excluded.provider_charge_id, ''), invoices.provider_charge_id),
  number = excluded.number, status = excluded.status,
  period_start = excluded.period_start, period_end = excluded.period_end,
  subtotal_cents = excluded.subtotal_cents, tax_cents = excluded.tax_cents,
  total_cents = excluded.total_cents, amount_paid_cents = excluded.amount_paid_cents,
  currency = excluded.currency, pdf_available = excluded.pdf_available,
  details = invoices.details || CASE WHEN excluded.details ? 'lines' THEN
    jsonb_set(excluded.details, '{lines,items}', coalesce((
      SELECT jsonb_agg(incoming.item || jsonb_build_object(
        'created_at', coalesce(invoices.details->'line_first_seen'->(incoming.item->>'id'), previous.item->'created_at', incoming.item->'created_at'),
        'updated_at', CASE WHEN incoming.item - 'created_at' - 'updated_at' = previous.item - 'created_at' - 'updated_at'
          THEN previous.item->'updated_at' ELSE incoming.item->'updated_at' END) ORDER BY incoming.ordinality)
      FROM jsonb_array_elements(excluded.details->'lines'->'items') WITH ORDINALITY AS incoming(item, ordinality)
      LEFT JOIN LATERAL (
        SELECT stored.item FROM jsonb_array_elements(coalesce(invoices.details->'lines'->'items', '[]'::jsonb)) AS stored(item)
        WHERE stored.item->>'id' = incoming.item->>'id' LIMIT 1
      ) AS previous ON true
    ), '[]'::jsonb))
  ELSE excluded.details END || CASE WHEN excluded.details ? 'lines' THEN
    jsonb_build_object('line_first_seen', coalesce(excluded.details->'line_first_seen', '{}'::jsonb) || coalesce(invoices.details->'line_first_seen', '{}'::jsonb))
  ELSE '{}'::jsonb END, updated_at = clock_timestamp()
RETURNING id, account_id, provider, provider_invoice_id, provider_charge_id, number, status,
          period_start, period_end, subtotal_cents, tax_cents, total_cents, amount_paid_cents,
          plan, amount_refunded_cents, amount_refund_pending_cents, credits_applied_cents,
          currency, pdf_available, created_at, updated_at, details, detail_lifecycle;

-- name: InsertInvoiceHistorySnapshot :one
INSERT INTO invoices (
  account_id, provider, provider_invoice_id, provider_charge_id, number, status,
  period_start, period_end, subtotal_cents, tax_cents, total_cents,
  amount_paid_cents, plan, currency, pdf_available, details, detail_lifecycle
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)
ON CONFLICT (account_id, provider, provider_invoice_id) DO NOTHING
RETURNING id, updated_at;

-- name: SetInvoiceDetailLifecycle :exec
-- The caller retains the natural-key upsert's row lock in the same transaction.
UPDATE invoices SET detail_lifecycle = $2 WHERE id = $1;

-- name: RollupMirrorResults :execrows
-- ADR-221: claiming and counting share one statement/transaction. SKIP LOCKED
-- permits concurrent workers without counting the same result twice.
WITH pending AS MATERIALIZED (
    SELECT id FROM mirror_invocation_results
    WHERE NOT rollup_counted
      AND completed_at >= sqlc.arg(window_start)::timestamptz
      AND completed_at < sqlc.arg(window_end)::timestamptz
    ORDER BY id
    FOR UPDATE SKIP LOCKED
), counted AS (
    UPDATE mirror_invocation_results AS result
    SET rollup_counted = true
    FROM pending
    WHERE result.id = pending.id
    RETURNING
        result.mirror_rule_id, result.app_id, result.completed_at,
        result.status_diff, result.schema_diff, result.body_diff,
        result.crashed, result.latency_ms
)
INSERT INTO mirror_invocation_summary (
    rule_id, app_id, hour_bucket, total_invocations,
    status_diff_count, schema_diff_count, body_diff_count, crash_count,
    cap_at_max_count, sum_latency_ms, rolled_up_at
)
SELECT mirror_rule_id, app_id,
    date_trunc('hour', completed_at AT TIME ZONE 'UTC') AT TIME ZONE 'UTC',
    count(*), count(*) FILTER (WHERE status_diff),
    count(*) FILTER (WHERE schema_diff), count(*) FILTER (WHERE body_diff),
    count(*) FILTER (WHERE crashed), 0, coalesce(sum(latency_ms), 0), now()
FROM counted
GROUP BY 1, 2, 3
ORDER BY 1, 3
ON CONFLICT (rule_id, hour_bucket) DO UPDATE SET
    total_invocations = mirror_invocation_summary.total_invocations + EXCLUDED.total_invocations,
    status_diff_count = mirror_invocation_summary.status_diff_count + EXCLUDED.status_diff_count,
    schema_diff_count = mirror_invocation_summary.schema_diff_count + EXCLUDED.schema_diff_count,
    body_diff_count = mirror_invocation_summary.body_diff_count + EXCLUDED.body_diff_count,
    crash_count = mirror_invocation_summary.crash_count + EXCLUDED.crash_count,
    cap_at_max_count = mirror_invocation_summary.cap_at_max_count + EXCLUDED.cap_at_max_count,
    sum_latency_ms = mirror_invocation_summary.sum_latency_ms + EXCLUDED.sum_latency_ms,
    rolled_up_at = now();

-- name: SweepCountedMirrorResults :execrows
DELETE FROM mirror_invocation_results
WHERE completed_at < sqlc.arg(cutoff)::timestamptz AND rollup_counted;

-- name: LockMirrorRuleForSlotLease :one
-- Serializes reservation attempts for one rule across every gateway replica.
SELECT id::text FROM mirror_rules
WHERE id = sqlc.arg(rule_id)::uuid
FOR UPDATE;

-- name: DeleteExpiredMirrorSlotLeases :execrows
DELETE FROM mirror_slot_leases
WHERE mirror_rule_id = sqlc.arg(rule_id)::uuid
  AND expires_at <= clock_timestamp();

-- name: CountActiveMirrorSlotLeases :one
SELECT count(*)::bigint FROM mirror_slot_leases
WHERE mirror_rule_id = sqlc.arg(rule_id)::uuid
  AND expires_at > clock_timestamp();

-- name: CreateMirrorSlotLease :one
INSERT INTO mirror_slot_leases (lease_id, mirror_rule_id, expires_at)
VALUES (
    sqlc.arg(lease_id)::uuid,
    sqlc.arg(rule_id)::uuid,
    clock_timestamp() + sqlc.arg(ttl_millis)::bigint * interval '1 millisecond'
)
RETURNING lease_id::text;

-- name: ReleaseMirrorSlotLease :exec
DELETE FROM mirror_slot_leases
WHERE mirror_rule_id = sqlc.arg(rule_id)::uuid
  AND lease_id = sqlc.arg(lease_id)::uuid;

-- name: CreateAccount :one
insert into accounts (id, email, plan, status, provider_customer_id)
values (gen_random_uuid(), $1, $2, $3, null)
returning id, email, plan, status, coalesce(provider_customer_id, ''), created_at;

-- name: AccountByID :one
select id, email, plan, status, coalesce(provider_customer_id, ''), created_at
from accounts where id = $1;

-- name: AccountsByIDs :many
select id, email, plan, status, coalesce(provider_customer_id, ''), coalesce(stripe_subscription_item, ''), created_at, deletion_requested_at, last_quota_warning_at, past_due_at, mfa_enrolled_at, mfa_secret_encrypted, mfa_recovery_codes_hash, mfa_required
from accounts where id = any($1::uuid[]);

-- name: AccountByEmail :one
select id, email, plan, status, coalesce(provider_customer_id, ''), created_at
from accounts where email = $1;

-- name: AccountByKeyHash :one
select a.id, a.email, a.plan, a.status, coalesce(a.provider_customer_id, ''), a.created_at
from accounts a
join api_keys k on k.account_id = a.id
where k.key_sha256 = $1;

-- name: UpdateAccountPlan :exec
update accounts set plan = $2 where id = $1;

-- name: UpdateAccountStatus :exec
update accounts set status = $2 where id = $1;

-- name: CreateAPIKey :one
-- scopes is $4 (text[]). The handler is responsible for validating the
-- scope vocabulary; the store does not. See ADR-034 rev2.
insert into api_keys (account_id, key_sha256, label, scopes)
values ($1, $2, $3, $4)
returning id, account_id, key_sha256, coalesce(label, ''), scopes, created_at, coalesce(last_used_at, 'epoch'::timestamptz);

-- name: DeleteAPIKey :exec
delete from api_keys where id = $1 and account_id = $2;

-- name: DeleteAPIKeyReturning :one
-- IAM-1 (ADR-034 rev2): delete a key and return the row in one
-- statement so the handler can emit `key.deleted` audit with the
-- dismissed scopes. list_secrets-shaped variant of DeleteAPIKey.
delete from api_keys where id = $1 and account_id = $2
returning id, account_id, key_sha256, coalesce(label, ''), scopes, created_at, coalesce(last_used_at, 'epoch'::timestamptz);

-- name: ListAPIKeys :many
-- scopes is the auth permission set surfaced to the dashboard and the
-- /v1/keys listing. See ADR-034 rev2.
select id, account_id, key_sha256, coalesce(label, ''), scopes, created_at, coalesce(last_used_at, 'epoch'::timestamptz)
from api_keys where account_id = $1 order by created_at desc;

-- name: APIKeyByHash :one
-- Used by handlers_auth.go so an operator investigating "who signed in
-- as alice?" can identify the key that authenticated. See ADR-034 rev2.
select id, account_id, key_sha256, coalesce(label, ''), scopes, created_at, coalesce(last_used_at, 'epoch'::timestamptz)
from api_keys where key_sha256 = $1;

-- name: TouchKeyLastUsed :exec
update api_keys set last_used_at = now() where id = $1;

-- name: CreateApp :one
insert into apps (id, account_id, slug, type, runtime, ram_mb, idle_timeout_s, max_concurrency, status, manifest)
values (gen_random_uuid(), $1, $2, $3, $4, $5, $6, $7, 'active', coalesce($8, '{}'::jsonb))
returning id, account_id, slug, type, coalesce(runtime, ''), ram_mb, coalesce(idle_timeout_s, 0),
          max_concurrency, status, manifest, created_at;

-- name: AppByID :one
select id, account_id, slug, type, coalesce(runtime, ''), ram_mb, coalesce(idle_timeout_s, 0),
       max_concurrency, status, manifest, created_at
from apps where id = $1;

-- name: AppBySlug :one
select id, account_id, slug, type, coalesce(runtime, ''), ram_mb, coalesce(idle_timeout_s, 0),
       max_concurrency, status, manifest, created_at
from apps where slug = $1;

-- name: ListApps :many
select id, account_id, slug, type, coalesce(runtime, ''), ram_mb, coalesce(idle_timeout_s, 0),
       max_concurrency, status, manifest, created_at
from apps where account_id = $1 order by created_at desc;

-- name: CountDeployedApps :one
select count(*) from apps where account_id = $1 and status in ('active', 'evicted_cold')
  and not (preview_of_slug is not null and coalesce(preview_pr_number, 0) = 0);

-- name: UpdateApp :one
update apps set
  ram_mb = coalesce($2, ram_mb),
  idle_timeout_s = case when $3::boolean then $4 else idle_timeout_s end,
  max_concurrency = coalesce($5, max_concurrency),
  status = coalesce($6, status)
where id = $1
returning id, account_id, slug, type, coalesce(runtime, ''), ram_mb, coalesce(idle_timeout_s, 0),
          max_concurrency, status, manifest, created_at;

-- name: SetAppManifest :exec
update apps set manifest = $2 where id = $1;

-- name: DeleteApp :exec
update apps
set status = 'deleted',
    deleted_at = coalesce(deleted_at, now()),
    delete_grace_until = coalesce(delete_grace_until, now() + interval '7 days')
where id = $1;

-- name: CreateDeployment :one
insert into deployments (id, app_id, build_id, image_digest, kind, source_path, source_root, source_bytes, handler, log_path, status)
values (gen_random_uuid(), $1, null, $2, $3, $4, $5, $6, $7, $8, 'pending')
returning id, app_id, coalesce(build_id::text, ''), image_digest, kind,
          coalesce(source_path, ''), coalesce(source_root, ''), coalesce(source_bytes, 0),
          coalesce(handler, ''), coalesce(log_path, ''),
          status, coalesce(error, ''), created_at;

-- name: DeploymentByID :one
select id, app_id, coalesce(build_id::text, ''), image_digest, kind,
       coalesce(source_path, ''), coalesce(source_root, ''), coalesce(source_bytes, 0),
       coalesce(handler, ''), coalesce(log_path, ''),
       status, coalesce(error, ''), created_at
from deployments where id = $1;

-- name: LatestDeployment :one
select id, app_id, coalesce(build_id::text, ''), image_digest, kind,
       coalesce(source_path, ''), coalesce(source_root, ''), coalesce(source_bytes, 0),
       coalesce(handler, ''), coalesce(log_path, ''),
       status, coalesce(error, ''), created_at
from deployments where app_id = $1 order by created_at desc limit 1;

-- name: ListDeploymentsForApp :many
select id, app_id, coalesce(build_id::text, ''), image_digest, kind,
       coalesce(source_path, ''), coalesce(source_root, ''), coalesce(source_bytes, 0),
       coalesce(handler, ''), coalesce(log_path, ''),
       status, coalesce(error, ''), created_at
from deployments where app_id = $1 order by created_at desc limit $2 offset $3;

-- name: ListLatestDeploymentPerApp :many
select distinct on (d.app_id) d.*
from deployments d
join apps a on a.id = d.app_id
where a.account_id = $1 and a.status <> 'deleted' and d.deleted_at IS NULL
order by d.app_id, d.created_at desc, d.id desc;

-- name: LatestSupersededDeployment :one
select id, app_id, coalesce(build_id::text, ''), image_digest, kind,
       coalesce(source_path, ''), coalesce(source_root, ''), coalesce(source_bytes, 0),
       coalesce(handler, ''), coalesce(log_path, ''),
       status, coalesce(error, ''), created_at
from deployments
where app_id = $1 and status = 'superseded'
order by created_at desc limit 1;

-- name: UpdateDeploymentStatus :exec
update deployments
set status = $2,
    error = $3,
    traffic_percent = case when $2 = 'failed' then 0 else traffic_percent end,
    rollout_state = case when $2 = 'failed' then 'aborted' else rollout_state end,
    rollout_completed_at = case when $2 = 'failed' then null else rollout_completed_at end,
    rollout_aborted_at = case when $2 = 'failed' then coalesce(rollout_aborted_at, now()) else rollout_aborted_at end,
    rollout_aborted_reason = case when $2 = 'failed' then coalesce(nullif($3, ''), 'deployment failed') else rollout_aborted_reason end
where id = $1;

-- name: SetDeploymentFailed :one
-- ADR-021 (G1, image digest enforcement hardening): durable
-- carrier for the RFC 7807 failure code that imaged writes when a
-- deployment transitions to `failed`. pkg/api.SentinelToCode maps
-- the three puller-side sentinels to the codes pkg/api.CodeImage*
-- (image_not_found / image_egress_denied / image_manifest_invalid)
-- and imaged passes the resulting code as $3 here. The free-text
-- error column ($2) is preserved for debugging. Status is pinned
-- to 'failed' (caller's status argument is ignored — this is a
-- failure-specific helper, not a generic update).
--
-- errcode is omitted in the scan (empty string on success means
-- "no code mapped"; null in the column means "not yet stamped" —
-- both render as "" on the Go side via the coalesce in the SELECT).
update deployments
   set status = 'failed', error = $2, error_code = $3,
       traffic_percent = 0, rollout_state = 'aborted',
       rollout_completed_at = null,
       rollout_aborted_at = coalesce(rollout_aborted_at, now()),
       rollout_aborted_reason = coalesce(nullif($2, ''), 'deployment failed')
 where id = $1
returning id, app_id, coalesce(build_id::text, ''), image_digest, kind,
          coalesce(source_path, ''), coalesce(source_root, ''), coalesce(source_bytes, 0),
          coalesce(handler, ''), coalesce(log_path, ''),
          coalesce(rootfs_path, ''), coalesce(rootfs_key, ''), coalesce(rootfs_bytes, 0),
          status, coalesce(error, ''), coalesce(error_code, ''), created_at;

-- name: MarkDeploymentSuperseded :exec
update deployments set status = 'superseded' where id = $1;

-- name: MarkDeploymentLive :exec
update deployments set status = 'live' where id = $1;

-- name: CreateCustomDomain :one
insert into custom_domains (domain, app_id, challenge_token)
values ($1, $2, $3)
returning domain, app_id, challenge_token, verified_at, environment_id;

-- name: DomainByName :one
select domain, app_id, challenge_token, verified_at, environment_id
from custom_domains where domain = $1;

-- name: ListDomainsForApp :many
select domain, app_id, challenge_token, verified_at, environment_id
from custom_domains where app_id = $1 order by domain;

-- name: ListDomainsForAccount :many
select d.domain, d.app_id, d.challenge_token, d.verified_at, d.environment_id
from custom_domains d join apps a on a.id = d.app_id
where a.account_id = $1 order by d.domain;

-- name: MarkDomainVerified :exec
update custom_domains set verified_at = now() where domain = $1;

-- name: DeleteCustomDomain :exec
delete from custom_domains where domain = $1;

-- name: CreateCron :one
insert into crons (id, app_id, schedule, path, enabled, timezone, skip_if_running)
values (gen_random_uuid(), $1, $2, $3, $4, $5, $6)
returning id, app_id, schedule, path, enabled, suspended_reason, timezone, skip_if_running, last_fired_at, created_at;

-- name: UpdateCron :one
update crons set
  schedule = coalesce($2, schedule),
  path = coalesce($3, path),
  enabled = coalesce($4, enabled)
where id = $1
returning id, app_id, schedule, path, enabled, suspended_reason, timezone, skip_if_running, last_fired_at, created_at;

-- name: DeleteCron :exec
delete from crons where id = $1 and app_id = $2;

-- name: ListCronsForApp :many
select id, app_id, schedule, path, enabled, suspended_reason, timezone, skip_if_running, last_fired_at, created_at
from crons where app_id = $1 order by created_at desc;

-- name: ListEnabledCrons :many
select id, app_id, schedule, path, enabled, suspended_reason, timezone, skip_if_running, last_fired_at, created_at
from crons where enabled = true and suspended_reason = '';

-- name: CronByID :one
select id, app_id, schedule, path, enabled, suspended_reason, timezone, skip_if_running, last_fired_at, created_at
from crons where id = $1;

-- name: AppendEvent :exec
insert into events (actor, kind, subject, data)
values ($1, $2, $3, $4);

-- name: LockWebhookAutomationEndpoint :one
SELECT * FROM inbound_webhook_endpoints
WHERE id=$1 AND app_id=$2 AND account_id=$3 FOR UPDATE;

-- name: GetWebhookAutomationBinding :one
SELECT * FROM workflow_webhook_bindings WHERE endpoint_id=$1;

-- name: SaveWebhookAutomationBinding :one
INSERT INTO workflow_webhook_bindings(endpoint_id,workflow_name,event_type,filter,version)
VALUES($1,$2,$3,$4,nextval('workflow_webhook_binding_revision_seq'))
ON CONFLICT(endpoint_id) DO UPDATE SET workflow_name=EXCLUDED.workflow_name,
event_type=EXCLUDED.event_type,filter=EXCLUDED.filter,version=EXCLUDED.version,updated_at=clock_timestamp()
RETURNING *;

-- name: DeleteWebhookAutomationBinding :execrows
DELETE FROM workflow_webhook_bindings WHERE endpoint_id=$1 AND version=$2;

-- name: WebhookAutomationLegacyReceiptExists :one
SELECT EXISTS(SELECT 1 FROM invocations WHERE id=$1);

-- name: InsertWebhookAutomationOutbox :one
INSERT INTO event_fanout_outbox(account_id,source,event_id,event_type,event_data,payload,recipient_snapshot)
VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING id;

-- name: InsertWebhookAutomationReceipt :exec
INSERT INTO workflow_webhook_receipts(endpoint_id,provider_event_id,receipt_id,body_hash,workflow_name,recipient_id,outbox_id,status,ignored_reason)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9);

-- name: GetWebhookAutomationReceipt :one
SELECT r.*, er.run_id, o.recipient_progress
FROM workflow_webhook_receipts r JOIN event_fanout_outbox o ON o.id=r.outbox_id
LEFT JOIN workflow_event_receipts er ON er.outbox_id=r.outbox_id AND er.recipient_id=r.recipient_id
WHERE r.endpoint_id=$1 AND r.provider_event_id=$2;

-- name: ListEvents :many
select id, at, actor, kind, subject, data
from events where subject = $1 order by at desc limit $2;

-- EPIC #1278 / Workstream B — durable internal event subscriptions.
-- A subscription is app-owned but keeps account_id denormalized so scheduler
-- fan-out can enforce tenant isolation without joining apps.

-- name: ListEventSubscriptionsForApp :many
select id, account_id, app_id, source, type, filter, enabled,
       created_at, updated_at
from event_subscriptions
where app_id = $1
order by created_at asc, id asc;

-- name: ListEnabledEventSubscriptionsForAccount :many
select s.id, s.account_id, s.app_id, s.source, s.type, s.filter, s.enabled,
       s.created_at, s.updated_at
from event_subscriptions s
join apps a on a.id = s.app_id
where s.account_id = $1 and s.enabled and a.status <> 'deleted'
order by s.created_at asc, s.id asc;

-- name: ListMatchingEventSubscriptionsForAccount :many
-- Candidate lookup for schedd fan-out. The final JSON filter matcher remains
-- in pkg/events; these predicates only prune source/type patterns and page
-- through the tenant's enabled subscriptions without an unbounded scan.
select s.id, s.account_id, s.app_id, s.source, s.type, s.filter, s.enabled,
       s.created_at, s.updated_at
from event_subscriptions s
join apps a on a.id = s.app_id
where s.account_id = sqlc.arg('account_id')::uuid
  and s.enabled
  and a.status <> 'deleted'
  and case
        when s.source = '*' then true
        when left(s.source, 1) = '*' and right(s.source, 1) = '*' then
          position(substring(s.source, 2, greatest(length(s.source) - 2, 0)) in sqlc.arg('source')::text) > 0
        when left(s.source, 1) = '*' then
          right(sqlc.arg('source')::text, greatest(length(s.source) - 1, 0)) = right(s.source, greatest(length(s.source) - 1, 0))
        when right(s.source, 1) = '*' then
          left(sqlc.arg('source')::text, greatest(length(s.source) - 1, 0)) = left(s.source, greatest(length(s.source) - 1, 0))
        else s.source = sqlc.arg('source')::text
      end
  and case
        when s.type = '*' then true
        when left(s.type, 1) = '*' and right(s.type, 1) = '*' then
          position(substring(s.type, 2, greatest(length(s.type) - 2, 0)) in sqlc.arg('type')::text) > 0
        when left(s.type, 1) = '*' then
          right(sqlc.arg('type')::text, greatest(length(s.type) - 1, 0)) = right(s.type, greatest(length(s.type) - 1, 0))
        when right(s.type, 1) = '*' then
          left(sqlc.arg('type')::text, greatest(length(s.type) - 1, 0)) = left(s.type, greatest(length(s.type) - 1, 0))
        else s.type = sqlc.arg('type')::text
      end
  and (sqlc.arg('cursor_created_at')::timestamptz is null
       or (s.created_at, s.id) > (sqlc.arg('cursor_created_at')::timestamptz,
                                  sqlc.arg('cursor_id')::uuid))
order by s.created_at asc, s.id asc
limit sqlc.arg('limit')::int;

-- name: UpsertEventSubscription :one
-- (xmax = 0) distinguishes a declaration first installed by this deploy from
-- an idempotent replay of the same manifest row.
insert into event_subscriptions (account_id, app_id, source, type, filter)
values ($1, $2, $3, $4, $5::jsonb)
on conflict (app_id, source, type, filter) do update
set enabled = true,
    updated_at = now()
returning id, account_id, app_id, source, type, filter, enabled,
          created_at, updated_at, (xmax = 0) as inserted;

-- name: DeleteEventSubscription :exec
delete from event_subscriptions
where id = $1 and account_id = $2 and app_id = $3;

-- name: ListEventsByWakeID :many
-- issue #517 / PR-C / ADR-064 — wake-timeline read-side query.
-- Filters on the jsonb expression index events_wake_id_idx
-- (migrations/00114_events_wake_id_idx.sql) and orders by at ASC
-- so the customer-facing timeline endpoint surfaces a forward
-- narrative. schedd is canonical for wake.boot_started; vmmd's
-- same-kind row is a corroborating observation. Rank that pair
-- before applying the $2 `since` lower bound and $3 limit so a
-- later mirror cannot appear on a subsequent page. Index path:
-- partial index on
-- (data->>'wake_id') WHERE data->>'wake_id' IS NOT NULL means
-- only rows with a wake_id tag (i.e. the 13 wake.* kinds) are
-- indexed — legacy audit rows are not in scope of PR-C, see
-- ADR-064 §"Compatibility".
with wake_events as (
  select id, at, actor, kind, subject, data,
         row_number() over (
           partition by kind
           order by (actor = 'schedd') desc, at asc, id asc
         ) as boot_rank
  from events
  where data->>'wake_id' = $1
)
select id, at, actor, kind, subject, data
from wake_events
where (kind <> 'wake.boot_started' or boot_rank = 1)
  and at > $2
order by at asc, id asc
limit $3;

-- name: ListAllEventsPaged :many
-- ADR-091 §3.7 / PR #3 — operator-obs backend audit-reading surface.
-- Reads the live events table (NOT audit_log — distinct source of
-- truth per ADR-091 §3.7.4). Optional filters:
--   * $1 actor    — exact match (handler passes "" to skip)
--   * $2 kind_prefix — LIKE 'prefix%' (handler passes "" to skip)
--   * $3 subject  — exact match (handler passes "" to skip)
--   * $4 since    — RFC 3339 timestamptz (handler passes zero time to skip)
--   * $5 limit    — top-N rows (handler default 200, cap 500;
--                   cast to int8 so sqlc emits int64 Params and the
--                   handler's int→int64 widening is safe)
-- Order: at DESC, id DESC — the id tiebreaker keeps the planner on
-- the (kind, at DESC) index added by 00190_admin_obs_index.sql for
-- kind-prefix queries and avoids an unstable sort on the
-- over-read window.
-- Subject is uuid (nullable in the schema); the cast is left to
-- the handler so the handler can pass an empty string for "no
-- subject filter" without a NULL literal.
select id, at, actor, kind, subject, data
from events
where ($1 = '' or actor = $1)
  and ($2 = '' or kind like $2 || '%')
  and ($3 = '' or subject = $3::uuid)
  and ($4 = '0001-01-01 00:00:00+00:00'::timestamptz or at >= $4)
order by at desc, id desc
limit $5::int8;

-- name: ListRecentEventsForAccount :many
-- ADR-091 §3.7 / PR #3 — per-account events drill-down. Backed by
-- the partial index events_actor_account_idx on
-- (actor_account_id) WHERE actor_account_id IS NOT NULL
-- (migrations/00099_orgs_memberships_invitations.sql). Filters:
--   * $1 actor_account_id — uuid (the account the actor belonged to)
--   * $2 since             — RFC 3339 timestamptz (handler passes
--                            zero time to skip; the predicate is
--                            uniform with ListAllEventsPaged)
--   * $3 limit             — top-N rows (handler default 200, cap 500;
--                            cast to int8 so sqlc emits int64 Params
--                            and the handler's int→int64 widening is
--                            safe)
-- Order: at DESC, id DESC — same rationale as ListAllEventsPaged.
-- PR #3 wires the per-account filter on the SSE mirror's
-- per-account projections; the broader ?actor + ?subject filter
-- shape lives on ListAllEventsPaged.
select id, at, actor, kind, subject, data
from events
where actor_account_id = $1
  and ($2 = '0001-01-01 00:00:00+00:00'::timestamptz or at >= $2)
order by at desc, id desc
limit $3::int8;

-- name: AppendUsage :exec
-- Idempotent on (instance_id, minute) for mb_seconds / requests
-- (M7 hardening, PR feat/m7-beta-hardening): a redelivered
-- minute is a no-op for the billing-floor columns so a meterd
-- restart / network blip / two meterd instances cannot inflate
-- billing. cpu_usec, tx_bytes, net_tx_bytes, net_rx_bytes,
-- cold_boot_count, and tail_seconds are ADDITIVE on the same
-- conflict key — the schedd / meterd accumulators can each call
-- AppendUsage many times within the same minute; the columns are
-- the sum of all per-tick deltas.
--   cpu_usec         — issue #279 / PR-B / ADR-039
--   tx_bytes         — ADR-046 (gateway HTTP response body bytes)
--   net_tx_bytes     — ADR-046 (root-side vethHost.rx_bytes delta)
--   net_rx_bytes     — ADR-048 (root-side vethHost.tx_bytes delta; ingress)
--   cold_boot_count  — ADR-048 (WAKE_RESTORE→WAKE_COLD_BOOT transitions)
--   tail_seconds     — issue #667 / ADR-078 (per-minute wall-clock seconds
--                      draining waitUntil tasks; INFORMATIONAL ONLY — pinned
--                      by pkg/meter/pusher_shadow_test.go::TestPushHour_ExcludesTailSeconds)
insert into usage_minutes (account_id, app_id, instance_id, minute, mb_seconds, requests, cpu_usec, tx_bytes, net_tx_bytes, net_rx_bytes, cold_boot_count, tail_seconds)
values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
on conflict (instance_id, minute) do update
   set mb_seconds      = case when usage_minutes.mb_seconds = 0 and EXCLUDED.mb_seconds > 0 then EXCLUDED.mb_seconds else usage_minutes.mb_seconds end,
       cpu_usec        = usage_minutes.cpu_usec        + EXCLUDED.cpu_usec,
       tx_bytes        = usage_minutes.tx_bytes        + EXCLUDED.tx_bytes,
       net_tx_bytes    = usage_minutes.net_tx_bytes    + EXCLUDED.net_tx_bytes,
       net_rx_bytes    = usage_minutes.net_rx_bytes    + EXCLUDED.net_rx_bytes,
       cold_boot_count = usage_minutes.cold_boot_count + EXCLUDED.cold_boot_count,
       tail_seconds    = usage_minutes.tail_seconds    + EXCLUDED.tail_seconds;

-- name: RegisterGatewayUsageEvent :one
with inserted as (
  insert into meter_gateway_usage_events (node_id, event_id, instance_id, minute)
  values ($1, $2, $3, $4)
  on conflict (node_id, event_id) do nothing
  returning 1
)
select exists(select 1 from inserted) as inserted;

-- name: ApplyGatewayUsageEvent :execrows
insert into usage_minutes (account_id, app_id, instance_id, minute, mb_seconds, requests, cpu_usec, tx_bytes, net_tx_bytes, net_rx_bytes, cold_boot_count, tail_seconds)
select a.account_id, i.app_id, i.id, $2::timestamptz, 0, $3::int, 0, $4::bigint, 0, 0, $5::int, 0
  from instances i
  join apps a on a.id = i.app_id
 where i.id = $1
on conflict (instance_id, minute) do update
   set requests        = usage_minutes.requests        + EXCLUDED.requests,
       tx_bytes        = usage_minutes.tx_bytes        + EXCLUDED.tx_bytes,
       cold_boot_count = usage_minutes.cold_boot_count + EXCLUDED.cold_boot_count;

-- name: UsageByMonth :many
select account_id, app_id, month, mb_seconds, cpu_usec, requests, tx_bytes, net_tx_bytes
from usage_monthly
where account_id = $1 and month = $2
order by app_id, month;

-- name: CreateInstance :one
insert into instances (id, app_id, deployment_id, state, ram_mb)
values (gen_random_uuid(), $1, $2, $3, $4)
returning id, app_id, deployment_id, state, coalesce(netns, ''), coalesce(guest_uid, 0),
          coalesce(host_ip::text, ''), ram_mb, started_at, last_request_at, parked_at;

-- name: InstanceByID :one
select id, app_id, deployment_id, state, coalesce(netns, ''), coalesce(guest_uid, 0),
       coalesce(host_ip::text, ''), ram_mb, started_at, last_request_at, parked_at
from instances where id = $1;

-- name: PublishOwnedInstanceRuntime :one
UPDATE instances SET netns=sqlc.arg(netns)::text, host_ip=sqlc.arg(host_ip)::inet,
    guest_uid=sqlc.arg(guest_uid)::integer, started_at=clock_timestamp(), state=sqlc.arg(target_state)::text
WHERE id=sqlc.arg(instance_id)::uuid AND app_id=sqlc.arg(app_id)::uuid
    AND deployment_id=sqlc.arg(deployment_id)::uuid AND node_id=sqlc.arg(node_id)::uuid
    AND wake_id=sqlc.arg(wake_id)::uuid AND state=sqlc.arg(expected_state)::text
    AND (sqlc.arg(expected_state)::text<>'warm' OR EXISTS (
        SELECT 1 FROM runtime_instance_config_proofs proof WHERE proof.instance_id=instances.id
            AND proof.wake_id=instances.wake_id AND proof.node_id=instances.node_id
            AND proof.deployment_id=instances.deployment_id
            AND proof.config_fingerprint=sqlc.arg(config_fingerprint)::text))
RETURNING id::text AS id, app_id::text AS app_id, deployment_id::text AS deployment_id,
    state, coalesce(netns,'')::text AS netns, coalesce(guest_uid,0)::integer AS guest_uid,
    coalesce(host(host_ip),'')::text AS host_ip, ram_mb, started_at, last_request_at, parked_at,
    node_id::text AS node_id, wake_id::text AS wake_id, framework_ready_at, tail_count, mode, request_count;

-- name: SaveRuntimeInstanceConfigProof :exec
INSERT INTO runtime_instance_config_proofs(instance_id,wake_id,node_id,deployment_id,environment_id,scope,secret_fingerprint,config_fingerprint)
SELECT id,wake_id,node_id,deployment_id,sqlc.narg(environment_id)::uuid,sqlc.arg(scope)::text,
    sqlc.arg(secret_fingerprint)::text,sqlc.arg(config_fingerprint)::text
FROM instances WHERE id=sqlc.arg(instance_id)::uuid
ON CONFLICT(instance_id) DO UPDATE SET wake_id=excluded.wake_id,node_id=excluded.node_id,
    deployment_id=excluded.deployment_id,environment_id=excluded.environment_id,scope=excluded.scope,
    secret_fingerprint=excluded.secret_fingerprint,config_fingerprint=excluded.config_fingerprint;

-- name: ReadRuntimeInstanceConfigProof :one
SELECT proof.deployment_id::text AS deployment_id,COALESCE(proof.environment_id::text,'')::text AS environment_id,
    proof.scope,proof.secret_fingerprint,proof.config_fingerprint
FROM runtime_instance_config_proofs proof
JOIN instances i ON i.id=proof.instance_id AND i.wake_id=proof.wake_id AND i.node_id=proof.node_id AND i.deployment_id=proof.deployment_id
JOIN apps a ON a.id=i.app_id
WHERE i.id=sqlc.arg(instance_id)::uuid AND a.id=sqlc.arg(app_id)::uuid AND a.account_id=sqlc.arg(account_id)::uuid AND a.status<>'deleted';

-- name: UpdateInstanceStateIf :execrows
UPDATE instances SET state=sqlc.arg(next_state)::text,
    parked_at=CASE WHEN sqlc.arg(next_state)::text='parked' THEN now() ELSE parked_at END,
    terminal_at=CASE WHEN sqlc.arg(next_state)::text IN ('stopped','failed') THEN clock_timestamp() ELSE terminal_at END
WHERE id=sqlc.arg(instance_id)::uuid AND state=sqlc.arg(expected_state)::text;

-- name: ListInstancesForApp :many
select id, app_id, deployment_id, state, coalesce(netns, ''), coalesce(guest_uid, 0),
       coalesce(host_ip::text, ''), ram_mb, started_at, last_request_at, parked_at
from instances where app_id = $1 order by started_at desc;

-- name: ListFirstSuccessfulRequestsForAccountsCreatedSince :many
-- Operator beta funnel: one bounded aggregate read replaces an N+1
-- ListInstancesForAccount loop. last_request_at is stamped only after a
-- successful public request and terminal instances remain for 30 days, which
-- fully covers the 14-day beta cohort window.
select a.account_id, min(i.last_request_at)::timestamptz as first_success_at
from instances i
join apps a on a.id = i.app_id
join accounts acct on acct.id = a.account_id
where acct.created_at >= $1
  and i.last_request_at is not null
group by a.account_id
order by a.account_id;

-- name: UpdateInstanceState :exec
update instances set state = $2 where id = $1;

-- name: BumpInstanceTailCount :one
-- issue #667 / ADR-078 — atomically apply delta to the instance's
-- `tail_count` column and return the post-update value. The
-- GREATEST(…, 0) floor mirrors DecrementInstanceTailCount's safety
-- property: a stale receipt from a guest that just parked cannot
-- underflow the counter, and the 5s watchdog in snapshotAndPark
-- force-parks regardless. RETURNING tail_count lets the caller
-- (vmmd's MarkInstanceTailTerminal) learn the new value without a
-- follow-up SELECT. Returns ErrNotFound when the instance row is
-- missing (pgx.ErrNoRows maps to state.ErrNotFound in pgstore).
update instances
   set tail_count = GREATEST(tail_count + $2, 0)
 where id = $1
returning tail_count;

-- name: DecrementInstanceTailCount :exec
-- issue #667 / ADR-078 — canonical "tail task reached terminal" path.
-- Equivalent to BumpInstanceTailCount(ctx, id, -n) but kept as a
-- separate method because every decrement site is a terminal event
-- receipt, and the explicit name makes the call sites self-
-- documenting. n is the number of tail tasks to decrement by (1 for
-- the steady-state path, the full unfinished-tail count for the
-- snapshotAndPark watchdog). The GREATEST(…, 0) floor is a
-- defence-in-depth guard against races where a receipt lands after
-- the runner exited cleanly: the counter floors at 0 rather than
-- underflowing, which would permanently stall the schedd reaper's
-- tail_count > 0 early-out. Returns ErrNotFound when the instance
-- row is missing.
update instances
   set tail_count = GREATEST(tail_count - $2, 0)
 where id = $1;

-- name: GetInstanceTailCount :one
-- issue #667 / ADR-078 — read-only probe for the snapshotAndPark
-- 5s watchdog's poll loop. Single SELECT … FROM instances WHERE
-- id = $1; the column is on the hot path so the row is already in
-- shared_buffers under normal load. Returns ErrNotFound when the
-- instance row is missing.
select tail_count from instances where id = $1;

-- name: CreateBuild :one
insert into builds (id, deployment_id, kind, source_bytes, status, log_path)
values (gen_random_uuid(), $1, $2, $3, 'queued', $4)
returning id, deployment_id, kind, source_bytes, status, failure_class, log_path, started_at, finished_at, enqueued_at, cache_status, cache_key_sha256;

-- name: BuildByID :one
select id, deployment_id, kind, source_bytes, status, failure_class, log_path, started_at, finished_at, enqueued_at, cache_status, cache_key_sha256
from builds where id = $1;

-- name: BuildByDeployment :one
select id, deployment_id, kind, source_bytes, status, failure_class, log_path, started_at, finished_at, enqueued_at, cache_status, cache_key_sha256
from builds where deployment_id = $1 order by started_at desc nulls last limit 1;

-- name: UpdateBuildStatus :exec
update builds set
  status = $2,
  failure_class = $3,
  started_at = case when $4::boolean then now() else started_at end,
  finished_at = case when $5::boolean then now() else finished_at end
where id = $1;

-- name: CreateSession :one
-- IAM-3 (ADR-039, issue #187 + #244 merged). One row per dashboard login.
-- Caller has already generated the uuid (the envelope seal needs the same
-- value). issued_ip is an inet ('' cast to NULL means "RemoteAddr
-- unparseable" — surfaced as "" on read by coalesce(host(...))).
insert into sessions (id, account_id, issued_ip, issued_ua)
values ($1, $2, nullif($3, '')::inet, nullif($4, ''))
returning id, account_id,
          coalesce(host(issued_ip), '') as issued_ip,
          coalesce(issued_ua, '') as issued_ua,
          issued_at, last_seen_at, revoked_at;

-- name: GetSession :one
-- Primary-key lookup; called on every authenticated dashboard request.
-- sql.ErrNoRows from pgx maps to state.ErrNotFound in pgstore.
select id, account_id,
       coalesce(host(issued_ip), '') as issued_ip,
       coalesce(issued_ua, '') as issued_ua,
       issued_at, last_seen_at, revoked_at
from sessions where id = $1;

-- name: RevokeSession :one
-- Account-scoped atomic stamp. WHERE includes account_id so a
-- cross-account DELETE returns 0 rows (handler maps false → 404) —
-- IDOR is a persistence invariant, not a handler check.
-- coalesce(revoked_at, now()) makes the call idempotent on already
-- revoked rows (returns 0 rows).
update sessions set revoked_at = coalesce(revoked_at, now())
where id = $1 and account_id = $2 and revoked_at is null
returning id;

-- name: ListSessions :many
-- Active rows only, newest first. Partial index keeps the scan tight.
select id, account_id,
       coalesce(host(issued_ip), '') as issued_ip,
       coalesce(issued_ua, '') as issued_ua,
       issued_at, last_seen_at, revoked_at
from sessions where account_id = $1 and revoked_at is null
order by issued_at desc;

-- name: RevokeAllSessions :many
-- Revokes every active row for accountID except the supplied sid
-- (the calling session). Returns the revoked ids for audit.
update sessions set revoked_at = now()
where account_id = $1 and id <> $2 and revoked_at is null
returning id;

-- name: TouchSessionLastSeen :exec
-- Best-effort, fire-and-forget. Allowed on revoked rows (observability
-- signal only; not authorization). pgx interface returns nothing.
update sessions set last_seen_at = now() where id = $1;

-- name: InsertComputeNodeHeartbeat :exec
-- CP-1 (operator observability): append one row to the heartbeat
-- history. The schedd Heartbeat.Tick goroutine is the only writer.
-- We deliberately do NOT use ON CONFLICT DO NOTHING — a duplicate
-- (node_id, received_at) is observable as a SQLSTATE 23505 unique-
-- violation, which the writer logs as a warning. A silently-deduped
-- stamp would mask a future bug where the scheduler tick fires twice.
-- received_at and last_heartbeat_at are passed by the caller; the
-- column default now() is intentionally NOT used so the writer
-- controls the wall-clock pair (the property test depends on
-- caller-supplied timestamps for deterministic gap classification).
insert into compute_node_heartbeats (node_id, received_at, last_heartbeat_at, source)
values ($1, $2, $3, $4);

-- name: ListComputeNodeHeartbeats :many
-- CP-1: read heartbeat history for one node, newest first. The
-- $2 parameter is nullable: passing pgtype.Timestamptz{} (the Go
-- zero value, mapped to SQL NULL by sqlc) means "no lower bound,
-- return most-recent N"; passing a populated timestamptz means
-- "history since t". The composite index
-- compute_node_heartbeats_node_at_idx (node_id, received_at desc)
-- matches this read shape.
--
-- The endpoint passes a hard-cap limit (default 200, max 2000). The
-- composite index is enough for the routine 30s × 60 nodes × 24h
-- steady-state workload; a 7-day retention sweep is a follow-on.
select id, node_id, received_at, last_heartbeat_at, source
from compute_node_heartbeats
where node_id = $1
  and ($2::timestamptz is null or received_at >= $2)
order by received_at desc
limit $3;

-- --- Organizations (ADR-061, IAM-6, PR 2) -------------------------------
--
-- PR 2's sqlc queries cover the deterministic reads + simple writes. The
-- tx-heavy methods (CreateOrg with initial owner membership; RemoveOrgMember
-- with last-owner FOR UPDATE; ConsumeOrgInvitation with cap check + email
-- equality + membership insert + invitation UPDATE in one tx) stay as
-- inline SQL in pgstore.go — they don't fit the :one / :many / :exec
-- sqlc surface cleanly and the existing precedent (CreateAppIfUnderQuota,
-- ConsumeRecoveryCode, ApplyProjectPlan) renders those inline.

-- name: CreateOrg :one
insert into orgs (
    slug,
    name,
    personal_org,
    personal_owner_account_id,
    plan,
    status,
    provider_customer_id,
    stripe_subscription_item,
    deleted_pending
) values (
    $1, $2, $3, $4, $5, $6, nullif($7, ''), nullif($8, ''), false
)
returning
    id, slug, name, personal_org,
    coalesce(personal_owner_account_id::text, ''),
    plan, status,
    coalesce(provider_customer_id, ''),
    coalesce(stripe_subscription_item, ''),
    deleted_pending,
    created_at, updated_at;

-- name: OrgByID :one
select
    id, slug, name, personal_org,
    coalesce(personal_owner_account_id::text, ''),
    plan, status,
    coalesce(provider_customer_id, ''),
    coalesce(stripe_subscription_item, ''),
    deleted_pending,
    created_at, updated_at
from orgs
where id = $1;

-- name: OrgBySlug :one
select
    id, slug, name, personal_org,
    coalesce(personal_owner_account_id::text, ''),
    plan, status,
    coalesce(provider_customer_id, ''),
    coalesce(stripe_subscription_item, ''),
    deleted_pending,
    created_at, updated_at
from orgs
where lower(slug) = lower($1);

-- name: OrgByPersonalAccount :one
select
    id, slug, name, personal_org,
    coalesce(personal_owner_account_id::text, ''),
    plan, status,
    coalesce(provider_customer_id, ''),
    coalesce(stripe_subscription_item, ''),
    deleted_pending,
    created_at, updated_at
from orgs
where personal_org = true
  and personal_owner_account_id = $1;

-- name: ListOrgsForAccount :many
select
    o.id, o.slug, o.name, o.personal_org,
    coalesce(o.personal_owner_account_id::text, ''),
    o.plan, o.status,
    coalesce(o.provider_customer_id, ''),
    coalesce(o.stripe_subscription_item, ''),
    o.deleted_pending,
    o.created_at, o.updated_at
from orgs o
join org_memberships m on m.org_id = o.id
where m.account_id = $1
  and m.removed_at is null
order by o.slug;

-- name: UpdateOrgPlan :exec
update orgs set plan = $2, updated_at = now() where id = $1;

-- name: UpdateOrgStatus :exec
update orgs set status = $2, updated_at = now() where id = $1;

-- name: SoftDeleteOrg :exec
update orgs set deleted_pending = true, status = 'deleted_pending', updated_at = now() where id = $1;

-- name: ListOrgMembers :many
select
    org_id, account_id, role,
    coalesce(invited_by_account_id::text, ''),
    joined_at, removed_at
from org_memberships
where org_id = $1
order by joined_at;

-- name: OrgMemberByAccount :one
select
    org_id, account_id, role,
    coalesce(invited_by_account_id::text, ''),
    joined_at, removed_at
from org_memberships
where org_id = $1 and account_id = $2;

-- name: OrgInvitationByTokenHash :one
select
    id,
    org_id,
    email::text as email,
    role,
    token_hash,
    coalesce(invited_by_account_id::text, ''),
    expires_at,
    consumed_at,
    revoked_at,
    coalesce(accepting_account_id::text, ''),
    created_at
from org_invitations
where token_hash = $1;

-- name: ListOrgInvitationsForOrg :many
select
    id,
    org_id,
    email::text as email,
    role,
    token_hash,
    coalesce(invited_by_account_id::text, ''),
    expires_at,
    consumed_at,
    revoked_at,
    coalesce(accepting_account_id::text, ''),
    created_at
from org_invitations
where org_id = $1
order by created_at desc;

-- name: ExpireOrgInvitations :execrows
update org_invitations
set revoked_at = now()
where consumed_at is null
  and revoked_at is null
  and expires_at <= $1;

-- name: TrafficAnomalyAggregate :many
-- ADR-091 §3.6 — operator observability backend (PR #2).
-- Hour-of-day baseline over a rolling 7-day window:
--   * baseline is per (account_id, app_id, EXTRACT(HOUR FROM minute))
--   * an anomaly is a row whose current mb_seconds exceeds
--     baseline_mean + 3.0*baseline_stddev (or baseline_mean * 5.0 when
--     baseline_stddev < 1.0 — guards against noisy-low-traffic apps
--     where a tiny stddev explodes the Z-score).
--   * $1 since       — RFC 3339 lower bound for "current" rows
--                      (handler default: now() - 24h, hard cap 168h)
--   * $2 baseline    — RFC 3339 lower bound for the baseline pool
--                      (handler default: now() - 7d, fixed by ADR)
--   * $3 limit       — top-N by deviation (handler default 50, cap 200;
--                      cast to int8 so sqlc emits int64 Params and the
--                      handler's int→int64 widening is safe)
-- Result columns:
--   * account_id, app_id, minute, current_mb_seconds
--   * baseline_mean, baseline_stddev, baseline_samples
--   * z_score, reason ('hour_of_day' | 'raw_z')
-- Index path: usage_minutes primary key (instance_id, minute) is
-- fine for current-minute scans in a 24h window. The 7-day baseline
-- pool scans the same primary key. For the fleet-wide aggregate a
-- future ADR adds (account_id, app_id, minute) as a covering index;
-- PR #2 does NOT add it (single-box posture; multi-host moves to
-- PromQL per ADR-091 §3.6).
with baseline as (
    select account_id,
           app_id,
           extract(hour from usage_minutes.minute) as hour_of_day,
           avg(mb_seconds)::float8 as mean_mb_seconds,
           coalesce(stddev_pop(mb_seconds), 0)::float8 as stddev_mb_seconds,
           count(*)::int as sample_count
    from usage_minutes
    where usage_minutes.minute >= $2
      and usage_minutes.minute <  $1
      and mb_seconds > 0
    group by account_id, app_id, extract(hour from usage_minutes.minute)
),
current_pool as (
    select account_id,
           app_id,
           minute,
           sum(mb_seconds)::float8 as current_mb_seconds
    from usage_minutes
    where minute >= $1
      and mb_seconds > 0
    group by account_id, app_id, minute
),
scored as (
    select c.account_id,
           c.app_id,
           c.minute,
           c.current_mb_seconds,
           b.mean_mb_seconds,
           b.stddev_mb_seconds,
           b.sample_count,
           case
               when b.sample_count < 3 then null
               when b.stddev_mb_seconds < 1.0 and c.current_mb_seconds >= 5.0 * b.mean_mb_seconds
                   and b.mean_mb_seconds > 0 then (c.current_mb_seconds - b.mean_mb_seconds) / 5.0
               when b.stddev_mb_seconds >= 1.0
                   and c.current_mb_seconds >= b.mean_mb_seconds + 3.0 * b.stddev_mb_seconds then
                   (c.current_mb_seconds - b.mean_mb_seconds) / b.stddev_mb_seconds
               else null
           end as z_score,
           case
               when b.stddev_mb_seconds < 1.0 then 'raw_z'
               else 'hour_of_day'
           end as reason
    from current_pool c
    join baseline b
      on c.account_id = b.account_id
     and c.app_id = b.app_id
     and extract(hour from c.minute) = b.hour_of_day
)
select account_id,
       app_id,
       minute,
       current_mb_seconds,
       mean_mb_seconds,
       stddev_mb_seconds,
       sample_count,
       z_score,
       reason
from scored
where z_score is not null
order by z_score desc
limit $3::int8;

-- name: PerAccountRateLimitAggregate :many
-- ADR-091 §3.5 — operator observability backend (PR #2) durable view.
-- Aggregates `events` rows of kind='auth.rate_limited' over a rolling
-- window, grouped by subject (account_id, NULL for anonymous actors).
--   * $1 since  — RFC 3339 lower bound (handler default: now() - 24h,
--                 hard cap 168h per pkg/api/limits.go::ObsAdminWindowMaxHours)
--   * $2 limit  — top-N by hits (handler default 100, cap 500;
--                 cast to int8 so sqlc emits int64 Params and the
--                 handler's int→int64 widening is safe)
-- Anonymous (subject IS NULL) rows are bucketed under a single
-- account_id = NULL row so the operator UI can render the "anon
-- credential stuffing" signal distinctly from named-account bursts.
-- Index path: events_kind_at_idx (added in migration 00190) covers
-- the kind + at DESC predicate. The subject grouping is in-memory
-- after the index scan.
select coalesce(subject, '00000000-0000-0000-0000-000000000000'::uuid) as account_id,
       count(*)::int as hits,
       max(at) as last_event_at
from events
where kind = 'auth.rate_limited'
  and at >= $1
group by coalesce(subject, '00000000-0000-0000-0000-000000000000'::uuid)
order by hits desc, last_event_at desc
limit $2::int8;


-- name: TrafficAnomalyAggregateByNode :many
-- PR #4 (ADR-092 §3.4 amendment) — per-node variant of
-- TrafficAnomalyAggregate. Joins usage_minutes to instances to
-- recover the hosting node_id, then groups by
-- (account_id, app_id, node_id, EXTRACT(HOUR FROM minute)) for
-- the baseline. The current_pool also groups by node_id so the
-- "today" anomaly is per-node, not per-app-wide.
--
-- Why a separate query and not a sqlc parameter on the existing
-- one: the baseline math is identical, but the GROUP BY keys
-- differ by one column, and trying to thread that through a
-- nullable WHERE filter would either lose the per-node grain
-- (NULL filter collapses the group) or return the wrong rollup
-- (a per-node "current" against an app-wide baseline reports
-- spurious anomalies when the fleet is unevenly loaded). A
-- separate query keeps each path simple and self-contained.
--
-- Index path: same as TrafficAnomalyAggregate — usage_minutes
-- primary key (instance_id, minute) is fine for the 24h current
-- window in single-box posture. The instances.node_id lookup
-- is by PK; the join is O(matches) on the PK.
with baseline as (
    select um.account_id,
           um.app_id,
           n.id as node_id,
           extract(hour from um.minute) as hour_of_day,
           avg(um.mb_seconds)::float8 as mean_mb_seconds,
           coalesce(stddev_pop(um.mb_seconds), 0)::float8 as stddev_mb_seconds,
           count(*)::int as sample_count
    from usage_minutes um
    join instances i on i.id = um.instance_id
    join compute_nodes n on n.id = i.node_id
    where um.minute >= $2
      and um.minute <  $1
      and um.mb_seconds > 0
    group by um.account_id, um.app_id, n.id, extract(hour from um.minute)
),
current_pool as (
    select um.account_id,
           um.app_id,
           n.id as node_id,
           um.minute,
           sum(um.mb_seconds)::float8 as current_mb_seconds
    from usage_minutes um
    join instances i on i.id = um.instance_id
    join compute_nodes n on n.id = i.node_id
    where um.minute >= $1
      and um.mb_seconds > 0
    group by um.account_id, um.app_id, n.id, um.minute
),
scored as (
    select c.account_id,
           c.app_id,
           c.node_id,
           c.minute,
           c.current_mb_seconds,
           b.mean_mb_seconds,
           b.stddev_mb_seconds,
           b.sample_count,
           case
               when b.sample_count < 3 then null
               when b.stddev_mb_seconds < 1.0 and c.current_mb_seconds >= 5.0 * b.mean_mb_seconds
                   and b.mean_mb_seconds > 0 then (c.current_mb_seconds - b.mean_mb_seconds) / 5.0
               when b.stddev_mb_seconds >= 1.0
                   and c.current_mb_seconds >= b.mean_mb_seconds + 3.0 * b.stddev_mb_seconds then
                   (c.current_mb_seconds - b.mean_mb_seconds) / b.stddev_mb_seconds
               else null
           end as z_score,
           case
               when b.stddev_mb_seconds < 1.0 then 'raw_z'
               else 'hour_of_day'
           end as reason
    from current_pool c
    join baseline b
      on c.account_id = b.account_id
     and c.app_id    = b.app_id
     and c.node_id   = b.node_id
     and extract(hour from c.minute) = b.hour_of_day
)
select account_id,
       app_id,
       node_id,
       minute,
       current_mb_seconds,
       mean_mb_seconds,
       stddev_mb_seconds,
       sample_count,
       z_score,
       reason
from scored
where z_score is not null
order by z_score desc
limit $3::int8;

-- ---------------------------------------------------------------------------
-- PR-D / ADR-012 §7 amendment — per-tenant GitHub App webhook secret.
--
-- The two queries below are exposed by pkg/state/pgstore.go as
-- (s *PgStore).UpsertGithubWebhookSecret and
-- (s *PgStore).GetGithubWebhookSecret. The body is hand-curated
-- rather than sqlc-generated because the github_installations pair
-- is also hand-curated (same precedent). The schema lives in
-- migrations/00212_github_webhook_secrets.sql (renumbered from
-- 00208 → 00209 → 00212 in the slot-collision cluster; see the
-- migration's header for the cross-pr-slot-fence chain).
-- ---------------------------------------------------------------------------

-- name: UpsertGithubWebhookSecret :execrows
-- Installs or rotates the per-tenant webhook secret for an
-- installation_id. ON CONFLICT (installation_id) DO UPDATE so a
-- rotation is one statement. upgradedAt + upgradedBy form a §11
-- audit trail.
INSERT INTO github_webhook_secrets (installation_id, secret_value, upgraded_by)
VALUES ($1, $2, $3)
ON CONFLICT (installation_id) DO UPDATE
SET secret_value = EXCLUDED.secret_value,
    upgraded_at  = now(),
    upgraded_by  = EXCLUDED.upgraded_by;

-- name: GetGithubWebhookSecret :one
-- Returns the bytea secret for the given installation_id. The
-- daemon-side resolver treats pgx.ErrNoRows as fail-closed (the
-- webhook is rejected rather than falling back to the platform-
-- wide FAAS_GITHUB_WEBHOOK_SECRET).
SELECT secret_value FROM github_webhook_secrets WHERE installation_id = $1;

-- ---------------------------------------------------------------------------
-- ADR-096 customer-facing automatic error grouping.
-- Tables live in migrations/00222_app_errors.sql. gatewayd-internal
-- writes via the apid gRPC IncrementAppError handler (pkg/apidgrpc/
-- apperrors.proto); apid is the only direct writer to the table
-- per the owner rules. The apid handlers in
-- cmd/apid/handlers_app_errors.go (PR-B) and the nightly purge
-- cron in cmd/apid/app_errors_purge.go (PR-A) read here.
--
-- Index paths pinned in the migration file (NOT regenerated here
-- — sqlc doesn't manage indexes, only the typed query surface).
-- ---------------------------------------------------------------------------

-- name: IncrementAppError :one
-- ADR-096 §3.5 dedupe-merge INSERT. The grpc_server_apperrors.go
-- handler runs this inside a single pgx transaction per stream
-- batch. ON CONFLICT target is app_errors_dedupe_uniq (the
-- migration's UNIQUE on (account_id, app_id, fingerprint)).
-- The dedupe window is enforced by the writer's LRU; this
-- unique constraint is the last-resort tripwire.
--
-- Returns (inserted bool) via the canonical Postgres
-- (xmax = 0) trick: xmax is 0 on a fresh INSERT and non-zero
-- on an UPDATE. This lets the handler distinguish
-- outcomeInserted vs outcomeMerged on the wire — the gateway
-- uses that signal to update its in-process LRU freshness.
INSERT INTO app_errors (
    id, account_id, app_id, deployment_id, fingerprint,
    route, http_status, error_class, sample_message,
    count, request_count, first_seen_at, last_seen_at,
    last_instance_id, last_node_id, last_region, last_commit_sha,
    last_deployment_tag, last_deployment_created_at, last_image_digest
) VALUES (
    $1, $2, $3, $4, $5,
    $6, $7, $8, $9,
    1, 1, $10, $10,
    $11, $12, $13, $14, $15, $16, $17
)
ON CONFLICT (account_id, app_id, fingerprint) DO UPDATE SET
    count         = app_errors.count + 1,
    request_count = app_errors.request_count + 1,
    last_seen_at  = greatest(app_errors.last_seen_at, $10),
    last_instance_id = COALESCE(NULLIF(EXCLUDED.last_instance_id, ''), app_errors.last_instance_id),
    last_node_id = COALESCE(NULLIF(EXCLUDED.last_node_id, ''), app_errors.last_node_id),
    last_region = COALESCE(NULLIF(EXCLUDED.last_region, ''), app_errors.last_region),
    last_commit_sha = COALESCE(NULLIF(EXCLUDED.last_commit_sha, ''), app_errors.last_commit_sha),
    last_deployment_tag = COALESCE(NULLIF(EXCLUDED.last_deployment_tag, ''), app_errors.last_deployment_tag),
    last_deployment_created_at = COALESCE(NULLIF(EXCLUDED.last_deployment_created_at, ''), app_errors.last_deployment_created_at),
    last_image_digest = COALESCE(NULLIF(EXCLUDED.last_image_digest, ''), app_errors.last_image_digest)
RETURNING (xmax = 0) AS inserted;

-- name: InsertAppErrorRequest :exec
-- One row per request that hit the grouped fingerprint. No
-- ON CONFLICT — every request gets its own row. request_count
-- on app_errors is bumped on the paired IncrementAppError
-- call; the read path derives the joined total at query time.
INSERT INTO app_error_requests (
    id, account_id, app_id, fingerprint, request_id, received_at,
    route, http_status, error_class, sample_message,
    deployment_id, headers_sample, redactions, instance_id, node_id,
    region, commit_sha, deployment_tag, deployment_created_at, image_digest
) VALUES (
    $1, $2, $3, $4, $5, $6,
    $7, $8, $9, $10,
    $11, $12, $13, $14, $15, $16, $17, $18, $19, $20
);

-- name: ListAppErrorGroups :many
-- ADR-096 §4.3 summary endpoint. Top-N grouped fingerprints for
-- one (account_id, app_id) over a (since, until) window.
-- Cursor pagination via the (count, last_seen_at, fingerprint)
-- compound tuple (distinct from the operator's (created_at, id)
-- cursor). All three columns are part of the ORDER BY so the
-- cursor predicate must include all three — dropping `count`
-- breaks pagination: rows with smaller count but newer
-- last_seen_at are silently dropped across pages (the cursor
-- predicate on (last_seen_at, fingerprint) only considers the
-- inner order, missing the leading count-DESC boundary).
-- Index path: app_errors_account_app_last_seen_idx covers the
-- primary scan; the (count DESC) sort happens post-filter on the
-- bounded set (limit ≤ AppErrorsSummaryMaxLimit = 100).
--
-- sqlc.arg(name) annotations disambiguate the cursor predicate
-- types — without them sqlc infers the timestamps as timestamptz
-- from the leading (count, last_seen_at) references and breaks
-- pagination.
--
-- cursor_count is a non-nullable bigint, so "no cursor" arrives as 0
-- (count is always >= 1). The predicate used to test IS NULL, which
-- never held: the first page matched no rows and the summary was
-- always empty. fingerprint sorts DESC to agree with the row-value
-- comparison; ASC made pages repeat or skip groups that tie on
-- (count, last_seen_at).
SELECT
    id, fingerprint, error_class, route, http_status,
    count, request_count, first_seen_at, last_seen_at,
    sample_message, last_instance_id, last_node_id, last_region,
    last_commit_sha, last_deployment_tag, last_deployment_created_at,
    last_image_digest
FROM app_errors
WHERE account_id = sqlc.arg('account_id')
  AND app_id     = sqlc.arg('app_id')
  AND last_seen_at >= sqlc.arg('since')
  AND last_seen_at <= sqlc.arg('until')
  AND (sqlc.arg('cursor_count')::bigint = 0
       OR count < sqlc.arg('cursor_count')
       OR (count = sqlc.arg('cursor_count')
           AND (last_seen_at, fingerprint) < (sqlc.arg('cursor_last_seen'), sqlc.arg('cursor_fingerprint')::text)))
ORDER BY count DESC, last_seen_at DESC, fingerprint DESC
LIMIT sqlc.arg('limit');

-- name: ListAppErrorRequests :many
-- Drill-down rows for one fingerprint. Cursor paginated via
-- (received_at, request_id). Index path:
-- app_error_requests_drill_idx. Does NOT include headers_sample
-- or redactions — those are returned only by GetAppErrorSample.
--
-- sqlc.arg(name) annotations disambiguate the cursor predicate
-- types — without them sqlc infers $5 as timestamptz from the
-- leading (received_at) reference, breaking pagination.
SELECT
    id, request_id, received_at, route, http_status,
    error_class, sample_message, deployment_id, instance_id, node_id,
    region, commit_sha, deployment_tag, deployment_created_at, image_digest
FROM app_error_requests
WHERE account_id  = sqlc.arg('account_id')
  AND app_id      = sqlc.arg('app_id')
  AND fingerprint = sqlc.arg('fingerprint')
  AND (sqlc.arg('cursor_received_at')::timestamptz IS NULL
       OR (received_at, request_id) < (sqlc.arg('cursor_received_at'), sqlc.arg('cursor_request_id')::uuid))
ORDER BY received_at DESC, request_id DESC
LIMIT sqlc.arg('limit');

-- name: GetAppErrorSample :one
-- Single oldest request row for one fingerprint, used by the
-- UI's "what does this look like" preview. Returns
-- headers_sample + redactions for the wire-side "we redacted
-- X / Y / Z" badge.
SELECT
    id, request_id, received_at, route, http_status,
    error_class, sample_message, deployment_id,
    headers_sample, redactions, instance_id, node_id, region, commit_sha,
    deployment_tag, deployment_created_at, image_digest
FROM app_error_requests
WHERE account_id  = $1
  AND app_id      = $2
  AND fingerprint = $3
ORDER BY received_at ASC, request_id ASC
LIMIT 1;

-- name: ListAppErrorFingerprintsForPurge :many
-- Nightly retention purge read path (cmd/apid/app_errors_purge.go).
-- Returns IDs of app_errors rows for an account older than
-- `cutoff`. Capped at 10000 per call so the DELETE loop can
-- iterate without blocking. Sorted by last_seen_at ASC so the
-- oldest rows are deleted first (a future eviction policy
-- could swap to "least recent activity" without touching this
-- query).
SELECT id FROM app_errors
WHERE account_id = $1
  AND last_seen_at < $2
ORDER BY last_seen_at ASC
LIMIT $3;

-- name: DeleteAppErrorsByIDs :exec
DELETE FROM app_errors WHERE id = ANY($1::uuid[]);

-- name: DeleteAppErrorRequestsByIDs :exec
DELETE FROM app_error_requests WHERE id = ANY($1::uuid[]);

-- name: DeleteAppErrorRequestsOlderThan :exec
-- The retention purge also runs on app_error_requests
-- independently — a customer's drill-down can age out without
-- the parent fingerprint row being removed (e.g. Hobby with
-- 100 rows/fingerprint cap evicts oldest request rows first).
DELETE FROM app_error_requests
WHERE account_id = $1
  AND received_at < $2;

-- ---------------------------------------------------------------------------
-- ADR-098 connection-aware execution (§9.A). Tables live in
-- migrations/00226_data_upstreams.sql. apid is the only writer to
-- data_upstreams (env-classifier side, PR-B); meterd is the only writer
-- to data_upstream_probes (probe loop, PR-C). schedd reads
-- data_upstream_probes via ListDataUpstreamProbesByHostRegion on wake
-- (PR-B wires pkg/sched/upstream_affinity.go). The Store interface is
-- extended in pkg/state/store.go; pgstore + memstore stubs added in
-- PR-A so the surface compiles — production reads/writes land in PR-B.
--
-- Index paths pinned in the migration file (NOT regenerated here —
-- sqlc doesn't manage indexes, only the typed query surface):
--   - data_upstreams_app_created_idx
--   - data_upstreams_host_redacted_idx
--   - data_upstreams_dedupe_uniq (UNIQUE)
--   - partitioned data_upstream_probes + default partition
-- ---------------------------------------------------------------------------

-- name: InsertDataUpstream :one
-- Dedupe-merge INSERT for data_upstreams. Mirrors the
-- IncrementAppError ON CONFLICT pattern (queries.sql:906).
-- The handler (PR-B's cmd/apid/extract.go) targets
-- data_upstreams_dedupe_uniq on (app_id, scope,
-- deployment_scope, kind, host, port) per ADR-098 amendment
-- (issue #954 / 00281_data_upstreams_deployment_scope.sql).
-- On conflict: bump last_seen_at; refresh last_rtt_ms /
-- last_probed_at / declared_region / deployment_scope
-- from EXCLUDED so a re-classification re-stamps the
-- deployment overlay on the latest observation. id is
-- caller-supplied (uuidv7) so the row identity is stable
-- across the dedupe-merge.
INSERT INTO data_upstreams (
    id, account_id, app_id, source, scope, deployment_scope, kind, host, port,
    host_redacted_hash, declared_region,
    last_rtt_ms, last_probed_at,
    last_seen_at, created_at
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9,
    $10, $11,
    $12, $13,
    now(), now()
)
ON CONFLICT (app_id, scope, deployment_scope, kind, host, port) DO UPDATE SET
    source          = EXCLUDED.source,
    declared_region = EXCLUDED.declared_region,
    last_rtt_ms     = EXCLUDED.last_rtt_ms,
    last_probed_at  = EXCLUDED.last_probed_at,
    deployment_scope = EXCLUDED.deployment_scope,
    last_seen_at    = now()
RETURNING id;

-- name: ListDataUpstreamsByApp :many
-- GET /v1/apps/{slug}/upstreams (PR-B). Cursor pagination
-- via (created_at, id) — distinct from app_errors'
-- (last_seen_at, fingerprint) cursor because
-- data_upstreams is a stable list, not a hot recency
-- list. Index path: data_upstreams_app_created_idx.
--
-- Optional ?deployment_scope= server-side filter lands via
-- `cursor_deployment_scope` (issue #954 / ADR-098 amendment).
-- Empty string means "no filter; return all deployments"
-- — the wide-open default. Setting a non-empty value restricts
-- to one deployment. Mirrors the existing ?scope= discipline.
--
-- sqlc.arg(...)::type casts disambiguate the cursor params
-- — without them sqlc named both fields `CreatedAt` (taken
-- from the SELECT list) and the generated Go wrapper bound a
-- timestamptz to the $3 uuid slot, tripping a type error on
-- every cursor page past the first. See the cross-PR slot-
-- fence sqlc.arg-disambiguates-cursor memory; the same
-- pattern pins ListAppErrorGroups.
SELECT
    id, account_id, app_id, source, scope, deployment_scope, kind, host, port,
    host_redacted_hash, coalesce(declared_region, ''),
    last_rtt_ms, last_probed_at, last_seen_at, created_at
FROM data_upstreams
WHERE app_id = sqlc.arg('app_id')::uuid
  AND (sqlc.arg('cursor_deployment_scope')::text IS NULL OR sqlc.arg('cursor_deployment_scope')::text = ''
       OR deployment_scope = sqlc.arg('cursor_deployment_scope')::text)
  AND (sqlc.arg('cursor_created_at')::timestamptz IS NULL
       OR (created_at, id) < (sqlc.arg('cursor_created_at')::timestamptz,
                              sqlc.arg('cursor_id')::uuid))
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg('page_limit')::int;

-- name: GetDataUpstreamByID :one
-- Single-row read for the dashboard's "edit upstream"
-- pane (PR-B). Cursor-safe: no pagination; the handler
-- reads the row directly. Projects the new deployment_scope
-- column (issue #954) so the typed DataUpstream.DeploymentScope
-- in pkg/state/types.go round-trips through sqlc.
SELECT
    id, account_id, app_id, source, scope, deployment_scope, kind, host, port,
    host_redacted_hash, coalesce(declared_region, ''),
    last_rtt_ms, last_probed_at, last_seen_at, created_at
FROM data_upstreams
WHERE id = $1;

-- name: DeleteDataUpstreamByID :exec
-- DELETE /v1/apps/{slug}/upstreams/{id} (PR-B). Soft-
-- delete is rejected by ADR-098 (a soft-deleted row
-- would still trigger pg_notify and confuse schedd);
-- the handler is the only path and uses a hard
-- DELETE. The CASCADE on account_id / app_id handles
-- the GDPR path (delete-account cascades through
-- apps → data_upstreams).
DELETE FROM data_upstreams WHERE id = $1;

-- name: InsertDataUpstreamProbe :exec
-- meterd's probe loop writer (PR-C). One row per
-- (host_redacted_hash, region) per 30s sample. The
-- PK is (id, sampled_at) — id is a caller-supplied
-- uuidv7 so dedupe on retry is trivial. Partitioning
-- on sampled_at gives the meterd loop a hot-write
-- path; the partition creator (PR-C) drops old
-- partitions wholesale.
INSERT INTO data_upstream_probes (
    id, host_redacted_hash, region, kind, sampled_at, rtt_ms,
    ok, error_class, probe_node
) VALUES (
    $1, $2, $3, $4, $5, $6,
    $7, $8, $9
);

-- name: ListDataUpstreamProbesByHostRegion :many
-- schedd's wake-side read path (PR-B/C). Returns the
-- N most recent probe samples for one (host, region)
-- pair, time-windowed to the meterd sliding window
-- (default: 30s × 5min). Index path: the
-- partitioned table's PARTITION BY RANGE on
-- sampled_at — partition pruning drops everything
-- outside the window. The (kind) projection lets
-- schedd key its upstream-affinity map on
-- (kind, region) without joining data_upstreams.
SELECT
    id, host_redacted_hash, region, kind, sampled_at, rtt_ms,
    ok, error_class, coalesce(probe_node, '')
FROM data_upstream_probes
WHERE host_redacted_hash = $1
  AND region = $2
  AND sampled_at >= $3
ORDER BY sampled_at DESC
LIMIT $4;

-- name: PruneDataUpstreamProbesOlderThan :exec
-- Retention purge. The meterd cron calls this
-- hourly with `cutoff = now() - interval '30 days'`
-- (matches the §12 prom_retention_days:15 floor +
-- a 2× safety margin). The partition pruning on
-- sampled_at makes this O(affected partitions),
-- not O(table size). The PR-C partition creator
-- DROPs whole partitions for ranges entirely older
-- than cutoff; this query handles the partial-
-- partition tail (rows in the default partition or
-- the current month that are older than cutoff).
DELETE FROM data_upstream_probes WHERE sampled_at < $1;

-- Issue #757 / ADR-0NN — Trigger primitive (event-source mappings).
-- Mirrors the cron `CreateCron` / `UpdateCron` / `DeleteCron` /
-- `CronByID` / `ListCronsForApp` shape so the apid handler can stay
-- symmetric with the existing cron surface. The dispatch tick
-- (pkg/sched/dispatch_triggers.go, commit #14) writes
-- ClaimTriggerRecords / MarkTriggerRecordSucceeded /
-- MarkTriggerRecordRetry / MarkTriggerRecordDeadLetter, which the
-- schedd uses under FOR UPDATE SKIP LOCKED to drain batches
-- concurrently.
--
-- The FOR UPDATE SKIP LOCKED on ClaimTriggerRecords mirrors the
-- precedent set by ADR-099 PR-C's claim_job_tasks query (issue
-- tracker 'job-task pull'): concurrent schedd replicas each claim
-- disjoint row sets with no advisory-lock plumbing.

-- name: CreateTrigger :one
insert into triggers (account_id, app_id, kind, slug, enabled, config,
                       batch_size_max, batch_window_ms, max_attempts,
                       cron_id, source, payload_max_bytes,
                       broker_poison_strategy)
values ($1, $2, $3, $4, $5, $6::jsonb, $7, $8, $9, $10, $11, $12, $13)
returning id, account_id, app_id, kind, slug, enabled, config,
          batch_size_max, batch_window_ms, max_attempts,
          cron_id, source, payload_max_bytes, broker_poison_strategy,
          created_at, updated_at;

-- name: UpdateTrigger :one
-- Review finding MED-1 (PR #993): the inline SQL at
-- pkg/state/pgstore.go::UpdateTrigger is the source of truth
-- (sqlc-generated UpdateTrigger stub is bypassed because sqlc
-- doesn't model nullable UPDATE parameters); the projection
-- shape is preserved by mirroring the same column list that
-- ListEnabledTriggers uses (filter_criteria is part of the
-- Trigger struct since commit 6 of issue #757 mega-PR).
update triggers set
  enabled = coalesce($2, enabled),
  config = coalesce($3::jsonb, config),
  batch_size_max = coalesce($4, batch_size_max),
  batch_window_ms = coalesce($5, batch_window_ms),
  max_attempts = coalesce($6, max_attempts),
  payload_max_bytes = coalesce($7, payload_max_bytes),
  broker_poison_strategy = coalesce($8, broker_poison_strategy),
  filter_criteria = coalesce($9::jsonb, filter_criteria)
where id = $1 and queue_binding_id is null
returning id, account_id, app_id, kind, slug, enabled, config,
          batch_size_max, batch_window_ms, max_attempts,
          cron_id, source, payload_max_bytes, broker_poison_strategy,
          filter_criteria,
          created_at, updated_at;

-- name: DeleteTrigger :exec
delete from triggers where id = $1 and app_id = $2 and queue_binding_id is null;

-- name: TriggerByID :one
-- ADR-118 / commit 6 of the issue #757 mega-PR: filter_criteria
-- is projected so pgstore.TriggerByID returns the same shape as
-- ListEnabledTriggers (sqlc generates identical column sets as
-- the same Go struct; projections that omit a column produce a
-- distinct Row type that breaks the existing pgstore return
-- type).
select id, account_id, app_id, kind, slug, enabled, config, queue_binding_id, queue_binding_scope, queue_binding_environment_id,
       batch_size_max, batch_window_ms, max_attempts,
       cron_id, source, payload_max_bytes, broker_poison_strategy,
       filter_criteria,
       created_at, updated_at
from triggers where id = $1;

-- name: ListTriggersForApp :many
-- Same rationale as TriggerByID — full Trigger projection so
-- sqlc's generated Row type matches the existing pgstore return
-- type. (commit 6 of the issue #757 mega-PR.)
select id, account_id, app_id, kind, slug, enabled, config, queue_binding_id, queue_binding_scope, queue_binding_environment_id,
       batch_size_max, batch_window_ms, max_attempts,
       cron_id, source, payload_max_bytes, broker_poison_strategy,
       filter_criteria,
       created_at, updated_at
from triggers where app_id = $1 order by created_at desc;

-- name: ListEnabledTriggers :many
-- Pulled by schedd's runTriggerTick on each 1-second cadence. The
-- query is unfiltered by kind because the dispatch tick reads
-- triggers.enabled = true regardless of kind and dispatches via the
-- per-kind poller (pkg/sched/poller.go).
--
-- ADR-118 / issue #757: filter_criteria is included so the dispatch
-- tick can evaluate per-record predicates without a second round-trip
-- (the column is JSONB; empty/null means "no filter").
select id, account_id, app_id, kind, slug, enabled, config, queue_binding_id, queue_binding_scope, queue_binding_environment_id,
       batch_size_max, batch_window_ms, max_attempts,
       cron_id, source, payload_max_bytes, broker_poison_strategy,
       filter_criteria,
       created_at, updated_at
from triggers where enabled = true;

-- name: CountTriggersByApp :one
select count(*) from triggers t where t.app_id = $1
and (t.queue_binding_id is null or exists (
  select 1 from queue_bindings b where b.id=t.queue_binding_id and b.retired_at is null and b.mode='push'));

-- name: CountTriggersByAccount :one
select count(*) from triggers t
join apps a on a.id = t.app_id
where a.account_id = $1 and a.status <> 'deleted'
and (t.queue_binding_id is null or exists (
  select 1 from queue_bindings b where b.id=t.queue_binding_id and b.retired_at is null and b.mode='push'));

-- name: ClaimTriggerRecords :many
-- Persist ownership before returning. SKIP LOCKED alone would release the
-- claim at statement end and let another scheduler deliver the same row.
-- A lost dispatcher becomes eligible again after the ten-minute lease.
WITH due AS MATERIALIZED (
    SELECT candidate.id FROM trigger_records candidate
    WHERE candidate.trigger_id = $1
      AND ((candidate.state IN ('pending','retry') AND candidate.next_fire_at <= now())
        OR (candidate.state = 'claimed' AND candidate.claim_expires_at <= now()))
    ORDER BY candidate.next_fire_at, candidate.id
    LIMIT $2
    FOR UPDATE SKIP LOCKED
), claimed AS (
    UPDATE trigger_records r
       SET state = 'claimed',
           claim_generation = r.claim_generation + 1,
           claim_expires_at = now() + INTERVAL '10 minutes'
      FROM due WHERE r.id = due.id
    RETURNING r.id, r.trigger_id, r.item_identifier, r.payload, r.headers,
              r.metadata, r.state, r.attempts, r.next_fire_at, r.received_at,
              r.last_error, r.last_dispatched_at, r.claim_generation,
              r.claim_expires_at
)
SELECT * FROM claimed ORDER BY next_fire_at, id;

-- name: ClaimTriggerRecordsByItems :many
-- The broker batch is authoritative: claiming an unrelated due row would
-- lease it without dispatching it and block its actual broker delivery.
WITH due AS MATERIALIZED (
    SELECT candidate.id FROM trigger_records candidate
    WHERE candidate.trigger_id = $1
      AND candidate.item_identifier = ANY(sqlc.arg(item_identifiers)::text[])
      AND ((candidate.state IN ('pending','retry') AND candidate.next_fire_at <= now())
        OR (candidate.state = 'claimed' AND candidate.claim_expires_at <= now()))
    ORDER BY candidate.next_fire_at, candidate.id
    FOR UPDATE SKIP LOCKED
), claimed AS (
    UPDATE trigger_records r
       SET state = 'claimed',
           claim_generation = r.claim_generation + 1,
           claim_expires_at = now() + INTERVAL '10 minutes'
      FROM due WHERE r.id = due.id
    RETURNING r.id, r.trigger_id, r.item_identifier, r.payload, r.headers,
              r.metadata, r.state, r.attempts, r.next_fire_at, r.received_at,
              r.last_error, r.last_dispatched_at, r.claim_generation,
              r.claim_expires_at
)
SELECT * FROM claimed ORDER BY next_fire_at, id;

-- name: ListTerminalTriggerRecordItems :many
-- A broker may redeliver after Gregale commits a terminal receipt but before
-- the broker acknowledges it. The current delivery handle can be Acked
-- without dispatching the application again.
SELECT item_identifier FROM trigger_records
WHERE trigger_id = $1
  AND item_identifier = ANY(sqlc.arg(item_identifiers)::text[])
  AND state IN ('succeeded', 'superseded', 'cancelled', 'expired');

-- name: MarkClaimedTriggerRecordSucceeded :execrows
UPDATE trigger_records
   SET state = 'succeeded', last_dispatched_at = now(), claim_expires_at = NULL
 WHERE id = $1 AND state = 'claimed' AND claim_generation = $2
   AND claim_expires_at > now();

-- name: MarkClaimedTriggerRecordRetry :execrows
UPDATE trigger_records
   SET state = 'retry', attempts = attempts + 1, last_error = $3,
       last_dispatched_at = now(), next_fire_at = $4, claim_expires_at = NULL
 WHERE id = $1 AND state = 'claimed' AND claim_generation = $2
   AND claim_expires_at > now();

-- name: MarkClaimedTriggerRecordDeadLetter :execrows
UPDATE trigger_records
   SET state = 'dead_letter', attempts = attempts + 1, last_error = $3,
       last_dispatched_at = now(), claim_expires_at = NULL
 WHERE id = $1 AND state = 'claimed' AND claim_generation = $2
   AND claim_expires_at > now();

-- name: InsertTriggerRecord :one
-- Review finding #1 (PR #910): the dispatcher MUST persist every
-- broker-delivered record into trigger_records BEFORE
-- ClaimTriggerRecords can find them. Without this insert the
-- entire dispatch tick is dead — ClaimTriggerRecords returns 0
-- rows, the broker messages accumulate forever in poller.inFlight,
-- and the unified Trigger primitive never fires a function.
--
-- ON CONFLICT (trigger_id, item_identifier) DO NOTHING mirrors the
-- broker-side dedupe guarantee (kafka per-partition offset,
-- NATS stream sequence, Redis entry-id, SQS receipt handle,
-- in-platform invocation_id — all globally unique within their
-- own ledger). A re-poll after a partial commit + Ack timeout
-- therefore never inserts a duplicate row.
--
-- Returning id gives the dispatcher the trigger_records.id that
-- ClaimTriggerRecords surfaces under FOR UPDATE SKIP LOCKED,
-- bridging the item_identifier → row_id namespace the
-- ReportBatchItemFailures handler needs.
insert into trigger_records (trigger_id, item_identifier, payload, headers, metadata)
values ($1, $2, $3::jsonb, $4::jsonb, $5::jsonb)
on conflict (trigger_id, item_identifier) do nothing
returning id;

-- name: MarkTriggerRecordSucceeded :exec
update trigger_records
   set state = 'succeeded',
       last_dispatched_at = now()
 where id = $1;

-- name: MarkTriggerRecordRetry :exec
update trigger_records
   set state = 'retry',
       attempts = attempts + 1,
       last_error = $2,
       last_dispatched_at = now(),
       next_fire_at = $3
 where id = $1;

-- name: MarkTriggerRecordDeadLetter :exec
update trigger_records
   set state = 'dead_letter',
       attempts = attempts + 1,
       last_error = $2,
       last_dispatched_at = now()
 where id = $1;

-- name: InsertTriggerDeadLetter :exec
-- One row per dead-lettered record. The reason is the closed-vocab
-- failure mode (rate_limited, poison_record, max_attempts,
-- broker_error, plan_quota, payload_too_large, customer_disabled);
-- the routed_to is the closed-vocab terminal action (drop,
-- manual_retry, customer_dlq). detail carries any per-reason payload
-- (the broker error text, the payload size that tripped the 6MB
-- cap, etc.) for the dashboard read-back.
insert into trigger_dead_letter (record_id, trigger_id, reason, routed_to, detail)
values ($1, $2, $3, $4, $5::jsonb);

-- name: ListTriggerDeadLetter :many
select record_id, trigger_id, reason, routed_to, detail, created_at, failure_history
from trigger_dead_letter
where trigger_id = $1
order by created_at desc
limit $2;

-- name: ListTriggerRecordsForTrigger :many
-- Used by GET /v1/triggers/{id}/records (dashboard + apid handler).
-- Returns records in dispatch-time order with the standard projection.
select id, trigger_id, item_identifier, payload, headers, metadata,
       state, attempts, next_fire_at, received_at, last_error,
       last_dispatched_at
from trigger_records
where trigger_id = $1
order by received_at desc
limit $2;

-- name: TriggerRecordIDByItemIdentifier :one
-- Audit round 2 finding #1 (PR #910): deadLetterAll() is invoked
-- with broker-side handles (kafka offset, NATS seq, SQS receipt
-- handle, Redis entry-id, queue invocation_id) but the
-- trigger_dead_letter.record_id column is a UUID FK into
-- trigger_records.id. The dispatcher needs to bridge the
-- item_identifier namespace to the row UUID before calling
-- InsertTriggerDeadLetter — otherwise every rate-limit denial
-- trips SQLSTATE 23503 and the dead_letter row is silently
-- dropped, MarkTriggerRecordDeadLetter updates 0 rows, the
-- record stays in poller.inFlight forever, and the broker
-- offset never advances.
--
-- Returns the trigger_records.id for the (trigger_id,
-- item_identifier) pair, or an empty pgtype.UUID (and nil
-- error) when no row exists yet — that case fires when the
-- rate-limit gate denies a record before InsertTriggerRecord
-- has had a chance to run. Callers MUST treat the empty UUID
-- as "skip the dead_letter insert; leave the record in
-- poller.inFlight for the next tick to retry".
select id
from trigger_records
where trigger_id = $1
  and item_identifier = $2;
-- ─── OIDC / keyless deploy auth (ADR-101, issue #270) ───────────────────
--
-- Per-(account_id, issuer_url) trust policy CRUD + exchanged-token
-- CRUD. PR-A scope. The composite PK on oidc_trust_policies is the
-- lookup key; the per-account dashboard list (PR-C) walks the
-- same index. token_hash UNIQUE on oidc_exchanged_tokens is the
-- bearer hot-path lookup.

-- name: GetOIDCTrustPolicy :one
-- Account-by-issuer lookup. Used by the OIDC exchange handler's
-- first-use auto-create path (PR-A) and the dashboard's Refine
-- form (PR-C).
select account_id, issuer_url, jwks_url, audience,
       coalesce(subject_pattern, '') as subject_pattern,
       algorithms, required_claims, created_at, updated_at,
       audit_login
from oidc_trust_policies
where account_id = $1 and issuer_url = $2;

-- name: UpsertOIDCTrustPolicy :one
-- PK conflict on (account_id, issuer_url) updates the mutable
-- columns (audience, subject_pattern, algorithms, required_claims,
-- updated_at, audit_login) and preserves created_at. Returns the
-- full row as stored.
insert into oidc_trust_policies
    (account_id, issuer_url, jwks_url, audience, subject_pattern,
     algorithms, required_claims, audit_login)
values ($1, $2, $3, $4, $5, $6, $7, $8)
on conflict (account_id, issuer_url) do update
    set audience = excluded.audience,
        subject_pattern = excluded.subject_pattern,
        algorithms = excluded.algorithms,
        required_claims = excluded.required_claims,
        updated_at = now(),
        audit_login = excluded.audit_login
returning account_id, issuer_url, jwks_url, audience,
          coalesce(subject_pattern, '') as subject_pattern,
          algorithms, required_claims, created_at, updated_at,
          audit_login;

-- name: ListOIDCTrustPoliciesForAccount :many
-- Per-account dashboard list (PR-C). Empty slice on miss.
select account_id, issuer_url, jwks_url, audience,
       coalesce(subject_pattern, '') as subject_pattern,
       algorithms, required_claims, created_at, updated_at,
       audit_login
from oidc_trust_policies
where account_id = $1
order by created_at desc;

-- name: AccountByOIDCIssuerSubject :one
-- Resolves an OIDC (issuer, subject) pair to the platform
-- account it's bound to: a trust policy row with matching
-- issuer_url whose subject_pattern matches the subject claim.
-- An empty subject_pattern binds nothing. The legacy first-use
-- policies were written that way (any subject, any audience);
-- treating them as "accept any subject" routed every foreign
-- GitHub Actions subject to such an account.
select a.id, a.email, a.plan, a.status,
       coalesce(a.provider_customer_id, ''), a.created_at
from accounts a
join oidc_trust_policies p on p.account_id = a.id
where p.issuer_url = $1
  and coalesce(p.subject_pattern, '') <> ''
  and $2 ~ p.subject_pattern
order by length(p.subject_pattern) desc,
         a.id
limit 1;

-- name: AccountIDByGitHubOIDCRepositoryIdentity :one
-- First-use GitHub Actions bootstrap through an OAuth-verified install
-- binding. Immutable subjects must match both persisted numeric IDs as well
-- as the current repository name; a zero ID preserves legacy name-only
-- subject behavior.
select min(a.github_install_account_id::text)::uuid as account_id
from apps a
join github_installations gi
  on gi.account_id = a.github_install_account_id
 and gi.installation_id = a.github_install_id
where lower(a.github_repo_full_name) = lower(sqlc.arg(repo_full_name)::text)
  and a.github_install_account_id = a.account_id
  and a.deleted_at is null
  and (sqlc.arg(owner_id)::bigint = 0 or a.github_owner_id = sqlc.arg(owner_id)::bigint)
  and (sqlc.arg(repo_id)::bigint = 0 or a.github_repo_id = sqlc.arg(repo_id)::bigint)
having count(distinct a.github_install_account_id) = 1;

-- name: InsertOIDCExchangedToken :one
-- Fresh-token insert. The id is server-minted by sqlc (gen_random_uuid).
-- Returns the full row (with created_at server-stamped).
insert into oidc_exchanged_tokens
    (account_id, token_hash, expires_at, issuer_url, subject,
     audience, jti, scopes)
values ($1, $2, $3, $4, $5, $6, $7, $8)
returning id, account_id, token_hash, expires_at, issuer_url,
          subject, audience, coalesce(jti, '') as jti, scopes,
          created_at;

-- name: GetOIDCExchangedTokenByHash :one
-- Bearer hot-path lookup. Filters past-TTL rows out at the SQL
-- layer so the pg contract is "WHERE expires_at > NOW()". The
-- MemStore mirror in pkg/state/memstore.go lazy-deletes instead.
select id, account_id, token_hash, expires_at, issuer_url, subject,
       audience, coalesce(jti, '') as jti, scopes, created_at
from oidc_exchanged_tokens
where token_hash = $1
  and expires_at > now();

-- name: DeleteOIDCExchangedToken :exec
-- Operator-driven revoke path (PR-C). Returns 0 rows on miss;
-- the caller maps that to ErrNotFound. The 5-min TTL is the
-- natural expiry path; Delete is the "kill this CI job's
-- credential now" lever.
delete from oidc_exchanged_tokens where id = $1;

-- ---------------------------------------------------------------------------
-- ADR-127 / issue #477 — production debugger per-request telemetry
--
-- The data plane that backs the example insight ("POST /checkout became
-- 38% slower after deployment v81; PostgreSQL queries 82ms → 191ms;
-- 31% of requests affected"). Every gateway-served request lands one row
-- here, keyed on (account_id, app_id, received_at DESC) for the canonical
-- read pattern. trace_id links the row to the in-process TraceRing and
-- to customer-emitted OTel spans.
--
-- Schema is in migrations/00427_request_telemetry.sql (partitioned by
-- RANGE(received_at) so the per-plan retention sweep drops whole monthly
-- partitions rather than per-row DELETEs). Indexes are pinned in the
-- migration; sqlc only generates the typed query surface here.
--
-- Wire path: gatewayd-internal recorder (pkg/gateway/request_telemetry.go)
-- enqueues in-process → publisher batches over unix-socket gRPC to apid's
-- IncrementRequestTelemetry streaming RPC (cmd/apid/grpc_server_request_telemetry.go)
-- → sqlc-generated InsertRequestTelemetry below. The recorder never opens
-- a Postgres connection (CLAUDE.md ownership: apid is the sole writer).
-- ---------------------------------------------------------------------------

-- name: InsertRequestTelemetry :exec
-- One row per gateway-served request. No ON CONFLICT — every request
-- gets its own row (request_id is the natural dedupe, but we don't have
-- it here; TraceRing dedupe is at minute granularity in the recorder).
-- The migration's PK is (id, received_at) because of PARTITION BY RANGE;
-- the id alone is generated by gen_random_uuid() default.
--
-- PR-B (ADR-127 §PR-B): the publisher collapse in
-- pkg/gateway/request_telemetry_publisher.go coalesces requests with
-- the same (app, deployment, route, method, status, dimensions,
-- minute_bucket, latency_bucket) into one row with `count` = the number
-- of originals. count is INT NOT NULL DEFAULT 1 (00440) so pre-PR-B
-- clients keep working — the DEFAULT fires for any INSERT that omits
-- the column. PR-B's publisher always passes it explicitly.
INSERT INTO request_telemetry (
    account_id, app_id, deployment_id, route, method,
    status, latency_ms, cold_boot, trace_id, received_at, count,
    ua_family, referrer_host, country, wake_id, instance_id,
    guest_duration_ms, guest_runtime, guest_outcome, guest_error_class, consumer_id,
    node_id, region, commit_sha, deployment_tag, deployment_created_at, image_digest,
    platform_tenant_id,
    guest_cpu_time_ms, guest_peak_rss_mb, guest_resource_usage_available, flag_evidence
) VALUES (
    $1, $2, $3, $4, $5,
    $6, $7, $8, $9, $10, $11,
    $12, $13, $14, $15, $16, $17,
    COALESCE(NULLIF(sqlc.arg('guest_runtime')::text, ''), '__unknown__'),
    COALESCE(NULLIF(sqlc.arg('guest_outcome')::text, ''), 'missing'),
    COALESCE(sqlc.arg('guest_error_class')::text, ''),
    sqlc.arg('consumer_id')::uuid,
    sqlc.arg('node_id')::text,
    sqlc.arg('region')::text,
    sqlc.arg('commit_sha')::text,
    sqlc.arg('deployment_tag')::text,
    sqlc.arg('deployment_created_at')::text,
    sqlc.arg('image_digest')::text,
    sqlc.arg('platform_tenant_id')::uuid,
    sqlc.arg('guest_cpu_time_ms')::int,
    sqlc.arg('guest_peak_rss_mb')::int,
    sqlc.arg('guest_resource_usage_available')::bool,
    COALESCE(NULLIF(sqlc.arg('flag_evidence_json')::text, '')::jsonb, '[]'::jsonb)
);

-- name: ListRequestTelemetryByPlatformTenant :many
-- Cross-app support view for a platform customer. Always constrain by both
-- owning account and the immutable request-time tenant snapshot; do not infer
-- attribution by joining today's consumer/surface links.
SELECT id, app_id, deployment_id, route, method, status, latency_ms, count,
       cold_boot, trace_id, received_at, wake_id, instance_id,
       guest_duration_ms, guest_runtime, guest_outcome, guest_error_class,
       consumer_id, node_id, region, commit_sha, deployment_tag,
       deployment_created_at, image_digest
FROM request_telemetry
WHERE account_id = sqlc.arg('account_id')
  AND platform_tenant_id = sqlc.arg('platform_tenant_id')
  AND received_at >= sqlc.arg('received_from')
  AND received_at < sqlc.arg('received_until')
  AND (sqlc.arg('cursor_received_at')::timestamptz IS NULL
       OR (received_at, id) < (sqlc.arg('cursor_received_at')::timestamptz,
                               sqlc.arg('cursor_id')::uuid))
  AND (sqlc.arg('app_id_filter')::text = ''
       OR app_id = NULLIF(sqlc.arg('app_id_filter')::text, '')::uuid)
  AND (sqlc.arg('status_filter')::int = 0
       OR status = sqlc.arg('status_filter')::int)
ORDER BY received_at DESC, id DESC
LIMIT sqlc.arg('limit')::int;

-- name: ListRequestTelemetryByApp :many
-- Canonical read pattern: "give me the last N requests for this app".
-- Backs GET /v1/apps/{slug}/debug/requests. Uses
-- request_telemetry_app_received_idx. The (since, until) pair is
-- timestamptz; handler-side date parsing is at cmd/apid/
-- handlers_debug_telemetry.go (parseDebugSinceFromString). Cursor pages use
-- the strict (received_at, id) tuple so equal timestamps cannot reorder rows.
SELECT id, deployment_id, route, method, status, latency_ms, count,
       cold_boot, trace_id, received_at, wake_id, instance_id,
       guest_duration_ms, guest_runtime, guest_outcome, guest_error_class,
       consumer_id, node_id, region, commit_sha, deployment_tag,
       deployment_created_at, image_digest
FROM request_telemetry
WHERE app_id = $1
  AND received_at >= $2
  AND received_at <  $3
  AND (sqlc.arg('cursor_received_at')::timestamptz IS NULL
       OR (received_at, id) < (sqlc.arg('cursor_received_at')::timestamptz,
                               sqlc.arg('cursor_id')::uuid))
  AND (sqlc.arg('route')::text = '' OR route = sqlc.arg('route')::text)
  -- Filters are explicit query parameters so sqlc keeps the generated
  -- params stable. The cursor stores the same values and the handler
  -- rejects a page walk when they change.
  AND (sqlc.arg('deployment_id')::text = ''
       OR deployment_id = NULLIF(sqlc.arg('deployment_id')::text, '')::uuid)
  AND (sqlc.arg('status_filter')::int = 0
       OR status = sqlc.arg('status_filter')::int)
  AND (sqlc.arg('cold_boot_filter')::int = -1
       OR cold_boot = (sqlc.arg('cold_boot_filter')::int = 1))
  AND (
       (sqlc.arg('consumer_anonymous')::boolean AND consumer_id IS NULL)
       OR (
           NOT sqlc.arg('consumer_anonymous')::boolean
           AND (
               sqlc.arg('consumer_id')::text = ''
               OR consumer_id = NULLIF(sqlc.arg('consumer_id')::text, '')::uuid
           )
       )
  )
  AND (sqlc.arg('min_latency_ms')::int = 0
       OR latency_ms >= sqlc.arg('min_latency_ms')::int)
ORDER BY received_at DESC, id DESC
LIMIT sqlc.arg('limit')::int;

-- name: ListRequestTelemetryDependencySpans :many
-- Bounded read path for route-scoped dependency analytics. The
-- account_id predicate is defense in depth for callers that accidentally
-- pass an app id from another tenant; the app lookup remains the primary
-- IDOR boundary. Evidence is newest-first within each route/deployment
-- partition, then interleaved so one high-volume revision cannot crowd all
-- prior deployments out of the bounded comparison window.
WITH ranked AS (
    SELECT id, route, method, count, status, trace_id, received_at, spans_summary,
           deployment_id, commit_sha, deployment_tag, deployment_created_at,
           ROW_NUMBER() OVER (
               PARTITION BY route, method, deployment_id
               ORDER BY received_at DESC, id DESC
           ) AS evidence_rank
    FROM request_telemetry
    WHERE app_id = $1
      AND account_id = $2
      AND received_at >= $3
      AND received_at <  $4
      AND spans_summary IS NOT NULL
)
SELECT id, route, method, count, status, trace_id, received_at, spans_summary,
       deployment_id::text, commit_sha, deployment_tag, deployment_created_at
FROM ranked
ORDER BY evidence_rank ASC, received_at DESC, id DESC
LIMIT $5;

-- name: RequestTelemetryRouteCustomers :many
-- One immutable deployment only. Identity joins validate ownership, never
-- infer a tenant from today's consumer link. Counts precede all output caps.
WITH filtered AS MATERIALIZED (
    SELECT rt.route, rt.method, rt.count::bigint AS requests, rt.received_at,
           c.id AS consumer_id, t.id AS platform_tenant_id,
           (rt.consumer_id IS NULL AND rt.platform_tenant_id IS NULL) AS anonymous
    FROM request_telemetry rt
    LEFT JOIN api_consumers c
      ON c.id = rt.consumer_id AND c.account_id = rt.account_id AND c.app_id = rt.app_id
    LEFT JOIN platform_tenants t
      ON t.id = rt.platform_tenant_id AND t.account_id = rt.account_id
    WHERE rt.app_id = sqlc.arg(app_id)
      AND rt.account_id = sqlc.arg(account_id)
      AND rt.deployment_id = sqlc.arg(deployment_id)
      AND rt.received_at >= sqlc.arg(since_at)
      AND rt.received_at < sqlc.arg(until_at)
), route_totals AS (
    SELECT route, method, SUM(requests)::bigint AS requests,
           COALESCE(SUM(requests) FILTER (WHERE consumer_id IS NOT NULL OR platform_tenant_id IS NOT NULL), 0)::bigint AS identified_requests,
           COALESCE(SUM(requests) FILTER (WHERE anonymous), 0)::bigint AS anonymous_requests,
           COALESCE(SUM(requests) FILTER (WHERE NOT anonymous AND consumer_id IS NULL AND platform_tenant_id IS NULL), 0)::bigint AS unresolved_identity_requests,
           COUNT(DISTINCT consumer_id)::bigint AS consumer_count,
           COUNT(DISTINCT platform_tenant_id)::bigint AS platform_tenant_count,
           MAX(received_at) AS last_observed_at
    FROM filtered GROUP BY route, method
), ranked_routes AS (
    SELECT route_totals.*, COUNT(*) OVER ()::bigint AS matched_routes,
           ROW_NUMBER() OVER (ORDER BY requests DESC, route ASC, method ASC) AS route_rank
    FROM route_totals
), top_routes AS (
    SELECT * FROM ranked_routes WHERE route_rank <= sqlc.arg(route_limit)::int
), customer_totals AS (
    SELECT f.route, f.method, f.consumer_id, f.platform_tenant_id,
           SUM(f.requests)::bigint AS requests, MAX(f.received_at) AS last_observed_at
    FROM filtered f JOIN top_routes USING (route, method)
    WHERE f.consumer_id IS NOT NULL OR f.platform_tenant_id IS NOT NULL
    GROUP BY f.route, f.method, f.consumer_id, f.platform_tenant_id
), ranked_customers AS (
    SELECT customer_totals.*,
           ROW_NUMBER() OVER (PARTITION BY route, method ORDER BY requests DESC, consumer_id ASC NULLS LAST, platform_tenant_id ASC NULLS LAST) AS customer_rank
    FROM customer_totals
), customer_bounds AS (
    SELECT route, method, COUNT(*)::bigint AS customer_groups,
           COALESCE(SUM(requests) FILTER (WHERE customer_rank > sqlc.arg(customer_limit)::int), 0)::bigint AS other_customer_requests
    FROM ranked_customers GROUP BY route, method
)
SELECT tr.route, tr.method, tr.requests, tr.identified_requests,
       tr.anonymous_requests, tr.unresolved_identity_requests,
       tr.consumer_count, tr.platform_tenant_count, tr.last_observed_at::timestamptz AS last_observed_at,
       tr.matched_routes,
       COALESCE(cb.customer_groups, 0)::bigint AS customer_groups,
       COALESCE(cb.other_customer_requests, 0)::bigint AS other_customer_requests,
       COALESCE(rc.consumer_id::text, '')::text AS consumer_id,
       COALESCE(rc.platform_tenant_id::text, '')::text AS platform_tenant_id,
       COALESCE(rc.requests, 0)::bigint AS customer_requests,
       rc.last_observed_at::timestamptz AS customer_last_observed_at
FROM top_routes tr
LEFT JOIN customer_bounds cb USING (route, method)
LEFT JOIN ranked_customers rc ON rc.route = tr.route AND rc.method = tr.method AND rc.customer_rank <= sqlc.arg(customer_limit)::int
ORDER BY tr.requests DESC, tr.route ASC, tr.method ASC,
         rc.requests DESC, rc.consumer_id ASC NULLS LAST, rc.platform_tenant_id ASC NULLS LAST;

-- name: RequestTelemetryCoverage :one
-- Signal coverage for the customer debugger. Counts are weighted by the
-- publisher's collapsed-row `count`, while the row totals make the amount
-- of aggregation visible to callers. This query deliberately reports
-- observed coverage only: request_telemetry has no trustworthy denominator
-- for requests dropped before persistence, so the API must not invent a
-- capture percentage.
SELECT
    COUNT(*)::bigint AS telemetry_rows,
    COALESCE(SUM(count::bigint), 0)::bigint AS represented_requests,
    COUNT(*) FILTER (WHERE trace_id IS NOT NULL)::bigint AS trace_linked_rows,
    COALESCE(SUM(count::bigint) FILTER (WHERE trace_id IS NOT NULL), 0)::bigint AS trace_linked_requests,
    COUNT(*) FILTER (WHERE spans_summary IS NOT NULL)::bigint AS span_evidence_rows,
    COALESCE(SUM(count::bigint) FILTER (WHERE spans_summary IS NOT NULL), 0)::bigint AS span_evidence_requests,
    COUNT(*) FILTER (WHERE wake_id IS NOT NULL AND wake_id <> '')::bigint AS wake_evidence_rows,
    COALESCE(SUM(count::bigint) FILTER (WHERE wake_id IS NOT NULL AND wake_id <> ''), 0)::bigint AS wake_evidence_requests,
    COUNT(*) FILTER (WHERE guest_runtime <> '__unknown__' AND guest_outcome <> 'missing')::bigint AS guest_evidence_rows,
    COALESCE(SUM(count::bigint) FILTER (WHERE guest_runtime <> '__unknown__' AND guest_outcome <> 'missing'), 0)::bigint AS guest_evidence_requests,
    COALESCE(SUM(count::bigint) FILTER (WHERE status >= 400), 0)::bigint AS error_requests,
    MIN(received_at) AS oldest_telemetry_at,
    MAX(received_at) AS latest_telemetry_at
FROM request_telemetry
WHERE app_id = $1
  AND account_id = $2
  AND received_at >= $3
  AND received_at <  $4;

-- name: GetRequestTelemetryByAppAndIdentifier :one
-- Direct request drill-down for the customer debugger. Customers normally
-- have the public x-faas-request-id stored as trace_id, while older clients
-- may retain the internal telemetry-row UUID. Accept both without weakening
-- the app_id tenant boundary. Prefer an exact row-id match if a future trace
-- value happens to equal another row's UUID text.
SELECT id, deployment_id, route, method, status, latency_ms, count,
       cold_boot, trace_id, received_at, spans_summary, wake_id, instance_id,
       guest_duration_ms, guest_runtime, guest_outcome, guest_error_class,
       consumer_id, node_id, region, commit_sha, deployment_tag,
       deployment_created_at, image_digest
FROM request_telemetry
WHERE app_id = sqlc.arg(app_id)
  AND (id::text = sqlc.arg(identifier)::text OR trace_id = sqlc.arg(identifier)::text)
  AND received_at >= sqlc.arg(received_from)
  AND received_at <  sqlc.arg(received_until)
ORDER BY (id::text = sqlc.arg(identifier)::text) DESC, received_at DESC
LIMIT 1;

-- name: RequestTelemetryByDeployment :many
-- Per-deployment drilldown. Used by gregale debug compare and the
-- regression detector (PR-B). Includes the publisher's `count`
-- weight so callers can report request totals rather than stored
-- aggregate-row totals. Uses request_telemetry_app_dep_received_idx.
SELECT id, route, method, status, latency_ms, count, cold_boot, trace_id, received_at
FROM request_telemetry
WHERE app_id = $1
  AND deployment_id = $2
  AND received_at >= $3
  AND received_at <  $4
ORDER BY received_at DESC
LIMIT $5;

-- name: RequestTelemetryCircuitBreakerSummary :one
-- Bounded candidate/stable health summary for the deployment circuit breaker.
-- `count` weights collapsed telemetry rows; compute request and 5xx totals,
-- overall p95, and cold-boot-only p95 in SQL so each progression tick transfers
-- only one row.
WITH weighted AS (
    SELECT latency_ms,
           SUM(count::bigint) AS requests,
           SUM(count::bigint) FILTER (WHERE status >= 500 AND status < 600) AS server_errors,
           SUM(count::bigint) FILTER (WHERE cold_boot) AS cold_boot_requests
      FROM request_telemetry
     WHERE app_id = sqlc.arg('app_id')::uuid
       AND deployment_id = sqlc.arg('deployment_id')::uuid
       AND received_at >= sqlc.arg('received_at')::timestamptz
       AND received_at < sqlc.arg('received_at_2')::timestamptz
     GROUP BY latency_ms
), cpu_usage AS (
    -- CPU is sampled per instance/minute. The retained instance row supplies
    -- its deployment identity; old stopped instances remain through the
    -- telemetry window, then state retention removes them. Ignore pure idle
    -- minutes so background CPU with no requests cannot dominate the ratio.
    SELECT COALESCE(SUM(u.cpu_usec) FILTER (WHERE u.requests > 0), 0)::bigint AS cpu_usec,
           COALESCE(SUM(u.requests::bigint) FILTER (WHERE u.requests > 0), 0)::bigint AS cpu_requests
      FROM usage_minutes AS u
      JOIN instances AS ins ON ins.id = u.instance_id
     WHERE u.app_id = sqlc.arg('app_id')::uuid
       AND ins.app_id = sqlc.arg('app_id')::uuid
       AND ins.deployment_id = sqlc.arg('deployment_id')::uuid
       AND u.minute >= date_trunc('minute', sqlc.arg('received_at')::timestamptz)
       AND u.minute < sqlc.arg('received_at_2')::timestamptz
), ranked AS (
      SELECT latency_ms,
             requests,
             server_errors,
             cold_boot_requests,
             SUM(requests) OVER (ORDER BY latency_ms ROWS UNBOUNDED PRECEDING) AS cumulative,
             SUM(requests) OVER () AS total,
             SUM(cold_boot_requests) OVER (ORDER BY latency_ms ROWS UNBOUNDED PRECEDING) AS cold_boot_cumulative,
             SUM(cold_boot_requests) OVER () AS cold_boot_total
        FROM weighted
), summary AS (
    SELECT COALESCE(SUM(requests), 0)::bigint AS requests,
           COALESCE(SUM(server_errors), 0)::bigint AS server_errors,
           COALESCE(
               MIN(latency_ms) FILTER (WHERE cumulative >= CEIL(total * 0.95)::bigint),
               0
           )::double precision AS p95_latency_ms,
           COALESCE(SUM(cold_boot_requests), 0)::bigint AS cold_boot_requests,
           COALESCE(
               MIN(latency_ms) FILTER (
                   WHERE cold_boot_cumulative >= CEIL(cold_boot_total * 0.95)::bigint
                     AND cold_boot_total > 0
               ),
               0
           )::double precision AS cold_boot_p95_latency_ms
      FROM ranked
)
SELECT summary.requests,
       summary.server_errors,
       summary.p95_latency_ms,
       summary.cold_boot_requests,
       summary.cold_boot_p95_latency_ms,
       cpu_usage.cpu_usec,
       cpu_usage.cpu_requests
  FROM summary
 CROSS JOIN cpu_usage;

-- name: RequestTelemetryBaselineP95ByRoute :many
-- Per-route p50/p95/p99 latency + represented request count for the
-- compare endpoint and the regression detector (ADR-127 PR-B
-- cron + PR Debugger UX v1 compare handler). Single index scan
-- over the existing request_telemetry_app_dep_received_idx
-- (PR-A migration 00427) so the four aggregates share one
-- window. The recorder collapses burst traffic into bounded
-- latency-bucket rows with a `count` weight; expand that weight
-- mathematically instead of treating each aggregate row as one
-- request. The rank/floor formulation below is equivalent to
-- percentile_cont over the expanded multiset of bucket
-- representatives, without materializing one row per request.
WITH weighted AS (
    SELECT route,
           latency_ms,
           SUM(count::bigint) AS weight
    FROM request_telemetry
    WHERE app_id = $1
      AND deployment_id = $2
      AND received_at >= $3
      AND received_at <  $4
    GROUP BY route, latency_ms
), ranked AS (
    SELECT route,
           latency_ms,
           SUM(weight) OVER (
               PARTITION BY route
               ORDER BY latency_ms
               ROWS UNBOUNDED PRECEDING
           ) AS cumulative,
           SUM(weight) OVER (PARTITION BY route) AS total
    FROM weighted
), targets AS (
    SELECT route,
           total,
           (total - 1)::numeric * 0.50 AS p50_rank,
           (total - 1)::numeric * 0.95 AS p95_rank,
           (total - 1)::numeric * 0.99 AS p99_rank
    FROM ranked
    GROUP BY route, total
), values_at_rank AS (
    SELECT r.route,
           t.total,
           t.p50_rank,
           t.p95_rank,
           t.p99_rank,
           MIN(r.latency_ms) FILTER (
               WHERE r.cumulative > floor(t.p50_rank)
           ) AS p50_low,
           MIN(r.latency_ms) FILTER (
               WHERE r.cumulative > ceil(t.p50_rank)
           ) AS p50_high,
           MIN(r.latency_ms) FILTER (
               WHERE r.cumulative > floor(t.p95_rank)
           ) AS p95_low,
           MIN(r.latency_ms) FILTER (
               WHERE r.cumulative > ceil(t.p95_rank)
           ) AS p95_high,
           MIN(r.latency_ms) FILTER (
               WHERE r.cumulative > floor(t.p99_rank)
           ) AS p99_low,
           MIN(r.latency_ms) FILTER (
               WHERE r.cumulative > ceil(t.p99_rank)
           ) AS p99_high
    FROM ranked AS r
    JOIN targets AS t USING (route)
    GROUP BY r.route, t.total, t.p50_rank, t.p95_rank, t.p99_rank
)
SELECT route,
       (p50_low + (p50_high - p50_low) *
           (p50_rank - floor(p50_rank)))::int AS p50_ms,
       (p95_low + (p95_high - p95_low) *
           (p95_rank - floor(p95_rank)))::int AS p95_ms,
       (p99_low + (p99_high - p99_low) *
           (p99_rank - floor(p99_rank)))::int AS p99_ms,
       total::bigint AS n
FROM values_at_rank;

-- name: RequestTelemetryAnalyticsSummary :one
-- Customer-facing request analytics over a bounded retention window.
-- The recorder collapses identical requests into bounded latency-bucket
-- rows with `count`, so all request/error/cold-boot totals and percentiles
-- must expand that weight rather than treating each stored row as one
-- request. Latency representatives are conservative within the bucket
-- width documented by requestTelemetryLatencyBucketUpperBound.
WITH filtered AS (
    SELECT latency_ms, cold_boot, status, count::bigint AS request_count
    FROM request_telemetry
    WHERE app_id = $1
      AND account_id = $2
      AND received_at >= $3
      AND received_at <  $4
), latency_values AS (
    SELECT latency_ms,
           SUM(request_count)::bigint AS sample_count
    FROM filtered
    GROUP BY latency_ms
), ranked AS (
    SELECT latency_ms,
           sample_count,
           SUM(sample_count) OVER (ORDER BY latency_ms ROWS UNBOUNDED PRECEDING) AS cumulative,
           SUM(sample_count) OVER () AS total
    FROM latency_values
), totals AS (
    SELECT COALESCE(SUM(request_count), 0)::bigint AS requests,
           COALESCE(SUM(request_count) FILTER (WHERE status >= 400), 0)::bigint AS error_requests,
           COALESCE(SUM(request_count) FILTER (WHERE cold_boot), 0)::bigint AS cold_boots
    FROM filtered
)
SELECT requests,
       error_requests,
       cold_boots,
       COALESCE((SELECT MIN(latency_ms) FROM ranked WHERE cumulative >= total * 0.50), 0)::int AS p50_ms,
       COALESCE((SELECT MIN(latency_ms) FROM ranked WHERE cumulative >= total * 0.95), 0)::int AS p95_ms,
       COALESCE((SELECT MIN(latency_ms) FROM ranked WHERE cumulative >= total * 0.99), 0)::int AS p99_ms
FROM totals;

-- name: RequestTelemetryAnalyticsByRoute :many
-- Top route/method rows for the customer analytics overview. `count` is
-- weighted throughout the same way as RequestTelemetryAnalyticsSummary.
WITH filtered AS (
    SELECT route, method, latency_ms, cold_boot, status,
           count::bigint AS request_count
    FROM request_telemetry
    WHERE app_id = $1
      AND account_id = $2
      AND received_at >= $3
      AND received_at <  $4
), route_totals AS (
    SELECT route,
           method,
           COALESCE(SUM(request_count), 0)::bigint AS requests,
           COALESCE(SUM(request_count) FILTER (WHERE status >= 400), 0)::bigint AS error_requests,
           COALESCE(SUM(request_count) FILTER (WHERE cold_boot), 0)::bigint AS cold_boots
    FROM filtered
    GROUP BY route, method
), latency_values AS (
    SELECT route,
           method,
           latency_ms,
           SUM(request_count)::bigint AS sample_count
    FROM filtered
    GROUP BY route, method, latency_ms
), ranked AS (
    SELECT route,
           method,
           latency_ms,
           sample_count,
           SUM(sample_count) OVER (PARTITION BY route, method ORDER BY latency_ms ROWS UNBOUNDED PRECEDING) AS cumulative,
           SUM(sample_count) OVER (PARTITION BY route, method) AS total
    FROM latency_values
), percentiles AS (
    SELECT route,
           method,
           COALESCE(MIN(latency_ms) FILTER (WHERE cumulative >= total * 0.50), 0)::int AS p50_ms,
           COALESCE(MIN(latency_ms) FILTER (WHERE cumulative >= total * 0.95), 0)::int AS p95_ms,
           COALESCE(MIN(latency_ms) FILTER (WHERE cumulative >= total * 0.99), 0)::int AS p99_ms
    FROM ranked
    GROUP BY route, method
)
SELECT totals.route,
       totals.method,
       totals.requests,
       totals.error_requests,
       totals.cold_boots,
       percentiles.p50_ms,
       percentiles.p95_ms,
       percentiles.p99_ms
FROM route_totals AS totals
JOIN percentiles USING (route, method)
ORDER BY totals.requests DESC, totals.method ASC, totals.route ASC
LIMIT $5;

-- name: RequestTelemetryAnalyticsByDeployment :many
-- Bounded deployment cost allocation for the customer request analytics
-- window. Request counts are weighted by the publisher's collapsed `count`.
-- The window total is computed before LIMIT so the handler can allocate the
-- omitted deployments into a visible __other__ bucket without an unbounded
-- response.
WITH per_deployment AS (
    SELECT deployment_id::text AS deployment_id,
           COALESCE(MAX(NULLIF(commit_sha, '')), '') AS commit_sha,
           COALESCE(MAX(NULLIF(deployment_tag, '')), '') AS deployment_tag,
           COALESCE(MAX(NULLIF(deployment_created_at, '')), '') AS deployment_created_at,
           SUM(count)::bigint AS requests,
           COALESCE(SUM(count) FILTER (WHERE guest_resource_usage_available), 0)::bigint AS guest_cpu_measured_requests,
           COALESCE(
               ROUND(
                   SUM(guest_cpu_time_ms::numeric * count)
                       FILTER (WHERE guest_resource_usage_available)
                   / NULLIF(SUM(count) FILTER (WHERE guest_resource_usage_available), 0)
               ),
               0
           )::int AS guest_cpu_avg_ms
    FROM request_telemetry
    WHERE app_id = $1
      AND account_id = $2
      AND received_at >= $3
      AND received_at <  $4
    GROUP BY deployment_id
)
SELECT deployment_id,
       commit_sha,
       deployment_tag,
       deployment_created_at,
       requests,
       guest_cpu_measured_requests,
       guest_cpu_avg_ms,
       SUM(requests) OVER ()::bigint AS total_requests
FROM per_deployment
ORDER BY requests DESC, deployment_id ASC
LIMIT $5;

-- name: RequestTelemetryAnalyticsByRouteDeployment :many
-- Per-route deployment split for the customer analytics window. Routes are
-- bounded to the same top-N surface as route analytics, and each route keeps
-- only its top deployments by request count; the remaining revisions are
-- folded into __other__ so the response cardinality is bounded by
-- route_limit * (deployment_limit + 1).
WITH filtered AS (
    SELECT route,
           method,
           deployment_id::text AS deployment_id,
           commit_sha,
           deployment_tag,
           deployment_created_at,
           count::bigint AS request_count,
           guest_resource_usage_available,
           guest_cpu_time_ms
    FROM request_telemetry
    WHERE app_id = $1
      AND account_id = $2
      AND received_at >= $3
      AND received_at <  $4
), per_route_deployment AS (
    SELECT route,
           method,
           deployment_id,
           COALESCE(MAX(NULLIF(commit_sha, '')), '') AS commit_sha,
           COALESCE(MAX(NULLIF(deployment_tag, '')), '') AS deployment_tag,
           COALESCE(MAX(NULLIF(deployment_created_at, '')), '') AS deployment_created_at,
           SUM(request_count)::bigint AS requests,
           COALESCE(SUM(request_count) FILTER (WHERE guest_resource_usage_available), 0)::bigint AS guest_cpu_measured_requests,
           COALESCE(
               ROUND(
                   SUM(guest_cpu_time_ms::numeric * request_count)
                       FILTER (WHERE guest_resource_usage_available)
                   / NULLIF(SUM(request_count) FILTER (WHERE guest_resource_usage_available), 0)
               ),
               0
           )::int AS guest_cpu_avg_ms
    FROM filtered
    GROUP BY route, method, deployment_id
), top_routes AS (
    SELECT route, method
    FROM per_route_deployment
    GROUP BY route, method
    ORDER BY SUM(requests) DESC, route ASC, method ASC
    LIMIT sqlc.arg('route_limit')::int
), ranked_deployments AS (
    SELECT per_route_deployment.*,
           ROW_NUMBER() OVER (
               PARTITION BY per_route_deployment.route, per_route_deployment.method
               ORDER BY per_route_deployment.requests DESC, per_route_deployment.deployment_id ASC
           ) AS deployment_rank
    FROM per_route_deployment
    JOIN top_routes USING (route, method)
)
SELECT route,
       method,
       CASE WHEN deployment_rank <= sqlc.arg('deployment_limit')::int THEN deployment_id ELSE '__other__' END AS deployment_id,
       CASE WHEN MAX(deployment_rank) <= sqlc.arg('deployment_limit')::int THEN COALESCE(MAX(commit_sha), '') ELSE '' END AS commit_sha,
       CASE WHEN MAX(deployment_rank) <= sqlc.arg('deployment_limit')::int THEN COALESCE(MAX(deployment_tag), '') ELSE '' END AS deployment_tag,
       CASE WHEN MAX(deployment_rank) <= sqlc.arg('deployment_limit')::int THEN COALESCE(MAX(deployment_created_at), '') ELSE '' END AS deployment_created_at,
       SUM(requests)::bigint AS requests,
       CASE WHEN MAX(deployment_rank) > sqlc.arg('deployment_limit')::int THEN 0
            ELSE COALESCE(MAX(guest_cpu_measured_requests), 0)::bigint END AS guest_cpu_measured_requests,
       CASE WHEN MAX(deployment_rank) > sqlc.arg('deployment_limit')::int THEN 0
            ELSE COALESCE(MAX(guest_cpu_avg_ms), 0)::int END AS guest_cpu_avg_ms
FROM ranked_deployments
GROUP BY route,
         method,
         CASE WHEN deployment_rank <= sqlc.arg('deployment_limit')::int THEN deployment_id ELSE '__other__' END
ORDER BY route ASC,
         method ASC,
         CASE WHEN CASE WHEN deployment_rank <= sqlc.arg('deployment_limit')::int THEN deployment_id ELSE '__other__' END = '__other__' THEN 1 ELSE 0 END,
         MAX(deployment_created_at) DESC,
         CASE WHEN deployment_rank <= sqlc.arg('deployment_limit')::int THEN deployment_id ELSE '__other__' END ASC;

-- name: RequestTelemetryAnalyticsByDimension :many
-- Top-N customer analytics grouped by one of the bounded dimensions. Rows
-- outside the top-N are folded into __other__ so a customer cannot turn this
-- endpoint into an unbounded cardinality surface. Counts and percentiles use
-- the publisher's collapsed row weight.
WITH filtered AS (
    SELECT
        CASE sqlc.arg('group_by')::text
            WHEN 'country' THEN country
            WHEN 'referrer_host' THEN referrer_host
            WHEN 'ua_family' THEN ua_family
            WHEN 'status' THEN status::text
            WHEN 'consumer_id' THEN COALESCE(consumer_id::text, '__anonymous__')
            ELSE route
        END::text AS dimension,
        CASE WHEN sqlc.arg('group_by')::text = 'route' THEN method ELSE '' END AS method,
        latency_ms,
        cold_boot,
        status,
        guest_duration_ms,
        guest_runtime,
        guest_cpu_time_ms,
        guest_peak_rss_mb,
        guest_resource_usage_available,
        wake_id,
        count::bigint AS request_count
    FROM request_telemetry
    WHERE app_id = $1
      AND account_id = $2
      AND received_at >= $3
      AND received_at <  $4
), top_groups AS (
    SELECT dimension, method
    FROM filtered
    GROUP BY dimension, method
    ORDER BY SUM(request_count) DESC, dimension ASC, method ASC
    LIMIT sqlc.arg('limit')::int
), assigned AS (
    SELECT
        CASE WHEN top_groups.dimension IS NULL THEN '__other__' ELSE filtered.dimension END AS dimension,
        CASE
            WHEN top_groups.dimension IS NULL OR sqlc.arg('group_by')::text <> 'route' THEN ''
            ELSE filtered.method
        END AS method,
        filtered.latency_ms,
        filtered.cold_boot,
        filtered.status,
        filtered.guest_duration_ms,
        filtered.guest_runtime,
        filtered.guest_cpu_time_ms,
        filtered.guest_peak_rss_mb,
        filtered.guest_resource_usage_available,
        filtered.wake_id,
        filtered.request_count
    FROM filtered
    LEFT JOIN top_groups
      ON top_groups.dimension = filtered.dimension
     AND top_groups.method = filtered.method
), latency_values AS (
    SELECT dimension, method, latency_ms,
           SUM(request_count)::bigint AS sample_count
    FROM assigned
    GROUP BY dimension, method, latency_ms
), ranked AS (
    SELECT dimension, method, latency_ms, sample_count,
           SUM(sample_count) OVER (PARTITION BY dimension, method ORDER BY latency_ms ROWS UNBOUNDED PRECEDING) AS cumulative,
           SUM(sample_count) OVER (PARTITION BY dimension, method) AS total
    FROM latency_values
), cold_latency_values AS (
    SELECT dimension,
           method,
           latency_ms,
           SUM(request_count)::bigint AS sample_count
    FROM assigned
    WHERE cold_boot
    GROUP BY dimension, method, latency_ms
), cold_wakes AS (
    SELECT DISTINCT dimension, method, wake_id
    FROM assigned
    WHERE cold_boot
      AND wake_id IS NOT NULL
      AND wake_id <> ''
      AND dimension <> '__other__'
      AND sqlc.arg('group_by')::text = 'route'
), wake_events AS (
    SELECT cold_wakes.dimension,
           cold_wakes.method,
           MIN(events.at) FILTER (WHERE events.kind = 'wake.boot_started' AND events.actor = 'schedd') AS started_at,
           MIN(events.at) FILTER (WHERE events.kind = 'wake.boot_completed' AND events.actor = 'schedd') AS completed_at
    FROM cold_wakes
    JOIN events ON events.data->>'wake_id' = cold_wakes.wake_id
    WHERE events.kind IN ('wake.boot_started', 'wake.boot_completed')
    GROUP BY cold_wakes.dimension, cold_wakes.method, cold_wakes.wake_id
), wake_durations AS (
    SELECT dimension,
           method,
           (EXTRACT(EPOCH FROM (completed_at - started_at)) * 1000)::int AS duration_ms
    FROM wake_events
    WHERE started_at IS NOT NULL
      AND completed_at IS NOT NULL
      AND completed_at > started_at
), wake_values AS (
    SELECT dimension,
           method,
           duration_ms,
           COUNT(*)::bigint AS sample_count
    FROM wake_durations
    GROUP BY dimension, method, duration_ms
), wake_ranked AS (
    SELECT dimension,
           method,
           duration_ms,
           SUM(sample_count) OVER (PARTITION BY dimension, method ORDER BY duration_ms ROWS UNBOUNDED PRECEDING) AS cumulative,
           SUM(sample_count) OVER (PARTITION BY dimension, method) AS total
    FROM wake_values
), wake_percentiles AS (
    SELECT dimension,
           method,
           (MIN(duration_ms) FILTER (WHERE cumulative >= total * 0.95))::int AS wake_boot_p95_ms
    FROM wake_ranked
    GROUP BY dimension, method
), cold_ranked AS (
    SELECT dimension,
           method,
           latency_ms,
           SUM(sample_count) OVER (PARTITION BY dimension, method ORDER BY latency_ms ROWS UNBOUNDED PRECEDING) AS cumulative,
           SUM(sample_count) OVER (PARTITION BY dimension, method) AS total
    FROM cold_latency_values
), cold_percentiles AS (
    SELECT dimension,
           method,
           (MIN(latency_ms) FILTER (WHERE cumulative >= total * 0.95))::int AS cold_request_p95_ms
    FROM cold_ranked
    GROUP BY dimension, method
), guest_latency_values AS (
    SELECT dimension,
           method,
           guest_duration_ms,
           SUM(request_count)::bigint AS sample_count
    FROM assigned
    WHERE guest_runtime <> '__unknown__'
    GROUP BY dimension, method, guest_duration_ms
), guest_ranked AS (
    SELECT dimension,
           method,
           guest_duration_ms,
           SUM(sample_count) OVER (PARTITION BY dimension, method ORDER BY guest_duration_ms ROWS UNBOUNDED PRECEDING) AS cumulative,
           SUM(sample_count) OVER (PARTITION BY dimension, method) AS total
    FROM guest_latency_values
), guest_percentiles AS (
    SELECT dimension,
           method,
           (MIN(guest_duration_ms) FILTER (WHERE cumulative >= total * 0.50))::int AS guest_execution_p50_ms,
           (MIN(guest_duration_ms) FILTER (WHERE cumulative >= total * 0.95))::int AS guest_execution_p95_ms
    FROM guest_ranked
    GROUP BY dimension, method
), guest_cpu_values AS (
    SELECT dimension,
           method,
           guest_cpu_time_ms,
           SUM(request_count)::bigint AS sample_count
    FROM assigned
    WHERE guest_resource_usage_available
    GROUP BY dimension, method, guest_cpu_time_ms
), guest_cpu_ranked AS (
    SELECT dimension,
           method,
           guest_cpu_time_ms,
           SUM(sample_count) OVER (PARTITION BY dimension, method ORDER BY guest_cpu_time_ms ROWS UNBOUNDED PRECEDING) AS cumulative,
           SUM(sample_count) OVER (PARTITION BY dimension, method) AS total
    FROM guest_cpu_values
), guest_resource_metrics AS (
    SELECT assigned.dimension,
           assigned.method,
           ROUND(SUM(assigned.guest_cpu_time_ms::numeric * assigned.request_count)
                 FILTER (WHERE assigned.guest_resource_usage_available)
                 / NULLIF(SUM(assigned.request_count) FILTER (WHERE assigned.guest_resource_usage_available), 0))::int AS guest_cpu_avg_ms,
           MAX(assigned.guest_peak_rss_mb) FILTER (WHERE assigned.guest_resource_usage_available)::int AS guest_peak_rss_max_mb
    FROM assigned
    GROUP BY assigned.dimension, assigned.method
), guest_cpu_percentiles AS (
    SELECT dimension,
           method,
           (MIN(guest_cpu_time_ms) FILTER (WHERE cumulative >= total * 0.95))::int AS guest_cpu_p95_ms
    FROM guest_cpu_ranked
    GROUP BY dimension, method
), percentiles AS (
    SELECT dimension,
           method,
           COALESCE(MIN(latency_ms) FILTER (WHERE cumulative >= total * 0.50), 0)::int AS p50_ms,
           COALESCE(MIN(latency_ms) FILTER (WHERE cumulative >= total * 0.95), 0)::int AS p95_ms,
           COALESCE(MIN(latency_ms) FILTER (WHERE cumulative >= total * 0.99), 0)::int AS p99_ms
    FROM ranked
    GROUP BY dimension, method
), totals AS (
    SELECT dimension,
           method,
           COALESCE(SUM(request_count), 0)::bigint AS requests,
           COALESCE(SUM(request_count) FILTER (WHERE status >= 400), 0)::bigint AS error_requests,
           COALESCE(SUM(request_count) FILTER (WHERE cold_boot), 0)::bigint AS cold_boots
    FROM assigned
    GROUP BY dimension, method
)
SELECT totals.dimension,
       totals.method,
       totals.requests,
       totals.error_requests,
       totals.cold_boots,
       percentiles.p50_ms,
       percentiles.p95_ms,
       percentiles.p99_ms,
       cold_percentiles.cold_request_p95_ms,
       wake_percentiles.wake_boot_p95_ms,
       guest_percentiles.guest_execution_p50_ms,
       guest_percentiles.guest_execution_p95_ms,
       guest_resource_metrics.guest_cpu_avg_ms,
       guest_cpu_percentiles.guest_cpu_p95_ms,
       guest_resource_metrics.guest_peak_rss_max_mb
FROM totals
JOIN percentiles USING (dimension, method)
LEFT JOIN cold_percentiles USING (dimension, method)
LEFT JOIN wake_percentiles USING (dimension, method)
LEFT JOIN guest_percentiles USING (dimension, method)
LEFT JOIN guest_resource_metrics USING (dimension, method)
LEFT JOIN guest_cpu_percentiles USING (dimension, method)
ORDER BY totals.requests DESC, totals.dimension ASC, totals.method ASC;

-- name: RequestTelemetryAnalyticsTimeseries :many
-- Zero-filled UTC hourly request analytics for customer charts. The
-- recorder collapses rows by count, so all totals and percentile ranks
-- expand that weight rather than counting stored rows.
WITH buckets AS (
    SELECT generate_series(
        date_bin('1 hour', sqlc.arg('received_at')::timestamptz, 'epoch'::timestamptz),
        date_bin('1 hour', sqlc.arg('received_at_2')::timestamptz - interval '1 microsecond', 'epoch'::timestamptz),
        interval '1 hour'
    )::timestamptz AS bucket_start
), filtered AS (
    SELECT date_bin('1 hour', received_at, 'epoch'::timestamptz) AS bucket_start,
           latency_ms,
           cold_boot,
           status,
           count::bigint AS request_count
    FROM request_telemetry
    WHERE app_id = $1
      AND account_id = $2
      AND received_at >= sqlc.arg('received_at')::timestamptz
      AND received_at <  sqlc.arg('received_at_2')::timestamptz
      AND (sqlc.arg('route')::text = '' OR route = sqlc.arg('route')::text)
      AND (sqlc.arg('method')::text = '' OR method = sqlc.arg('method')::text)
), latency_values AS (
    SELECT bucket_start,
           latency_ms,
           SUM(request_count)::bigint AS sample_count
    FROM filtered
    GROUP BY bucket_start, latency_ms
), ranked AS (
    SELECT bucket_start,
           latency_ms,
           sample_count,
           SUM(sample_count) OVER (PARTITION BY bucket_start ORDER BY latency_ms ROWS UNBOUNDED PRECEDING) AS cumulative,
           SUM(sample_count) OVER (PARTITION BY bucket_start) AS total
    FROM latency_values
), percentiles AS (
    SELECT bucket_start,
           COALESCE(MIN(latency_ms) FILTER (WHERE cumulative >= total * 0.50), 0)::int AS p50_ms,
           COALESCE(MIN(latency_ms) FILTER (WHERE cumulative >= total * 0.95), 0)::int AS p95_ms,
           COALESCE(MIN(latency_ms) FILTER (WHERE cumulative >= total * 0.99), 0)::int AS p99_ms
    FROM ranked
    GROUP BY bucket_start
), totals AS (
    SELECT bucket_start,
           COALESCE(SUM(request_count), 0)::bigint AS requests,
           COALESCE(SUM(request_count) FILTER (WHERE status >= 400), 0)::bigint AS error_requests,
           COALESCE(SUM(request_count) FILTER (WHERE cold_boot), 0)::bigint AS cold_boots
    FROM filtered
    GROUP BY bucket_start
)
SELECT b.bucket_start,
       COALESCE(t.requests, 0)::bigint AS requests,
       COALESCE(t.error_requests, 0)::bigint AS error_requests,
       COALESCE(t.cold_boots, 0)::bigint AS cold_boots,
       COALESCE(p.p50_ms, 0)::int AS p50_ms,
       COALESCE(p.p95_ms, 0)::int AS p95_ms,
       COALESCE(p.p99_ms, 0)::int AS p99_ms
FROM buckets AS b
LEFT JOIN totals AS t USING (bucket_start)
LEFT JOIN percentiles AS p USING (bucket_start)
ORDER BY b.bucket_start ASC;

-- name: RequestTelemetryAnalyticsTimeseriesGrouped :many
-- Zero-filled hourly series for a bounded top-N analytics dimension. The
-- group set is selected over the whole window, then every selected group is
-- zero-filled per hour. This keeps charts stable while preserving the same
-- retention and cardinality bounds as the overview query.
WITH buckets AS (
    SELECT generate_series(
        date_bin('1 hour', sqlc.arg('received_at')::timestamptz, 'epoch'::timestamptz),
        date_bin('1 hour', sqlc.arg('received_at_2')::timestamptz - interval '1 microsecond', 'epoch'::timestamptz),
        interval '1 hour'
    )::timestamptz AS bucket_start
), filtered AS (
    SELECT date_bin('1 hour', received_at, 'epoch'::timestamptz) AS bucket_start,
           CASE sqlc.arg('group_by')::text
               WHEN 'country' THEN country
               WHEN 'referrer_host' THEN referrer_host
               WHEN 'ua_family' THEN ua_family
               WHEN 'status' THEN status::text
               WHEN 'consumer_id' THEN COALESCE(consumer_id::text, '__anonymous__')
               ELSE route
           END::text AS dimension,
           CASE WHEN sqlc.arg('group_by')::text = 'route' THEN method ELSE '' END AS method,
           latency_ms,
           cold_boot,
           status,
           count::bigint AS request_count
    FROM request_telemetry
    WHERE app_id = $1
      AND account_id = $2
      AND received_at >= sqlc.arg('received_at')::timestamptz
      AND received_at <  sqlc.arg('received_at_2')::timestamptz
      AND (sqlc.arg('route')::text = '' OR route = sqlc.arg('route')::text)
      AND (sqlc.arg('method')::text = '' OR method = sqlc.arg('method')::text)
), top_groups AS (
    SELECT dimension, method
    FROM filtered
    GROUP BY dimension, method
    ORDER BY SUM(request_count) DESC, dimension ASC, method ASC
    LIMIT sqlc.arg('limit')::int
), group_set AS (
    SELECT dimension, method FROM top_groups
    UNION ALL
    SELECT '__other__', ''
    WHERE EXISTS (
        SELECT 1
        FROM filtered
        WHERE NOT EXISTS (
            SELECT 1
            FROM top_groups
            WHERE top_groups.dimension = filtered.dimension
              AND top_groups.method = filtered.method
        )
    )
), assigned AS (
    SELECT filtered.bucket_start,
           CASE WHEN top_groups.dimension IS NULL THEN '__other__' ELSE filtered.dimension END AS dimension,
           CASE
               WHEN top_groups.dimension IS NULL OR sqlc.arg('group_by')::text <> 'route' THEN ''
               ELSE filtered.method
           END AS method,
           filtered.latency_ms,
           filtered.cold_boot,
           filtered.status,
           filtered.request_count
    FROM filtered
    LEFT JOIN top_groups
      ON top_groups.dimension = filtered.dimension
     AND top_groups.method = filtered.method
), latency_values AS (
    SELECT bucket_start, dimension, method, latency_ms,
           SUM(request_count)::bigint AS sample_count
    FROM assigned
    GROUP BY bucket_start, dimension, method, latency_ms
), ranked AS (
    SELECT bucket_start, dimension, method, latency_ms, sample_count,
           SUM(sample_count) OVER (PARTITION BY bucket_start, dimension, method ORDER BY latency_ms ROWS UNBOUNDED PRECEDING) AS cumulative,
           SUM(sample_count) OVER (PARTITION BY bucket_start, dimension, method) AS total
    FROM latency_values
), percentiles AS (
    SELECT bucket_start,
           dimension,
           method,
           COALESCE(MIN(latency_ms) FILTER (WHERE cumulative >= total * 0.50), 0)::int AS p50_ms,
           COALESCE(MIN(latency_ms) FILTER (WHERE cumulative >= total * 0.95), 0)::int AS p95_ms,
           COALESCE(MIN(latency_ms) FILTER (WHERE cumulative >= total * 0.99), 0)::int AS p99_ms
    FROM ranked
    GROUP BY bucket_start, dimension, method
), totals AS (
    SELECT bucket_start,
           dimension,
           method,
           COALESCE(SUM(request_count), 0)::bigint AS requests,
           COALESCE(SUM(request_count) FILTER (WHERE status >= 400), 0)::bigint AS error_requests,
           COALESCE(SUM(request_count) FILTER (WHERE cold_boot), 0)::bigint AS cold_boots
    FROM assigned
    GROUP BY bucket_start, dimension, method
)
SELECT b.bucket_start,
       g.dimension,
       g.method,
       COALESCE(t.requests, 0)::bigint AS requests,
       COALESCE(t.error_requests, 0)::bigint AS error_requests,
       COALESCE(t.cold_boots, 0)::bigint AS cold_boots,
       COALESCE(p.p50_ms, 0)::int AS p50_ms,
       COALESCE(p.p95_ms, 0)::int AS p95_ms,
       COALESCE(p.p99_ms, 0)::int AS p99_ms
FROM buckets AS b
CROSS JOIN group_set AS g
LEFT JOIN totals AS t
  ON t.bucket_start = b.bucket_start
 AND t.dimension = g.dimension
 AND t.method = g.method
LEFT JOIN percentiles AS p
  ON p.bucket_start = b.bucket_start
 AND p.dimension = g.dimension
 AND p.method = g.method
ORDER BY g.dimension ASC, g.method ASC, b.bucket_start ASC;

-- PR-B (ADR-127 §PR-B) — regression observation persistence + dashboard
-- read patterns. The cron in cmd/apid/debug_regression_cron.go composes
-- RequestTelemetryBaselineP95ByRoute (above) + RequestTelemetryByDeployment
-- in Go to detect per-route regressions (ADR-127 §Decision 5: p95_ms
-- exceeds p95_base_ms * 1.20). The detections persist here.
--
-- A native sqlc RequestTelemetryRegression query was tried during the PR-A
-- recon: the CTE-on-CTE shape trips sqlc v1.31's "ambiguous column
-- reference" parser on (deployment_id) even after explicit ON clauses;
-- the workaround is a LATERAL join that requires a more invasive query
-- refactor. Go-side composition of the two PR-A queries above is the
-- chosen workaround (cheap, no parser gymnastics).

-- name: UpsertRegressionObservation :exec
-- Persist a regression observation. PRIMARY KEY (app_id, deployment_id,
-- route) — the cron upserts on this triple so the table grows at most
-- one row per (deployment, route) across all cron passes, not one row
-- per cron tick. first_detected_at remains stable during one active
-- lifecycle, then resets when a resolved regression is detected again
-- (or a dismissal expires). The webhook trigger uses that timestamp as
-- the detection transition's idempotency key. last_detected_at is
-- refreshed on every pass and backs the dashboard's since filter.
INSERT INTO debug_regression_observations (
    app_id, deployment_id, route,
    p95_ms, p95_base_ms, affected_count,
    regression_factor, state, first_detected_at, last_detected_at
) VALUES (
    $1, $2, $3,
    $4, $5, $6,
    $7, 'active', now(), now()
)
ON CONFLICT (app_id, deployment_id, route) DO UPDATE SET
    p95_ms            = EXCLUDED.p95_ms,
    p95_base_ms       = EXCLUDED.p95_base_ms,
    affected_count    = EXCLUDED.affected_count,
    regression_factor = EXCLUDED.regression_factor,
    first_detected_at = CASE
        WHEN debug_regression_observations.state = 'resolved'
          OR (debug_regression_observations.state = 'dismissed'
              AND (debug_regression_observations.dismissed_until IS NULL
                   OR debug_regression_observations.dismissed_until <= now()))
        THEN EXCLUDED.last_detected_at
        ELSE debug_regression_observations.first_detected_at
    END,
    last_detected_at  = EXCLUDED.last_detected_at,
    state             = CASE
        WHEN debug_regression_observations.state = 'acknowledged' THEN 'acknowledged'
        WHEN debug_regression_observations.state = 'dismissed'
             AND debug_regression_observations.dismissed_until IS NOT NULL
             AND debug_regression_observations.dismissed_until > now() THEN 'dismissed'
        ELSE 'active'
    END,
    acknowledged_at   = CASE
        WHEN debug_regression_observations.state = 'acknowledged' THEN debug_regression_observations.acknowledged_at
        ELSE NULL
    END,
    dismissed_until   = CASE
        WHEN debug_regression_observations.state = 'dismissed'
             AND debug_regression_observations.dismissed_until IS NOT NULL
             AND debug_regression_observations.dismissed_until > now()
            THEN debug_regression_observations.dismissed_until
        ELSE NULL
    END,
    resolved_at       = NULL;

-- name: GetRegressionObservation :one
-- Read the row after a detector upsert so the notification reflects a
-- preserved acknowledgement/dismissal rather than assuming active state.
SELECT app_id, deployment_id, route,
       p95_ms, p95_base_ms, affected_count,
       regression_factor, first_detected_at, last_detected_at,
       state, acknowledged_at, dismissed_until, resolved_at
FROM debug_regression_observations
WHERE app_id = $1
  AND deployment_id = $2
  AND route = $3;

-- name: ListActiveRegressionsByApp :many
-- Dashboard + GET /v1/apps/{slug}/debug/regressions read pattern.
-- `since` is an interval (e.g. '1 hour') clamped handler-side to the
-- plan's DebugTelemetryRetentionDays cap. ORDER BY regression_factor
-- DESC, last_detected_at DESC matches the dashboard render order
-- (worst regression at the top, then most-recently-reconfirmed).
-- Uses debug_regression_observations_app_idx (00436).
SELECT deployment_id, route,
       p95_ms, p95_base_ms, affected_count,
       regression_factor, first_detected_at, last_detected_at,
       CASE
           WHEN state = 'dismissed'
                AND dismissed_until IS NOT NULL
                AND dismissed_until <= now() THEN 'active'
           ELSE state
       END AS state,
       acknowledged_at, dismissed_until, resolved_at
FROM debug_regression_observations
WHERE app_id = $1
  AND last_detected_at > now() - $2::interval
  AND state <> 'resolved'
  AND (state <> 'dismissed' OR dismissed_until IS NULL OR dismissed_until <= now())
ORDER BY regression_factor DESC, last_detected_at DESC;

-- name: ApplyRegressionAction :one
-- Change only the debugger workflow state for one app-scoped observation.
-- The handler maps reopen to active before calling this query. Reopening a
-- non-active observation starts a new detection lifecycle so the transition
-- webhook gets its own stable id.
UPDATE debug_regression_observations
SET state = $4,
    first_detected_at = CASE
        WHEN $4 = 'active' AND state <> 'active' THEN now()
        ELSE first_detected_at
    END,
    last_detected_at = CASE WHEN $4 = 'active' THEN now() ELSE last_detected_at END,
    acknowledged_at = CASE WHEN $4 = 'acknowledged' THEN now() ELSE NULL END,
    dismissed_until = CASE WHEN $4 = 'dismissed' THEN $5::timestamptz ELSE NULL END,
    resolved_at = CASE WHEN $4 = 'resolved' THEN now() ELSE NULL END
WHERE app_id = $1
  AND deployment_id = $2
  AND route = $3
RETURNING app_id, deployment_id, route,
          p95_ms, p95_base_ms, affected_count,
          regression_factor, first_detected_at, last_detected_at,
          state, acknowledged_at, dismissed_until, resolved_at;

-- name: ResolveStaleRegressionObservations :many
-- A detector pass that no longer sees a regression resolves the previous
-- observation. Returning rows lets apid publish one account-scoped event per
-- lifecycle transition without a second read.
UPDATE debug_regression_observations
SET state = 'resolved',
    resolved_at = COALESCE(resolved_at, now())
WHERE last_detected_at <= now() - $1::interval
  AND state <> 'resolved'
RETURNING app_id, deployment_id, route,
          p95_ms, p95_base_ms, affected_count,
          regression_factor, first_detected_at, last_detected_at,
          state, acknowledged_at, dismissed_until, resolved_at;

-- name: ListDeploymentsForCompare :many
-- Backs the dashboard compare panel's two `<select>` dropdowns: "pick
-- source deployment" and "pick mirror deployment". GROUP BY on
-- request_telemetry reads the distinct deployment_ids that have
-- shipped traffic in the window. first_seen/last_seen row_count give
-- the dashboard enough metadata to render "v81 — 17m of traffic,
-- 4123 rows" without a second query.
--
-- PR-B uses this to seed the dropdowns; the actual percentile
-- comparison is two RequestTelemetryBaselineP95ByRoute calls in Go
-- (PR-A ships that query; the regression cron uses the same shape).
SELECT deployment_id,
       MIN(received_at) AS first_seen,
       MAX(received_at) AS last_seen,
       COUNT(*)         AS row_count
FROM request_telemetry
WHERE app_id = $1
  AND received_at > now() - $2::interval
GROUP BY deployment_id
ORDER BY last_seen DESC
LIMIT $3;

-- name: ListAppsWithRecentTelemetry :many
-- Backs the regression cron's discovery loop. Walks all apps with
-- >=1 row in the regression window so the cron doesn't have to query
-- the full `apps` table on every tick. Returns the app_id and slug
-- (the cron needs slug for log lines; the rest of the cron needs
-- app_id to bound RequestTelemetryBaselineP95ByRoute / ByDeployment).
-- Index: request_telemetry_app_received_idx on (app_id, received_at
-- DESC) makes this DISTINCT scan cheap.
SELECT DISTINCT app_id
FROM request_telemetry
WHERE received_at > now() - $1::interval;

-- name: UpdateSpansSummary :exec
-- ADR-127 PR-D: writer for spans_summary jsonb. The gatewayd-public
-- OTLP/HTTP handler coalesces incoming batches for the same trace_id
-- in-process (pkg/gateway/spans_accumulator.go) and flushes the
-- accumulated summary every FAAS_OTEL_FLUSH_INTERVAL (default 30s).
-- UPDATE (not INSERT) because the row already exists — the recorder
-- wrote it from the gateway edge (pkg/gateway/request_telemetry_publisher.go).
-- Multiple trusted producers can contribute to one trace (for example,
-- gatewayd-internal service bindings and outboundd provider calls), so merge
-- by span identity instead of allowing a later writer to erase earlier spans.
-- Keep the slowest 1000 unique spans, the Scale-tier maximum, to bound storage.
-- $N::jsonb cast is load-bearing — without it sqlc binds as text and
-- Postgres raises SQLSTATE 22P02 (invalid_text_representation).
--
-- PR-D code-review #1: the WHERE clause now also pins account_id.
-- Defense in depth against cross-customer overwrite. The
-- gateway-side accumulator already rejects trace_id/account_id
-- mismatches (pkg/gateway/spans_accumulator.go ErrAccountMismatch),
-- and the apid gRPC handler forwards the same account_id, but a
-- bug at any layer could otherwise let account A's flush wipe
-- account B's row. Adding the account_id = $3::uuid predicate
-- makes cross-customer overwrite impossible regardless of which
-- upstream guard fails. The composite (trace_id, account_id)
-- lookup still hits request_telemetry_trace_idx for the trace_id
-- selectivity; the residual account_id check is a post-fetch
-- row-level filter (one row, microseconds).
update request_telemetry as target
   set spans_summary = (
       select coalesce(jsonb_agg(bounded.span order by bounded.duration_nanos desc, bounded.span_id), '[]'::jsonb)
         from (
           select span, duration_nanos, span_id
             from (
               select distinct on (span_id, end_time_unix_nano)
                      span, duration_nanos, span_id
                 from (
                   select item as span,
                          coalesce(item->>'span_id', '') as span_id,
                          coalesce(item->>'end_time_unix_nano', '') as end_time_unix_nano,
                          case when coalesce(item->>'duration_nanos', '') ~ '^[0-9]{1,20}$'
                               then (item->>'duration_nanos')::numeric
                               else 0::numeric end as duration_nanos
                     from jsonb_array_elements(
                       (case when jsonb_typeof(target.spans_summary) = 'array'
                             then target.spans_summary else '[]'::jsonb end)
                       ||
                       (case when jsonb_typeof($2::jsonb) = 'array'
                             then $2::jsonb else '[]'::jsonb end)
                     ) as source(item)
                 ) normalized
                order by span_id, end_time_unix_nano, duration_nanos desc
             ) deduplicated
            order by duration_nanos desc, span_id
            limit 1000
         ) bounded
   )
 where target.trace_id = $1
   and target.account_id = $3::uuid
   and target.received_at >= now() - interval '24 hours';

-- ---------------------------------------------------------------------------
-- Issue #246 acceptance item 7 — hard-bounce + complaint suppression list
-- (ADR-115 §D.3, RFC 8058 follow-on). One row per (source,
-- provider_event_id) so Resend's webhook redelivery dedupes to the
-- same row instead of double-suppressing. The unique index is the
-- dedupe key. Schema lives in migrations/00562_mail_suppressions.sql.
-- ---------------------------------------------------------------------------

-- name: RecordMailSuppression :one
-- INSERT with ON CONFLICT (source, provider_event_id) DO UPDATE so
-- the Resend webhook redelivery is idempotent. RETURNING (xmax = 0)
-- exposes the canonical "fresh insert vs replay" signal — the
-- bounce handler (pkg/meter/bounce_handler.go) reads it to decide
-- whether to advance dunning (fresh) or skip (replay). The SET
-- clause is intentionally a no-op rewrite of email: the row's
-- contents are already correct, but Postgres needs an UPDATE arm
-- to fire RETURNING when the conflict hits.
--
-- $1 = account_id (nullable — the bounce handler may not have
--      correlated the address to an account yet)
-- $2 = email
-- $3 = reason (closed: hard_bounce / complaint / manual)
-- $4 = source (closed: resend / postmark / operator)
-- $5 = provider_event_id
-- $6 = expires_at (nullable — null means suppression is permanent
--      until operator override; non-null is the TTL deadline)
INSERT INTO mail_suppressions (
    account_id, email, reason, source, provider_event_id, expires_at
) VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (source, provider_event_id) DO UPDATE
SET email = EXCLUDED.email
RETURNING (xmax = 0) AS inserted;

-- name: IsMailSuppressed :one
-- Returns true if any active suppression matches the address.
-- "Active" means expires_at IS NULL OR expires_at > now(); the
-- partial index mail_suppressions_active_email_idx keeps expired
-- rows out so a row that fell out of TTL doesn't block future
-- mail to that address. Lower-casing on both sides makes the
-- match case-insensitive (Postfix accepts mixed case; the
-- providers' bounce webhooks do too).
--
-- $1 = email
SELECT EXISTS (
    SELECT 1 FROM mail_suppressions
    WHERE lower(email) = lower($1)
      AND (expires_at IS NULL OR expires_at > now())
) AS suppressed;

-- ----------------------------------------------------------------------
-- NodeLifecycleStore (Workstream B, issue #1184)
--
-- 12 queries wrap the recovery arbiter's DB I/O. The arbiter reads via
-- NodeGet/NodeList/NodeListRecoverable/NodeListDrainable and writes via
-- NodeSetLifecycle (CAS on the prior lifecycle, so two competing writers
-- can't race from 'active'→'draining' vs 'active'→'unavailable'). Drain
-- initiation stamps `drain_initiated_at`; the drain-complete sweep marks
-- `drain_completed_at` once the last live instance migrates or recreates.
--
-- DeploymentRecordSnapshotMiss / DeploymentClearSnapshotBackoff are the
-- per-deployment backoff state added by 00585 — wake flow records a
-- miss and stamps a `Retry-After` until, the recovery arbiter clears
-- it after a successful migrate-or-recreate sweep. Partial index
-- `deployments_snapshot_backoff_idx` makes the wake-side check an
-- index-only scan.
-- ----------------------------------------------------------------------

-- name: NodeGet :one
-- Resolve a single compute_nodes row by its UUID. Used by the recovery
-- arbiter's per-tick decision and by the apid drain handler.
SELECT
    id, name, target_url, vpcpus, mem_mb, max_concurrency,
    admission_ceiling_mb,
    lifecycle::text AS lifecycle, active, last_heartbeat_at, created_at,
    region, zone, schedd_target_url, vcpu_budget, public_ip, public_ip_set_at,
    release_id, manifest_hash, host_certificate, cert_fingerprint, role,
    generation, gateway_target_url,
    drain_initiated_at, drain_completed_at, recovery_initiated_at,
    last_recovery_outcome
FROM compute_nodes
WHERE id = $1;

-- name: NodeGetByName :one
-- Same as NodeGet but by the human-stable name. The apid handler
-- (POST /v1/compute-nodes/{name}/drain) and the recovery arbiter's
-- cold-start reconciliation path both key by name because operator
-- input is name-based.
SELECT
    id, name, target_url, vpcpus, mem_mb, max_concurrency,
    admission_ceiling_mb,
    lifecycle::text AS lifecycle, active, last_heartbeat_at, created_at,
    region, zone, schedd_target_url, vcpu_budget, public_ip, public_ip_set_at,
    release_id, manifest_hash, host_certificate, cert_fingerprint, role,
    generation, gateway_target_url,
    drain_initiated_at, drain_completed_at, recovery_initiated_at,
    last_recovery_outcome
FROM compute_nodes
WHERE name = $1;

-- name: NodeList :many
-- All nodes, optionally filtered by lifecycle. The recovery arbiter
-- passes NULL to enumerate every row on cold-start reconciliation;
-- the placement filter passes lifecycle='active' (via the existing
-- `WHERE active = true` partial-index path — unchanged). $1 is the
-- lifecycle filter; pass the empty string for "any".
SELECT
    id, name, target_url, vpcpus, mem_mb, max_concurrency,
    admission_ceiling_mb,
    lifecycle::text AS lifecycle, active, last_heartbeat_at, created_at,
    region, zone, schedd_target_url, vcpu_budget, public_ip, public_ip_set_at,
    release_id, manifest_hash, host_certificate, cert_fingerprint, role,
    generation, gateway_target_url,
    drain_initiated_at, drain_completed_at, recovery_initiated_at,
    last_recovery_outcome
FROM compute_nodes
WHERE ($1 = '' OR lifecycle::text = $1)
ORDER BY name;

-- name: NodeSetLifecycle :execrows
-- CAS lifecycle transition. Returns 0 if the prior state didn't match
-- $2 (i.e. another writer raced us) — the caller treats that as a
-- soft no-op and re-reads via NodeGet. Returns 1 on success.
--
-- Args:
--   $1 = node id (UUID)
--   $2 = expected prior lifecycle text
--   $3 = new lifecycle text
--   $4 = wall-clock timestamp to stamp on the relevant audit column:
--        'draining' | 'force_draining' → drain_initiated_at
--        'unavailable'     → NULL (heartbeat gap is the writer; this
--                             path is for the rare explicit flip)
--        'recovering'      → recovery_initiated_at
--        'maintenance'     → drain_completed_at (last step of a
--                             successful operator drain)
UPDATE compute_nodes
SET lifecycle = $3::compute_node_lifecycle,
    drain_initiated_at    = CASE WHEN $3::compute_node_lifecycle IN ('draining','force_draining') THEN $4 ELSE drain_initiated_at END,
    recovery_initiated_at = CASE WHEN $3::compute_node_lifecycle = 'recovering' THEN $4 ELSE recovery_initiated_at END,
    drain_completed_at    = CASE WHEN $3::compute_node_lifecycle = 'maintenance' THEN $4 ELSE drain_completed_at    END
WHERE id = $1
  AND lifecycle::text = $2;

-- name: NodeMarkDrainCompleted :execrows
-- Stamps drain_completed_at + flips lifecycle='maintenance'. Called once
-- the drain arbiter confirms zero live instances remain on the node.
-- CAS on a draining lifecycle so a concurrent reactivate can't race.
UPDATE compute_nodes
SET lifecycle         = 'maintenance',
    drain_completed_at = $2
WHERE id = $1
  AND lifecycle IN ('draining','force_draining');

-- name: NodeMarkRecovered :execrows
-- Stamps last_recovery_outcome='succeeded' and flips lifecycle='active'.
-- Called by the recovery arbiter after the migrate-or-recreate sweep
-- has cleared all stranded instances on a 'recovering' node. CAS on
-- 'recovering' so a fresh heartbeat-driven reactivate wins cleanly.
UPDATE compute_nodes
SET lifecycle            = 'active',
    last_recovery_outcome = 'succeeded'
WHERE id = $1
  AND lifecycle = 'recovering';

-- name: NodeListRecoverable :many
-- Nodes that need the recovery arbiter's attention. Two lifecycle
-- states qualify:
--   'unavailable'  → heartbeat gap detected; instances stranded.
--   'recovering'   → first post-failure ping succeeded; sweep to
--                    confirm zero stranded instances.
-- Unavailable rows age out of active polling after 24 hours. They remain in
-- inventory for audit; a returning vmmd re-registers through the heartbeat
-- path and becomes active again.
SELECT
    id, name, target_url, vpcpus, mem_mb, max_concurrency,
    admission_ceiling_mb,
    lifecycle::text AS lifecycle, active, last_heartbeat_at, created_at,
    region, zone, schedd_target_url, vcpu_budget, public_ip, public_ip_set_at,
    release_id, manifest_hash, host_certificate, cert_fingerprint, role,
    generation, gateway_target_url,
    drain_initiated_at, drain_completed_at, recovery_initiated_at,
    last_recovery_outcome
FROM compute_nodes
WHERE lifecycle = 'recovering'
   OR (lifecycle = 'unavailable' AND coalesce(last_heartbeat_at, created_at) >= now() - interval '24 hours')
ORDER BY name;

-- name: NodeListDrainable :many
-- Drainable candidates: lifecycle='active' AND zero live instances.
-- The drain handler refuses to flip lifecycle='draining' for nodes
-- with active traffic (it surfaces RFC 7807 `node_draining_refused`
-- instead) and waits for the operator to clear the load first; this
-- query is the "safe to drain right now" enumeration.
SELECT
    id, name, target_url, vpcpus, mem_mb, max_concurrency,
    admission_ceiling_mb,
    lifecycle::text AS lifecycle, active, last_heartbeat_at, created_at,
    region, zone, schedd_target_url, vcpu_budget, public_ip, public_ip_set_at,
    release_id, manifest_hash, host_certificate, cert_fingerprint, role,
    generation, gateway_target_url,
    drain_initiated_at, drain_completed_at, recovery_initiated_at,
    last_recovery_outcome
FROM compute_nodes
WHERE lifecycle = 'active'
  AND NOT EXISTS (
      SELECT 1 FROM instances
      WHERE instances.node_id = compute_nodes.id
        AND instances.state IN ('running', 'cold_booting', 'waking', 'draining', 'snapshotting', 'migrating', 'warm')
  )
ORDER BY name;

-- name: InstanceListByNodeForRecovery :many
-- Live instances on a specific node — input to the arbiter's
-- per-instance decision. Limited to states the arbiter can act on:
-- 'running' (live-migrate), 'cold_booting' (recreate, the snapshot
-- may not have made it to the destination yet), 'waking' (recreate —
-- same reason). The arbiter only needs the
-- (app_id, deployment_id, state, id) tuple — account_id is reachable
-- via the existing app/deployment joins if needed by downstream
-- code, but the per-tick hot loop doesn't pay for it here.
SELECT id, state, app_id, deployment_id, kind
FROM instances
WHERE node_id = $1
  AND state IN ('running', 'cold_booting', 'waking', 'draining', 'snapshotting', 'migrating', 'warm')
ORDER BY started_at;

-- name: DeploymentRecordSnapshotMiss :exec
-- Bump snapshot_miss_count + stamp Retry-After until. Called by the
-- wake flow when the snapshot-fetch path fails (stale cache, missing
-- replica on the destination, etc.). Capped-exponential backoff math
-- lives in pkg/sched/snapshot_backoff.go; this query is the state
-- write only.
--
-- $1 = deployment id
-- $2 = backoff_until timestamp
UPDATE deployments
SET snapshot_miss_count          = snapshot_miss_count + 1,
    snapshot_miss_last_at        = now(),
    snapshot_miss_backoff_until  = $2
WHERE id = $1;

-- name: DeploymentClearSnapshotBackoff :exec
-- Called by the recovery arbiter after a successful migrate-or-
-- recreate sweep has restored the destination's snapshot set, OR by
-- the wake flow on a successful cold boot. Resets the counter and
-- clears the backoff_until so future wakes don't short-circuit.
UPDATE deployments
SET snapshot_miss_count         = 0,
    snapshot_miss_last_at       = NULL,
    snapshot_miss_backoff_until = NULL
WHERE id = $1;

-- name: DeploymentSnapshotBackoffActive :one
-- The wake-side gate. Returns the row while a backoff timestamp is
-- present; the store computes whether it is still active. Returning
-- expired rows preserves the miss count for the next backoff stamp.
-- The partial index `deployments_snapshot_backoff_idx` covers this lookup.
SELECT snapshot_miss_count, snapshot_miss_backoff_until
FROM deployments
WHERE id = $1
  AND snapshot_miss_backoff_until IS NOT NULL;

-- ==============================================================
-- name: CreateUploadSession :one
-- Inserts a fresh upload_sessions row. The handler pre-validates
-- total_size against limits.SourceTarballMaxMB (pkg/api/limits.go)
-- and the per-account open-session cap (5 per (account_id, app_slug))
-- before this INSERT — sqlc only owns the type-safe binding. The
-- 1-GiB hard ceiling in the SQL CHECK is the worst-case spool size,
-- not the customer-facing quota.
INSERT INTO upload_sessions (
    id, account_id, app_slug, total_size, chunk_size, sha256_hex, part_path, deploy_options
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8
)
RETURNING id, account_id, app_slug, total_size, received_bytes, chunk_size,
          sha256_hex, part_path, status, created_at, last_patched_at, expires_at,
          deployment_id, deploy_options;

-- name: GetUploadSession :one
-- Reads a single upload_sessions row by id. Used by:
--   (a) the handler's POST /commit pre-check (validate status='open',
--       received_bytes == total_size before validating tar shape);
--   (b) the CLI's GET-when-resuming-after-network-drop path
--       (PR-2 cmd/gregale/upload_session.go) which learns the
--       server's current received_bytes to compute the next chunk's
--       Upload-Offset header.
-- No FOR UPDATE here — the row is append-only under normal operation
-- and the CAS in AppendUploadBytes is the serialisation point. If
-- future work needs a transactional read-modify-write (e.g., admin
-- force-close), add a separate GetUploadSessionForUpdate :one.
SELECT id, account_id, app_slug, total_size, received_bytes, chunk_size,
       sha256_hex, part_path, status, created_at, last_patched_at, expires_at,
       deployment_id, deploy_options
FROM upload_sessions
WHERE id = $1;

-- name: AppendUploadBytes :one
-- The atomic CAS that makes the resumable protocol safe under
-- concurrent PATCHes on the same upload_id. The handler reads
-- the client's Upload-Offset header (the offset the client claims
-- the server is currently at) and the chunk_size it sent, then
-- computes expected_new = client_offset + chunk_bytes. The WHERE
-- clause pins the row to the upload id and the explicitly named expected
-- offset — a row whose received_bytes has already
-- advanced (e.g., a racing PATCH from a retry) returns 0 rows and
-- the handler maps that to 409 Conflict with the actual current
-- offset in the body.
--
-- RETURNING exposes the new received_bytes so the handler doesn't
-- need a follow-up SELECT on the happy path. last_patched_at is
-- bumped to now() so the reaper's idle-aware expiry (NOT in PR-1;
-- deferred — see plan "Out of scope") has a fresh anchor.
UPDATE upload_sessions
   SET received_bytes = sqlc.arg(new_received_bytes),
       last_patched_at = now()
 WHERE id = sqlc.arg(id)
   AND status = 'open'
   AND expires_at > now()
   AND received_bytes = sqlc.arg(expected_received_bytes)
RETURNING received_bytes, total_size;

-- name: MarkUploadSessionCommitted :one
-- Final state transition: open → committed. The handler runs
-- validateTarballShape + scanForStatefulShape (cmd/apid/
-- deploy_inputs.go:291-447) BEFORE this UPDATE so a commit that
-- fails validation leaves the row at status='open' and the .part
-- file in place for retry. deployment_id is set after the build
-- row is enqueued (apidsource.Enqueue) so the row points at the
-- deployment that consumed the .part.
--
-- The UPDATE WHERE status='open' is the second-line idempotency
-- guard: a retry of POST /v1/uploads/{id}/commit that races with
-- itself hits 0 rows and the handler reads upload_commit_outcomes
-- to return the original deployment_id.
UPDATE upload_sessions
   SET status = 'committed',
       deployment_id = $2
 WHERE id = $1
   AND status = 'open'
RETURNING id, status, deployment_id;

-- name: CancelUploadSession :exec
-- Explicit cancel from DELETE /v1/uploads/{id}. The handler also
-- removes the .part file via os.Remove AFTER the UPDATE commits —
-- doing it before would leak a file if the UPDATE rolled back.
-- Status transition is open → cancelled; a second cancel or a
-- commit-after-cancel hits 0 rows and the handler returns 409
-- upload_session_already_cancelled.
UPDATE upload_sessions
   SET status = 'cancelled'
 WHERE id = $1
   AND account_id = $2::uuid
   AND status = 'open';

-- name: ReapExpiredUploadSessions :many
-- The reaper's scan query (cmd/apid/upload_session_reaper.go).
-- Returns at most 100 rows per invocation to bound memory; the
-- goroutine ticker at cmd/apid/main.go re-invokes on its 5-minute
-- cadence. partial index upload_sessions_expires_idx makes this
-- an index-only scan over the open sessions whose expires_at has
-- passed. The handler then UPDATEs status='expired' and removes
-- the .part file via os.Remove.
--
-- The status='open' predicate is load-bearing — once a session is
-- committed/cancelled/expired the .part file is already gone and
-- the row is terminal.
SELECT id, part_path
FROM upload_sessions
WHERE status = 'open'
  AND expires_at < now()
ORDER BY expires_at ASC
LIMIT 100;

-- name: ReapStaleUploadPartFiles :many
-- PR-1 fixup #5: sweep .part files for terminal rows whose
-- builderd consumption window has closed. The commit handler
-- leaves .part in place for builderd to consume
-- (pkg/builderd/builderd.go:407 hashFile(SourcePath)); the
-- cancel handler removes its .part at the same time it flips
-- status='cancelled'; but neither has a 1-hour cleanup guarantee
-- for committed rows. This query returns rows in terminal
-- status whose last_patched_at is >1h old and whose part_path
-- cleanup marker is still set; the reaper removes the file and
-- clears the marker after a successful removal.
--
-- The status IN (committed, cancelled, expired) predicate is
-- load-bearing — we never sweep open sessions (could race a
-- PATCH). The last_patched_at < now() - '1 hour' guard stops
-- us from racing a builderd that's mid-consumption right after
-- commit. The 100-row LIMIT bounds the per-tick work the same
-- way ReapExpiredUploadSessions does.
--
-- Index strategy: pg doesn't have an index on (status,
-- last_patched_at) today; the scan is sequential over the
-- terminal-status rows. At expected volumes (≪ 1k terminal
-- rows/day per apid) this is fine. If terminal-row volume
-- grows, add a partial index on (last_patched_at) WHERE
-- status IN ('committed', 'cancelled', 'expired') — leaving
-- as a follow-up ADR rather than conflated into PR-1's
-- migration slot 533.
SELECT id, part_path
FROM upload_sessions
WHERE status IN ('committed', 'cancelled', 'expired')
  AND part_path <> ''
  AND last_patched_at < now() - INTERVAL '1 hour'
ORDER BY last_patched_at ASC
LIMIT 100;

-- name: ClearUploadSessionPartPath :exec
-- Records that the spool file has been removed. Terminal status is
-- required so an out-of-order cleanup call cannot hide the path of
-- an open session that a concurrent PATCH still needs.
UPDATE upload_sessions
   SET part_path = ''
 WHERE id = $1
   AND status IN ('committed', 'cancelled', 'expired')
   AND part_path <> '';

-- name: ExpireUploadSession :exec
-- Marks a single session as expired after the reaper removes its
-- .part file. Split into a separate query from ReapExpiredUploadSessions
-- so the reaper can: (a) scan, (b) delete the file, (c) UPDATE.
-- If (c) fails the row stays at status='open' and the next reaper
-- tick re-runs against it — the file is already gone, so os.Remove
-- returns ErrNotExist and is logged + skipped. This avoids the
-- alternative of a single UPDATE ... RETURNING part_path that
-- would race the file delete across two replicas (single-process
-- for now; future multi-replica deployment needs SELECT ... FOR
-- UPDATE SKIP LOCKED).
UPDATE upload_sessions
   SET status = 'expired'
 WHERE id = $1
   AND status = 'open';

-- name: RecordUploadCommitOutcome :one
-- INSERT ON CONFLICT DO NOTHING for the upload_commit_outcomes
-- companion table. The handler calls this AFTER a successful
-- apidsource.Enqueue and BEFORE writing the 201 response. On
-- retry of POST /v1/uploads/{id}/commit (network blip after the
-- server wrote the row but before the client got the response),
-- the INSERT hits the conflict path and returns 0 rows; the
-- handler then calls GetUploadCommitOutcome to return the
-- original deployment_id. ON CONFLICT DO NOTHING (rather than
-- DO UPDATE) is correct: the original row is canonical.
INSERT INTO upload_commit_outcomes (upload_id, deployment_id, build_id)
VALUES ($1, $2, $3)
ON CONFLICT (upload_id) DO NOTHING
RETURNING upload_id, deployment_id, build_id, finalized_at;

-- name: GetUploadCommitOutcome :one
-- Reads the dedupe row for a retry of POST /v1/uploads/{id}/commit.
-- Returns 0 rows if the original commit never wrote (handler
-- surfaces this as 500 — the prior UPDATE MarkUploadSessionCommitted
-- also failed, so the operator needs the build row's failure
-- class).
SELECT upload_id, deployment_id, build_id, finalized_at
FROM upload_commit_outcomes
WHERE upload_id = $1;

-- name: CountOpenUploadSessionsByAccountApp :one
-- Per-(account_id, app_slug) open-session cap check at the top of
-- POST /v1/uploads. Returns the current count; the handler
-- refuses with 429 upload_session_too_many when count >= 5.
-- Hits the partial index upload_sessions_account_open_idx.
SELECT COUNT(*)::bigint AS count
FROM upload_sessions
WHERE account_id = $1::uuid
  AND app_slug = $2
  AND status = 'open';

-- name: SumOpenUploadSessionBytesByAccount :one
-- Per-account open-spool budget check (4 × SourceTarballMaxMB cap
-- per plan). The handler sums the declared total_size across all
-- open sessions for the account, adds the new total_size, and
-- refuses with 429 upload_session_too_many if the sum exceeds
-- the budget. Hits upload_sessions_account_open_idx for the
-- (account_id) predicate; the SUM is over the partial index.
SELECT COALESCE(SUM(total_size), 0)::bigint AS bytes
FROM upload_sessions
WHERE account_id = $1::uuid
  AND status = 'open';

-- name: ObjectBucketLockApp :one
SELECT id FROM apps WHERE id = $1 AND account_id = $2 AND status <> 'deleted' FOR UPDATE;

-- name: ObjectBucketByName :one
SELECT * FROM object_buckets WHERE app_id = $1 AND account_id = $2 AND name = $3 AND scope = $4 AND state <> 'deleted';

-- name: ObjectBucketCount :one
SELECT count(*) FROM object_buckets WHERE app_id = $1 AND state <> 'deleted';

-- name: ObjectBucketCountForAccount :one
SELECT count(*) FROM object_buckets WHERE account_id = $1 AND state <> 'deleted';

-- name: ObjectBucketPruneTombstones :exec
DELETE FROM object_buckets WHERE account_id = $1 AND state = 'deleted';

-- name: ObjectBucketInsert :one
INSERT INTO object_buckets (id, account_id, app_id, name, scope, region, backend_id, backend_fingerprint, physical_name, public_read, serve_at, environment_clone_source_bucket_id, environment_clone_operation_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13) RETURNING *;

-- name: ObjectBucketReserveLockAccount :one
SELECT id FROM accounts WHERE id=$1 AND status='active' FOR UPDATE;

-- name: ObjectBucketList :many
SELECT b.* FROM object_buckets b WHERE b.account_id = $1 AND b.app_id = $2 AND b.state <> 'deleted'
  AND (b.environment_clone_operation_id IS NULL OR EXISTS (
      SELECT 1 FROM project_environment_clone_operations clone_owner
      WHERE clone_owner.id = b.environment_clone_operation_id
        AND clone_owner.account_id = b.account_id
        AND clone_owner.target_environment = b.scope AND clone_owner.status = 'ready'
  ))
ORDER BY b.created_at, b.id;

-- name: ObjectAccountBucketCleanupList :many
SELECT * FROM object_buckets WHERE account_id=$1 AND state<>'deleted' ORDER BY created_at,id LIMIT $2;

-- name: ObjectBucketGet :one
SELECT b.* FROM object_buckets b WHERE b.account_id = $1 AND b.app_id = $2 AND b.id = $3 AND b.state <> 'deleted'
  AND (b.environment_clone_operation_id IS NULL OR EXISTS (
      SELECT 1 FROM project_environment_clone_operations clone_owner
      WHERE clone_owner.id = b.environment_clone_operation_id
        AND clone_owner.account_id = b.account_id
        AND clone_owner.target_environment = b.scope AND clone_owner.status = 'ready'
  ));

-- name: ObjectBucketAccessGrantList :many
SELECT g.account_id, g.bucket_id, g.api_key_id, g.permission,
       coalesce(k.label, '')::text AS key_label, k.status AS key_status,
       g.created_at, g.updated_at
FROM object_storage_access_grants g
JOIN api_keys k ON k.id = g.api_key_id AND k.account_id = g.account_id
JOIN object_buckets b ON b.id = g.bucket_id AND b.account_id = g.account_id
WHERE g.account_id = $1 AND g.bucket_id = $2 AND b.state <> 'deleted'
ORDER BY g.created_at, g.api_key_id;

-- name: ObjectBucketAccessGrantGet :one
SELECT g.account_id, g.bucket_id, g.api_key_id, g.permission,
       coalesce(k.label, '')::text AS key_label, k.status AS key_status,
       g.created_at, g.updated_at
FROM object_storage_access_grants g
JOIN api_keys k ON k.id = g.api_key_id AND k.account_id = g.account_id
JOIN object_buckets b ON b.id = g.bucket_id AND b.account_id = g.account_id
WHERE g.account_id = $1 AND g.bucket_id = $2 AND g.api_key_id = $3
  AND b.state <> 'deleted';

-- name: ObjectBucketAccessGrantUpsert :execrows
INSERT INTO object_storage_access_grants (account_id, bucket_id, api_key_id, permission)
SELECT b.account_id, b.id, k.id, sqlc.arg(permission)::text
FROM object_buckets b
JOIN api_keys k ON k.account_id = b.account_id
WHERE b.account_id = sqlc.arg(account_id) AND b.id = sqlc.arg(bucket_id)
  AND b.state <> 'deleted'
  AND (b.environment_clone_operation_id IS NULL OR EXISTS (
      SELECT 1 FROM project_environment_clone_operations clone_owner
      WHERE clone_owner.id = b.environment_clone_operation_id
        AND clone_owner.account_id = b.account_id
        AND clone_owner.target_environment = b.scope AND clone_owner.status = 'ready'
  )) AND k.id = sqlc.arg(api_key_id)
  AND k.status IN ('active', 'grace')
  AND NOT ('admin' = ANY(k.scopes))
  AND (sqlc.arg(permission)::text <> 'read' OR k.scopes @> ARRAY['storage:read']::text[])
  AND (sqlc.arg(permission)::text <> 'write' OR k.scopes @> ARRAY['storage:write']::text[])
  AND (sqlc.arg(permission)::text <> 'read_write' OR k.scopes @> ARRAY['storage:read', 'storage:write']::text[])
ON CONFLICT (bucket_id, api_key_id) DO UPDATE
SET permission = EXCLUDED.permission, updated_at = now();

-- name: ObjectBucketAccessGrantDelete :execrows
DELETE FROM object_storage_access_grants g
USING object_buckets b, api_keys k
WHERE g.account_id = $1 AND g.bucket_id = $2 AND g.api_key_id = $3
  AND b.id = g.bucket_id AND b.account_id = g.account_id AND b.state <> 'deleted'
  AND k.id = g.api_key_id AND k.account_id = g.account_id;

-- name: ObjectBucketAccessCheck :one
SELECT EXISTS (
    SELECT 1
    FROM object_storage_access_grants g
    JOIN object_buckets b ON b.id = g.bucket_id AND b.account_id = g.account_id
    JOIN api_keys k ON k.id = g.api_key_id AND k.account_id = g.account_id
    WHERE g.account_id = $1 AND g.bucket_id = $2 AND g.api_key_id = $3
      AND b.state <> 'deleted'
  AND (b.environment_clone_operation_id IS NULL OR EXISTS (
      SELECT 1 FROM project_environment_clone_operations clone_owner
      WHERE clone_owner.id = b.environment_clone_operation_id
        AND clone_owner.account_id = b.account_id
        AND clone_owner.target_environment = b.scope AND clone_owner.status = 'ready'
  )) AND k.status IN ('active', 'grace')
      AND (($4::text = 'read' AND g.permission IN ('read', 'read_write'))
        OR ($4::text = 'write' AND g.permission IN ('write', 'read_write')))
      AND (($4::text = 'read' AND k.scopes @> ARRAY['storage:read']::text[])
        OR ($4::text = 'write' AND k.scopes @> ARRAY['storage:write']::text[]))
) AS allowed;

-- name: ObjectBucketListForKey :many
SELECT b.*
FROM object_buckets b
JOIN object_storage_access_grants g
  ON g.bucket_id = b.id AND g.account_id = b.account_id
JOIN api_keys k ON k.id = g.api_key_id AND k.account_id = g.account_id
WHERE b.account_id = $1 AND b.app_id = $2 AND g.api_key_id = $3
  AND b.state <> 'deleted'
  AND (b.environment_clone_operation_id IS NULL OR EXISTS (
      SELECT 1 FROM project_environment_clone_operations clone_owner
      WHERE clone_owner.id = b.environment_clone_operation_id
        AND clone_owner.account_id = b.account_id
        AND clone_owner.target_environment = b.scope AND clone_owner.status = 'ready'
  )) AND k.status IN ('active', 'grace')
ORDER BY b.created_at, b.id;

-- name: ObjectBucketClaim :one
UPDATE object_buckets SET state = $1, lease_token = $2, lease_until = now() + ($3::int * interval '1 second'), updated_at = now(),
attempt_count = CASE WHEN state <> $1 THEN 1 ELSE least(attempt_count + 1, 30) END,
last_error_code = CASE WHEN state <> $1 THEN '' ELSE last_error_code END, retry_at = now()
WHERE object_buckets.account_id = $4 AND object_buckets.app_id = $5 AND object_buckets.id = $6
AND object_buckets.state <> 'deleted' AND (object_buckets.lease_until IS NULL OR object_buckets.lease_until < now())
AND ($1 = 'deleting' OR object_buckets.state = 'provisioning')
AND ($1 <> 'deleting' OR NOT EXISTS(SELECT 1 FROM object_version_protection p WHERE p.bucket_id=object_buckets.id AND p.state IN ('waiting','applying')))
AND ($1 <> 'deleting' OR NOT EXISTS (
  SELECT 1 FROM object_storage_multipart_uploads m WHERE m.bucket_id = object_buckets.id
  AND m.state IN ('initiating','active','completing','completing_conditional','aborting')
))
AND ($1 <> 'deleting' OR NOT EXISTS (
  SELECT 1 FROM object_bucket_write_fences f WHERE f.bucket_id = object_buckets.id
))
AND ($1 <> 'deleting' OR NOT EXISTS (
  SELECT 1 FROM object_storage_write_admissions w WHERE w.bucket_id = object_buckets.id AND w.state = 'pending'
  AND (w.multipart_upload_id IS NULL OR EXISTS (
    SELECT 1 FROM object_storage_multipart_uploads m WHERE m.id = w.multipart_upload_id
    AND m.state IN ('initiating','active','completing','completing_conditional','aborting')
  ))
))
AND (NOT sqlc.arg(recovery)::boolean OR object_buckets.state = $1)
AND (object_buckets.retry_at <= now() OR object_buckets.state <> $1) RETURNING *;

-- name: ObjectBucketFinish :execrows
UPDATE object_buckets SET state = $1, lease_token = NULL, lease_until = NULL, updated_at = now(),
attempt_count = 0, last_error_code = '', retry_at = now() WHERE id = $2 AND lease_token = $3;

-- name: ObjectBucketRetry :execrows
UPDATE object_buckets SET lease_token = NULL, lease_until = NULL, updated_at = now(),
last_error_code = $3, retry_at = now() + ($4::int * interval '1 second')
WHERE id = $1 AND lease_token = $2 AND state IN ('provisioning', 'deleting');

-- name: ObjectBucketsDue :many
SELECT * FROM object_buckets
WHERE (state = 'deleting' OR (sqlc.arg(include_provisioning)::boolean AND state = 'provisioning'))
AND retry_at <= now() AND (lease_until IS NULL OR lease_until < now())
ORDER BY retry_at, id LIMIT sqlc.arg(batch_limit)::int;

-- name: SnapshotLocalityNodes :many
SELECT node_id::text AS node_id, true AS is_origin
FROM snapshot_origins
WHERE snapshot_id = $1::uuid AND node_id IS NOT NULL
UNION ALL
SELECT node_id::text AS node_id, false AS is_origin
FROM snapshot_replicas
WHERE snapshot_id = $1::uuid AND state = 'ready'
ORDER BY node_id, is_origin DESC;

-- name: ObjectUsageLockAccount :one
SELECT id FROM accounts WHERE id = $1 FOR UPDATE;

-- name: ObjectUsageBucketAccount :one
SELECT account_id FROM object_buckets WHERE id=$1;

-- name: ObjectUsageBuckets :many
SELECT b.*, u.baseline_bytes, u.baseline_keys, u.granted_bytes, u.granted_keys,
u.observed_bytes, u.observed_keys, u.observed_at, u.attempt_at, u.lease_until AS inventory_lease_until, u.token, u.inventory_scope,
COALESCE((SELECT sum(g.max_bytes)::bigint FROM object_storage_multipart_part_grants g
JOIN object_storage_multipart_uploads m ON m.id=g.upload_id
WHERE m.bucket_id=b.id AND m.state <> 'completed'),0)::bigint AS multipart_bytes
FROM object_buckets b LEFT JOIN object_storage_bucket_usage u ON u.bucket_id = b.id
WHERE b.account_id = $1;

-- name: ObjectUsageGrant :one
SELECT max_bytes FROM object_storage_key_grants WHERE bucket_id = $1 AND key_hash = $2;

-- name: ObjectUsageGrantUpsert :exec
INSERT INTO object_storage_key_grants (bucket_id, key_hash, max_bytes) VALUES ($1,$2,$3)
ON CONFLICT (bucket_id,key_hash) DO UPDATE SET max_bytes = greatest(object_storage_key_grants.max_bytes, EXCLUDED.max_bytes);

-- name: ObjectUsageGrantIncrement :exec
UPDATE object_storage_bucket_usage SET granted_bytes = granted_bytes + $2, granted_keys = granted_keys + $3 WHERE bucket_id = $1;

-- name: ObjectUsageAuthorizationCount :one
SELECT count FROM object_storage_authorizations WHERE account_id=$1 AND period_start=$2;

-- name: ObjectUsageAuthorize :exec
INSERT INTO object_storage_authorizations (account_id, period_start, count) VALUES ($1,$2,1)
ON CONFLICT (account_id,period_start) DO UPDATE SET count = object_storage_authorizations.count + 1;

-- name: ObjectStorageProviderRequestIncrement :exec
INSERT INTO object_storage_request_metrics (bucket_id, period_start, request_count)
VALUES ($1, $2, 1)
ON CONFLICT (bucket_id, period_start) DO UPDATE
SET request_count = object_storage_request_metrics.request_count + 1;

-- name: ObjectStorageProviderEgressIncrement :exec
INSERT INTO object_storage_request_metrics (bucket_id, period_start, egress_bytes)
VALUES ($1, $2, $3)
ON CONFLICT (bucket_id, period_start) DO UPDATE
SET egress_bytes = object_storage_request_metrics.egress_bytes + EXCLUDED.egress_bytes;

-- name: ObjectStorageProviderRequestMetrics :many
SELECT b.id, b.account_id, b.backend_id, b.backend_fingerprint, b.physical_name,
       sqlc.arg(period_start)::timestamptz AS period_start, COALESCE(m.request_count, 0)::bigint AS request_count,
       COALESCE(m.egress_bytes, 0)::bigint AS egress_bytes
FROM object_buckets b
LEFT JOIN object_storage_request_metrics m
  ON m.bucket_id = b.id AND m.period_start = sqlc.arg(period_start)
WHERE b.backend_id = $1 AND b.backend_fingerprint = $2
ORDER BY b.physical_name, b.id;

-- name: ObjectStorageProviderBuckets :many
SELECT * FROM object_buckets
WHERE backend_id = $1 AND backend_fingerprint = $2
ORDER BY physical_name, id;

-- name: ObjectUsageReports :many
SELECT r.* FROM object_storage_usage_heads h JOIN object_storage_usage_reports r
USING (account_id,backend_id,period_start,observed_at)
WHERE h.account_id=$1 AND h.period_start=$2 ORDER BY h.backend_id;

-- name: ObjectUsageReportHead :exec
INSERT INTO object_storage_usage_heads (account_id,backend_id,period_start,observed_at) VALUES ($1,$2,$3,$4)
ON CONFLICT (account_id,period_start,backend_id) DO UPDATE SET observed_at=EXCLUDED.observed_at;

-- name: ObjectUsageReportGet :one
SELECT * FROM object_storage_usage_reports WHERE account_id=$1 AND backend_id=$2 AND period_start=$3 AND observed_at=$4;

-- name: ObjectUsageReportInsert :exec
INSERT INTO object_storage_usage_reports (account_id,backend_id,backend_fingerprint,source,period_start,observed_at,stored_byte_hours,request_count,egress_bytes,cost_millicents)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10);

-- name: ObjectInventoriesDue :many
SELECT b.* FROM object_buckets b LEFT JOIN object_storage_bucket_usage u ON u.bucket_id=b.id
WHERE b.state='ready' AND NOT EXISTS(SELECT 1 FROM object_bucket_versioning WHERE bucket_id=b.id AND state<>'ready') AND NOT EXISTS (SELECT 1 FROM object_storage_capacity_reconciliations c WHERE c.bucket_id=b.id AND c.state IN ('waiting','scanning')) AND (u.attempt_at IS NULL OR u.attempt_at < now() - interval '5 minutes')
AND (u.lease_until IS NULL OR u.lease_until < now())
ORDER BY u.attempt_at NULLS FIRST, b.id LIMIT $1;

-- name: ObjectInventoryClaim :execrows
INSERT INTO object_storage_bucket_usage (bucket_id, attempt_at, lease_until, token)
SELECT b.id, now(), now()+interval '2 minutes', sqlc.arg(token)::text FROM object_buckets b WHERE b.id=$1 AND b.state='ready' AND NOT EXISTS(SELECT 1 FROM object_bucket_versioning WHERE bucket_id=$1 AND state<>'ready') AND NOT EXISTS (SELECT 1 FROM object_storage_capacity_reconciliations c WHERE c.bucket_id=$1 AND c.state IN ('waiting','scanning'))
ON CONFLICT (bucket_id) DO UPDATE SET attempt_at=now(),lease_until=now()+interval '2 minutes',token=EXCLUDED.token
WHERE object_storage_bucket_usage.lease_until IS NULL OR object_storage_bucket_usage.lease_until < now();

-- name: ObjectInventoryFinish :execrows
UPDATE object_storage_bucket_usage u SET baseline_bytes = CASE WHEN observed_at IS NULL THEN sqlc.arg(bytes)::bigint ELSE baseline_bytes END,
baseline_keys = CASE WHEN observed_at IS NULL THEN sqlc.arg(objects)::bigint ELSE baseline_keys END,
observed_bytes=sqlc.arg(bytes),observed_keys=sqlc.arg(objects),observed_at=attempt_at,lease_until=NULL,token=''
WHERE u.bucket_id=$1 AND u.token=$2 AND u.lease_until > now()
AND EXISTS (SELECT 1 FROM object_buckets WHERE id=$1 AND state='ready')
AND u.inventory_scope='current'
AND NOT EXISTS(SELECT 1 FROM object_upload_completions WHERE bucket_id=$1 AND recovery_versions_observed)
AND NOT EXISTS(SELECT 1 FROM object_version_references WHERE bucket_id=$1 AND versions_observed)
AND NOT EXISTS(SELECT 1 FROM object_storage_multipart_uploads WHERE bucket_id=$1 AND completion_versions_observed)
AND NOT EXISTS(SELECT 1 FROM object_bucket_versioning WHERE bucket_id=$1 AND (versions_required OR state<>'ready'))
AND NOT EXISTS (SELECT 1 FROM object_storage_capacity_reconciliations c WHERE c.bucket_id=$1 AND c.state IN ('waiting','scanning'));

-- name: ObjectInventorySample :exec
INSERT INTO object_storage_inventory_samples (token,bucket_id,observed_at,bytes,objects)
SELECT $2,u.bucket_id,u.observed_at,u.observed_bytes,u.observed_keys FROM object_storage_bucket_usage u WHERE u.bucket_id=$1;

-- name: ObjectMultipartByKey :one
SELECT * FROM object_storage_multipart_uploads
WHERE account_id=$1 AND app_id=$2 AND bucket_id=$3 AND object_key=$4
AND state IN ('initiating','active','completing','completing_conditional','aborting');

-- name: ObjectMultipartLockBucket :one
SELECT b.id FROM object_buckets b
WHERE b.id=$1 AND b.account_id=$2 AND b.app_id=$3 AND b.state='ready'
  AND (b.environment_clone_operation_id IS NULL OR EXISTS (
      SELECT 1 FROM project_environment_clone_operations clone_owner
      WHERE clone_owner.id = b.environment_clone_operation_id
        AND clone_owner.account_id = b.account_id
        AND clone_owner.target_environment = b.scope AND clone_owner.status = 'ready'
  ))
FOR NO KEY UPDATE;

-- name: ObjectMultipartCount :one
SELECT count(*) FROM object_storage_multipart_uploads
WHERE bucket_id=$1 AND state IN ('initiating','active','completing','completing_conditional','aborting');

-- name: ObjectMultipartInsert :one
INSERT INTO object_storage_multipart_uploads
(id,account_id,app_id,bucket_id,object_key,size_bytes,part_size_bytes,part_count,content_type,object_metadata,expires_at,encryption_snapshot,protection_snapshot,fixed_admission,encryption_default_revision)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,sqlc.arg(encryption_snapshot)::jsonb,sqlc.arg(protection_snapshot)::jsonb,sqlc.arg(fixed_admission)::boolean,sqlc.arg(encryption_default_revision)::bigint) RETURNING *;

-- name: ObjectMultipartGet :one
SELECT * FROM object_storage_multipart_uploads
WHERE account_id=$1 AND app_id=$2 AND bucket_id=$3 AND id=$4;

-- name: ObjectMultipartList :many
SELECT * FROM object_storage_multipart_uploads
WHERE account_id=$1 AND app_id=$2 AND bucket_id=$3 AND id>$4
ORDER BY id LIMIT sqlc.arg(page_limit)::int;

-- name: ObjectMultipartClaim :one
UPDATE object_storage_multipart_uploads SET
state=sqlc.arg(operation), lease_token=sqlc.arg(token),
encryption_lease_token=CASE WHEN encryption_snapshot='{}' THEN '' ELSE sqlc.arg(token)::text END,
protection_lease_token=CASE WHEN protection_snapshot='{}' THEN '' ELSE sqlc.arg(token)::text END,
lease_until=now()+(sqlc.arg(lease_seconds)::int * interval '1 second'),
completion_if_match=CASE WHEN state='active' THEN sqlc.arg(completion_if_match)::text ELSE completion_if_match END,
completion_if_none_match=CASE WHEN state='active' THEN sqlc.arg(completion_if_none_match)::text ELSE completion_if_none_match END,
completion_parts=CASE WHEN state='active' AND sqlc.arg(operation)::text IN ('completing','completing_conditional')
  THEN sqlc.arg(completion_parts)::jsonb ELSE completion_parts END,
attempt_count=CASE WHEN state<>sqlc.arg(operation)::text THEN 1 ELSE least(attempt_count+1,30) END,
last_error_code=CASE WHEN state<>sqlc.arg(operation)::text THEN '' ELSE last_error_code END,
retry_at=now(), updated_at=now()
WHERE account_id=sqlc.arg(account_id) AND app_id=sqlc.arg(app_id)
AND bucket_id=sqlc.arg(bucket_id) AND id=sqlc.arg(id)
AND (lease_until IS NULL OR lease_until<now())
AND (encryption_snapshot='{}' OR octet_length(sqlc.arg(token)::text)<=128)
AND (retry_at<=now() OR state<>sqlc.arg(operation)::text)
AND (NOT sqlc.arg(recovery)::boolean OR state=sqlc.arg(operation)::text
  OR (state='active' AND sqlc.arg(operation)::text='aborting'))
AND (
  (sqlc.arg(operation)::text='initiating' AND state='initiating' AND provider_upload_id='') OR
  (sqlc.arg(operation)::text IN ('completing','completing_conditional') AND (state='active' OR state=sqlc.arg(operation)::text)
    AND (state<>'active' OR ((sqlc.arg(operation)::text='completing') = (sqlc.arg(completion_if_match)::text='' AND sqlc.arg(completion_if_none_match)::text='')))
    AND (state<>'active' OR expires_at>now())
    AND (state<>'active' OR jsonb_array_length(sqlc.arg(completion_parts)::jsonb)>0)) OR
  (sqlc.arg(operation)::text='aborting' AND state IN ('active','aborting') AND provider_upload_id<>'')
) RETURNING *;

-- name: ObjectMultipartActivate :execrows
UPDATE object_storage_multipart_uploads SET state='active',provider_upload_id=$3,
lease_token=NULL,lease_until=NULL,attempt_count=0,last_error_code='',retry_at=now(),updated_at=now()
WHERE id=$1 AND lease_token=$2 AND state='initiating' AND $3<>'';

-- name: ObjectMultipartFinish :execrows
UPDATE object_storage_multipart_uploads SET state=$3,lease_token=NULL,lease_until=NULL,
attempt_count=0,last_error_code='',retry_at=now(),updated_at=now()
WHERE id=$1 AND lease_token=$2 AND
(state IN ('completing','completing_conditional') AND $3='completed' AND NOT completion_dispatched AND encryption_snapshot='{}' AND protection_snapshot='{}');

-- name: ObjectMultipartFinishVerifiedAbort :execrows
UPDATE object_storage_multipart_uploads SET state='aborted',lease_token=NULL,lease_until=NULL,
attempt_count=0,last_error_code='',retry_at=now(),updated_at=now()
WHERE id=$1 AND lease_token=$2 AND state='aborting'
AND (part_url_unsafe_until IS NULL OR part_url_unsafe_until<=clock_timestamp());

-- name: ObjectMultipartRecordPartURL :execrows
UPDATE object_storage_multipart_uploads
SET part_url_unsafe_until=greatest(part_url_unsafe_until,sqlc.arg(signed_expires_at)::timestamptz+make_interval(secs=>sqlc.arg(drain_seconds)::int))
WHERE id=sqlc.arg(id) AND account_id=sqlc.arg(account_id) AND app_id=sqlc.arg(app_id) AND bucket_id=sqlc.arg(bucket_id)
AND object_key=sqlc.arg(object_key) AND provider_upload_id=sqlc.arg(provider_upload_id)
AND state='active' AND part_count>0 AND expires_at>clock_timestamp()
AND sqlc.arg(signed_expires_at)::timestamptz>clock_timestamp()
AND sqlc.arg(signed_expires_at)::timestamptz<=clock_timestamp()+make_interval(secs=>sqlc.arg(max_ttl_seconds)::int);

-- name: ObjectMultipartSetSize :execrows
UPDATE object_storage_multipart_uploads SET size_bytes=$3,updated_at=now()
WHERE id=$1 AND lease_token=$2 AND state IN ('completing','completing_conditional');

-- name: ObjectMultipartRetry :execrows
UPDATE object_storage_multipart_uploads SET lease_token=NULL,lease_until=NULL,last_error_code=$3,
retry_at=now()+($4::int * interval '1 second'),updated_at=now()
WHERE id=$1 AND lease_token=$2 AND state IN ('initiating','completing','completing_conditional','aborting');

-- name: ObjectMultipartDue :many
SELECT * FROM object_storage_multipart_uploads
WHERE (((state IN ('initiating','completing','completing_conditional','aborting')) AND retry_at<=now())
  OR (state='active' AND expires_at<=now()))
AND (lease_until IS NULL OR lease_until<now())
ORDER BY retry_at,id LIMIT sqlc.arg(batch_limit)::int;

-- name: ObjectMultipartResultLock :one
SELECT * FROM object_storage_multipart_uploads WHERE id=$1 AND account_id=$2 AND app_id=$3 AND bucket_id=$4 FOR UPDATE;

-- name: ObjectMultipartDispatch :execrows
UPDATE object_storage_multipart_uploads SET completion_dispatched=true,updated_at=now()
WHERE id=$1 AND lease_token=$2 AND state IN ('completing','completing_conditional');

-- name: ObjectMultipartFinishResult :one
UPDATE object_storage_multipart_uploads SET state='completed',encryption_verified=sqlc.arg(encryption_verified)::boolean,protection_verified=sqlc.arg(protection_verified)::boolean,completion_etag=sqlc.arg(etag),completion_version_id=sqlc.arg(version_id),
 completion_recovery_cursor='',completion_versions_observed=completion_versions_observed OR sqlc.arg(versions_observed)::boolean,
 lease_token=NULL,lease_until=NULL,attempt_count=0,last_error_code='',retry_at=now(),updated_at=now()
WHERE id=sqlc.arg(id) AND lease_token=sqlc.arg(token) AND completion_dispatched AND state IN ('completing','completing_conditional') RETURNING *;

-- name: ObjectMultipartRetryResult :execrows
UPDATE object_storage_multipart_uploads SET completion_recovery_cursor=sqlc.arg(cursor),completion_versions_observed=completion_versions_observed OR sqlc.arg(versions_observed)::boolean,
 lease_token=NULL,lease_until=NULL,last_error_code=sqlc.arg(code),retry_at=now()+(sqlc.arg(delay_seconds)::int * interval '1 second'),updated_at=now()
WHERE id=sqlc.arg(id) AND lease_token=sqlc.arg(token) AND completion_dispatched AND state IN ('completing','completing_conditional');

-- name: ObjectMultipartRejectResult :execrows
UPDATE object_storage_multipart_uploads SET state='aborting',completion_error_code=sqlc.arg(code),completion_recovery_cursor='',
 completion_versions_observed=completion_versions_observed OR sqlc.arg(versions_observed)::boolean,
 lease_token=NULL,lease_until=NULL,attempt_count=0,last_error_code=sqlc.arg(code),retry_at=now(),updated_at=now()
WHERE id=sqlc.arg(id) AND lease_token=sqlc.arg(token) AND completion_dispatched AND state='completing_conditional';

-- name: ObjectS3CredentialLockBucket :one
SELECT b.id FROM object_buckets b
WHERE b.id=$1 AND b.account_id=$2 AND b.state='ready'
  AND (b.environment_clone_operation_id IS NULL OR EXISTS (
      SELECT 1 FROM project_environment_clone_operations clone_owner
      WHERE clone_owner.id = b.environment_clone_operation_id
        AND clone_owner.account_id = b.account_id
        AND clone_owner.target_environment = b.scope AND clone_owner.status = 'ready'
  ))
FOR UPDATE;

-- name: ObjectURLCredentialLockBucket :one
SELECT b.id FROM object_buckets b
WHERE b.id=$1 AND b.account_id=$2 AND b.state='ready'
  AND (b.environment_clone_operation_id IS NULL OR EXISTS (
      SELECT 1 FROM project_environment_clone_operations clone_owner
      WHERE clone_owner.id=b.environment_clone_operation_id
        AND clone_owner.account_id=b.account_id
        AND clone_owner.target_environment=b.scope AND clone_owner.status='ready'
  ))
FOR NO KEY UPDATE;

-- name: ObjectS3CredentialCount :one
SELECT count(*) FROM object_storage_s3_credentials
WHERE bucket_id=$1 AND status='active' AND rotation_parent_id IS NULL AND url_request IS NULL;

-- name: ObjectS3BindingLockApp :one
SELECT a.id FROM apps a JOIN object_buckets b ON b.app_id=a.id
WHERE a.id=sqlc.arg(app_id)::uuid AND a.account_id=sqlc.arg(account_id)::uuid
  AND b.id=sqlc.arg(bucket_id)::uuid AND b.account_id=sqlc.arg(account_id)::uuid
FOR UPDATE OF a;

-- name: ObjectS3BindingSecretCount :one
SELECT count(*) FROM app_secrets WHERE account_id=$1 AND app_id=$2;

-- name: ObjectS3BindingSecretInsert :one
INSERT INTO app_secrets (account_id,app_id,scope,key,ciphertext,kid,value_hash,managed_object_storage_credential_id)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
ON CONFLICT (app_id,scope,key) DO NOTHING RETURNING key;

-- name: ObjectS3BindingStampRuntime :exec
INSERT INTO app_runtime_config_changes (app_id,changed_at) VALUES ($1,clock_timestamp())
ON CONFLICT (app_id) DO UPDATE SET changed_at=excluded.changed_at;

-- name: ObjectS3BindingStaleSnapshots :exec
UPDATE snapshots SET stale=true
WHERE deployment_id IN (SELECT id FROM deployments WHERE app_id=$1) AND stale=false;

-- name: ObjectS3BindingRevokeLock :one
SELECT * FROM object_storage_s3_credentials
WHERE id=$1 AND account_id=$2 AND bucket_id=$3
  AND managed_app_id IS NOT NULL AND rotation_parent_id IS NULL
FOR UPDATE;

-- name: ObjectS3BindingDeleteSecrets :execrows
DELETE FROM app_secrets WHERE managed_object_storage_credential_id=$1;

-- name: ObjectS3CredentialInsert :one
INSERT INTO object_storage_s3_credentials
(id,account_id,bucket_id,access_key_id,secret_sealed,kid,label,permission,status,managed_app_id,managed_scope,managed_prefix)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,'active',NULLIF($9::text,'')::uuid,NULLIF($10,''),NULLIF($11,'')) RETURNING *;

-- name: ObjectS3CredentialList :many
SELECT * FROM object_storage_s3_credentials
WHERE account_id=$1 AND bucket_id=$2 AND status='active' AND rotation_parent_id IS NULL AND url_request IS NULL
ORDER BY created_at,id;

-- name: ObjectS3CredentialRevoke :execrows
UPDATE object_storage_s3_credentials SET status='revoked',revoked_at=now()
WHERE (id=$1 OR rotation_parent_id=$1) AND account_id=$2 AND bucket_id=$3 AND status='active';

-- name: ObjectS3CredentialGet :one
SELECT * FROM object_storage_s3_credentials
WHERE id=$1 AND account_id=$2 AND bucket_id=$3 AND rotation_parent_id IS NULL AND url_request IS NULL;

-- name: ObjectS3CredentialRotationParentForUpdate :one
SELECT * FROM object_storage_s3_credentials
WHERE id=$1 AND account_id=$2 AND bucket_id=$3 AND status='active'
  AND managed_app_id IS NOT NULL AND rotation_parent_id IS NULL
FOR UPDATE;

-- name: ObjectS3CredentialRotationPending :one
SELECT rotation_wake_id::text FROM object_storage_s3_credentials
WHERE account_id=$1 AND bucket_id=$2 AND rotation_parent_id=$3 AND status='active';

-- name: ObjectS3CredentialRotationStage :one
INSERT INTO object_storage_s3_credentials
(id,account_id,bucket_id,access_key_id,secret_sealed,kid,label,permission,status,rotation_parent_id,rotation_wake_id)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,'active',$9,$10)
RETURNING *;

-- name: ObjectS3CredentialRotationReplace :one
UPDATE object_storage_s3_credentials
SET access_key_id=$4, secret_sealed=$5, kid=$6, last_used_at=NULL
WHERE id=$1 AND account_id=$2 AND bucket_id=$3 AND status='active'
  AND managed_app_id IS NOT NULL AND rotation_parent_id IS NULL
RETURNING *;

-- name: ObjectS3CredentialRotationFinalizeForApp :execrows
UPDATE object_storage_s3_credentials AS previous
SET status='revoked', revoked_at=now()
FROM object_storage_s3_credentials AS binding
WHERE previous.rotation_parent_id=binding.id
  AND binding.managed_app_id=$1 AND previous.rotation_wake_id=$2
  AND previous.status='active' AND binding.status='active';

-- name: ObjectS3CredentialRotationStampStage :one
UPDATE object_storage_s3_credentials AS previous
SET rotation_stamped_at=clock_timestamp()
FROM object_storage_s3_credentials AS binding
WHERE previous.rotation_parent_id=binding.id
  AND binding.managed_app_id=$1 AND previous.rotation_wake_id=$2
  AND previous.status='active' AND binding.status='active'
  AND previous.rotation_stamped_at IS NULL
RETURNING previous.rotation_stamped_at;

-- name: ObjectS3CredentialRotationStampApp :exec
INSERT INTO app_runtime_config_changes (app_id, changed_at) VALUES ($1, $2)
ON CONFLICT (app_id) DO UPDATE
SET changed_at=GREATEST(app_runtime_config_changes.changed_at, excluded.changed_at);

-- name: ObjectStorageManagedSecretRotate :execrows
UPDATE app_secrets SET ciphertext=$6, kid=$7, value_hash=$8,
  delivery_version=delivery_version+1, delivery_status='pending',
  last_delivery_attempt_at=NULL, last_delivery_error_code=NULL, updated_at=now()
WHERE account_id=$1 AND app_id=$2 AND scope=$3
  AND managed_object_storage_credential_id=$4 AND key=$5;

-- name: ObjectS3CredentialResolve :one
SELECT c.*, b.app_id, b.name AS bucket_name, b.scope AS bucket_scope,
       b.region AS bucket_region, b.backend_id, b.backend_fingerprint,
       b.physical_name, b.state AS bucket_state, b.created_at AS bucket_created_at,
       b.updated_at AS bucket_updated_at
FROM object_storage_s3_credentials c
JOIN object_buckets b ON b.id=c.bucket_id AND b.account_id=c.account_id
WHERE c.access_key_id=$1 AND c.status='active' AND b.state='ready'
  AND (b.environment_clone_operation_id IS NULL OR EXISTS (
      SELECT 1 FROM project_environment_clone_operations clone_owner
      WHERE clone_owner.id = b.environment_clone_operation_id
        AND clone_owner.account_id = b.account_id
        AND clone_owner.target_environment = b.scope AND clone_owner.status = 'ready'
  ))

 AND (c.url_request IS NULL OR object_url_issuer_live(c.account_id,c.bucket_id,c.url_api_key_id,c.permission,c.url_expires_at));

-- name: ObjectS3CredentialTouch :execrows
UPDATE object_storage_s3_credentials
SET last_used_at=sqlc.arg(used_at)::timestamptz
WHERE id=sqlc.arg(id) AND status='active'
  AND (last_used_at IS NULL OR last_used_at < sqlc.arg(used_at)::timestamptz - interval '1 minute');

-- name: ObjectS3CredentialListForRekey :many
SELECT * FROM object_storage_s3_credentials
WHERE status='active' AND id > $1 AND (url_request IS NULL OR url_expires_at>clock_timestamp())
ORDER BY id LIMIT sqlc.arg(batch_limit)::int;

-- name: ObjectS3CredentialReseal :execrows
UPDATE object_storage_s3_credentials SET secret_sealed=$4,kid=$3
WHERE id=$1 AND kid=$2 AND status='active';

-- name: SnapshotStorageKeys :many
SELECT storage_key FROM snapshots WHERE deployment_id = $1;

-- name: ExecutionLockAccount :one
SELECT id, plan FROM accounts WHERE id = sqlc.arg(account_id) FOR UPDATE;

-- name: ExecutionCountActive :one
SELECT count(*) FROM executions
WHERE account_id = sqlc.arg(account_id)
  AND status IN ('queued', 'restoring', 'running');

-- name: ExecutionInsert :one
INSERT INTO executions (
  account_id, runtime, profile, status, network_mode, timeout_ms, memory_mb,
  cpu_millicores, ephemeral_disk_mb, max_output_bytes, pids_max,
  source_bytes, input_bytes, deadline_at, created_at, updated_at, runs_principal_id,
  workflow_id, step_label
) VALUES (
  sqlc.arg(account_id), sqlc.arg(runtime), sqlc.arg(profile), 'queued', sqlc.arg(network_mode),
  sqlc.arg(timeout_ms), sqlc.arg(memory_mb), sqlc.arg(cpu_millicores),
  sqlc.arg(ephemeral_disk_mb), sqlc.arg(max_output_bytes), sqlc.arg(pids_max),
  sqlc.arg(source_bytes), sqlc.arg(input_bytes), sqlc.arg(deadline_at),
  sqlc.arg(admitted_at), sqlc.arg(admitted_at), sqlc.narg(runs_principal_id)::uuid,
  sqlc.narg(workflow_id), sqlc.narg(step_label)
)
RETURNING *;

-- name: ExecutionPayloadInsert :exec
INSERT INTO execution_payloads (execution_id, sealed_payload, kid, created_at)
VALUES (sqlc.arg(execution_id), sqlc.arg(sealed_payload), sqlc.arg(kid), sqlc.arg(created_at));

-- name: ExecutionGetForAccount :one
SELECT * FROM executions
WHERE account_id = sqlc.arg(account_id) AND id = sqlc.arg(execution_id);

-- name: ExecutionListForAccount :many
SELECT * FROM executions
WHERE account_id = sqlc.arg(account_id)
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(page_limit)::int OFFSET sqlc.arg(page_offset)::int;

-- name: ExecutionListForAccountPrincipal :many
SELECT * FROM executions
WHERE account_id = sqlc.arg(account_id)
  AND runs_principal_id = sqlc.arg(runs_principal_id)
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(page_limit)::int OFFSET sqlc.arg(page_offset)::int;

-- name: ExecutionListForAccountPrincipalStatus :many
SELECT * FROM executions
WHERE account_id = sqlc.arg(account_id)
  AND runs_principal_id = sqlc.arg(runs_principal_id)
  AND status = sqlc.arg(status)
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(page_limit)::int OFFSET sqlc.arg(page_offset)::int;

-- name: ExecutionListForAccountStatus :many
SELECT * FROM executions
WHERE account_id = sqlc.arg(account_id)
  AND status = sqlc.arg(status)
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(page_limit)::int OFFSET sqlc.arg(page_offset)::int;

-- name: ExecutionListForWorkflow :many
SELECT * FROM executions
WHERE account_id = sqlc.arg(account_id)::uuid
  AND workflow_id = sqlc.arg(workflow_id)::text
  AND (sqlc.narg(runs_principal_id)::uuid IS NULL OR runs_principal_id = sqlc.narg(runs_principal_id)::uuid)
  AND (sqlc.arg(status)::text = '' OR status = sqlc.arg(status)::text)
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(page_limit)::int OFFSET sqlc.arg(page_offset)::int;

-- name: ExecutionWorkflowSummary :one
SELECT count(*)::bigint AS run_count,
       count(*) FILTER (WHERE status = 'queued')::bigint AS queued,
       count(*) FILTER (WHERE status = 'restoring')::bigint AS restoring,
       count(*) FILTER (WHERE status = 'running')::bigint AS running,
       count(*) FILTER (WHERE status = 'succeeded')::bigint AS succeeded,
       count(*) FILTER (WHERE status = 'failed')::bigint AS failed,
       count(*) FILTER (WHERE status = 'timed_out')::bigint AS timed_out,
       count(*) FILTER (WHERE status = 'out_of_memory')::bigint AS out_of_memory,
       count(*) FILTER (WHERE status = 'cancelled')::bigint AS cancelled,
       coalesce(sum(wall_time_ms) FILTER (WHERE status IN ('succeeded', 'failed', 'timed_out', 'out_of_memory', 'cancelled')), 0)::bigint AS wall_time_ms,
       coalesce(sum(cpu_time_ms) FILTER (WHERE status IN ('succeeded', 'failed', 'timed_out', 'out_of_memory', 'cancelled')), 0)::bigint AS cpu_time_ms,
       coalesce(max(peak_memory_mb) FILTER (WHERE status IN ('succeeded', 'failed', 'timed_out', 'out_of_memory', 'cancelled')), 0)::bigint AS peak_memory_mb,
       coalesce(sum(result_bytes + octet_length(stdout) + octet_length(stderr) + octet_length(artifacts)) FILTER (WHERE status IN ('succeeded', 'failed', 'timed_out', 'out_of_memory', 'cancelled')), 0)::bigint AS output_bytes
FROM executions
WHERE account_id = sqlc.arg(account_id)::uuid
  AND workflow_id = sqlc.arg(workflow_id)::text
  AND (sqlc.narg(runs_principal_id)::uuid IS NULL OR runs_principal_id = sqlc.narg(runs_principal_id)::uuid);

-- name: ExecutionClaimNext :one
WITH candidate AS (
  SELECT id, deadline_at
  FROM executions
  WHERE status = 'queued'
    AND cancel_requested_at IS NULL
    AND created_at <= sqlc.arg(claimed_at)::timestamptz
    AND deadline_at > sqlc.arg(claimed_at)::timestamptz
  ORDER BY created_at, id
  FOR UPDATE SKIP LOCKED
  LIMIT 1
)
UPDATE executions AS execution
SET status = 'restoring',
    lease_token = sqlc.arg(lease_token),
    lease_owner = sqlc.arg(lease_owner),
    lease_expires_at = LEAST(sqlc.arg(lease_expires_at)::timestamptz, candidate.deadline_at),
    updated_at = sqlc.arg(claimed_at)
FROM candidate
WHERE execution.id = candidate.id
RETURNING execution.*;

-- name: ExecutionClaimNextForAccount :one
WITH candidate AS (
  SELECT id, deadline_at
  FROM executions
  WHERE executions.account_id = sqlc.arg(account_id)
    AND executions.status = 'queued'
    AND executions.cancel_requested_at IS NULL
    AND executions.created_at <= sqlc.arg(claimed_at)::timestamptz
    AND executions.deadline_at > sqlc.arg(claimed_at)::timestamptz
  ORDER BY executions.created_at, executions.id
  FOR UPDATE SKIP LOCKED
  LIMIT 1
)
UPDATE executions AS execution
SET status = 'restoring',
    lease_token = sqlc.arg(lease_token),
    lease_owner = sqlc.arg(lease_owner),
    lease_expires_at = LEAST(sqlc.arg(lease_expires_at)::timestamptz, candidate.deadline_at),
    updated_at = sqlc.arg(claimed_at)
FROM candidate
WHERE execution.id = candidate.id
RETURNING execution.*;

-- name: ExecutionQueueStats :one
SELECT count(*)::bigint AS queued, min(created_at) AS oldest_created_at
FROM executions
WHERE status = 'queued'
  AND cancel_requested_at IS NULL
  AND created_at <= sqlc.arg(at)::timestamptz
  AND deadline_at > sqlc.arg(at)::timestamptz;

-- name: ExecutionQueueAccounts :many
SELECT executions.account_id, count(*)::bigint AS queued_count, min(executions.created_at) AS oldest_created_at
FROM executions
WHERE executions.status = 'queued'
  AND executions.cancel_requested_at IS NULL
  AND executions.created_at <= sqlc.arg(at)::timestamptz
  AND executions.deadline_at > sqlc.arg(at)::timestamptz
GROUP BY executions.account_id
ORDER BY executions.account_id
LIMIT sqlc.arg(page_limit)::int;

-- name: ExecutionPayloadForLease :one
SELECT payload.execution_id, payload.sealed_payload, payload.kid, payload.created_at
FROM execution_payloads AS payload
JOIN executions AS execution ON execution.id = payload.execution_id
WHERE payload.execution_id = sqlc.arg(execution_id)
  AND execution.status = 'restoring'
  AND execution.lease_token = sqlc.arg(lease_token);

-- name: ExecutionMarkRunning :one
UPDATE executions
SET status = 'running', started_at = sqlc.arg(started_at), updated_at = sqlc.arg(started_at)
WHERE id = sqlc.arg(execution_id)
  AND status = 'restoring'
  AND lease_token = sqlc.arg(lease_token)
  AND lease_expires_at > sqlc.arg(started_at)
  AND cancel_requested_at IS NULL
  AND deadline_at > sqlc.arg(started_at)
  AND (profile = 'standard' OR runtime_image_digest IS NOT NULL)
RETURNING *;

-- name: ExecutionPinRuntime :one
UPDATE executions
SET runtime_image_digest = sqlc.arg(image_digest), updated_at = sqlc.arg(pinned_at)
WHERE id = sqlc.arg(execution_id) AND status = 'restoring'
  AND lease_token = sqlc.arg(lease_token)
  AND lease_expires_at > sqlc.arg(pinned_at) AND deadline_at > sqlc.arg(pinned_at)
  AND cancel_requested_at IS NULL
  AND (runtime_image_digest IS NULL OR runtime_image_digest = sqlc.arg(image_digest))
RETURNING *;

-- name: ExecutionLockForLease :one
SELECT * FROM executions
WHERE id = sqlc.arg(execution_id)
  AND status IN ('restoring', 'running')
  AND lease_token = sqlc.arg(lease_token)
FOR UPDATE;

-- name: ExecutionRenewLease :execrows
UPDATE executions
SET lease_expires_at = LEAST(sqlc.arg(lease_expires_at)::timestamptz, deadline_at),
    updated_at = sqlc.arg(renewed_at)
WHERE id = sqlc.arg(execution_id)
  AND status IN ('restoring', 'running')
  AND lease_token = sqlc.arg(lease_token)
  AND lease_expires_at > sqlc.arg(renewed_at)
  AND cancel_requested_at IS NULL
  AND deadline_at > sqlc.arg(renewed_at);

-- name: ExecutionMarkTerminal :one
UPDATE executions
SET status = sqlc.arg(terminal_status),
    lease_token = NULL,
    lease_owner = NULL,
    lease_expires_at = NULL,
    result = NULLIF(sqlc.arg(result_json)::text, '')::jsonb,
    result_bytes = sqlc.arg(result_bytes),
    artifacts = sqlc.arg(artifacts),
    stdout = sqlc.arg(stdout),
    stderr = sqlc.arg(stderr),
    output_truncated = sqlc.arg(output_truncated),
    exit_code = sqlc.narg(exit_code),
    failure_code = NULLIF(sqlc.arg(failure_code), ''),
    failure_message = NULLIF(sqlc.arg(failure_message), ''),
    wall_time_ms = sqlc.arg(wall_time_ms),
    cpu_time_ms = sqlc.arg(cpu_time_ms),
    peak_memory_mb = sqlc.arg(peak_memory_mb),
    finished_at = sqlc.arg(finished_at),
    updated_at = sqlc.arg(finished_at)
WHERE id = sqlc.arg(execution_id)
  AND status IN ('restoring', 'running')
  AND lease_token = sqlc.arg(lease_token)
RETURNING *;

-- name: ExecutionLockForAccount :one
SELECT * FROM executions
WHERE account_id = sqlc.arg(account_id) AND id = sqlc.arg(execution_id)
FOR UPDATE;

-- name: ExecutionRequestCancel :one
UPDATE executions
SET status = CASE WHEN status = 'queued' THEN 'cancelled' ELSE status END,
    cancel_requested_at = COALESCE(cancel_requested_at, sqlc.arg(requested_at)),
    finished_at = CASE WHEN status = 'queued' THEN sqlc.arg(requested_at) ELSE finished_at END,
    updated_at = sqlc.arg(requested_at)
WHERE id = sqlc.arg(execution_id)
  AND status IN ('queued', 'restoring', 'running')
RETURNING *;

-- name: ExecutionExpireQueued :many
WITH candidates AS (
  SELECT id
  FROM executions
  WHERE status = 'queued' AND deadline_at <= sqlc.arg(sweep_at)::timestamptz
  ORDER BY deadline_at, id
  FOR UPDATE SKIP LOCKED
  LIMIT sqlc.arg(batch_limit)::int
)
UPDATE executions AS execution
SET status = 'timed_out', finished_at = sqlc.arg(sweep_at), updated_at = sqlc.arg(sweep_at),
    failure_code = 'deadline_exceeded', failure_message = 'execution deadline elapsed before dispatch'
FROM candidates
WHERE execution.id = candidates.id
RETURNING execution.*;

-- name: ExecutionRequeueExpiredRestores :many
WITH candidates AS (
  SELECT id
  FROM executions
  WHERE status = 'restoring'
    AND lease_expires_at <= sqlc.arg(sweep_at)::timestamptz
    AND deadline_at > sqlc.arg(sweep_at)::timestamptz
    AND cancel_requested_at IS NULL
  ORDER BY lease_expires_at, id
  FOR UPDATE SKIP LOCKED
  LIMIT sqlc.arg(batch_limit)::int
)
UPDATE executions AS execution
SET status = 'queued', lease_token = NULL, lease_owner = NULL, lease_expires_at = NULL,
    updated_at = sqlc.arg(sweep_at)
FROM candidates
WHERE execution.id = candidates.id
RETURNING execution.*;

-- name: ExecutionFinishExpiredRestores :many
WITH candidates AS (
  SELECT id
  FROM executions
  WHERE status = 'restoring'
    AND lease_expires_at <= sqlc.arg(sweep_at)::timestamptz
    AND (deadline_at <= sqlc.arg(sweep_at)::timestamptz OR cancel_requested_at IS NOT NULL)
  ORDER BY lease_expires_at, id
  FOR UPDATE SKIP LOCKED
  LIMIT sqlc.arg(batch_limit)::int
)
UPDATE executions AS execution
SET status = CASE WHEN cancel_requested_at IS NOT NULL THEN 'cancelled' ELSE 'timed_out' END,
    lease_token = NULL, lease_owner = NULL, lease_expires_at = NULL,
    failure_code = CASE WHEN cancel_requested_at IS NULL THEN 'deadline_exceeded' ELSE NULL END,
    failure_message = CASE WHEN cancel_requested_at IS NULL THEN 'execution deadline elapsed during restore' ELSE NULL END,
    finished_at = sqlc.arg(sweep_at), updated_at = sqlc.arg(sweep_at)
FROM candidates
WHERE execution.id = candidates.id
RETURNING execution.*;

-- name: ExecutionFinishExpiredRuns :many
WITH candidates AS (
  SELECT id
  FROM executions
  WHERE status = 'running' AND lease_expires_at <= sqlc.arg(sweep_at)::timestamptz
  ORDER BY lease_expires_at, id
  FOR UPDATE SKIP LOCKED
  LIMIT sqlc.arg(batch_limit)::int
)
UPDATE executions AS execution
SET status = CASE
      WHEN cancel_requested_at IS NOT NULL THEN 'cancelled'
      WHEN deadline_at <= sqlc.arg(sweep_at)::timestamptz THEN 'timed_out'
      ELSE 'failed'
    END,
    lease_token = NULL, lease_owner = NULL, lease_expires_at = NULL,
    failure_code = CASE
      WHEN cancel_requested_at IS NOT NULL THEN NULL
      WHEN deadline_at <= sqlc.arg(sweep_at)::timestamptz THEN 'deadline_exceeded'
      ELSE 'lease_expired'
    END,
    failure_message = CASE
      WHEN cancel_requested_at IS NOT NULL THEN NULL
      WHEN deadline_at <= sqlc.arg(sweep_at)::timestamptz THEN 'execution deadline elapsed while running'
      ELSE 'scheduler lease expired after execution dispatch'
    END,
    finished_at = sqlc.arg(sweep_at), updated_at = sqlc.arg(sweep_at)
FROM candidates
WHERE execution.id = candidates.id
RETURNING execution.*;

-- name: ExecutionPayloadDelete :execrows
DELETE FROM execution_payloads WHERE execution_id = sqlc.arg(execution_id);

-- name: ExecutionPayloadDeleteMany :execrows
DELETE FROM execution_payloads WHERE execution_id = ANY(sqlc.arg(execution_ids)::uuid[]);

-- name: ExecutionPayloadDeleteTerminal :execrows
WITH candidates AS (
  SELECT payload.execution_id
  FROM execution_payloads AS payload
  JOIN executions AS execution ON execution.id = payload.execution_id
  WHERE execution.status IN ('succeeded', 'failed', 'timed_out', 'out_of_memory', 'cancelled')
  ORDER BY payload.created_at, payload.execution_id
  FOR UPDATE OF payload SKIP LOCKED
  LIMIT sqlc.arg(batch_limit)::int
)
DELETE FROM execution_payloads AS payload
USING candidates
WHERE payload.execution_id = candidates.execution_id;

-- name: ExecutionUsageRecord :exec
-- The execution ID is the idempotency key. Recording from the terminal
-- execution row keeps usage and the lifecycle projection in lockstep and
-- makes retries/recovery harmless.
INSERT INTO execution_usage_ledger (
    execution_id, account_id, runtime, status, wall_time_ms, cpu_time_ms,
    peak_memory_mb, output_bytes, started_at, finished_at, created_at
)
SELECT id, account_id, runtime, status, wall_time_ms, cpu_time_ms,
       peak_memory_mb,
       (result_bytes + octet_length(stdout) + octet_length(stderr) + octet_length(artifacts))::bigint,
       started_at, finished_at, created_at
FROM executions
WHERE id = sqlc.arg(execution_id)
  AND status IN ('succeeded', 'failed', 'timed_out', 'out_of_memory', 'cancelled')
ON CONFLICT (execution_id) DO NOTHING;

-- name: ExecutionUsageByAccount :one
SELECT
    count(*)::bigint AS runs,
    COALESCE(sum(wall_time_ms), 0)::bigint AS wall_time_ms,
    COALESCE(sum(cpu_time_ms), 0)::bigint AS cpu_time_ms,
    COALESCE(max(peak_memory_mb), 0)::bigint AS peak_memory_mb,
    COALESCE(sum(output_bytes), 0)::bigint AS output_bytes,
    count(*) FILTER (WHERE status = 'succeeded')::bigint AS succeeded,
    count(*) FILTER (WHERE status = 'failed')::bigint AS failed,
    count(*) FILTER (WHERE status = 'timed_out')::bigint AS timed_out,
    count(*) FILTER (WHERE status = 'out_of_memory')::bigint AS out_of_memory,
    count(*) FILTER (WHERE status = 'cancelled')::bigint AS cancelled
FROM execution_usage_ledger
WHERE account_id = sqlc.arg(account_id)
  AND finished_at >= sqlc.arg(month_start)::timestamptz
  AND finished_at < sqlc.arg(month_end)::timestamptz;

-- Runtime snapshot catalog (ADR-171 follow-up / durable publication boundary).
-- Publication is insert-only; retirement is the sole mutable transition.
-- name: RuntimeSnapshotInsert :one
INSERT INTO runtime_snapshots (
    catalog_key, runtime, profile, architecture, kernel_digest, guest_executor_digest,
    base_image_digest, memory_mb, ephemeral_disk_mb, format_version,
    storage_key, snapshot_digest, mem_bytes, vm_state_bytes, sanitized,
    payload_free, state, created_at, published_at, retired_at
)
VALUES (
    sqlc.arg(catalog_key), sqlc.arg(runtime), sqlc.arg(profile), sqlc.arg(architecture),
    sqlc.arg(kernel_digest), sqlc.arg(guest_executor_digest),
    sqlc.arg(base_image_digest), sqlc.arg(memory_mb), sqlc.arg(ephemeral_disk_mb),
    sqlc.arg(format_version), sqlc.arg(storage_key), sqlc.arg(snapshot_digest),
    sqlc.arg(mem_bytes), sqlc.arg(vm_state_bytes), sqlc.arg(sanitized),
    sqlc.arg(payload_free), sqlc.arg(state), sqlc.arg(created_at),
    sqlc.arg(published_at), sqlc.arg(retired_at)
)
RETURNING *;

-- name: RuntimeSnapshotByCatalogKey :one
SELECT * FROM runtime_snapshots
WHERE catalog_key = sqlc.arg(catalog_key);

-- name: RuntimeSnapshotRetire :execrows
UPDATE runtime_snapshots
SET state = 'retired', retired_at = sqlc.arg(retired_at)
WHERE catalog_key = sqlc.arg(catalog_key) AND state = 'ready';

-- name: ListEgressCircuitCandidates :many
-- schedd's egress circuit-breaker feed (ADR-201 §3). Returns every
-- opted-in upstream joined to its NEWEST probe verdict, which is the
-- complete input the breaker loop needs for one reconcile pass.
--
-- Only circuit_breaker_enabled rows are considered, so the scan is
-- served by data_upstreams_circuit_enabled_idx (a partial index) and
-- stays proportional to the opt-in count rather than to the whole
-- data_upstreams table, which grows with every captured env var on
-- every app.
--
-- LEFT JOIN, not INNER: an opted-in upstream that has never been
-- probed must still appear, carrying a NULL sampled_at. Dropping it
-- here would make "never probed" indistinguishable from "row gone",
-- and the loop needs the difference — it skips unprobed upstreams but
-- must still count them as live candidates so their dedupe state is
-- not retired out from under them.
--
-- DISTINCT ON picks one row per upstream: the probe table holds one
-- sample per 30s per (host, region), so without it a single upstream
-- would fan out to every sample in the retention window.
--
-- host is projected because schedd resolves it locally to write the
-- nftables element. It never reaches a metric label, a log line, or
-- the customer-facing API — those carry host_redacted_hash only
-- (ADR-098 §11).
SELECT DISTINCT ON (u.app_id, u.host_redacted_hash, u.port)
    u.app_id,
    u.host_redacted_hash,
    u.host,
    u.port,
    u.circuit_breaker_failure_threshold,
    u.circuit_breaker_min_samples,
    u.circuit_breaker_open_seconds,
    p.ok,
    p.sampled_at
FROM data_upstreams u
LEFT JOIN data_upstream_probes p
    ON p.host_redacted_hash = u.host_redacted_hash
   AND p.sampled_at >= $1
WHERE u.circuit_breaker_enabled
ORDER BY u.app_id, u.host_redacted_hash, u.port, p.sampled_at DESC NULLS LAST;

-- name: ListDeploymentAliases :many
-- Stable per-app revision names. Join deployments for the human-readable
-- revision while retaining aliases whose targets later become superseded;
-- the alias continues to identify the same immutable row.
SELECT a.app_id, a.name, a.deployment_id, d.revision, a.created_at, a.updated_at
  FROM deployment_aliases a
  JOIN deployments d ON d.id = a.deployment_id AND d.app_id = a.app_id
 WHERE a.app_id = sqlc.arg(app_id)
 ORDER BY a.name;

-- name: DeploymentAliasByHostLabel :many
-- The hostname label uses the app's immutable UUID so aliases remain stable
-- across app slug renames. Keep the deployment join app-scoped and hide
-- soft-deleted owners/targets.
SELECT a.app_id, a.name, a.deployment_id, d.revision, a.created_at, a.updated_at
  FROM deployment_aliases a
  JOIN apps p ON p.id = a.app_id
             AND p.status <> 'deleted'
             AND p.deleted_at IS NULL
  JOIN deployments d ON d.id = a.deployment_id
                    AND d.app_id = a.app_id
                    AND d.deleted_at IS NULL
 WHERE ('tag-' || a.name || '-' || replace(a.app_id::text, '-', ''))
       = sqlc.arg(host_label)
 ORDER BY a.app_id, a.name;

-- name: UpsertDeploymentAlias :one
-- Accept only a routable target on this app. Using INSERT .. SELECT makes the
-- ownership/status check atomic with writing the alias.
WITH upserted AS (
    INSERT INTO deployment_aliases (app_id, name, deployment_id)
    SELECT d.app_id, sqlc.arg(name), d.id
      FROM deployments d
      JOIN apps a ON a.id = d.app_id
     WHERE d.app_id = sqlc.arg(app_id)
       AND d.id = sqlc.arg(deployment_id)
       AND a.deleted_at IS NULL
       AND a.status <> 'deleted'
       AND d.deleted_at IS NULL
       AND d.status IN ('pending', 'building', 'imaging', 'snapshotting', 'live')
    ON CONFLICT (app_id, name) DO UPDATE
       SET deployment_id = EXCLUDED.deployment_id,
           updated_at = now()
    RETURNING app_id, name, deployment_id, created_at, updated_at
)
SELECT u.app_id, u.name, u.deployment_id, d.revision, u.created_at, u.updated_at
  FROM upserted u
  JOIN deployments d ON d.id = u.deployment_id;

-- name: DeleteDeploymentAlias :execrows
DELETE FROM deployment_aliases
 WHERE app_id = sqlc.arg(app_id) AND name = sqlc.arg(name);

-- name: UpdateDataUpstreamCircuitBreaker :exec
-- ADR-201 §3 per-upstream egress-breaker policy. Each field uses the
-- COALESCE(sqlc.narg, existing) shape so a PATCH that omits a field
-- leaves it untouched — the same partial-update convention the app
-- PATCH paths use.
--
-- The threshold fields are deliberately NOT cleared when enabled flips
-- to false: a customer toggling protection off should not silently lose
-- their tuning, and re-enabling should restore what they configured.
UPDATE data_upstreams
SET circuit_breaker_enabled           = COALESCE(sqlc.narg('circuit_breaker_enabled')::boolean, circuit_breaker_enabled),
    circuit_breaker_failure_threshold = COALESCE(sqlc.narg('circuit_breaker_failure_threshold')::double precision, circuit_breaker_failure_threshold),
    circuit_breaker_min_samples       = COALESCE(sqlc.narg('circuit_breaker_min_samples')::integer, circuit_breaker_min_samples),
    circuit_breaker_open_seconds      = COALESCE(sqlc.narg('circuit_breaker_open_seconds')::integer, circuit_breaker_open_seconds)
WHERE id = $1 AND app_id = $2;

-- name: LatestInstanceReadiness :many
-- Gateway restart hydration: readiness is independent of the instance's
-- RUNNING state, so replay only the latest reversible ready/unready event.
SELECT DISTINCT ON (CAST(data->>'instance_id' AS text))
       CAST(data->>'instance_id' AS text) AS instance_id,
       CAST(data->>'status' AS text) AS status,
       at,
       id
FROM events
WHERE kind IN ('wake.sidecar_health', 'wake.app_readiness')
  AND data->>'status' IN ('ready', 'unready')
  AND data->>'instance_id' = ANY(sqlc.arg(instance_ids)::text[])
ORDER BY CAST(data->>'instance_id' AS text), at DESC, id DESC;

-- name: ReadInvocationPinScope :one
SELECT d.scope::text AS scope
FROM deployments d JOIN apps a ON a.id = d.app_id
WHERE a.id = sqlc.arg(app_id)::uuid AND a.status <> 'deleted'
  AND d.id = sqlc.narg(revision_id)::uuid
UNION ALL
SELECT rs.environment_slug::text AS scope
FROM project_release_sets rs JOIN apps a
  ON a.project_id = rs.project_id AND a.account_id = rs.account_id
WHERE a.id = sqlc.arg(app_id)::uuid AND a.status <> 'deleted'
  AND rs.id = sqlc.narg(release_id)::uuid;

-- name: ReadProjectReleaseSet :one
-- A single statement reads the pointer and its complete membership together.
SELECT (to_jsonb(rs) || jsonb_build_object('environment', rs.environment_slug,
        'members', COALESCE((SELECT jsonb_agg(jsonb_build_object(
            'app_id', rm.app_id, 'deployment_id', rm.deployment_id) ORDER BY rm.app_id)
            FROM project_release_members rm WHERE rm.release_id = rs.id), '[]'::jsonb)))::jsonb AS release
  FROM project_release_sets rs
  JOIN projects p ON p.id = rs.project_id AND p.account_id = rs.account_id
 WHERE rs.account_id = sqlc.arg(account_id) AND rs.project_id = sqlc.arg(project_id)
   AND rs.environment_slug = sqlc.arg(environment)
   AND ((sqlc.narg(release_id)::uuid IS NULL AND rs.active)
     OR rs.id = sqlc.narg(release_id)::uuid);

-- name: ListProjectReleaseSetsBefore :many
-- Retired and expired graphs remain visible for diagnosis. UUID breaks ties.
SELECT (to_jsonb(rs) || jsonb_build_object('environment', rs.environment_slug,
        'members', COALESCE((SELECT jsonb_agg(jsonb_build_object(
            'app_id', rm.app_id, 'deployment_id', rm.deployment_id) ORDER BY rm.app_id)
            FROM project_release_members rm WHERE rm.release_id = rs.id), '[]'::jsonb)))::jsonb AS release
  FROM project_release_sets rs
  JOIN projects p ON p.id = rs.project_id AND p.account_id = rs.account_id
 WHERE rs.account_id = sqlc.arg(account_id) AND rs.project_id = sqlc.arg(project_id)
   AND rs.environment_slug = sqlc.arg(environment)
   AND (sqlc.narg(before_at)::timestamptz IS NULL
     OR (rs.created_at, rs.id) < (sqlc.narg(before_at)::timestamptz, sqlc.narg(before_id)::uuid))
 ORDER BY rs.created_at DESC, rs.id DESC
 LIMIT sqlc.arg(page_limit);
-- name: LatestInstanceReadinessBySource :many
-- Gateway hydration keeps each required readiness source independent so one
-- recovered probe cannot override another probe that is still unready.
SELECT DISTINCT ON (
           CAST(data->>'instance_id' AS text),
           CAST(CASE WHEN kind = 'wake.app_readiness' THEN 'primary_app'
                ELSE 'sidecar:' || CAST(data->>'sidecar_name' AS text) END AS text)
       )
       CAST(data->>'instance_id' AS text) AS instance_id,
       CAST(CASE WHEN kind = 'wake.app_readiness' THEN 'primary_app'
            ELSE 'sidecar:' || CAST(data->>'sidecar_name' AS text) END AS text) AS source,
       CAST(data->>'status' AS text) AS status,
       at,
       id
FROM events
WHERE kind IN ('wake.sidecar_health', 'wake.app_readiness')
  AND data->>'status' IN ('ready', 'unready')
  AND data->>'instance_id' = ANY(sqlc.arg(instance_ids)::text[])
  AND (kind <> 'wake.sidecar_health' OR COALESCE(data->>'sidecar_name', '') <> '')
ORDER BY CAST(data->>'instance_id' AS text), source, at DESC, id DESC;

-- name: StampSafeReleaseWorkerLease :exec
INSERT INTO safe_release_worker_lease (singleton, healthy_at, expires_at)
VALUES (true, now(), now() + (sqlc.arg(ttl_seconds)::bigint * interval '1 second'))
ON CONFLICT (singleton) DO UPDATE SET
    healthy_at = EXCLUDED.healthy_at,
    expires_at = EXCLUDED.expires_at;

-- name: SafeReleaseWorkerLeaseReady :one
SELECT EXISTS(SELECT 1 FROM safe_release_worker_lease
              WHERE singleton = true AND expires_at > now()) AS ready;

-- name: RecordRequestIDJournal :one
-- The request-ID journal is independent from sampled request telemetry. Only
-- insert when the app is still owned by the authenticated account. The
-- caller-generated record UUID makes an RPC retry idempotent without
-- collapsing two customer requests that happen to reuse a public ID.
INSERT INTO request_id_journal (
    id, account_id, app_id, request_id, trace_id, received_at, expires_at
)
SELECT sqlc.arg(id)::uuid,
       a.account_id,
       a.id,
       sqlc.arg(request_id)::text,
       NULLIF(sqlc.arg(trace_id)::text, ''),
       sqlc.arg(received_at)::timestamptz,
       sqlc.arg(expires_at)::timestamptz
  FROM apps a
 WHERE a.id = sqlc.arg(app_id)::uuid
   AND a.account_id = sqlc.arg(account_id)::uuid
ON CONFLICT (id) DO UPDATE SET id = EXCLUDED.id
 WHERE request_id_journal.account_id = EXCLUDED.account_id
   AND request_id_journal.app_id = EXCLUDED.app_id
   AND request_id_journal.request_id = EXCLUDED.request_id
   AND request_id_journal.trace_id IS NOT DISTINCT FROM EXCLUDED.trace_id
   AND request_id_journal.received_at = EXCLUDED.received_at
   AND request_id_journal.expires_at = EXCLUDED.expires_at
RETURNING id;

-- name: GetRequestIDJournalByAppAndIdentifier :one
-- Exact app/account-scoped lookup, latest first when callers reuse an ID.
-- expires_at is checked as well as received_at so plan downgrades do not
-- extend the original request-time retention window.
SELECT id, request_id, trace_id, received_at, expires_at
  FROM request_id_journal
 WHERE account_id = sqlc.arg(account_id)::uuid
   AND app_id = sqlc.arg(app_id)::uuid
   AND request_id = sqlc.arg(request_id)::text
   AND received_at >= sqlc.arg(received_from)::timestamptz
   AND received_at < sqlc.arg(received_until)::timestamptz
   AND expires_at > sqlc.arg(now_at)::timestamptz
 ORDER BY received_at DESC, id DESC
 LIMIT 1;
-- name: LockProjectEnvironmentCloneProject :one
SELECT id::text FROM projects
WHERE id = sqlc.arg(project_id)::uuid AND account_id = sqlc.arg(account_id)::uuid
FOR UPDATE;

-- name: ReadProjectEnvironmentCloneWorkloads :many
SELECT w.app_id::text AS app_id, w.source_deployment_id::text AS source_deployment_id, w.source_hash, w.snapshot,
       coalesce(w.target_deployment_id::text, '')::text AS target_deployment_id, w.target_settings_hash
FROM project_environment_clone_workloads w
JOIN project_environment_clone_operations o ON o.id = w.operation_id
WHERE o.id = sqlc.arg(operation_id)::uuid AND o.account_id = sqlc.arg(account_id)::uuid
  AND o.project_id = sqlc.arg(project_id)::uuid ORDER BY w.app_id;

-- name: ReadProjectEnvironmentCloneProjectConfiguration :one
SELECT id::text, config_hash, config_json FROM project_environment_config_versions
WHERE account_id = sqlc.arg(account_id)::uuid AND project_id = sqlc.arg(project_id)::uuid
  AND environment_slug = sqlc.arg(environment)::text ORDER BY version DESC LIMIT 1;

-- name: InsertProjectEnvironmentCloneProjectConfiguration :execrows
INSERT INTO project_environment_config_versions (account_id, project_id, environment_slug, version, config_hash, config_json)
VALUES (sqlc.arg(account_id)::uuid, sqlc.arg(project_id)::uuid, sqlc.arg(environment)::text,
        1, sqlc.arg(config_hash)::text, sqlc.arg(config_json)::jsonb);

-- name: InsertProjectEnvironmentCloneWorkload :exec
INSERT INTO project_environment_clone_workloads (operation_id, app_id, source_deployment_id, source_hash, snapshot)
VALUES (sqlc.arg(operation_id)::uuid, sqlc.arg(app_id)::uuid, sqlc.arg(source_deployment_id)::uuid,
        sqlc.arg(source_hash)::text, sqlc.arg(snapshot)::json);

-- name: AttachProjectEnvironmentCloneDeployment :execrows
UPDATE project_environment_clone_workloads SET target_deployment_id = sqlc.arg(deployment_id)::uuid,
       target_settings_hash = sqlc.arg(settings_hash)::text
WHERE operation_id = sqlc.arg(operation_id)::uuid AND app_id = sqlc.arg(app_id)::uuid AND target_deployment_id IS NULL;

-- name: ReadProjectEnvironmentCloneSelectedArtifact :one
SELECT (to_jsonb(d) || jsonb_build_object('secret_reload_signal_known', d.secret_reload_signal IS NOT NULL))::jsonb AS artifact
FROM deployments d
WHERE d.app_id = sqlc.arg(app_id)::uuid AND d.status = 'live'
  AND (CASE WHEN sqlc.arg(release_id)::text <> '' THEN d.id =
       (SELECT deployment_id FROM project_release_members WHERE release_id = nullif(sqlc.arg(release_id)::text, '')::uuid AND app_id = d.app_id)
       ELSE d.scope = sqlc.arg(source_scope)::text AND d.traffic_percent > 0 END)
ORDER BY d.created_at DESC, d.id DESC LIMIT 1;

-- name: ReadProjectEnvironmentCloneTargetArtifact :one
SELECT (to_jsonb(d) || jsonb_build_object('secret_reload_signal_known', d.secret_reload_signal IS NOT NULL))::jsonb AS artifact
FROM deployments d WHERE d.id = sqlc.arg(deployment_id)::uuid;

-- name: ReadProjectEnvironmentCloneDeployedSettings :one
SELECT s.settings, s.config_hash FROM project_environment_workload_specs s
JOIN project_environment_workload_deployment_specs p ON p.spec_id = s.id
WHERE p.deployment_id = sqlc.arg(deployment_id)::uuid;

-- name: ReadProjectEnvironmentCloneLegacySettings :one
SELECT (to_jsonb(a) || jsonb_build_object('public_auth_basic_sealed', encode(a.public_auth_basic, 'base64'),
       'only_allow_declared_routes', a.only_declared_routes, 'retry_policy_json', a.retry_policy))::jsonb AS app, coalesce(to_jsonb(r), '{}'::jsonb)::jsonb AS route
FROM apps a LEFT JOIN project_environment_route_policies r ON r.app_id = a.id AND r.environment_slug = sqlc.arg(environment)::text
WHERE a.id = sqlc.arg(app_id)::uuid;

-- name: ReadProjectEnvironmentCloneSourceRelease :one
SELECT coalesce((SELECT id::text FROM project_release_sets WHERE project_id = sqlc.arg(project_id)::uuid
       AND environment_slug = sqlc.arg(environment)::text AND active), '')::text AS release_id;

-- name: ReadProjectEnvironmentCloneSidecarLayers :many
SELECT to_jsonb(l)::jsonb AS layer FROM deployment_sidecar_layers l
WHERE deployment_id = sqlc.arg(deployment_id)::uuid ORDER BY sidecar_name;

-- name: ReadProjectEnvironmentCloneSidecarSignals :many
SELECT sidecar_name, signal FROM deployment_sidecar_secret_reload_signals
WHERE deployment_id = sqlc.arg(deployment_id)::uuid ORDER BY sidecar_name;

-- name: SetProjectEnvironmentCloneDeploymentArtifact :exec
UPDATE deployments SET rootfs_path = nullif(sqlc.arg(rootfs_path)::text, ''), rootfs_key = nullif(sqlc.arg(rootfs_key)::text, ''),
       rootfs_bytes = sqlc.arg(rootfs_bytes)::bigint,
       secret_reload_signal = CASE WHEN sqlc.arg(signal_known)::boolean THEN sqlc.arg(signal)::text ELSE NULL END
WHERE id = sqlc.arg(deployment_id)::uuid;

-- name: InsertProjectEnvironmentCloneSidecarLayer :exec
INSERT INTO deployment_sidecar_layers (deployment_id, sidecar_name, storage_key, bytes, content_digest)
VALUES (sqlc.arg(deployment_id)::uuid, sqlc.arg(sidecar_name)::text, sqlc.arg(storage_key)::text,
        sqlc.arg(bytes)::bigint, sqlc.arg(content_digest)::text);

-- name: InsertProjectEnvironmentCloneSidecarSignal :exec
INSERT INTO deployment_sidecar_secret_reload_signals (deployment_id, sidecar_name, signal)
VALUES (sqlc.arg(deployment_id)::uuid, sqlc.arg(sidecar_name)::text, sqlc.arg(signal)::text);

-- name: LockProjectEnvironmentCloneWorkloadOperation :one
SELECT source_environment, target_environment, source_revision_hash,
       coalesce(source_release_set_id::text, '')::text AS source_release_set_id, status, revision,
       resources, error_code, coalesce(target_release_set_id::text, '')::text AS target_release_set_id,
       (lease_token IS NULL OR lease_until > clock_timestamp())::boolean AS lease_live
FROM project_environment_clone_operations
WHERE id = sqlc.arg(operation_id)::uuid AND account_id = sqlc.arg(account_id)::uuid AND project_id = sqlc.arg(project_id)::uuid
FOR UPDATE;

-- name: ReadProjectEnvironmentCloneTargetOperationID :one
SELECT id::text FROM project_environment_clone_operations
WHERE account_id = sqlc.arg(account_id)::uuid AND project_id = sqlc.arg(project_id)::uuid
  AND target_environment = sqlc.arg(environment)::text AND status <> 'compensated'
ORDER BY created_at DESC, id DESC LIMIT 1;

-- name: LockProjectEnvironmentCloneTargetDeployments :many
SELECT d.id::text FROM deployments d JOIN project_environment_clone_workloads w ON w.target_deployment_id = d.id
WHERE w.operation_id = sqlc.arg(operation_id)::uuid ORDER BY d.id FOR UPDATE OF d;

-- name: CompleteProjectEnvironmentClonePublication :execrows
UPDATE project_environment_clone_operations
SET status = 'ready', revision = revision + 1, target_release_set_id = sqlc.arg(release_id)::uuid, updated_at = now(),
    lease_token = NULL, lease_until = NULL
WHERE id = sqlc.arg(operation_id)::uuid AND status = 'publishing' AND revision = sqlc.arg(revision)::bigint
  AND NOT EXISTS (SELECT 1 FROM object_bucket_write_fences f WHERE f.clone_operation_id = project_environment_clone_operations.id)
  AND NOT EXISTS (SELECT 1 FROM project_environment_clone_postgres_write_fences f WHERE f.operation_id = project_environment_clone_operations.id AND f.state<>'released')
  AND ((attempt_count = 0 AND lease_token IS NULL)
    OR (lease_token IS NOT NULL AND lease_until > clock_timestamp()));

-- name: LockProjectEnvironmentCloneTargetSidecarLayers :many
SELECT l.sidecar_name FROM deployment_sidecar_layers l
JOIN project_environment_clone_workloads w ON w.target_deployment_id = l.deployment_id
WHERE w.operation_id = sqlc.arg(operation_id)::uuid ORDER BY l.deployment_id, l.sidecar_name FOR UPDATE OF l;

-- name: LockProjectEnvironmentCloneTargetSidecarSignals :many
SELECT s.sidecar_name FROM deployment_sidecar_secret_reload_signals s
JOIN project_environment_clone_workloads w ON w.target_deployment_id = s.deployment_id
WHERE w.operation_id = sqlc.arg(operation_id)::uuid ORDER BY s.deployment_id, s.sidecar_name FOR UPDATE OF s;

-- name: ReadProjectEnvironmentCloneTargetSettings :one
SELECT s.settings, s.config_hash FROM project_environment_workload_specs s
JOIN project_environment_workload_heads h ON h.spec_id = s.id
JOIN project_environments e ON e.id = h.environment_id
JOIN apps a ON a.id = h.app_id AND a.account_id = e.account_id AND a.project_id = e.project_id
WHERE e.account_id = sqlc.arg(account_id)::uuid AND e.project_id = sqlc.arg(project_id)::uuid
  AND e.slug = sqlc.arg(environment)::text AND a.id = sqlc.arg(app_id)::uuid
  AND a.status <> 'deleted' AND a.preview_of_slug IS NULL;

-- name: ReadProjectEnvironmentCloneOwnedApp :one
SELECT id::text FROM apps
WHERE id = sqlc.arg(app_id)::uuid AND account_id = sqlc.arg(account_id)::uuid AND project_id = sqlc.arg(project_id)::uuid
  AND status <> 'deleted' AND preview_of_slug IS NULL;

-- name: ReadProjectEnvironmentCloneEnvironmentPresence :one
SELECT EXISTS(SELECT 1 FROM project_environments
              WHERE project_id = sqlc.arg(project_id)::uuid
                AND slug = sqlc.arg(source_environment)::text)::boolean AS source_exists,
       EXISTS(SELECT 1 FROM project_environments
              WHERE project_id = sqlc.arg(project_id)::uuid
                AND slug = sqlc.arg(target_environment)::text)::boolean AS target_exists;

-- name: LockProjectEnvironmentCloneTargetReservation :one
SELECT id::text, revision, status, source_environment,
       (lease_token IS NULL OR lease_until > clock_timestamp())::boolean AS lease_live
FROM project_environment_clone_operations
WHERE project_id = sqlc.arg(project_id)::uuid AND account_id = sqlc.arg(account_id)::uuid
  AND target_environment = sqlc.arg(target_environment)::text
  AND status IN ('pending', 'capturing', 'copying', 'publishing', 'failed', 'compensating')
FOR UPDATE;

-- name: LockNextProjectEnvironmentCloneWorkerProject :one
-- All clone mutations acquire the project row before the operation row.
-- Skip projects held by another transaction instead of reversing that order.
SELECT p.id::text AS project_id, p.account_id::text AS account_id
FROM projects p
WHERE EXISTS (
    SELECT 1 FROM project_environment_clone_operations o WHERE o.project_id = p.id
      AND o.status IN ('pending', 'capturing', 'copying', 'publishing', 'compensating')
      AND o.next_attempt_at <= clock_timestamp() AND (o.lease_until IS NULL OR o.lease_until <= clock_timestamp())
)
ORDER BY (SELECT min(o.next_attempt_at) FROM project_environment_clone_operations o WHERE o.project_id = p.id
      AND o.status IN ('pending', 'capturing', 'copying', 'publishing', 'compensating')
      AND o.next_attempt_at <= clock_timestamp() AND (o.lease_until IS NULL OR o.lease_until <= clock_timestamp())), p.id
LIMIT 1 FOR UPDATE OF p SKIP LOCKED;

-- name: ClaimProjectEnvironmentCloneInProject :one
WITH candidate AS (
    SELECT id FROM project_environment_clone_operations
    WHERE project_id = sqlc.arg(project_id)::uuid AND account_id = sqlc.arg(account_id)::uuid
      AND status IN ('pending', 'capturing', 'copying', 'publishing', 'compensating')
      AND next_attempt_at <= clock_timestamp() AND (lease_until IS NULL OR lease_until <= clock_timestamp())
    ORDER BY next_attempt_at, created_at, id LIMIT 1 FOR UPDATE SKIP LOCKED
)
UPDATE project_environment_clone_operations o
SET lease_token = sqlc.arg(lease_token)::uuid,
    lease_until = clock_timestamp() + sqlc.arg(lease_microseconds)::bigint * interval '1 microsecond',
    attempt_count = o.attempt_count + 1, revision = o.revision + 1, updated_at = clock_timestamp()
FROM candidate c WHERE o.id = c.id
RETURNING o.id::text AS operation_id, o.lease_until, o.attempt_count;

-- name: ReadProjectEnvironmentCloneWorkerOperation :one
SELECT id::text, account_id::text, project_id::text, source_environment, target_environment,
       idempotency_key, source_revision_hash, coalesce(source_release_set_id::text, '')::text AS source_release_set_id,
       status, revision, resources, error_code, created_at, updated_at,
       coalesce(target_release_set_id::text, '')::text AS target_release_set_id
FROM project_environment_clone_operations
WHERE id = sqlc.arg(operation_id)::uuid AND account_id = sqlc.arg(account_id)::uuid AND project_id = sqlc.arg(project_id)::uuid;

-- name: AdvanceProjectEnvironmentCloneOperationStatus :execrows
UPDATE project_environment_clone_operations
SET status = sqlc.arg(next_status)::text, revision = revision + 1,
    resources = sqlc.arg(resources)::jsonb, error_code = sqlc.arg(error_code)::text, updated_at = now(),
    lease_token = CASE WHEN sqlc.arg(next_status)::text IN ('ready', 'failed', 'compensated') THEN NULL ELSE lease_token END,
    lease_until = CASE WHEN sqlc.arg(next_status)::text IN ('ready', 'failed', 'compensated') THEN NULL ELSE lease_until END
WHERE id = sqlc.arg(operation_id)::uuid AND account_id = sqlc.arg(account_id)::uuid AND project_id = sqlc.arg(project_id)::uuid
  AND status = sqlc.arg(expected_status)::text AND revision = sqlc.arg(expected_revision)::bigint
  -- Native forks must first be adopted or retired by a qualified lifecycle.
  -- Until that lifecycle is available, preserve capture/compensation authority.
  AND (sqlc.arg(next_status)::text IN ('capturing','compensating')
    OR (NOT EXISTS (SELECT 1 FROM project_environment_clone_postgres_snapshot_restores r WHERE r.operation_id=project_environment_clone_operations.id AND r.state<>'deleted')
      AND NOT EXISTS (SELECT 1 FROM project_environment_clone_postgres_copy_targets c WHERE c.operation_id=project_environment_clone_operations.id AND c.state<>'retired')))
  AND (sqlc.arg(next_status)::text NOT IN ('failed','compensated')
    OR NOT EXISTS (SELECT 1 FROM project_environment_clone_postgres_snapshots s WHERE s.operation_id=project_environment_clone_operations.id AND s.state<>'deleted'))
  AND (sqlc.arg(next_status)::text IN ('capturing', 'compensating')
    OR (NOT EXISTS (SELECT 1 FROM object_bucket_write_fences f WHERE f.clone_operation_id = project_environment_clone_operations.id)
      AND NOT EXISTS (SELECT 1 FROM project_environment_clone_postgres_write_fences f WHERE f.operation_id = project_environment_clone_operations.id AND f.state<>'released')))
  AND ((attempt_count = 0 AND lease_token IS NULL)
    OR (lease_token IS NOT NULL AND lease_until > clock_timestamp()));

-- name: RenewProjectEnvironmentCloneWorkerLease :one
UPDATE project_environment_clone_operations
SET lease_until = greatest(lease_until, clock_timestamp() + sqlc.arg(lease_microseconds)::bigint * interval '1 microsecond')
WHERE id = sqlc.arg(operation_id)::uuid AND account_id = sqlc.arg(account_id)::uuid AND project_id = sqlc.arg(project_id)::uuid
  AND lease_token = sqlc.arg(lease_token)::uuid AND lease_until > clock_timestamp()
  AND revision = sqlc.arg(revision)::bigint AND status = sqlc.arg(status)::text
  AND status IN ('pending', 'capturing', 'copying', 'publishing', 'compensating')
RETURNING lease_until, attempt_count;

-- name: ReleaseProjectEnvironmentCloneWorkerLease :execrows
UPDATE project_environment_clone_operations
SET lease_token = NULL, lease_until = NULL, revision = revision + 1, updated_at = clock_timestamp(),
    next_attempt_at = clock_timestamp() + sqlc.arg(retry_microseconds)::bigint * interval '1 microsecond'
WHERE id = sqlc.arg(operation_id)::uuid AND account_id = sqlc.arg(account_id)::uuid AND project_id = sqlc.arg(project_id)::uuid
  AND lease_token = sqlc.arg(lease_token)::uuid AND lease_until > clock_timestamp()
  AND revision = sqlc.arg(revision)::bigint AND status = sqlc.arg(status)::text
  AND status IN ('pending', 'capturing', 'copying', 'publishing', 'compensating');

-- name: ReadProjectEnvironmentClonePostgresBindings :many
SELECT jsonb_build_object(
    'id', b.id::text, 'database_id', d.id::text, 'database_name', d.name,
    'environment_key', b.environment_key, 'access', b.access,
    'credential_ref', coalesce(b.credential_ref, ''), 'credential_generation', b.credential_generation,
    'backend_id', d.backend_id, 'backend_fingerprint', d.backend_fingerprint,
    'provider_resource_id', coalesce(d.provider_resource_id, ''), 'data_resource_id', coalesce(d.data_resource_id, ''), 'region', d.region,
    'postgres_major', d.postgres_major, 'service_class', d.service_class,
    'availability', d.availability, 'scale_to_zero', d.scale_to_zero,
    'storage_limit_bytes', d.storage_limit_bytes, 'restore_window_seconds', d.restore_window_seconds
) AS definition,
    (b.state = 'ready' AND d.state = 'ready' AND b.provider_identity_id IS NOT NULL
     AND d.observed_generation = d.desired_generation AND d.account_id = b.account_id)::boolean AS ready
FROM managed_postgres_bindings b JOIN managed_postgres_databases d ON d.id = b.database_id
WHERE b.account_id = sqlc.arg(account_id)::uuid AND b.app_id = sqlc.arg(app_id)::uuid
  AND b.scope = sqlc.arg(source_scope)::text AND b.state <> 'deleted'
ORDER BY b.id;

-- name: ReadProjectEnvironmentCloneObjectBuckets :many
SELECT jsonb_build_object(
    'id', b.id::text, 'name', b.name, 'region', b.region,
    'backend_id', b.backend_id, 'backend_fingerprint', b.backend_fingerprint,
    'physical_name', b.physical_name, 'public_read', b.public_read, 'serve_at', coalesce(b.serve_at, ''),
    'credentials', coalesce((
        SELECT jsonb_agg(jsonb_build_object('id', c.id::text, 'label', c.label, 'permission', c.permission,
            'managed_app_id', coalesce(c.managed_app_id::text, ''), 'managed_scope', coalesce(c.managed_scope, ''),
            'managed_prefix', coalesce(c.managed_prefix, '')) ORDER BY c.id)
        FROM object_storage_s3_credentials c
        WHERE c.bucket_id = b.id AND c.account_id = b.account_id AND c.status = 'active' AND c.rotation_parent_id IS NULL
    ), '[]'::jsonb),
    'access_grants', coalesce((
        SELECT jsonb_agg(jsonb_build_object('api_key_id', g.api_key_id::text, 'permission', g.permission) ORDER BY g.api_key_id)
        FROM object_storage_access_grants g WHERE g.bucket_id = b.id AND g.account_id = b.account_id
    ), '[]'::jsonb)
) AS definition, (b.state = 'ready')::boolean AS ready
FROM object_buckets b
WHERE b.account_id = sqlc.arg(account_id)::uuid AND b.app_id = sqlc.arg(app_id)::uuid
  AND b.scope = sqlc.arg(source_scope)::text AND b.state <> 'deleted'
ORDER BY b.id;

-- name: ReadProjectEnvironmentCloneScopedEdgePolicy :one
SELECT jsonb_build_object('edge_present', p.app_id IS NOT NULL, 'edge_rules', coalesce(p.rules, '[]'::jsonb)) AS definition
FROM apps a LEFT JOIN project_environment_edge_policies p ON p.app_id = a.id
    AND p.account_id = a.account_id AND p.project_id = a.project_id AND p.environment_slug = sqlc.arg(environment)::text
WHERE a.id = sqlc.arg(app_id)::uuid AND a.account_id = sqlc.arg(account_id)::uuid AND a.project_id = sqlc.arg(project_id)::uuid;

-- name: ReadProjectEnvironmentCloneScopedPolicyProof :one
SELECT jsonb_build_object('only_allow_declared_routes', r.only_allow_declared_routes, 'declared_routes', r.declared_routes,
    'edge_present', e.app_id IS NOT NULL, 'edge_rules', coalesce(e.rules, '[]'::jsonb)) AS definition
FROM project_environment_route_policies r
LEFT JOIN project_environment_edge_policies e ON e.app_id = r.app_id AND e.account_id = r.account_id
    AND e.project_id = r.project_id AND e.environment_slug = r.environment_slug
WHERE r.account_id = sqlc.arg(account_id)::uuid AND r.project_id = sqlc.arg(project_id)::uuid
  AND r.app_id = sqlc.arg(app_id)::uuid AND r.environment_slug = sqlc.arg(environment)::text;

-- name: InsertProjectEnvironmentCloneCapturedRoutePolicy :exec
INSERT INTO project_environment_route_policies(account_id, project_id, app_id, environment_slug, only_allow_declared_routes, declared_routes)
VALUES(sqlc.arg(account_id)::uuid, sqlc.arg(project_id)::uuid, sqlc.arg(app_id)::uuid, sqlc.arg(environment)::text,
    sqlc.arg(only_allow_declared_routes)::boolean, sqlc.arg(declared_routes)::jsonb);

-- name: InsertProjectEnvironmentCloneCapturedEdgePolicy :exec
INSERT INTO project_environment_edge_policies(account_id, project_id, app_id, environment_slug, rules)
VALUES(sqlc.arg(account_id)::uuid, sqlc.arg(project_id)::uuid, sqlc.arg(app_id)::uuid, sqlc.arg(environment)::text, sqlc.arg(rules)::jsonb);

-- name: ReadProjectEnvironmentCloneObjectMutationAuthority :one
SELECT coalesce(attempt_count = 0 AND lease_token IS NULL, false)::boolean AS legacy_allowed,
       coalesce(lease_token = nullif(sqlc.arg(worker_token)::text, '')::uuid
         AND lease_until > clock_timestamp() AND revision = sqlc.arg(expected_revision)::bigint
         AND status = sqlc.arg(expected_status)::text, false)::boolean AS worker_allowed
FROM project_environment_clone_operations
WHERE id = sqlc.arg(operation_id)::uuid AND account_id = sqlc.arg(account_id)::uuid AND project_id = sqlc.arg(project_id)::uuid;

-- name: ReadProjectEnvironmentCloneObjectManifestHeader :one
SELECT m.operation_id::text AS operation_id, m.source_bucket_id::text AS source_bucket_id, m.target_bucket_id::text AS target_bucket_id,
       m.captured_at_exact, m.manifest_hash, m.object_count
FROM project_environment_clone_object_manifests m
JOIN project_environment_clone_operations o ON o.id = m.operation_id
WHERE m.operation_id = sqlc.arg(operation_id)::uuid AND m.source_bucket_id = sqlc.arg(source_bucket_id)::uuid
  AND o.account_id = sqlc.arg(account_id)::uuid AND o.project_id = sqlc.arg(project_id)::uuid;

-- name: InsertProjectEnvironmentCloneObjectManifestHeader :exec
INSERT INTO project_environment_clone_object_manifests
    (operation_id, source_bucket_id, target_bucket_id, captured_at, captured_at_exact, manifest_hash, object_count)
VALUES (sqlc.arg(operation_id)::uuid, sqlc.arg(source_bucket_id)::uuid, sqlc.arg(target_bucket_id)::uuid,
    sqlc.arg(captured_at)::timestamptz, sqlc.arg(captured_at_exact)::text, sqlc.arg(manifest_hash)::text, sqlc.arg(object_count)::integer);

-- name: InsertProjectEnvironmentCloneObjectManifestEntries :execrows
INSERT INTO project_environment_clone_object_entries(operation_id, source_bucket_id, object_key, source_version, source_object)
SELECT sqlc.arg(operation_id)::uuid, sqlc.arg(source_bucket_id)::uuid, e.object_key, e.source_version, e.source_object
FROM jsonb_to_recordset(sqlc.arg(entries)::jsonb) AS e(object_key text, source_version text, source_object jsonb);

-- name: ReadProjectEnvironmentCloneObjectManifestEntries :many
SELECT source_object, copied_at, target_etag, verified_sha256
FROM project_environment_clone_object_entries
WHERE operation_id = sqlc.arg(operation_id)::uuid AND source_bucket_id = sqlc.arg(source_bucket_id)::uuid ORDER BY object_key;

-- name: MarkProjectEnvironmentCloneObjectManifestEntryCopied :execrows
UPDATE project_environment_clone_object_entries
SET copied_at = coalesce(copied_at, clock_timestamp()), target_etag = sqlc.arg(target_etag)::text, verified_sha256 = sqlc.arg(verified_sha256)::text
WHERE operation_id = sqlc.arg(operation_id)::uuid AND source_bucket_id = sqlc.arg(source_bucket_id)::uuid
  AND object_key = sqlc.arg(object_key)::text AND source_version = sqlc.arg(source_version)::text
  AND (copied_at IS NULL OR (target_etag = sqlc.arg(target_etag)::text AND verified_sha256 = sqlc.arg(verified_sha256)::text));

-- name: ReadProjectEnvironmentCloneObjectBucket :one
SELECT * FROM object_buckets
WHERE account_id = $1 AND app_id = $2 AND id = $3
  AND environment_clone_operation_id = $4 AND state <> 'deleted';

-- name: ReadProjectEnvironmentCloneObjectCredentialPreparation :one
SELECT p.target_credential_id, p.preparation_hash, p.preparation
FROM project_environment_clone_object_credentials p
JOIN project_environment_clone_operations o ON o.id = p.operation_id
WHERE o.account_id = $1 AND o.project_id = $2 AND o.id = $3 AND p.source_credential_id = $4;

-- name: InsertProjectEnvironmentCloneObjectCredentialPreparation :exec
INSERT INTO project_environment_clone_object_credentials
(operation_id, source_credential_id, target_credential_id, preparation_hash, preparation)
VALUES ($1, $2, $3, $4, $5);

-- name: ReadProjectEnvironmentCloneObjectCredentialSecrets :many
SELECT account_id::text, app_id::text, scope, key, ciphertext, coalesce(kid, '')::text AS kid,
       coalesce(value_hash, '')::text AS value_hash, secret_class, secret_version,
       managed_object_storage_credential_id::text,
       coalesce(managed_postgres_binding_id::text, '')::text AS managed_postgres_binding_id,
       coalesce(managed_credential_ref, '')::text AS managed_credential_ref,
       coalesce(managed_credential_generation, 0)::bigint AS managed_credential_generation
FROM app_secrets
WHERE account_id = $1 AND app_id = $2 AND scope = $3 AND managed_object_storage_credential_id = $4
ORDER BY key FOR UPDATE;

-- name: LockProjectEnvironmentClonePreparedObjectCredential :one
SELECT * FROM object_storage_s3_credentials
WHERE id = $1 AND account_id = $2 AND bucket_id = $3 AND rotation_parent_id IS NULL
FOR UPDATE;

-- name: LockProjectEnvironmentCloneCredentialBucket :one
SELECT b.* FROM object_buckets b
WHERE b.account_id = $1 AND b.app_id = $2 AND b.id = $3 AND b.environment_clone_operation_id = $4
  AND b.state = 'ready'
FOR UPDATE OF b;

-- name: GetManagedPostgresCustomerDatabase :one
SELECT d.* FROM managed_postgres_databases d
WHERE d.account_id=$1 AND d.id=$2 AND d.clone_resource_role='target'
  AND NOT EXISTS(SELECT 1 FROM project_environment_clone_postgres_copy_targets c WHERE c.target_database_id=d.id AND c.state<>'retired') AND (
    d.environment_clone_operation_id IS NULL OR EXISTS (
        SELECT 1 FROM project_environment_clone_operations o
        WHERE o.id=d.environment_clone_operation_id AND o.account_id=d.account_id AND o.status='ready'
          AND EXISTS (SELECT 1 FROM project_environments e JOIN projects p ON p.id=e.project_id
              WHERE e.project_id=o.project_id AND e.slug=o.target_environment AND p.account_id=d.account_id)
          AND EXISTS (SELECT 1 FROM jsonb_array_elements(o.resources) r
              WHERE r->>'kind' IN ('postgres','managed_postgres') AND r->>'source_id'=d.restore_source_database_id::text
                AND r->>'target_id'=d.id::text AND r->>'status'='ready')));

-- name: ListManagedPostgresCustomerDatabases :many
SELECT d.* FROM managed_postgres_databases d
WHERE d.account_id=$1 AND d.state<>'deleted' AND d.clone_resource_role='target'
  AND NOT EXISTS(SELECT 1 FROM project_environment_clone_postgres_copy_targets c WHERE c.target_database_id=d.id AND c.state<>'retired') AND (
    d.environment_clone_operation_id IS NULL OR EXISTS (
        SELECT 1 FROM project_environment_clone_operations o
        WHERE o.id=d.environment_clone_operation_id AND o.account_id=d.account_id AND o.status='ready'
          AND EXISTS (SELECT 1 FROM project_environments e JOIN projects p ON p.id=e.project_id
              WHERE e.project_id=o.project_id AND e.slug=o.target_environment AND p.account_id=d.account_id)
          AND EXISTS (SELECT 1 FROM jsonb_array_elements(o.resources) r
              WHERE r->>'kind' IN ('postgres','managed_postgres') AND r->>'source_id'=d.restore_source_database_id::text
                AND r->>'target_id'=d.id::text AND r->>'status'='ready')))
ORDER BY d.created_at,d.id;

-- name: LockManagedPostgresCustomerDatabase :one
SELECT d.id FROM managed_postgres_databases d
WHERE d.account_id=$1 AND d.id=$2 AND d.clone_resource_role='target'
  AND NOT EXISTS(SELECT 1 FROM project_environment_clone_postgres_copy_targets c WHERE c.target_database_id=d.id AND c.state<>'retired') AND (
    d.environment_clone_operation_id IS NULL OR EXISTS (
        SELECT 1 FROM project_environment_clone_operations o
        WHERE o.id=d.environment_clone_operation_id AND o.account_id=d.account_id AND o.status='ready'
          AND EXISTS (SELECT 1 FROM project_environments e JOIN projects p ON p.id=e.project_id
              WHERE e.project_id=o.project_id AND e.slug=o.target_environment AND p.account_id=d.account_id)
          AND EXISTS (SELECT 1 FROM jsonb_array_elements(o.resources) r
              WHERE r->>'kind' IN ('postgres','managed_postgres') AND r->>'source_id'=d.restore_source_database_id::text
                AND r->>'target_id'=d.id::text AND r->>'status'='ready')))
FOR KEY SHARE OF d;

-- name: LockProjectEnvironmentCloneDatabaseAccount :one
SELECT id FROM accounts WHERE id=$1 AND status<>'deleted_pending' FOR UPDATE;

-- name: ReadProjectEnvironmentCloneDatabaseByName :one
SELECT d.* FROM managed_postgres_databases d WHERE d.account_id=$1 AND d.name=$2 ORDER BY d.created_at,d.id LIMIT 1 FOR UPDATE OF d;

-- name: ReadProjectEnvironmentCloneDatabaseReservation :one
SELECT d.* FROM managed_postgres_databases d
WHERE d.account_id=$1 AND d.environment_clone_operation_id=$2 AND d.restore_source_database_id=$3 AND d.clone_resource_role='target' FOR UPDATE OF d;

-- name: ReadProjectEnvironmentCloneDatabaseSource :one
SELECT d.* FROM managed_postgres_databases d WHERE d.account_id=$1 AND d.id=$2 FOR UPDATE OF d;

-- name: ReadProjectEnvironmentCloneDatabaseReservationTime :one
SELECT clock_timestamp()::timestamptz AS observed_at;

-- name: CountProjectEnvironmentCloneDatabaseAccount :one
SELECT ((SELECT count(*) FROM managed_postgres_databases d WHERE d.account_id=$1 AND d.state<>'deleted')
    + (SELECT count(*) FROM project_environment_clone_postgres_snapshot_restores r WHERE r.account_id=$1 AND r.state<>'deleted' AND r.adopted_database_id IS NULL))::bigint AS count;

-- name: InsertProjectEnvironmentCloneDatabase :one
INSERT INTO managed_postgres_databases(id, account_id, name, region, postgres_major, service_class, availability, scale_to_zero,
    storage_limit_bytes, restore_window_seconds, backend_id, backend_fingerprint, restore_source_database_id, restore_source_resource_id,
    restore_point_in_time, environment_clone_operation_id, state, desired_generation, observed_generation, clone_resource_role)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,'provisioning',1,0,$17) RETURNING *;

-- name: ReadProjectEnvironmentClonePostgresBindingLedger :one
SELECT * FROM project_environment_clone_postgres_bindings WHERE operation_id=$1 AND source_binding_id=$2 FOR UPDATE;

-- name: InsertProjectEnvironmentClonePostgresBindingLedger :exec
INSERT INTO project_environment_clone_postgres_bindings(operation_id,source_binding_id,target_binding_id,reservation_hash) VALUES($1,$2,$3,$4);

-- name: InsertProjectEnvironmentClonePostgresBinding :one
INSERT INTO managed_postgres_bindings(id,account_id,database_id,app_id,scope,environment_key,access,credential_generation,state)
VALUES($1,$2,$3,$4,$5,$6,$7,1,'provisioning') RETURNING *;

-- name: LockProjectEnvironmentClonePostgresBinding :one
SELECT * FROM managed_postgres_bindings WHERE id=$1 AND account_id=$2 FOR UPDATE;

-- name: ReadProjectEnvironmentClonePostgresBindingSecrets :many
SELECT * FROM app_secrets WHERE managed_postgres_binding_id=$1 ORDER BY app_id,scope,key FOR UPDATE;

-- name: InsertProjectEnvironmentClonePostgresSecret :exec
INSERT INTO app_secrets(account_id,app_id,scope,key,ciphertext,kid,value_hash,managed_postgres_binding_id,managed_credential_ref,managed_credential_generation)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,1);

-- name: FinishProjectEnvironmentClonePostgresBinding :execrows
UPDATE managed_postgres_bindings SET state='ready',provider_identity_id=$3,credential_ref=$4,updated_at=now()
WHERE id=$1 AND account_id=$2 AND state='provisioning' AND lease_token IS NULL AND credential_generation=1;

-- name: FinishProjectEnvironmentClonePostgresBindingLedger :execrows
UPDATE project_environment_clone_postgres_bindings SET preparation_hash=$3,preparation=$4
WHERE operation_id=$1 AND source_binding_id=$2 AND preparation_hash IS NULL;

-- name: LockProjectEnvironmentCloneSecretTarget :exec
SELECT pg_advisory_xact_lock(managed_secret_target_lock_key(sqlc.arg(app_id)::uuid,sqlc.arg(scope)::text,sqlc.arg(key)::text));

-- name: ProjectEnvironmentCloneSecretTargetExists :one
SELECT EXISTS(SELECT 1 FROM app_secrets WHERE app_id=$1 AND scope=$2 AND key=$3)::boolean;

-- name: ManagedPostgresBindingDatabaseID :one
SELECT database_id FROM managed_postgres_bindings WHERE id=$1 AND account_id=$2;

-- name: ManagedPostgresDueBindings :many
SELECT b.* FROM managed_postgres_bindings b JOIN managed_postgres_databases d ON d.id=b.database_id
WHERE (b.state='deleting' OR (sqlc.arg(include_provisioning)::boolean AND b.state IN ('provisioning','failed'))
    OR (b.rotation_cleanup_ready AND b.state IN ('ready','retiring')
      AND (b.access <> 'migration' OR NOT EXISTS (SELECT 1 FROM app_tasks task
          WHERE task.account_id=b.account_id AND task.app_id=b.app_id AND task.deployment_scope=b.scope
            AND task.status IN ('restoring','running')))))
  AND b.retry_at<=sqlc.arg(observed_at)::timestamptz AND (b.lease_until IS NULL OR b.lease_until<=sqlc.arg(observed_at)::timestamptz)
  AND (d.environment_clone_operation_id IS NULL OR EXISTS (
    SELECT 1 FROM project_environment_clone_operations o WHERE o.id=d.environment_clone_operation_id AND o.account_id=d.account_id AND o.status='ready'
      AND EXISTS(SELECT 1 FROM project_environments e JOIN projects p ON p.id=e.project_id
          WHERE e.project_id=o.project_id AND e.slug=o.target_environment AND p.account_id=d.account_id)
      AND EXISTS(SELECT 1 FROM jsonb_array_elements(o.resources) r WHERE r->>'kind' IN ('postgres','managed_postgres')
          AND r->>'source_id'=d.restore_source_database_id::text AND r->>'target_id'=d.id::text AND r->>'status'='ready')))
ORDER BY b.retry_at,b.id LIMIT sqlc.arg(batch_limit)::int;

-- name: ReadProjectEnvironmentCloneMaterialization :one
SELECT * FROM project_environment_clone_materializations WHERE operation_id=$1;

-- name: InsertProjectEnvironmentCloneMaterialization :exec
INSERT INTO project_environment_clone_materializations(operation_id,environment_id,workload_settings) VALUES($1,$2,$3);

-- name: LockProjectEnvironmentCloneMaterializedEnvironment :one
SELECT * FROM project_environments WHERE id=$1 AND account_id=$2 AND project_id=$3 AND slug=$4 FOR UPDATE;

-- name: ReadProjectEnvironmentCloneMaterializedWorkloads :many
SELECT h.app_id,h.spec_id,s.config_hash,s.settings
FROM project_environment_workload_heads h
JOIN project_environment_workload_specs s ON s.id=h.spec_id AND s.environment_id=h.environment_id AND s.app_id=h.app_id
WHERE h.environment_id=$1 ORDER BY h.app_id FOR SHARE OF h,s;

-- name: ReadProjectEnvironmentCloneOperationIDByKey :one
SELECT id FROM project_environment_clone_operations WHERE account_id=$1 AND project_id=$2 AND idempotency_key=$3;

-- name: CreateCapturedProjectEnvironmentCloneOperation :exec
INSERT INTO project_environment_clone_operations(id,account_id,project_id,source_environment,target_environment,idempotency_key,source_revision_hash,source_release_set_id,configuration_capture_version)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,1);

-- name: InsertProjectEnvironmentCloneConfigurationCapture :exec
INSERT INTO project_environment_clone_configuration_captures(operation_id,version,configuration_hash,configuration) VALUES($1,1,$2,$3);

-- name: ReadProjectEnvironmentCloneConfigurationCaptureIdentity :one
SELECT o.configuration_capture_version,o.source_revision_hash,o.source_environment,coalesce(o.source_release_set_id::text,'')::text AS source_release_set_id,
    EXISTS(SELECT 1 FROM project_environment_clone_configuration_captures c WHERE c.operation_id=o.id)::boolean AS has_capture
FROM project_environment_clone_operations o
WHERE o.account_id=sqlc.arg(account_id)::uuid AND o.project_id=sqlc.arg(project_id)::uuid AND o.id=sqlc.arg(operation_id)::uuid;

-- name: LockProjectEnvironmentCloneConfigurationCapture :one
SELECT * FROM project_environment_clone_configuration_captures WHERE operation_id=$1 FOR UPDATE;

-- name: ReadProjectEnvironmentCloneCoverageSchema :many
-- Include all application-schema tables. Several configuration tables have
-- neither tenant identity columns nor foreign keys, so ownership heuristics
-- would silently omit them. Partition children inherit their parent's policy.
SELECT c.relname::text AS table_name,
    array_agg(a.attname::text ORDER BY a.attname)::text[] AS columns
FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace
JOIN pg_catalog.pg_attribute a ON a.attrelid=c.oid AND a.attnum>0 AND NOT a.attisdropped
WHERE n.nspname=current_schema() AND c.relkind IN ('r','p') AND NOT c.relispartition
GROUP BY c.oid,c.relname ORDER BY c.relname;

-- name: CaptureProjectEnvironmentCloneWorkPolicies :one
SELECT jsonb_build_object(
    'version',1,'app_id',a.id::text,'source_scope',sqlc.arg(source_scope)::text,
    'policies',coalesce((SELECT jsonb_agg(jsonb_build_object(
        'name',p.name,'revision',p.revision,'max_running_per_key',p.max_running_per_key,
        'max_running_per_fairness_key',p.max_running_per_fairness_key,'pending_updates',p.pending_updates,
        'debounce_ms',p.debounce_ms,'expires_after_ms',p.expires_after_ms) ORDER BY p.name)
        FROM app_work_policies p WHERE p.app_id=a.id),'[]'::jsonb),
    'event_bindings',coalesce((SELECT jsonb_agg(jsonb_build_object(
        'subscription_id',b.subscription_id::text,'policy_name',b.policy_name,'key_selector',b.key_selector,
        'fairness_selector',b.fairness_key_selector,'action',b.action) ORDER BY b.subscription_id)
        FROM event_subscription_work_bindings b WHERE b.app_id=a.id),'[]'::jsonb),
    'trigger_bindings',coalesce((SELECT jsonb_agg(jsonb_build_object(
        'trigger_id',b.trigger_id::text,'policy_name',b.policy_name,'key_selector',b.key_selector,
        'fairness_selector',b.fairness_key_selector) ORDER BY b.trigger_id)
        FROM trigger_work_bindings b WHERE b.app_id=a.id),'[]'::jsonb)
)::jsonb AS definitions,
    ((SELECT count(*) FROM app_work_policies p WHERE p.app_id=a.id AND p.account_id<>a.account_id)
    +(SELECT count(*) FROM event_subscription_work_bindings b LEFT JOIN event_subscriptions s ON s.id=b.subscription_id
        WHERE b.app_id=a.id AND (s.id IS NULL OR s.app_id<>a.id OR s.account_id<>a.account_id))
    +(SELECT count(*) FROM trigger_work_bindings b LEFT JOIN triggers t ON t.id=b.trigger_id
        WHERE b.app_id=a.id AND (t.id IS NULL OR t.app_id<>a.id OR t.account_id<>a.account_id)))::bigint AS ownership_violations
FROM apps a WHERE a.id=sqlc.arg(app_id)::uuid AND a.account_id=sqlc.arg(account_id)::uuid AND a.status<>'deleted';

-- name: LockProjectEnvironmentQueuePreparationEnvironment :one
SELECT e.id FROM project_environments e
JOIN apps a ON a.project_id=e.project_id AND a.account_id=e.account_id
JOIN deployments d ON d.app_id=a.id AND d.scope=e.slug
WHERE e.account_id=sqlc.arg(account_id)::uuid AND e.project_id=sqlc.arg(project_id)::uuid
    AND d.id=sqlc.arg(deployment_id)::uuid AND (NOT sqlc.arg(require_live)::boolean OR d.status='live') AND a.status<>'deleted'
    AND e.slug NOT IN ('production','default')
FOR KEY SHARE OF e;

-- name: LockProjectEnvironmentQueuePreparationApp :one
SELECT a.id FROM apps a
JOIN project_environments e ON e.project_id=a.project_id AND e.account_id=a.account_id
JOIN deployments d ON d.app_id=a.id AND d.scope=e.slug
WHERE e.account_id=sqlc.arg(account_id)::uuid AND e.project_id=sqlc.arg(project_id)::uuid
    AND e.id=sqlc.arg(environment_id)::uuid AND d.id=sqlc.arg(deployment_id)::uuid
    AND (NOT sqlc.arg(require_live)::boolean OR d.status='live') AND a.status<>'deleted' AND e.slug NOT IN ('production','default')
FOR KEY SHARE OF a;

-- name: LockProjectEnvironmentQueuePreparationSpec :one
SELECT s.id,e.account_id,e.project_id,e.id AS environment_id,e.slug AS environment_slug,
    s.app_id,s.revision,s.config_hash,s.settings,s.created_at
FROM project_environment_workload_deployment_specs p
JOIN deployments d ON d.id=p.deployment_id
JOIN project_environment_workload_specs s ON s.id=p.spec_id AND s.app_id=d.app_id
JOIN project_environments e ON e.id=s.environment_id AND e.slug=d.scope
JOIN apps a ON a.id=s.app_id AND a.project_id=e.project_id AND a.account_id=e.account_id
WHERE e.account_id=sqlc.arg(account_id)::uuid AND e.project_id=sqlc.arg(project_id)::uuid
    AND e.id=sqlc.arg(environment_id)::uuid AND d.id=sqlc.arg(deployment_id)::uuid AND a.id=sqlc.arg(app_id)::uuid
    AND (NOT sqlc.arg(require_live)::boolean OR d.status='live') AND a.status<>'deleted' AND e.slug NOT IN ('production','default')
FOR UPDATE OF d FOR SHARE OF s,p;

-- name: ReadProjectEnvironmentQueueRuntimeSet :one
SELECT * FROM project_environment_queue_runtime_sets WHERE deployment_id=$1 FOR SHARE;

-- name: ReadProjectEnvironmentQueueConsumers :many
SELECT * FROM project_environment_queue_consumers WHERE runtime_set_id=$1 ORDER BY name FOR SHARE;

-- name: InsertProjectEnvironmentQueueRuntimeSet :exec
INSERT INTO project_environment_queue_runtime_sets
    (id,account_id,project_id,environment_id,app_id,deployment_id,workload_spec_id,
     settings_hash,queue_revision,binding_count,book_hash,state,created_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13);

-- name: InsertProjectEnvironmentQueueConsumer :exec
INSERT INTO project_environment_queue_consumers
    (id,runtime_set_id,name,queue_name,definition,definition_hash,created_at)
VALUES ($1,$2,$3,$4,$5,$6,$7);

-- name: RegisterInvocationWorkEnvironmentDomain :one
INSERT INTO invocation_work_environment_domains AS current (app_id,environment_id,policy_name,kind,digest)
SELECT a.id,e.id,sqlc.arg(policy_name)::text,sqlc.arg(kind)::text,sqlc.arg(digest)::bytea
FROM apps a JOIN project_environments e ON e.project_id=a.project_id AND e.account_id=a.account_id
WHERE a.id=sqlc.arg(app_id)::uuid AND a.account_id=sqlc.arg(account_id)::uuid AND a.status<>'deleted'
    AND e.id=sqlc.arg(environment_id)::uuid AND e.slug NOT IN ('production','default')
ON CONFLICT (app_id,policy_name,kind,digest) DO UPDATE SET environment_id=current.environment_id
WHERE current.environment_id=excluded.environment_id
RETURNING environment_id;

-- name: ReadInvocationWorkEnvironmentDomain :one
SELECT environment_id FROM invocation_work_environment_domains
WHERE app_id=sqlc.arg(app_id)::uuid AND policy_name=sqlc.arg(policy_name)::text
    AND kind=sqlc.arg(kind)::text AND digest=sqlc.arg(digest)::bytea;

-- name: CreateInvocationWorkEnvironmentAdmission :execrows
-- One server admission clock anchors the stage's debounce, expiry and created
-- time. The legacy insert otherwise uses PostgreSQL's transaction-start clock.
WITH admitted AS (
    UPDATE invocations SET created_at=sqlc.arg(admitted_at)::timestamptz,environment_id=sqlc.arg(environment_id)::uuid
    WHERE id=sqlc.arg(invocation_id)::uuid AND app_id=sqlc.arg(app_id)::uuid AND account_id=sqlc.arg(account_id)::uuid
    RETURNING id,app_id,account_id,work_policy_name,work_policy_revision,work_key_digest,work_fairness_digest,work_fairness_limit
)
INSERT INTO invocation_work_environment_admissions
    (invocation_id,environment_id,workload_spec_id,settings_hash,app_id,policy_name,policy_revision,key_digest,fairness_digest,fairness_limit)
SELECT i.id,e.id,s.id,s.config_hash,i.app_id,i.work_policy_name,i.work_policy_revision,i.work_key_digest,i.work_fairness_digest,coalesce(i.work_fairness_limit,0)
FROM admitted i JOIN apps a ON a.id=i.app_id AND a.account_id=i.account_id
JOIN project_environments e ON e.project_id=a.project_id AND e.account_id=a.account_id
JOIN project_environment_workload_specs s ON s.environment_id=e.id AND s.app_id=a.id
JOIN project_environment_workload_deployment_specs p ON p.spec_id=s.id
JOIN deployments d ON d.id=p.deployment_id AND d.app_id=a.id AND d.scope=e.slug
WHERE i.id=sqlc.arg(invocation_id)::uuid AND i.app_id=sqlc.arg(app_id)::uuid AND a.account_id=sqlc.arg(account_id)::uuid
    AND a.status<>'deleted' AND e.id=sqlc.arg(environment_id)::uuid AND e.slug NOT IN ('production','default')
    AND s.id=sqlc.arg(workload_spec_id)::uuid AND s.config_hash=sqlc.arg(settings_hash)::text
    AND d.id=sqlc.arg(deployment_id)::uuid AND d.status='live'
    AND i.work_policy_name=sqlc.arg(policy_name)::text AND i.work_policy_revision=sqlc.arg(policy_revision)::bigint
    AND i.work_key_digest=sqlc.arg(key_digest)::bytea
    AND i.work_fairness_digest IS NOT DISTINCT FROM sqlc.narg(fairness_digest)::bytea
    AND coalesce(i.work_fairness_limit,0)=sqlc.arg(fairness_limit)::integer
ON CONFLICT (invocation_id) DO NOTHING;

-- name: ReadInvocationWorkEnvironmentAdmission :one
SELECT * FROM invocation_work_environment_admissions WHERE invocation_id=sqlc.arg(invocation_id)::uuid;

-- name: ValidateInvocationWorkEnvironmentClaim :one
SELECT
    EXISTS(SELECT 1 FROM deployments d WHERE d.app_id=i.app_id AND d.id::text=lower(i.headers->>'X-Gregale-Revision')
        AND d.scope NOT IN ('production','default')) OR
    EXISTS(SELECT 1 FROM project_release_sets r JOIN apps a ON a.project_id=r.project_id AND a.account_id=r.account_id
        WHERE a.id=i.app_id AND r.id::text=lower(i.headers->>'X-Gregale-Release') AND r.environment_slug NOT IN ('production','default')) AS stage_pin,
    EXISTS(SELECT 1 FROM invocation_work_environment_admissions p
        JOIN invocation_work_environment_domains k ON k.app_id=p.app_id AND k.policy_name=p.policy_name
            AND k.kind='key' AND k.digest=p.key_digest AND k.environment_id=p.environment_id
        JOIN apps a ON a.id=p.app_id AND a.account_id=i.account_id AND a.status<>'deleted'
        JOIN project_environments e ON e.id=p.environment_id AND e.project_id=a.project_id AND e.account_id=a.account_id
        JOIN project_environment_workload_specs s ON s.id=p.workload_spec_id AND s.environment_id=e.id AND s.app_id=a.id AND s.config_hash=p.settings_hash
        JOIN project_environment_workload_deployment_specs ds ON ds.spec_id=s.id
        JOIN deployments d ON d.id=ds.deployment_id AND d.app_id=a.id AND d.scope=e.slug AND d.status='live'
        WHERE p.invocation_id=i.id AND p.environment_id=i.environment_id AND p.app_id=i.app_id AND p.policy_name=i.work_policy_name
            AND p.policy_revision=i.work_policy_revision AND p.key_digest=i.work_key_digest
            AND p.fairness_digest IS NOT DISTINCT FROM i.work_fairness_digest AND p.fairness_limit=coalesce(i.work_fairness_limit,0)
            AND (p.fairness_limit=0 OR EXISTS(SELECT 1 FROM invocation_work_environment_domains f
                WHERE f.app_id=p.app_id AND f.policy_name=p.policy_name AND f.kind='fairness'
                    AND f.digest=p.fairness_digest AND f.environment_id=p.environment_id))
            AND e.slug NOT IN ('production','default') AND i.source IN ('async_invoke','delayed_task') AND i.cron_id IS NULL AND i.queue_name=''
            AND i.on_success_destination_id IS NULL AND i.on_failure_destination_id IS NULL
            AND ((d.id::text=lower(i.headers->>'X-Gregale-Revision') AND NOT i.headers ? 'X-Gregale-Release')
                OR (NOT i.headers ? 'X-Gregale-Revision' AND EXISTS(
                    SELECT 1 FROM project_release_sets r JOIN project_release_members m ON m.release_id=r.id AND m.app_id=a.id AND m.deployment_id=d.id
                    WHERE r.id::text=lower(i.headers->>'X-Gregale-Release') AND r.account_id=a.account_id AND r.project_id=a.project_id
                        AND r.environment_slug=e.slug AND (r.expires_at IS NULL OR r.expires_at>now()))))) AS admission_valid
FROM invocations i WHERE i.id=sqlc.arg(invocation_id)::uuid;

-- name: LockInvocationEnvironmentAdmission :one
SELECT e.id FROM project_environments e JOIN apps a ON a.project_id=e.project_id AND a.account_id=e.account_id
WHERE e.id=sqlc.arg(environment_id)::uuid AND e.slug NOT IN ('production','default')
    AND a.id=sqlc.arg(app_id)::uuid AND a.account_id=sqlc.arg(account_id)::uuid AND a.status<>'deleted'
FOR KEY SHARE OF e;

-- name: BindInvocationEnvironment :execrows
UPDATE invocations i SET environment_id=e.id
FROM project_environments e JOIN apps a ON a.project_id=e.project_id AND a.account_id=e.account_id
JOIN deployments d ON d.app_id=a.id AND d.scope=e.slug AND d.status='live'
WHERE i.id=sqlc.arg(invocation_id)::uuid AND i.app_id=a.id AND i.account_id=a.account_id
    AND a.id=sqlc.arg(app_id)::uuid AND a.account_id=sqlc.arg(account_id)::uuid AND a.status<>'deleted'
    AND e.id=sqlc.arg(environment_id)::uuid AND e.slug NOT IN ('production','default')
    AND d.id=sqlc.arg(deployment_id)::uuid AND (i.environment_id IS NULL OR i.environment_id=e.id)
    AND i.source IN ('async_invoke','delayed_task') AND i.cron_id IS NULL AND i.queue_name=''
    AND i.on_success_destination_id IS NULL AND i.on_failure_destination_id IS NULL
    AND ((d.id::text=lower(i.headers->>'X-Gregale-Revision') AND NOT i.headers ? 'X-Gregale-Release')
        OR (NOT i.headers ? 'X-Gregale-Revision' AND EXISTS(SELECT 1 FROM project_release_sets r
            JOIN project_release_members m ON m.release_id=r.id AND m.app_id=a.id AND m.deployment_id=d.id
            WHERE r.id::text=lower(i.headers->>'X-Gregale-Release') AND r.project_id=e.project_id AND r.account_id=e.account_id
                AND r.environment_slug=e.slug AND (r.expires_at IS NULL OR r.expires_at>now()))));

-- name: ReadInvocationEnvironmentOwner :one
SELECT coalesce(environment_id::text,'')::text AS environment_id,app_id,account_id FROM invocations WHERE id=sqlc.arg(invocation_id)::uuid;

-- name: ValidateInvocationEnvironmentClaim :one
SELECT CASE WHEN i.environment_id IS NULL THEN
    NOT (EXISTS(SELECT 1 FROM deployments d WHERE d.app_id=i.app_id AND d.id::text=lower(i.headers->>'X-Gregale-Revision') AND d.scope NOT IN ('production','default'))
        OR EXISTS(SELECT 1 FROM project_release_sets r JOIN apps a ON a.project_id=r.project_id AND a.account_id=r.account_id
            WHERE a.id=i.app_id AND r.id::text=lower(i.headers->>'X-Gregale-Release') AND r.environment_slug NOT IN ('production','default')))
ELSE EXISTS(SELECT 1 FROM project_environments e JOIN apps a ON a.project_id=e.project_id AND a.account_id=e.account_id
    JOIN deployments d ON d.app_id=a.id AND d.scope=e.slug AND d.status='live'
    WHERE e.id=i.environment_id AND e.slug NOT IN ('production','default') AND a.id=i.app_id AND a.account_id=i.account_id AND a.status<>'deleted'
        AND i.source IN ('async_invoke','delayed_task') AND i.cron_id IS NULL AND i.queue_name=''
        AND i.on_success_destination_id IS NULL AND i.on_failure_destination_id IS NULL
        AND ((d.id::text=lower(i.headers->>'X-Gregale-Revision') AND NOT i.headers ? 'X-Gregale-Release')
            OR (NOT i.headers ? 'X-Gregale-Revision' AND EXISTS(SELECT 1 FROM project_release_sets r
                JOIN project_release_members m ON m.release_id=r.id AND m.app_id=a.id AND m.deployment_id=d.id
                WHERE r.id::text=lower(i.headers->>'X-Gregale-Release') AND r.project_id=e.project_id AND r.account_id=e.account_id
                    AND r.environment_slug=e.slug AND (r.expires_at IS NULL OR r.expires_at>now())))))
END::boolean AS owner_valid
FROM invocations i WHERE i.id=sqlc.arg(invocation_id)::uuid;

-- name: LockEnvironmentInvocationCleanup :one
SELECT id FROM project_environments WHERE account_id=$1 AND project_id=$2 AND slug=$3 FOR UPDATE;

-- name: ValidateEnvironmentInvocationCleanup :one
SELECT
    EXISTS(SELECT 1 FROM invocations i WHERE i.environment_id=e.id AND (i.state='dispatching' OR i.quota_reserved)) AS busy,
    EXISTS(SELECT 1 FROM invocations i LEFT JOIN apps a ON a.id=i.app_id
        WHERE i.environment_id=e.id AND (a.id IS NULL OR a.project_id<>e.project_id OR a.account_id<>e.account_id OR i.account_id<>e.account_id
            OR NOT ((i.source IN ('async_invoke','delayed_task') AND i.queue_name='') OR (i.source='queue' AND EXISTS(SELECT 1 FROM invocation_environment_queue_admissions q WHERE q.invocation_id=i.id AND q.environment_id=e.id AND q.app_id=i.app_id AND q.account_id=i.account_id AND q.queue_name=i.queue_name))) OR i.cron_id IS NOT NULL
            OR i.on_success_destination_id IS NOT NULL OR i.on_failure_destination_id IS NOT NULL
            OR (i.work_policy_name IS NULL AND EXISTS(SELECT 1 FROM invocation_work_environment_admissions p WHERE p.invocation_id=i.id))
            OR (i.work_policy_name IS NOT NULL AND NOT EXISTS(
                SELECT 1 FROM invocation_work_environment_admissions p
                JOIN project_environment_workload_specs s ON s.id=p.workload_spec_id AND s.environment_id=p.environment_id AND s.app_id=p.app_id AND s.config_hash=p.settings_hash
                JOIN invocation_work_environment_domains k ON k.environment_id=p.environment_id AND k.app_id=p.app_id AND k.policy_name=p.policy_name AND k.kind='key' AND k.digest=p.key_digest
                WHERE p.invocation_id=i.id AND p.environment_id=e.id AND p.app_id=i.app_id AND p.policy_name=i.work_policy_name
                    AND p.policy_revision=i.work_policy_revision AND p.key_digest=i.work_key_digest
                    AND p.fairness_digest IS NOT DISTINCT FROM i.work_fairness_digest AND p.fairness_limit=coalesce(i.work_fairness_limit,0)
                    AND (p.fairness_limit=0 OR EXISTS(SELECT 1 FROM invocation_work_environment_domains f
                        WHERE f.environment_id=e.id AND f.app_id=p.app_id AND f.policy_name=p.policy_name AND f.kind='fairness' AND f.digest=p.fairness_digest))))))
    OR EXISTS(SELECT 1 FROM invocation_work_environment_admissions p JOIN invocations i ON i.id=p.invocation_id
        WHERE p.environment_id=e.id AND i.environment_id IS DISTINCT FROM e.id)
    OR EXISTS(SELECT 1 FROM invocation_environment_queue_admissions p JOIN invocations i ON i.id=p.invocation_id
        WHERE p.environment_id=e.id AND i.environment_id IS DISTINCT FROM e.id)
    OR EXISTS(SELECT 1 FROM invocation_work_environment_domains d LEFT JOIN apps a ON a.id=d.app_id
        WHERE d.environment_id=e.id AND (a.id IS NULL OR a.project_id<>e.project_id OR a.account_id<>e.account_id))
    OR EXISTS(SELECT 1 FROM invocation_work_environment_domains d JOIN invocations i ON i.app_id=d.app_id AND i.work_policy_name=d.policy_name
        AND ((d.kind='key' AND i.work_key_digest=d.digest) OR (d.kind='fairness' AND i.work_fairness_digest=d.digest))
        WHERE d.environment_id=e.id AND i.environment_id IS DISTINCT FROM e.id)
    OR EXISTS(SELECT 1 FROM invocation_work_environment_domains d JOIN triggers t ON t.app_id=d.app_id
        JOIN trigger_records r ON r.trigger_id=t.id AND r.work_policy_name=d.policy_name
            AND ((d.kind='key' AND r.work_key_digest=d.digest) OR (d.kind='fairness' AND r.work_fairness_digest=d.digest))
        WHERE d.environment_id=e.id) AS invalid
FROM project_environments e WHERE e.id=sqlc.arg(environment_id)::uuid;

-- name: DeleteEnvironmentInvocations :execrows
DELETE FROM invocations WHERE environment_id=$1;

-- name: DeleteEnvironmentWorkKeyLanes :exec
DELETE FROM invocation_work_lanes l USING invocation_work_environment_domains d
WHERE d.environment_id=$1 AND d.kind='key' AND l.app_id=d.app_id AND l.policy_name=d.policy_name AND l.key_digest=d.digest;

-- name: DeleteEnvironmentWorkFairnessLanes :exec
DELETE FROM invocation_work_fairness_lanes l USING invocation_work_environment_domains d
WHERE d.environment_id=$1 AND d.kind='fairness' AND l.app_id=d.app_id AND l.policy_name=d.policy_name AND l.fairness_digest=d.digest;

-- name: DeleteEnvironmentWorkDomains :exec
DELETE FROM invocation_work_environment_domains WHERE environment_id=$1;

-- name: CaptureProjectEnvironmentCloneQueues :one
SELECT jsonb_build_object('version',1,'app_id',a.id::text,'source_scope',sqlc.arg(source_scope)::text,'environment_owned',false,
    'bindings',coalesce((SELECT jsonb_agg(jsonb_build_object('source_id',b.id::text,'name',b.name,'queue_name',b.queue_name,
        'mode',b.mode,'workload_class',b.workload_class,'enabled',b.enabled,'max_concurrency',b.max_concurrency,'retry_policy',b.retry_policy) ORDER BY b.name)
        FROM queue_bindings b WHERE b.app_id=a.id),'[]'::jsonb)) AS definitions,
    (SELECT count(*) FROM queue_bindings b WHERE b.app_id=a.id AND b.account_id<>a.account_id)::bigint AS ownership_violations
FROM apps a WHERE a.id=sqlc.arg(app_id)::uuid AND a.account_id=sqlc.arg(account_id)::uuid AND a.status<>'deleted';

-- ADR-531: production queue pollers cannot own stage work.
-- name: ListProductionNamedQueueCandidates :many
select i.id::text from invocations i
		left join trigger_records tr on tr.trigger_id = sqlc.arg(trigger_id)
		  and tr.item_identifier = i.id::text
		where i.app_id = sqlc.arg(app_id) and i.source = 'queue' and i.state = 'pending'

          and i.environment_id is null
          and not exists (select 1 from invocation_environment_queue_receipts receipt where receipt.invocation_id=i.id)
          and not exists (select 1 from deployments stage
              where stage.app_id=i.app_id and stage.scope not in ('production','default')
                and exists (select 1 from jsonb_each_text(case when jsonb_typeof(i.headers)='object' then i.headers else '{}'::jsonb end) pin
                    where lower(pin.key)='x-gregale-revision' and translate(regexp_replace(lower(pin.value), '[[:space:]]|(^urn:uuid:)|[{}]', '', 'g'), '-', '')=replace(stage.id::text,'-','')))
          and not exists (select 1 from project_release_sets stage join apps owner
              on owner.project_id=stage.project_id and owner.account_id=stage.account_id
              where owner.id=i.app_id and stage.environment_slug not in ('production','default')
                and exists (select 1 from jsonb_each_text(case when jsonb_typeof(i.headers)='object' then i.headers else '{}'::jsonb end) pin
                    where lower(pin.key)='x-gregale-release' and translate(regexp_replace(lower(pin.value), '[[:space:]]|(^urn:uuid:)|[{}]', '', 'g'), '-', '')=replace(stage.id::text,'-','')))
		  and i.due_at <= clock_timestamp()
		  and (tr.id is null
		    or (tr.state in ('pending','retry') and tr.next_fire_at <= clock_timestamp())
		    or (tr.state = 'claimed' and tr.claim_expires_at <= clock_timestamp()))
		  and (i.work_policy_name is null or not exists (
		      select 1 from invocations older
		      where older.app_id = i.app_id
		        and older.work_policy_name = i.work_policy_name
		        and older.work_key_digest = i.work_key_digest
		        and older.work_sequence < i.work_sequence
		        and older.state in ('pending','dispatching')))
		  and (i.work_policy_name is null or not exists (
		      select 1 from trigger_records older
		      join triggers source on source.id=older.trigger_id
		      where source.app_id=i.app_id
		        and older.work_policy_name=i.work_policy_name
		        and older.work_key_digest=i.work_key_digest
		        and older.work_sequence<i.work_sequence
		        and older.state in ('pending','retry','claimed')))
		  and (i.work_fairness_limit is null or (
		      select count(*) from invocations active
		      where active.app_id = i.app_id
		        and active.work_policy_name = i.work_policy_name
		        and active.work_fairness_digest = i.work_fairness_digest
		        and active.state = 'dispatching'
		        and active.lease_expires_at > clock_timestamp()
		  ) + (
		      select count(*) from trigger_records active
		      join triggers source on source.id=active.trigger_id
		      where source.app_id=i.app_id
		        and active.work_policy_name=i.work_policy_name
		        and active.work_fairness_digest=i.work_fairness_digest
		        and active.state='claimed'
		        and active.claim_expires_at > clock_timestamp()
		  ) < i.work_fairness_limit)
		  and (i.queue_name = sqlc.arg(queue_name) or (i.queue_name = ''
		      and i.work_policy_name is null and not exists (
		      select 1 from triggers other where other.app_id = sqlc.arg(app_id)
		        and other.kind = 'queue' and other.enabled and other.source = 'queue'
		        and other.id <> sqlc.arg(trigger_id))))
		order by i.created_at, i.id limit sqlc.arg(candidate_limit);

-- name: ReleaseProductionNamedQueueClaims :exec
with targets as (
		select unnest(sqlc.arg(invocation_ids)::text[]) as id, unnest(sqlc.arg(attempts)::int[]) as attempt
	) update invocations i set state = 'pending', lease_expires_at = null
	  from targets where i.id::text = targets.id and i.attempts = targets.attempt
	    and i.app_id = sqlc.arg(app_id) and i.source = 'queue' and i.queue_name in (sqlc.arg(queue_name), '')
	    and i.state = 'dispatching'

          and i.environment_id is null
          and not exists (select 1 from invocation_environment_queue_receipts receipt where receipt.invocation_id=i.id)
          and not exists (select 1 from deployments stage
              where stage.app_id=i.app_id and stage.scope not in ('production','default')
                and exists (select 1 from jsonb_each_text(case when jsonb_typeof(i.headers)='object' then i.headers else '{}'::jsonb end) pin
                    where lower(pin.key)='x-gregale-revision' and translate(regexp_replace(lower(pin.value), '[[:space:]]|(^urn:uuid:)|[{}]', '', 'g'), '-', '')=replace(stage.id::text,'-','')))
          and not exists (select 1 from project_release_sets stage join apps owner
              on owner.project_id=stage.project_id and owner.account_id=stage.account_id
              where owner.id=i.app_id and stage.environment_slug not in ('production','default')
                and exists (select 1 from jsonb_each_text(case when jsonb_typeof(i.headers)='object' then i.headers else '{}'::jsonb end) pin
                    where lower(pin.key)='x-gregale-release' and translate(regexp_replace(lower(pin.value), '[[:space:]]|(^urn:uuid:)|[{}]', '', 'g'), '-', '')=replace(stage.id::text,'-','')));

-- name: ClaimProductionLegacyQueueInvocations :many
with claimed as (
			select i.id
			  from invocations i
			  left join trigger_records tr
			    on tr.trigger_id = sqlc.arg(trigger_id)
			   and tr.item_identifier = i.id::text
			 where i.app_id = sqlc.arg(app_id)

          and i.environment_id is null
          and not exists (select 1 from invocation_environment_queue_receipts receipt where receipt.invocation_id=i.id)
          and not exists (select 1 from deployments stage
              where stage.app_id=i.app_id and stage.scope not in ('production','default')
                and exists (select 1 from jsonb_each_text(case when jsonb_typeof(i.headers)='object' then i.headers else '{}'::jsonb end) pin
                    where lower(pin.key)='x-gregale-revision' and translate(regexp_replace(lower(pin.value), '[[:space:]]|(^urn:uuid:)|[{}]', '', 'g'), '-', '')=replace(stage.id::text,'-','')))
          and not exists (select 1 from project_release_sets stage join apps owner
              on owner.project_id=stage.project_id and owner.account_id=stage.account_id
              where owner.id=i.app_id and stage.environment_slug not in ('production','default')
                and exists (select 1 from jsonb_each_text(case when jsonb_typeof(i.headers)='object' then i.headers else '{}'::jsonb end) pin
                    where lower(pin.key)='x-gregale-release' and translate(regexp_replace(lower(pin.value), '[[:space:]]|(^urn:uuid:)|[{}]', '', 'g'), '-', '')=replace(stage.id::text,'-','')))
			   and i.source = sqlc.arg(source)
			   and (i.queue_name = sqlc.arg(queue_name) or (
				       i.queue_name = ''
				   and not exists (
				       select 1 from triggers other
				        where other.app_id = sqlc.arg(app_id)
				          and other.kind = 'queue'
				          and other.enabled
				          and other.source = sqlc.arg(source)
				          and other.id <> sqlc.arg(trigger_id)
				   )
			       ))
			   and i.state = 'pending'
			   and i.work_policy_name is null
			   and i.due_at <= now()
			   and (tr.id is null
			        or (tr.state in ('pending','retry') and tr.next_fire_at <= now())
			        or (tr.state = 'claimed' and tr.claim_expires_at <= now()))
			 order by i.created_at asc
			 limit sqlc.arg(batch_limit)
			 for update of i skip locked
		), updated as (
			update invocations i
			   set state = 'dispatching',
			       lease_expires_at = now() + interval '10 minutes',
			       received_at = coalesce(i.received_at, now()),
			       attempts = i.attempts + 1
			  from claimed c
			 where i.id = c.id
			returning i.id::text as id, i.payload::text as payload, i.headers::text as headers,
			           '{}'::text as metadata, i.created_at, i.attempts
		)
		select id, payload, headers, metadata, created_at, attempts
		  from updated
		 order by created_at asc, id asc;

-- name: FinishProductionQueueTriggerInvocations :exec
with targets as (
			select unnest(sqlc.arg(invocation_ids)::text[]) as id, unnest(sqlc.arg(attempts)::int[]) as attempt
		), finalized_invocations as (
		update invocations i
		   set state = sqlc.arg(invocation_state),
		       outcome = sqlc.arg(outcome),
		       result = sqlc.arg(result)::jsonb,
		       completed_at = now(),
		       lease_expires_at = null,
		       last_error = sqlc.arg(last_error)::text
		  from targets
		 where i.id::text = targets.id and i.attempts = targets.attempt
		   and i.app_id = sqlc.arg(app_id) and i.source = sqlc.arg(source) and i.state = 'dispatching'

          and i.environment_id is null
          and not exists (select 1 from invocation_environment_queue_receipts receipt where receipt.invocation_id=i.id)
          and not exists (select 1 from deployments stage
              where stage.app_id=i.app_id and stage.scope not in ('production','default')
                and exists (select 1 from jsonb_each_text(case when jsonb_typeof(i.headers)='object' then i.headers else '{}'::jsonb end) pin
                    where lower(pin.key)='x-gregale-revision' and translate(regexp_replace(lower(pin.value), '[[:space:]]|(^urn:uuid:)|[{}]', '', 'g'), '-', '')=replace(stage.id::text,'-','')))
          and not exists (select 1 from project_release_sets stage join apps owner
              on owner.project_id=stage.project_id and owner.account_id=stage.account_id
              where owner.id=i.app_id and stage.environment_slug not in ('production','default')
                and exists (select 1 from jsonb_each_text(case when jsonb_typeof(i.headers)='object' then i.headers else '{}'::jsonb end) pin
                    where lower(pin.key)='x-gregale-release' and translate(regexp_replace(lower(pin.value), '[[:space:]]|(^urn:uuid:)|[{}]', '', 'g'), '-', '')=replace(stage.id::text,'-','')))
		 returning i.id::text as id
		)
		update trigger_records tr
		   set state = sqlc.arg(record_state),
		       attempts = tr.attempts + case when sqlc.arg(record_state) = 'dead_letter' and tr.state <> 'dead_letter' then 1 else 0 end,
		       last_error = case when sqlc.arg(record_state) = 'dead_letter' then nullif(sqlc.arg(last_error)::text, '') else tr.last_error end,
		       last_dispatched_at = now()
		  from finalized_invocations finalized
		 where tr.trigger_id = sqlc.arg(trigger_id) and tr.item_identifier = finalized.id
		   and tr.state <> sqlc.arg(record_state);

-- name: RetryProductionQueueTriggerInvocations :exec
with targets as (
			select unnest(sqlc.arg(invocation_ids)::text[]) as id, unnest(sqlc.arg(attempts)::int[]) as attempt
		)
		update invocations i
		   set state = 'pending',
		       outcome = null,
		       completed_at = null,
		       due_at = coalesce((
		           select tr.next_fire_at
		             from trigger_records tr
		            where tr.trigger_id = sqlc.arg(trigger_id)
		              and tr.item_identifier = i.id::text
		       ), now() + interval '1 second'),
		       lease_expires_at = null,
		       last_error = sqlc.arg(last_error)::text
		  from targets
		 where i.id::text = targets.id and i.attempts = targets.attempt
		   and i.app_id = sqlc.arg(app_id)
		   and i.source = sqlc.arg(source)
		   and i.state = 'dispatching'

          and i.environment_id is null
          and not exists (select 1 from invocation_environment_queue_receipts receipt where receipt.invocation_id=i.id)
          and not exists (select 1 from deployments stage
              where stage.app_id=i.app_id and stage.scope not in ('production','default')
                and exists (select 1 from jsonb_each_text(case when jsonb_typeof(i.headers)='object' then i.headers else '{}'::jsonb end) pin
                    where lower(pin.key)='x-gregale-revision' and translate(regexp_replace(lower(pin.value), '[[:space:]]|(^urn:uuid:)|[{}]', '', 'g'), '-', '')=replace(stage.id::text,'-','')))
          and not exists (select 1 from project_release_sets stage join apps owner
              on owner.project_id=stage.project_id and owner.account_id=stage.account_id
              where owner.id=i.app_id and stage.environment_slug not in ('production','default')
                and exists (select 1 from jsonb_each_text(case when jsonb_typeof(i.headers)='object' then i.headers else '{}'::jsonb end) pin
                    where lower(pin.key)='x-gregale-release' and translate(regexp_replace(lower(pin.value), '[[:space:]]|(^urn:uuid:)|[{}]', '', 'g'), '-', '')=replace(stage.id::text,'-','')));

-- name: ListDueInvocationRows :many
select i.*
		  from invocations i
		 where i.state = 'pending' and i.due_at <= sqlc.arg(now_at)
		   and not exists (select 1 from invocation_environment_queue_receipts receipt where receipt.invocation_id=i.id)
		   and not exists (select 1 from queue_bindings b where i.source='queue' and b.app_id=i.app_id and (b.id=i.queue_binding_id or (i.queue_binding_id is null and b.deployment_scope='' and b.queue_name=i.queue_name)) and b.retired_at is not null)
		   and (i.source <> 'queue' or (i.queue_name = '' and i.queue_binding_id is null))
		   and (i.environment_id is not null or i.work_policy_name is not null or not exists (
		       select 1
		         from triggers t
		         where t.app_id = i.app_id
		          and t.kind = 'queue'
		          and t.enabled
		          and t.source = i.source
		   ))
		   and (i.work_policy_name is null or not exists (
		       select 1 from invocations older
		       where older.app_id = i.app_id
		         and older.work_policy_name = i.work_policy_name
		         and older.work_key_digest = i.work_key_digest
		         and ((older.work_sequence < i.work_sequence and older.state='pending') or older.state='dispatching')
		   ))
		   and (i.work_policy_name is null or not exists (
		       select 1 from trigger_records older
		       join triggers source on source.id=older.trigger_id
		       where source.app_id=i.app_id
		         and older.work_policy_name=i.work_policy_name
		         and older.work_key_digest=i.work_key_digest
		         and ((older.work_sequence<i.work_sequence and older.state in ('pending','retry')) or older.state='claimed')
		   ))
		   and (i.work_fairness_limit is null or ((
		       select count(*) from invocations active
		       where active.app_id = i.app_id
		         and active.work_policy_name = i.work_policy_name
		         and active.work_fairness_digest = i.work_fairness_digest
		         and active.state = 'dispatching'
		         and active.lease_expires_at > sqlc.arg(now_at)
		   ) + (
		       select count(*) from trigger_records active
		       join triggers source on source.id=active.trigger_id
		       where source.app_id=i.app_id
		         and active.work_policy_name=i.work_policy_name
		         and active.work_fairness_digest=i.work_fairness_digest
		         and active.state='claimed' and active.claim_expires_at > sqlc.arg(now_at)
		   )) < i.work_fairness_limit)
		 order by i.due_at
		 for update skip locked
		 limit sqlc.arg(batch_limit)::bigint;

-- name: ListDueInvocationRowsAfter :many
select i.*
		  from invocations i
		 where i.state = 'pending' and i.due_at <= sqlc.arg(now_at)
		   and not exists (select 1 from invocation_environment_queue_receipts receipt where receipt.invocation_id=i.id)
		   and not exists (select 1 from queue_bindings b where i.source='queue' and b.app_id=i.app_id and (b.id=i.queue_binding_id or (i.queue_binding_id is null and b.deployment_scope='' and b.queue_name=i.queue_name)) and b.retired_at is not null)
		   and (i.source <> 'queue' or (i.queue_name = '' and i.queue_binding_id is null))
		   and (i.environment_id is not null or i.work_policy_name is not null or not exists (
		       select 1
		         from triggers t
		         where t.app_id = i.app_id
		          and t.kind = 'queue'
		          and t.enabled
		          and t.source = i.source
		   ))
		   and (i.work_policy_name is null or not exists (
		       select 1 from invocations older
		       where older.app_id = i.app_id
		         and older.work_policy_name = i.work_policy_name
		         and older.work_key_digest = i.work_key_digest
		         and ((older.work_sequence < i.work_sequence and older.state='pending') or older.state='dispatching')
		   ))
		   and (i.work_policy_name is null or not exists (
		       select 1 from trigger_records older
		       join triggers source on source.id=older.trigger_id
		       where source.app_id=i.app_id
		         and older.work_policy_name=i.work_policy_name
		         and older.work_key_digest=i.work_key_digest
		         and ((older.work_sequence<i.work_sequence and older.state in ('pending','retry')) or older.state='claimed')
		   ))
		   and (i.work_fairness_limit is null or ((
		       select count(*) from invocations active
		       where active.app_id = i.app_id
		         and active.work_policy_name = i.work_policy_name
		         and active.work_fairness_digest = i.work_fairness_digest
		         and active.state = 'dispatching'
		         and active.lease_expires_at > sqlc.arg(now_at)
		   ) + (
		       select count(*) from trigger_records active
		       join triggers source on source.id=active.trigger_id
		       where source.app_id=i.app_id
		         and active.work_policy_name=i.work_policy_name
		         and active.work_fairness_digest=i.work_fairness_digest
		         and active.state='claimed' and active.claim_expires_at > sqlc.arg(now_at)
		   )) < i.work_fairness_limit)
		   and (sqlc.arg(first_page)::boolean or (i.due_at, i.id) > (sqlc.arg(after_due_at)::timestamptz, sqlc.arg(after_id)::uuid))
		 order by i.due_at, i.id
		 for update skip locked
		 limit sqlc.arg(batch_limit)::bigint;

-- name: ReadProductionQueueTriggerInvocation :one
SELECT i.* FROM invocations i WHERE i.id=sqlc.arg(invocation_id) AND i.app_id=sqlc.arg(app_id) AND i.source='queue'
          and i.environment_id is null
          and not exists (select 1 from invocation_environment_queue_receipts receipt where receipt.invocation_id=i.id)
          and not exists (select 1 from deployments stage
              where stage.app_id=i.app_id and stage.scope not in ('production','default')
                and exists (select 1 from jsonb_each_text(case when jsonb_typeof(i.headers)='object' then i.headers else '{}'::jsonb end) pin
                    where lower(pin.key)='x-gregale-revision' and translate(regexp_replace(lower(pin.value), '[[:space:]]|(^urn:uuid:)|[{}]', '', 'g'), '-', '')=replace(stage.id::text,'-','')))
          and not exists (select 1 from project_release_sets stage join apps owner
              on owner.project_id=stage.project_id and owner.account_id=stage.account_id
              where owner.id=i.app_id and stage.environment_slug not in ('production','default')
                and exists (select 1 from jsonb_each_text(case when jsonb_typeof(i.headers)='object' then i.headers else '{}'::jsonb end) pin
                    where lower(pin.key)='x-gregale-release' and translate(regexp_replace(lower(pin.value), '[[:space:]]|(^urn:uuid:)|[{}]', '', 'g'), '-', '')=replace(stage.id::text,'-','')));

-- name: LockProductionQueueTriggerInvocationRow :one
SELECT i.id FROM invocations i WHERE i.id=sqlc.arg(invocation_id) AND i.app_id=sqlc.arg(app_id) AND i.source='queue' AND i.state='pending'
          and i.environment_id is null
          and not exists (select 1 from invocation_environment_queue_receipts receipt where receipt.invocation_id=i.id)
          and not exists (select 1 from deployments stage
              where stage.app_id=i.app_id and stage.scope not in ('production','default')
                and exists (select 1 from jsonb_each_text(case when jsonb_typeof(i.headers)='object' then i.headers else '{}'::jsonb end) pin
                    where lower(pin.key)='x-gregale-revision' and translate(regexp_replace(lower(pin.value), '[[:space:]]|(^urn:uuid:)|[{}]', '', 'g'), '-', '')=replace(stage.id::text,'-','')))
          and not exists (select 1 from project_release_sets stage join apps owner
              on owner.project_id=stage.project_id and owner.account_id=stage.account_id
              where owner.id=i.app_id and stage.environment_slug not in ('production','default')
                and exists (select 1 from jsonb_each_text(case when jsonb_typeof(i.headers)='object' then i.headers else '{}'::jsonb end) pin
                    where lower(pin.key)='x-gregale-release' and translate(regexp_replace(lower(pin.value), '[[:space:]]|(^urn:uuid:)|[{}]', '', 'g'), '-', '')=replace(stage.id::text,'-',''))) FOR UPDATE OF i SKIP LOCKED;

-- name: LockProductionQueueBindingCap :one
select max_concurrency from queue_bindings
		where app_id = sqlc.arg(app_id) and queue_name = sqlc.arg(queue_name) and enabled for update;

-- name: CountProductionQueueBindingActive :one
select count(*) from invocations i
			where i.app_id = sqlc.arg(app_id) and source = 'queue' and queue_name = sqlc.arg(queue_name)
			  and state = 'dispatching' and lease_expires_at > clock_timestamp()
          and i.environment_id is null
          and not exists (select 1 from invocation_environment_queue_receipts receipt where receipt.invocation_id=i.id)
          and not exists (select 1 from deployments stage
              where stage.app_id=i.app_id and stage.scope not in ('production','default')
                and exists (select 1 from jsonb_each_text(case when jsonb_typeof(i.headers)='object' then i.headers else '{}'::jsonb end) pin
                    where lower(pin.key)='x-gregale-revision' and translate(regexp_replace(lower(pin.value), '[[:space:]]|(^urn:uuid:)|[{}]', '', 'g'), '-', '')=replace(stage.id::text,'-','')))
          and not exists (select 1 from project_release_sets stage join apps owner
              on owner.project_id=stage.project_id and owner.account_id=stage.account_id
              where owner.id=i.app_id and stage.environment_slug not in ('production','default')
                and exists (select 1 from jsonb_each_text(case when jsonb_typeof(i.headers)='object' then i.headers else '{}'::jsonb end) pin
                    where lower(pin.key)='x-gregale-release' and translate(regexp_replace(lower(pin.value), '[[:space:]]|(^urn:uuid:)|[{}]', '', 'g'), '-', '')=replace(stage.id::text,'-','')));

-- name: ClaimProductionQueueTriggerInvocation :one
update invocations i set state = 'dispatching',
		lease_expires_at = clock_timestamp() + sqlc.arg(lease)::text::interval,
		received_at = coalesce(i.received_at, clock_timestamp()),
		attempts = i.attempts + 1
		where i.id = sqlc.arg(invocation_id) and i.app_id = sqlc.arg(app_id) and i.source = 'queue'
		  and i.state = 'pending' and i.due_at <= clock_timestamp()
          and i.environment_id is null
          and not exists (select 1 from invocation_environment_queue_receipts receipt where receipt.invocation_id=i.id)
          and not exists (select 1 from deployments stage
              where stage.app_id=i.app_id and stage.scope not in ('production','default')
                and exists (select 1 from jsonb_each_text(case when jsonb_typeof(i.headers)='object' then i.headers else '{}'::jsonb end) pin
                    where lower(pin.key)='x-gregale-revision' and translate(regexp_replace(lower(pin.value), '[[:space:]]|(^urn:uuid:)|[{}]', '', 'g'), '-', '')=replace(stage.id::text,'-','')))
          and not exists (select 1 from project_release_sets stage join apps owner
              on owner.project_id=stage.project_id and owner.account_id=stage.account_id
              where owner.id=i.app_id and stage.environment_slug not in ('production','default')
                and exists (select 1 from jsonb_each_text(case when jsonb_typeof(i.headers)='object' then i.headers else '{}'::jsonb end) pin
                    where lower(pin.key)='x-gregale-release' and translate(regexp_replace(lower(pin.value), '[[:space:]]|(^urn:uuid:)|[{}]', '', 'g'), '-', '')=replace(stage.id::text,'-','')))
		  and (i.queue_name = sqlc.arg(queue_name) or (i.queue_name = ''
		      and i.work_policy_name is null and not exists (
		      select 1 from triggers other where other.app_id = sqlc.arg(app_id)
		        and other.kind = 'queue' and other.enabled and other.source = 'queue'
		        and other.id <> sqlc.arg(trigger_id))))
		  and not exists (select 1 from trigger_records tr
		      where tr.trigger_id = sqlc.arg(trigger_id) and tr.item_identifier = i.id::text
		        and not ((tr.state in ('pending','retry') and tr.next_fire_at <= clock_timestamp())
		          or (tr.state = 'claimed' and tr.claim_expires_at <= clock_timestamp())))
		returning i.*;

-- ADR-531: queue ownership is operational evidence, never a cloned message.
-- name: CreateInvocationEnvironmentQueueAdmission :execrows
WITH admitted AS (
    UPDATE invocations SET environment_id=sqlc.arg(environment_id)::uuid,created_at=sqlc.arg(admitted_at)::timestamptz
    WHERE id=sqlc.arg(invocation_id)::uuid AND app_id=sqlc.arg(app_id)::uuid AND account_id=sqlc.arg(account_id)::uuid
        AND source='queue' AND queue_name=sqlc.arg(queue_name)::text AND state='pending'
    RETURNING id
)
INSERT INTO invocation_environment_queue_admissions
    (invocation_id,environment_id,account_id,app_id,consumer_id,runtime_set_id,deployment_id,workload_spec_id,
     pin_hash,settings_hash,definition_hash,queue_name,admitted_at)
SELECT id,sqlc.arg(environment_id)::uuid,sqlc.arg(account_id)::uuid,sqlc.arg(app_id)::uuid,
    sqlc.arg(consumer_id)::uuid,sqlc.arg(runtime_set_id)::uuid,sqlc.arg(deployment_id)::uuid,sqlc.arg(workload_spec_id)::uuid,
    sqlc.arg(pin_hash)::text,sqlc.arg(settings_hash)::text,sqlc.arg(definition_hash)::text,sqlc.arg(queue_name)::text,sqlc.arg(admitted_at)::timestamptz
FROM admitted;

-- name: ReadInvocationEnvironmentQueueAdmission :one
SELECT * FROM invocation_environment_queue_admissions WHERE invocation_id=$1 FOR SHARE;

-- name: ReadEnvironmentQueueInvocation :one
SELECT * FROM invocations WHERE id=$1;

-- name: ValidateInvocationEnvironmentQueuePin :one
SELECT EXISTS(SELECT 1 FROM deployments d JOIN apps a ON a.id=d.app_id
    JOIN project_environments e ON e.project_id=a.project_id AND e.account_id=a.account_id AND e.slug=d.scope
    WHERE e.id=i.environment_id AND a.id=i.app_id AND a.account_id=i.account_id AND a.status<>'deleted'
        AND e.slug NOT IN ('production','default') AND d.id=p.deployment_id
        AND (NOT sqlc.arg(require_live)::boolean OR d.status='live')
        AND ((d.id::text=lower(i.headers->>'X-Gregale-Revision') AND NOT i.headers ? 'X-Gregale-Release')
            OR (NOT i.headers ? 'X-Gregale-Revision' AND EXISTS(SELECT 1 FROM project_release_sets r
                JOIN project_release_members m ON m.release_id=r.id AND m.app_id=a.id AND m.deployment_id=d.id
                WHERE r.id::text=lower(i.headers->>'X-Gregale-Release') AND r.account_id=e.account_id AND r.project_id=e.project_id
                    AND r.environment_slug=e.slug AND (NOT sqlc.arg(require_live)::boolean OR r.expires_at IS NULL OR r.expires_at>now())))))::boolean AS pin_valid
FROM invocations i JOIN invocation_environment_queue_admissions p ON p.invocation_id=i.id WHERE i.id=sqlc.arg(invocation_id)::uuid;

-- name: EnvironmentQueueClaimCapacity :one
SELECT (SELECT count(*) FROM invocation_environment_queue_admissions other JOIN invocations i ON i.id=other.invocation_id
    WHERE other.environment_id=p.environment_id AND other.app_id=p.app_id AND other.queue_name=p.queue_name
        AND (i.state='dispatching' OR i.quota_reserved))::bigint AS active,
    (c.definition->>'max_concurrency')::integer AS max_concurrency
FROM invocation_environment_queue_admissions p JOIN project_environment_queue_consumers c ON c.id=p.consumer_id
WHERE p.invocation_id=$1;

-- name: ListEnvironmentQueueCleanupInvocations :many
SELECT i.id FROM invocations i JOIN invocation_environment_queue_admissions p ON p.invocation_id=i.id
WHERE i.environment_id=$1 OR p.environment_id=$1 ORDER BY i.id;

-- name: ReadEnvironmentQueueAdmissionProject :one
SELECT project_id FROM project_environments WHERE id=$1;

-- ADR-531: production filtering precedes aggregates, limits, and cursor anchors.
-- name: ReadProductionQueueInvocation :one
SELECT i.* FROM invocations i JOIN production_invocation_work p ON p.id=i.id
WHERE i.id=$1 AND i.source='queue';

-- name: CountProductionPendingInvocations :one
SELECT count(*) FROM production_invocation_work WHERE app_id=$1 AND source=$2 AND state IN ('pending','dispatching');

-- name: CountProductionPendingQueueWorkInLane :one
SELECT count(*) FROM production_invocation_work WHERE app_id=$1 AND source='queue' AND state='pending'
    AND work_policy_name=$2 AND work_key_digest=$3;

-- name: ReadProductionQueueStateLive :one
SELECT count(*) AS depth,
    count(*) FILTER(WHERE state='dispatching' AND lease_expires_at IS NOT NULL AND lease_expires_at>now()) AS in_flight,
    min(created_at) FILTER(WHERE state='pending')::timestamptz AS oldest_pending_at
FROM production_invocation_work WHERE app_id=sqlc.arg(app_id)::uuid AND source='queue' AND state IN ('pending','dispatching')
    AND (NOT sqlc.arg(named)::boolean OR queue_name=sqlc.arg(queue_name)::text);

-- name: CountProductionQueueDeadLetter :one
SELECT count(*) FROM production_invocation_work WHERE app_id=sqlc.arg(app_id)::uuid AND source='queue' AND state='dead_letter'
    AND (NOT sqlc.arg(named)::boolean OR queue_name=sqlc.arg(queue_name)::text);

-- name: PeekProductionQueue :many
WITH anchor AS (SELECT created_at,id FROM production_invocation_work WHERE id=sqlc.narg(cursor_id)::uuid AND app_id=sqlc.arg(app_id)::uuid AND source='queue')
SELECT i.* FROM invocations i JOIN production_invocation_work p ON p.id=i.id
WHERE i.app_id=sqlc.arg(app_id)::uuid AND i.source='queue' AND i.state='pending'
    AND (sqlc.narg(cursor_id)::uuid IS NULL OR (i.created_at,i.id)>(SELECT created_at,id FROM anchor))
ORDER BY i.created_at,i.id LIMIT sqlc.arg(page_limit)::bigint;

-- name: ListProductionQueueDeadLetter :many
WITH anchor AS (SELECT created_at,id FROM production_invocation_work WHERE id=sqlc.narg(cursor_id)::uuid AND app_id=sqlc.arg(app_id)::uuid AND source='queue')
SELECT i.* FROM invocations i JOIN production_invocation_work p ON p.id=i.id
WHERE i.app_id=sqlc.arg(app_id)::uuid AND i.source='queue' AND i.state='dead_letter'
    AND (sqlc.narg(cursor_id)::uuid IS NULL OR (i.created_at,i.id)<(SELECT created_at,id FROM anchor))
ORDER BY i.created_at DESC,i.id DESC LIMIT sqlc.arg(page_limit)::bigint;

-- name: RetryProductionQueueDeadLetter :one
UPDATE invocations i SET state='pending',attempts=0,last_error=NULL,outcome=NULL,due_at=now(),lease_expires_at=NULL,
    instance_id=NULL,last_replayed_at=now(),completed_at=NULL,quota_reserved=false,replay_generation=i.replay_generation+1,work_decision=NULL,outcome_code=''
FROM production_invocation_work p WHERE p.id=i.id AND i.id=$1 AND i.account_id=$2 AND i.state='dead_letter' AND NOT EXISTS(SELECT 1 FROM customer_operation_executions e WHERE e.invocation_id=i.id)
RETURNING i.*;

-- name: ListProductionDeadLetterEvents :many
WITH anchor AS (SELECT last_failed_at,id FROM production_dead_letter_events
    WHERE id=sqlc.narg(cursor_id)::uuid AND (sqlc.narg(account_id)::uuid IS NULL OR account_id=sqlc.narg(account_id)::uuid)
    AND (sqlc.narg(app_id)::uuid IS NULL OR app_id=sqlc.narg(app_id)::uuid))
SELECT d.* FROM dead_letter_events d JOIN production_dead_letter_events p ON p.id=d.id
WHERE (sqlc.narg(account_id)::uuid IS NULL OR d.account_id=sqlc.narg(account_id)::uuid)
    AND (sqlc.narg(app_id)::uuid IS NULL OR d.app_id=sqlc.narg(app_id)::uuid)
    AND (sqlc.narg(cursor_id)::uuid IS NULL OR (d.last_failed_at,d.id)<(SELECT last_failed_at,id FROM anchor))
ORDER BY d.last_failed_at DESC,d.id DESC LIMIT sqlc.arg(page_limit)::bigint;

-- name: ReadProductionDeadLetterEvent :one
SELECT d.* FROM dead_letter_events d JOIN production_dead_letter_events p ON p.id=d.id
WHERE d.id=sqlc.arg(event_id)::uuid
    AND (sqlc.narg(account_id)::uuid IS NULL OR d.account_id=sqlc.narg(account_id)::uuid)
    AND (sqlc.narg(app_id)::uuid IS NULL OR d.app_id=sqlc.narg(app_id)::uuid);

-- name: LockProductionDeadLetterEvent :one
SELECT d.* FROM dead_letter_events d JOIN production_dead_letter_events p ON p.id=d.id
WHERE d.id=sqlc.arg(event_id)::uuid AND d.account_id=sqlc.arg(account_id)::uuid
    AND (sqlc.narg(app_id)::uuid IS NULL OR d.app_id=sqlc.narg(app_id)::uuid) AND d.replayed_at IS NULL
FOR UPDATE OF d;

-- name: LockProductionDeadLetterEvents :many
SELECT d.* FROM dead_letter_events d JOIN production_dead_letter_events p ON p.id=d.id
WHERE d.account_id=sqlc.arg(account_id)::uuid
    AND (sqlc.narg(app_id)::uuid IS NULL OR d.app_id=sqlc.narg(app_id)::uuid)
    AND (NOT sqlc.arg(open_only)::boolean OR d.replayed_at IS NULL)
    AND d.id=ANY(sqlc.arg(event_ids)::uuid[])
ORDER BY d.last_failed_at DESC,d.id DESC LIMIT sqlc.arg(page_limit)::bigint FOR UPDATE OF d SKIP LOCKED;

-- name: DeleteProductionDeadLetterEvent :execrows
DELETE FROM dead_letter_events d USING production_dead_letter_events p WHERE p.id=d.id
    AND d.id=sqlc.arg(event_id)::uuid AND d.account_id=sqlc.arg(account_id)::uuid
    AND (sqlc.narg(app_id)::uuid IS NULL OR d.app_id=sqlc.narg(app_id)::uuid);

-- name: ReplayProductionDeadLetterInvocation :execrows
UPDATE invocations i SET state='pending',attempts=0,last_error=NULL,outcome=NULL,due_at=now(),lease_expires_at=NULL,
    instance_id=NULL,last_replayed_at=now(),completed_at=NULL,quota_reserved=false,replay_generation=i.replay_generation+1,work_decision=NULL,outcome_code=''
FROM production_invocation_work p WHERE p.id=i.id AND i.id=sqlc.arg(invocation_id)::uuid
    AND i.account_id=sqlc.arg(account_id)::uuid AND i.app_id=sqlc.arg(app_id)::uuid AND i.state='dead_letter' AND NOT EXISTS(SELECT 1 FROM customer_operation_executions e WHERE e.invocation_id=i.id);

-- name: DeleteProductionDeadLetterEvents :execrows
WITH victims AS (SELECT d.id FROM dead_letter_events d JOIN production_dead_letter_events p ON p.id=d.id
    WHERE d.account_id=sqlc.arg(account_id)::uuid AND (sqlc.narg(app_id)::uuid IS NULL OR d.app_id=sqlc.narg(app_id)::uuid)
    ORDER BY d.last_failed_at DESC,d.id DESC LIMIT sqlc.arg(page_limit)::bigint FOR UPDATE OF d SKIP LOCKED)
DELETE FROM dead_letter_events d USING victims v WHERE d.id=v.id;

-- name: StampDeadLetterEventReplay :exec
UPDATE dead_letter_events SET replayed_at=$1 WHERE id=$2;

-- ADR-531: serialize stage producers before app/deployment locks, across
-- every binding and deployment generation in the environment's workload.
-- name: LockEnvironmentQueueProducer :one
SELECT e.id FROM project_environments e JOIN apps a ON a.project_id=e.project_id AND a.account_id=e.account_id
WHERE e.id=sqlc.arg(environment_id)::uuid AND e.account_id=sqlc.arg(account_id)::uuid
    AND e.project_id=sqlc.arg(project_id)::uuid AND a.id=sqlc.arg(app_id)::uuid
    AND a.status<>'deleted' AND e.slug NOT IN ('production','default')
FOR NO KEY UPDATE OF e;

-- name: CountEnvironmentQueueProducerDepth :one
WITH owned AS (
    SELECT invocation_id AS id FROM invocation_environment_queue_admissions
    WHERE environment_id=sqlc.arg(environment_id)::uuid AND app_id=sqlc.arg(app_id)::uuid
    UNION
    SELECT id FROM invocations WHERE environment_id=sqlc.arg(environment_id)::uuid
        AND app_id=sqlc.arg(app_id)::uuid AND source='queue'
)
SELECT count(*) FROM invocations i JOIN owned o ON o.id=i.id
WHERE i.state IN ('pending','dispatching') OR i.quota_reserved;

-- name: ReadEnvironmentQueueProducerPlan :one
SELECT plan FROM accounts WHERE id=$1;

-- ADR-531: private stage transport, independent of the legacy completion inbox.
-- name: ReadEnvironmentQueueDeliveryAccount :one
SELECT plan,status,abuse_hold_at FROM accounts WHERE id=$1;

-- name: NextEnvironmentQueueDeliveryInvocation :one
SELECT i.* FROM invocations i JOIN invocation_environment_queue_admissions p ON p.invocation_id=i.id
WHERE p.consumer_id=sqlc.arg(consumer_id)::uuid AND p.runtime_set_id=sqlc.arg(runtime_set_id)::uuid
    AND i.state='pending' AND i.due_at<=now() AND (i.deadline_at IS NULL OR i.deadline_at>now())
ORDER BY i.due_at,i.created_at,i.id LIMIT 1;

-- name: EnsureEnvironmentQueueDeliveryQuota :exec
INSERT INTO account_async_quota(account_id,max_inflight) VALUES($1,$2)
ON CONFLICT(account_id) DO UPDATE SET updated_at=now();

-- name: ReserveEnvironmentQueueDeliveryQuota :one
UPDATE account_async_quota SET current_inflight=current_inflight+1,updated_at=now()
WHERE account_id=$1 AND current_inflight<max_inflight RETURNING current_inflight;

-- name: ReleaseEnvironmentQueueDeliveryQuota :exec
UPDATE account_async_quota SET current_inflight=greatest(current_inflight-1,0),updated_at=now() WHERE account_id=$1;

-- name: ClaimEnvironmentQueueDeliveryInvocation :one
WITH delivery_clock AS MATERIALIZED (SELECT clock_timestamp() AS at)
UPDATE invocations i SET state='dispatching',quota_reserved=true,received_at=delivery_clock.at,
    lease_expires_at=delivery_clock.at+make_interval(secs => sqlc.arg(lease_seconds)::integer),attempts=i.attempts+1
FROM invocation_environment_queue_admissions p, delivery_clock WHERE p.invocation_id=i.id AND i.id=sqlc.arg(invocation_id)::uuid
    AND p.consumer_id=sqlc.arg(consumer_id)::uuid AND p.runtime_set_id=sqlc.arg(runtime_set_id)::uuid
    AND i.state='pending' AND NOT i.quota_reserved AND i.due_at<=delivery_clock.at AND (i.deadline_at IS NULL OR i.deadline_at>delivery_clock.at)
RETURNING i.*;

-- name: UpsertEnvironmentQueueDeliveryReceipt :execrows
INSERT INTO invocation_environment_queue_receipts(invocation_id,attempt,token_hash,owner_hash,issued_at,lease_expires_at)
VALUES($1,$2,$3,$4,$5,$6)
ON CONFLICT(invocation_id) DO UPDATE SET attempt=excluded.attempt,token_hash=excluded.token_hash,
    issued_at=excluded.issued_at,lease_expires_at=excluded.lease_expires_at
WHERE invocation_environment_queue_receipts.attempt<excluded.attempt
    AND invocation_environment_queue_receipts.owner_hash=excluded.owner_hash;

-- name: ReadEnvironmentQueueDeliveryReceipt :one
SELECT * FROM invocation_environment_queue_receipts WHERE invocation_id=$1;

-- name: EnvironmentQueueDeliveryReceiptExists :one
SELECT EXISTS(SELECT 1 FROM invocation_environment_queue_receipts WHERE invocation_id=$1);

-- name: LockEnvironmentQueueDeliveryInvocation :one
SELECT i.* FROM invocations i WHERE i.id=$1 FOR UPDATE OF i;

-- name: EnvironmentQueueDeliveryClock :one
SELECT clock_timestamp()::timestamptz AS wall_time;

-- name: FinishEnvironmentQueueDeliveryInvocation :execrows
UPDATE invocations SET state=sqlc.arg(state)::text,quota_reserved=false,outcome=sqlc.narg(outcome)::text,
    last_error=sqlc.arg(last_error)::text,completed_at=sqlc.narg(completed_at)::timestamptz,
    due_at=sqlc.arg(due_at)::timestamptz,lease_expires_at=sqlc.narg(lease_expires_at)::timestamptz,
    instance_id=sqlc.narg(instance_id)::uuid,result=COALESCE(sqlc.narg(result)::jsonb,result)
WHERE id=sqlc.arg(invocation_id)::uuid AND state='dispatching' AND quota_reserved
    AND attempts=sqlc.arg(attempt)::integer AND lease_expires_at>clock_timestamp()
    AND (deadline_at IS NULL OR deadline_at>clock_timestamp());

-- name: LockLegacyInvocationReceiptFence :one
SELECT id FROM invocations WHERE id=$1 FOR UPDATE;

-- name: ExhaustEnvironmentQueueDeliveryInvocation :execrows
UPDATE invocations i SET state='dead_letter',outcome='dead_letter',completed_at=clock_timestamp(),
    last_error='queue delivery attempt budget exhausted after lease recovery',lease_expires_at=NULL,instance_id=NULL
FROM invocation_environment_queue_admissions p WHERE p.invocation_id=i.id AND i.id=sqlc.arg(invocation_id)::uuid
    AND p.consumer_id=sqlc.arg(consumer_id)::uuid AND p.runtime_set_id=sqlc.arg(runtime_set_id)::uuid
    AND i.state='pending' AND NOT i.quota_reserved AND i.attempts=sqlc.arg(attempt)::integer;

-- name: ReadRuntimeAppEnvForDeployment :one
WITH owner AS (
    SELECT d.id, a.id AS app_id, a.account_id,
        COALESCE(NULLIF(d.scope,''),'default')::text AS scope,
        COALESCE(bound.id::text,legacy.id::text,'')::text AS environment_id
    FROM deployments d
    JOIN apps a ON a.id=d.app_id
    LEFT JOIN projects project ON project.id=a.project_id AND project.account_id=a.account_id
    LEFT JOIN deployment_runtime_environment_owners runtime_owner ON runtime_owner.deployment_id=d.id
    LEFT JOIN project_environment_workload_deployment_specs pin ON pin.deployment_id=d.id
    LEFT JOIN project_environment_workload_specs spec ON spec.id=pin.spec_id AND spec.app_id=a.id
    LEFT JOIN project_environments bound ON bound.id=runtime_owner.environment_id
        AND bound.account_id=a.account_id AND bound.project_id=a.project_id
        AND bound.slug=CASE WHEN COALESCE(NULLIF(d.scope,''),'default')='default' THEN 'production' ELSE d.scope END
    LEFT JOIN project_environments legacy ON runtime_owner.deployment_id IS NULL AND pin.deployment_id IS NULL
        AND COALESCE(NULLIF(d.scope,''),'default') NOT IN ('default','production')
        AND legacy.account_id=a.account_id AND legacy.project_id=a.project_id AND legacy.slug=d.scope
        AND legacy.created_at<=d.created_at
    WHERE a.account_id=sqlc.arg(account_id)::uuid AND a.id=sqlc.arg(app_id)::uuid
        AND d.id=sqlc.arg(deployment_id)::uuid AND a.status<>'deleted'
        AND (a.project_id IS NULL OR project.id IS NOT NULL)
        AND d.status IN ('pending','building','imaging','snapshotting','live','superseded')
        AND ((runtime_owner.deployment_id IS NOT NULL AND bound.id IS NOT NULL AND spec.environment_id=bound.id)
            OR (runtime_owner.deployment_id IS NULL AND pin.deployment_id IS NULL
                AND (COALESCE(NULLIF(d.scope,''),'default') IN ('default','production') OR a.project_id IS NULL OR legacy.id IS NOT NULL)))
)
SELECT owner.scope, owner.environment_id,
    COALESCE((SELECT jsonb_agg(jsonb_build_object('key',v.key,'value',v.value,'created_at',v.created_at,'updated_at',v.updated_at) ORDER BY v.key)
        FROM app_envs v WHERE v.account_id=owner.account_id AND v.app_id=owner.app_id AND v.scope=owner.scope),'[]'::jsonb)::jsonb AS values
FROM owner;

-- name: LockRuntimeSecretEnvironment :one
SELECT e.id FROM project_environments e JOIN apps a ON a.project_id=e.project_id AND a.account_id=e.account_id
WHERE e.id=sqlc.arg(environment_id)::uuid AND e.account_id=sqlc.arg(account_id)::uuid AND a.id=sqlc.arg(app_id)::uuid
    AND e.slug=CASE WHEN sqlc.arg(scope)::text='default' THEN 'production' ELSE sqlc.arg(scope)::text END
FOR SHARE OF e;

-- name: LockRuntimeSecretOwner :one
SELECT d.id AS deployment_id,COALESCE(NULLIF(d.scope,''),'default')::text AS scope,
    (COALESCE(NULLIF(d.scope,''),'default') NOT IN ('default','production')
        OR EXISTS(SELECT 1 FROM deployment_runtime_environment_owners o WHERE o.deployment_id=d.id)
        OR EXISTS(SELECT 1 FROM project_environment_workload_deployment_specs p WHERE p.deployment_id=d.id))::boolean AS requires_fence
FROM apps a JOIN deployments d ON d.app_id=a.id JOIN instances i ON i.deployment_id=d.id AND i.app_id=a.id
WHERE a.account_id=sqlc.arg(account_id)::uuid AND a.id=sqlc.arg(app_id)::uuid AND i.id=sqlc.arg(instance_id)::uuid AND a.status<>'deleted'
    AND d.status IN ('pending','building','imaging','snapshotting','live','superseded')
    AND (NOT sqlc.arg(require_active)::boolean OR i.state IN ('waking','cold_booting','running','draining','snapshotting','migrating','warm'))
FOR SHARE OF a,d FOR KEY SHARE OF i;

-- name: LockRuntimeSecretApp :one
SELECT id FROM apps WHERE account_id=sqlc.arg(account_id)::uuid AND id=sqlc.arg(app_id)::uuid AND status<>'deleted'
FOR SHARE;

-- name: LockRuntimeSecretConfigurationPins :many
SELECT p.spec_id FROM project_environment_workload_deployment_specs p
JOIN project_environment_workload_specs s ON s.id=p.spec_id
JOIN deployment_runtime_environment_owners o ON o.deployment_id=p.deployment_id
WHERE p.deployment_id=sqlc.arg(deployment_id)::uuid
FOR SHARE OF p,s,o;

-- name: LockRuntimeSecretRows :many
SELECT key FROM app_secrets WHERE account_id=sqlc.arg(account_id)::uuid AND app_id=sqlc.arg(app_id)::uuid AND scope=sqlc.arg(scope)::text
ORDER BY key FOR UPDATE;

-- name: LockRuntimeSecretSidecarSignals :many
SELECT sidecar_name FROM deployment_sidecar_secret_reload_signals WHERE deployment_id=sqlc.arg(deployment_id)::uuid
ORDER BY sidecar_name FOR SHARE;

-- name: RecordAppSecretRuntimeReloadSummary :execrows
UPDATE app_secrets SET last_runtime_reload_version=sqlc.arg(version)::bigint,last_runtime_reload_revision=sqlc.arg(revision)::text,
    last_runtime_reload_projection=sqlc.arg(projection)::text,last_runtime_reload_signal=sqlc.arg(signal)::text,
    last_runtime_reload_at=sqlc.arg(observed_at)::timestamptz,last_runtime_reload_error_code=nullif(sqlc.arg(error_code)::text,''),
    last_runtime_reload_instance_id=sqlc.arg(instance_id)::uuid
WHERE account_id=sqlc.arg(account_id)::uuid AND app_id=sqlc.arg(app_id)::uuid AND scope=sqlc.arg(scope)::text
    AND key=sqlc.arg(key)::text AND delivery_version=sqlc.arg(version)::bigint;

-- name: LockRuntimeSecretDeliveryAttempt :one
SELECT id FROM instances
WHERE id=sqlc.arg(instance_id)::uuid AND app_id=sqlc.arg(app_id)::uuid
    AND deployment_id=sqlc.arg(deployment_id)::uuid AND wake_id=sqlc.arg(wake_id)::uuid
    AND (state IN ('waking','cold_booting','running','draining','snapshotting','migrating','warm')
        OR (NOT sqlc.arg(require_active)::boolean AND state='failed'))
FOR SHARE;

-- name: ReadRuntimeSecretDeliveryVersions :many
SELECT key,delivery_version FROM app_secrets
WHERE account_id=sqlc.arg(account_id)::uuid AND app_id=sqlc.arg(app_id)::uuid AND scope=sqlc.arg(scope)::text
ORDER BY key;

-- name: RecordAppSecretDeliverySuccess :execrows
UPDATE app_secrets SET delivered_version=delivery_version,delivery_status='delivered',
    last_delivery_attempt_at=sqlc.arg(attempted_at)::timestamptz,last_delivered_at=sqlc.arg(attempted_at)::timestamptz,
    last_delivery_error_code=NULL,last_delivered_wake_id=sqlc.arg(wake_id)::text,last_delivered_instance_id=sqlc.arg(instance_id)::text
WHERE account_id=sqlc.arg(account_id)::uuid AND app_id=sqlc.arg(app_id)::uuid AND scope=sqlc.arg(scope)::text
    AND key=sqlc.arg(key)::text AND delivery_version=sqlc.arg(version)::bigint;

-- name: RecordAppSecretDeliveryFailure :execrows
UPDATE app_secrets SET delivery_status='failed',last_delivery_attempt_at=sqlc.arg(attempted_at)::timestamptz,
    last_delivery_error_code=sqlc.arg(error_code)::text
WHERE account_id=sqlc.arg(account_id)::uuid AND app_id=sqlc.arg(app_id)::uuid AND scope=sqlc.arg(scope)::text
    AND key=sqlc.arg(key)::text AND delivery_version=sqlc.arg(version)::bigint
    AND COALESCE(delivered_version,0)<delivery_version;

-- name: RecordAppSecretRuntimeReloadObservation :execrows
INSERT INTO app_secret_runtime_reload_observations(app_id,scope,key,instance_id,workload_name,secret_version,projection,signal,observed_at,error_code)
SELECT s.app_id,s.scope,s.key,i.id,sqlc.arg(workload_name)::text,sqlc.arg(version)::bigint,
    sqlc.arg(projection)::text,sqlc.arg(signal)::text,sqlc.arg(observed_at)::timestamptz,nullif(sqlc.arg(error_code)::text,'')
FROM app_secrets s JOIN instances i ON i.id=sqlc.arg(instance_id)::uuid AND i.app_id=s.app_id
WHERE s.account_id=sqlc.arg(account_id)::uuid AND s.app_id=sqlc.arg(app_id)::uuid AND s.scope=sqlc.arg(scope)::text
    AND s.key=sqlc.arg(key)::text AND s.delivery_version=sqlc.arg(version)::bigint
ON CONFLICT(app_id,scope,key,instance_id,workload_name) DO UPDATE
SET secret_version=excluded.secret_version,projection=excluded.projection,signal=excluded.signal,observed_at=excluded.observed_at,error_code=excluded.error_code,
    application_ack_version=CASE WHEN app_secret_runtime_reload_observations.application_ack_version>=excluded.secret_version THEN app_secret_runtime_reload_observations.application_ack_version END,
    application_ack_status=CASE WHEN app_secret_runtime_reload_observations.application_ack_version>=excluded.secret_version THEN app_secret_runtime_reload_observations.application_ack_status END,
    application_ack_at=CASE WHEN app_secret_runtime_reload_observations.application_ack_version>=excluded.secret_version THEN app_secret_runtime_reload_observations.application_ack_at END,
    application_ack_error_code=CASE WHEN app_secret_runtime_reload_observations.application_ack_version>=excluded.secret_version THEN app_secret_runtime_reload_observations.application_ack_error_code END
WHERE app_secret_runtime_reload_observations.secret_version<=excluded.secret_version;

-- name: RecordAppSecretRuntimeReloadApplicationAck :execrows
UPDATE app_secret_runtime_reload_observations o
SET application_ack_version=sqlc.arg(version)::bigint,application_ack_status=sqlc.arg(status)::text,
    application_ack_at=sqlc.arg(ack_at)::timestamptz,application_ack_error_code=nullif(sqlc.arg(error_code)::text,'')
WHERE o.app_id=sqlc.arg(app_id)::uuid AND o.scope=sqlc.arg(scope)::text AND o.key=sqlc.arg(key)::text
    AND o.instance_id=sqlc.arg(instance_id)::uuid AND o.workload_name=sqlc.arg(workload_name)::text
    AND o.secret_version<=sqlc.arg(version)::bigint AND coalesce(o.application_ack_version,0)<=sqlc.arg(version)::bigint
    AND EXISTS(SELECT 1 FROM app_secrets s JOIN instances i ON i.id=sqlc.arg(instance_id)::uuid AND i.app_id=s.app_id
        WHERE s.account_id=sqlc.arg(account_id)::uuid AND s.app_id=sqlc.arg(app_id)::uuid AND s.scope=sqlc.arg(scope)::text
            AND s.key=sqlc.arg(key)::text AND s.delivery_version=sqlc.arg(version)::bigint);

-- name: ReadRuntimeAppValuesForDeployment :one
WITH owner AS (
    SELECT d.id, a.id AS app_id, a.account_id,
        COALESCE(NULLIF(d.scope,''),'default')::text AS scope,
        COALESCE(bound.id::text,legacy.id::text,'')::text AS environment_id,
        d.environment_workload_runtime,d.override_env_secrets,d.sidecars,COALESCE(d.secret_reload_signal,'')::text AS reload_signal,
        COALESCE(spec.id::text,'')::text AS spec_id,COALESCE(spec.config_hash,'')::text AS settings_hash,
        COALESCE(spec.settings,(to_jsonb(a)||jsonb_build_object(
            'public_auth_basic_sealed',encode(a.public_auth_basic,'base64'),
            'only_allow_declared_routes',a.only_declared_routes,'retry_policy_json',a.retry_policy))::json)::json AS settings,
        (to_jsonb(d)||jsonb_build_object('secret_reload_signal_known',d.secret_reload_signal IS NOT NULL))::jsonb AS artifact
    FROM deployments d
    JOIN apps a ON a.id=d.app_id
    LEFT JOIN projects project ON project.id=a.project_id AND project.account_id=a.account_id
    LEFT JOIN deployment_runtime_environment_owners runtime_owner ON runtime_owner.deployment_id=d.id
    LEFT JOIN project_environment_workload_deployment_specs pin ON pin.deployment_id=d.id
    LEFT JOIN project_environment_workload_specs spec ON spec.id=pin.spec_id AND spec.app_id=a.id
    LEFT JOIN project_environments bound ON bound.id=runtime_owner.environment_id
        AND bound.account_id=a.account_id AND bound.project_id=a.project_id
        AND bound.slug=CASE WHEN COALESCE(NULLIF(d.scope,''),'default')='default' THEN 'production' ELSE d.scope END
    LEFT JOIN project_environments legacy ON runtime_owner.deployment_id IS NULL AND pin.deployment_id IS NULL
        AND COALESCE(NULLIF(d.scope,''),'default') NOT IN ('default','production')
        AND legacy.account_id=a.account_id AND legacy.project_id=a.project_id AND legacy.slug=d.scope
        AND legacy.created_at<=d.created_at
    WHERE a.account_id=sqlc.arg(account_id)::uuid AND a.id=sqlc.arg(app_id)::uuid
        AND d.id=sqlc.arg(deployment_id)::uuid AND a.status<>'deleted'
        AND (a.project_id IS NULL OR project.id IS NOT NULL)
        AND d.status IN ('pending','building','imaging','snapshotting','live','superseded')
        AND ((runtime_owner.deployment_id IS NOT NULL AND bound.id IS NOT NULL AND spec.environment_id=bound.id)
            OR (runtime_owner.deployment_id IS NULL AND pin.deployment_id IS NULL
                AND (COALESCE(NULLIF(d.scope,''),'default') IN ('default','production') OR a.project_id IS NULL OR legacy.id IS NOT NULL)))
)
SELECT owner.scope,owner.environment_id,owner.environment_workload_runtime,owner.override_env_secrets,owner.sidecars,owner.reload_signal,
    owner.spec_id,owner.settings_hash,owner.settings,owner.artifact,
    COALESCE((SELECT jsonb_agg(to_jsonb(layer) ORDER BY layer.sidecar_name)
        FROM deployment_sidecar_layers layer WHERE layer.deployment_id=owner.id),'[]'::jsonb)::jsonb AS layers,
    COALESCE((SELECT jsonb_agg(jsonb_build_object('key',v.key,'value',v.value,'created_at',v.created_at,'updated_at',v.updated_at) ORDER BY v.key)
        FROM app_envs v WHERE v.account_id=owner.account_id AND v.app_id=owner.app_id AND v.scope=owner.scope),'[]'::jsonb)::jsonb AS values,
    COALESCE((SELECT jsonb_agg(jsonb_build_object('key',s.key,'ciphertext',encode(s.ciphertext,'base64'),
        'secret_class',s.secret_class,'kid',s.kid,'value_hash',s.value_hash,'secret_version',s.secret_version,'delivery_version',s.delivery_version,
        'created_at',s.created_at,'updated_at',s.updated_at,'managed_postgres_binding_id',s.managed_postgres_binding_id,
        'managed_postgres_access',COALESCE((SELECT b.access FROM managed_postgres_bindings b WHERE b.id=s.managed_postgres_binding_id AND b.account_id=owner.account_id AND b.app_id=owner.app_id AND b.scope=owner.scope AND b.environment_key=s.key AND b.state<>'deleted'),''),
        'managed_credential_ref',s.managed_credential_ref,'managed_credential_generation',s.managed_credential_generation,
        'managed_object_storage_credential_id',s.managed_object_storage_credential_id) ORDER BY s.key)
        FROM app_secrets s WHERE s.account_id=owner.account_id AND s.app_id=owner.app_id AND s.scope=owner.scope),'[]'::jsonb)::jsonb AS secrets,
    COALESCE((SELECT jsonb_object_agg(signal.sidecar_name,signal.signal) FROM deployment_sidecar_secret_reload_signals signal
        WHERE signal.deployment_id=owner.id),'{}'::jsonb)::jsonb AS reload_signals
FROM owner;

-- name: LockRuntimeConfigWorkloadSpec :many
SELECT s.id FROM project_environment_workload_specs s
JOIN project_environment_workload_deployment_specs p ON p.spec_id=s.id
WHERE p.deployment_id=sqlc.arg(deployment_id)::uuid FOR SHARE OF s;

-- name: ReadSnapshotGarbageCollection :many
WITH metadata AS (
    SELECT s.id, s.deployment_id::text AS deployment_id, d.app_id::text AS app_id,
        a.account_id::text AS account_id, a.slug AS app_slug, a.status AS app_status,
        d.status AS deployment_status, s.fc_version, s.mem_bytes, s.disk_bytes,
        s.storage_key, s.stale, s.delete_pending, s.created_at, s.tier,
        COALESCE(NULLIF(d.scope,''),'default')::text AS scope,
        COALESCE(runtime_owner.environment_id::text,legacy.id::text,'')::text AS environment_id,
        COALESCE(d.rootfs_key,'')::text AS deployment_rootfs_key,
        CASE WHEN pin.deployment_id IS NOT NULL THEN COALESCE(spec.settings->>'warm_snapshot_enabled'='true',false)
            ELSE a.warm_snapshot_enabled END::boolean AS warm_snapshot_enabled,
        COALESCE(NOT (a.status<>'deleted'
            AND d.status IN ('pending','building','imaging','snapshotting','live','superseded')
            AND (a.project_id IS NULL OR project.id IS NOT NULL)
            AND ((runtime_owner.deployment_id IS NOT NULL AND bound.id IS NOT NULL AND spec.environment_id=bound.id)
                OR (runtime_owner.deployment_id IS NULL AND pin.deployment_id IS NULL
                    AND (COALESCE(NULLIF(d.scope,''),'default') IN ('default','production') OR a.project_id IS NULL OR legacy.id IS NOT NULL)))),true)::boolean AS runtime_owner_invalid
    FROM snapshots s
    JOIN deployments d ON d.id=s.deployment_id
    JOIN apps a ON a.id=d.app_id
    LEFT JOIN projects project ON project.id=a.project_id AND project.account_id=a.account_id
    LEFT JOIN deployment_runtime_environment_owners runtime_owner ON runtime_owner.deployment_id=d.id
    LEFT JOIN project_environment_workload_deployment_specs pin ON pin.deployment_id=d.id
    LEFT JOIN project_environment_workload_specs spec ON spec.id=pin.spec_id AND spec.app_id=a.id
    LEFT JOIN project_environments bound ON bound.id=runtime_owner.environment_id
        AND bound.account_id=a.account_id AND bound.project_id=a.project_id
        AND bound.slug=CASE WHEN COALESCE(NULLIF(d.scope,''),'default')='default' THEN 'production' ELSE d.scope END
    LEFT JOIN project_environments legacy ON runtime_owner.deployment_id IS NULL AND pin.deployment_id IS NULL
        AND COALESCE(NULLIF(d.scope,''),'default') NOT IN ('default','production')
        AND legacy.account_id=a.account_id AND legacy.project_id=a.project_id AND legacy.slug=d.scope
        AND legacy.created_at<=d.created_at
)
SELECT * FROM metadata
WHERE (sqlc.arg(mode)::text='active' AND (NOT stale OR runtime_owner_invalid))
    OR (sqlc.arg(mode)::text='stale' AND stale AND created_at<now()-sqlc.arg(retention_seconds)::bigint*interval '1 second')
    OR (sqlc.arg(mode)::text='pending' AND delete_pending)
ORDER BY CASE WHEN sqlc.arg(mode)::text='active' THEN created_at END DESC,
    CASE WHEN sqlc.arg(mode)::text<>'active' THEN created_at END ASC, id ASC
LIMIT 10000;

-- name: ReadRuntimeScalingStateForDeployment :one
WITH owner AS (
    SELECT d.id, a.id AS app_id, a.account_id,
        COALESCE(NULLIF(d.scope,''),'default')::text AS scope,
        COALESCE(bound.id::text,legacy.id::text,'')::text AS environment_id
    FROM deployments d
    JOIN apps a ON a.id=d.app_id
    LEFT JOIN projects project ON project.id=a.project_id AND project.account_id=a.account_id
    LEFT JOIN deployment_runtime_environment_owners runtime_owner ON runtime_owner.deployment_id=d.id
    LEFT JOIN project_environment_workload_deployment_specs pin ON pin.deployment_id=d.id
    LEFT JOIN project_environment_workload_specs spec ON spec.id=pin.spec_id AND spec.app_id=a.id
    LEFT JOIN project_environments bound ON bound.id=runtime_owner.environment_id
        AND bound.account_id=a.account_id AND bound.project_id=a.project_id
        AND bound.slug=CASE WHEN COALESCE(NULLIF(d.scope,''),'default')='default' THEN 'production' ELSE d.scope END
    LEFT JOIN project_environments legacy ON runtime_owner.deployment_id IS NULL AND pin.deployment_id IS NULL
        AND COALESCE(NULLIF(d.scope,''),'default') NOT IN ('default','production')
        AND legacy.account_id=a.account_id AND legacy.project_id=a.project_id AND legacy.slug=d.scope
        AND legacy.created_at<=d.created_at
    WHERE a.account_id=sqlc.arg(account_id)::uuid AND a.id=sqlc.arg(app_id)::uuid
        AND d.id=sqlc.arg(deployment_id)::uuid AND a.status<>'deleted'
        AND (a.project_id IS NULL OR project.id IS NOT NULL)
        AND d.status IN ('pending','building','imaging','snapshotting','live','superseded')
        AND ((runtime_owner.deployment_id IS NOT NULL AND bound.id IS NOT NULL AND spec.environment_id=bound.id)
            OR (runtime_owner.deployment_id IS NULL AND pin.deployment_id IS NULL
                AND (COALESCE(NULLIF(d.scope,''),'default') IN ('default','production') OR a.project_id IS NULL OR legacy.id IS NOT NULL)))
)
SELECT owner.scope,owner.environment_id,
    CASE WHEN scaling.app_id IS NOT NULL THEN scaling.last_scale_in_at
        WHEN owner.scope IN ('default','production') THEN a.last_scale_in_at END::timestamptz AS last_scale_in_at,
    CASE WHEN scaling.app_id IS NOT NULL THEN scaling.last_scale_out_at
        WHEN owner.scope IN ('default','production') THEN a.last_scale_out_at END::timestamptz AS last_scale_out_at
FROM owner JOIN apps a ON a.id=owner.app_id
LEFT JOIN runtime_environment_scaling_states scaling ON scaling.app_id=owner.app_id
    AND scaling.environment_key=CASE WHEN owner.environment_id<>'' THEN 'environment:'||owner.environment_id
        ELSE 'scope:'||CASE WHEN owner.scope='default' THEN 'production' ELSE owner.scope END END;

-- name: WriteRuntimeScalingState :exec
INSERT INTO runtime_environment_scaling_states(app_id,environment_key,environment_id,scope,last_scale_in_at,last_scale_out_at)
VALUES (sqlc.arg(app_id)::uuid,sqlc.arg(environment_key)::text,sqlc.narg(environment_id)::uuid,sqlc.arg(scope)::text,
    CASE WHEN sqlc.arg(direction)::text='in' THEN now() ELSE sqlc.narg(prior_scale_in)::timestamptz END,
    CASE WHEN sqlc.arg(direction)::text='out' THEN now() ELSE sqlc.narg(prior_scale_out)::timestamptz END)
ON CONFLICT(app_id,environment_key) DO UPDATE
SET last_scale_in_at=CASE WHEN sqlc.arg(direction)::text='in' THEN now() ELSE runtime_environment_scaling_states.last_scale_in_at END,
    last_scale_out_at=CASE WHEN sqlc.arg(direction)::text='out' THEN now() ELSE runtime_environment_scaling_states.last_scale_out_at END;

-- name: ProjectProductionScalingState :exec
UPDATE apps a SET last_scale_in_at=scaling.last_scale_in_at,last_scale_out_at=scaling.last_scale_out_at
FROM runtime_environment_scaling_states scaling
WHERE a.id=sqlc.arg(app_id)::uuid AND scaling.app_id=a.id AND scaling.environment_key=sqlc.arg(environment_key)::text
    AND scaling.scope='production';

-- name: StampLegacyProductionScaleOut :one
WITH app_stamp AS (UPDATE apps SET last_scale_out_at=now() WHERE id=$1 RETURNING id,last_scale_out_at),
    scaling_stamp AS (UPDATE runtime_environment_scaling_states scaling SET last_scale_out_at=app_stamp.last_scale_out_at
        FROM app_stamp WHERE scaling.app_id=app_stamp.id AND scaling.scope='production')
SELECT id FROM app_stamp;

-- name: SyncProductionScalingStates :exec
UPDATE runtime_environment_scaling_states scaling
SET last_scale_in_at=a.last_scale_in_at,last_scale_out_at=a.last_scale_out_at
FROM apps a
WHERE scaling.app_id=a.id AND a.id=sqlc.arg(app_id)::uuid AND scaling.scope='production';

-- name: StampLegacyProductionScaleIn :one
WITH app_stamp AS (UPDATE apps SET last_scale_in_at=now() WHERE id=$1 RETURNING id,last_scale_in_at),
    scaling_stamp AS (UPDATE runtime_environment_scaling_states scaling SET last_scale_in_at=app_stamp.last_scale_in_at
        FROM app_stamp WHERE scaling.app_id=app_stamp.id AND scaling.scope='production')
SELECT id FROM app_stamp;

-- name: FinishManagedPostgresCloneRestoreWithProof :one
WITH ready AS (
    UPDATE managed_postgres_databases d SET state='ready', observed_generation=desired_generation,
        data_resource_id=sqlc.arg(data_resource_id)::text, last_error_code=NULL, lease_token=NULL, lease_until=NULL, attempt_count=0,
        retry_at=sqlc.arg(observed_at)::timestamptz, updated_at=sqlc.arg(observed_at)::timestamptz
    WHERE d.id=sqlc.arg(database_id)::uuid AND d.account_id=sqlc.arg(account_id)::uuid
        AND d.state='provisioning' AND d.deleted_at IS NULL AND d.lease_token=sqlc.arg(lease_token)::text
        AND d.lease_until>sqlc.arg(observed_at)::timestamptz AND d.lease_until>clock_timestamp()
        AND d.environment_clone_operation_id=sqlc.arg(operation_id)::uuid
        AND d.backend_id=sqlc.arg(backend_id)::text AND d.backend_fingerprint=sqlc.arg(backend_fingerprint)::text
        AND d.provider_resource_id=sqlc.arg(provider_resource_id)::text
        AND (d.data_resource_id IS NULL OR d.data_resource_id=sqlc.arg(data_resource_id)::text)
        AND d.restore_source_database_id=sqlc.arg(source_database_id)::uuid
        AND d.restore_source_resource_id=sqlc.arg(source_resource_id)::text
        AND d.restore_point_in_time=sqlc.arg(point_in_time)::timestamptz
        AND d.desired_generation=sqlc.arg(generation)::bigint
        AND sqlc.arg(spec)::jsonb=jsonb_build_object('Region',d.region,'PostgresMajor',d.postgres_major,
            'Class',d.service_class,'Availability',d.availability,'ScaleToZero',d.scale_to_zero,
            'StorageLimitBytes',d.storage_limit_bytes,'RestoreWindowSeconds',d.restore_window_seconds)
    RETURNING d.*
)
INSERT INTO managed_postgres_restore_proofs(database_id,account_id,operation_id,backend_id,backend_fingerprint,
    provider_resource_id,source_database_id,source_resource_id,point_in_time,spec,generation,observed_at,data_resource_id)
SELECT id,account_id,environment_clone_operation_id,backend_id,backend_fingerprint,provider_resource_id,
    restore_source_database_id,restore_source_resource_id,restore_point_in_time,sqlc.arg(spec)::jsonb,
    observed_generation,sqlc.arg(observed_at)::timestamptz,data_resource_id FROM ready
ON CONFLICT(database_id) DO NOTHING RETURNING database_id;

-- name: ReadManagedPostgresCloneRestoreProof :one
SELECT p.* FROM managed_postgres_restore_proofs p
JOIN managed_postgres_databases d ON d.id=p.database_id
WHERE d.id=sqlc.arg(database_id)::uuid AND d.account_id=sqlc.arg(account_id)::uuid
    AND d.state='ready' AND d.deleted_at IS NULL AND d.lease_token IS NULL
    AND p.account_id=d.account_id AND p.operation_id=d.environment_clone_operation_id
    AND p.backend_id=d.backend_id AND p.backend_fingerprint=d.backend_fingerprint
    AND p.provider_resource_id=d.provider_resource_id AND p.data_resource_id=d.data_resource_id AND p.source_database_id=d.restore_source_database_id
    AND p.source_resource_id=d.restore_source_resource_id AND p.point_in_time=d.restore_point_in_time
    AND p.generation=d.desired_generation AND p.generation=d.observed_generation
    AND p.spec=jsonb_build_object('Region',d.region,'PostgresMajor',d.postgres_major,
        'Class',d.service_class,'Availability',d.availability,'ScaleToZero',d.scale_to_zero,
        'StorageLimitBytes',d.storage_limit_bytes,'RestoreWindowSeconds',d.restore_window_seconds)
FOR SHARE OF d,p;

-- name: IssueLockApp :one
SELECT id, account_id, org_id FROM apps WHERE id = sqlc.arg(app_id) FOR UPDATE;
-- name: IssueDeploymentScope :one
SELECT d.id, d.commit_sha, d.image_digest, d.created_at FROM deployments d JOIN apps a ON a.id = d.app_id
WHERE d.id = sqlc.arg(deployment_id) AND d.app_id = sqlc.arg(app_id) AND a.account_id = sqlc.arg(account_id)
AND (sqlc.arg(environment)::text = 'application' OR EXISTS (
 SELECT 1 FROM project_release_members rm JOIN project_release_sets rs ON rs.id = rm.release_id
 WHERE rm.app_id = a.id AND rm.deployment_id = d.id AND rs.environment_slug = sqlc.arg(environment)::text AND rs.account_id = a.account_id));
-- name: IssueFindGroup :one
SELECT * FROM app_issues WHERE app_id = sqlc.arg(app_id) AND environment = sqlc.arg(environment)
AND grouping_version = sqlc.arg(grouping_version) AND fingerprint = sqlc.arg(fingerprint) FOR UPDATE;
-- name: IssueGet :one
SELECT * FROM app_issues WHERE app_id = sqlc.arg(app_id) AND id = sqlc.arg(id);
-- name: IssueGetLocked :one
SELECT * FROM app_issues WHERE app_id = sqlc.arg(app_id) AND id = sqlc.arg(id) FOR UPDATE;
-- name: IssueCreate :one
INSERT INTO app_issues(account_id,app_id,environment,fingerprint,grouping_version,title,first_seen_at,last_seen_at)
VALUES(sqlc.arg(account_id),sqlc.arg(app_id),sqlc.arg(environment),sqlc.arg(fingerprint),sqlc.arg(grouping_version),sqlc.arg(title),sqlc.arg(occurred_at),sqlc.arg(occurred_at)) RETURNING *;
-- name: IssueList :many
SELECT * FROM app_issues WHERE app_id = sqlc.arg(app_id)
AND (sqlc.arg(state)::text = '' OR state = sqlc.arg(state))
AND (sqlc.arg(environment)::text = '' OR environment = sqlc.arg(environment))
AND (sqlc.narg(assignee_account_id)::uuid IS NULL OR assignee_account_id = sqlc.narg(assignee_account_id))
AND (NOT sqlc.arg(unassigned)::bool OR assignee_account_id IS NULL)
AND (sqlc.narg(cursor_time)::timestamptz IS NULL OR (last_seen_at,id) < (sqlc.narg(cursor_time),sqlc.narg(cursor_id)::uuid))
ORDER BY last_seen_at DESC,id DESC LIMIT sqlc.arg(page_limit);
-- name: IssueListByImpact :many
WITH candidate_issues AS MATERIALIZED (
    SELECT i.* FROM app_issues i
    WHERE i.app_id = sqlc.arg(app_id)
      AND (sqlc.arg(state)::text = '' OR i.state = sqlc.arg(state))
      AND (sqlc.arg(environment)::text = '' OR i.environment = sqlc.arg(environment))
      AND (sqlc.narg(assignee_account_id)::uuid IS NULL OR i.assignee_account_id = sqlc.narg(assignee_account_id))
      AND (NOT sqlc.arg(unassigned)::bool OR i.assignee_account_id IS NULL)
), issue_impact AS (
    SELECT e.issue_id,
           count(DISTINCT COALESCE(e.verified_platform_tenant_id,e.verified_consumer_id)) AS identified_customers,
           count(*) AS observed_events,
           count(*) FILTER(WHERE e.verified_platform_tenant_id IS NULL AND e.verified_consumer_id IS NULL) AS unattributed_events
      FROM candidate_issues i
      JOIN issue_events e ON e.issue_id = i.id
     WHERE e.occurred_at >= sqlc.arg(since)::timestamptz
       AND e.occurred_at <= sqlc.arg(until)::timestamptz
     GROUP BY e.issue_id
)
SELECT i.*,
       COALESCE(impact.identified_customers,0)::bigint AS identified_customers,
       COALESCE(impact.observed_events,0)::bigint AS observed_events,
       COALESCE(impact.unattributed_events,0)::bigint AS unattributed_events
  FROM candidate_issues i
  LEFT JOIN issue_impact impact ON impact.issue_id = i.id
 WHERE COALESCE(impact.identified_customers,0) >= sqlc.arg(min_customers)::bigint
   AND (
       (NOT sqlc.arg(sort_by_impact)::bool AND
        (sqlc.narg(cursor_time)::timestamptz IS NULL OR (i.last_seen_at,i.id) < (sqlc.narg(cursor_time),sqlc.narg(cursor_id)::uuid)))
       OR
       (sqlc.arg(sort_by_impact)::bool AND
        (sqlc.narg(cursor_customers)::bigint IS NULL OR
         COALESCE(impact.identified_customers,0) < sqlc.narg(cursor_customers) OR
         (COALESCE(impact.identified_customers,0) = sqlc.narg(cursor_customers) AND
          (i.last_seen_at,i.id) < (sqlc.narg(cursor_time),sqlc.narg(cursor_id)::uuid))))
   )
 ORDER BY CASE WHEN sqlc.arg(sort_by_impact)::bool THEN COALESCE(impact.identified_customers,0) END DESC,
          i.last_seen_at DESC,i.id DESC
 LIMIT sqlc.arg(page_limit);
-- name: IssueImpactSummaries :many
SELECT issue_id,
       count(*) AS observed_events,
       count(DISTINCT COALESCE(verified_platform_tenant_id,verified_consumer_id)) AS identified_customers,
       count(*) FILTER(WHERE verified_platform_tenant_id IS NULL AND verified_consumer_id IS NULL) AS unattributed_events
FROM issue_events
WHERE issue_id = ANY(sqlc.arg(issue_ids)::uuid[])
  AND occurred_at >= sqlc.arg(since)::timestamptz
  AND occurred_at <= sqlc.arg(until)::timestamptz
GROUP BY issue_id;
-- name: IssueCount :one
SELECT count(*) FROM app_issues WHERE app_id = sqlc.arg(app_id);
-- name: IssueCountEvents :one
SELECT count(*) FROM issue_events WHERE app_id = sqlc.arg(app_id);
-- name: IssueCountRecentEvents :one
SELECT count(*) FROM issue_events WHERE app_id = sqlc.arg(app_id) AND received_at >= sqlc.arg(since);
-- name: IssuePurgeEvents :exec
DELETE FROM issue_events WHERE app_id = sqlc.arg(app_id) AND received_at < sqlc.arg(before);
-- name: IssueFindEvent :one
SELECT issue_id,payload_hash FROM issue_events WHERE app_id=sqlc.arg(app_id) AND deployment_id=sqlc.arg(deployment_id) AND event_id=sqlc.arg(event_id);
-- name: IssueInsertEvent :exec
INSERT INTO issue_events(app_id,deployment_id,event_id,issue_id,payload_hash,payload,occurred_at,received_at,verified_consumer_id,verified_platform_tenant_id)
VALUES(sqlc.arg(app_id),sqlc.arg(deployment_id),sqlc.arg(event_id),sqlc.arg(issue_id),sqlc.arg(payload_hash),sqlc.arg(payload),sqlc.arg(occurred_at),sqlc.arg(received_at),sqlc.narg(consumer_id),sqlc.narg(tenant_id));
-- name: IssueObserve :one
UPDATE app_issues SET event_count=event_count+1,first_seen_at=LEAST(first_seen_at,sqlc.arg(occurred_at)),last_seen_at=GREATEST(last_seen_at,sqlc.arg(occurred_at)),
state=CASE WHEN sqlc.arg(regressed)::boolean THEN 'open' ELSE state END,
regression_count=regression_count+CASE WHEN sqlc.arg(regressed)::boolean THEN 1 ELSE 0 END
WHERE id=sqlc.arg(id) RETURNING *;
-- name: IssueObserveRelease :exec
INSERT INTO issue_releases(issue_id,deployment_id,commit_sha,image_digest,event_count,first_seen_at,last_seen_at)
VALUES(sqlc.arg(issue_id),sqlc.arg(deployment_id),sqlc.arg(commit_sha),sqlc.arg(image_digest),1,sqlc.arg(occurred_at),sqlc.arg(occurred_at))
ON CONFLICT(issue_id,deployment_id) DO UPDATE SET event_count=issue_releases.event_count+1,
first_seen_at=LEAST(issue_releases.first_seen_at,excluded.first_seen_at),last_seen_at=GREATEST(issue_releases.last_seen_at,excluded.last_seen_at);
-- name: IssueListEvents :many
SELECT * FROM issue_events WHERE issue_id=sqlc.arg(issue_id) AND occurred_at >= sqlc.arg(since) AND occurred_at <= sqlc.arg(until)
AND (sqlc.narg(cursor_time)::timestamptz IS NULL OR (occurred_at,id) < (sqlc.narg(cursor_time),sqlc.narg(cursor_id)::uuid))
ORDER BY occurred_at DESC,id DESC LIMIT sqlc.arg(page_limit);
-- name: IssueListReleases :many
SELECT * FROM issue_releases WHERE issue_id=sqlc.arg(issue_id) AND (sqlc.narg(cursor_time)::timestamptz IS NULL OR (first_seen_at,deployment_id) < (sqlc.narg(cursor_time),sqlc.narg(cursor_id)::uuid)) ORDER BY first_seen_at DESC,deployment_id DESC LIMIT sqlc.arg(page_limit);
-- name: IssueListActivity :many
SELECT * FROM issue_activity WHERE issue_id=sqlc.arg(issue_id) AND (sqlc.narg(cursor_time)::timestamptz IS NULL OR (created_at,id) < (sqlc.narg(cursor_time),sqlc.narg(cursor_id)::uuid)) ORDER BY created_at DESC,id DESC LIMIT sqlc.arg(page_limit);
-- name: IssueImpact :one
SELECT count(*) AS observed_events,count(DISTINCT COALESCE(verified_platform_tenant_id,verified_consumer_id)) AS identified_customers,
count(*) FILTER(WHERE verified_platform_tenant_id IS NULL AND verified_consumer_id IS NULL) AS unattributed_events
FROM issue_events WHERE issue_id=sqlc.arg(issue_id) AND occurred_at >= sqlc.arg(since) AND occurred_at <= sqlc.arg(until);
-- name: IssueImpactCustomerCount :one
SELECT count(DISTINCT COALESCE(verified_platform_tenant_id,verified_consumer_id))
FROM issue_events
WHERE issue_id=sqlc.arg(issue_id)
  AND occurred_at >= sqlc.arg(since)
  AND occurred_at <= sqlc.arg(until);
-- name: IssueGetImpactAlertPolicy :one
SELECT COALESCE((SELECT minimum_customers FROM app_issue_impact_alert_policies WHERE app_id=sqlc.arg(app_id)),0)::integer AS minimum_customers;
-- name: IssueUpsertImpactAlertPolicy :exec
INSERT INTO app_issue_impact_alert_policies(app_id,minimum_customers,updated_at)
VALUES(sqlc.arg(app_id),sqlc.arg(minimum_customers),sqlc.arg(updated_at))
ON CONFLICT(app_id) DO UPDATE SET minimum_customers=excluded.minimum_customers,updated_at=excluded.updated_at;
-- name: IssueDeleteImpactAlertPolicy :exec
DELETE FROM app_issue_impact_alert_policies WHERE app_id=sqlc.arg(app_id);
-- name: IssueListOwnershipRules :many
SELECT rule_order, exception_type, source_kind, route_prefix, assignee_account_id
  FROM app_issue_ownership_rules
 WHERE app_id=sqlc.arg(app_id)
 ORDER BY rule_order;
-- name: IssueDeleteOwnershipRules :exec
DELETE FROM app_issue_ownership_rules WHERE app_id=sqlc.arg(app_id);
-- name: IssueInsertOwnershipRule :exec
INSERT INTO app_issue_ownership_rules(app_id,rule_order,exception_type,source_kind,route_prefix,assignee_account_id)
VALUES(sqlc.arg(app_id),sqlc.arg(rule_order),sqlc.narg(exception_type),sqlc.narg(source_kind),sqlc.narg(route_prefix),sqlc.arg(assignee_account_id));
-- name: IssueSetNewIssueAssignee :one
UPDATE app_issues SET assignee_account_id=sqlc.arg(assignee_account_id)
 WHERE id=sqlc.arg(id) RETURNING *;
-- name: IssueAttribution :many
SELECT DISTINCT consumer_id,platform_tenant_id FROM request_telemetry
WHERE account_id=sqlc.arg(account_id) AND app_id=sqlc.arg(app_id) AND deployment_id=sqlc.arg(deployment_id)
AND trace_id=sqlc.arg(trace_id) AND count=1 AND received_at >= sqlc.arg(since) AND received_at <= sqlc.arg(until) LIMIT 2;
-- name: IssueInvocationScope :one
SELECT id,platform_tenant_id FROM invocations WHERE id=sqlc.arg(id) AND app_id=sqlc.arg(app_id) AND account_id=sqlc.arg(account_id);
-- name: IssueUpdateAction :one
UPDATE app_issues SET state=sqlc.arg(state),assignee_account_id=sqlc.narg(assignee),resolved_at=sqlc.narg(resolved_at),
fixed_deployment_id=sqlc.narg(fixed_deployment_id),fixed_deployment_created_at=sqlc.narg(fixed_deployment_created_at),ignored_until=sqlc.narg(ignored_until) WHERE id=sqlc.arg(id) RETURNING *;
-- name: IssueAddActivity :one
INSERT INTO issue_activity(issue_id,action,actor_account_id,created_at,details) VALUES(sqlc.arg(issue_id),sqlc.arg(action),sqlc.narg(actor_account_id),sqlc.arg(created_at),sqlc.arg(details)) RETURNING *;
-- name: IssueAddResolution :exec
INSERT INTO issue_resolutions(issue_id,fixed_deployment_id,resolved_at,actor_account_id) VALUES(sqlc.arg(issue_id),sqlc.arg(deployment_id),sqlc.arg(resolved_at),sqlc.arg(actor_account_id));
-- name: IssueAssigneeAllowed :one
SELECT EXISTS(SELECT 1 FROM apps a LEFT JOIN org_memberships m ON m.org_id=a.org_id AND m.account_id=sqlc.arg(assignee) AND m.removed_at IS NULL
WHERE a.id=sqlc.arg(app_id) AND (a.account_id=sqlc.arg(assignee) OR m.account_id IS NOT NULL));
-- name: IssueInsertToken :one
INSERT INTO issue_ingest_tokens(account_id,app_id,deployment_id,environment,name,token_hash,expires_at)
VALUES(sqlc.arg(account_id),sqlc.arg(app_id),sqlc.arg(deployment_id),sqlc.arg(environment),sqlc.arg(name),sqlc.arg(token_hash),sqlc.arg(expires_at)) RETURNING *;
-- name: IssueFindToken :one
SELECT * FROM issue_ingest_tokens WHERE token_hash=sqlc.arg(token_hash) AND revoked_at IS NULL AND expires_at > sqlc.arg(now);
-- name: IssueListTokens :many
SELECT * FROM issue_ingest_tokens WHERE app_id=sqlc.arg(app_id) ORDER BY created_at DESC;
-- name: IssueRevokeToken :execrows
UPDATE issue_ingest_tokens SET revoked_at=now() WHERE app_id=sqlc.arg(app_id) AND id=sqlc.arg(id);
-- name: IssueCountTokens :one
SELECT count(*) FROM issue_ingest_tokens WHERE app_id=sqlc.arg(app_id) AND revoked_at IS NULL AND expires_at > now();
-- name: IssueAddTransition :exec
WITH recipients AS (
 SELECT array_agg(id) AS ids FROM app_webhooks
 WHERE app_id=sqlc.arg(app_id) AND account_id=sqlc.arg(account_id) AND scope='app' AND enabled
 AND (cardinality(event_filter)=0 OR (sqlc.arg(payload)::jsonb->>'type')=ANY(event_filter))
)
INSERT INTO app_webhook_event_outbox(id,account_id,app_id,event,source_id,payload,recipient_webhook_ids)
SELECT sqlc.arg(activity_id),sqlc.arg(account_id),sqlc.arg(app_id),sqlc.arg(payload)::jsonb->>'type',sqlc.arg(activity_id),sqlc.arg(payload),ids
FROM recipients WHERE cardinality(ids)>0 ON CONFLICT(event,source_id) DO NOTHING;

-- name: IssueRequestAttribution :many
SELECT DISTINCT consumer_key,platform_tenant_id FROM request_audit_events
WHERE account_id=sqlc.arg(account_id) AND app_id=sqlc.arg(app_id) AND deployment_id=sqlc.arg(deployment_id)
AND request_id=sqlc.arg(request_id) LIMIT 2;

-- name: IssueTokenStillValid :one
SELECT EXISTS(SELECT 1 FROM issue_ingest_tokens WHERE id=sqlc.arg(id) AND app_id=sqlc.arg(app_id) AND deployment_id=sqlc.arg(deployment_id) AND revoked_at IS NULL AND expires_at>sqlc.arg(now));
-- name: IssuePurgePlanEvents :execrows
WITH expired AS (
 SELECT e.app_id,e.deployment_id,e.event_id FROM issue_events e JOIN apps a ON a.id=e.app_id JOIN accounts c ON c.id=a.account_id
 WHERE c.plan=sqlc.arg(plan) AND e.received_at<sqlc.arg(before) ORDER BY e.received_at LIMIT sqlc.arg(batch_limit) FOR UPDATE OF e SKIP LOCKED
)
DELETE FROM issue_events e USING expired x WHERE e.app_id=x.app_id AND e.deployment_id=x.deployment_id AND e.event_id=x.event_id;
-- name: IssuePurgeExpiredTokens :execrows
DELETE FROM issue_ingest_tokens WHERE expires_at < sqlc.arg(now);
-- name: IssueExpiredIgnores :many
SELECT app_id,id FROM app_issues WHERE state='ignored' AND ignored_until <= sqlc.arg(now) ORDER BY ignored_until LIMIT sqlc.arg(batch_limit);

-- name: IssueDebugRequest :many
SELECT id FROM request_telemetry WHERE account_id=sqlc.arg(account_id) AND app_id=sqlc.arg(app_id) AND deployment_id=sqlc.arg(deployment_id)
AND count=1 AND received_at BETWEEN sqlc.arg(since) AND sqlc.arg(until)
AND (id=sqlc.narg(request_id)::uuid OR (sqlc.arg(trace_id)::text<>'' AND trace_id=sqlc.arg(trace_id))) LIMIT 2;

-- name: IssueUnattributedEvents :many
SELECT e.*,i.account_id,i.environment FROM issue_events e JOIN app_issues i ON i.id=e.issue_id
WHERE e.verified_consumer_id IS NULL AND e.verified_platform_tenant_id IS NULL
AND e.attribution_checked_at < sqlc.arg(before) ORDER BY e.attribution_checked_at LIMIT sqlc.arg(batch_limit);

-- name: IssueEnrichAttribution :execrows
UPDATE issue_events SET verified_consumer_id=sqlc.narg(consumer_id),verified_platform_tenant_id=sqlc.narg(tenant_id),attribution_checked_at=sqlc.arg(now)
WHERE app_id=sqlc.arg(app_id) AND deployment_id=sqlc.arg(deployment_id) AND event_id=sqlc.arg(event_id)
AND verified_consumer_id IS NULL AND verified_platform_tenant_id IS NULL;

-- name: CreateDevBridge :execrows
INSERT INTO dev_bridge_sessions
(id,account_id,target_app_id,environment_id,scope,attachment_digest,request_digest,expires_at)
SELECT sqlc.arg(id),sqlc.arg(account_id),sqlc.arg(target_app_id),sqlc.arg(environment_id),
       sqlc.arg(scope),sqlc.arg(attachment_digest),sqlc.arg(request_digest),sqlc.arg(expires_at)
FROM apps a JOIN project_environments e ON e.project_id=a.project_id AND e.account_id=a.account_id
WHERE a.id=sqlc.arg(target_app_id) AND a.account_id=sqlc.arg(account_id) AND a.status='active'
  AND e.id=sqlc.arg(environment_id) AND NOT e.protected AND e.slug NOT IN ('production','default')
  AND (sqlc.arg(scope)::jsonb->>'project_id')=e.project_id::text
  AND (SELECT count(*) FROM dev_bridge_sessions b WHERE b.account_id=a.account_id
       AND b.revoked_at IS NULL AND b.expires_at > now()) < sqlc.arg(max_sessions)::integer;

-- name: DevBridgeByID :one
SELECT id,scope,attachment_digest,request_digest,expires_at,revoked_at
FROM dev_bridge_sessions WHERE id=$1 AND account_id=$2;

-- name: ListDevBridges :many
SELECT id,scope,attachment_digest,request_digest,expires_at,revoked_at
FROM dev_bridge_sessions
WHERE account_id=sqlc.arg(account_id) AND revoked_at IS NULL AND expires_at > now()
ORDER BY expires_at DESC, id ASC LIMIT sqlc.arg(row_limit);

-- name: PruneDevBridgeSessions :exec
DELETE FROM dev_bridge_sessions WHERE account_id=$1 AND expires_at < $2;

-- name: RevokeDevBridge :execrows
UPDATE dev_bridge_sessions SET revoked_at=COALESCE(revoked_at,sqlc.arg(revoked_at))
WHERE id=sqlc.arg(id) AND account_id=sqlc.arg(account_id);

-- name: LockDevBridgeAccount :one
SELECT plan FROM accounts WHERE id=$1 FOR UPDATE;

-- name: LockDevBridgeReplaySession :one
SELECT id FROM dev_bridge_sessions WHERE id=$1 AND account_id=$2
  AND revoked_at IS NULL AND expires_at > now() FOR UPDATE;

-- name: CreateDevBridgeWebhookReplay :execrows
INSERT INTO dev_bridge_webhook_replays (id,session_id,account_id,invocation_id,idempotency_key)
SELECT sqlc.arg(id),sqlc.arg(session_id),sqlc.arg(account_id),sqlc.arg(invocation_id),sqlc.arg(idempotency_key)
WHERE (SELECT count(*) FROM dev_bridge_webhook_replays WHERE session_id=sqlc.arg(session_id)) < sqlc.arg(max_replays)::integer
ON CONFLICT (session_id,idempotency_key) DO NOTHING;

-- name: DevBridgeWebhookReplayByKey :one
SELECT * FROM dev_bridge_webhook_replays WHERE session_id=$1 AND account_id=$2 AND idempotency_key=$3;

-- name: FinishDevBridgeWebhookReplay :execrows
UPDATE dev_bridge_webhook_replays SET state=sqlc.arg(state),http_status=sqlc.arg(http_status),completed_at=now()
WHERE id=sqlc.arg(id) AND account_id=sqlc.arg(account_id) AND state='dispatching';

-- name: DevBridgeWebhookReplayByID :one
SELECT * FROM dev_bridge_webhook_replays WHERE id=$1 AND account_id=$2 AND session_id=$3;
-- name: LockFeatureFlagProject :one
SELECT id FROM projects
WHERE id = sqlc.arg(project_id)::uuid AND account_id = sqlc.arg(account_id)::uuid
FOR KEY SHARE;

-- name: LockFeatureFlagEnvironment :one
SELECT e.id FROM project_environments e
JOIN projects p ON p.id = e.project_id AND p.account_id = e.account_id
WHERE e.id = sqlc.arg(environment_id)::uuid AND e.project_id = sqlc.arg(project_id)::uuid
 AND e.account_id = sqlc.arg(account_id)::uuid
FOR UPDATE OF e;

-- name: ReadProjectEnvironmentCloneFlagScope :one
SELECT e.id FROM project_environments e
JOIN projects p ON p.id = e.project_id AND p.account_id = e.account_id
WHERE e.project_id = sqlc.arg(project_id)::uuid AND e.account_id = sqlc.arg(account_id)::uuid
 AND e.slug = sqlc.arg(environment)::text;

-- name: GetFeatureFlagVersion :one
SELECT * FROM feature_flag_versions
WHERE environment_id = sqlc.arg(environment_id)::uuid
 AND account_id = sqlc.arg(account_id)::uuid AND project_id = sqlc.arg(project_id)::uuid
 AND (sqlc.arg(version)::bigint = 0 OR version = sqlc.arg(version)::bigint)
ORDER BY version DESC LIMIT 1;

-- name: ListFeatureFlagVersions :many
SELECT * FROM feature_flag_versions
WHERE environment_id = sqlc.arg(environment_id)::uuid
 AND account_id = sqlc.arg(account_id)::uuid AND project_id = sqlc.arg(project_id)::uuid
 AND version < sqlc.arg(before_version)::bigint
ORDER BY version DESC LIMIT 100;

-- name: InsertFeatureFlagVersion :one
INSERT INTO feature_flag_versions (account_id, project_id, environment_id, version, config, actor, restored_from)
VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING *;

-- name: LockPromotionFeatureFlagCustomer :one
SELECT id FROM platform_tenants
WHERE account_id = sqlc.arg(account_id)::uuid AND id = sqlc.arg(tenant_id)::uuid
FOR KEY SHARE;

-- name: InsertPromotionFeatureFlags :exec
INSERT INTO project_environment_promotion_feature_flags
 (promotion_id, source_snapshot, previous_target_snapshot, source_hash, previous_target_hash)
VALUES ($1, $2, $3, $4, $5);

-- name: ReadPromotionFeatureFlags :one
SELECT f.* FROM project_environment_promotion_feature_flags f
JOIN project_environment_promotions p ON p.id = f.promotion_id
WHERE p.id = sqlc.arg(promotion_id)::uuid AND p.account_id = sqlc.arg(account_id)::uuid;

-- name: UpdatePromotionFeatureFlagReceipt :one
UPDATE project_environment_promotion_feature_flags
SET target_version = sqlc.arg(target_version)::bigint, rollback_version = sqlc.arg(rollback_version)::bigint
WHERE promotion_id = sqlc.arg(promotion_id)::uuid
 AND target_version = sqlc.arg(previous_target_version)::bigint
 AND rollback_version = sqlc.arg(previous_rollback_version)::bigint
RETURNING promotion_id;

-- name: FeatureFlagCustomerOwned :one
SELECT EXISTS(SELECT 1 FROM platform_tenants
 WHERE account_id = sqlc.arg(account_id)::uuid AND id = sqlc.arg(tenant_id)::uuid) AS owned;

-- name: ListFeatureFlagAutoRolloutCandidates :many
SELECT pe.account_id, pe.project_id, pe.id AS environment_id,
       p.slug AS project_slug, pe.slug AS environment_slug
FROM project_environments pe
JOIN projects p ON p.id = pe.project_id AND p.account_id = pe.account_id
JOIN LATERAL (
 SELECT v.config
 FROM feature_flag_versions v
 WHERE v.account_id = pe.account_id
   AND v.project_id = pe.project_id
   AND v.environment_id = pe.id
 ORDER BY v.version DESC
 LIMIT 1
) current_config ON current_config.config @> '{"flags":[{"rules":[{"progression":{"auto_advance":true}}]}]}'::jsonb
WHERE sqlc.narg(after_environment_id)::uuid IS NULL
   OR pe.id > sqlc.narg(after_environment_id)::uuid
ORDER BY pe.id
LIMIT sqlc.arg(limit_rows)::int;

-- name: ListFeatureFlagRequestEvidence :many
SELECT t.id, t.app_id, t.deployment_id, t.platform_tenant_id, t.received_at, t.route, t.method,
 t.status, t.latency_ms, t.count, t.cold_boot, t.trace_id, t.flag_evidence
FROM request_telemetry t JOIN deployments d ON d.id = t.deployment_id
WHERE (d.scope = sqlc.arg(environment_slug)::text OR (d.scope = 'default' AND sqlc.arg(environment_slug)::text = 'production'))
 AND t.account_id = sqlc.arg(account_id)::uuid
 AND t.app_id = ANY(sqlc.arg(app_ids)::uuid[])
 AND t.received_at >= sqlc.arg(received_from)::timestamptz
 AND t.received_at < sqlc.arg(received_until)::timestamptz
 AND (sqlc.arg(customer_id)::text = '' OR platform_tenant_id::text = sqlc.arg(customer_id)::text)
 AND flag_evidence @> sqlc.arg(evidence_filter)::jsonb
 AND (sqlc.narg(cursor_at)::timestamptz IS NULL OR (t.received_at,t.id) < (sqlc.narg(cursor_at)::timestamptz,sqlc.narg(cursor_id)::uuid))
ORDER BY t.received_at DESC, t.id DESC LIMIT 101;
-- name: CreateEnvironmentGitSource :one
INSERT INTO environment_git_sources
    (account_id, project_id, environment_id, repository_id, installation_id,
     repository, source_ref, manifest_path, mode, approval_policy, prune, generation)
SELECT p.account_id, p.id, e.id, sqlc.arg(repository_id)::bigint,
       sqlc.arg(installation_id)::bigint, sqlc.arg(repository)::text,
       sqlc.arg(source_ref)::text, sqlc.arg(manifest_path)::text,
       sqlc.arg(mode)::text, sqlc.arg(approval_policy)::text, sqlc.arg(prune)::boolean,
       coalesce((SELECT max(old.generation)+1 FROM environment_git_sources old WHERE old.environment_id=e.id),0)
FROM projects p JOIN project_environments e ON e.project_id = p.id AND e.account_id = p.account_id
WHERE p.account_id = sqlc.arg(account_id)::uuid AND p.id = sqlc.arg(project_id)::uuid
  AND e.slug = sqlc.arg(environment_slug)::text
  AND p.repo_full_name = sqlc.arg(repository)::text AND p.install_id = sqlc.arg(installation_id)::bigint
RETURNING *;

-- name: GetEnvironmentGitSource :one
SELECT s.* FROM environment_git_sources s
JOIN project_environments e ON e.id = s.environment_id
WHERE s.account_id = sqlc.arg(account_id)::uuid AND s.project_id = sqlc.arg(project_id)::uuid
  AND e.slug = sqlc.arg(environment_slug)::text AND NOT s.detached;

-- name: LockEnvironmentGitSource :one
SELECT s.* FROM environment_git_sources s
WHERE s.account_id = sqlc.arg(account_id)::uuid AND s.id = sqlc.arg(source_id)::uuid
FOR UPDATE;

-- name: GetEnvironmentGitOpsScope :one
SELECT p.slug AS project_slug, e.slug AS environment_slug
FROM environment_git_sources s
JOIN projects p ON p.id = s.project_id AND p.account_id = s.account_id
JOIN project_environments e ON e.id = s.environment_id AND e.account_id = s.account_id
WHERE s.id = sqlc.arg(source_id)::uuid;

-- name: InsertEnvironmentDesiredRevision :one
INSERT INTO environment_desired_revisions
    (source_id, commit_sha, definition_digest, definition, approved_by)
VALUES (sqlc.arg(source_id)::uuid, sqlc.arg(commit_sha)::text,
        sqlc.arg(definition_digest)::text, sqlc.arg(definition)::jsonb, sqlc.arg(approved_by)::text)
ON CONFLICT (source_id, commit_sha, definition_digest) DO UPDATE
    SET source_id = excluded.source_id
RETURNING *;

-- name: SetEnvironmentApprovedRevision :one
UPDATE environment_git_sources
SET approved_revision_id = sqlc.arg(revision_id)::uuid,
    generation = generation + 1, updated_at = now()
WHERE id = sqlc.arg(source_id)::uuid AND generation = sqlc.arg(expected_generation)::bigint
RETURNING *;

-- name: EnqueueEnvironmentGitOps :exec
INSERT INTO environment_gitops_jobs (source_id, desired_generation, next_attempt_at)
VALUES (sqlc.arg(source_id)::uuid, sqlc.arg(generation)::bigint, sqlc.arg(next_attempt_at)::timestamptz)
ON CONFLICT (source_id) DO UPDATE
SET desired_generation = excluded.desired_generation,
    next_attempt_at = least(environment_gitops_jobs.next_attempt_at, excluded.next_attempt_at);

-- name: ClaimEnvironmentGitOpsJob :one
WITH candidate AS (
    SELECT j.source_id, s.generation FROM environment_gitops_jobs j
    JOIN environment_git_sources s ON s.id = j.source_id
    WHERE NOT s.suspended AND s.approved_revision_id IS NOT NULL
      AND (sqlc.arg(mode)::text = '' OR s.mode = sqlc.arg(mode)::text)
      AND (s.approval_policy = 'manual' OR EXISTS (SELECT 1 FROM environment_git_revision_approvals a
        WHERE a.source_id=s.id AND a.revision_id=s.approved_revision_id AND a.approved_generation<=s.generation))
      AND s.generation = j.desired_generation AND j.next_attempt_at <= sqlc.arg(now_at)::timestamptz
      AND (j.lease_until IS NULL OR j.lease_until <= sqlc.arg(now_at)::timestamptz
           OR j.claimed_generation <> j.desired_generation)
    ORDER BY j.next_attempt_at, j.source_id
    FOR UPDATE OF s SKIP LOCKED LIMIT 1
)
UPDATE environment_gitops_jobs j
SET claimed_generation = j.desired_generation,
    lease_token = sqlc.arg(lease_token)::text,
    lease_until = sqlc.arg(lease_until)::timestamptz,
    attempt_count = j.attempt_count + 1
FROM candidate c WHERE j.source_id = c.source_id
  AND j.desired_generation = c.generation AND j.next_attempt_at <= sqlc.arg(now_at)::timestamptz
  AND (j.lease_until IS NULL OR j.lease_until <= sqlc.arg(now_at)::timestamptz
       OR j.claimed_generation <> j.desired_generation)
RETURNING j.*;

-- name: GetEnvironmentGitSourceByID :one
SELECT * FROM environment_git_sources WHERE id = sqlc.arg(source_id)::uuid;

-- name: GetEnvironmentDesiredRevision :one
SELECT * FROM environment_desired_revisions
WHERE source_id = sqlc.arg(source_id)::uuid AND id = sqlc.arg(revision_id)::uuid;

-- name: SupersedeEnvironmentGitOpsRuns :exec
UPDATE environment_gitops_runs SET status = 'superseded', completed_at = sqlc.arg(now_at)::timestamptz
WHERE source_id = sqlc.arg(source_id)::uuid AND completed_at IS NULL;

-- name: InsertEnvironmentGitOpsRun :one
INSERT INTO environment_gitops_runs (source_id, revision_id, generation, lease_token, status, started_at)
VALUES (sqlc.arg(source_id)::uuid, sqlc.arg(revision_id)::uuid, sqlc.arg(generation)::bigint,
        sqlc.arg(lease_token)::text, 'planning', sqlc.arg(now_at)::timestamptz)
RETURNING *;

-- name: LockEnvironmentGitOpsLease :one
SELECT j.* FROM environment_gitops_jobs j
JOIN environment_git_sources s ON s.id = j.source_id
WHERE j.source_id = sqlc.arg(source_id)::uuid AND j.lease_token = sqlc.arg(lease_token)::text
  AND j.claimed_generation = sqlc.arg(generation)::bigint AND s.generation = j.claimed_generation
  AND j.desired_generation = j.claimed_generation AND NOT s.suspended
  AND j.lease_until > sqlc.arg(now_at)::timestamptz
FOR UPDATE OF j;

-- name: RenewEnvironmentGitOpsLease :execrows
UPDATE environment_gitops_jobs
SET lease_until = sqlc.arg(lease_until)::timestamptz
WHERE source_id = sqlc.arg(source_id)::uuid AND lease_token = sqlc.arg(lease_token)::text;

-- name: FinishEnvironmentGitOpsRun :execrows
UPDATE environment_gitops_runs
SET status = sqlc.arg(status)::text, plan = sqlc.arg(plan)::jsonb,
    steps = CASE WHEN jsonb_array_length(sqlc.arg(steps)::jsonb) = 0 AND jsonb_array_length(steps) > 0
        THEN steps ELSE sqlc.arg(steps)::jsonb END,
    error_code = sqlc.arg(error_code)::text, completed_at = sqlc.arg(now_at)::timestamptz
WHERE id = sqlc.arg(run_id)::uuid AND source_id = sqlc.arg(source_id)::uuid
  AND generation = sqlc.arg(generation)::bigint AND lease_token = sqlc.arg(lease_token)::text
  AND completed_at IS NULL;

-- name: SetEnvironmentAppliedRevision :execrows
UPDATE environment_git_sources SET applied_revision_id = approved_revision_id, updated_at = now()
WHERE id = sqlc.arg(source_id)::uuid AND generation = sqlc.arg(generation)::bigint;

-- name: ReleaseEnvironmentGitOpsLease :execrows
UPDATE environment_gitops_jobs
SET lease_token = '', lease_until = NULL, next_attempt_at = sqlc.arg(next_attempt_at)::timestamptz
WHERE source_id = sqlc.arg(source_id)::uuid AND lease_token = sqlc.arg(lease_token)::text;

-- name: ListEnvironmentGitOpsRuns :many
SELECT r.* FROM environment_gitops_runs r
JOIN environment_git_sources s ON s.id = r.source_id
WHERE s.account_id = sqlc.arg(account_id)::uuid AND s.id = sqlc.arg(source_id)::uuid
ORDER BY r.started_at DESC, r.id DESC LIMIT sqlc.arg(row_limit)::integer;

-- name: ObserveEnvironmentGitOpsIntent :one
SELECT jsonb_build_object(
    'version', s.intent_version, 'project', p.slug, 'environment', e.slug, 'environment_id', e.id, 'plan', acct.plan, 'source_id', s.id, 'prune', s.prune,
    'configuration', coalesce((SELECT config_json FROM project_environment_config_versions c
        WHERE c.project_id = s.project_id AND c.environment_slug = e.slug ORDER BY version DESC LIMIT 1), '{}'::jsonb),
    'resources', coalesce((SELECT jsonb_agg(jsonb_build_object('resource', r.logical_name, 'app_id', r.app_id))
        FROM environment_gitops_resources r WHERE r.source_id = s.id), '[]'::jsonb),
    'queue_bindings', coalesce((SELECT jsonb_agg(jsonb_build_object('resource', q.resource, 'path', q.field_path, 'binding_id', q.binding_id))
        FROM environment_gitops_queue_bindings q WHERE q.source_id=s.id), '[]'::jsonb),
    'apps', coalesce((SELECT jsonb_agg(jsonb_build_object(
        'id', a.id, 'slug', a.slug, 'type', a.type, 'workload_class', a.workload_class,
        'manifest',a.manifest, 'start_command',coalesce(a.start_command,''), 'runtime_base',coalesce(a.runtime,''),
        'workload_intent',(SELECT to_jsonb(w) FROM app_environment_workload_intents w WHERE w.app_id=a.id AND w.environment_id=s.environment_id),
        'sources',coalesce((SELECT jsonb_agg(jsonb_build_object('id',d.id,'kind',d.kind,'image',d.image_digest,'inputs',environment_workload_deployment_inputs(d)) ORDER BY d.id) FROM deployments d
          WHERE d.app_id=a.id AND d.scope=e.slug AND d.status='live'),'[]'::jsonb),
        'queue_bindings', coalesce((SELECT jsonb_agg(jsonb_build_object('id', b.id, 'name', b.name, 'retired_at', b.retired_at,
            'intent', jsonb_build_object('queue_name', b.queue_name, 'mode', b.mode, 'workload_class', b.workload_class,
                'enabled', b.enabled, 'max_concurrency', b.max_concurrency, 'retry_policy', b.retry_policy),
            'consumers', coalesce((SELECT jsonb_agg(jsonb_build_object('id', t.id, 'slug', t.slug, 'enabled', t.enabled,
                'config', t.config, 'batch_size', t.batch_size_max, 'batch_window', t.batch_window_ms,
                'max_attempts', t.max_attempts, 'payload_max', t.payload_max_bytes, 'broker_poison_strategy', t.broker_poison_strategy, 'filter_criteria', t.filter_criteria)) FROM triggers t WHERE t.queue_binding_id=b.id), '[]'::jsonb)))
            FROM queue_bindings b WHERE b.app_id=a.id AND b.account_id=s.account_id AND b.environment_id=s.environment_id), '[]'::jsonb), 'variable_count', (SELECT count(*) FROM app_envs v WHERE v.app_id = a.id AND v.account_id = s.account_id),
        'secret_refs', environment_scoped_secret_refs(a.id,e.slug),
        'suppressed_keys', environment_scoped_secret_suppressions(a.id,e.slug),
        'suppression_count', (SELECT count(*) FROM app_environment_secret_ref_suppressions r WHERE r.app_id=a.id AND r.account_id=s.account_id),
        'live_deployments', coalesce((SELECT jsonb_agg(jsonb_build_object('id',d.id,'secret_refs',d.override_env_secrets) ORDER BY d.id)
            FROM deployments d WHERE d.app_id=a.id AND d.scope=e.slug AND d.status='live'),'[]'::jsonb),
        'secret_ref_count', (SELECT count(*) FROM app_environment_secret_refs r WHERE r.app_id=a.id AND r.account_id=s.account_id),
        'secret_names', coalesce((SELECT jsonb_agg(v.key) FROM app_secrets v WHERE v.app_id=a.id AND v.account_id=s.account_id AND v.scope=e.slug),'[]'::jsonb),
        'variables', coalesce((SELECT jsonb_object_agg(v.key, v.value) FROM app_envs v
            WHERE v.app_id = a.id AND v.account_id = s.account_id AND v.scope = e.slug), '{}'::jsonb),
        'routes', (SELECT jsonb_build_object('only_allow_declared_routes', r.only_allow_declared_routes, 'declared_routes', r.declared_routes)
            FROM project_environment_route_policies r WHERE r.app_id = a.id AND r.environment_slug = e.slug),
        'policies', (SELECT rules FROM project_environment_edge_policies r WHERE r.app_id = a.id AND r.environment_slug = e.slug)
        )) FROM apps a WHERE a.project_id = s.project_id AND a.account_id = s.account_id AND a.status <> 'deleted'), '[]'::jsonb),
    'external_owners',coalesce((SELECT jsonb_agg(jsonb_build_object('resource',f.resource,'path',f.field_path,'manager',f.manager_id)) FROM environment_external_field_owners f WHERE f.environment_id=s.environment_id),'[]'::jsonb),
    'owners', coalesce((SELECT jsonb_agg(jsonb_build_object('resource', f.resource, 'path', f.field_path,
        'value', f.desired_value, 'manager', f.manager_id)) FROM environment_managed_fields f
        WHERE f.environment_id = s.environment_id), '[]'::jsonb),
    'overrides', coalesce((SELECT jsonb_agg(jsonb_build_object('resource', o.resource, 'path', o.field_path, 'expires_at', o.expires_at))
        FROM environment_management_overrides o WHERE o.environment_id = s.environment_id), '[]'::jsonb)
)::jsonb AS observation
FROM environment_git_sources s JOIN project_environments e ON e.id = s.environment_id
JOIN projects p ON p.id = s.project_id
JOIN accounts acct ON acct.id = s.account_id
WHERE s.id = sqlc.arg(source_id)::uuid AND s.account_id = sqlc.arg(account_id)::uuid;

-- name: BindEnvironmentGitOpsResource :execrows
INSERT INTO environment_gitops_resources(source_id, logical_name, app_id)
SELECT s.id, sqlc.arg(resource)::text, a.id FROM environment_git_sources s
JOIN apps a ON a.account_id = s.account_id AND a.project_id = s.project_id AND a.status <> 'deleted'
WHERE s.id = sqlc.arg(source_id)::uuid AND a.id = sqlc.arg(app_id)::uuid
ON CONFLICT (source_id, logical_name) DO UPDATE SET app_id = excluded.app_id
WHERE environment_gitops_resources.app_id = excluded.app_id;

-- name: OwnEnvironmentGitOpsField :execrows
INSERT INTO environment_managed_fields(environment_id, resource, field_path, manager_kind, manager_id, source_id, desired_value)
SELECT environment_id, sqlc.arg(resource)::text, sqlc.arg(field_path)::text, 'git', id::text, id, sqlc.arg(value)::jsonb
FROM environment_git_sources WHERE id = sqlc.arg(source_id)::uuid
ON CONFLICT (environment_id, resource, field_path) DO UPDATE
SET desired_value = excluded.desired_value, updated_at = now()
WHERE environment_managed_fields.source_id = excluded.source_id;

-- name: ReleaseEnvironmentGitOpsField :exec
DELETE FROM environment_managed_fields WHERE source_id = sqlc.arg(source_id)::uuid
AND resource = sqlc.arg(resource)::text AND field_path = sqlc.arg(field_path)::text;

-- name: SetEnvironmentGitOpsLeaseContext :one
SELECT set_config('gregale.gitops_lease', sqlc.arg(lease_token)::text, true)::text;

-- name: TouchEnvironmentGitOpsIntent :exec
UPDATE environment_git_sources SET intent_version = intent_version + 1, updated_at = now()
WHERE id = sqlc.arg(source_id)::uuid;

-- name: PutEnvironmentGitOpsVariable :exec
INSERT INTO app_envs(account_id, app_id, scope, key, value)
VALUES (sqlc.arg(account_id)::uuid, sqlc.arg(app_id)::uuid, sqlc.arg(scope)::text, sqlc.arg(key)::text, sqlc.arg(value)::text)
ON CONFLICT (app_id, scope, key) DO UPDATE SET value = excluded.value, updated_at = now();

-- name: DeleteEnvironmentGitOpsVariable :exec
DELETE FROM app_envs WHERE account_id = sqlc.arg(account_id)::uuid AND app_id = sqlc.arg(app_id)::uuid
AND scope = sqlc.arg(scope)::text AND key = sqlc.arg(key)::text;

-- name: PutEnvironmentGitOpsRoutes :exec
INSERT INTO project_environment_route_policies(account_id, project_id, app_id, environment_slug, only_allow_declared_routes, declared_routes)
VALUES (sqlc.arg(account_id)::uuid, sqlc.arg(project_id)::uuid, sqlc.arg(app_id)::uuid, sqlc.arg(environment)::text,
    sqlc.arg(enforced)::boolean, sqlc.arg(routes)::jsonb)
ON CONFLICT (app_id, environment_slug) DO UPDATE SET only_allow_declared_routes = excluded.only_allow_declared_routes,
    declared_routes = excluded.declared_routes, updated_at = now();

-- name: DeleteEnvironmentGitOpsRoutes :exec
DELETE FROM project_environment_route_policies WHERE account_id = sqlc.arg(account_id)::uuid
AND app_id = sqlc.arg(app_id)::uuid AND environment_slug = sqlc.arg(environment)::text;

-- name: PutEnvironmentGitOpsPolicies :exec
INSERT INTO project_environment_edge_policies(account_id, project_id, app_id, environment_slug, rules)
VALUES (sqlc.arg(account_id)::uuid, sqlc.arg(project_id)::uuid, sqlc.arg(app_id)::uuid, sqlc.arg(environment)::text, sqlc.arg(rules)::jsonb)
ON CONFLICT (app_id, environment_slug) DO UPDATE SET rules = excluded.rules, updated_at = now();

-- name: DeleteEnvironmentGitOpsPolicies :exec
DELETE FROM project_environment_edge_policies WHERE account_id = sqlc.arg(account_id)::uuid
AND app_id = sqlc.arg(app_id)::uuid AND environment_slug = sqlc.arg(environment)::text;

-- name: InsertEnvironmentGitOpsConfig :exec
INSERT INTO project_environment_config_versions(account_id, project_id, environment_slug, version, config_hash, config_json)
SELECT sqlc.arg(account_id)::uuid, sqlc.arg(project_id)::uuid, sqlc.arg(environment)::text,
    coalesce(max(version), 0) + 1, sqlc.arg(hash)::text, sqlc.arg(values)::jsonb
FROM project_environment_config_versions WHERE project_id = sqlc.arg(project_id)::uuid AND environment_slug = sqlc.arg(environment)::text;

-- name: SaveEnvironmentGitOpsProgress :execrows
UPDATE environment_gitops_runs SET status = 'applying', plan = sqlc.arg(plan)::jsonb, steps = sqlc.arg(steps)::jsonb
WHERE id = sqlc.arg(run_id)::uuid AND source_id = sqlc.arg(source_id)::uuid
AND lease_token = sqlc.arg(lease_token)::text AND completed_at IS NULL;

-- name: LockEnvironmentGitSourceForScope :many
SELECT s.id FROM active_environment_git_sources s JOIN project_environments e ON e.id = s.environment_id
WHERE s.account_id = sqlc.arg(account_id)::uuid AND s.project_id = sqlc.arg(project_id)::uuid
AND e.slug = sqlc.arg(environment)::text FOR UPDATE OF s;

-- name: UpdateEnvironmentGitSourceControl :one
UPDATE environment_git_sources SET mode = sqlc.arg(mode)::text, prune = sqlc.arg(prune)::boolean,
    suspended = sqlc.arg(suspended)::boolean, generation = generation + 1, intent_version = intent_version + 1, updated_at = now()
WHERE id = sqlc.arg(source_id)::uuid AND generation = sqlc.arg(expected_generation)::bigint RETURNING *;

-- name: PutEnvironmentGitOpsOverride :execrows
INSERT INTO environment_management_overrides(environment_id, resource, field_path, authorized_by, reason, expires_at)
SELECT f.environment_id, f.resource, f.field_path, sqlc.arg(actor)::text, sqlc.arg(reason)::text, sqlc.arg(expires_at)::timestamptz
FROM environment_managed_fields f WHERE f.source_id = sqlc.arg(source_id)::uuid
AND f.resource = sqlc.arg(resource)::text AND f.field_path = sqlc.arg(field_path)::text
ON CONFLICT (environment_id, resource, field_path) DO UPDATE SET authorized_by = excluded.authorized_by,
    reason = excluded.reason, expires_at = excluded.expires_at, created_at = now();

-- name: DeleteEnvironmentGitOpsOverride :execrows
DELETE FROM environment_management_overrides o USING environment_git_sources s
WHERE o.environment_id = s.environment_id AND s.id = sqlc.arg(source_id)::uuid
AND o.resource = sqlc.arg(resource)::text AND o.field_path = sqlc.arg(field_path)::text;

-- name: RecordEnvironmentGitOpsEvent :exec
INSERT INTO environment_gitops_events(source_id, actor, kind, details)
VALUES (sqlc.arg(source_id)::uuid, sqlc.arg(actor)::text, sqlc.arg(kind)::text, sqlc.arg(details)::jsonb);

-- name: InvalidateEnvironmentGitOpsRuntimeConfig :execrows
WITH stamped AS (
    INSERT INTO app_runtime_config_scope_changes(app_id, scope, changed_at)
    VALUES (sqlc.arg(app_id)::uuid, sqlc.arg(scope)::text, clock_timestamp())
    ON CONFLICT (app_id, scope) DO UPDATE SET changed_at = greatest(app_runtime_config_scope_changes.changed_at, excluded.changed_at)
    RETURNING app_id, scope, changed_at
)
UPDATE snapshots SET stale = true FROM deployments d, stamped
WHERE snapshots.deployment_id = d.id AND d.app_id = stamped.app_id AND d.scope = stamped.scope
AND NOT snapshots.stale AND (snapshots.created_at <= stamped.changed_at OR
    (environment_runtime_receipt_required(d.app_id, d.scope) AND NOT EXISTS (
        SELECT 1 FROM snapshot_runtime_config_receipts r WHERE r.snapshot_id = snapshots.id
        AND r.scope = d.scope AND r.boundary_at >= stamped.changed_at AND environment_runtime_inputs_fresh(d.app_id, r.scope, r.boundary_at, r.variables, r.secret_versions, r.all_secrets,r.secret_refs,r.sidecar_secret_versions))));

-- name: InsertEnvironmentGitOpsEffect :exec
INSERT INTO environment_gitops_effects(source_id, revision_id, generation, intent_version, plan_hash,
    app_id, kind, gateway_generation, match_hosts, expected_nodes)
SELECT s.id, sqlc.arg(revision_id)::uuid, s.generation, s.intent_version, sqlc.arg(plan_hash)::text,
    sqlc.arg(app_id)::uuid, sqlc.arg(kind)::text, sqlc.arg(gateway_generation)::bigint,
    sqlc.arg(match_hosts)::text[], sqlc.arg(expected_nodes)::text[]
FROM environment_git_sources s WHERE s.id = sqlc.arg(source_id)::uuid;

-- name: PendingEnvironmentGitOpsEffects :many
SELECT * FROM environment_gitops_effects WHERE source_id = sqlc.arg(source_id)::uuid AND completed_at IS NULL
ORDER BY gateway_generation, id;

-- name: ExtendEnvironmentGitOpsEffectTargets :one
UPDATE environment_gitops_effects
SET expected_nodes = ARRAY(SELECT DISTINCT v FROM unnest(expected_nodes || sqlc.arg(nodes)::text[]) AS v ORDER BY v)
WHERE source_id = sqlc.arg(source_id)::uuid AND id = sqlc.arg(effect_id)::uuid AND completed_at IS NULL
RETURNING *;

-- name: AcknowledgeEnvironmentGitOpsEffect :execrows
UPDATE environment_gitops_effects
SET acknowledged_nodes = ARRAY(SELECT DISTINCT v FROM unnest(acknowledged_nodes || ARRAY[sqlc.arg(node)::text]) AS v ORDER BY v)
WHERE source_id = sqlc.arg(source_id)::uuid AND id = sqlc.arg(effect_id)::uuid
AND gateway_generation = sqlc.arg(gateway_generation)::bigint AND sqlc.arg(node)::text = ANY(expected_nodes)
AND completed_at IS NULL;

-- name: CompleteEnvironmentGitOpsEffect :execrows
UPDATE environment_gitops_effects SET completed_at = now()
WHERE source_id = sqlc.arg(source_id)::uuid AND id = sqlc.arg(effect_id)::uuid
AND completed_at IS NULL AND expected_nodes <@ acknowledged_nodes;

-- name: HasPendingEnvironmentGitOpsEffects :one
SELECT EXISTS(SELECT 1 FROM environment_gitops_effects WHERE source_id = sqlc.arg(source_id)::uuid AND completed_at IS NULL) AS pending;

-- name: TryEdgeRuleMutationLock :one
SELECT pg_try_advisory_lock(hashtextextended(sqlc.arg(app_id)::text, 0))::boolean AS locked;

-- name: ReleaseEdgeRuleMutationLock :one
SELECT pg_advisory_unlock(hashtextextended(sqlc.arg(app_id)::text, 0))::boolean AS unlocked;

-- name: ObserveEnvironmentGitOpsRuntime :many
SELECT app_id, resource, environment_slug, required_at::timestamptz AS required_at, stale_residents, starting_residents, stale_snapshots
FROM environment_gitops_runtime_targets WHERE source_id = sqlc.arg(source_id)::uuid
AND account_id = sqlc.arg(account_id)::uuid ORDER BY app_id;

-- name: InsertEnvironmentGitOpsRuntimeEffect :execrows
INSERT INTO environment_gitops_runtime_effects(source_id, revision_id, generation, intent_version, plan_hash,
    app_id, environment_slug, required_at)
SELECT s.id, sqlc.arg(revision_id)::uuid, s.generation, s.intent_version, sqlc.arg(plan_hash)::text,
    sqlc.arg(app_id)::uuid, e.slug, greatest(coalesce(c.changed_at, 'epoch'::timestamptz), sqlc.arg(required_at)::timestamptz,
        coalesce((SELECT max(x.changed_at) FROM app_runtime_config_scope_changes x
            WHERE x.app_id = sqlc.arg(app_id)::uuid AND x.scope IN ('default', e.slug)), 'epoch'::timestamptz))
FROM environment_git_sources s JOIN project_environments e ON e.id = s.environment_id
LEFT JOIN app_runtime_config_changes c ON c.app_id = sqlc.arg(app_id)::uuid
WHERE s.id = sqlc.arg(source_id)::uuid
ON CONFLICT (source_id, generation, plan_hash, app_id) DO UPDATE SET
    completed_at = NULL,
    required_at = CASE WHEN environment_gitops_runtime_effects.completed_at IS NOT NULL
        THEN greatest(environment_gitops_runtime_effects.required_at, excluded.required_at)
        ELSE environment_gitops_runtime_effects.required_at END,
    wake_id = CASE WHEN environment_gitops_runtime_effects.completed_at IS NOT NULL THEN gen_random_uuid() ELSE environment_gitops_runtime_effects.wake_id END,
    requested_at = CASE WHEN environment_gitops_runtime_effects.completed_at IS NOT NULL THEN NULL ELSE environment_gitops_runtime_effects.requested_at END,
    next_request_at = CASE WHEN environment_gitops_runtime_effects.completed_at IS NOT NULL THEN now() ELSE environment_gitops_runtime_effects.next_request_at END;

-- name: PendingEnvironmentGitOpsRuntime :many
SELECT * FROM environment_gitops_runtime_effects
WHERE source_id = sqlc.arg(source_id)::uuid AND completed_at IS NULL ORDER BY app_id, created_at, id;

-- name: LockEnvironmentGitOpsRuntimeEffect :one
SELECT * FROM environment_gitops_runtime_effects WHERE source_id = sqlc.arg(source_id)::uuid
AND id = sqlc.arg(effect_id)::uuid AND completed_at IS NULL FOR UPDATE;

-- name: AdvanceEnvironmentGitOpsRuntimeBoundary :one
UPDATE environment_gitops_runtime_effects SET required_at = sqlc.arg(required_at)::timestamptz,
    wake_id = gen_random_uuid(), requested_at = NULL, next_request_at = now()
WHERE source_id = sqlc.arg(source_id)::uuid AND id = sqlc.arg(effect_id)::uuid
AND completed_at IS NULL AND required_at < sqlc.arg(required_at)::timestamptz RETURNING *;

-- name: InvalidateEnvironmentGitOpsRuntimeAtBoundary :exec
WITH stamped AS (
    INSERT INTO app_runtime_config_scope_changes(app_id, scope, changed_at)
    VALUES (sqlc.arg(app_id)::uuid, sqlc.arg(scope)::text, sqlc.arg(required_at)::timestamptz)
    ON CONFLICT (app_id, scope) DO UPDATE SET changed_at = greatest(app_runtime_config_scope_changes.changed_at, excluded.changed_at)
    RETURNING app_id, scope, changed_at
)
UPDATE snapshots p SET stale = true FROM deployments d, stamped c
WHERE p.deployment_id = d.id AND d.app_id = c.app_id AND d.scope = c.scope
AND NOT p.stale AND (p.created_at <= c.changed_at OR NOT EXISTS (
    SELECT 1 FROM snapshot_runtime_config_receipts r WHERE r.snapshot_id = p.id AND r.scope = d.scope AND r.boundary_at >= c.changed_at
    AND environment_runtime_inputs_fresh(d.app_id, r.scope, r.boundary_at, r.variables, r.secret_versions, r.all_secrets,r.secret_refs,r.sidecar_secret_versions)));

-- name: AppRuntimeConfigChangedAtInScope :one
SELECT max(boundary.changed_at)::timestamptz AS changed_at FROM (
    SELECT changed_at FROM app_runtime_config_changes WHERE app_id = sqlc.arg(app_id)::uuid
    UNION ALL
    SELECT changed_at FROM app_runtime_config_scope_changes WHERE app_id = sqlc.arg(app_id)::uuid
        AND scope IN ('default', sqlc.arg(scope)::text)
) AS boundary;

-- name: LockSnapshotRuntimePublicationScope :one
SELECT a.id AS app_id, d.scope FROM apps a JOIN deployments d ON d.app_id = a.id
WHERE d.id = sqlc.arg(deployment_id)::uuid FOR UPDATE OF a;

-- name: LockSnapshotRuntimeSource :one
SELECT app_id, deployment_id, started_at FROM instances
WHERE id = sqlc.arg(instance_id)::uuid FOR UPDATE;

-- name: RecordInstanceRuntimeConfigReceipt :execrows
INSERT INTO instance_runtime_config_receipts(instance_id, wake_id, scope, boundary_at, variables, secret_versions, all_secrets,secret_refs,sidecar_secret_versions)
SELECT i.id, i.wake_id, d.scope, sqlc.arg(boundary_at)::timestamptz, sqlc.arg(variables)::jsonb, sqlc.arg(secret_versions)::jsonb, sqlc.arg(all_secrets)::boolean,sqlc.arg(secret_refs)::jsonb,sqlc.arg(sidecar_secret_versions)::jsonb
FROM instances i JOIN deployments d ON d.id = i.deployment_id
WHERE i.id = sqlc.arg(instance_id)::uuid AND i.wake_id = sqlc.arg(wake_id)::uuid AND i.state = 'running'
AND d.scope = sqlc.arg(scope)::text FOR UPDATE OF i
ON CONFLICT (instance_id) DO UPDATE SET wake_id = excluded.wake_id, scope = excluded.scope,
    boundary_at = excluded.boundary_at, variables = excluded.variables, secret_versions = excluded.secret_versions,
    all_secrets = excluded.all_secrets,secret_refs=excluded.secret_refs,sidecar_secret_versions=excluded.sidecar_secret_versions, acknowledged_at = now()
WHERE instance_runtime_config_receipts.wake_id <> excluded.wake_id OR
    (instance_runtime_config_receipts.scope = excluded.scope AND instance_runtime_config_receipts.boundary_at = excluded.boundary_at
     AND instance_runtime_config_receipts.variables = excluded.variables AND instance_runtime_config_receipts.secret_versions = excluded.secret_versions
     AND instance_runtime_config_receipts.all_secrets = excluded.all_secrets AND instance_runtime_config_receipts.secret_refs=excluded.secret_refs
     AND instance_runtime_config_receipts.sidecar_secret_versions=excluded.sidecar_secret_versions);

-- name: ClearInstanceRuntimeConfigReceipt :exec
DELETE FROM instance_runtime_config_receipts WHERE instance_id = sqlc.arg(instance_id)::uuid;

-- name: MigrateInstanceRuntimeConfig :one
UPDATE instances i SET node_id = sqlc.arg(to_node_id)::uuid,
    migrated_from_node_id = sqlc.arg(from_node_id)::uuid, migrated_at = now(), migration_started_at = NULL,
    state = 'running', wake_id = sqlc.arg(wake_id)::uuid, started_at = clock_timestamp(),
    netns = CASE WHEN sqlc.arg(netns)::text <> '' THEN sqlc.arg(netns)::text ELSE i.netns END,
    host_ip = CASE WHEN sqlc.arg(host_ip)::text <> '' THEN sqlc.arg(host_ip)::inet ELSE i.host_ip END,
    guest_uid = CASE WHEN sqlc.arg(guest_uid)::int > 0 THEN sqlc.arg(guest_uid)::int ELSE i.guest_uid END
WHERE i.id = sqlc.arg(instance_id)::uuid AND i.state = 'migrating'
    AND i.node_id = sqlc.arg(from_node_id)::uuid AND i.lease_token = sqlc.arg(lease_token)::text
    AND (NOT sqlc.arg(check_source_wake)::boolean OR i.wake_id IS NOT DISTINCT FROM sqlc.narg(expected_wake_id)::uuid)
    AND (NOT sqlc.arg(has_inputs)::boolean OR EXISTS (SELECT 1 FROM deployments d WHERE d.id = i.deployment_id AND d.scope = sqlc.arg(scope)::text))
RETURNING i.*;

-- name: StampRuntimeMigrationApp :exec
UPDATE apps SET migrated_at = now() WHERE id = sqlc.arg(app_id)::uuid;

-- name: LockInstanceMigrationCommit :one
SELECT id, node_id, state, wake_id, lease_token, migrated_from_node_id
FROM instances WHERE id = sqlc.arg(instance_id)::uuid FOR UPDATE;

-- name: AbortLockedInstanceMigration :execrows
UPDATE instances SET state = 'parked', lease_token = NULL, migration_started_at = NULL
WHERE id = sqlc.arg(instance_id)::uuid AND node_id = sqlc.arg(source_node_id)::uuid
    AND state = sqlc.arg(expected_state)::text AND lease_token = sqlc.arg(lease_token)::text
    AND wake_id IS NOT DISTINCT FROM sqlc.narg(source_wake_id)::uuid;

-- name: PublishInstanceRuntimeConfig :one
UPDATE instances i SET netns = sqlc.arg(netns), host_ip = sqlc.arg(host_ip)::text::inet,
    guest_uid = sqlc.arg(guest_uid), started_at = clock_timestamp(), state = 'running'
WHERE i.id = sqlc.arg(instance_id)::uuid AND i.state = sqlc.arg(expected_state)::text AND i.wake_id = sqlc.arg(wake_id)::uuid
AND EXISTS (SELECT 1 FROM deployments d WHERE d.id = i.deployment_id AND d.scope = sqlc.arg(scope)::text)
RETURNING i.*;

-- name: InstanceRuntimeConfigReceipt :one
SELECT r.* FROM instance_runtime_config_receipts r JOIN instances i ON i.id = r.instance_id AND i.wake_id = r.wake_id
WHERE r.instance_id = sqlc.arg(instance_id)::uuid;

-- name: SnapshotRuntimeConfigReceipt :one
SELECT * FROM snapshot_runtime_config_receipts WHERE snapshot_id = sqlc.arg(snapshot_id)::uuid;

-- name: RuntimeConfigInputsFresh :one
SELECT environment_runtime_inputs_fresh(sqlc.arg(app_id)::uuid, sqlc.arg(scope)::text, sqlc.arg(boundary_at)::timestamptz,
    sqlc.arg(variables)::jsonb, sqlc.arg(secret_versions)::jsonb, sqlc.arg(all_secrets)::boolean,sqlc.arg(secret_refs)::jsonb,sqlc.arg(sidecar_secret_versions)::jsonb)::boolean AS fresh;

-- name: RuntimeConfigReceiptRequired :one
SELECT environment_runtime_receipt_required(sqlc.arg(app_id)::uuid, sqlc.arg(scope)::text)::boolean AS required;

-- name: InsertSnapshotRuntimeConfigReceipt :exec
INSERT INTO snapshot_runtime_config_receipts(snapshot_id, scope, boundary_at, variables, secret_versions, all_secrets,secret_refs,sidecar_secret_versions)
VALUES (sqlc.arg(snapshot_id)::uuid, sqlc.arg(scope)::text, sqlc.arg(boundary_at)::timestamptz, sqlc.arg(variables)::jsonb,
    sqlc.arg(secret_versions)::jsonb, sqlc.arg(all_secrets)::boolean,sqlc.arg(secret_refs)::jsonb,sqlc.arg(sidecar_secret_versions)::jsonb);

-- name: ClaimEnvironmentGitSourcePoll :one
WITH candidate AS (
    SELECT p.source_id FROM environment_git_source_polls p JOIN environment_git_sources s ON s.id = p.source_id
    WHERE NOT s.suspended AND p.next_poll_at <= sqlc.arg(now_at)::timestamptz
      AND (p.lease_until IS NULL OR p.lease_until <= sqlc.arg(now_at)::timestamptz)
    ORDER BY p.next_poll_at, p.source_id FOR UPDATE OF p SKIP LOCKED LIMIT 1
)
UPDATE environment_git_source_polls p SET lease_token = sqlc.arg(lease_token)::uuid, lease_until = sqlc.arg(lease_until)::timestamptz
FROM candidate WHERE p.source_id = candidate.source_id RETURNING p.*;

-- name: FinishEnvironmentGitSourcePoll :execrows
UPDATE environment_git_source_polls SET lease_token = NULL, lease_until = NULL, next_poll_at = sqlc.arg(next_poll_at)::timestamptz
WHERE source_id = sqlc.arg(source_id)::uuid AND lease_token = sqlc.arg(lease_token)::uuid AND lease_until > greatest(sqlc.arg(now_at)::timestamptz, clock_timestamp());

-- name: RecordEnvironmentGitSourcePoll :exec
UPDATE environment_git_sources SET source_checked_at = sqlc.arg(checked_at)::timestamptz,
    source_error_code = sqlc.arg(error_code)::text,
    source_commit_sha = CASE WHEN sqlc.arg(error_code)::text = '' THEN sqlc.arg(commit_sha)::text ELSE source_commit_sha END,
    source_definition_digest = CASE WHEN sqlc.arg(error_code)::text = '' THEN sqlc.arg(definition_digest)::text ELSE source_definition_digest END,
    source_verified_at = CASE WHEN sqlc.arg(error_code)::text = '' THEN sqlc.arg(checked_at)::timestamptz ELSE source_verified_at END
WHERE id = sqlc.arg(source_id)::uuid;

-- name: EnvironmentGitSourceHealth :one
SELECT count(*) FILTER (WHERE NOT s.suspended)::bigint AS active,
    count(*) FILTER (WHERE s.suspended)::bigint AS suspended,
    count(*) FILTER (WHERE NOT s.suspended AND s.source_checked_at IS NULL)::bigint AS unchecked,
    count(*) FILTER (WHERE NOT s.suspended AND s.source_verified_at IS NULL)::bigint AS unverified,
    count(*) FILTER (WHERE NOT s.suspended AND coalesce(s.source_checked_at, s.created_at) <= sqlc.arg(stale_before)::timestamptz)::bigint AS poll_stale,
    count(*) FILTER (WHERE NOT s.suspended AND coalesce(s.source_verified_at, s.created_at) <= sqlc.arg(stale_before)::timestamptz)::bigint AS verification_stale,
    count(*) FILTER (WHERE NOT s.suspended AND s.source_error_code <> '')::bigint AS unavailable,
    count(*) FILTER (WHERE NOT s.suspended AND s.source_commit_sha <> '' AND
        (s.source_commit_sha IS DISTINCT FROM r.commit_sha OR s.source_definition_digest IS DISTINCT FROM r.definition_digest))::bigint AS candidate_pending_approval,
    count(*) FILTER (WHERE NOT s.suspended AND s.approved_revision_id IS NOT NULL AND s.approved_revision_id IS DISTINCT FROM s.applied_revision_id)::bigint AS approved_pending_apply,
    greatest(coalesce(max(extract(epoch FROM (sqlc.arg(now_at)::timestamptz - coalesce(s.source_checked_at, s.created_at)))) FILTER (WHERE NOT s.suspended), 0), 0)::double precision AS oldest_check_age_seconds,
    greatest(coalesce(max(extract(epoch FROM (sqlc.arg(now_at)::timestamptz - coalesce(s.source_verified_at, s.created_at)))) FILTER (WHERE NOT s.suspended), 0), 0)::double precision AS oldest_verification_age_seconds
FROM active_environment_git_sources s LEFT JOIN environment_desired_revisions r ON r.source_id = s.id AND r.id = s.approved_revision_id;

-- name: RequestEnvironmentGitOpsRuntimeRefresh :exec
UPDATE environment_gitops_runtime_effects SET requested_at = now(), next_request_at = sqlc.arg(next_request_at)::timestamptz
WHERE source_id = sqlc.arg(source_id)::uuid AND id = sqlc.arg(effect_id)::uuid AND completed_at IS NULL;

-- name: CompleteEnvironmentGitOpsRuntime :execrows
UPDATE environment_gitops_runtime_effects SET completed_at = now()
WHERE source_id = sqlc.arg(source_id)::uuid AND id = sqlc.arg(effect_id)::uuid AND completed_at IS NULL;

-- name: HasPendingEnvironmentGitOpsRuntime :one
SELECT EXISTS(SELECT 1 FROM environment_gitops_runtime_effects
WHERE source_id = sqlc.arg(source_id)::uuid AND completed_at IS NULL) AS pending;

-- name: HasEnvironmentGitOpsRuntimeDrift :one
SELECT EXISTS(SELECT 1 FROM environment_gitops_runtime_targets WHERE source_id = sqlc.arg(source_id)::uuid
AND (stale_residents > 0 OR starting_residents > 0 OR stale_snapshots > 0)
 UNION ALL
 SELECT 1 FROM environment_managed_fields WHERE source_id=sqlc.arg(source_id)::uuid
 AND (field_path='source' OR starts_with(field_path,'runtime/'))) AS drifted;

-- name: ListServiceRecoveryApps :many
SELECT a.id FROM apps a JOIN accounts ac ON ac.id = a.account_id
LEFT JOIN service_recovery r ON r.app_id = a.id
WHERE a.status = 'active' AND a.manifest->>'execution_mode' = 'service'
  AND ac.status IN ('active', 'past_due') AND ac.abuse_hold_at IS NULL
  AND (sqlc.arg(owner_node_id)::text = '' OR a.node_id IS NULL OR a.node_id::text = sqlc.arg(owner_node_id)::text)
  AND coalesce(r.lease_until, '1970-01-01'::timestamptz) <= sqlc.arg(sampled_at)::timestamptz
  AND coalesce(r.next_attempt_at, '1970-01-01'::timestamptz) <= sqlc.arg(sampled_at)::timestamptz
ORDER BY coalesce(r.next_attempt_at, '1970-01-01'::timestamptz), a.id
LIMIT sqlc.arg(batch_limit)::integer;

-- name: ClaimServiceRecovery :one
INSERT INTO service_recovery(app_id, revision, claim_token, lease_until, status, next_attempt_at, updated_at)
SELECT a.id, sqlc.arg(revision)::text, sqlc.arg(claim_token)::uuid, sqlc.arg(lease_until)::timestamptz,
       'reconciling', sqlc.arg(sampled_at)::timestamptz, sqlc.arg(sampled_at)::timestamptz
FROM apps a JOIN accounts ac ON ac.id = a.account_id
WHERE a.id = sqlc.arg(app_id)::uuid AND a.status = 'active' AND a.manifest->>'execution_mode' = 'service'
  AND ac.status IN ('active', 'past_due') AND ac.abuse_hold_at IS NULL
ON CONFLICT (app_id) DO UPDATE SET
 revision = EXCLUDED.revision, claim_token = EXCLUDED.claim_token, lease_until = EXCLUDED.lease_until,
 status = 'reconciling', updated_at = EXCLUDED.updated_at,
 failures = CASE WHEN service_recovery.revision <> EXCLUDED.revision THEN 0 ELSE service_recovery.failures END
WHERE service_recovery.lease_until <= EXCLUDED.updated_at
  AND (service_recovery.revision <> EXCLUDED.revision
    OR service_recovery.status NOT IN ('waiting_capacity', 'retrying_startup', 'waiting_dependency')
    OR service_recovery.next_attempt_at <= EXCLUDED.updated_at)
RETURNING *;

-- name: CompleteServiceRecovery :execrows
UPDATE service_recovery SET status = sqlc.arg(status)::text, failures = sqlc.arg(failures)::integer,
 next_attempt_at = sqlc.arg(next_attempt_at)::timestamptz, updated_at = sqlc.arg(updated_at)::timestamptz,
 claim_token = NULL, lease_until = '1970-01-01'::timestamptz
WHERE app_id = sqlc.arg(app_id)::uuid AND claim_token = sqlc.arg(claim_token)::uuid;

-- name: ServiceRecoveryByApp :one
SELECT * FROM service_recovery WHERE app_id = $1;

-- ADR-421: keyset paging advances even when a candidate cannot fit.
-- name: ListOrphanedAppsPage :many
SELECT a.id, a.node_id, a.ram_mb FROM apps a
WHERE a.node_id IS NOT NULL AND a.status IN ('active', 'evicted_cold')
  AND NOT EXISTS (SELECT 1 FROM compute_nodes n WHERE n.id=a.node_id AND n.active)
  AND (sqlc.narg(after_app_id)::uuid IS NULL OR a.id > sqlc.narg(after_app_id)::uuid)
  AND (sqlc.narg(dead_node_id)::uuid IS NULL OR a.node_id=sqlc.narg(dead_node_id)::uuid)
  AND (sqlc.arg(cooldown_seconds)::integer < 0 OR a.reassigned_at IS NULL
       OR a.reassigned_at < now() - make_interval(secs => sqlc.arg(cooldown_seconds)::integer))
ORDER BY a.id LIMIT sqlc.arg(batch_limit)::integer;

-- Lifecycle writers take incompatible locks, held until the transfer commits.
-- Stable node order avoids deadlocks between transfers in opposite directions.
-- name: LockOwnershipRecoveryNodes :many
SELECT id, lifecycle FROM compute_nodes
WHERE id IN (sqlc.arg(from_node_id)::uuid, sqlc.arg(to_node_id)::uuid)
ORDER BY id FOR SHARE;

-- name: ReassignOrphanedAppOwner :execrows
UPDATE apps SET node_id=sqlc.arg(to_node_id)::uuid, reassigned_at=now()
WHERE id=sqlc.arg(app_id)::uuid AND node_id=sqlc.arg(from_node_id)::uuid
  AND status IN ('active', 'evicted_cold')
  AND (sqlc.arg(cooldown_seconds)::integer < 0 OR reassigned_at IS NULL
       OR reassigned_at < now() - make_interval(secs => sqlc.arg(cooldown_seconds)::integer));

-- name: ServiceCapacityProtection :one
SELECT service_capacity_snapshot()::jsonb AS snapshot;

-- name: SetServiceCapacityProtection :one
SELECT set_service_capacity_protection(sqlc.arg(enabled)::boolean)::jsonb AS snapshot;

-- name: ServiceCapacityPlacement :one
SELECT CASE WHEN enabled THEN service_capacity_snapshot()
            ELSE jsonb_build_object('enabled', false) END::jsonb AS snapshot
FROM service_capacity_policy WHERE singleton;

-- name: LockCustomerOperationAccount :one
SELECT plan::text FROM accounts WHERE id = sqlc.arg(account_id)::uuid FOR UPDATE;

-- name: CustomerOperationBlobUsage :one
SELECT count(*)::bigint AS blob_count, COALESCE(sum(size_bytes),0)::bigint AS bytes
FROM customer_operation_result_blobs WHERE account_id = sqlc.arg(account_id)::uuid;

-- name: CustomerOperationBlobMetrics :many
SELECT state, count(*)::bigint AS blob_count, COALESCE(sum(size_bytes),0)::bigint AS bytes
FROM customer_operation_result_blobs GROUP BY state;

-- name: InsertCustomerOperationBlob :exec
INSERT INTO customer_operation_result_blobs
(id,operation_id,account_id,generation,execution_id,attempt,report_id,fingerprint,storage_key,size_bytes,state,expires_at,next_attempt_at)
VALUES (sqlc.arg(id)::uuid,sqlc.arg(operation_id)::uuid,sqlc.arg(account_id)::uuid,sqlc.arg(generation)::integer,
sqlc.arg(execution_id)::uuid,sqlc.arg(attempt)::integer,sqlc.arg(report_id)::text,sqlc.arg(fingerprint)::text,
sqlc.arg(storage_key)::text,sqlc.arg(size_bytes)::bigint,'staging',sqlc.arg(expires_at)::timestamptz,sqlc.arg(expires_at)::timestamptz);

-- name: LockCustomerOperationBlob :one
SELECT * FROM customer_operation_result_blobs WHERE id = sqlc.arg(id)::uuid FOR UPDATE;

-- name: CustomerOperationBlobByKey :one
SELECT * FROM customer_operation_result_blobs WHERE storage_key = sqlc.arg(storage_key)::text;

-- name: RetainCustomerOperationBlob :execrows
UPDATE customer_operation_result_blobs SET state = 'retained'
WHERE id = sqlc.arg(id)::uuid AND state = 'staging' AND expires_at > sqlc.arg(now)::timestamptz;

-- name: ClaimCustomerOperationBlobCleanup :one
WITH candidate AS (
 SELECT b.id FROM customer_operation_result_blobs b
 WHERE b.next_attempt_at <= sqlc.arg(now)::timestamptz
 AND (b.lease_until IS NULL OR b.lease_until <= sqlc.arg(now)::timestamptz)
 AND (b.state = 'deleting' OR (b.state = 'staging' AND b.expires_at <= sqlc.arg(now)::timestamptz)
 OR (b.state = 'retained' AND NOT EXISTS (
  SELECT 1 FROM customer_operations o WHERE o.id = b.operation_id
  AND (o.expires_at > sqlc.arg(now)::timestamptz OR o.state IN ('accepted','running'))
  AND EXISTS (SELECT 1 FROM jsonb_each_text(COALESCE(o.record->'artifact_storage_keys','{}'::jsonb)) k WHERE k.value = b.storage_key)
 )))
 ORDER BY b.next_attempt_at,b.id FOR UPDATE OF b SKIP LOCKED LIMIT 1
)
UPDATE customer_operation_result_blobs b SET state = 'deleting', lease_token = sqlc.arg(lease_token)::text,
lease_until = sqlc.arg(lease_until)::timestamptz
FROM candidate c WHERE b.id = c.id RETURNING b.*;

-- name: RetryCustomerOperationBlobCleanup :execrows
UPDATE customer_operation_result_blobs SET lease_token = '',lease_until = NULL,next_attempt_at = sqlc.arg(next_attempt_at)::timestamptz
WHERE id = sqlc.arg(id)::uuid AND state = 'deleting' AND lease_token = sqlc.arg(lease_token)::text
AND lease_until > sqlc.arg(now)::timestamptz;

-- name: CompleteCustomerOperationBlobCleanup :execrows
DELETE FROM customer_operation_result_blobs WHERE id = sqlc.arg(id)::uuid AND state = 'deleting'
AND lease_token = sqlc.arg(lease_token)::text AND lease_until > sqlc.arg(now)::timestamptz;

-- name: CustomerOperationDeploymentScope :one
SELECT d.scope FROM deployments d JOIN apps a ON a.id = d.app_id
WHERE d.id = sqlc.arg(deployment_id)::uuid AND a.id = sqlc.arg(app_id)::uuid
AND a.account_id = sqlc.arg(account_id)::uuid AND a.status <> 'deleted';

-- name: CustomerOperationDeploymentDefinition :one
SELECT d.scope,d.workflows FROM deployments d JOIN apps a ON a.id = d.app_id
WHERE d.id = sqlc.arg(deployment_id)::uuid AND a.id = sqlc.arg(app_id)::uuid
AND a.account_id = sqlc.arg(account_id)::uuid AND a.status <> 'deleted';

-- name: LockCustomerOperationTenant :one
SELECT status FROM platform_tenants
WHERE id = sqlc.arg(tenant_id)::uuid AND account_id = sqlc.arg(account_id)::uuid FOR SHARE;

-- name: CountCustomerOperationDefinitionNames :one
SELECT count(DISTINCT name)::bigint FROM customer_operation_definitions
WHERE app_id = sqlc.arg(app_id)::uuid AND scope = sqlc.arg(scope)::text;

-- name: CustomerOperationDefinitionNameExists :one
SELECT EXISTS(SELECT 1 FROM customer_operation_definitions
WHERE app_id = sqlc.arg(app_id)::uuid AND scope = sqlc.arg(scope)::text AND name = sqlc.arg(name)::text);

-- name: InsertCustomerOperationDefinition :one
INSERT INTO customer_operation_definitions(id, account_id, app_id, scope, name, revision, deployment_id, release_id, spec, workflow_snapshot)
VALUES(sqlc.arg(id)::uuid,sqlc.arg(account_id)::uuid,sqlc.arg(app_id)::uuid,sqlc.arg(scope)::text,
       sqlc.arg(name)::text,sqlc.arg(revision)::text,sqlc.arg(deployment_id)::uuid,sqlc.arg(release_id)::text,sqlc.arg(spec)::jsonb,sqlc.narg(workflow_snapshot)::jsonb)
RETURNING id::text,account_id::text,app_id::text,scope,name,revision,deployment_id::text,release_id,spec,workflow_snapshot,created_at;

-- name: GetCustomerOperationDefinition :one
SELECT id::text,account_id::text,app_id::text,scope,name,revision,deployment_id::text,release_id,spec,workflow_snapshot,created_at
FROM customer_operation_definitions WHERE id=sqlc.arg(id)::uuid AND account_id=sqlc.arg(account_id)::uuid;

-- name: GetCustomerOperationDefinitionForDeployment :one
SELECT id::text,account_id::text,app_id::text,scope,name,revision,deployment_id::text,release_id,spec,workflow_snapshot,created_at
FROM customer_operation_definitions WHERE app_id=sqlc.arg(app_id)::uuid AND account_id=sqlc.arg(account_id)::uuid
AND deployment_id=sqlc.arg(deployment_id)::uuid AND name=sqlc.arg(name)::text;

-- name: GetCustomerOperationIdempotency :one
SELECT operation_id::text,fingerprint,expires_at,
 EXISTS(SELECT 1 FROM customer_operations o WHERE o.id=customer_operation_idempotency.operation_id AND o.state IN ('accepted','running')) AS active
FROM customer_operation_idempotency
WHERE scope_digest=sqlc.arg(scope_digest)::text AND account_id=sqlc.arg(account_id)::uuid;

-- name: PutCustomerOperationIdempotency :exec
INSERT INTO customer_operation_idempotency(scope_digest,account_id,app_id,operation_id,fingerprint,expires_at)
VALUES(sqlc.arg(scope_digest)::text,sqlc.arg(account_id)::uuid,sqlc.arg(app_id)::uuid,
       sqlc.arg(operation_id)::uuid,sqlc.arg(fingerprint)::text,sqlc.arg(expires_at)::timestamptz)
ON CONFLICT(scope_digest) DO UPDATE SET operation_id=EXCLUDED.operation_id,fingerprint=EXCLUDED.fingerprint,expires_at=EXCLUDED.expires_at;

-- name: CountPendingCustomerOperations :one
SELECT count(*)::bigint FROM customer_operations WHERE account_id=sqlc.arg(account_id)::uuid
AND state IN ('accepted','running','requires_reconciliation');

-- name: InsertCustomerOperation :exec
INSERT INTO customer_operations(id,account_id,app_id,platform_tenant_id,definition_id,current_invocation_id,state,record,expires_at,created_at)
VALUES(sqlc.arg(id)::uuid,sqlc.arg(account_id)::uuid,sqlc.arg(app_id)::uuid,sqlc.arg(tenant_id)::uuid,
       sqlc.arg(definition_id)::uuid,sqlc.narg(invocation_id)::uuid,sqlc.arg(state)::text,
       sqlc.arg(record)::jsonb,sqlc.arg(expires_at)::timestamptz,sqlc.arg(created_at)::timestamptz);

-- name: InsertCustomerOperationExecution :exec
INSERT INTO customer_operation_executions(operation_id,generation,invocation_id)
VALUES(sqlc.arg(operation_id)::uuid,sqlc.arg(generation)::integer,sqlc.arg(invocation_id)::uuid);

-- name: InsertCustomerOperationEvent :exec
INSERT INTO customer_operation_events(operation_id,sequence,event_type,execution_id,attempt,data,created_at)
VALUES(sqlc.arg(operation_id)::uuid,sqlc.arg(sequence)::bigint,sqlc.arg(event_type)::text,
       sqlc.narg(execution_id)::uuid,sqlc.arg(attempt)::integer,sqlc.arg(data)::jsonb,sqlc.arg(created_at)::timestamptz);

-- name: GetCustomerOperation :one
SELECT record FROM customer_operations WHERE id=sqlc.arg(id)::uuid AND account_id=sqlc.arg(account_id)::uuid
AND (sqlc.arg(tenant_id)::text='' OR platform_tenant_id::text=sqlc.arg(tenant_id)::text);

-- name: ListPlatformTenantCustomerOperations :many
-- The tenant creation index supports descending keyset paging. Only public
-- summary fields cross this boundary; source input/results/capabilities do not.
SELECT jsonb_build_object(
 'id', o.record->'id',
 'name', o.record->'name',
 'generation', o.record->'generation',
 'state', o.record->'state',
 'progress', o.record->'progress',
 'cancellation_requested', o.record->'cancellation_requested',
 'latest_sequence', o.record->'latest_sequence',
 'updated_at', o.record->'updated_at',
 'expires_at', o.record->'expires_at',
 'created_at', o.created_at,
 'completion_delivery', jsonb_strip_nulls(jsonb_build_object(
   'state', CASE WHEN coalesce(o.record->'completion_delivery'->>'delivery_id','') = ''
     THEN o.record->'completion_delivery'->>'state'
     WHEN delivery.id IS NULL THEN 'delivery_expired' ELSE delivery.status END,
   'attempts', coalesce(delivery.attempt,(o.record->'completion_delivery'->>'attempts')::integer,0),
   'next_attempt_at', CASE WHEN delivery.status IN ('pending','failed') THEN delivery.next_attempt_at END
 ))) AS summary
FROM customer_operations o
JOIN customer_operation_definitions d ON d.id=o.definition_id AND d.account_id=o.account_id AND d.app_id=o.app_id
LEFT JOIN app_webhook_deliveries delivery ON delivery.id=nullif(o.record->'completion_delivery'->>'delivery_id','')::uuid
 AND delivery.account_id=o.account_id AND delivery.app_id=o.app_id
WHERE o.account_id=sqlc.arg(account_id)::uuid AND o.platform_tenant_id=sqlc.arg(tenant_id)::uuid
 AND o.app_id=sqlc.arg(app_id)::uuid AND d.scope=sqlc.arg(scope)::text
 AND (sqlc.arg(operation_name)::text='' OR d.name=sqlc.arg(operation_name)::text)
 AND (sqlc.arg(operation_state)::text='' OR o.state=sqlc.arg(operation_state)::text)
 AND (o.state IN ('accepted','running') OR o.expires_at>sqlc.arg(now)::timestamptz)
 AND (sqlc.narg(before_created_at)::timestamptz IS NULL OR
      (o.created_at,o.id)<(sqlc.narg(before_created_at)::timestamptz,sqlc.narg(before_id)::uuid))
ORDER BY o.created_at DESC,o.id DESC LIMIT sqlc.arg(page_limit)::integer;

-- name: ListAccountCustomerOperations :many
-- The account/app creation index supports descending keyset paging across
-- tenants. Only explicit public summary fields cross this operator boundary.
SELECT jsonb_build_object(
 'platform_tenant_id', o.platform_tenant_id,
 'id', o.record->'id',
 'name', o.record->'name',
 'generation', o.record->'generation',
 'state', o.record->'state',
 'progress', o.record->'progress',
 'cancellation_requested', o.record->'cancellation_requested',
 'latest_sequence', o.record->'latest_sequence',
 'updated_at', o.record->'updated_at',
 'expires_at', o.record->'expires_at',
 'created_at', o.created_at,
 'completion_delivery', jsonb_strip_nulls(jsonb_build_object(
   'state', CASE WHEN coalesce(o.record->'completion_delivery'->>'delivery_id','') = ''
     THEN o.record->'completion_delivery'->>'state'
     WHEN delivery.id IS NULL THEN 'delivery_expired' ELSE delivery.status END,
   'attempts', coalesce(delivery.attempt,(o.record->'completion_delivery'->>'attempts')::integer,0),
   'next_attempt_at', CASE WHEN delivery.status IN ('pending','failed') THEN delivery.next_attempt_at END
 ))) AS summary
FROM customer_operations o
JOIN customer_operation_definitions d ON d.id=o.definition_id AND d.account_id=o.account_id AND d.app_id=o.app_id
LEFT JOIN app_webhook_deliveries delivery ON delivery.id=nullif(o.record->'completion_delivery'->>'delivery_id','')::uuid
 AND delivery.account_id=o.account_id AND delivery.app_id=o.app_id
WHERE o.account_id=sqlc.arg(account_id)::uuid AND (sqlc.arg(tenant_id)::text='' OR o.platform_tenant_id::text=sqlc.arg(tenant_id)::text)
 AND o.app_id=sqlc.arg(app_id)::uuid AND d.scope=sqlc.arg(scope)::text
 AND (sqlc.arg(operation_name)::text='' OR d.name=sqlc.arg(operation_name)::text)
 AND (sqlc.arg(operation_state)::text='' OR o.state=sqlc.arg(operation_state)::text)
 AND (o.state IN ('accepted','running') OR o.expires_at>sqlc.arg(now)::timestamptz)
 AND (sqlc.narg(before_created_at)::timestamptz IS NULL OR
      (o.created_at,o.id)<(sqlc.narg(before_created_at)::timestamptz,sqlc.narg(before_id)::uuid))
ORDER BY o.created_at DESC,o.id DESC LIMIT sqlc.arg(page_limit)::integer;

-- name: ListAccountCustomerOperationExecutions :many
SELECT e.generation,i.id::text AS invocation_id,i.state,i.attempts,i.created_at,i.completed_at
FROM customer_operation_executions e
JOIN customer_operations o ON o.id=e.operation_id
JOIN invocations i ON i.id=e.invocation_id AND i.account_id=o.account_id AND i.app_id=o.app_id
WHERE o.id=sqlc.arg(operation_id)::uuid AND o.account_id=sqlc.arg(account_id)::uuid
 AND e.generation>sqlc.arg(after_generation)::integer
ORDER BY e.generation LIMIT sqlc.arg(page_limit)::integer;

-- name: ReadCustomerOperationEvents :many
SELECT operation_id::text,sequence,event_type,coalesce(execution_id::text,''::text)::text AS execution_id,attempt,data,created_at
FROM customer_operation_events WHERE operation_id=sqlc.arg(operation_id)::uuid AND sequence>sqlc.arg(after_sequence)::bigint
AND sequence<=sqlc.arg(latest_sequence)::bigint
ORDER BY sequence LIMIT sqlc.arg(page_limit)::integer;

-- name: LockCustomerOperationExecution :one
SELECT o.record FROM customer_operations o JOIN customer_operation_executions e ON e.operation_id=o.id
WHERE e.invocation_id=sqlc.arg(invocation_id)::uuid FOR UPDATE OF o;

-- name: UpdateCustomerOperation :exec
UPDATE customer_operations SET current_invocation_id=sqlc.narg(invocation_id)::uuid,
 state=sqlc.arg(state)::text,record=sqlc.arg(record)::jsonb,expires_at=sqlc.arg(expires_at)::timestamptz
WHERE id=sqlc.arg(id)::uuid;

-- name: GetCustomerOperationReport :one
SELECT fingerprint FROM customer_operation_reports
WHERE operation_id=sqlc.arg(operation_id)::uuid AND execution_id=sqlc.arg(execution_id)::uuid
 AND attempt=sqlc.arg(attempt)::integer AND report_id=sqlc.arg(report_id)::text;

-- name: InsertCustomerOperationReport :exec
INSERT INTO customer_operation_reports(operation_id,execution_id,attempt,report_id,fingerprint)
VALUES(sqlc.arg(operation_id)::uuid,sqlc.arg(execution_id)::uuid,sqlc.arg(attempt)::integer,
 sqlc.arg(report_id)::text,sqlc.arg(fingerprint)::text);

-- name: CustomerOperationCompletionWebhook :one
SELECT enabled FROM app_webhooks WHERE id=sqlc.arg(id)::uuid
 AND account_id=sqlc.arg(account_id)::uuid AND app_id=sqlc.arg(app_id)::uuid FOR SHARE;

-- name: InsertCustomerOperationCompletionDelivery :exec
INSERT INTO app_webhook_deliveries(id,webhook_id,app_id,account_id,event,payload,next_attempt_at)
VALUES(sqlc.arg(id)::uuid,sqlc.arg(webhook_id)::uuid,sqlc.arg(app_id)::uuid,
 sqlc.arg(account_id)::uuid,'operation.finished',sqlc.arg(payload)::jsonb,sqlc.arg(now)::timestamptz);

-- name: NotifyCustomerOperation :exec
SELECT pg_notify('customer_operation_changed',sqlc.arg(operation_id)::text);

-- name: CustomerOperationAccountPlan :one
SELECT plan::text FROM accounts WHERE id=sqlc.arg(account_id)::uuid;

-- name: ListExpiredCustomerOperationExecutions :many
SELECT i.id::text FROM invocations i WHERE i.state='dispatching' AND i.lease_expires_at<=sqlc.arg(now)::timestamptz
 AND EXISTS(SELECT 1 FROM customer_operation_executions e WHERE e.invocation_id=i.id)
ORDER BY i.lease_expires_at,i.id LIMIT sqlc.arg(page_limit)::integer FOR UPDATE SKIP LOCKED;

-- name: RecoverExpiredCustomerOperationExecution :exec
UPDATE invocations SET state=sqlc.arg(state)::text,quota_reserved=false,due_at=sqlc.arg(now)::timestamptz,
 lease_expires_at=NULL,instance_id=NULL,last_error=sqlc.arg(last_error)::text,
 outcome=CASE WHEN sqlc.arg(state)::text='failed' THEN 'failed' ELSE NULL END,
 completed_at=CASE WHEN sqlc.arg(state)::text='failed' THEN sqlc.arg(now)::timestamptz ELSE completed_at END
WHERE id=sqlc.arg(id)::uuid;

-- name: GetCustomerOperationRecovery :one
SELECT fingerprint FROM customer_operation_recoveries
WHERE operation_id=sqlc.arg(operation_id)::uuid AND recovery_id=sqlc.arg(recovery_id)::text;

-- name: InsertCustomerOperationRecovery :exec
INSERT INTO customer_operation_recoveries(operation_id,recovery_id,fingerprint,request,created_at)
VALUES(sqlc.arg(operation_id)::uuid,sqlc.arg(recovery_id)::text,sqlc.arg(fingerprint)::text,
 sqlc.arg(request)::jsonb,sqlc.arg(now)::timestamptz);

-- name: CustomerOperationIDForInvocation :one
SELECT coalesce(i.operation_id::text,e.operation_id::text,'')::text AS operation_id FROM invocations i
LEFT JOIN customer_operation_executions e ON e.invocation_id=i.id WHERE i.id=sqlc.arg(invocation_id)::uuid;

-- name: PruneCustomerOperationEvents :execrows
WITH doomed AS (SELECT e.operation_id,e.sequence FROM customer_operation_events e JOIN customer_operations o ON o.id=e.operation_id
 WHERE (o.record->>'event_expires_at')::timestamptz<=sqlc.arg(now)::timestamptz
 ORDER BY e.created_at,e.operation_id,e.sequence LIMIT sqlc.arg(page_limit)::integer)
DELETE FROM customer_operation_events e USING doomed d WHERE e.operation_id=d.operation_id AND e.sequence=d.sequence;

-- name: PruneCustomerOperations :execrows
WITH doomed AS (SELECT id FROM customer_operations
 WHERE expires_at<=sqlc.arg(now)::timestamptz AND state IN ('succeeded','failed','cancelled','requires_reconciliation')
 ORDER BY expires_at,id LIMIT sqlc.arg(page_limit)::integer FOR UPDATE SKIP LOCKED)
DELETE FROM customer_operations o USING doomed d WHERE o.id=d.id;

-- name: PruneCustomerOperationIdempotency :execrows
WITH doomed AS (SELECT scope_digest FROM customer_operation_idempotency i WHERE expires_at<=sqlc.arg(now)::timestamptz
 AND NOT EXISTS(SELECT 1 FROM customer_operations o WHERE o.id=i.operation_id AND o.state IN ('accepted','running'))
 ORDER BY expires_at,scope_digest LIMIT sqlc.arg(page_limit)::integer)
DELETE FROM customer_operation_idempotency i USING doomed d WHERE i.scope_digest=d.scope_digest;

-- name: CancelCustomerOperationExecution :exec
UPDATE invocations SET state='cancelled',quota_reserved=false,completed_at=sqlc.arg(now)::timestamptz
WHERE id=sqlc.arg(id)::uuid AND state='pending';

-- name: StampCustomerOperationExecutionAttempt :execrows
UPDATE invocations SET instance_id=sqlc.arg(instance_id)::uuid
WHERE id=sqlc.arg(id)::uuid AND state='dispatching' AND attempts=sqlc.arg(attempt)::integer
AND lease_expires_at>sqlc.arg(now)::timestamptz
AND EXISTS(SELECT 1 FROM customer_operation_executions e WHERE e.invocation_id=invocations.id);

-- name: GetCustomerOperationDefinitionForRoute :one
SELECT id::text,account_id::text,app_id::text,scope,name,revision,deployment_id::text,release_id,spec,workflow_snapshot,created_at
FROM customer_operation_definitions WHERE account_id=sqlc.arg(account_id)::uuid AND app_id=sqlc.arg(app_id)::uuid
AND deployment_id=sqlc.arg(deployment_id)::uuid AND spec->>'method'=sqlc.arg(method)::text AND spec->>'path'=sqlc.arg(path)::text;

-- name: ListCustomerOperationDefinitionsForDeployment :many
SELECT id::text,account_id::text,app_id::text,scope,name,revision,deployment_id::text,release_id,spec,workflow_snapshot,created_at
FROM customer_operation_definitions WHERE account_id=sqlc.arg(account_id)::uuid AND app_id=sqlc.arg(app_id)::uuid
AND deployment_id=sqlc.arg(deployment_id)::uuid ORDER BY name;

-- name: PruneAccountCustomerOperationStreams :exec
DELETE FROM customer_operation_stream_leases WHERE account_id=sqlc.arg(account_id)::uuid AND expires_at<=sqlc.arg(now)::timestamptz;

-- name: CountCustomerOperationStreams :one
SELECT count(*)::bigint FROM customer_operation_stream_leases WHERE account_id=sqlc.arg(account_id)::uuid;

-- name: InsertCustomerOperationStream :exec
INSERT INTO customer_operation_stream_leases(id,account_id,operation_id,expires_at)
VALUES(sqlc.arg(id)::uuid,sqlc.arg(account_id)::uuid,sqlc.arg(operation_id)::uuid,sqlc.arg(expires_at)::timestamptz);

-- name: RenewCustomerOperationStream :execrows
UPDATE customer_operation_stream_leases SET expires_at=sqlc.arg(expires_at)::timestamptz
WHERE id=sqlc.arg(id)::uuid AND account_id=sqlc.arg(account_id)::uuid AND expires_at>sqlc.arg(now)::timestamptz;

-- name: DeleteCustomerOperationStream :exec
DELETE FROM customer_operation_stream_leases WHERE id=sqlc.arg(id)::uuid;

-- name: PruneCustomerOperationStreams :execrows
WITH doomed AS (SELECT id FROM customer_operation_stream_leases WHERE expires_at<=sqlc.arg(now)::timestamptz
ORDER BY expires_at,id LIMIT sqlc.arg(page_limit)::integer)
DELETE FROM customer_operation_stream_leases l USING doomed d WHERE l.id=d.id;

-- name: LockCustomerOperationDeployment :one
SELECT d.status::text FROM deployments d JOIN apps a ON a.id=d.app_id
WHERE d.id=sqlc.arg(deployment_id)::uuid AND d.app_id=sqlc.arg(app_id)::uuid
AND d.scope=sqlc.arg(scope)::text
AND a.account_id=sqlc.arg(account_id)::uuid AND a.status<>'deleted' FOR SHARE OF a,d;

-- name: PinCustomerOperationDeployment :execrows
INSERT INTO customer_operation_code_pins(deployment_id,app_id,expires_at)
SELECT d.id,d.app_id,sqlc.arg(expires_at)::timestamptz FROM deployments d JOIN apps a ON a.id=d.app_id
WHERE d.id=sqlc.arg(deployment_id)::uuid AND d.app_id=sqlc.arg(app_id)::uuid AND d.scope=sqlc.arg(scope)::text
AND a.account_id=sqlc.arg(account_id)::uuid AND a.status<>'deleted'
ON CONFLICT(deployment_id) DO UPDATE SET expires_at=greatest(customer_operation_code_pins.expires_at,excluded.expires_at);

-- name: SetCustomerOperationExecutionIdentity :exec
UPDATE invocations SET operation_id=sqlc.arg(operation_id)::uuid
WHERE id=sqlc.arg(invocation_id)::uuid AND operation_id IS NULL;

-- name: LockCustomerOperationClaim :one
SELECT id::text, app_id::text, account_id::text,
       coalesce(platform_tenant_id::text,'')::text AS platform_tenant_id,
       coalesce(instance_id::text,'')::text AS instance_id,
       state, attempts, lease_expires_at
FROM invocations WHERE id = sqlc.arg(id) FOR UPDATE;

-- name: CustomerOperationStateMetrics :many
SELECT state,count(*)::bigint AS retained_count,min(created_at)::timestamptz AS oldest_created_at
FROM customer_operations WHERE expires_at>sqlc.arg(now)::timestamptz OR state IN ('accepted','running')
GROUP BY state ORDER BY state;

-- name: CustomerOperationStreamMetric :one
SELECT count(*)::bigint FROM customer_operation_stream_leases WHERE expires_at>sqlc.arg(now)::timestamptz;

-- name: LockCustomerOperationInvocation :one
SELECT * FROM invocations WHERE id=sqlc.arg(id)::uuid FOR UPDATE;

-- name: DeleteCustomerOperationsForOwner :exec
DELETE FROM customer_operations WHERE account_id=sqlc.narg(account_id)::uuid OR app_id=sqlc.narg(app_id)::uuid;

-- name: DeleteCustomerOperationDefinitionsForOwner :exec
DELETE FROM customer_operation_definitions WHERE account_id=sqlc.narg(account_id)::uuid OR app_id=sqlc.narg(app_id)::uuid;

-- name: DeleteCustomerOperationReceiptsForOwner :exec
DELETE FROM customer_operation_idempotency WHERE account_id=sqlc.narg(account_id)::uuid OR app_id=sqlc.narg(app_id)::uuid;

-- name: DeleteCustomerOperationExecutionsForOwner :exec
DELETE FROM invocations WHERE operation_id IS NOT NULL
 AND (account_id=sqlc.narg(account_id)::uuid OR app_id=sqlc.narg(app_id)::uuid);

-- name: RenewCustomerOperationExecution :execrows
UPDATE invocations
SET lease_expires_at = least(now() + sqlc.arg(lease_seconds)::integer * interval '1 second', deadline_at)
WHERE id = sqlc.arg(id)::uuid AND operation_id IS NOT NULL
  AND state = 'dispatching' AND attempts = sqlc.arg(attempt)::integer
  AND lease_expires_at > now() AND deadline_at > now();

-- name: FeatureFlagRequestOutcomes :many
WITH evidence AS MATERIALIZED (
 SELECT
  COALESCE(e->>'type', 'boolean')::text AS decision_type,
  (e->>'value')::text AS decision_value,
  e->>'used' = 'true' AS used,
  t.status,
  t.latency_ms,
  t.count::bigint AS request_count
 FROM request_telemetry t
 JOIN deployments d ON d.id = t.deployment_id
 CROSS JOIN LATERAL jsonb_array_elements(t.flag_evidence) AS decision(e)
 WHERE (d.scope = sqlc.arg(environment_slug)::text OR (d.scope = 'default' AND sqlc.arg(environment_slug)::text = 'production'))
  AND t.account_id = sqlc.arg(account_id)::uuid
  AND t.app_id = ANY(sqlc.arg(app_ids)::uuid[])
  AND t.received_at >= sqlc.arg(received_from)::timestamptz
  AND t.received_at < sqlc.arg(received_until)::timestamptz
  AND (sqlc.arg(customer_id)::text = '' OR t.platform_tenant_id::text = sqlc.arg(customer_id)::text)
  AND e->>'flag' = sqlc.arg(flag_key)::text
  AND (sqlc.arg(rule_id)::text = '' OR e->>'rule_id' = sqlc.arg(rule_id)::text)
  AND (sqlc.arg(config_version)::bigint = 0 OR (e->>'config_version')::bigint = sqlc.arg(config_version)::bigint)
  AND jsonb_typeof(e->'value') IN ('boolean', 'string')
  AND COALESCE(e->>'type', 'boolean') IN ('boolean', 'variant')
), totals AS (
 SELECT decision_type, decision_value,
  sum(request_count)::bigint AS request_count,
  COALESCE(sum(request_count) FILTER (WHERE used), 0)::bigint AS used_count,
  COALESCE(sum(request_count) FILTER (WHERE status >= 500), 0)::bigint AS error_count
 FROM evidence
 GROUP BY decision_type, decision_value
), latency_counts AS (
 SELECT decision_type, decision_value, latency_ms, sum(request_count)::bigint AS latency_count
 FROM evidence
 GROUP BY decision_type, decision_value, latency_ms
), latency_ranked AS (
 SELECT decision_type, decision_value, latency_ms,
  sum(latency_count) OVER (PARTITION BY decision_type, decision_value ORDER BY latency_ms) AS cumulative_count
 FROM latency_counts
)
SELECT totals.decision_type, totals.decision_value, totals.request_count, totals.used_count, totals.error_count,
 min(latency_ranked.latency_ms) FILTER (WHERE latency_ranked.cumulative_count >= ceil(totals.request_count * 0.50))::int AS p50_latency_ms,
 min(latency_ranked.latency_ms) FILTER (WHERE latency_ranked.cumulative_count >= ceil(totals.request_count * 0.95))::int AS p95_latency_ms
FROM totals
LEFT JOIN latency_ranked USING (decision_type, decision_value)
GROUP BY totals.decision_type, totals.decision_value, totals.request_count, totals.used_count, totals.error_count
-- Keep a sentinel row so the API can report when a busy flag has more than
-- the bounded response can display. Most flags have at most 16 live variants.
ORDER BY totals.request_count DESC, totals.decision_type, totals.decision_value
LIMIT 101;

-- name: EnqueueInvocationRow :one
WITH replay_parent AS (
  SELECT i.id, i.replay_root_invocation_id, coalesce(i.replay_root_created_at, i.created_at) AS root_created_at
  FROM invocations i JOIN apps a ON a.id=i.app_id AND a.account_id=i.account_id
  WHERE i.id=sqlc.narg(replayed_from_invocation_id)::uuid
    AND i.account_id=sqlc.arg(account_id)::uuid AND i.app_id=sqlc.arg(app_id)::uuid
    AND i.deployment_scope=coalesce(nullif(sqlc.arg(deployment_scope)::text, ''),
      CASE WHEN a.project_id IS NOT NULL AND coalesce(a.preview_of_slug, '')='' THEN 'production' ELSE 'default' END)
    AND i.platform_tenant_id IS NOT DISTINCT FROM sqlc.narg(platform_tenant_id)::uuid
    AND i.state IN ('failed', 'dead_letter') AND sqlc.arg(source)::text='replay'
    AND (i.work_policy_name IS NULL OR (
      i.work_policy_name=nullif(sqlc.arg(work_policy_name)::text, '')
      AND i.work_key_digest=sqlc.narg(work_key_digest)::bytea
      AND i.work_policy_revision IS NOT DISTINCT FROM sqlc.narg(work_policy_revision)::bigint
      AND i.work_fairness_digest IS NOT DISTINCT FROM sqlc.narg(work_fairness_digest)::bytea
      AND i.work_fairness_limit IS NOT DISTINCT FROM sqlc.narg(work_fairness_limit)::int
      AND i.work_expires_at IS NOT DISTINCT FROM sqlc.narg(work_expires_at)::timestamptz
    ))
  FOR SHARE OF i, a
)
INSERT INTO invocations (
  id, app_id, account_id, source, queue_name, state, method, path,
  payload, headers, due_at, scheduled_at, cron_id, ack_url, lease_expires_at,
  deadline_at, retry_policy, result_retention_until,
  on_success_destination_id, on_failure_destination_id,
  work_policy_name, work_key_digest, work_expires_at,
  work_sequence, work_policy_revision, work_fairness_digest,
  work_fairness_limit, platform_tenant_id, deployment_scope, queue_binding_id,
  occurrence_id, start_deadline_at, failure_rules, environment_id, replayed_from_invocation_id, replay_root_invocation_id, replay_root_created_at
) SELECT
  coalesce(sqlc.narg(id)::uuid, gen_random_uuid()), sqlc.arg(app_id), sqlc.arg(account_id),
  sqlc.arg(source), sqlc.arg(queue_name), coalesce(nullif(sqlc.arg(state)::text, ''), 'pending'),
  sqlc.arg(method), sqlc.arg(path), sqlc.arg(payload), sqlc.arg(headers), sqlc.arg(due_at),
  sqlc.narg(scheduled_at), sqlc.narg(cron_id), nullif(sqlc.arg(ack_url)::text, ''),
  sqlc.narg(lease_expires_at), sqlc.narg(deadline_at), sqlc.narg(retry_policy),
  sqlc.narg(result_retention_until), sqlc.narg(on_success_destination_id),
  sqlc.narg(on_failure_destination_id), nullif(sqlc.arg(work_policy_name)::text, ''),
  sqlc.narg(work_key_digest), sqlc.narg(work_expires_at), sqlc.narg(work_sequence),
  sqlc.narg(work_policy_revision), sqlc.narg(work_fairness_digest), sqlc.narg(work_fairness_limit),
  sqlc.narg(platform_tenant_id), nullif(sqlc.arg(deployment_scope)::text, ''), sqlc.narg(queue_binding_id),
  sqlc.narg(occurrence_id)::uuid, sqlc.narg(start_deadline_at)::timestamptz, sqlc.narg(failure_rules)::jsonb,
  sqlc.narg(environment_id)::uuid, replay_parent.id, coalesce(replay_parent.replay_root_invocation_id, replay_parent.id), replay_parent.root_created_at
FROM (SELECT 1) seed LEFT JOIN replay_parent ON true
WHERE sqlc.narg(replayed_from_invocation_id)::uuid IS NULL OR replay_parent.id IS NOT NULL
RETURNING *;

-- Queue binding/consumer publication (ADR-393). Parent locks also serialize
-- trigger admission, so quota checks and the projection share the same commit.
-- name: QueueConsumerLockApp :one
select id, account_id, type, workload_class from apps
where id=sqlc.arg(app_id) and account_id=sqlc.arg(account_id) and status<>'deleted' for update;

-- name: QueueConsumerLockAccount :one
select id, plan from accounts where id=$1 for update;

-- name: QueueConsumerBindingForUpdate :one
select * from queue_bindings where id=sqlc.arg(id)
and app_id=sqlc.arg(app_id) and account_id=sqlc.arg(account_id) and retired_at is null for update;

-- name: QueueBindingHistoryByID :one
select * from queue_bindings where id=sqlc.arg(id)
and app_id=sqlc.arg(app_id) and account_id=sqlc.arg(account_id);

-- name: ListQueueBindingHistoryForApp :many
select * from queue_bindings where app_id=sqlc.arg(app_id) and account_id=sqlc.arg(account_id)
order by created_at, id;

-- The approved-intent transaction holds source/app/account before this row.
-- name: EnvironmentGitOpsQueueForUpdate :one
select * from queue_bindings where id=sqlc.arg(id) and app_id=sqlc.arg(app_id)
and account_id=sqlc.arg(account_id) and environment_id=sqlc.arg(environment_id) for update;

-- Only the retirement guard's current approved lease may release this hold.
-- name: RecoverEnvironmentGitOpsQueue :one
update queue_bindings set retired_at=null,updated_at=now()
where id=sqlc.arg(id) and app_id=sqlc.arg(app_id) and account_id=sqlc.arg(account_id)
and environment_id=sqlc.arg(environment_id) and retired_at is not null returning *;

-- name: QueueConsumerInsertBinding :one
insert into queue_bindings (id,account_id,app_id,name,queue_name,mode,workload_class,enabled,max_concurrency,retry_policy,deployment_scope,environment_id)
values (sqlc.arg(id),sqlc.arg(account_id),sqlc.arg(app_id),sqlc.arg(name),sqlc.arg(queue_name),
sqlc.arg(mode),sqlc.arg(workload_class),sqlc.arg(enabled),sqlc.arg(max_concurrency),sqlc.arg(retry_policy)::jsonb,sqlc.arg(deployment_scope),sqlc.narg(environment_id)::uuid) returning *;

-- name: QueueConsumerUpdateBinding :one
update queue_bindings set queue_name=sqlc.arg(queue_name),mode=sqlc.arg(mode),workload_class=sqlc.arg(workload_class),
enabled=sqlc.arg(enabled),max_concurrency=sqlc.arg(max_concurrency),retry_policy=sqlc.arg(retry_policy)::jsonb,updated_at=now()
where id=sqlc.arg(id) and app_id=sqlc.arg(app_id) and account_id=sqlc.arg(account_id) and retired_at is null returning *;

-- name: QueueConsumerRetireBinding :one
update queue_bindings set retired_at=now(), enabled=false, updated_at=now()
where id=sqlc.arg(id) and app_id=sqlc.arg(app_id) and account_id=sqlc.arg(account_id) and retired_at is null returning *;

-- name: QueueConsumerOwnedTriggers :many
select id, queue_binding_id from triggers where app_id=sqlc.arg(app_id) and kind='queue' and source='queue'
and (queue_binding_id=sqlc.arg(binding_id)::uuid
or (queue_binding_id is null and config->>'queue_binding_id'=sqlc.arg(binding_id)::uuid::text)) for update;

-- name: QueueConsumerUpdateTrigger :execrows
update triggers set slug=sqlc.arg(slug), enabled=sqlc.arg(enabled),config=sqlc.arg(config)::jsonb,
batch_size_max=sqlc.arg(batch_size_max),batch_window_ms=sqlc.arg(batch_window_ms),max_attempts=sqlc.arg(max_attempts),
payload_max_bytes=sqlc.arg(payload_max_bytes),updated_at=now()
where id=sqlc.arg(id) and app_id=sqlc.arg(app_id) and queue_binding_id=sqlc.arg(binding_id)::uuid
and kind='queue' and source='queue';

-- name: QueueConsumerCreateTrigger :one
insert into triggers (account_id,app_id,queue_binding_id,kind,source,slug,enabled,config,
batch_size_max,batch_window_ms,max_attempts,payload_max_bytes,broker_poison_strategy)
values (sqlc.arg(account_id),sqlc.arg(app_id),sqlc.arg(binding_id)::uuid,'queue','queue',sqlc.arg(slug),
sqlc.arg(enabled),sqlc.arg(config)::jsonb,sqlc.arg(batch_size_max),sqlc.arg(batch_window_ms),
sqlc.arg(max_attempts),sqlc.arg(payload_max_bytes),'commit') returning *;

-- name: QueueConsumerDisableTrigger :execrows
update triggers set enabled=false, updated_at=now() where id=sqlc.arg(id) and app_id=sqlc.arg(app_id)
and queue_binding_id=sqlc.arg(binding_id)::uuid;

-- name: PublicPatchTrigger :one
update triggers set enabled=coalesce(sqlc.narg(enabled)::boolean,enabled),
config=coalesce(sqlc.narg(config)::jsonb,config),
batch_size_max=coalesce(sqlc.narg(batch_size_max)::integer,batch_size_max),
batch_window_ms=coalesce(sqlc.narg(batch_window_ms)::integer,batch_window_ms),
max_attempts=coalesce(sqlc.narg(max_attempts)::integer,max_attempts),
payload_max_bytes=coalesce(sqlc.narg(payload_max_bytes)::integer,payload_max_bytes),
broker_poison_strategy=coalesce(sqlc.narg(broker_poison_strategy)::text,broker_poison_strategy),
filter_criteria=coalesce(sqlc.narg(filter_criteria)::jsonb,filter_criteria),
source=coalesce(sqlc.narg(source)::text,source)
where id=sqlc.arg(id) and queue_binding_id is null returning *;

-- name: QueueConsumerNotify :exec
select pg_notify('trigger_changed',sqlc.arg(payload)::text);

-- name: QueueClaimConsumerIdentity :one
select queue_binding_id, queue_binding_scope, (config ? 'queue_binding_id')::boolean as has_marker from triggers
where id=sqlc.arg(id) and app_id=sqlc.arg(app_id) and kind='queue' and source='queue';

-- name: QueueClaimLockBinding :one
select b.max_concurrency from queue_bindings b where b.id=sqlc.arg(id) and b.app_id=sqlc.arg(app_id)
and b.deployment_scope=sqlc.arg(binding_scope) and b.queue_name=sqlc.arg(queue_name) and b.mode='push' and b.enabled and b.retired_at is null
and (b.deployment_scope='' or exists (select 1 from apps a join project_environments e on e.project_id=a.project_id and e.account_id=a.account_id
  where a.id=b.app_id and e.id=b.environment_id and e.slug=b.deployment_scope)) for update;

-- name: QueueClaimLegacyBindingCap :one
select b.id, b.max_concurrency from queue_bindings b where b.app_id=sqlc.arg(app_id)
and b.deployment_scope='' and b.queue_name=sqlc.arg(queue_name) and b.mode='push' and b.enabled and b.retired_at is null
and not exists (select 1 from triggers owned where owned.queue_binding_id=b.id) for update;

-- name: QueueClaimLockLiveConsumer :one
select id from triggers where id=sqlc.arg(id) and app_id=sqlc.arg(app_id)
and kind='queue' and source='queue' and slug=sqlc.arg(queue_name) and enabled
and queue_binding_id is not distinct from sqlc.narg(binding_id)::uuid for share;

-- name: QueueStateInScope :one
-- One snapshot includes active work and dead letters. A NULL queue selects
-- every name in this scope; an empty string selects only legacy unnamed work.
select
  count(*) filter (where state in ('pending','dispatching'))::bigint as depth,
  count(*) filter (where state='dispatching' and lease_expires_at > now())::bigint as in_flight,
  count(*) filter (where state='dead_letter')::bigint as dead_letter,
  min(created_at) filter (where state='pending')::timestamptz as oldest_pending_at
from invocations
where app_id=sqlc.arg(app_id) and deployment_scope=sqlc.arg(deployment_scope)
  and source='queue' and state in ('pending','dispatching','dead_letter')
  and (sqlc.narg(queue_name)::text is null or queue_name=sqlc.narg(queue_name)::text);

-- name: WorkerPoolHistory :one
select max(started_at)::timestamptz as last_admission_at,
       max(terminal_at)::timestamptz as last_termination_at
from instances where app_id=sqlc.arg(app_id) and deployment_id=sqlc.arg(deployment_id) and mode='worker';
-- name: SelectPendingFireNowRequestForNode :one
-- Hold placement stable while the caller changes the claimed request status.
SELECT r.id::text AS id, r.cron_id::text AS cron_id, r.account_id::text AS account_id, r.requested_at, r.status
FROM cron_fire_now_requests r
JOIN crons c ON c.id = r.cron_id
JOIN apps a ON a.id = c.app_id
WHERE r.status = 'pending'
  AND (sqlc.narg(node_id)::text IS NULL OR a.node_id IS NULL OR a.node_id::text = sqlc.narg(node_id))
ORDER BY r.requested_at ASC, r.id ASC
FOR UPDATE OF r, a SKIP LOCKED
LIMIT 1;

-- name: RequeueFireNowRequest :execrows
UPDATE cron_fire_now_requests
SET status = 'pending'
WHERE id = sqlc.arg(id)::uuid AND status = 'running';

-- name: WorkerAdmissionLockAccount :one
select acct.id, acct.plan from accounts acct
join apps a on a.account_id=acct.id
join deployments d on d.app_id=a.id and d.id=sqlc.arg(deployment_id)
where a.id=sqlc.arg(app_id) for update of acct;

-- name: WorkerAdmissionCount :one
select count(*)::bigint from instances i join apps a on a.id=i.app_id
where a.account_id=sqlc.arg(account_id) and i.mode='worker'
and i.state in ('waking','cold_booting','running','draining','snapshotting','migrating','warm');

-- name: QueueStateForBinding :one
-- Legacy unassigned work remains visible under its historical name; pinned
-- work never follows a replacement binding that reuses that name.
select
  count(*) filter (where i.state in ('pending','dispatching'))::bigint as depth,
  count(*) filter (where i.state='dispatching' and i.lease_expires_at > now())::bigint as in_flight,
  count(*) filter (where i.state='dead_letter')::bigint as dead_letter,
  min(i.created_at) filter (where i.state='pending')::timestamptz as oldest_pending_at
from invocations i join queue_bindings b on b.app_id=i.app_id and b.account_id=i.account_id
where b.id=sqlc.arg(binding_id)::uuid and b.app_id=sqlc.arg(app_id)::uuid
  and (sqlc.narg(deployment_scope)::text is null or i.deployment_scope=sqlc.narg(deployment_scope)::text)
  and i.source='queue' and i.state in ('pending','dispatching','dead_letter')
  and (b.deployment_scope='' or i.deployment_scope=b.deployment_scope)
  and (i.queue_binding_id=b.id or (b.deployment_scope='' and i.queue_binding_id is null and i.queue_name=b.queue_name));

-- name: QueueClaimActiveCount :one
select count(*)::bigint from invocations i
where i.app_id=sqlc.arg(app_id)::uuid and i.source='queue'
  and (sqlc.arg(binding_scope)::text='' or i.deployment_scope=sqlc.arg(binding_scope)::text)
  and (i.queue_binding_id=sqlc.narg(binding_id)::uuid
    or (sqlc.arg(binding_scope)::text='' and i.queue_binding_id is null and (i.queue_name=sqlc.arg(queue_name) or (i.queue_name=''
      and i.work_policy_name is null and not exists (select 1 from triggers other
      where other.app_id=sqlc.arg(app_id)::uuid and other.kind='queue' and other.queue_binding_scope='' and other.enabled
        and other.source='queue' and other.id<>sqlc.arg(trigger_id)::uuid)))))
  and i.state='dispatching' and i.lease_expires_at > clock_timestamp()
 AND (exists (select 1 from queue_bindings accepted where accepted.id=i.queue_binding_id
 and accepted.app_id=i.app_id and accepted.account_id=i.account_id and accepted.deployment_scope<>''
 and accepted.deployment_scope=i.deployment_scope) OR (not exists (select 1 from deployments stage
              where stage.app_id=i.app_id and stage.scope not in ('production','default')
                and exists (select 1 from jsonb_each_text(case when jsonb_typeof(i.headers)='object' then i.headers else '{}'::jsonb end) pin
                    where lower(pin.key)='x-gregale-revision' and translate(regexp_replace(lower(pin.value), '[[:space:]]|(^urn:uuid:)|[{}]', '', 'g'), '-', '')=replace(stage.id::text,'-','')))
          and not exists (select 1 from project_release_sets stage join apps owner
              on owner.project_id=stage.project_id and owner.account_id=stage.account_id
              where owner.id=i.app_id and stage.environment_slug not in ('production','default')
                and exists (select 1 from jsonb_each_text(case when jsonb_typeof(i.headers)='object' then i.headers else '{}'::jsonb end) pin
                    where lower(pin.key)='x-gregale-release' and translate(regexp_replace(lower(pin.value), '[[:space:]]|(^urn:uuid:)|[{}]', '', 'g'), '-', '')=replace(stage.id::text,'-','')))))
 AND i.environment_id IS NULL AND NOT EXISTS (SELECT 1 FROM invocation_environment_queue_receipts r WHERE r.invocation_id=i.id);

-- name: QueueClaimPendingInvocation :one
update invocations i set state='dispatching',
  lease_expires_at=clock_timestamp() + make_interval(secs=>sqlc.arg(lease_seconds)::integer),
  received_at=coalesce(i.received_at,clock_timestamp()), attempts=i.attempts+1
where i.id=sqlc.arg(id)::uuid and i.app_id=sqlc.arg(app_id)::uuid and i.source='queue'
  and i.state='pending' and i.due_at<=clock_timestamp()
  and (sqlc.arg(binding_scope)::text='' or i.deployment_scope=sqlc.arg(binding_scope)::text)
  and (i.queue_binding_id=sqlc.narg(binding_id)::uuid or (sqlc.arg(binding_scope)::text='' and i.queue_binding_id is null
    and (i.queue_name=sqlc.arg(queue_name) or (i.queue_name='' and i.work_policy_name is null
      and not exists (select 1 from triggers other where other.app_id=i.app_id
        and other.kind='queue' and other.queue_binding_scope='' and other.enabled and other.source='queue'
        and other.id<>sqlc.arg(trigger_id)::uuid)))))
  and not exists (select 1 from trigger_records tr where tr.trigger_id=sqlc.arg(trigger_id)::uuid
    and tr.item_identifier=i.id::text
    and not ((tr.state in ('pending','retry') and tr.next_fire_at<=clock_timestamp())
      or (tr.state='claimed' and tr.claim_expires_at<=clock_timestamp())))
 AND (exists (select 1 from queue_bindings accepted where accepted.id=i.queue_binding_id
 and accepted.app_id=i.app_id and accepted.account_id=i.account_id and accepted.deployment_scope<>''
 and accepted.deployment_scope=i.deployment_scope) OR (not exists (select 1 from deployments stage
              where stage.app_id=i.app_id and stage.scope not in ('production','default')
                and exists (select 1 from jsonb_each_text(case when jsonb_typeof(i.headers)='object' then i.headers else '{}'::jsonb end) pin
                    where lower(pin.key)='x-gregale-revision' and translate(regexp_replace(lower(pin.value), '[[:space:]]|(^urn:uuid:)|[{}]', '', 'g'), '-', '')=replace(stage.id::text,'-','')))
          and not exists (select 1 from project_release_sets stage join apps owner
              on owner.project_id=stage.project_id and owner.account_id=stage.account_id
              where owner.id=i.app_id and stage.environment_slug not in ('production','default')
                and exists (select 1 from jsonb_each_text(case when jsonb_typeof(i.headers)='object' then i.headers else '{}'::jsonb end) pin
                    where lower(pin.key)='x-gregale-release' and translate(regexp_replace(lower(pin.value), '[[:space:]]|(^urn:uuid:)|[{}]', '', 'g'), '-', '')=replace(stage.id::text,'-','')))))
 AND i.environment_id IS NULL AND NOT EXISTS (SELECT 1 FROM invocation_environment_queue_receipts r WHERE r.invocation_id=i.id)
returning i.*;

-- name: QueueReleasePendingBatchClaims :exec
with targets as (
  select unnest(sqlc.arg(ids)::text[]) as id, unnest(sqlc.arg(attempts)::integer[]) as attempt,
    unnest(sqlc.arg(replay_generations)::bigint[]) as replay_generation
) update invocations i set state='pending',lease_expires_at=null from targets
where i.id::text=targets.id and i.environment_id is null and i.attempts=targets.attempt and i.replay_generation=targets.replay_generation
  and i.app_id=sqlc.arg(app_id)::uuid and i.source='queue' and i.state='dispatching'
  and (sqlc.narg(binding_id)::uuid is null or i.queue_binding_id=sqlc.narg(binding_id)::uuid
    or (i.queue_binding_id is null and i.queue_name in (sqlc.arg(queue_name),'')));

-- name: QueuePollCandidates :many
with consumer as (
 select coalesce(t.queue_binding_id, (select b.id from queue_bindings b
   where b.app_id=t.app_id and b.deployment_scope='' and b.queue_name=t.slug
     and not exists (select 1 from triggers owned where owned.queue_binding_id=b.id))) as binding_id, t.queue_binding_scope as binding_scope
 from triggers t where t.id=sqlc.arg(trigger_id)::uuid and t.app_id=sqlc.arg(app_id)::uuid
 and (t.queue_binding_scope='' or exists (select 1 from apps a join project_environments e on e.project_id=a.project_id and e.account_id=a.account_id
   where a.id=t.app_id and e.id=t.queue_binding_environment_id and e.slug=t.queue_binding_scope))
)
select i.id::text from invocations i cross join consumer
		left join trigger_records tr on tr.trigger_id = sqlc.arg(trigger_id)::uuid
		  and tr.item_identifier = i.id::text
		where i.app_id = sqlc.arg(app_id)::uuid and i.source = 'queue' and i.state = 'pending' and i.environment_id is null
		  and i.due_at <= clock_timestamp()
		  and (tr.id is null
		    or (tr.state in ('pending','retry') and tr.next_fire_at <= clock_timestamp())
		    or (tr.state = 'claimed' and tr.claim_expires_at <= clock_timestamp()))
		  and (i.work_policy_name is null or not exists (
		      select 1 from invocations older
		      where older.app_id = i.app_id
		        and older.work_policy_name = i.work_policy_name
		        and older.work_key_digest = i.work_key_digest
		        and ((older.work_sequence < i.work_sequence and older.state='pending')
		          or older.state='dispatching')))
		  and (i.work_policy_name is null or not exists (
		      select 1 from trigger_records older
		      join triggers source on source.id=older.trigger_id
		      where source.app_id=i.app_id
		        and older.work_policy_name=i.work_policy_name
		        and older.work_key_digest=i.work_key_digest
		        and ((older.work_sequence<i.work_sequence and older.state in ('pending','retry'))
		          or older.state='claimed')))
		  and (i.work_fairness_limit is null or (
		      select count(*) from invocations active
		      where active.app_id = i.app_id
		        and active.work_policy_name = i.work_policy_name
		        and active.work_fairness_digest = i.work_fairness_digest
		        and active.state = 'dispatching'
		        and active.lease_expires_at > clock_timestamp()
		  ) + (
		      select count(*) from trigger_records active
		      join triggers source on source.id=active.trigger_id
		      where source.app_id=i.app_id
		        and active.work_policy_name=i.work_policy_name
		        and active.work_fairness_digest=i.work_fairness_digest
		        and active.state='claimed'
		        and active.claim_expires_at > clock_timestamp()
		  ) < i.work_fairness_limit)
		  and (consumer.binding_scope='' or i.deployment_scope=consumer.binding_scope)
		  and (i.queue_binding_id=consumer.binding_id or (consumer.binding_scope='' and i.queue_binding_id is null
    and (i.queue_name=sqlc.arg(queue_name)::text or (i.queue_name='' and i.work_policy_name is null
      and not exists (select 1 from triggers other where other.app_id=sqlc.arg(app_id)::uuid
        and other.kind='queue' and other.queue_binding_scope='' and other.enabled and other.source='queue' and other.id<>sqlc.arg(trigger_id)::uuid)))))

 AND (exists (select 1 from queue_bindings accepted where accepted.id=i.queue_binding_id
 and accepted.app_id=i.app_id and accepted.account_id=i.account_id and accepted.deployment_scope<>''
 and accepted.deployment_scope=i.deployment_scope) OR (not exists (select 1 from deployments stage
              where stage.app_id=i.app_id and stage.scope not in ('production','default')
                and exists (select 1 from jsonb_each_text(case when jsonb_typeof(i.headers)='object' then i.headers else '{}'::jsonb end) pin
                    where lower(pin.key)='x-gregale-revision' and translate(regexp_replace(lower(pin.value), '[[:space:]]|(^urn:uuid:)|[{}]', '', 'g'), '-', '')=replace(stage.id::text,'-','')))
          and not exists (select 1 from project_release_sets stage join apps owner
              on owner.project_id=stage.project_id and owner.account_id=stage.account_id
              where owner.id=i.app_id and stage.environment_slug not in ('production','default')
                and exists (select 1 from jsonb_each_text(case when jsonb_typeof(i.headers)='object' then i.headers else '{}'::jsonb end) pin
                    where lower(pin.key)='x-gregale-release' and translate(regexp_replace(lower(pin.value), '[[:space:]]|(^urn:uuid:)|[{}]', '', 'g'), '-', '')=replace(stage.id::text,'-','')))))		order by i.created_at, i.id limit sqlc.arg(candidate_limit)::integer;


-- name: RetryQueueDeadLetterInvocation :one
update invocations set state='pending', attempts=0, last_error=null, outcome=null,
  due_at=clock_timestamp(), lease_expires_at=null, instance_id=null,
  last_replayed_at=clock_timestamp(), completed_at=null, result=null
where id=sqlc.arg(id)::uuid and account_id=sqlc.arg(account_id)::uuid and state='dead_letter'
  and operation_id IS NULL
  and NOT EXISTS(SELECT 1 FROM customer_operation_executions e WHERE e.invocation_id=invocations.id)
returning *;

-- name: QueueInvocationForTriggerReceipt :one
select i.* from trigger_records r join triggers t on t.id=r.trigger_id
join invocations i on i.id::text=r.item_identifier and i.app_id=t.app_id and i.account_id=t.account_id
  and i.source=t.source
where r.id=sqlc.arg(record_id)::uuid and t.kind='queue' and t.source in ('queue','delayed_task')
  and (i.queue_binding_id is null or i.queue_binding_id=t.queue_binding_id)
  and r.state not in ('superseded','cancelled','expired');

-- name: RetryExternalTriggerRecordByOperator :execrows
update trigger_records r set state='pending', attempts=0, last_error=null,
  next_fire_at=clock_timestamp(), claim_generation=claim_generation+1, claim_expires_at=null
from triggers t where r.id=sqlc.arg(id)::uuid and t.id=r.trigger_id
  and not (t.kind='queue' and coalesce(t.source in ('queue','delayed_task'),false))
  and r.state not in ('superseded','cancelled','expired')
  and (sqlc.narg(expected_account_id)::uuid is null or t.account_id=sqlc.narg(expected_account_id)::uuid)
  and (sqlc.narg(expected_app_id)::uuid is null or t.app_id=sqlc.narg(expected_app_id)::uuid);

-- name: QueueFinishDeliveryClaims :many
with targets as (
  select unnest(sqlc.arg(ids)::text[]) as id, unnest(sqlc.arg(attempts)::integer[]) as attempt,
    unnest(sqlc.arg(replay_generations)::bigint[]) as replay_generation
), finalized as (
  update invocations i set state=sqlc.arg(invocation_state), outcome=sqlc.arg(outcome),
    result=sqlc.arg(result)::jsonb, completed_at=clock_timestamp(), lease_expires_at=null,
    last_error=sqlc.arg(last_error)
  from targets where i.id::text=targets.id and i.environment_id is null and i.attempts=targets.attempt
    and i.replay_generation=targets.replay_generation and i.app_id=sqlc.arg(app_id)::uuid
    and i.source=sqlc.arg(source) and i.state='dispatching'
    and i.lease_expires_at>clock_timestamp()
  returning i.id::text as id
), receipts as (update trigger_records r set state=sqlc.arg(record_state),
  attempts=r.attempts+case when sqlc.arg(record_state)::text='dead_letter' and r.state<>'dead_letter' then 1 else 0 end,
  last_error=case when sqlc.arg(record_state)::text='dead_letter' then nullif(sqlc.arg(last_error)::text,'') else r.last_error end,
  last_dispatched_at=clock_timestamp(), claim_expires_at=null
from finalized where r.trigger_id=sqlc.arg(trigger_id)::uuid and r.item_identifier=finalized.id
  and r.state<>sqlc.arg(record_state)::text returning r.id)
select id from finalized;

-- name: QueueRetryDeliveryClaims :exec
with targets as (
  select unnest(sqlc.arg(ids)::text[]) as id, unnest(sqlc.arg(attempts)::integer[]) as attempt,
    unnest(sqlc.arg(replay_generations)::bigint[]) as replay_generation
) update invocations i set state='pending', outcome=null, completed_at=null,
  due_at=coalesce((select r.next_fire_at from trigger_records r
    where r.trigger_id=sqlc.arg(trigger_id)::uuid and r.item_identifier=i.id::text),clock_timestamp()+interval '1 second'),
  lease_expires_at=null,last_error=sqlc.arg(reason)
from targets where i.id::text=targets.id and i.environment_id is null and i.attempts=targets.attempt
  and i.replay_generation=targets.replay_generation and i.app_id=sqlc.arg(app_id)::uuid
  and i.source=sqlc.arg(source) and i.state='dispatching';

-- name: QueuePollLegacyClaims :many
with claimed as (
			select i.id
			  from invocations i
			  left join trigger_records tr
			    on tr.trigger_id = sqlc.arg(trigger_id)::uuid
			   and tr.item_identifier = i.id::text
			 where i.app_id = sqlc.arg(app_id)::uuid and i.environment_id is null
			   and i.source = sqlc.arg(source)::text
			   and i.queue_binding_id is null
               and exists (select 1 from triggers live where live.id=sqlc.arg(trigger_id)::uuid and live.queue_binding_scope='')
			   and (i.queue_name = sqlc.arg(queue_name)::text or (
				       i.queue_name = ''
				   and not exists (
				       select 1 from triggers other
				        where other.app_id = sqlc.arg(app_id)::uuid
				          and other.kind = 'queue' and other.queue_binding_scope=''
				          and other.enabled
				          and other.source = sqlc.arg(source)::text
				          and other.id <> sqlc.arg(trigger_id)::uuid
				   )
			       ))
			   and i.state = 'pending'
			   and i.work_policy_name is null
			   and i.due_at <= now()
			   and (tr.id is null
			        or (tr.state in ('pending','retry') and tr.next_fire_at <= now())
			        or (tr.state = 'claimed' and tr.claim_expires_at <= now()))

 AND NOT EXISTS (SELECT 1 FROM invocation_environment_queue_receipts r WHERE r.invocation_id=i.id)
 and not exists (select 1 from deployments stage
              where stage.app_id=i.app_id and stage.scope not in ('production','default')
                and exists (select 1 from jsonb_each_text(case when jsonb_typeof(i.headers)='object' then i.headers else '{}'::jsonb end) pin
                    where lower(pin.key)='x-gregale-revision' and translate(regexp_replace(lower(pin.value), '[[:space:]]|(^urn:uuid:)|[{}]', '', 'g'), '-', '')=replace(stage.id::text,'-','')))
          and not exists (select 1 from project_release_sets stage join apps owner
              on owner.project_id=stage.project_id and owner.account_id=stage.account_id
              where owner.id=i.app_id and stage.environment_slug not in ('production','default')
                and exists (select 1 from jsonb_each_text(case when jsonb_typeof(i.headers)='object' then i.headers else '{}'::jsonb end) pin
                    where lower(pin.key)='x-gregale-release' and translate(regexp_replace(lower(pin.value), '[[:space:]]|(^urn:uuid:)|[{}]', '', 'g'), '-', '')=replace(stage.id::text,'-','')))			 order by i.created_at asc
			 limit sqlc.arg(poll_limit)::integer
			 for update of i skip locked
		), updated as (
			update invocations i
			   set state = 'dispatching',
			       lease_expires_at = now() + interval '10 minutes',
			       received_at = coalesce(i.received_at, now()),
			       attempts = i.attempts + 1
			  from claimed c
			 where i.id = c.id
			returning i.id::text as id, i.payload::text as payload, i.headers::text as headers,
			           '{}'::text as metadata, i.created_at, i.attempts, i.replay_generation
		)
		select id, payload, headers, metadata, created_at, attempts, replay_generation
		  from updated
		 order by created_at asc, id asc;

-- name: ReplayDeadLetterInvocation :execrows
update invocations set state='pending', attempts=0, last_error=null, outcome=null,
  due_at=clock_timestamp(),lease_expires_at=null,instance_id=null,
  last_replayed_at=clock_timestamp(),completed_at=null,result=null
where id=sqlc.arg(id)::uuid and account_id=sqlc.arg(account_id)::uuid
  and app_id=sqlc.arg(app_id)::uuid and state='dead_letter'
  and operation_id IS NULL
  and NOT EXISTS(SELECT 1 FROM customer_operation_executions e WHERE e.invocation_id=invocations.id);

-- name: DeleteExternalTriggerDeadLetterAudit :exec
delete from trigger_dead_letter d using trigger_records r, triggers t
where d.record_id=sqlc.arg(record_id)::uuid and r.id=d.record_id and t.id=r.trigger_id
  and not (t.kind='queue' and coalesce(t.source in ('queue','delayed_task'),false));

-- name: ObjectMultipartCapacityLock :one
SELECT * FROM object_storage_multipart_uploads
WHERE id=$1 AND account_id=$2 AND bucket_id=$3 FOR UPDATE;

-- name: ObjectMultipartPartGrant :one
SELECT max_bytes FROM object_storage_multipart_part_grants WHERE upload_id=$1 AND part_number=$2;

-- name: ObjectMultipartPartTotal :one
SELECT coalesce(sum(max_bytes),0)::bigint FROM object_storage_multipart_part_grants WHERE upload_id=$1;

-- name: ObjectMultipartPartGrantUpsert :exec
INSERT INTO object_storage_multipart_part_grants (upload_id,part_number,max_bytes) VALUES ($1,$2,$3)
ON CONFLICT (upload_id,part_number) DO UPDATE
SET max_bytes=greatest(object_storage_multipart_part_grants.max_bytes,EXCLUDED.max_bytes), cleanup_tracked=false;

-- name: ObjectS3MultipartList :many
SELECT * FROM object_storage_multipart_uploads
WHERE account_id=sqlc.arg(account_id) AND app_id=sqlc.arg(app_id) AND bucket_id=sqlc.arg(bucket_id)
AND part_count=0 AND state IN ('active','completing','completing_conditional','aborting')
AND starts_with(object_key,sqlc.arg(prefix)::text)
AND (sqlc.arg(key_marker)::text='' OR object_key COLLATE "C">sqlc.arg(key_marker)::text
 OR (object_key=sqlc.arg(key_marker)::text AND sqlc.arg(upload_marker)::text<>'' AND id::text>sqlc.arg(upload_marker)::text))
ORDER BY object_key COLLATE "C",id LIMIT sqlc.arg(page_limit)::int;


-- name: ObjectMultipartPartTransfer :one
SELECT transfer_token,unsafe_until FROM object_storage_multipart_part_grants WHERE upload_id=$1 AND part_number=$2;

-- name: ObjectMultipartPartBegin :exec
INSERT INTO object_storage_multipart_part_grants (upload_id,part_number,max_bytes,cleanup_tracked,transfer_token,unsafe_until)
VALUES ($1,$2,$3,true,$4,clock_timestamp()+($5::int * interval '1 second'))
ON CONFLICT (upload_id,part_number) DO UPDATE
SET max_bytes=greatest(object_storage_multipart_part_grants.max_bytes,EXCLUDED.max_bytes),
transfer_token=EXCLUDED.transfer_token,unsafe_until=EXCLUDED.unsafe_until,source_bucket_id=NULL,source_copy_grant_id=NULL,source_subject_id='',source_key='';

-- name: ObjectMultipartPartRevision :exec
UPDATE object_storage_multipart_uploads SET part_revision=part_revision+1,updated_at=clock_timestamp() WHERE id=$1;

-- name: ObjectMultipartPartSettle :execrows
UPDATE object_storage_multipart_part_grants SET transfer_token=NULL,unsafe_until=NULL
WHERE upload_id=$1 AND part_number=$2 AND transfer_token=$3
AND EXISTS (SELECT 1 FROM object_storage_multipart_uploads u WHERE u.id=$1 AND u.account_id=$4);

-- name: ObjectMultipartTransfersPending :one
SELECT EXISTS (SELECT 1 FROM object_storage_multipart_part_grants
WHERE upload_id=$1 AND transfer_token IS NOT NULL AND unsafe_until>clock_timestamp()) AS pending;

-- name: ObjectMultipartAbortOwner :one
SELECT account_id,bucket_id,(part_url_unsafe_until IS NULL OR part_url_unsafe_until<=clock_timestamp())::boolean AS part_urls_drained
FROM object_storage_multipart_uploads WHERE id=$1 AND lease_token=$2 AND state='aborting';

-- name: ObjectMultipartReleaseTrackedParts :exec
DELETE FROM object_storage_multipart_part_grants WHERE upload_id=$1 AND cleanup_tracked;


-- name: ObjectMultipartClearTransfers :exec
UPDATE object_storage_multipart_part_grants SET transfer_token=NULL,unsafe_until=NULL WHERE upload_id=$1;

-- name: ObjectMultipartRejectCompletion :execrows
UPDATE object_storage_multipart_uploads SET state='aborting',completion_error_code=$3,
lease_token=NULL,lease_until=NULL,attempt_count=0,last_error_code=$3,retry_at=now(),updated_at=now()
WHERE id=$1 AND lease_token=$2 AND state='completing_conditional';

-- name: ObjectCapacityFenced :one
SELECT (EXISTS(SELECT 1 FROM object_version_protection p WHERE p.bucket_id=$1 AND p.state IN ('waiting','applying')) OR EXISTS(SELECT 1 FROM object_bucket_object_lock l WHERE l.bucket_id=$1 AND l.state<>'ready') OR EXISTS(SELECT 1 FROM object_deletions d WHERE d.bucket_id=$1 AND d.state IN ('prepared','dispatched')) OR EXISTS(SELECT 1 FROM object_bucket_versioning v WHERE v.bucket_id=$1 AND v.state<>'ready') OR EXISTS (SELECT 1 FROM object_storage_capacity_reconciliations c WHERE c.bucket_id=$1 AND c.state IN ('waiting','scanning')))::boolean AS fenced;

-- name: ObjectWriteKeyFenced :one
SELECT EXISTS(SELECT 1 FROM object_storage_write_admissions w
 WHERE w.bucket_id=$1 AND w.key_hash=$2 AND w.state='pending'
 AND (w.multipart_upload_id IS NULL OR EXISTS(SELECT 1 FROM object_storage_multipart_uploads m
  WHERE m.id=w.multipart_upload_id AND m.state IN ('initiating','active','completing','completing_conditional','aborting')))
 AND w.id IS DISTINCT FROM sqlc.narg(own_write)::uuid)::boolean AS fenced;

-- name: ObjectWriteInsert :exec
INSERT INTO object_storage_write_admissions(id,bucket_id,key_hash,kind,multipart_upload_id,route_receipt,native_version,native_bytes)
VALUES($1,$2,$3,$4,$5,$6,sqlc.arg(native_version)::boolean,sqlc.arg(native_bytes)::bigint);

-- name: ObjectWriteSettle :execrows
UPDATE object_storage_write_admissions w SET state='settled',settled_at=coalesce(settled_at,now())
WHERE w.id=$1 AND w.bucket_id=$2 AND w.kind='proxy' AND NOT w.route_receipt
AND EXISTS (SELECT 1 FROM object_buckets b WHERE b.id=w.bucket_id AND b.account_id=$3);

-- name: ObjectTrackedGrantUpsert :exec
INSERT INTO object_storage_key_grants(bucket_id,key_hash,max_bytes,reclaimable,last_write_id) VALUES($1,$2,$3,true,$4)
ON CONFLICT(bucket_id,key_hash) DO UPDATE SET max_bytes=greatest(object_storage_key_grants.max_bytes,EXCLUDED.max_bytes),last_write_id=EXCLUDED.last_write_id,reclaimable=object_storage_key_grants.reclaimable;

-- name: ObjectCapacityReadiness :one
SELECT
 ((SELECT count(*) FROM object_version_protection WHERE object_version_protection.bucket_id=$1 AND object_version_protection.state IN ('waiting','applying')) + (SELECT count(*) FROM object_deletions d WHERE d.bucket_id=$1 AND d.state IN ('prepared','dispatched')) + (SELECT count(*) FROM object_storage_write_admissions w LEFT JOIN object_storage_multipart_uploads m ON m.id=w.multipart_upload_id
  WHERE w.bucket_id=$1 AND ((w.kind='proxy' AND w.state='pending') OR (w.kind='multipart' AND m.state NOT IN ('completed','aborted')))))::bigint AS pending,
 EXISTS (SELECT 1 FROM object_storage_key_grants WHERE bucket_id=$1 AND NOT reclaimable) AS unsafe,
 EXISTS (SELECT 1 FROM object_storage_multipart_uploads m WHERE m.bucket_id=$1 AND
  (m.state NOT IN ('completed','aborted') OR (m.state<>'completed' AND EXISTS (SELECT 1 FROM object_storage_multipart_part_grants p WHERE p.upload_id=m.id)))) AS multipart,
 (EXISTS (SELECT 1 FROM object_upload_completions WHERE bucket_id=$1 AND recovery_versions_observed) OR EXISTS(SELECT 1 FROM object_version_references WHERE bucket_id=$1 AND versions_observed) OR EXISTS(SELECT 1 FROM object_storage_multipart_uploads WHERE bucket_id=$1 AND completion_versions_observed) OR EXISTS (SELECT 1 FROM object_storage_bucket_usage WHERE bucket_id=$1 AND inventory_scope='all_versions') OR EXISTS(SELECT 1 FROM object_bucket_versioning WHERE bucket_id=$1 AND versions_required)) AS versions;

-- name: ObjectCapacityActive :one
SELECT * FROM object_storage_capacity_reconciliations WHERE bucket_id=$1 AND state IN ('waiting','scanning');

-- name: ObjectCapacityInsert :one
INSERT INTO object_storage_capacity_reconciliations(id,bucket_id,deadline_at,before_bytes,before_keys,after_bytes,after_keys)
VALUES($1,$2,now()+make_interval(secs=>sqlc.arg(deadline_seconds)::int),$3,$4,$3,$4) RETURNING *;

-- name: ObjectCapacityGet :one
SELECT c.*,b.account_id,b.app_id FROM object_storage_capacity_reconciliations c JOIN object_buckets b ON b.id=c.bucket_id WHERE c.id=$1;

-- name: ObjectCapacityLock :one
SELECT * FROM object_storage_capacity_reconciliations WHERE id=$1 FOR UPDATE;

-- name: ObjectCapacityDue :many
SELECT * FROM object_storage_capacity_reconciliations WHERE state IN ('waiting','scanning') AND retry_at<=now() AND (lease_until IS NULL OR lease_until<=now()) ORDER BY retry_at,id LIMIT $1;

-- name: ObjectCapacitySave :exec
UPDATE object_storage_capacity_reconciliations SET state=$2,lease_token=$3,lease_until=$4,retry_at=$5,
 before_bytes=$6,before_keys=$7,after_bytes=$8,after_keys=$9,reclaimed_bytes=$10,reclaimed_keys=$11,
 pending_writes=$12,last_error_code=$13,updated_at=$14,finished_at=$15,inventory_scope=sqlc.arg(inventory_scope),inventory_cursor=sqlc.arg(inventory_cursor),inventory_verified=sqlc.arg(inventory_verified),scanned_pages=sqlc.arg(scanned_pages),scanned_bytes=sqlc.arg(scanned_bytes),scanned_versions=sqlc.arg(scanned_versions) WHERE id=$1;

-- name: ObjectCapacityRebase :execrows
INSERT INTO object_storage_bucket_usage(bucket_id,baseline_bytes,baseline_keys,observed_bytes,observed_keys,observed_at,attempt_at)
SELECT b.id,sqlc.arg(bytes)::bigint,sqlc.arg(keys)::bigint,sqlc.arg(bytes),sqlc.arg(keys),now(),now() FROM object_buckets b WHERE b.id=$1 AND b.state='ready'
ON CONFLICT(bucket_id) DO UPDATE SET baseline_bytes=EXCLUDED.baseline_bytes,baseline_keys=EXCLUDED.baseline_keys,granted_bytes=0,granted_keys=0,
 observed_bytes=EXCLUDED.observed_bytes,observed_keys=EXCLUDED.observed_keys,observed_at=now(),attempt_at=now(),token='',lease_until=NULL;

-- name: ObjectCapacityDeleteGrants :exec
DELETE FROM object_storage_key_grants WHERE bucket_id=$1;

-- name: ObjectCapacityDeleteWrites :exec
DELETE FROM object_storage_write_admissions WHERE bucket_id=$1;

-- name: ObjectCapacityLockBucket :one
SELECT * FROM object_buckets WHERE id=$1 AND account_id=$2 AND app_id=$3 AND state='ready' FOR NO KEY UPDATE;


-- name: ObjectUploadRouteForWrite :one
SELECT * FROM object_upload_routes WHERE id=$1 AND account_id=$2 AND app_id=$3 AND bucket_id=$4 AND enabled FOR SHARE;

-- name: ObjectTrackedUploadInsert :one
INSERT INTO object_upload_completions
 (id,route_id,account_id,app_id,bucket_id,subject_id,object_key,bytes,content_type,request_id,idempotency_key,request_fingerprint,status,write_phase,encryption_snapshot,protection_snapshot,recovery_retry_at,encryption_default_revision)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,'pending','prepared',sqlc.arg(encryption_snapshot)::jsonb,sqlc.arg(protection_snapshot)::jsonb,now()+make_interval(secs=>sqlc.arg(retry_seconds)::int),sqlc.arg(encryption_default_revision)::bigint) RETURNING *;

-- name: ObjectTrackedUploadGet :one
SELECT * FROM object_upload_completions WHERE id=$1 AND account_id=$2 AND bucket_id=$3 FOR UPDATE;

-- name: ObjectTrackedUploadReplay :one
SELECT * FROM object_upload_completions WHERE route_id=$1 AND subject_id=$2 AND idempotency_key=$3 AND account_id=$4 AND app_id=$5;

-- name: ObjectTrackedUploadDispatch :one
UPDATE object_upload_completions SET write_phase='dispatched', encryption_dispatched=(encryption_snapshot<>'{}'), protection_dispatched=(protection_snapshot<>'{}'), recovery_retry_at=now()+make_interval(secs=>sqlc.arg(retry_seconds)::int)
 WHERE id=$1 AND account_id=$2 AND bucket_id=$3 AND write_phase='prepared' RETURNING *;

-- name: ObjectTrackedUploadFinish :one
UPDATE object_upload_completions SET status=$2,etag=$3,error_code=$4,encryption_verified=sqlc.arg(encryption_verified)::boolean,protection_verified=sqlc.arg(protection_verified)::boolean,write_phase='settled',recovery_token='',recovery_lease_until=NULL,recovery_cursor='',
	version_id=sqlc.arg(version_id)::text,
 recovery_versions_observed=recovery_versions_observed OR sqlc.arg(recovery_versions_observed)::boolean
 WHERE id=$1 RETURNING *;

-- name: ObjectRouteWriteSettle :execrows
UPDATE object_storage_write_admissions SET state='settled',settled_at=coalesce(settled_at,now()) WHERE id=$1 AND bucket_id=$2 AND kind='proxy' AND route_receipt;

-- name: ObjectMutationEventAppend :exec
INSERT INTO events (actor, kind, subject, data, at)
VALUES ('objectstorage', 'event.published', sqlc.arg(account_id)::uuid,
        sqlc.arg(payload)::jsonb, sqlc.arg(at)::timestamptz);

-- name: ObjectNotificationsGet :one
SELECT n.revision, n.rules, b.name, b.region, b.account_id, b.app_id
FROM object_bucket_notifications n JOIN object_buckets b ON b.id=n.bucket_id
WHERE n.bucket_id=$1;

-- name: ObjectNotificationsSave :exec
INSERT INTO object_bucket_notifications (bucket_id, revision, rules) VALUES ($1,$2,$3)
ON CONFLICT (bucket_id) DO UPDATE SET revision=EXCLUDED.revision, rules=EXCLUDED.rules;

-- name: ObjectNotificationsCapture :execrows
UPDATE event_fanout_outbox SET recipient_snapshot=recipient_snapshot || sqlc.arg(recipients)::jsonb
WHERE account_id=sqlc.arg(account_id)::uuid AND source='gregale.storage' AND event_id=sqlc.arg(event_id)::text
AND NOT EXISTS (SELECT 1 FROM jsonb_array_elements(recipient_snapshot) r WHERE r ? 'object_notification');

-- name: ObjectNotificationTargetApp :one
SELECT id FROM apps WHERE id=$1 AND account_id=$2 AND status<>'deleted';

-- name: ObjectNotificationTargetQueue :one
SELECT id, retry_policy FROM queue_bindings WHERE account_id=$1 AND app_id=$2 AND queue_name=$3 AND enabled;

-- name: ObjectNotificationLockAccount :one
SELECT plan FROM accounts WHERE id=$1 FOR UPDATE;

-- name: ObjectNotificationLockApp :one
SELECT id FROM apps WHERE id=$1 AND account_id=$2 AND status<>'deleted' FOR UPDATE;

-- name: ObjectNotificationInvocationExisting :one
SELECT app_id,account_id,source,queue_name,payload FROM invocations WHERE id=$1;

-- name: ObjectNotificationQueueDepth :one
SELECT count(*) FROM invocations WHERE app_id=$1 AND source='queue' AND state IN ('pending','dispatching');

-- name: ObjectNotificationInvocationInsert :exec
INSERT INTO invocations (id,app_id,account_id,source,queue_name,payload,headers,due_at,method,path,retry_policy)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,'POST','/',$9);

-- name: ObjectTrackedUploadDue :many
SELECT * FROM object_upload_completions WHERE write_phase IN ('prepared','dispatched') AND recovery_retry_at<=now()
 AND (recovery_lease_until IS NULL OR recovery_lease_until<=now()) ORDER BY recovery_retry_at,id LIMIT $1;

-- name: ObjectTrackedUploadClaim :one
UPDATE object_upload_completions SET recovery_token=$2,recovery_lease_until=now()+make_interval(secs=>sqlc.arg(lease_seconds)::int)
 WHERE id=$1 RETURNING *;

-- name: ObjectTrackedUploadRetry :exec
UPDATE object_upload_completions SET recovery_token='',recovery_lease_until=NULL,
 recovery_retry_at=now()+make_interval(secs=>sqlc.arg(retry_seconds)::int),error_code=$2, recovery_cursor=sqlc.arg(recovery_cursor)::text,
 recovery_versions_observed=recovery_versions_observed OR sqlc.arg(recovery_versions_observed)::boolean WHERE id=$1;


-- name: ObjectUploadReceiptGet :one
SELECT * FROM object_upload_completions WHERE id=$1 AND account_id=$2 AND app_id=$3 AND route_id IS NOT DISTINCT FROM $4 AND subject_id=$5;

-- name: ObjectGatewayUploadInsert :one
INSERT INTO object_upload_completions
 (id,account_id,app_id,bucket_id,subject_id,object_key,bytes,content_type,request_id,status,write_phase,origin,source_key,source_etag,source_bucket_id,source_copy_grant_id,encryption_snapshot,protection_snapshot,recovery_retry_at,encryption_default_revision)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,'pending','prepared',sqlc.arg(origin)::text,sqlc.arg(source_key)::text,sqlc.arg(source_etag)::text,sqlc.narg(source_bucket_id)::uuid,sqlc.narg(source_copy_grant_id)::uuid,sqlc.arg(encryption_snapshot)::jsonb,sqlc.arg(protection_snapshot)::jsonb,now()+make_interval(secs=>sqlc.arg(retry_seconds)::int),sqlc.arg(encryption_default_revision)::bigint) RETURNING *;

-- name: ObjectWriteReceiptGet :one
SELECT * FROM object_upload_completions WHERE id=$1 AND account_id=$2 AND app_id=$3 AND bucket_id=$4 AND write_phase <> 'untracked';

-- name: ObjectWriteReceiptsList :many
SELECT * FROM object_upload_completions WHERE account_id=$1 AND app_id=$2 AND bucket_id=$3 AND write_phase <> 'untracked'
 AND status=sqlc.arg(status_filter)::text
 AND (created_at,id) < (coalesce(sqlc.narg(cursor_created)::timestamptz,'infinity'::timestamptz),coalesce(sqlc.narg(cursor_id)::uuid,'ffffffff-ffff-ffff-ffff-ffffffffffff'::uuid))
 ORDER BY created_at DESC,id DESC LIMIT sqlc.arg(page_limit)::int;

-- name: ObjectWriteReceiptsListAll :many
SELECT * FROM object_upload_completions WHERE account_id=$1 AND app_id=$2 AND bucket_id=$3 AND write_phase <> 'untracked'
 AND (created_at,id) < (coalesce(sqlc.narg(cursor_created)::timestamptz,'infinity'::timestamptz),coalesce(sqlc.narg(cursor_id)::uuid,'ffffffff-ffff-ffff-ffff-ffffffffffff'::uuid))
 ORDER BY created_at DESC,id DESC LIMIT sqlc.arg(page_limit)::int;

-- name: ObjectVersionAccountingStatus :one
SELECT coalesce(u.inventory_scope,'current')::text AS inventory_scope,
 EXISTS(SELECT 1 FROM object_upload_completions WHERE bucket_id=b.id AND recovery_versions_observed UNION ALL SELECT 1 FROM object_version_references WHERE bucket_id=b.id AND versions_observed UNION ALL SELECT 1 FROM object_storage_multipart_uploads WHERE bucket_id=b.id AND completion_versions_observed UNION ALL SELECT 1 FROM object_bucket_versioning WHERE bucket_id=b.id AND versions_required) AS versions_observed,
 EXISTS(SELECT 1 FROM object_storage_capacity_reconciliations WHERE bucket_id=b.id AND inventory_scope='all_versions' AND state IN ('waiting','scanning')) AS native_scan_active
FROM object_buckets b LEFT JOIN object_storage_bucket_usage u ON u.bucket_id=b.id WHERE b.id=$1 AND b.account_id=$2;

-- name: ObjectVersionInventoryEntriesInsert :execrows
INSERT INTO object_storage_version_inventory_entries(job_id,identity_hash,bytes)
SELECT sqlc.arg(job_id),d.identity,d.bytes FROM jsonb_to_recordset(sqlc.arg(items)::jsonb) AS d(identity text,bytes bigint);

-- name: ObjectVersionInventoryCursorInsert :exec
INSERT INTO object_storage_version_inventory_cursors(job_id,cursor_hash) VALUES($1,$2);

-- name: ObjectVersionInventoryEntriesDelete :exec
DELETE FROM object_storage_version_inventory_entries WHERE job_id=$1;

-- name: ObjectVersionInventoryCursorsDelete :exec
DELETE FROM object_storage_version_inventory_cursors WHERE job_id=$1;

-- name: ObjectVersionCapacityRebase :execrows
UPDATE object_storage_bucket_usage SET baseline_bytes=sqlc.arg(bytes),baseline_keys=sqlc.arg(objects),observed_bytes=sqlc.arg(bytes),observed_keys=sqlc.arg(objects),
 granted_bytes=0,granted_keys=0,observed_at=now(),attempt_at=now(),token='',lease_until=NULL,inventory_scope='all_versions'
WHERE bucket_id=$1 AND EXISTS(SELECT 1 FROM object_buckets WHERE id=$1 AND state='ready');

-- name: ObjectVersionBucketOwned :one
SELECT id FROM object_buckets WHERE id=$1 AND account_id=$2 AND state='ready' FOR SHARE;

-- name: ObjectVersionReferencesRecord :many
INSERT INTO object_version_references(bucket_id,object_key,native_version_id,versions_observed)
SELECT sqlc.arg(bucket_id),x.object_key,x.native_version_id,x.versions_observed
FROM jsonb_to_recordset(sqlc.arg(items)::jsonb) AS x(object_key text,native_version_id text,versions_observed boolean)
ON CONFLICT(bucket_id,object_key,native_version_id) DO UPDATE SET versions_observed=object_version_references.versions_observed OR EXCLUDED.versions_observed
RETURNING id,object_key,native_version_id;

-- name: ObjectVersionReferenceResolve :one
SELECT v.native_version_id FROM object_version_references v JOIN object_buckets b ON b.id=v.bucket_id
WHERE v.id=$1 AND b.account_id=$2 AND v.bucket_id=$3 AND v.object_key=$4 AND v.native_version_id<>'null' AND b.state='ready';

-- name: ObjectVersioningGet :one
SELECT v.*,b.account_id,b.app_id FROM object_bucket_versioning v JOIN object_buckets b ON b.id=v.bucket_id WHERE v.bucket_id=$1;

-- name: ObjectVersioningSave :exec
INSERT INTO object_bucket_versioning(bucket_id,desired_status,observed_status,state,revision,versions_required,dispatched,propagation_until,capacity_job_id,lease_token,lease_until,retry_at,last_error_code,updated_at)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
ON CONFLICT(bucket_id) DO UPDATE SET desired_status=EXCLUDED.desired_status,observed_status=EXCLUDED.observed_status,state=EXCLUDED.state,revision=EXCLUDED.revision,versions_required=object_bucket_versioning.versions_required OR EXCLUDED.versions_required,dispatched=EXCLUDED.dispatched,propagation_until=EXCLUDED.propagation_until,capacity_job_id=EXCLUDED.capacity_job_id,lease_token=EXCLUDED.lease_token,lease_until=EXCLUDED.lease_until,retry_at=EXCLUDED.retry_at,last_error_code=EXCLUDED.last_error_code,updated_at=EXCLUDED.updated_at;

-- name: ObjectVersioningDue :many
SELECT bucket_id FROM object_bucket_versioning WHERE state<>'ready' AND retry_at<=now() AND (lease_until IS NULL OR lease_until<=now()) ORDER BY retry_at,bucket_id LIMIT $1;

-- name: ObjectVersioningEnsureUsage :exec
INSERT INTO object_storage_bucket_usage(bucket_id) VALUES($1) ON CONFLICT(bucket_id) DO NOTHING;

-- name: ObjectVersioningNow :one
SELECT clock_timestamp()::timestamptz AS now;

-- name: ObjectBucketEncryptionGet :one
SELECT * FROM object_bucket_encryption WHERE bucket_id=$1;

-- name: ObjectBucketEncryptionForAdmission :one
SELECT * FROM object_bucket_encryption WHERE bucket_id=$1 FOR SHARE;

-- name: ObjectBucketEncryptionInsert :exec
INSERT INTO object_bucket_encryption(bucket_id,account_id,app_id,state,revision,encryption_snapshot,desired_snapshot,lease_token,lease_until,retry_at,dispatched,updated_at)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12);

-- name: ObjectBucketEncryptionUpdate :execrows
UPDATE object_bucket_encryption SET state=$2,revision=$3,encryption_snapshot=$4,desired_snapshot=$5,
 lease_token=$6,lease_until=$7,retry_at=$8,dispatched=$9,updated_at=$10 WHERE bucket_id=$1;

-- name: ObjectBucketEncryptionDue :many
SELECT bucket_id FROM object_bucket_encryption WHERE state<>'ready' AND retry_at<=clock_timestamp()
 AND (lease_until IS NULL OR lease_until<=clock_timestamp()) ORDER BY retry_at,bucket_id LIMIT $1;

-- name: ObjectDeletionGet :one
SELECT d.*,b.account_id,b.app_id FROM object_deletions d JOIN object_buckets b ON b.id=d.bucket_id WHERE d.id=$1;

-- name: ObjectDeletionActive :one
SELECT (EXISTS(SELECT 1 FROM object_deletions WHERE object_deletions.bucket_id=$1 AND object_deletions.state IN ('prepared','dispatched')) OR EXISTS(SELECT 1 FROM object_version_protection WHERE object_version_protection.bucket_id=$1 AND object_version_protection.state IN ('waiting','applying')))::boolean AS active;

-- name: ObjectDeletionInsert :exec
INSERT INTO object_deletions(id,bucket_id,object_key,selector,state,provider_status,reserved_bytes,lease_token,lease_until,retry_at,created_at,updated_at,target_provider_version_id,lifecycle_scan_id,lifecycle_binding,protection_required)
VALUES($1,$2,$3,$4,'prepared',$5,$6,$7,$8,$9,$9,$9,sqlc.arg(target_provider_version_id),sqlc.narg(lifecycle_scan_id),sqlc.arg(lifecycle_binding),sqlc.arg(protection_required));

-- name: ObjectDeletionSave :exec
UPDATE object_deletions SET state=$2,baseline=$3,provider_version_id=$4,version_id=$5,delete_marker=$6,lease_token=$7,lease_until=$8,retry_at=$9,last_error_code=$10,updated_at=$11,recovery_claimed=sqlc.arg(recovery_claimed),protection_verified=sqlc.arg(protection_verified),deletion_verified=sqlc.arg(deletion_verified) WHERE id=$1;

-- name: ObjectDeletionDue :many
SELECT id FROM object_deletions WHERE state IN ('prepared','dispatched') AND retry_at<=now() AND (lease_until IS NULL OR lease_until<=now()) ORDER BY retry_at,id LIMIT $1;

-- name: ObjectLifecyclePolicyGet :one
SELECT l.*,b.account_id,b.app_id FROM object_bucket_lifecycle l JOIN object_buckets b ON b.id=l.bucket_id WHERE l.bucket_id=$1;

-- name: ObjectLifecyclePolicySave :exec
INSERT INTO object_bucket_lifecycle(bucket_id,revision,rules,next_scan_at,updated_at) VALUES($1,$2,$3,$4,$5)
ON CONFLICT(bucket_id) DO UPDATE SET revision=EXCLUDED.revision,rules=EXCLUDED.rules,next_scan_at=EXCLUDED.next_scan_at,updated_at=EXCLUDED.updated_at;

-- name: ObjectLifecyclePolicyDue :many
SELECT l.bucket_id FROM object_bucket_lifecycle l JOIN object_buckets b ON b.id=l.bucket_id
WHERE b.state='ready' AND EXISTS(SELECT 1 FROM jsonb_array_elements(l.rules) r WHERE r->>'status'='Enabled')
AND ((NOT EXISTS(SELECT 1 FROM object_lifecycle_scans s WHERE s.bucket_id=l.bucket_id AND s.state='scanning') AND l.next_scan_at<=clock_timestamp())
 OR EXISTS(SELECT 1 FROM object_lifecycle_scans s WHERE s.bucket_id=l.bucket_id AND s.state='scanning' AND s.retry_at<=clock_timestamp() AND (s.lease_until IS NULL OR s.lease_until<=clock_timestamp())))
ORDER BY coalesce((SELECT s.retry_at FROM object_lifecycle_scans s WHERE s.bucket_id=l.bucket_id AND s.state='scanning'),l.next_scan_at),l.bucket_id LIMIT $1;

-- name: ObjectLifecycleScanGet :one
SELECT s.*,b.account_id,b.app_id FROM object_lifecycle_scans s JOIN object_buckets b ON b.id=s.bucket_id WHERE s.id=$1;

-- name: ObjectLifecycleScanActive :one
SELECT id FROM object_lifecycle_scans WHERE bucket_id=$1 AND state='scanning';

-- name: ObjectLifecycleScanInsert :exec
INSERT INTO object_lifecycle_scans(id,bucket_id,revision,rules,retry_at,created_at,updated_at,phase) VALUES($1,$2,$3,$4,$5,$5,$5,$6);

-- name: ObjectLifecycleScanSave :exec
UPDATE object_lifecycle_scans SET state=$2,last_key=$3,scanned_keys=$4,lease_token=$5,lease_until=$6,retry_at=$7,updated_at=$8,finished_at=$9,phase=$10,last_upload_id=$11,scanned_uploads=$12 WHERE id=$1;

-- name: ObjectLifecycleMultipartList :many
SELECT * FROM object_storage_multipart_uploads
WHERE account_id=$1 AND app_id=$2 AND bucket_id=$3 AND state='active' AND provider_upload_id<>'' AND created_at<=$4 AND id>$5
ORDER BY id LIMIT sqlc.arg(page_limit)::int;

-- name: ObjectLifecycleMultipartAdmit :execrows
UPDATE object_storage_multipart_uploads SET state='aborting',lease_token=NULL,lease_until=NULL,attempt_count=0,last_error_code='',retry_at=$2,updated_at=$2,lifecycle_scan_id=$3,lifecycle_binding=$4
WHERE id=$1 AND state='active';


-- name: ObjectURLCredentialCount :one
SELECT count(*) FROM object_storage_s3_credentials WHERE bucket_id=$1 AND url_request IS NOT NULL AND status='active' AND url_expires_at>clock_timestamp();

-- name: ObjectURLCredentialForReceipt :one
SELECT id FROM object_storage_s3_credentials
WHERE account_id=$1 AND bucket_id=$2 AND url_receipt_id=$3 AND url_request IS NOT NULL AND status='active'
 AND object_url_issuer_live(account_id,bucket_id,url_api_key_id,permission,url_expires_at);

-- name: ObjectURLMultipartCredential :one
SELECT * FROM object_storage_s3_credentials WHERE id=$1 AND url_request ? 'multipart' AND status='active'
AND object_url_issuer_live(account_id,bucket_id,url_api_key_id,permission,url_expires_at) FOR UPDATE;

-- name: ObjectMultipartURLPartBegin :exec
INSERT INTO object_storage_multipart_part_grants (upload_id,part_number,max_bytes,cleanup_tracked,transfer_token,unsafe_until,url_credential_id)
VALUES (sqlc.arg(upload_id),sqlc.arg(part_number),0,true,sqlc.arg(transfer_token),clock_timestamp()+(sqlc.arg(window_seconds)::int * interval '1 second'),sqlc.arg(url_credential_id))
ON CONFLICT (upload_id,part_number) DO UPDATE SET
max_bytes=0,cleanup_tracked=true,transfer_token=EXCLUDED.transfer_token,unsafe_until=EXCLUDED.unsafe_until,url_credential_id=EXCLUDED.url_credential_id;

-- name: ObjectURLCredentialCleanup :exec
DELETE FROM object_storage_s3_credentials WHERE id IN (
 SELECT c.id FROM object_storage_s3_credentials c
 WHERE c.bucket_id=$1 AND c.url_request IS NOT NULL AND c.url_expires_at<=clock_timestamp()
 AND (c.url_receipt_id IS NULL OR EXISTS(SELECT 1 FROM object_upload_completions w WHERE w.id=c.url_receipt_id AND w.write_phase='settled'))
 ORDER BY c.url_expires_at,c.id LIMIT sqlc.arg(batch_limit)::int
);

-- name: ObjectURLCredentialInsert :one
INSERT INTO object_storage_s3_credentials
(id,account_id,bucket_id,access_key_id,secret_sealed,kid,label,permission,url_request,url_api_key_id,url_expires_at,url_receipt_id)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,sqlc.arg(url_request)::jsonb,sqlc.narg(url_api_key_id)::uuid,sqlc.arg(url_expires_at)::timestamptz,sqlc.narg(url_receipt_id)::uuid) RETURNING *;

-- name: ObjectUploadRoutesList :many
SELECT * FROM object_upload_routes WHERE account_id=$1 AND app_id=$2 ORDER BY name;

-- name: ObjectUploadRouteGet :one
SELECT * FROM object_upload_routes WHERE account_id=$1 AND app_id=$2 AND name=$3;

-- name: ObjectUploadRouteUpsert :one
INSERT INTO object_upload_routes(id,account_id,app_id,name,bucket_id,key_prefix,max_bytes,allowed_content_types,enabled,encryption_snapshot)
SELECT sqlc.arg(id)::uuid,sqlc.arg(account_id)::uuid,sqlc.arg(app_id)::uuid,sqlc.arg(name)::text,sqlc.arg(bucket_id)::uuid,sqlc.arg(key_prefix)::text,sqlc.arg(max_bytes)::bigint,COALESCE(sqlc.arg(allowed_content_types)::text[],ARRAY[]::text[]),sqlc.arg(enabled)::boolean,sqlc.arg(encryption_snapshot)::jsonb
WHERE EXISTS(SELECT 1 FROM object_buckets WHERE id=sqlc.arg(bucket_id) AND account_id=sqlc.arg(account_id) AND app_id=sqlc.arg(app_id) AND state='ready')
ON CONFLICT(app_id,name) DO UPDATE SET bucket_id=EXCLUDED.bucket_id,key_prefix=EXCLUDED.key_prefix,max_bytes=EXCLUDED.max_bytes,
allowed_content_types=EXCLUDED.allowed_content_types,enabled=EXCLUDED.enabled,encryption_snapshot=EXCLUDED.encryption_snapshot,updated_at=now()
WHERE object_upload_routes.account_id=EXCLUDED.account_id AND object_upload_routes.id=EXCLUDED.id
RETURNING *;

-- name: ObjectUploadRouteDelete :execrows
DELETE FROM object_upload_routes WHERE account_id=$1 AND app_id=$2 AND name=$3;

-- name: ObjectUploadIntentGet :one
SELECT * FROM object_upload_completions WHERE route_id=$1 AND subject_id=$2 AND idempotency_key=$3;

-- name: ObjectCopySourceLockBuckets :many
SELECT * FROM object_buckets WHERE account_id=$1 AND id IN (sqlc.arg(destination_bucket)::uuid,sqlc.arg(source_bucket)::uuid)
 ORDER BY id FOR NO KEY UPDATE;

-- name: ObjectCopySourceCredentialLock :one
SELECT * FROM object_storage_s3_credentials WHERE id=$1 AND account_id=$2 AND bucket_id=$3
 AND rotation_parent_id IS NULL AND url_request IS NULL FOR NO KEY UPDATE;

-- name: ObjectCopySourcesList :many
SELECT * FROM object_s3_copy_source_grants WHERE account_id=$1 AND bucket_id=$2 AND credential_id=$3 ORDER BY source_bucket_id;

-- name: ObjectCopySourceGet :one
SELECT * FROM object_s3_copy_source_grants WHERE credential_id=$1 AND source_bucket_id=$2;

-- name: ObjectCopySourceCount :one
SELECT count(*) FROM object_s3_copy_source_grants WHERE credential_id=$1;

-- name: ObjectCopySourceUpsert :one
INSERT INTO object_s3_copy_source_grants(id,account_id,bucket_id,credential_id,source_bucket_id,prefix)
 VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(credential_id,source_bucket_id) DO UPDATE
 SET id=EXCLUDED.id,prefix=EXCLUDED.prefix,updated_at=now() RETURNING *;

-- name: ObjectCopySourceDelete :execrows
DELETE FROM object_s3_copy_source_grants WHERE account_id=$1 AND bucket_id=$2 AND credential_id=$3 AND source_bucket_id=$4;

-- name: ObjectCopySourceResolve :one
SELECT sqlc.embed(g), sqlc.embed(s) FROM object_storage_s3_credentials c
 JOIN object_storage_s3_credentials p ON p.id=coalesce(c.rotation_parent_id,c.id)
 JOIN object_s3_copy_source_grants g ON g.credential_id=p.id
 JOIN object_buckets d ON d.id=c.bucket_id JOIN object_buckets s ON s.id=g.source_bucket_id
 WHERE c.id=sqlc.arg(credential_id)::uuid AND c.account_id=sqlc.arg(account_id)::uuid AND c.status='active'
 AND c.permission IN ('write','read_write') AND c.url_request IS NULL
 AND p.account_id=c.account_id AND p.bucket_id=c.bucket_id AND p.status='active' AND p.permission IN ('write','read_write') AND p.url_request IS NULL
 AND g.account_id=c.account_id AND g.bucket_id=c.bucket_id AND g.source_bucket_id=sqlc.arg(source_bucket_id)::uuid
 AND s.account_id=c.account_id AND d.account_id=c.account_id AND s.state='ready' AND d.state='ready'
 AND (s.backend_id,s.backend_fingerprint)=(d.backend_id,d.backend_fingerprint)
 AND left(sqlc.arg(object_key)::text,length(g.prefix))=g.prefix
 AND NOT EXISTS(SELECT 1 FROM object_deletions WHERE bucket_id IN (s.id,d.id) AND state IN ('prepared','dispatched'));

-- name: ObjectMultipartCopyPartBegin :exec
INSERT INTO object_storage_multipart_part_grants(upload_id,part_number,max_bytes,cleanup_tracked,transfer_token,unsafe_until,source_bucket_id,source_copy_grant_id,source_subject_id,source_key)
 VALUES($1,$2,$3,true,$4,clock_timestamp()+make_interval(secs=>sqlc.arg(window_seconds)::int),sqlc.arg(source_bucket_id)::uuid,sqlc.arg(source_copy_grant_id)::uuid,sqlc.arg(source_subject_id)::text,sqlc.arg(source_key)::text)
 ON CONFLICT(upload_id,part_number) DO UPDATE SET max_bytes=greatest(object_storage_multipart_part_grants.max_bytes,EXCLUDED.max_bytes),
 transfer_token=EXCLUDED.transfer_token,unsafe_until=EXCLUDED.unsafe_until,source_bucket_id=EXCLUDED.source_bucket_id,
 source_copy_grant_id=EXCLUDED.source_copy_grant_id,source_subject_id=EXCLUDED.source_subject_id,source_key=EXCLUDED.source_key;

-- name: ObjectCopySourceOwnedBucket :one
SELECT * FROM object_buckets WHERE account_id=$1 AND id=$2 AND state<>'deleted';

-- name: ObjectBucketObjectLockGet :one
SELECT l.* FROM object_bucket_object_lock l JOIN object_buckets b ON b.id=l.bucket_id AND (b.account_id,b.app_id)=(l.account_id,l.app_id) WHERE l.bucket_id=$1;

-- name: ObjectBucketObjectLockInsert :exec
INSERT INTO object_bucket_object_lock(bucket_id,account_id,app_id,state,revision,enabled_required,native_enabled_observed,observed_known,observed_snapshot,desired_snapshot,lease_token,lease_until,retry_at,dispatched,last_error_code,updated_at)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16);

-- name: ObjectBucketObjectLockUpdate :execrows
UPDATE object_bucket_object_lock SET state=$2,revision=$3,enabled_required=$4,native_enabled_observed=$5,observed_known=$6,observed_snapshot=$7,desired_snapshot=$8,lease_token=$9,lease_until=$10,retry_at=$11,dispatched=$12,last_error_code=$13,updated_at=$14 WHERE bucket_id=$1;

-- name: ObjectBucketObjectLockDue :many
SELECT bucket_id FROM object_bucket_object_lock WHERE state<>'ready' AND retry_at<=now() AND (lease_until IS NULL OR lease_until<=now()) ORDER BY retry_at,bucket_id LIMIT $1;

-- name: ListAppSecretsWithBindingAccessInScope :many
SELECT s.account_id::text AS account_id, s.app_id::text AS app_id, s.scope, s.key, s.ciphertext,
 coalesce(s.kid, '')::text AS kid, coalesce(s.value_hash, '')::text AS value_hash,
 coalesce(s.managed_postgres_binding_id::text, '')::text AS managed_postgres_binding_id,
 coalesce(s.managed_credential_ref, '')::text AS managed_credential_ref,
 coalesce(s.managed_credential_generation, 0)::bigint AS managed_credential_generation,
 coalesce(s.managed_object_storage_credential_id::text, '')::text AS managed_object_storage_credential_id,
 coalesce(s.secret_version, 0)::bigint AS secret_version, s.delivery_version,
 coalesce(s.delivered_version, 0)::bigint AS delivered_version, s.delivery_status,
 s.last_delivery_attempt_at, s.last_delivered_at,
 coalesce(s.last_delivery_error_code, '')::text AS last_delivery_error_code,
 coalesce(s.last_delivered_wake_id, '')::text AS last_delivered_wake_id,
 coalesce(s.last_delivered_instance_id, '')::text AS last_delivered_instance_id,
 coalesce(s.last_runtime_reload_version, 0)::bigint AS last_runtime_reload_version,
 coalesce(s.last_runtime_reload_revision, '')::text AS last_runtime_reload_revision,
 coalesce(s.last_runtime_reload_projection, '')::text AS last_runtime_reload_projection,
 coalesce(s.last_runtime_reload_signal, '')::text AS last_runtime_reload_signal,
 s.last_runtime_reload_at, coalesce(s.last_runtime_reload_error_code, '')::text AS last_runtime_reload_error_code,
 coalesce(s.last_runtime_reload_instance_id, '')::text AS last_runtime_reload_instance_id,
 s.created_at, s.updated_at, coalesce(s.secret_class, 'persistent')::text AS secret_class,
 coalesce(b.access, '')::text AS managed_postgres_access
FROM app_secrets s
LEFT JOIN managed_postgres_bindings b ON b.id = s.managed_postgres_binding_id
 AND b.account_id = s.account_id AND b.app_id = s.app_id
 AND b.scope = s.scope AND b.environment_key = s.key AND b.state <> 'deleted'
WHERE s.account_id = sqlc.arg(account_id)::text::uuid
 AND s.app_id = sqlc.arg(app_id)::text::uuid AND s.scope = sqlc.arg(scope)::text
ORDER BY s.scope, s.key;

-- name: FinishManagedPostgresBindingProvision :one
UPDATE managed_postgres_bindings AS binding SET state = 'ready',
 provider_identity_id = sqlc.arg(provider_identity_id)::text,
 credential_ref = sqlc.arg(credential_ref)::text,
 rotation_cleanup_ready = rotation_cleanup_ready OR (access = 'migration' AND rotation_previous_generation IS NOT NULL),
 last_error_code = NULL, lease_token = NULL, lease_until = NULL,
 attempt_count = 0, retry_at = sqlc.arg(now)::timestamptz, updated_at = sqlc.arg(now)
WHERE binding.id = sqlc.arg(id)::uuid AND binding.state = 'provisioning'
 AND binding.lease_token = sqlc.arg(lease_token)::text AND binding.lease_until > sqlc.arg(now)
 AND EXISTS (SELECT 1 FROM app_secrets secret WHERE secret.managed_postgres_binding_id = binding.id
  AND secret.managed_credential_ref = sqlc.arg(credential_ref)
  AND secret.managed_credential_generation = binding.credential_generation)
RETURNING id::text AS id, account_id::text AS account_id, database_id::text AS database_id,
 app_id::text AS app_id, scope, environment_key, access,
 coalesce(provider_identity_id, '')::text AS provider_identity_id,
 coalesce(credential_ref, '')::text AS credential_ref, credential_generation,
 coalesce(rotation_previous_generation, 0)::bigint AS rotation_previous_generation,
 coalesce(rotation_wake_id::text, '')::text AS rotation_wake_id, rotation_cleanup_ready,
 state, coalesce(last_error_code, '')::text AS last_error_code,
 coalesce(lease_token, '')::text AS lease_token, lease_until,
 attempt_count, retry_at, created_at, updated_at, deleted_at;

-- name: ClaimManagedPostgresBindingRetirement :one
UPDATE managed_postgres_bindings AS binding SET state = 'retiring',
 lease_token = sqlc.arg(lease_token)::text, lease_until = sqlc.arg(lease_until)::timestamptz,
 updated_at = sqlc.arg(now)::timestamptz, retry_at = sqlc.arg(now),
 attempt_count = CASE WHEN state <> 'retiring' THEN 1 ELSE least(attempt_count + 1, 30) END,
 last_error_code = CASE WHEN state <> 'retiring' THEN NULL ELSE last_error_code END
WHERE binding.account_id = sqlc.arg(account_id)::uuid AND binding.id = sqlc.arg(id)::uuid
 AND binding.state IN ('ready','retiring') AND binding.rotation_cleanup_ready
 AND binding.rotation_previous_generation IS NOT NULL AND binding.retry_at <= sqlc.arg(now)
 AND (binding.lease_until IS NULL OR binding.lease_until <= sqlc.arg(now))
 AND (binding.access <> 'migration' OR NOT EXISTS (SELECT 1 FROM app_tasks task WHERE task.account_id = binding.account_id
 AND task.app_id = binding.app_id AND task.deployment_scope = binding.scope
 AND task.status IN ('restoring','running')))
RETURNING id::text AS id, account_id::text AS account_id, database_id::text AS database_id,
 app_id::text AS app_id, scope, environment_key, access,
 coalesce(provider_identity_id, '')::text AS provider_identity_id,
 coalesce(credential_ref, '')::text AS credential_ref, credential_generation,
 coalesce(rotation_previous_generation, 0)::bigint AS rotation_previous_generation,
 coalesce(rotation_wake_id::text, '')::text AS rotation_wake_id, rotation_cleanup_ready,
 state, coalesce(last_error_code, '')::text AS last_error_code,
 coalesce(lease_token, '')::text AS lease_token, lease_until,
 attempt_count, retry_at, created_at, updated_at, deleted_at;

-- name: ListDueManagedPostgresBindings :many
SELECT id::text AS id, account_id::text AS account_id, database_id::text AS database_id,
 app_id::text AS app_id, scope, environment_key, access,
 coalesce(provider_identity_id, '')::text AS provider_identity_id,
 coalesce(credential_ref, '')::text AS credential_ref, credential_generation,
 coalesce(rotation_previous_generation, 0)::bigint AS rotation_previous_generation,
 coalesce(rotation_wake_id::text, '')::text AS rotation_wake_id, rotation_cleanup_ready,
 state, coalesce(last_error_code, '')::text AS last_error_code,
 coalesce(lease_token, '')::text AS lease_token, lease_until,
 attempt_count, retry_at, created_at, updated_at, deleted_at FROM managed_postgres_bindings binding
WHERE (state = 'deleting' OR (sqlc.arg(include_provisioning)::boolean AND state IN ('provisioning','failed'))
 OR (rotation_cleanup_ready AND state IN ('ready','retiring')
  AND (access <> 'migration' OR NOT EXISTS (SELECT 1 FROM app_tasks task WHERE task.account_id = binding.account_id
 AND task.app_id = binding.app_id AND task.deployment_scope = binding.scope
 AND task.status IN ('restoring','running')))))
 AND retry_at <= sqlc.arg(now)::timestamptz AND (lease_until IS NULL OR lease_until <= sqlc.arg(now))
ORDER BY retry_at, id LIMIT sqlc.arg(batch_size)::int;

-- name: ClaimManagedPostgresHealthCheck :one
WITH candidate AS (
 SELECT d.* FROM managed_postgres_databases d
 LEFT JOIN managed_postgres_health h ON h.database_id = d.id
 WHERE d.state = 'ready' AND d.provider_resource_id IS NOT NULL
 AND (h.database_id IS NULL OR h.next_check_at <= sqlc.arg(now)::timestamptz
  OR h.provider_resource_id <> d.provider_resource_id OR h.backend_fingerprint <> d.backend_fingerprint
  OR h.backend_id <> d.backend_id OR h.desired_generation <> d.desired_generation)
 AND (h.lease_until IS NULL OR h.lease_until <= sqlc.arg(now))
 ORDER BY coalesce(h.next_check_at, d.created_at), d.id
 LIMIT 1 FOR UPDATE OF d SKIP LOCKED
), claimed AS (
 INSERT INTO managed_postgres_health AS h
 (database_id, account_id, backend_id, backend_fingerprint, provider_resource_id, desired_generation, next_check_at, lease_token, lease_until)
 SELECT id, account_id, backend_id, backend_fingerprint, provider_resource_id, desired_generation,
 sqlc.arg(now), sqlc.arg(lease_token)::text, sqlc.arg(lease_until)::timestamptz FROM candidate
 ON CONFLICT (database_id) DO UPDATE SET
 account_id = EXCLUDED.account_id, backend_id = EXCLUDED.backend_id,
 backend_fingerprint = EXCLUDED.backend_fingerprint, provider_resource_id = EXCLUDED.provider_resource_id,
 desired_generation = EXCLUDED.desired_generation, next_check_at = EXCLUDED.next_check_at,
 lease_token = EXCLUDED.lease_token, lease_until = EXCLUDED.lease_until,
 provider_status = CASE WHEN (h.backend_id,h.backend_fingerprint,h.provider_resource_id,h.desired_generation)
  IS DISTINCT FROM (EXCLUDED.backend_id,EXCLUDED.backend_fingerprint,EXCLUDED.provider_resource_id,EXCLUDED.desired_generation) THEN 'unknown' ELSE h.provider_status END,
 compute_state = CASE WHEN (h.backend_id,h.backend_fingerprint,h.provider_resource_id,h.desired_generation)
  IS DISTINCT FROM (EXCLUDED.backend_id,EXCLUDED.backend_fingerprint,EXCLUDED.provider_resource_id,EXCLUDED.desired_generation) THEN 'unknown' ELSE h.compute_state END,
 checked_at = CASE WHEN (h.backend_id,h.backend_fingerprint,h.provider_resource_id,h.desired_generation)
  IS DISTINCT FROM (EXCLUDED.backend_id,EXCLUDED.backend_fingerprint,EXCLUDED.provider_resource_id,EXCLUDED.desired_generation) THEN NULL ELSE h.checked_at END,
 last_success_at = CASE WHEN (h.backend_id,h.backend_fingerprint,h.provider_resource_id,h.desired_generation)
  IS DISTINCT FROM (EXCLUDED.backend_id,EXCLUDED.backend_fingerprint,EXCLUDED.provider_resource_id,EXCLUDED.desired_generation) THEN NULL ELSE h.last_success_at END,
 last_error_code = CASE WHEN (h.backend_id,h.backend_fingerprint,h.provider_resource_id,h.desired_generation)
  IS DISTINCT FROM (EXCLUDED.backend_id,EXCLUDED.backend_fingerprint,EXCLUDED.provider_resource_id,EXCLUDED.desired_generation) THEN NULL ELSE h.last_error_code END,
 attempt_count = CASE WHEN (h.backend_id,h.backend_fingerprint,h.provider_resource_id,h.desired_generation)
  IS DISTINCT FROM (EXCLUDED.backend_id,EXCLUDED.backend_fingerprint,EXCLUDED.provider_resource_id,EXCLUDED.desired_generation) THEN 0 ELSE h.attempt_count END
 WHERE h.lease_until IS NULL OR h.lease_until <= sqlc.arg(now)
 RETURNING database_id, attempt_count
)
SELECT d.id::text AS id, d.account_id::text AS account_id, d.backend_id, d.backend_fingerprint,
 d.provider_resource_id::text AS provider_resource_id, d.desired_generation,
 d.region, d.postgres_major, d.service_class, d.availability, d.scale_to_zero, d.storage_limit_bytes,
 d.restore_window_seconds, c.attempt_count
FROM candidate d JOIN claimed c ON c.database_id = d.id;

-- name: FinishManagedPostgresHealthCheck :execrows
WITH target AS (
 SELECT d.* FROM managed_postgres_databases d
 WHERE d.id = sqlc.arg(database_id)::text::uuid AND d.state = 'ready' FOR SHARE
)
UPDATE managed_postgres_health h SET
 provider_status = sqlc.arg(provider_status)::text, compute_state = sqlc.arg(compute_state)::text,
 checked_at = sqlc.arg(checked_at)::timestamptz,
 last_success_at = CASE WHEN sqlc.arg(succeeded)::boolean THEN sqlc.arg(checked_at) ELSE h.last_success_at END,
 last_error_code = nullif(sqlc.arg(error_code)::text, ''), next_check_at = sqlc.arg(next_check_at)::timestamptz,
 attempt_count = CASE WHEN sqlc.arg(succeeded) THEN 0 ELSE least(h.attempt_count + 1,20) END,
 lease_token = NULL, lease_until = NULL
FROM target d
WHERE h.database_id = sqlc.arg(database_id)::text::uuid AND h.database_id = d.id AND d.state = 'ready'
 AND h.lease_token = sqlc.arg(lease_token)::text AND h.lease_until > sqlc.arg(checked_at)
 AND (d.account_id,d.backend_id,d.backend_fingerprint,d.provider_resource_id,d.desired_generation)
 = (h.account_id,h.backend_id,h.backend_fingerprint,h.provider_resource_id,h.desired_generation);

-- name: ReadManagedPostgresHealthSnapshots :many
SELECT h.database_id::text AS database_id, h.provider_status, h.compute_state,
 h.checked_at, h.last_success_at, coalesce(h.last_error_code,'')::text AS last_error_code
FROM managed_postgres_health h JOIN managed_postgres_databases d ON d.id = h.database_id
WHERE d.account_id = sqlc.arg(account_id)::text::uuid AND h.account_id = d.account_id AND d.state = 'ready'
 AND h.database_id::text = ANY(sqlc.arg(database_ids)::text[])
 AND (d.backend_id,d.backend_fingerprint,d.provider_resource_id,d.desired_generation)
 = (h.backend_id,h.backend_fingerprint,h.provider_resource_id,h.desired_generation);

-- name: CountManagedPostgresHealth :one
WITH statuses AS (
 SELECT CASE
 WHEN h.checked_at IS NULL AND d.created_at >= sqlc.arg(cutoff)::timestamptz THEN 'unknown'
 WHEN h.checked_at IS NULL OR h.checked_at < sqlc.arg(cutoff) OR h.checked_at > sqlc.arg(now)::timestamptz THEN 'stale'
 WHEN h.last_error_code IS NOT NULL OR h.provider_status <> 'ready' OR h.compute_state = 'unknown' THEN 'degraded'
 ELSE 'healthy' END AS status
 FROM managed_postgres_databases d LEFT JOIN managed_postgres_health h ON h.database_id = d.id
 AND h.account_id = d.account_id
 AND (d.backend_id,d.backend_fingerprint,d.provider_resource_id,d.desired_generation)
 = (h.backend_id,h.backend_fingerprint,h.provider_resource_id,h.desired_generation)
 WHERE d.state = 'ready'
)
SELECT count(*) FILTER (WHERE status='healthy') AS healthy,
 count(*) FILTER (WHERE status='degraded') AS degraded,
 count(*) FILTER (WHERE status='unknown') AS unknown,
 count(*) FILTER (WHERE status='stale') AS stale FROM statuses;

-- name: LockManagedPostgresCutoverAccount :one
SELECT status FROM accounts WHERE id=sqlc.arg(account_id)::text::uuid FOR KEY SHARE;

-- name: LockManagedPostgresCutoverApp :one
SELECT account_id::text AS account_id, status FROM apps WHERE id=sqlc.arg(app_id)::text::uuid FOR KEY SHARE;

-- name: LockManagedPostgresCutoverAdmissionApp :one
SELECT account_id::text AS account_id,status,managed_postgres_admission_cutover_id,
 managed_postgres_admission_fenced_at FROM apps WHERE id=sqlc.arg(app_id)::text::uuid FOR UPDATE;

-- name: FenceManagedPostgresCutoverAdmission :one
UPDATE apps SET managed_postgres_admission_cutover_id=sqlc.arg(cutover_id)::text::uuid,
 managed_postgres_admission_fenced_at=clock_timestamp()
WHERE id=sqlc.arg(app_id)::text::uuid AND managed_postgres_admission_cutover_id IS NULL
RETURNING managed_postgres_admission_fenced_at;

-- name: UnfenceCancelledManagedPostgresCutover :exec
UPDATE apps SET managed_postgres_admission_cutover_id=NULL,managed_postgres_admission_fenced_at=NULL
WHERE managed_postgres_admission_cutover_id=sqlc.arg(cutover_id)::text::uuid;

-- name: ManagedPostgresAdmissionFenced :one
SELECT (managed_postgres_admission_cutover_id IS NOT NULL)::boolean AS fenced FROM apps WHERE id=sqlc.arg(app_id)::text::uuid;

-- name: CancelExpiredAppTasksForClaim :exec
UPDATE app_tasks task SET status='cancelled',retry_at=NULL,finished_at=sqlc.arg(claimed_at)::timestamptz,updated_at=sqlc.arg(claimed_at)
WHERE task.status='queued' AND task.start_deadline_at<sqlc.arg(claimed_at)
AND (task.occurrence_id IS NULL OR NOT EXISTS (SELECT 1 FROM schedule_occurrences occurrence
 WHERE occurrence.id=task.occurrence_id AND occurrence.started_at IS NOT NULL));

-- name: ClaimNextUnfencedAppTask :one
WITH candidate AS (
 SELECT task.id FROM app_tasks task JOIN apps app ON app.id=task.app_id
 WHERE task.status='queued' AND task.cancel_requested_at IS NULL
 AND app.managed_postgres_admission_cutover_id IS NULL
 AND task.created_at<=sqlc.arg(claimed_at)::timestamptz
 AND (task.retry_at IS NULL OR task.retry_at<=sqlc.arg(claimed_at))
 AND (task.start_deadline_at IS NULL OR task.start_deadline_at>=sqlc.arg(claimed_at)
  OR EXISTS (SELECT 1 FROM schedule_occurrences occurrence WHERE occurrence.id=task.occurrence_id AND occurrence.started_at IS NOT NULL))
 AND (task.exclusive_operation_id IS NULL OR EXISTS (
  SELECT 1 FROM exclusive_work_operations operation
  WHERE operation.id=task.exclusive_operation_id AND operation.account_id=task.account_id
  AND operation.app_id=task.app_id AND operation.state='running'
  AND operation.generation=task.exclusive_generation
  AND operation.lease_expires_at>clock_timestamp() AND operation.attempt_deadline>clock_timestamp()))
 ORDER BY coalesce(task.retry_at,task.created_at),task.created_at,task.id
 LIMIT 1 FOR UPDATE OF task SKIP LOCKED
)
UPDATE app_tasks task SET status='restoring',lease_token=gen_random_uuid(),lease_owner=sqlc.arg(owner)::text,
lease_expires_at=sqlc.arg(expires_at)::timestamptz,retry_at=NULL,stdout_tail='',stderr_tail='',output_truncated=false,
exit_code=NULL,failure_code=NULL,failure_message=NULL,updated_at=sqlc.arg(claimed_at)
FROM candidate WHERE task.id=candidate.id RETURNING task.*;

-- name: MarkUnfencedAppTaskRunning :one
UPDATE app_tasks SET status='running',started_at=sqlc.arg(started_at)::timestamptz,attempt_count=attempt_count+1,
retry_at=NULL,stdout_tail='',stderr_tail='',output_truncated=false,exit_code=NULL,failure_code=NULL,failure_message=NULL,updated_at=sqlc.arg(started_at)
WHERE id=sqlc.arg(id)::text::uuid AND status='restoring' AND lease_token=sqlc.arg(token)::text::uuid
AND cancel_requested_at IS NULL AND lease_expires_at>sqlc.arg(started_at)
AND (start_deadline_at IS NULL OR start_deadline_at>=sqlc.arg(started_at) OR EXISTS (
 SELECT 1 FROM schedule_occurrences occurrence WHERE occurrence.id=app_tasks.occurrence_id AND occurrence.started_at IS NOT NULL))
RETURNING *;

-- name: LockManagedPostgresCutoverDatabases :many
SELECT * FROM managed_postgres_databases
WHERE id::text=ANY(sqlc.arg(database_ids)::text[]) ORDER BY id FOR UPDATE;

-- name: LockManagedPostgresCutoverBindings :many
SELECT * FROM managed_postgres_bindings WHERE account_id=sqlc.arg(account_id)::text::uuid
AND database_id=sqlc.arg(database_id)::text::uuid AND app_id=sqlc.arg(app_id)::text::uuid
AND scope=sqlc.arg(scope)::text AND state<>'deleted' ORDER BY id FOR UPDATE;

-- name: GetManagedPostgresCutover :one
SELECT * FROM managed_postgres_cutovers WHERE account_id=sqlc.arg(account_id)::text::uuid AND id=sqlc.arg(id)::text::uuid;

-- name: GetActiveManagedPostgresCutover :one
SELECT * FROM managed_postgres_cutovers WHERE account_id=sqlc.arg(account_id)::text::uuid
AND app_id=sqlc.arg(app_id)::text::uuid AND scope=sqlc.arg(scope)::text AND state<>'cancelled';

-- name: ListManagedPostgresCutoverCredentials :many
SELECT * FROM managed_postgres_cutover_credentials WHERE cutover_id=sqlc.arg(id)::text::uuid ORDER BY environment_key;

-- name: InsertManagedPostgresCutover :exec
INSERT INTO managed_postgres_cutovers(id,account_id,app_id,scope,source_database_id,target_database_id,
source_backend_id,source_backend_fingerprint,source_resource_id,source_generation,
target_backend_id,target_backend_fingerprint,target_resource_id,target_generation,state,retry_at,created_at,updated_at)
VALUES(sqlc.arg(id)::text::uuid,sqlc.arg(account_id)::text::uuid,sqlc.arg(app_id)::text::uuid,sqlc.arg(scope)::text,
sqlc.arg(source_id)::text::uuid,sqlc.arg(target_id)::text::uuid,
sqlc.arg(source_backend)::text,sqlc.arg(source_fingerprint)::text,sqlc.arg(source_resource)::text,sqlc.arg(source_generation)::bigint,
sqlc.arg(target_backend)::text,sqlc.arg(target_fingerprint)::text,sqlc.arg(target_resource)::text,sqlc.arg(target_generation)::bigint,
'preparing',sqlc.arg(now)::timestamptz,sqlc.arg(now),sqlc.arg(now));

-- name: PinManagedPostgresCutoverDatabases :execrows
UPDATE managed_postgres_databases SET cutover_id=sqlc.arg(id)::text::uuid
WHERE id::text=ANY(sqlc.arg(database_ids)::text[]) AND cutover_id IS NULL AND state='ready';

-- name: InsertManagedPostgresCutoverCredential :execrows
WITH pinned AS (UPDATE managed_postgres_bindings SET cutover_id=sqlc.arg(cutover_id)::text::uuid
WHERE id=sqlc.arg(binding_id)::text::uuid AND cutover_id IS NULL AND state='ready' AND coalesce(rotation_previous_generation,0)=0 RETURNING *)
INSERT INTO managed_postgres_cutover_credentials(id,cutover_id,source_binding_id,source_credential_generation,environment_key,access)
SELECT sqlc.arg(id)::text::uuid,cutover_id,id,credential_generation,environment_key,access FROM pinned;

-- name: ClaimManagedPostgresCutover :one
UPDATE managed_postgres_cutovers SET lease_token=sqlc.arg(token)::text,lease_until=sqlc.arg(until)::timestamptz,
attempt_count=least(attempt_count+1,30),updated_at=sqlc.arg(now)::timestamptz
WHERE id=sqlc.arg(id)::text::uuid AND account_id=sqlc.arg(account_id)::text::uuid
AND state IN ('preparing','verifying','cancelling') AND retry_at<=sqlc.arg(now) AND (lease_until IS NULL OR lease_until<=sqlc.arg(now)) RETURNING *;

-- name: LockManagedPostgresCutoverLease :one
SELECT * FROM managed_postgres_cutovers WHERE id=sqlc.arg(id)::text::uuid AND account_id=sqlc.arg(account_id)::text::uuid
AND lease_token=sqlc.arg(token)::text AND lease_until>sqlc.arg(now)::timestamptz FOR UPDATE;

-- name: SaveManagedPostgresCutoverCredential :execrows
UPDATE managed_postgres_cutover_credentials SET state='sealed',provider_identity_id=sqlc.arg(provider_identity)::text,
credential_ref=sqlc.arg(ref)::text,ciphertext=sqlc.arg(ciphertext)::bytea,kid=sqlc.arg(kid)::text,value_hash=sqlc.arg(value_hash)::text
WHERE id=sqlc.arg(id)::text::uuid AND cutover_id=sqlc.arg(cutover_id)::text::uuid AND state='pending'
AND EXISTS (SELECT 1 FROM managed_postgres_cutovers c WHERE c.id=sqlc.arg(cutover_id)::text::uuid
 AND c.state='preparing' AND c.lease_token=sqlc.arg(token)::text AND c.lease_until>clock_timestamp());

-- name: RevokeManagedPostgresCutoverCredential :execrows
UPDATE managed_postgres_cutover_credentials SET state='revoked',provider_identity_id=NULL,credential_ref=NULL,ciphertext=NULL,kid=NULL,value_hash=NULL,verified_at=NULL
WHERE id=sqlc.arg(id)::text::uuid AND cutover_id=sqlc.arg(cutover_id)::text::uuid AND state<>'revoked'
AND EXISTS (SELECT 1 FROM managed_postgres_cutovers c WHERE c.id=sqlc.arg(cutover_id)::text::uuid
 AND c.state='cancelling' AND c.lease_token=sqlc.arg(token)::text AND c.lease_until>clock_timestamp());

-- name: FinishManagedPostgresCutoverStep :exec
UPDATE managed_postgres_cutovers c SET
state=CASE WHEN c.state='preparing' AND NOT EXISTS(SELECT 1 FROM managed_postgres_cutover_credentials WHERE cutover_id=c.id AND state<>'sealed') THEN 'prepared'
WHEN c.state='cancelling' AND NOT EXISTS(SELECT 1 FROM managed_postgres_cutover_credentials WHERE cutover_id=c.id AND state<>'revoked') THEN 'cancelled' ELSE c.state END,
lease_token=NULL,lease_until=NULL,attempt_count=0,last_error_code=NULL,retry_at=sqlc.arg(now)::timestamptz,updated_at=sqlc.arg(now)
WHERE id=sqlc.arg(id)::text::uuid;

-- name: UnpinManagedPostgresCutoverDatabases :exec
UPDATE managed_postgres_databases SET cutover_id=NULL WHERE cutover_id=sqlc.arg(id)::text::uuid;

-- name: UnpinManagedPostgresCutoverBindings :exec
UPDATE managed_postgres_bindings SET cutover_id=NULL WHERE cutover_id=sqlc.arg(id)::text::uuid;

-- name: ReleaseManagedPostgresCutover :execrows
UPDATE managed_postgres_cutovers SET lease_token=NULL,lease_until=NULL,last_error_code=sqlc.arg(code)::text,
retry_at=sqlc.arg(retry_at)::timestamptz,updated_at=sqlc.arg(now)::timestamptz
WHERE id=sqlc.arg(id)::text::uuid AND account_id=sqlc.arg(account_id)::text::uuid
AND lease_token=sqlc.arg(token)::text AND lease_until>sqlc.arg(now);

-- name: CancelManagedPostgresCutover :one
UPDATE managed_postgres_cutovers SET state=CASE WHEN state='cancelled' THEN state ELSE 'cancelling' END,verified_at=NULL,
retry_at=sqlc.arg(now)::timestamptz,updated_at=sqlc.arg(now)
WHERE id=sqlc.arg(id)::text::uuid AND account_id=sqlc.arg(account_id)::text::uuid RETURNING *;

-- name: CancelDeletedOwnerManagedPostgresCutovers :exec
UPDATE managed_postgres_cutovers c SET state='cancelling',verified_at=NULL,retry_at=sqlc.arg(now)::timestamptz,updated_at=sqlc.arg(now)
FROM accounts a, apps app WHERE a.id=c.account_id AND app.id=c.app_id
AND (a.status='deleted_pending' OR app.status='deleted') AND c.state IN ('preparing','prepared','verifying','verified');

-- name: ListDueManagedPostgresCutovers :many
SELECT * FROM managed_postgres_cutovers WHERE (state='cancelling' OR (state IN ('preparing','verifying') AND sqlc.arg(include_preparing)::boolean))
AND retry_at<=sqlc.arg(now)::timestamptz AND (lease_until IS NULL OR lease_until<=sqlc.arg(now))
ORDER BY retry_at,id LIMIT sqlc.arg(batch_size)::int;

-- name: CountManagedPostgresCutoverTargetBindings :one
SELECT count(*) FROM managed_postgres_bindings WHERE database_id=sqlc.arg(id)::text::uuid AND state<>'deleted';

-- name: LockManagedPostgresCutoverForVerification :one
SELECT * FROM managed_postgres_cutovers WHERE account_id=sqlc.arg(account_id)::text::uuid
AND id=sqlc.arg(id)::text::uuid FOR UPDATE;

-- name: RequestManagedPostgresCutoverVerification :exec
UPDATE managed_postgres_cutovers SET state='verifying',verified_at=NULL,lease_token=NULL,lease_until=NULL,
attempt_count=0,last_error_code=NULL,retry_at=sqlc.arg(now)::timestamptz,updated_at=sqlc.arg(now)
WHERE id=sqlc.arg(id)::text::uuid;

-- name: ResetManagedPostgresCutoverVerification :exec
UPDATE managed_postgres_cutover_credentials SET verified_at=NULL WHERE cutover_id=sqlc.arg(id)::text::uuid;

-- name: SaveManagedPostgresCutoverVerification :execrows
UPDATE managed_postgres_cutover_credentials SET verified_at=sqlc.arg(now)::timestamptz
WHERE cutover_id=sqlc.arg(cutover_id)::text::uuid AND id=sqlc.arg(id)::text::uuid AND state='sealed'
AND provider_identity_id=sqlc.arg(provider_identity)::text AND credential_ref=sqlc.arg(ref)::text
AND ciphertext=sqlc.arg(ciphertext)::bytea AND kid=sqlc.arg(kid)::text AND value_hash=sqlc.arg(value_hash)::text
AND EXISTS (SELECT 1 FROM managed_postgres_cutovers c WHERE c.id=sqlc.arg(cutover_id)::text::uuid
 AND c.state='verifying' AND c.lease_token=sqlc.arg(token)::text
 AND c.lease_until>sqlc.arg(now)::timestamptz AND c.lease_until>clock_timestamp());

-- name: FinishManagedPostgresCutoverVerification :exec
WITH evidence AS (SELECT count(*)>0 AND bool_and(state='sealed' AND verified_at IS NOT NULL
 AND verified_at>=sqlc.arg(cutoff)::timestamptz AND verified_at<=sqlc.arg(now)::timestamptz) AS complete
 FROM managed_postgres_cutover_credentials WHERE cutover_id=sqlc.arg(id)::text::uuid)
UPDATE managed_postgres_cutovers SET state=CASE WHEN evidence.complete THEN 'verified' ELSE state END,
verified_at=CASE WHEN evidence.complete THEN sqlc.arg(now) ELSE NULL END,
lease_token=CASE WHEN evidence.complete THEN NULL ELSE lease_token END,
lease_until=CASE WHEN evidence.complete THEN NULL ELSE lease_until END,
attempt_count=CASE WHEN evidence.complete THEN 0 ELSE attempt_count END,
last_error_code=NULL,updated_at=sqlc.arg(now),retry_at=sqlc.arg(now)
FROM evidence WHERE id=sqlc.arg(id)::text::uuid;

-- name: ProbeManagedPostgresCredential :one
SELECT session_user::text AS login, current_user::text AS effective_user,
 current_database()::text AS database_name, current_setting('server_version_num')::integer AS version_num,
 current_setting('transaction_read_only')::boolean AS read_only,
 current_setting('row_security')='on' AS row_security,
 has_schema_privilege(current_user,'public','USAGE') AS schema_usage,
 has_schema_privilege(current_user,'public','CREATE') AS schema_create,
 (r.rolsuper OR r.rolcreatedb OR r.rolcreaterole OR r.rolreplication OR r.rolbypassrls OR r.rolinherit
 OR e.rolsuper OR e.rolcreatedb OR e.rolcreaterole OR e.rolreplication OR e.rolbypassrls OR e.rolinherit) AS unsafe_role,
 (has_database_privilege(current_user,current_database(),'CREATE')
 OR has_database_privilege(current_user,current_database(),'TEMPORARY')
 OR EXISTS (SELECT 1 FROM pg_catalog.pg_auth_members WHERE member=r.oid
 AND (sqlc.arg(access)::text<>'migration' OR roleid<>e.oid))
 OR EXISTS (SELECT 1 FROM pg_catalog.pg_auth_members WHERE member=e.oid AND e.oid<>r.oid)) AS elevated_runtime,
 NOT EXISTS (SELECT 1 FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace
 WHERE n.nspname='public' AND ((c.relkind IN ('r','p','v','m','f') AND NOT
 (has_table_privilege(current_user,c.oid,'SELECT') AND
 ((sqlc.arg(access)::text='read_only' AND NOT (has_table_privilege(current_user,c.oid,'INSERT')
 OR has_table_privilege(current_user,c.oid,'UPDATE') OR has_table_privilege(current_user,c.oid,'DELETE')))
 OR (sqlc.arg(access)::text<>'read_only' AND has_table_privilege(current_user,c.oid,'INSERT')
 AND has_table_privilege(current_user,c.oid,'UPDATE') AND has_table_privilege(current_user,c.oid,'DELETE')))
 AND (sqlc.arg(access)::text='migration' OR
 (c.relowner<>e.oid AND NOT has_table_privilege(current_user,c.oid,'TRUNCATE')))))
 OR (c.relkind='S' AND NOT (has_sequence_privilege(current_user,c.oid,'SELECT') AND
 ((sqlc.arg(access)::text='read_only' AND NOT (has_sequence_privilege(current_user,c.oid,'USAGE')
 OR has_sequence_privilege(current_user,c.oid,'UPDATE'))) OR (sqlc.arg(access)::text<>'read_only'
 AND has_sequence_privilege(current_user,c.oid,'USAGE')))))))
 AND (sqlc.arg(access)::text<>'read_only' OR NOT (
 EXISTS (SELECT 1 FROM pg_catalog.pg_namespace n WHERE n.nspname !~ '^pg_' AND n.nspname<>'information_schema'
 AND has_schema_privilege(current_user,n.oid,'CREATE'))
 OR EXISTS (SELECT 1 FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace
 WHERE n.nspname !~ '^pg_' AND n.nspname<>'information_schema' AND
 ((c.relkind IN ('r','p','v','m','f') AND (has_table_privilege(current_user,c.oid,'INSERT,UPDATE,DELETE,TRUNCATE,REFERENCES,TRIGGER')
 OR has_any_column_privilege(current_user,c.oid,'INSERT,UPDATE,REFERENCES')))
 OR (c.relkind='S' AND has_sequence_privilege(current_user,c.oid,'USAGE,UPDATE'))))
 OR EXISTS (SELECT 1 FROM pg_catalog.pg_proc p JOIN pg_catalog.pg_namespace n ON n.oid=p.pronamespace
 WHERE n.nspname !~ '^pg_' AND n.nspname<>'information_schema' AND p.prosecdef AND has_function_privilege(current_user,p.oid,'EXECUTE'))
 OR EXISTS (SELECT 1 FROM pg_catalog.pg_default_acl d, LATERAL aclexplode(d.defaclacl) a
 WHERE a.grantee IN (0,e.oid) AND
 ((d.defaclobjtype='r' AND a.privilege_type IN ('INSERT','UPDATE','DELETE','TRUNCATE','REFERENCES','TRIGGER'))
 OR (d.defaclobjtype='S' AND a.privilege_type IN ('USAGE','UPDATE')))))) AS data_access
FROM pg_catalog.pg_roles r JOIN pg_catalog.pg_roles e ON e.rolname=current_user WHERE r.rolname=session_user;

-- name: LockUDPListenerAppOwner :one
SELECT account_id::text AS account_id FROM apps
WHERE id = sqlc.arg(app_id)::text::uuid AND status <> 'deleted'
FOR UPDATE;

-- name: CreateUDPListener :one
INSERT INTO app_udp_listeners
(id, app_id, account_id, listener_name, guest_port, public_port, protocol, enabled)
VALUES (sqlc.arg(id)::text::uuid, sqlc.arg(app_id)::text::uuid,
        sqlc.arg(account_id)::text::uuid, sqlc.arg(listener_name),
        sqlc.arg(guest_port), sqlc.arg(public_port), sqlc.arg(protocol), sqlc.arg(enabled))
RETURNING *;

-- name: UDPListenerByID :one
SELECT * FROM app_udp_listeners WHERE id = sqlc.arg(id)::text::uuid;

-- name: UDPListenerByAppAndName :one
SELECT * FROM app_udp_listeners
WHERE app_id = sqlc.arg(app_id)::text::uuid AND listener_name = sqlc.arg(listener_name);

-- name: UDPListenerByPublicPort :one
SELECT * FROM app_udp_listeners l
WHERE public_port = sqlc.arg(public_port) AND enabled
AND EXISTS (SELECT 1 FROM apps a WHERE a.id = l.app_id AND a.account_id = l.account_id AND a.status <> 'deleted');

-- name: ListUDPListenersForApp :many
SELECT * FROM app_udp_listeners
WHERE app_id = sqlc.arg(app_id)::text::uuid ORDER BY created_at DESC, id DESC;

-- name: ListEnabledUDPListeners :many
SELECT * FROM app_udp_listeners l
WHERE enabled AND EXISTS (SELECT 1 FROM apps a WHERE a.id = l.app_id AND a.account_id = l.account_id AND a.status <> 'deleted')
ORDER BY public_port ASC;

-- name: SetUDPListenerEnabled :one
UPDATE app_udp_listeners SET enabled = sqlc.arg(enabled), updated_at = now()
WHERE id = sqlc.arg(id)::text::uuid RETURNING *;

-- name: DeleteUDPListener :execrows
DELETE FROM app_udp_listeners WHERE id = sqlc.arg(id)::text::uuid;

-- name: CountUDPListenersForApp :one
SELECT count(*) FROM app_udp_listeners WHERE app_id = sqlc.arg(app_id)::text::uuid;

-- name: ActiveTCPListenerByPublicPort :one
SELECT l.* FROM app_tcp_listeners l JOIN apps a ON a.id = l.app_id
WHERE l.public_port = sqlc.arg(public_port) AND l.enabled
  AND a.status <> 'deleted' AND a.account_id = l.account_id;

-- name: ListActiveTCPListeners :many
SELECT l.* FROM app_tcp_listeners l JOIN apps a ON a.id = l.app_id
WHERE l.enabled AND a.status <> 'deleted' AND a.account_id = l.account_id
ORDER BY l.public_port;
-- name: LockExclusiveWorkAccount :one
SELECT id::text,plan FROM accounts WHERE id=sqlc.arg(account_id)::text::uuid
AND status='active' FOR UPDATE;

-- name: ExclusiveWorkAppScope :one
SELECT id::text, coalesce(project_id::text,'')::text AS project_id
FROM apps WHERE id=sqlc.arg(app_id)::text::uuid
AND account_id=sqlc.arg(account_id)::text::uuid AND status<>'deleted' FOR SHARE;

-- name: ExclusiveWorkTenantScope :one
SELECT id::text, status FROM platform_tenants
WHERE id=sqlc.arg(tenant_id)::text::uuid AND account_id=sqlc.arg(account_id)::text::uuid
FOR SHARE;

-- name: ExclusiveWorkEnvironmentScope :one
SELECT id::text FROM project_environments
WHERE id=sqlc.arg(environment_id)::text::uuid AND account_id=sqlc.arg(account_id)::text::uuid
AND project_id=sqlc.arg(project_id)::text::uuid FOR SHARE;

-- name: ReadExclusiveWorkPolicy :one
SELECT * FROM exclusive_work_policies
WHERE account_id=sqlc.arg(account_id)::text::uuid AND name=sqlc.arg(name)::text FOR UPDATE;

-- name: ListExclusiveWorkPolicies :many
SELECT * FROM exclusive_work_policies
WHERE account_id=sqlc.arg(account_id)::text::uuid ORDER BY name;

-- name: SaveExclusiveWorkPolicy :one
INSERT INTO exclusive_work_policies(id,account_id,name,configuration,retired)
VALUES(sqlc.arg(id)::text::uuid,sqlc.arg(account_id)::text::uuid,sqlc.arg(name)::text,sqlc.arg(configuration)::jsonb,sqlc.arg(retired)::boolean)
ON CONFLICT(account_id,name) DO UPDATE SET configuration=excluded.configuration,retired=excluded.retired,
revision=exclusive_work_policies.revision+1,updated_at=clock_timestamp()
RETURNING *;

-- name: ExclusiveWorkPolicyInUse :one
SELECT EXISTS (
    SELECT 1 FROM exclusive_work_operations o
    JOIN exclusive_work_keys k ON k.id=o.key_id
    WHERE k.policy_id=sqlc.arg(policy_id)::text::uuid AND o.state IN ('pending','running')
) OR EXISTS (
    SELECT 1 FROM exclusive_work_trigger_bindings
    WHERE policy_id=sqlc.arg(policy_id)::text::uuid
) OR EXISTS (
    SELECT 1 FROM commit_sources c JOIN exclusive_work_policies p
    ON p.account_id=c.account_id AND p.name=c.operation_policy
    WHERE p.id=sqlc.arg(policy_id)::text::uuid AND c.enabled
) AS in_use;

-- name: EnsureExclusiveWorkKey :one
INSERT INTO exclusive_work_keys(id,account_id,policy_id,scope_id,environment_id,key_digest)
VALUES(sqlc.arg(id)::text::uuid,sqlc.arg(account_id)::text::uuid,sqlc.arg(policy_id)::text::uuid,
sqlc.arg(scope_id)::text::uuid,sqlc.arg(environment_id)::text,sqlc.arg(key_digest)::bytea)
ON CONFLICT(policy_id,scope_id,environment_id,key_digest) DO UPDATE SET id=exclusive_work_keys.id
RETURNING *;

-- name: ReadExclusiveWorkKey :one
SELECT * FROM exclusive_work_keys
WHERE id=sqlc.arg(id)::text::uuid AND account_id=sqlc.arg(account_id)::text::uuid FOR UPDATE;

-- name: SaveExclusiveWorkKey :exec
UPDATE exclusive_work_keys SET generation=sqlc.arg(generation)::bigint,
next_sequence=sqlc.arg(next_sequence)::bigint WHERE id=sqlc.arg(id)::text::uuid;

-- name: ReadExclusiveWorkOperation :one
SELECT * FROM exclusive_work_operations
WHERE id=sqlc.arg(id)::text::uuid AND account_id=sqlc.arg(account_id)::text::uuid;

-- name: ReadExclusiveWorkReplay :one
SELECT o.* FROM exclusive_work_operations o JOIN exclusive_work_submissions s ON s.operation_id=o.id
WHERE s.key_id=sqlc.arg(key_id)::text::uuid AND s.idempotency_digest=sqlc.arg(digest)::bytea;

-- name: ListExclusiveWorkActive :many
SELECT * FROM exclusive_work_operations
WHERE key_id=sqlc.arg(key_id)::text::uuid AND state IN ('pending','running') ORDER BY sequence;

-- name: CountExclusiveWorkPending :one
SELECT count(*) FROM exclusive_work_operations
WHERE account_id=sqlc.arg(account_id)::text::uuid AND state IN ('pending','running');

-- name: InsertExclusiveWorkOperation :one
INSERT INTO exclusive_work_operations(id,account_id,key_id,app_id,job_id,platform_tenant_id,sequence,
policy_revision,configuration,request,request_digest,equivalence_digest,idempotency_digest)
VALUES(sqlc.arg(id)::text::uuid,sqlc.arg(account_id)::text::uuid,sqlc.arg(key_id)::text::uuid,
sqlc.narg(app_id)::text::uuid,sqlc.narg(job_id)::text::uuid,nullif(sqlc.arg(tenant_id)::text,'')::uuid,
sqlc.arg(sequence)::bigint,sqlc.arg(policy_revision)::bigint,sqlc.arg(configuration)::jsonb,
sqlc.arg(request)::jsonb,sqlc.arg(request_digest)::bytea,sqlc.narg(equivalence_digest)::bytea,
sqlc.narg(idempotency_digest)::bytea) RETURNING *;

-- name: SaveExclusiveWorkOperation :exec
UPDATE exclusive_work_operations SET state=sqlc.arg(state)::text,generation=sqlc.arg(generation)::bigint,
claim_token=nullif(sqlc.arg(claim_token)::text,'')::uuid,incarnation_id=sqlc.arg(incarnation_id)::text,
lease_expires_at=sqlc.narg(lease_expires_at)::timestamptz,
attempt_deadline=sqlc.narg(attempt_deadline)::timestamptz,result=sqlc.narg(result)::jsonb,
last_error=sqlc.arg(last_error)::text,completed_at=sqlc.narg(completed_at)::timestamptz,
due_at=sqlc.arg(due_at)::timestamptz,attempts=sqlc.arg(attempts)::integer,
quota_reserved=sqlc.arg(quota_reserved)::boolean
WHERE id=sqlc.arg(id)::text::uuid;

-- name: InsertExclusiveWorkEffect :exec
INSERT INTO exclusive_work_effects(id,operation_id,generation,name,payload,webhook_id,event_type)
VALUES(sqlc.arg(id)::text::uuid,sqlc.arg(operation_id)::text::uuid,
sqlc.arg(generation)::bigint,sqlc.arg(name)::text,sqlc.arg(payload)::jsonb,
nullif(sqlc.arg(webhook_id)::text,'')::uuid,nullif(sqlc.arg(event_type)::text,''));

-- name: ReadManagedWorkflowRunForUpdate :one
SELECT app_id::text, coalesce(platform_tenant_id::text, '')::text AS platform_tenant_id, status FROM workflow_runs
WHERE id=sqlc.arg(run_id)::text::uuid FOR UPDATE;

-- name: ReadManagedWorkflowStepForUpdate :one
SELECT status, attempt FROM workflow_steps
WHERE run_id=sqlc.arg(run_id)::text::uuid AND step_name=sqlc.arg(step_name)::text
FOR UPDATE;

-- name: ManagedWorkflowEffectAppScope :one
SELECT a.account_id::text AS account_id, ac.status AS account_status
FROM apps a JOIN accounts ac ON ac.id=a.account_id
WHERE a.id=sqlc.arg(app_id)::text::uuid AND a.status<>'deleted'
FOR SHARE OF a, ac;

-- name: InsertWorkflowOperationEffect :exec
INSERT INTO workflow_operation_effects(
 id,account_id,app_id,run_id,step_name,operation_id,generation,name,payload,webhook_id,event_type
) VALUES(
 sqlc.arg(id)::text::uuid,sqlc.arg(account_id)::text::uuid,sqlc.arg(app_id)::text::uuid,
 sqlc.arg(run_id)::text::uuid,sqlc.arg(step_name)::text,sqlc.arg(operation_id)::text::uuid,
 sqlc.arg(generation)::bigint,sqlc.arg(name)::text,sqlc.arg(payload)::jsonb,
 sqlc.arg(webhook_id)::text::uuid,sqlc.arg(event_type)::text
);

-- name: ListWorkflowOperationEffects :many
SELECT e.id::text AS id,e.name,e.generation,e.webhook_id::text AS webhook_id,
 e.id::text AS delivery_id,e.event_type AS type,
 coalesce(d.status,'unavailable')::text AS status,coalesce(d.attempt,0)::integer AS attempt,
 coalesce(d.last_error,'')::text AS last_error
FROM workflow_operation_effects e
LEFT JOIN app_webhook_deliveries d ON d.id=e.id AND d.webhook_id=e.webhook_id
 AND d.app_id=e.app_id AND d.account_id=e.account_id
WHERE e.run_id=sqlc.arg(run_id)::text::uuid AND e.step_name=sqlc.arg(step_name)::text
ORDER BY e.generation,e.name;

-- name: CompleteManagedWorkflowStep :execrows
UPDATE workflow_steps
SET status='succeeded',output=sqlc.arg(output)::jsonb,error=NULL,
 next_retry_at=NULL,finished_at=clock_timestamp()
WHERE run_id=sqlc.arg(run_id)::text::uuid AND step_name=sqlc.arg(step_name)::text
 AND status='running' AND attempt=sqlc.arg(attempt)::integer;

-- name: CompleteManagedWorkflowAttempt :execrows
UPDATE workflow_step_attempts
SET status='succeeded',http_status=sqlc.arg(http_status)::integer,
 finished_at=clock_timestamp(),next_attempt_at=NULL,error=NULL
WHERE run_id=sqlc.arg(run_id)::text::uuid AND step_name=sqlc.arg(step_name)::text
 AND attempt=sqlc.arg(attempt)::integer AND status='running';

-- name: CompleteManagedWorkflowRun :execrows
UPDATE workflow_runs
SET current_step=sqlc.arg(step_name)::text,updated_at=clock_timestamp()
WHERE id=sqlc.arg(run_id)::text::uuid AND status='running';

-- name: LockWorkflowRetryAdmission :exec
SELECT pg_advisory_xact_lock(hashtextextended(sqlc.arg(app_id)::text, 0));

-- name: CountActiveWorkflowRunsForRetry :one
SELECT count(*)::bigint
FROM workflow_runs
WHERE app_id=sqlc.arg(app_id)::text::uuid
  AND status IN ('pending','running','awaiting_event');

-- name: LockWorkflowRunForManualRetry :one
SELECT * FROM workflow_runs
WHERE id=sqlc.arg(run_id)::text::uuid
FOR UPDATE;

-- name: LockWorkflowStepsForManualRetry :many
SELECT * FROM workflow_steps
WHERE run_id=sqlc.arg(run_id)::text::uuid
ORDER BY created_at,step_name
FOR UPDATE;

-- name: RequeueFailedWorkflowStepForRetry :execrows
UPDATE workflow_steps
SET status='pending',output=NULL,next_check_at=NULL,next_retry_at=NULL,
    finished_at=NULL,error=NULL
WHERE run_id=sqlc.arg(run_id)::text::uuid
  AND step_name=sqlc.arg(step_name)::text
  AND status IN ('failed','dead');

-- name: ReopenSkippedWorkflowStepsForRetry :execrows
UPDATE workflow_steps
SET status='pending',input=NULL,output=NULL,next_check_at=NULL,next_retry_at=NULL,
    finished_at=NULL,error=NULL
WHERE run_id=sqlc.arg(run_id)::text::uuid
  AND step_name=ANY(sqlc.arg(step_names)::text[])
  AND status='skipped';

-- name: RequeueWorkflowRunForRetry :one
UPDATE workflow_runs
SET status='pending',current_step=sqlc.arg(step_name)::text,
    scheduled_for=clock_timestamp(),output=NULL,finished_at=NULL,last_error=NULL,
    lease_until=NULL,updated_at=clock_timestamp()
WHERE id=sqlc.arg(run_id)::text::uuid
  AND status IN ('failed','dead')
RETURNING *;

-- name: ResolveExclusiveWebhookEffectTarget :one
SELECT id::text FROM app_webhooks
WHERE id=sqlc.arg(webhook_id)::text::uuid AND account_id=sqlc.arg(account_id)::text::uuid
 AND enabled AND 'operation.effect'=ANY(event_filter)
 AND ((sqlc.arg(tenant_id)::text='' AND scope='app' AND app_id=sqlc.arg(app_id)::text::uuid)
 OR (sqlc.arg(tenant_id)::text<>'' AND scope='platform_tenant'
 AND platform_tenant_id=nullif(sqlc.arg(tenant_id)::text,'')::uuid))
FOR SHARE;

-- name: EnqueueExclusiveWebhookEffect :exec
INSERT INTO app_webhook_deliveries(id,webhook_id,app_id,account_id,event,payload,next_attempt_at)
VALUES(sqlc.arg(id)::text::uuid,sqlc.arg(webhook_id)::text::uuid,
sqlc.arg(app_id)::text::uuid,sqlc.arg(account_id)::text::uuid,'operation.effect',sqlc.arg(payload)::jsonb,clock_timestamp());

-- name: ListExclusiveWorkEffects :many
SELECT e.id::text,e.name,e.generation,coalesce(e.webhook_id::text,'')::text AS webhook_id,
 coalesce(e.event_type,'')::text AS event_type,
 CASE WHEN e.webhook_id IS NULL THEN 'recorded' ELSE coalesce(d.status,'unavailable') END::text AS status,
 coalesce(d.attempt,0)::integer AS attempt,coalesce(d.last_error,'')::text AS last_error
FROM exclusive_work_effects e JOIN exclusive_work_operations o ON o.id=e.operation_id
LEFT JOIN app_webhook_deliveries d ON d.id=e.id AND d.webhook_id=e.webhook_id AND d.account_id=o.account_id
WHERE o.id=sqlc.arg(operation_id)::text::uuid AND o.account_id=sqlc.arg(account_id)::text::uuid
ORDER BY e.name;

-- name: ExclusiveWorkClock :one
SELECT clock_timestamp()::timestamptz AS now;

-- name: OperationEffectDeliveryAllowed :one
WITH exclusive_effect AS (
 SELECT EXISTS(SELECT 1 FROM exclusive_work_effects e
  WHERE e.id=sqlc.arg(delivery_id)::text::uuid AND e.webhook_id IS NOT NULL)::boolean AS managed,
 EXISTS (
 SELECT 1 FROM exclusive_work_effects e
 JOIN exclusive_work_operations o ON o.id=e.operation_id
 JOIN accounts ac ON ac.id=o.account_id AND ac.status='active'
 JOIN apps a ON a.id=o.app_id AND a.account_id=o.account_id AND a.status<>'deleted'
 JOIN app_webhook_deliveries d ON d.id=e.id AND d.webhook_id=e.webhook_id
  AND d.app_id=o.app_id AND d.account_id=o.account_id AND d.event='operation.effect'
 JOIN app_webhooks h ON h.id=e.webhook_id AND h.account_id=o.account_id
 WHERE e.id=sqlc.arg(delivery_id)::text::uuid AND o.state='completed' AND o.generation=e.generation
  AND h.enabled AND 'operation.effect'=ANY(h.event_filter)
  AND ((o.platform_tenant_id IS NULL AND h.scope='app' AND h.app_id=o.app_id)
   OR (o.platform_tenant_id=h.platform_tenant_id AND h.scope='platform_tenant'
    AND EXISTS(SELECT 1 FROM platform_tenants t WHERE t.id=o.platform_tenant_id AND t.account_id=o.account_id AND t.status='active')
    AND EXISTS(SELECT 1 FROM tenant_surfaces s WHERE s.app_id=o.app_id AND s.account_id=o.account_id AND s.platform_tenant_id=o.platform_tenant_id AND s.status='active')))
)::boolean AS allowed
), workflow_effect AS (
 SELECT EXISTS(SELECT 1 FROM workflow_operation_effects e
  WHERE e.id=sqlc.arg(delivery_id)::text::uuid)::boolean AS managed,
 EXISTS (
 SELECT 1 FROM workflow_operation_effects e
 JOIN accounts ac ON ac.id=e.account_id AND ac.status='active'
 JOIN apps a ON a.id=e.app_id AND a.account_id=e.account_id AND a.status<>'deleted'
 JOIN workflow_runs r ON r.id=e.run_id AND r.app_id=e.app_id
 JOIN app_webhook_deliveries d ON d.id=e.id AND d.webhook_id=e.webhook_id
   AND d.app_id=e.app_id AND d.account_id=e.account_id AND d.event='operation.effect'
  JOIN app_webhooks h ON h.id=e.webhook_id AND h.account_id=e.account_id
  WHERE e.id=sqlc.arg(delivery_id)::text::uuid AND h.enabled
   AND 'operation.effect'=ANY(h.event_filter)
   AND ((r.platform_tenant_id IS NULL AND h.scope='app' AND h.app_id=e.app_id)
    OR (r.platform_tenant_id=h.platform_tenant_id AND h.scope='platform_tenant'
     AND EXISTS(SELECT 1 FROM platform_tenants t WHERE t.id=r.platform_tenant_id AND t.account_id=e.account_id AND t.status='active')
     AND EXISTS(SELECT 1 FROM tenant_surfaces s WHERE s.app_id=e.app_id AND s.account_id=e.account_id AND s.platform_tenant_id=r.platform_tenant_id AND s.status='active')))
 )::boolean AS allowed
)
SELECT exclusive_effect.managed OR workflow_effect.managed AS managed,
 exclusive_effect.allowed OR workflow_effect.allowed AS allowed
FROM exclusive_effect, workflow_effect;

-- name: BindExclusiveWorkSubmission :exec
INSERT INTO exclusive_work_submissions(key_id,idempotency_digest,operation_id)
VALUES(sqlc.arg(key_id)::text::uuid,sqlc.arg(digest)::bytea,sqlc.arg(operation_id)::text::uuid);

-- name: CheckExclusiveWorkRuntime :one
SELECT i.id::text FROM instances i JOIN apps a ON a.id=i.app_id
WHERE i.id=sqlc.arg(instance_id)::text::uuid AND i.wake_id=sqlc.arg(wake_id)::text::uuid
AND i.node_id=sqlc.arg(node_id)::text::uuid AND i.app_id=sqlc.arg(app_id)::text::uuid
AND a.account_id=sqlc.arg(account_id)::text::uuid AND i.state='running' AND NOT i.exclusive_capture_blocked
FOR SHARE OF i;

-- name: ListDueExclusiveWork :many
WITH heads AS (
 SELECT DISTINCT ON (o.key_id) o.* FROM exclusive_work_operations o
 JOIN accounts a ON a.id=o.account_id AND a.status='active'
 LEFT JOIN platform_tenants t ON t.id=o.platform_tenant_id
 WHERE o.state IN ('pending','running') AND (t.id IS NULL OR t.status='active')
 ORDER BY o.key_id,o.sequence
), ranked AS (
 SELECT id,row_number() OVER (PARTITION BY account_id ORDER BY due_at,id) AS account_rank,due_at
 FROM heads WHERE (state='pending' AND due_at<=clock_timestamp())
 OR (state='running' AND lease_expires_at<=clock_timestamp())
)
SELECT o.* FROM exclusive_work_operations o JOIN ranked r ON r.id=o.id
ORDER BY r.account_rank,r.due_at,o.id LIMIT sqlc.arg(row_limit)::integer;

-- name: EnsureExclusiveWorkQuota :exec
INSERT INTO account_async_quota(account_id,max_inflight)
VALUES(sqlc.arg(account_id)::text::uuid,sqlc.arg(max_inflight)::integer)
ON CONFLICT(account_id) DO UPDATE SET max_inflight=excluded.max_inflight;

-- name: ReserveExclusiveWorkQuota :one
UPDATE account_async_quota SET current_inflight=current_inflight+1,updated_at=clock_timestamp()
WHERE account_id=sqlc.arg(account_id)::text::uuid AND current_inflight<max_inflight
RETURNING current_inflight;

-- name: LockExclusiveSnapshotInstance :one
SELECT id::text,exclusive_capture_blocked FROM instances
WHERE id=sqlc.arg(instance_id)::text::uuid FOR UPDATE;

-- name: HasExclusiveSnapshotOwner :one
SELECT EXISTS(SELECT 1 FROM exclusive_work_operations o JOIN instances i
ON o.incarnation_id=i.id::text||'/'||i.wake_id::text||'/'||i.node_id::text
WHERE i.id=sqlc.arg(instance_id)::text::uuid AND o.state='running'
AND o.lease_expires_at>clock_timestamp() AND o.attempt_deadline>clock_timestamp());

-- name: SetExclusiveCaptureBarrier :exec
UPDATE instances SET exclusive_capture_blocked=sqlc.arg(blocked)::boolean
WHERE id=sqlc.arg(instance_id)::text::uuid;

-- name: RecordTriggerConsumerHealth :exec
INSERT INTO trigger_consumer_health (
    trigger_id, last_poll_at, last_success_at, last_error_at, last_error,
    lag_messages, lag_age_seconds
) VALUES (
    sqlc.arg(trigger_id)::uuid,
    sqlc.arg(polled_at)::timestamptz,
    CASE WHEN sqlc.arg(success)::boolean THEN sqlc.arg(polled_at)::timestamptz ELSE NULL END,
    CASE WHEN sqlc.arg(success)::boolean THEN NULL ELSE sqlc.arg(polled_at)::timestamptz END,
    CASE WHEN sqlc.arg(success)::boolean THEN NULL ELSE NULLIF(sqlc.arg(error_detail)::text, '') END,
    sqlc.narg(lag_messages)::bigint,
    sqlc.narg(lag_age_seconds)::double precision
)
ON CONFLICT (trigger_id) DO UPDATE SET
    last_poll_at = EXCLUDED.last_poll_at,
    last_success_at = CASE WHEN sqlc.arg(success)::boolean THEN EXCLUDED.last_poll_at ELSE trigger_consumer_health.last_success_at END,
    last_error_at = CASE WHEN sqlc.arg(success)::boolean THEN trigger_consumer_health.last_error_at ELSE EXCLUDED.last_poll_at END,
    last_error = CASE WHEN sqlc.arg(success)::boolean THEN trigger_consumer_health.last_error ELSE NULLIF(sqlc.arg(error_detail)::text, '') END,
    lag_messages = EXCLUDED.lag_messages,
    lag_age_seconds = EXCLUDED.lag_age_seconds,
    updated_at = NOW();

-- name: TriggerConsumerHealth :one
SELECT last_poll_at, last_success_at, last_error_at, last_error,
       lag_messages, lag_age_seconds
FROM trigger_consumer_health
WHERE trigger_id = sqlc.arg(trigger_id)::uuid;

-- name: ReplaceProductionAppWorkloadSettings :exec
with config as (select (json_populate_record(null::apps, sqlc.arg(settings)::json)).*)
		update apps a set visibility = c.visibility, type = c.type, runtime = nullif(c.runtime, ''),
		ram_mb = c.ram_mb, cpu_millicores = nullif(c.cpu_millicores, 0), idle_timeout_s = nullif(c.idle_timeout_s, 0),
		max_concurrency = c.max_concurrency, request_rate_limit_rps = c.request_rate_limit_rps,
		request_rate_limit_burst = c.request_rate_limit_burst, min_instances = c.min_instances,
		egress_allowlist = coalesce(c.egress_allowlist, '{}'), egress_ports = coalesce(c.egress_ports, '{}'), static_egress_ip = c.static_egress_ip,
		public_auth_ip_allowlist = coalesce(c.public_auth_ip_allowlist, '{}'), autoscale_target_rps = c.autoscale_target_rps,
		autoscale_target_cpu_pct = c.autoscale_target_cpu_pct, root_dir = c.root_dir, workload_class = c.workload_class,
		streaming_enabled = c.streaming_enabled, websocket_enabled = c.websocket_enabled,
		route_metrics_enabled = c.route_metrics_enabled, app_protocol = c.app_protocol, maintenance_mode = c.maintenance_mode,
		only_declared_routes = c.only_declared_routes, declared_routes = coalesce(c.declared_routes, '[]'::jsonb),
		require_signed = c.require_signed, security_policy = c.security_policy, start_command = nullif(c.start_command, ''),
		manifest = coalesce(c.manifest, '{}'::jsonb), scaling_policy = coalesce(c.scaling_policy, '{}'::jsonb),
		retry_policy = coalesce(c.retry_policy, '{}'::jsonb), overflow_node = c.overflow_node,
		warm_snapshot_enabled = c.warm_snapshot_enabled, require_authn = c.require_authn,
		public_auth_mode = c.public_auth_mode, consumer_auth_mode = c.consumer_auth_mode,
		platform_tenant_required = coalesce(c.platform_tenant_required, false),
		public_auth_basic = c.public_auth_basic, warm_snapshot_min_requests = c.warm_snapshot_min_requests,
		warm_snapshot_min_ms = c.warm_snapshot_min_ms, warm_pool_size = c.warm_pool_size,
		eviction_priority = c.eviction_priority, cors_default_enabled = c.cors_default_enabled,
		cors_default_origins = coalesce(c.cors_default_origins, '{}'), scaling_policy_revision = a.scaling_policy_revision + 1
		from config c where a.id = sqlc.arg(app_id)::uuid;

-- ADR-531: managed PostgreSQL lifecycle reads include the separately pinned
-- dataset identity. These replace the catalog adapter's dynamic projections.
-- name: LockManagedPostgresLifecycleAccount :one
SELECT id FROM accounts WHERE id=$1 AND status<>'deleted_pending' FOR UPDATE;

-- name: FindManagedPostgresLifecycleDatabase :one
SELECT * FROM managed_postgres_databases WHERE account_id=$1 AND name=$2 AND state<>'deleted';

-- name: GetManagedPostgresLifecycleDatabase :one
SELECT * FROM managed_postgres_databases WHERE account_id=$1 AND id=$2;

-- name: ListManagedPostgresLifecycleDatabases :many
SELECT * FROM managed_postgres_databases WHERE account_id=$1 AND state<>'deleted' ORDER BY created_at,id;

-- name: DueManagedPostgresLifecycleDatabases :many
SELECT * FROM managed_postgres_databases
WHERE clone_resource_role='target' AND (state IN ('deleting','updating') OR (sqlc.arg(include_provisioning)::boolean AND state IN ('provisioning','failed')))
    AND retry_at<=sqlc.arg(at)::timestamptz AND (lease_until IS NULL OR lease_until<=sqlc.arg(at)::timestamptz)
    AND NOT EXISTS(SELECT 1 FROM project_environment_clone_postgres_snapshot_restores r WHERE r.adopted_database_id=managed_postgres_databases.id AND r.state<>'deleted')
    AND NOT EXISTS(SELECT 1 FROM project_environment_clone_postgres_copy_targets c WHERE c.target_database_id=managed_postgres_databases.id AND c.state<>'retired')
ORDER BY retry_at,id LIMIT sqlc.arg(row_limit)::integer;

-- name: CountManagedPostgresLifecycleDatabases :one
SELECT ((SELECT count(*) FROM managed_postgres_databases d WHERE d.account_id=$1 AND d.state<>'deleted')
    + (SELECT count(*) FROM project_environment_clone_postgres_snapshot_restores r WHERE r.account_id=$1 AND r.state<>'deleted' AND r.adopted_database_id IS NULL))::bigint AS count;

-- name: ReadManagedPostgresLifecycleRestoreSource :one
SELECT * FROM managed_postgres_databases WHERE id=$1 FOR KEY SHARE;

-- name: InsertManagedPostgresLifecycleDatabase :one
INSERT INTO managed_postgres_databases(id,account_id,name,region,postgres_major,service_class,availability,scale_to_zero,
    storage_limit_bytes,restore_window_seconds,backend_id,backend_fingerprint,restore_source_database_id,restore_source_resource_id,
    restore_point_in_time,state,desired_generation,observed_generation,retry_at,created_at,updated_at,accounting_required)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,false) RETURNING *;

-- name: ClaimManagedPostgresLifecycleProvision :one
UPDATE managed_postgres_databases SET state='provisioning',lease_token=sqlc.arg(lease_token)::text,
    lease_until=sqlc.arg(lease_until)::timestamptz,updated_at=sqlc.arg(at)::timestamptz,
    attempt_count=least(attempt_count+1,30),last_error_code=CASE WHEN state<>'provisioning' THEN NULL ELSE last_error_code END,
    retry_at=sqlc.arg(at)::timestamptz
WHERE account_id=sqlc.arg(account_id)::uuid AND id=sqlc.arg(database_id)::uuid AND clone_resource_role='target' AND state IN ('provisioning','failed')
    AND (lease_until IS NULL OR lease_until<=sqlc.arg(at)::timestamptz) AND retry_at<=sqlc.arg(at)::timestamptz
    AND NOT EXISTS(SELECT 1 FROM project_environment_clone_postgres_snapshot_restores r WHERE r.adopted_database_id=managed_postgres_databases.id AND r.state<>'deleted')
    AND NOT EXISTS(SELECT 1 FROM project_environment_clone_postgres_copy_targets c WHERE c.target_database_id=managed_postgres_databases.id AND c.state<>'retired') RETURNING *;

-- name: ExistsManagedPostgresLifecycleDatabase :one
SELECT EXISTS(SELECT 1 FROM managed_postgres_databases WHERE account_id=$1 AND id=$2);

-- name: LockManagedPostgresLifecycleDatabase :one
SELECT * FROM managed_postgres_databases WHERE account_id=$1 AND id=$2 FOR UPDATE;

-- name: ReadManagedPostgresLifecycleDependants :one
SELECT EXISTS(SELECT 1 FROM managed_postgres_bindings WHERE database_id=$1 AND state<>'deleted') AS has_bindings,
    EXISTS(SELECT 1 FROM managed_postgres_databases WHERE restore_source_database_id=$1 AND state<>'deleted') AS has_restore_descendants,
    EXISTS(SELECT 1 FROM project_environment_clone_postgres_snapshots WHERE source_database_id=$1 AND state<>'deleted'
        UNION ALL SELECT 1 FROM project_environment_clone_postgres_snapshot_restores WHERE adopted_database_id=$1 AND state<>'deleted'
        UNION ALL SELECT 1 FROM project_environment_clone_postgres_copy_targets WHERE (target_database_id=$1 OR capture_database_id=$1) AND state<>'retired') AS has_clone_snapshot_holds,
    EXISTS(SELECT 1 FROM project_environment_clone_postgres_write_fences WHERE source_database_id=$1 AND state<>'released') AS has_clone_write_fence_holds;

-- name: ClaimManagedPostgresLifecycleDelete :one
UPDATE managed_postgres_databases SET state='deleting',lease_token=sqlc.arg(lease_token)::text,
    lease_until=sqlc.arg(lease_until)::timestamptz,updated_at=sqlc.arg(at)::timestamptz,
    attempt_count=CASE WHEN state<>'deleting' THEN 1 ELSE least(attempt_count+1,30) END,
    last_error_code=CASE WHEN state<>'deleting' THEN NULL ELSE last_error_code END,retry_at=sqlc.arg(at)::timestamptz
WHERE account_id=sqlc.arg(account_id)::uuid AND id=sqlc.arg(database_id)::uuid AND clone_resource_role='target'
    AND NOT EXISTS(SELECT 1 FROM project_environment_clone_postgres_snapshot_restores r WHERE r.adopted_database_id=managed_postgres_databases.id AND r.state<>'deleted')
    AND NOT EXISTS(SELECT 1 FROM project_environment_clone_postgres_copy_targets c WHERE c.target_database_id=managed_postgres_databases.id AND c.state<>'retired') RETURNING *;

-- name: RecordManagedPostgresLifecycleResource :execrows
UPDATE managed_postgres_databases SET provider_resource_id=sqlc.arg(provider_resource_id)::text,accounting_required=true,updated_at=sqlc.arg(at)::timestamptz
WHERE id=sqlc.arg(database_id)::uuid AND state IN ('provisioning','deleting') AND lease_token=sqlc.arg(lease_token)::text
    AND lease_until>sqlc.arg(at)::timestamptz
    AND (NULLIF(provider_resource_id,'') IS NULL OR provider_resource_id=sqlc.arg(provider_resource_id)::text);

-- name: FinishManagedPostgresLifecycleProvision :one
UPDATE managed_postgres_databases SET state='ready',observed_generation=desired_generation,last_error_code=NULL,
    lease_token=NULL,lease_until=NULL,attempt_count=0,retry_at=sqlc.arg(at)::timestamptz,updated_at=sqlc.arg(at)::timestamptz
WHERE id=sqlc.arg(database_id)::uuid AND state='provisioning' AND lease_token=sqlc.arg(lease_token)::text
    AND lease_until>sqlc.arg(at)::timestamptz AND provider_resource_id IS NOT NULL RETURNING *;

-- name: FinishManagedPostgresLifecycleDataProvision :one
UPDATE managed_postgres_databases d SET state='ready',observed_generation=desired_generation,last_error_code=NULL,
    lease_token=NULL,lease_until=NULL,attempt_count=0,retry_at=sqlc.arg(at)::timestamptz,updated_at=sqlc.arg(at)::timestamptz,
    data_resource_id=sqlc.arg(data_resource_id)::text
WHERE d.id=sqlc.arg(database_id)::uuid AND d.account_id=sqlc.arg(account_id)::uuid AND d.state='provisioning'
    AND d.environment_clone_operation_id IS NULL AND d.deleted_at IS NULL AND d.lease_token=sqlc.arg(lease_token)::text
    AND d.lease_until>sqlc.arg(at)::timestamptz AND d.lease_until>clock_timestamp()
    AND d.provider_resource_id=sqlc.arg(provider_resource_id)::text AND d.backend_id=sqlc.arg(backend_id)::text
    AND d.backend_fingerprint=sqlc.arg(backend_fingerprint)::text AND d.desired_generation=sqlc.arg(generation)::bigint
    AND (d.data_resource_id IS NULL OR d.data_resource_id=sqlc.arg(data_resource_id)::text)
    AND sqlc.arg(spec)::jsonb=jsonb_build_object('Region',d.region,'PostgresMajor',d.postgres_major,
        'Class',d.service_class,'Availability',d.availability,'ScaleToZero',d.scale_to_zero,
        'StorageLimitBytes',d.storage_limit_bytes,'RestoreWindowSeconds',d.restore_window_seconds)
RETURNING d.*;

-- name: ReleaseManagedPostgresLifecycleLease :execrows
UPDATE managed_postgres_databases SET state=sqlc.arg(next_state)::text,last_error_code=NULLIF(sqlc.arg(error_code)::text,''),
    lease_token=NULL,lease_until=NULL,retry_at=sqlc.arg(retry_at)::timestamptz,updated_at=sqlc.arg(at)::timestamptz
WHERE id=sqlc.arg(database_id)::uuid AND lease_token=sqlc.arg(lease_token)::text AND lease_until>sqlc.arg(at)::timestamptz;

-- name: FinishManagedPostgresLifecycleDelete :one
UPDATE managed_postgres_databases SET state='deleted',last_error_code=NULL,lease_token=NULL,lease_until=NULL,
    attempt_count=0,retry_at=sqlc.arg(at)::timestamptz,updated_at=sqlc.arg(at)::timestamptz,deleted_at=sqlc.arg(at)::timestamptz
WHERE id=sqlc.arg(database_id)::uuid AND state='deleting' AND lease_token=sqlc.arg(lease_token)::text
    AND lease_until>sqlc.arg(at)::timestamptz RETURNING *;

-- name: ListManagedPostgresLifecycleUsageDatabases :many
SELECT * FROM managed_postgres_databases WHERE state='ready' AND provider_resource_id IS NOT NULL
    AND (sqlc.arg(first_page)::boolean OR (updated_at,id)>(sqlc.arg(after_time)::timestamptz,sqlc.arg(after_id)::uuid))
ORDER BY updated_at,id LIMIT sqlc.arg(row_limit)::integer;

-- name: ReadProjectEnvironmentClonePostgresSnapshot :one
SELECT * FROM project_environment_clone_postgres_snapshots
WHERE operation_id=$1 AND source_database_id=$2 FOR UPDATE;

-- name: InsertProjectEnvironmentClonePostgresSnapshot :one
INSERT INTO project_environment_clone_postgres_snapshots(operation_id,source_database_id,source_version,backend_id,backend_fingerprint,
    source_provider_resource_id,source_data_resource_id,capture_point)
SELECT sqlc.arg(operation_id)::uuid,sqlc.arg(source_database_id)::uuid,sqlc.arg(source_version)::text,sqlc.arg(backend_id)::text,
    sqlc.arg(backend_fingerprint)::text,sqlc.arg(source_provider_resource_id)::text,sqlc.arg(source_data_resource_id)::text,sqlc.arg(capture_point)::timestamptz
WHERE EXISTS(SELECT 1 FROM project_environment_clone_operations o WHERE o.id=sqlc.arg(operation_id)::uuid
    AND o.status='capturing' AND o.revision=sqlc.arg(expected_revision)::bigint AND o.lease_token::text=sqlc.arg(worker_token)::text
    AND o.lease_until>clock_timestamp()) RETURNING *;

-- name: RetainProjectEnvironmentClonePostgresSnapshot :one
UPDATE project_environment_clone_postgres_snapshots s SET state='retained',provider_snapshot_id=sqlc.arg(provider_snapshot_id)::text,
    snapshot_created_at=sqlc.arg(snapshot_created_at)::timestamptz,observed_at=clock_timestamp(),updated_at=clock_timestamp()
WHERE s.operation_id=sqlc.arg(operation_id)::uuid AND s.source_database_id=sqlc.arg(source_database_id)::uuid
    AND s.state IN ('requested','retained') AND (s.provider_snapshot_id IS NULL OR
        (s.provider_snapshot_id=sqlc.arg(provider_snapshot_id)::text AND s.snapshot_created_at=sqlc.arg(snapshot_created_at)::timestamptz))
    AND EXISTS(SELECT 1 FROM project_environment_clone_operations o WHERE o.id=s.operation_id AND o.status='capturing'
        AND o.revision=sqlc.arg(expected_revision)::bigint AND o.lease_token::text=sqlc.arg(worker_token)::text
        AND o.lease_until>clock_timestamp()) RETURNING s.*;

-- name: BeginProjectEnvironmentClonePostgresSnapshotCleanup :one
UPDATE project_environment_clone_postgres_snapshots s SET state='deleting',updated_at=clock_timestamp()
WHERE s.operation_id=sqlc.arg(operation_id)::uuid AND s.source_database_id=sqlc.arg(source_database_id)::uuid
    AND s.state IN ('capturing','requested','retained','deleting')
    AND NOT EXISTS(SELECT 1 FROM project_environment_clone_postgres_snapshot_restores r WHERE r.operation_id=s.operation_id AND r.source_database_id=s.source_database_id AND r.state<>'deleted')
    AND EXISTS(SELECT 1 FROM project_environment_clone_operations o
        WHERE o.id=s.operation_id AND o.status='compensating' AND o.revision=sqlc.arg(expected_revision)::bigint
            AND o.lease_token::text=sqlc.arg(worker_token)::text AND o.lease_until>clock_timestamp()) RETURNING s.*;

-- name: FinishProjectEnvironmentClonePostgresSnapshotCleanup :one
UPDATE project_environment_clone_postgres_snapshots s SET state='deleted',cleanup_observed_at=clock_timestamp(),updated_at=clock_timestamp()
WHERE s.operation_id=sqlc.arg(operation_id)::uuid AND s.source_database_id=sqlc.arg(source_database_id)::uuid
    AND s.state='deleting' AND (s.request_started_at IS NULL OR s.provider_snapshot_id IS NOT NULL
        OR EXISTS(SELECT 1 FROM managed_postgres_creation_receipts c JOIN project_environment_clone_operations owner ON owner.id=s.operation_id
            WHERE c.kind='snapshot' AND c.cleanup_started_at IS NOT NULL AND c.resource_id=('environment-clone-' || s.operation_id::text || '-' || s.source_database_id::text)
                AND c.account_id=owner.account_id AND c.backend_id=s.backend_id AND c.backend_fingerprint=s.backend_fingerprint
                AND c.source_resource_id=s.source_data_resource_id AND c.point_in_time=s.capture_point))
    AND NOT EXISTS(SELECT 1 FROM project_environment_clone_postgres_snapshot_restores r WHERE r.operation_id=s.operation_id AND r.source_database_id=s.source_database_id AND r.state<>'deleted')
    AND EXISTS(SELECT 1 FROM project_environment_clone_operations o
        WHERE o.id=s.operation_id AND o.status='compensating' AND o.revision=sqlc.arg(expected_revision)::bigint
            AND o.lease_token::text=sqlc.arg(worker_token)::text AND o.lease_until>clock_timestamp()) RETURNING s.*;

-- name: ClaimProjectEnvironmentClonePostgresSnapshotRequest :one
UPDATE project_environment_clone_postgres_snapshots s SET state='requested',request_started_at=clock_timestamp(),updated_at=clock_timestamp()
WHERE s.operation_id=sqlc.arg(operation_id)::uuid AND s.source_database_id=sqlc.arg(source_database_id)::uuid AND s.state='capturing'
    AND EXISTS(SELECT 1 FROM project_environment_clone_operations o WHERE o.id=s.operation_id AND o.status='capturing'
        AND o.revision=sqlc.arg(expected_revision)::bigint AND o.lease_token::text=sqlc.arg(worker_token)::text
        AND o.lease_until>clock_timestamp()) RETURNING s.*;

-- name: RecordProjectEnvironmentClonePostgresSnapshotCleanupIdentity :one
UPDATE project_environment_clone_postgres_snapshots s SET provider_snapshot_id=sqlc.arg(provider_snapshot_id)::text,
    snapshot_created_at=sqlc.arg(snapshot_created_at)::timestamptz,observed_at=clock_timestamp(),updated_at=clock_timestamp()
WHERE s.operation_id=sqlc.arg(operation_id)::uuid AND s.source_database_id=sqlc.arg(source_database_id)::uuid AND s.state='deleting'
    AND (s.provider_snapshot_id IS NULL OR (s.provider_snapshot_id=sqlc.arg(provider_snapshot_id)::text AND s.snapshot_created_at=sqlc.arg(snapshot_created_at)::timestamptz))
    AND EXISTS(SELECT 1 FROM project_environment_clone_operations o WHERE o.id=s.operation_id AND o.status='compensating'
        AND o.revision=sqlc.arg(expected_revision)::bigint AND o.lease_token::text=sqlc.arg(worker_token)::text
        AND o.lease_until>clock_timestamp()) RETURNING s.*;

-- name: ReadProjectEnvironmentClonePostgresSnapshotRestore :one
SELECT * FROM project_environment_clone_postgres_snapshot_restores WHERE operation_id=$1 AND source_database_id=$2 FOR UPDATE;

-- name: InsertProjectEnvironmentClonePostgresSnapshotRestore :one
INSERT INTO project_environment_clone_postgres_snapshot_restores(operation_id,source_database_id,account_id,backend_id,backend_fingerprint)
SELECT s.operation_id,s.source_database_id,o.account_id,s.backend_id,s.backend_fingerprint
FROM project_environment_clone_postgres_snapshots s JOIN project_environment_clone_operations o ON o.id=s.operation_id
WHERE s.operation_id=sqlc.arg(operation_id)::uuid AND s.source_database_id=sqlc.arg(source_database_id)::uuid AND s.state='retained'
    AND o.status='capturing' AND o.revision=sqlc.arg(expected_revision)::bigint AND o.lease_token::text=sqlc.arg(worker_token)::text
    AND o.lease_until>clock_timestamp() RETURNING *;

-- name: ClaimProjectEnvironmentClonePostgresSnapshotRestoreRequest :one
UPDATE project_environment_clone_postgres_snapshot_restores r SET state='requested',request_started_at=clock_timestamp(),updated_at=clock_timestamp()
WHERE r.operation_id=sqlc.arg(operation_id)::uuid AND r.source_database_id=sqlc.arg(source_database_id)::uuid AND r.state='reserved'
    AND EXISTS(SELECT 1 FROM project_environment_clone_operations o JOIN project_environment_clone_postgres_snapshots s ON s.operation_id=o.id
        WHERE o.id=r.operation_id AND s.source_database_id=r.source_database_id AND s.state='retained' AND o.status='capturing'
        AND o.revision=sqlc.arg(expected_revision)::bigint AND o.lease_token::text=sqlc.arg(worker_token)::text
        AND o.lease_until>clock_timestamp()) RETURNING r.*;

-- name: RecordProjectEnvironmentClonePostgresSnapshotRestore :one
UPDATE project_environment_clone_postgres_snapshot_restores r
SET state=CASE WHEN sqlc.arg(restored)::boolean THEN 'restored' ELSE 'restoring' END,
    target_provider_resource_id=sqlc.arg(target_provider_resource_id)::text,target_created_at=sqlc.arg(target_created_at)::timestamptz,
    observed_at=clock_timestamp(),restored_at=CASE WHEN sqlc.arg(restored)::boolean THEN coalesce(r.restored_at,clock_timestamp()) ELSE NULL END,
    updated_at=clock_timestamp()
WHERE r.operation_id=sqlc.arg(operation_id)::uuid AND r.source_database_id=sqlc.arg(source_database_id)::uuid
    AND r.state IN ('requested','restoring','restored') AND (r.state<>'restored' OR sqlc.arg(restored)::boolean)
    AND sqlc.arg(target_created_at)::timestamptz<=clock_timestamp()
    AND (r.target_provider_resource_id IS NULL OR (r.target_provider_resource_id=sqlc.arg(target_provider_resource_id)::text AND r.target_created_at=sqlc.arg(target_created_at)::timestamptz))
    AND EXISTS(SELECT 1 FROM project_environment_clone_operations o JOIN project_environment_clone_postgres_snapshots s ON s.operation_id=o.id
        WHERE o.id=r.operation_id AND s.source_database_id=r.source_database_id AND s.state='retained' AND o.status='capturing'
        AND o.revision=sqlc.arg(expected_revision)::bigint AND o.lease_token::text=sqlc.arg(worker_token)::text
        AND o.lease_until>clock_timestamp()) RETURNING r.*;

-- name: BeginProjectEnvironmentClonePostgresSnapshotRestoreCleanup :one
UPDATE project_environment_clone_postgres_snapshot_restores r SET state='deleting',deletion_started_at=coalesce(r.deletion_started_at,clock_timestamp()),updated_at=clock_timestamp()
WHERE r.operation_id=sqlc.arg(operation_id)::uuid AND r.source_database_id=sqlc.arg(source_database_id)::uuid AND r.state<>'deleted'
    AND EXISTS(SELECT 1 FROM project_environment_clone_operations o WHERE o.id=r.operation_id AND o.status='compensating'
        AND o.revision=sqlc.arg(expected_revision)::bigint AND o.lease_token::text=sqlc.arg(worker_token)::text AND o.lease_until>clock_timestamp())
    AND NOT EXISTS(SELECT 1 FROM project_environment_clone_postgres_copy_targets c WHERE c.operation_id=r.operation_id AND c.source_database_id=r.source_database_id AND c.state<>'retired')
    AND NOT EXISTS(SELECT 1 FROM project_environment_clone_postgres_copy_readers c WHERE c.operation_id=r.operation_id AND c.source_database_id=r.source_database_id
        AND c.state<>'retired' AND (c.state<>'deleting' OR c.endpoint_id IS NULL)) RETURNING r.*;

-- name: RecordProjectEnvironmentClonePostgresSnapshotRestoreCleanupIdentity :one
UPDATE project_environment_clone_postgres_snapshot_restores r SET target_provider_resource_id=sqlc.arg(target_provider_resource_id)::text,
    target_created_at=sqlc.arg(target_created_at)::timestamptz,observed_at=clock_timestamp(),updated_at=clock_timestamp()
WHERE r.operation_id=sqlc.arg(operation_id)::uuid AND r.source_database_id=sqlc.arg(source_database_id)::uuid AND r.state='deleting' AND r.request_started_at IS NOT NULL
    AND sqlc.arg(target_created_at)::timestamptz<=clock_timestamp()
    AND (r.target_provider_resource_id IS NULL OR (r.target_provider_resource_id=sqlc.arg(target_provider_resource_id)::text AND r.target_created_at=sqlc.arg(target_created_at)::timestamptz))
    AND EXISTS(SELECT 1 FROM project_environment_clone_operations o WHERE o.id=r.operation_id AND o.status='compensating'
        AND o.revision=sqlc.arg(expected_revision)::bigint AND o.lease_token::text=sqlc.arg(worker_token)::text AND o.lease_until>clock_timestamp()) RETURNING r.*;

-- name: RecordProjectEnvironmentClonePostgresSnapshotRestoreDeletionOperations :one
UPDATE project_environment_clone_postgres_snapshot_restores r SET delete_operation_ids=sqlc.arg(delete_operation_ids)::jsonb,updated_at=clock_timestamp()
WHERE r.operation_id=sqlc.arg(operation_id)::uuid AND r.source_database_id=sqlc.arg(source_database_id)::uuid AND r.state='deleting'
    AND r.target_provider_resource_id=sqlc.arg(target_provider_resource_id)::text
    AND (r.delete_operation_ids='[]'::jsonb OR r.delete_operation_ids=sqlc.arg(delete_operation_ids)::jsonb)
    AND EXISTS(SELECT 1 FROM project_environment_clone_operations o WHERE o.id=r.operation_id AND o.status='compensating'
        AND o.revision=sqlc.arg(expected_revision)::bigint AND o.lease_token::text=sqlc.arg(worker_token)::text AND o.lease_until>clock_timestamp()) RETURNING r.*;

-- name: FinishProjectEnvironmentClonePostgresSnapshotRestoreCleanup :one
UPDATE project_environment_clone_postgres_snapshot_restores r SET state='deleted',deleted_at=clock_timestamp(),updated_at=clock_timestamp()
WHERE r.operation_id=sqlc.arg(operation_id)::uuid AND r.source_database_id=sqlc.arg(source_database_id)::uuid AND r.state='deleting'
    AND (r.request_started_at IS NULL OR (r.target_provider_resource_id=sqlc.arg(target_provider_resource_id)::text AND jsonb_array_length(r.delete_operation_ids)>0 AND r.delete_operation_ids=sqlc.arg(delete_operation_ids)::jsonb))
    AND EXISTS(SELECT 1 FROM project_environment_clone_operations o WHERE o.id=r.operation_id AND o.status='compensating'
        AND o.revision=sqlc.arg(expected_revision)::bigint AND o.lease_token::text=sqlc.arg(worker_token)::text AND o.lease_until>clock_timestamp())
    AND NOT EXISTS(SELECT 1 FROM project_environment_clone_postgres_copy_targets c WHERE c.operation_id=r.operation_id AND c.source_database_id=r.source_database_id AND c.state<>'retired') RETURNING r.*;

-- ADR-531: these statements run after ObjectBucketMutationLock in one
-- transaction. Separate statements are necessary for a fresh READ COMMITTED
-- snapshot after waiting for a concurrent source writer or lifecycle change.
-- name: ObjectBucketMutationLock :one
SELECT * FROM object_buckets
WHERE id=sqlc.arg(bucket_id) AND account_id=sqlc.arg(account_id) AND app_id=sqlc.arg(app_id)
FOR UPDATE;

-- name: ObjectBucketMutationInsert :one
INSERT INTO object_bucket_mutations (id, bucket_id, kind, backend_id, backend_fingerprint, physical_name)
VALUES (sqlc.arg(id), sqlc.arg(bucket_id), sqlc.arg(kind), sqlc.arg(backend_id), sqlc.arg(backend_fingerprint), sqlc.arg(physical_name))
RETURNING *;

-- name: ObjectBucketMutationFinish :execrows
DELETE FROM object_bucket_mutations
WHERE id=sqlc.arg(id) AND bucket_id=sqlc.arg(bucket_id) AND kind='request'
AND backend_id=sqlc.arg(backend_id) AND backend_fingerprint=sqlc.arg(backend_fingerprint)
AND physical_name=sqlc.arg(physical_name);

-- name: ObjectBucketNativeGrants :many
SELECT * FROM object_bucket_mutations
WHERE bucket_id=sqlc.arg(bucket_id) AND kind='native_grant' ORDER BY id;

-- Only an independently authenticated provider retirement observation may
-- consume this private statement. Request completion cannot call it.
-- name: CloneObjectNativeGrantsFinish :execrows
DELETE FROM object_bucket_mutations
WHERE bucket_id=sqlc.arg(bucket_id) AND id=ANY(sqlc.arg(grant_ids)::uuid[]) AND kind='native_grant'
AND backend_id=sqlc.arg(backend_id) AND backend_fingerprint=sqlc.arg(backend_fingerprint)
AND physical_name=sqlc.arg(physical_name);

-- name: CloneObjectGrantRevocationRead :one
SELECT * FROM project_environment_clone_object_grant_revocations
WHERE operation_id=sqlc.arg(operation_id) AND source_bucket_id=sqlc.arg(source_bucket_id);

-- name: CloneObjectGrantRevocationInsert :one
INSERT INTO project_environment_clone_object_grant_revocations
(operation_id, source_bucket_id, request_id, plan, plan_sha256)
VALUES (sqlc.arg(operation_id), sqlc.arg(source_bucket_id), sqlc.arg(request_id), sqlc.arg(plan), sqlc.arg(plan_sha256))
RETURNING *;

-- name: CloneObjectGrantRevocationDispatch :one
UPDATE project_environment_clone_object_grant_revocations
SET state=CASE WHEN state='reserved' THEN 'dispatched' ELSE state END,
request_started_at=COALESCE(request_started_at,clock_timestamp())
WHERE operation_id=sqlc.arg(operation_id) AND source_bucket_id=sqlc.arg(source_bucket_id)
AND request_id=sqlc.arg(request_id) AND plan_sha256=sqlc.arg(plan_sha256)
RETURNING *;

-- name: CloneObjectGrantRevocationObserve :one
UPDATE project_environment_clone_object_grant_revocations
SET revocation_id=sqlc.arg(revocation_id), observed_at=clock_timestamp(),
state=CASE WHEN sqlc.arg(drained)::boolean THEN 'drained' ELSE state END,
drained_at=CASE WHEN sqlc.arg(drained)::boolean THEN COALESCE(drained_at,clock_timestamp()) ELSE drained_at END
WHERE operation_id=sqlc.arg(operation_id) AND source_bucket_id=sqlc.arg(source_bucket_id)
AND request_id=sqlc.arg(request_id) AND plan_sha256=sqlc.arg(plan_sha256) AND state<>'reserved'
AND (revocation_id='' OR revocation_id=sqlc.arg(revocation_id))
AND (state<>'drained' OR sqlc.arg(drained)::boolean)
RETURNING *;

-- name: ObjectBucketWriteFenceInsert :exec
INSERT INTO object_bucket_write_fences (bucket_id, token, backend_id, backend_fingerprint, physical_name)
VALUES (sqlc.arg(bucket_id), sqlc.arg(token), sqlc.arg(backend_id), sqlc.arg(backend_fingerprint), sqlc.arg(physical_name))
ON CONFLICT (bucket_id) DO NOTHING;

-- name: ObjectBucketWriteFenceRead :one
SELECT f.*,
 (SELECT count(*) FROM object_bucket_mutations m WHERE m.bucket_id=f.bucket_id AND m.kind='request') AS requests,
 (SELECT count(*) FROM object_bucket_mutations m WHERE m.bucket_id=f.bucket_id AND m.kind='native_grant') AS native_grants
FROM object_bucket_write_fences f WHERE f.bucket_id=sqlc.arg(bucket_id);

-- name: ObjectBucketWriteFenceDelete :execrows
DELETE FROM object_bucket_write_fences
WHERE bucket_id=sqlc.arg(bucket_id) AND token=sqlc.arg(token)
AND backend_id=sqlc.arg(backend_id) AND backend_fingerprint=sqlc.arg(backend_fingerprint)
AND physical_name=sqlc.arg(physical_name) AND clone_operation_id IS NULL;

-- name: CloneObjectWriteFenceInsert :execrows
INSERT INTO object_bucket_write_fences (bucket_id, token, backend_id, backend_fingerprint, physical_name, clone_operation_id)
SELECT sqlc.arg(bucket_id), op.id, sqlc.arg(backend_id), sqlc.arg(backend_fingerprint), sqlc.arg(physical_name), op.id
FROM project_environment_clone_operations op
WHERE op.id=sqlc.arg(operation_id) AND op.status='capturing'
AND op.revision=sqlc.arg(expected_revision) AND op.lease_token=sqlc.arg(worker_token)::uuid
AND op.lease_until > clock_timestamp()
ON CONFLICT (bucket_id) DO NOTHING;

-- name: CloneObjectWriteFenceBuckets :many
SELECT b.* FROM object_buckets b JOIN object_bucket_write_fences f ON f.bucket_id=b.id
WHERE f.clone_operation_id=sqlc.arg(operation_id) ORDER BY b.id;

-- name: CloneObjectWriteFenceDelete :execrows
DELETE FROM object_bucket_write_fences f USING project_environment_clone_operations op
WHERE f.bucket_id=sqlc.arg(bucket_id) AND f.clone_operation_id=op.id AND f.token=op.id
AND f.backend_id=sqlc.arg(backend_id) AND f.backend_fingerprint=sqlc.arg(backend_fingerprint)
AND f.physical_name=sqlc.arg(physical_name)
AND op.id=sqlc.arg(operation_id) AND op.status='compensating'
AND op.revision=sqlc.arg(expected_revision) AND op.lease_token=sqlc.arg(worker_token)::uuid
AND op.lease_until > clock_timestamp()
AND NOT EXISTS (SELECT 1 FROM project_environment_clone_object_grant_revocations r
 WHERE r.operation_id=op.id AND r.source_bucket_id=f.bucket_id AND r.request_started_at IS NOT NULL AND r.state<>'drained');

-- name: ObjectUploadGrantInsert :one
WITH receipt_clock AS (SELECT clock_timestamp() AS at)
INSERT INTO object_storage_upload_grants
(id,bucket_id,account_id,app_id,token_hash,kind,object_key,size_bytes,headers,upload_id,provider_upload_id,part_number,backend_id,backend_fingerprint,physical_name,created_at,expires_at)
SELECT sqlc.arg(id),sqlc.arg(bucket_id),sqlc.arg(account_id),sqlc.arg(app_id),sqlc.arg(token_hash),sqlc.arg(kind),sqlc.arg(object_key),sqlc.arg(size_bytes),sqlc.arg(headers),
 sqlc.narg(upload_id),sqlc.narg(provider_upload_id),sqlc.arg(part_number),sqlc.arg(backend_id),sqlc.arg(backend_fingerprint),sqlc.arg(physical_name),
 receipt_clock.at,LEAST(receipt_clock.at+sqlc.arg(ttl_seconds)::int*interval '1 second',sqlc.narg(expiry_cap)::timestamptz)
FROM receipt_clock RETURNING *;

-- name: ObjectUploadGrantResolve :one
SELECT g.* FROM object_storage_upload_grants g
JOIN object_buckets b ON b.id=g.bucket_id AND b.account_id=g.account_id AND b.app_id=g.app_id
 AND b.backend_id=g.backend_id AND b.backend_fingerprint=g.backend_fingerprint AND b.physical_name=g.physical_name
WHERE g.token_hash=sqlc.arg(token_hash) AND g.expires_at>clock_timestamp() AND b.state='ready'
 AND (b.environment_clone_operation_id IS NULL OR EXISTS (
  SELECT 1 FROM project_environment_clone_operations o WHERE o.id=b.environment_clone_operation_id
   AND o.account_id=b.account_id AND o.target_environment=b.scope AND o.status='ready'))
 AND (g.kind='put' OR EXISTS (
  SELECT 1 FROM object_storage_multipart_uploads u WHERE u.id=g.upload_id AND u.bucket_id=b.id AND u.app_id=b.app_id AND u.account_id=b.account_id
   AND u.object_key=g.object_key AND u.provider_upload_id=g.provider_upload_id AND u.state='active' AND u.expires_at>clock_timestamp()
   AND u.part_count>0 AND g.part_number<=u.part_count
   AND g.size_bytes=CASE WHEN g.part_number=u.part_count THEN u.size_bytes-u.part_size_bytes*(u.part_count-1) ELSE u.part_size_bytes END));

-- name: ObjectUploadGrantPrune :execrows
WITH expired AS (
 SELECT id FROM object_storage_upload_grants WHERE expires_at<=clock_timestamp()
 ORDER BY expires_at,id FOR UPDATE SKIP LOCKED LIMIT sqlc.arg(batch_limit)::int
)
DELETE FROM object_storage_upload_grants g USING expired WHERE g.id=expired.id;

-- name: ObjectUploadGrantClock :one
SELECT clock_timestamp()::timestamptz AS at;

-- ADR-531: source recovery holds precede any remote PostgreSQL closure.
-- name: ReadClonePostgresWriteFence :one
SELECT * FROM project_environment_clone_postgres_write_fences
WHERE operation_id=sqlc.arg(operation_id)::uuid AND source_database_id=sqlc.arg(source_database_id)::uuid FOR UPDATE;

-- name: ListClonePostgresWriteFences :many
SELECT * FROM project_environment_clone_postgres_write_fences
WHERE operation_id=$1 ORDER BY source_database_id FOR UPDATE;

-- name: InsertClonePostgresWriteFence :one
INSERT INTO project_environment_clone_postgres_write_fences(operation_id,source_database_id,source_version,
 backend_id,backend_fingerprint,source_provider_resource_id,source_data_resource_id)
SELECT o.id,sqlc.arg(source_database_id)::uuid,sqlc.arg(source_version)::text,sqlc.arg(backend_id)::text,
 sqlc.arg(backend_fingerprint)::text,sqlc.arg(source_provider_resource_id)::text,sqlc.arg(source_data_resource_id)::text
FROM project_environment_clone_operations o WHERE o.id=sqlc.arg(operation_id)::uuid AND o.status='capturing'
 AND o.revision=sqlc.arg(expected_revision)::bigint AND o.lease_token::text=sqlc.arg(worker_token)::text
 AND o.lease_until>clock_timestamp() RETURNING *;

-- name: BeginClonePostgresWriteFenceAbandonment :one
UPDATE project_environment_clone_postgres_write_fences f SET state='abandoning',updated_at=clock_timestamp()
WHERE f.operation_id=sqlc.arg(operation_id)::uuid AND f.source_database_id=sqlc.arg(source_database_id)::uuid
 AND f.state IN ('held','abandoning') AND EXISTS (SELECT 1 FROM project_environment_clone_operations o
  WHERE o.id=f.operation_id AND o.status='compensating' AND o.revision=sqlc.arg(expected_revision)::bigint
   AND o.lease_token::text=sqlc.arg(worker_token)::text AND o.lease_until>clock_timestamp()) RETURNING f.*;

-- name: FinishClonePostgresWriteFenceAbandonment :one
UPDATE project_environment_clone_postgres_write_fences f SET state='released',remote_terminal_state=sqlc.arg(remote_terminal_state)::text,
 remote_released_at=sqlc.arg(remote_released_at)::timestamptz,released_at=clock_timestamp(),updated_at=clock_timestamp()
WHERE f.operation_id=sqlc.arg(operation_id)::uuid AND f.source_database_id=sqlc.arg(source_database_id)::uuid
 AND f.state='abandoning' AND sqlc.arg(remote_released_at)::timestamptz<=clock_timestamp()
 AND NOT EXISTS (SELECT 1 FROM managed_postgres_checkpoint_maintenance m WHERE m.source_database_id=f.source_database_id AND m.state<>'ready')
 AND EXISTS (SELECT 1 FROM project_environment_clone_operations o
  WHERE o.id=f.operation_id AND o.status='compensating' AND o.revision=sqlc.arg(expected_revision)::bigint
   AND o.lease_token::text=sqlc.arg(worker_token)::text AND o.lease_until>clock_timestamp()) RETURNING f.*;

-- ADR-531: private original selection precedes a coordinated capture point.
-- name: ReadClonePostgresCheckpointSelection :one
SELECT * FROM project_environment_clone_postgres_checkpoint_selections
WHERE operation_id=sqlc.arg(operation_id)::uuid AND source_database_id=sqlc.arg(source_database_id)::uuid FOR UPDATE;

-- name: InsertClonePostgresCheckpointSelection :one
INSERT INTO project_environment_clone_postgres_checkpoint_selections(operation_id,source_database_id,maintenance_id,scope,fingerprint,key_id,ciphertext_sha256,ciphertext)
SELECT o.id,sqlc.arg(source_database_id)::uuid,sqlc.arg(maintenance_id)::uuid,sqlc.arg(scope)::jsonb,sqlc.arg(fingerprint)::text,
 sqlc.arg(key_id)::text,sqlc.arg(ciphertext_sha256)::text,sqlc.arg(ciphertext)::bytea
FROM project_environment_clone_operations o WHERE o.id=sqlc.arg(operation_id)::uuid AND o.status='capturing'
 AND o.revision=sqlc.arg(expected_revision)::bigint AND o.lease_token::text=sqlc.arg(worker_token)::text
 AND o.lease_until>clock_timestamp() RETURNING *;

-- ADR-531: resource ownership is separate from an operation's writer barrier.
-- name: ReadClonePostgresMaintenance :one
SELECT * FROM managed_postgres_checkpoint_maintenance WHERE source_database_id=$1 FOR UPDATE;

-- name: InsertClonePostgresMaintenance :one
INSERT INTO managed_postgres_checkpoint_maintenance(source_database_id,reserved_by_operation_id,
 backend_id,backend_fingerprint,source_provider_resource_id,source_data_resource_id)
SELECT f.source_database_id,f.operation_id,f.backend_id,f.backend_fingerprint,f.source_provider_resource_id,f.source_data_resource_id
FROM project_environment_clone_postgres_write_fences f JOIN project_environment_clone_operations o ON o.id=f.operation_id
WHERE f.operation_id=sqlc.arg(operation_id)::uuid AND f.source_database_id=sqlc.arg(source_database_id)::uuid
 AND ((o.status='capturing' AND f.state='held') OR (o.status='compensating' AND f.state='abandoning'))
 AND o.revision=sqlc.arg(expected_revision)::bigint AND o.lease_token::text=sqlc.arg(worker_token)::text
 AND o.lease_until>clock_timestamp() RETURNING *;

-- name: ClaimClonePostgresMaintenanceDispatch :one
UPDATE managed_postgres_checkpoint_maintenance m SET state=sqlc.arg(requested_state)::text,
 role_requested_at=CASE WHEN sqlc.arg(requested_state)::text='role_requested' THEN clock_timestamp() ELSE m.role_requested_at END,
 database_requested_at=CASE WHEN sqlc.arg(requested_state)::text='database_requested' THEN clock_timestamp() ELSE m.database_requested_at END,
 activation_requested_at=CASE WHEN sqlc.arg(requested_state)::text='activation_requested' THEN clock_timestamp() ELSE m.activation_requested_at END,
 updated_at=clock_timestamp()
WHERE m.source_database_id=sqlc.arg(source_database_id)::uuid AND m.state=sqlc.arg(before_state)::text
 AND EXISTS (SELECT 1 FROM project_environment_clone_postgres_write_fences f JOIN project_environment_clone_operations o ON o.id=f.operation_id
  WHERE f.operation_id=sqlc.arg(operation_id)::uuid AND f.source_database_id=m.source_database_id
   AND ((o.status='capturing' AND f.state='held') OR (o.status='compensating' AND f.state='abandoning'))
   AND o.revision=sqlc.arg(expected_revision)::bigint AND o.lease_token::text=sqlc.arg(worker_token)::text AND o.lease_until>clock_timestamp())
RETURNING m.*;

-- name: RecordClonePostgresMaintenance :one
UPDATE managed_postgres_checkpoint_maintenance m SET state=sqlc.arg(complete_state)::text,
 owner_oid=sqlc.arg(owner_oid)::bigint,database_oid=sqlc.narg(database_oid)::bigint,
 ready_at=CASE WHEN sqlc.arg(complete_state)::text='ready' THEN clock_timestamp() ELSE m.ready_at END,updated_at=clock_timestamp()
WHERE m.source_database_id=sqlc.arg(source_database_id)::uuid AND m.state=sqlc.arg(requested_state)::text
 AND (m.owner_oid IS NULL OR m.owner_oid=sqlc.arg(owner_oid)::bigint)
 AND (m.database_oid IS NULL OR m.database_oid IS NOT DISTINCT FROM sqlc.narg(database_oid)::bigint)
 AND EXISTS (SELECT 1 FROM project_environment_clone_postgres_write_fences f JOIN project_environment_clone_operations o ON o.id=f.operation_id
  WHERE f.operation_id=sqlc.arg(operation_id)::uuid AND f.source_database_id=m.source_database_id
   AND ((o.status='capturing' AND f.state='held') OR (o.status='compensating' AND f.state='abandoning'))
   AND o.revision=sqlc.arg(expected_revision)::bigint AND o.lease_token::text=sqlc.arg(worker_token)::text AND o.lease_until>clock_timestamp())
RETURNING m.*;

-- name: PinProjectEnvironmentCloneNativeForkDatabase :one
UPDATE managed_postgres_databases d SET provider_resource_id=r.target_provider_resource_id,updated_at=clock_timestamp()
FROM project_environment_clone_postgres_snapshot_restores r,project_environment_clone_operations o
WHERE r.operation_id=sqlc.arg(operation_id)::uuid AND r.source_database_id=sqlc.arg(source_database_id)::uuid AND r.state='restored'
    AND d.id=r.target_owner_id AND d.account_id=r.account_id AND d.environment_clone_operation_id=r.operation_id
    AND d.restore_source_database_id=r.source_database_id AND d.clone_resource_role='checkpoint' AND d.state='provisioning' AND d.provider_resource_id IS NULL
    AND d.data_resource_id IS NULL AND d.desired_generation=1 AND d.observed_generation=0 AND d.lease_token IS NULL
    AND o.id=r.operation_id AND o.status='capturing' AND o.revision=sqlc.arg(expected_revision)::bigint
    AND o.lease_token::text=sqlc.arg(worker_token)::text AND o.lease_until>clock_timestamp() RETURNING d.*;

-- name: AdoptProjectEnvironmentClonePostgresSnapshotRestore :one
UPDATE project_environment_clone_postgres_snapshot_restores r SET state='adopted',adopted_database_id=r.target_owner_id,
    adopted_at=clock_timestamp(),updated_at=clock_timestamp()
WHERE r.operation_id=sqlc.arg(operation_id)::uuid AND r.source_database_id=sqlc.arg(source_database_id)::uuid AND r.state='restored'
    AND EXISTS(SELECT 1 FROM managed_postgres_databases d WHERE d.id=r.target_owner_id AND d.account_id=r.account_id
        AND d.environment_clone_operation_id=r.operation_id AND d.restore_source_database_id=r.source_database_id
        AND d.clone_resource_role='checkpoint' AND d.provider_resource_id=r.target_provider_resource_id AND d.backend_id=r.backend_id AND d.backend_fingerprint=r.backend_fingerprint
        AND d.state='provisioning' AND d.desired_generation=1 AND d.observed_generation=0 AND d.data_resource_id IS NULL AND d.lease_token IS NULL)
    AND EXISTS(SELECT 1 FROM project_environment_clone_operations o WHERE o.id=r.operation_id AND o.status='capturing'
        AND o.revision=sqlc.arg(expected_revision)::bigint AND o.lease_token::text=sqlc.arg(worker_token)::text AND o.lease_until>clock_timestamp()) RETURNING r.*;

-- name: RetireProjectEnvironmentCloneNativeForkDatabase :one
UPDATE managed_postgres_databases d SET state='deleted',deleted_at=clock_timestamp(),updated_at=clock_timestamp()
FROM project_environment_clone_postgres_snapshot_restores r,project_environment_clone_operations o
WHERE r.operation_id=sqlc.arg(operation_id)::uuid AND r.source_database_id=sqlc.arg(source_database_id)::uuid AND r.state='deleting'
    AND d.id=r.adopted_database_id AND d.id=r.target_owner_id AND d.account_id=r.account_id AND d.environment_clone_operation_id=r.operation_id
    AND d.restore_source_database_id=r.source_database_id AND d.clone_resource_role='checkpoint' AND d.provider_resource_id=r.target_provider_resource_id
    AND d.state='provisioning' AND d.desired_generation=1 AND d.observed_generation=0 AND d.data_resource_id IS NULL AND d.lease_token IS NULL
    AND d.backend_id=r.backend_id AND d.backend_fingerprint=r.backend_fingerprint
    AND NOT EXISTS(SELECT 1 FROM managed_postgres_bindings b WHERE b.database_id=d.id AND b.state<>'deleted')
    AND NOT EXISTS(SELECT 1 FROM managed_postgres_databases child WHERE child.restore_source_database_id=d.id AND child.state<>'deleted')
    AND o.id=r.operation_id AND o.status='compensating' AND o.revision=sqlc.arg(expected_revision)::bigint
    AND o.lease_token::text=sqlc.arg(worker_token)::text AND o.lease_until>clock_timestamp()
    AND NOT EXISTS(SELECT 1 FROM project_environment_clone_postgres_copy_targets c WHERE c.operation_id=r.operation_id AND c.source_database_id=r.source_database_id AND c.state<>'retired')
    AND NOT EXISTS(SELECT 1 FROM project_environment_clone_postgres_copy_readers c WHERE c.operation_id=r.operation_id AND c.source_database_id=r.source_database_id AND c.state<>'retired') RETURNING d.*;


-- name: ReadProjectEnvironmentClonePostgresCopyTarget :one
SELECT * FROM project_environment_clone_postgres_copy_targets WHERE operation_id=$1 AND source_database_id=$2 FOR UPDATE;

-- name: ReadProjectEnvironmentClonePostgresTargetSQLPins :one
SELECT * FROM project_environment_clone_postgres_target_sql_pins WHERE operation_id=$1 AND source_database_id=$2 FOR UPDATE;

-- name: ReadProjectEnvironmentClonePostgresRolePlan :one
SELECT * FROM project_environment_clone_postgres_role_plans WHERE operation_id=$1 AND source_database_id=$2 FOR UPDATE;

-- name: ReadProjectEnvironmentClonePostgresDatabasePlan :one
SELECT * FROM project_environment_clone_postgres_database_plans WHERE operation_id=$1 AND source_database_id=$2 FOR UPDATE;

-- name: ReadProjectEnvironmentClonePostgresDatabaseSQLPins :one
SELECT * FROM project_environment_clone_postgres_database_sql_pins WHERE operation_id=$1 AND source_database_id=$2 AND database_oid=$3 FOR UPDATE;

-- name: InsertProjectEnvironmentClonePostgresDatabaseSQLPins :one
INSERT INTO project_environment_clone_postgres_database_sql_pins(operation_id,source_database_id,database_oid,account_id,project_id,target_database_id,archive_owner_id,
 archive_reservation_sha256,database_plan_ciphertext_sha256,target_provider_resource_id,target_provider_created_at,scope,inventory_fingerprint,target_fingerprint,key_id,ciphertext,ciphertext_sha256)
SELECT sqlc.arg(operation_id)::uuid,sqlc.arg(source_database_id)::uuid,sqlc.arg(database_oid)::bigint,sqlc.arg(account_id)::uuid,sqlc.arg(project_id)::uuid,sqlc.arg(target_database_id)::uuid,sqlc.arg(archive_owner_id)::uuid,
 sqlc.arg(archive_reservation_sha256)::text,sqlc.arg(database_plan_ciphertext_sha256)::text,sqlc.arg(target_provider_resource_id)::text,sqlc.arg(target_provider_created_at)::timestamptz,
 sqlc.arg(scope)::jsonb,sqlc.arg(inventory_fingerprint)::text,sqlc.arg(target_fingerprint)::text,sqlc.arg(key_id)::text,sqlc.arg(ciphertext)::bytea,sqlc.arg(ciphertext_sha256)::text
WHERE EXISTS(SELECT 1 FROM project_environment_clone_operations o WHERE o.id=sqlc.arg(operation_id)::uuid
 AND o.account_id=sqlc.arg(account_id)::uuid AND o.project_id=sqlc.arg(project_id)::uuid AND o.status='capturing'
 AND o.revision=sqlc.arg(expected_revision)::bigint AND o.lease_token::text=sqlc.arg(worker_token)::text AND o.lease_until>clock_timestamp()) RETURNING *;

-- name: InsertProjectEnvironmentClonePostgresDatabasePlan :one
INSERT INTO project_environment_clone_postgres_database_plans(operation_id,source_database_id,account_id,project_id,target_database_id,
 scope,inventory_fingerprint,inventory_ciphertext_sha256,target_fingerprint,target_pins_ciphertext_sha256,role_plan_ciphertext_sha256,key_id,ciphertext,ciphertext_sha256)
SELECT sqlc.arg(operation_id)::uuid,sqlc.arg(source_database_id)::uuid,sqlc.arg(account_id)::uuid,sqlc.arg(project_id)::uuid,sqlc.arg(target_database_id)::uuid,
 sqlc.arg(scope)::jsonb,sqlc.arg(inventory_fingerprint)::text,sqlc.arg(inventory_ciphertext_sha256)::text,sqlc.arg(target_fingerprint)::text,
 sqlc.arg(target_pins_ciphertext_sha256)::text,sqlc.arg(role_plan_ciphertext_sha256)::text,sqlc.arg(key_id)::text,sqlc.arg(ciphertext)::bytea,sqlc.arg(ciphertext_sha256)::text
WHERE EXISTS(SELECT 1 FROM project_environment_clone_operations o WHERE o.id=sqlc.arg(operation_id)::uuid
 AND o.account_id=sqlc.arg(account_id)::uuid AND o.project_id=sqlc.arg(project_id)::uuid AND o.status='capturing'
 AND o.revision=sqlc.arg(expected_revision)::bigint AND o.lease_token::text=sqlc.arg(worker_token)::text AND o.lease_until>clock_timestamp()) RETURNING *;

-- name: ReadProjectEnvironmentClonePostgresMembershipPlan :one
SELECT * FROM project_environment_clone_postgres_membership_plans WHERE operation_id=$1 AND source_database_id=$2 FOR UPDATE;

-- name: InsertProjectEnvironmentClonePostgresMembershipPlan :one
INSERT INTO project_environment_clone_postgres_membership_plans(operation_id,source_database_id,account_id,project_id,target_database_id,
 scope,inventory_fingerprint,inventory_ciphertext_sha256,target_fingerprint,target_pins_ciphertext_sha256,role_plan_ciphertext_sha256,key_id,ciphertext,ciphertext_sha256)
SELECT sqlc.arg(operation_id)::uuid,sqlc.arg(source_database_id)::uuid,sqlc.arg(account_id)::uuid,sqlc.arg(project_id)::uuid,sqlc.arg(target_database_id)::uuid,
 sqlc.arg(scope)::jsonb,sqlc.arg(inventory_fingerprint)::text,sqlc.arg(inventory_ciphertext_sha256)::text,sqlc.arg(target_fingerprint)::text,
 sqlc.arg(target_pins_ciphertext_sha256)::text,sqlc.arg(role_plan_ciphertext_sha256)::text,sqlc.arg(key_id)::text,sqlc.arg(ciphertext)::bytea,sqlc.arg(ciphertext_sha256)::text
WHERE EXISTS(SELECT 1 FROM project_environment_clone_operations o WHERE o.id=sqlc.arg(operation_id)::uuid
 AND o.account_id=sqlc.arg(account_id)::uuid AND o.project_id=sqlc.arg(project_id)::uuid AND o.status='capturing'
 AND o.revision=sqlc.arg(expected_revision)::bigint AND o.lease_token::text=sqlc.arg(worker_token)::text AND o.lease_until>clock_timestamp()) RETURNING *;

-- name: InsertProjectEnvironmentClonePostgresRolePlan :one
INSERT INTO project_environment_clone_postgres_role_plans(operation_id,source_database_id,account_id,project_id,target_database_id,
 scope,inventory_fingerprint,inventory_ciphertext_sha256,target_fingerprint,target_pins_ciphertext_sha256,key_id,ciphertext,ciphertext_sha256)
SELECT sqlc.arg(operation_id)::uuid,sqlc.arg(source_database_id)::uuid,sqlc.arg(account_id)::uuid,sqlc.arg(project_id)::uuid,sqlc.arg(target_database_id)::uuid,
 sqlc.arg(scope)::jsonb,sqlc.arg(inventory_fingerprint)::text,sqlc.arg(inventory_ciphertext_sha256)::text,sqlc.arg(target_fingerprint)::text,
 sqlc.arg(target_pins_ciphertext_sha256)::text,sqlc.arg(key_id)::text,sqlc.arg(ciphertext)::bytea,sqlc.arg(ciphertext_sha256)::text
WHERE EXISTS(SELECT 1 FROM project_environment_clone_operations o WHERE o.id=sqlc.arg(operation_id)::uuid
 AND o.account_id=sqlc.arg(account_id)::uuid AND o.project_id=sqlc.arg(project_id)::uuid AND o.status='capturing'
 AND o.revision=sqlc.arg(expected_revision)::bigint AND o.lease_token::text=sqlc.arg(worker_token)::text AND o.lease_until>clock_timestamp()) RETURNING *;

-- name: InsertProjectEnvironmentClonePostgresTargetSQLPins :one
INSERT INTO project_environment_clone_postgres_target_sql_pins(operation_id,source_database_id,account_id,project_id,target_database_id,
    target_provider_resource_id,target_provider_created_at,scope,inventory_fingerprint,target_fingerprint,key_id,ciphertext,ciphertext_sha256)
SELECT sqlc.arg(operation_id)::uuid,sqlc.arg(source_database_id)::uuid,sqlc.arg(account_id)::uuid,sqlc.arg(project_id)::uuid,sqlc.arg(target_database_id)::uuid,
    sqlc.arg(target_provider_resource_id)::text,sqlc.arg(target_provider_created_at)::timestamptz,sqlc.arg(scope)::jsonb,sqlc.arg(inventory_fingerprint)::text,
    sqlc.arg(target_fingerprint)::text,sqlc.arg(key_id)::text,sqlc.arg(ciphertext)::bytea,sqlc.arg(ciphertext_sha256)::text
WHERE EXISTS(SELECT 1 FROM project_environment_clone_operations o WHERE o.id=sqlc.arg(operation_id)::uuid
    AND o.account_id=sqlc.arg(account_id)::uuid AND o.project_id=sqlc.arg(project_id)::uuid AND o.status='capturing'
    AND o.revision=sqlc.arg(expected_revision)::bigint AND o.lease_token::text=sqlc.arg(worker_token)::text AND o.lease_until>clock_timestamp()) RETURNING *;

-- name: ReadProjectEnvironmentClonePostgresInventory :one
SELECT * FROM project_environment_clone_postgres_inventories WHERE operation_id=$1 AND source_database_id=$2 FOR UPDATE;

-- name: ReadProjectEnvironmentClonePostgresContents :one
SELECT * FROM project_environment_clone_postgres_contents WHERE operation_id=$1 AND source_database_id=$2 AND database_oid=$3 FOR UPDATE;

-- name: ReadProjectEnvironmentClonePostgresVerification :one
SELECT * FROM project_environment_clone_postgres_verifications WHERE operation_id=$1 AND source_database_id=$2 AND database_oid=$3 FOR UPDATE;

-- name: InsertProjectEnvironmentClonePostgresVerification :one
INSERT INTO project_environment_clone_postgres_verifications(operation_id,source_database_id,database_oid,account_id,project_id,verification_id,scope,
 contents_owner_id,contents_ciphertext_sha256,manifest_fingerprint,import_id,import_started_at,database_sql_pins_ciphertext_sha256,database_plan_ciphertext_sha256,
 archive_reservation_sha256,target_fingerprint,key_id,reserved_bytes)
SELECT sqlc.arg(operation_id)::uuid,sqlc.arg(source_database_id)::uuid,sqlc.arg(database_oid)::bigint,sqlc.arg(account_id)::uuid,sqlc.arg(project_id)::uuid,sqlc.arg(verification_id)::uuid,sqlc.arg(scope)::jsonb,
 sqlc.arg(contents_owner_id)::uuid,sqlc.arg(contents_ciphertext_sha256)::text,sqlc.arg(manifest_fingerprint)::text,sqlc.arg(import_id)::uuid,sqlc.arg(import_started_at)::timestamptz,
 sqlc.arg(database_sql_pins_ciphertext_sha256)::text,sqlc.arg(database_plan_ciphertext_sha256)::text,sqlc.arg(archive_reservation_sha256)::text,
 sqlc.arg(target_fingerprint)::text,sqlc.arg(key_id)::text,sqlc.arg(reserved_bytes)::bigint
WHERE EXISTS(SELECT 1 FROM project_environment_clone_operations o WHERE o.id=sqlc.arg(operation_id)::uuid AND o.status='capturing'
 AND o.account_id=sqlc.arg(account_id)::uuid AND o.project_id=sqlc.arg(project_id)::uuid AND o.revision=sqlc.arg(expected_revision)::bigint
 AND o.lease_token::text=sqlc.arg(worker_token)::text AND o.lease_until>clock_timestamp()) RETURNING *;

-- name: ClaimProjectEnvironmentClonePostgresVerification :one
UPDATE project_environment_clone_postgres_verifications v SET state='verifying',request_started_at=clock_timestamp()
WHERE v.operation_id=sqlc.arg(operation_id)::uuid AND v.source_database_id=sqlc.arg(source_database_id)::uuid AND v.database_oid=sqlc.arg(database_oid)::bigint AND v.state='reserved'
 AND EXISTS(SELECT 1 FROM project_environment_clone_operations o WHERE o.id=v.operation_id AND o.status='capturing' AND o.revision=sqlc.arg(expected_revision)::bigint
 AND o.lease_token::text=sqlc.arg(worker_token)::text AND o.lease_until>clock_timestamp()) RETURNING v.*;

-- name: RecordProjectEnvironmentClonePostgresVerificationMatch :one
UPDATE project_environment_clone_postgres_verifications v SET state='compared',window_opened_at=sqlc.arg(window_opened_at)::timestamptz,
 target_database_oid=sqlc.arg(target_database_oid)::bigint,fingerprint=sqlc.arg(fingerprint)::text,ciphertext=sqlc.arg(ciphertext)::bytea,
 ciphertext_sha256=sqlc.arg(ciphertext_sha256)::text,compared_at=clock_timestamp()
WHERE v.operation_id=sqlc.arg(operation_id)::uuid AND v.source_database_id=sqlc.arg(source_database_id)::uuid AND v.database_oid=sqlc.arg(database_oid)::bigint AND v.state='verifying'
 AND EXISTS(SELECT 1 FROM project_environment_clone_operations o WHERE o.id=v.operation_id AND o.status='capturing' AND o.revision=sqlc.arg(expected_revision)::bigint
 AND o.lease_token::text=sqlc.arg(worker_token)::text AND o.lease_until>clock_timestamp()) RETURNING v.*;

-- name: RecordProjectEnvironmentClonePostgresVerificationClosure :one
UPDATE project_environment_clone_postgres_verifications v SET state='verified',native_closed_at=sqlc.arg(native_closed_at)::timestamptz,verified_at=clock_timestamp()
WHERE v.operation_id=sqlc.arg(operation_id)::uuid AND v.source_database_id=sqlc.arg(source_database_id)::uuid AND v.database_oid=sqlc.arg(database_oid)::bigint AND v.state='compared'
 AND EXISTS(SELECT 1 FROM project_environment_clone_operations o WHERE o.id=v.operation_id AND o.status='capturing' AND o.revision=sqlc.arg(expected_revision)::bigint
 AND o.lease_token::text=sqlc.arg(worker_token)::text AND o.lease_until>clock_timestamp()) RETURNING v.*;

-- name: ReadProjectEnvironmentClonePostgresVerificationAttempts :many
SELECT * FROM project_environment_clone_postgres_verification_attempts WHERE operation_id=$1 AND source_database_id=$2 AND database_oid=$3 ORDER BY attempt FOR UPDATE;

-- name: HasProjectEnvironmentClonePostgresVerificationAttempts :one
SELECT EXISTS(SELECT 1 FROM project_environment_clone_postgres_verification_attempts WHERE operation_id=$1 AND source_database_id=$2 AND database_oid=$3);

-- name: InsertProjectEnvironmentClonePostgresVerificationAttempt :one
INSERT INTO project_environment_clone_postgres_verification_attempts(operation_id,source_database_id,database_oid,original_verification_id,attempt,verification_id,
 previous_attempt,previous_verification_id,previous_opened_at,previous_closed_at,key_id,reserved_bytes,state,request_started_at,window_opened_at,target_database_oid,native_closed_at,failed_at,created_at)
SELECT sqlc.arg(operation_id)::uuid,sqlc.arg(source_database_id)::uuid,sqlc.arg(database_oid)::bigint,sqlc.arg(original_verification_id)::uuid,sqlc.arg(attempt)::smallint,sqlc.arg(verification_id)::uuid,
 sqlc.narg(previous_attempt)::smallint,sqlc.narg(previous_verification_id)::uuid,sqlc.narg(previous_opened_at)::timestamptz,sqlc.narg(previous_closed_at)::timestamptz,
 sqlc.arg(key_id)::text,sqlc.arg(reserved_bytes)::bigint,sqlc.arg(state)::text,sqlc.narg(request_started_at)::timestamptz,sqlc.narg(window_opened_at)::timestamptz,
 sqlc.narg(target_database_oid)::bigint,sqlc.narg(native_closed_at)::timestamptz,
 CASE WHEN sqlc.arg(state)::text='failed' THEN clock_timestamp() ELSE NULL END,coalesce(sqlc.narg(created_at)::timestamptz,clock_timestamp())
WHERE EXISTS(SELECT 1 FROM project_environment_clone_operations o WHERE o.id=sqlc.arg(operation_id)::uuid AND o.status='capturing' AND o.revision=sqlc.arg(expected_revision)::bigint
 AND o.lease_token::text=sqlc.arg(worker_token)::text AND o.lease_until>clock_timestamp()) RETURNING *;

-- name: ClaimProjectEnvironmentClonePostgresVerificationAttempt :one
UPDATE project_environment_clone_postgres_verification_attempts v SET state='verifying',request_started_at=clock_timestamp()
WHERE v.operation_id=sqlc.arg(operation_id)::uuid AND v.source_database_id=sqlc.arg(source_database_id)::uuid AND v.database_oid=sqlc.arg(database_oid)::bigint AND v.verification_id=sqlc.arg(verification_id)::uuid AND v.state='reserved'
 AND EXISTS(SELECT 1 FROM project_environment_clone_operations o WHERE o.id=v.operation_id AND o.status='capturing' AND o.revision=sqlc.arg(expected_revision)::bigint
 AND o.lease_token::text=sqlc.arg(worker_token)::text AND o.lease_until>clock_timestamp()) RETURNING v.*;

-- name: RecordProjectEnvironmentClonePostgresVerificationAttemptMatch :one
UPDATE project_environment_clone_postgres_verification_attempts v SET state='compared',window_opened_at=sqlc.arg(window_opened_at)::timestamptz,
 target_database_oid=sqlc.arg(target_database_oid)::bigint,fingerprint=sqlc.arg(fingerprint)::text,ciphertext=sqlc.arg(ciphertext)::bytea,
 ciphertext_sha256=sqlc.arg(ciphertext_sha256)::text,compared_at=clock_timestamp()
WHERE v.operation_id=sqlc.arg(operation_id)::uuid AND v.source_database_id=sqlc.arg(source_database_id)::uuid AND v.database_oid=sqlc.arg(database_oid)::bigint AND v.verification_id=sqlc.arg(verification_id)::uuid AND v.state='verifying'
 AND EXISTS(SELECT 1 FROM project_environment_clone_operations o WHERE o.id=v.operation_id AND o.status='capturing' AND o.revision=sqlc.arg(expected_revision)::bigint
 AND o.lease_token::text=sqlc.arg(worker_token)::text AND o.lease_until>clock_timestamp()) RETURNING v.*;

-- name: RecordProjectEnvironmentClonePostgresVerificationAttemptClosure :one
UPDATE project_environment_clone_postgres_verification_attempts v SET state='verified',native_closed_at=sqlc.arg(native_closed_at)::timestamptz,verified_at=clock_timestamp()
WHERE v.operation_id=sqlc.arg(operation_id)::uuid AND v.source_database_id=sqlc.arg(source_database_id)::uuid AND v.database_oid=sqlc.arg(database_oid)::bigint AND v.verification_id=sqlc.arg(verification_id)::uuid AND v.state='compared'
 AND EXISTS(SELECT 1 FROM project_environment_clone_operations o WHERE o.id=v.operation_id AND o.status='capturing' AND o.revision=sqlc.arg(expected_revision)::bigint
 AND o.lease_token::text=sqlc.arg(worker_token)::text AND o.lease_until>clock_timestamp()) RETURNING v.*;

-- name: RecordProjectEnvironmentClonePostgresVerificationAttemptFailure :one
UPDATE project_environment_clone_postgres_verification_attempts v SET state='failed',window_opened_at=sqlc.arg(window_opened_at)::timestamptz,
 target_database_oid=sqlc.arg(target_database_oid)::bigint,native_closed_at=sqlc.arg(native_closed_at)::timestamptz,failed_at=clock_timestamp()
WHERE v.operation_id=sqlc.arg(operation_id)::uuid AND v.source_database_id=sqlc.arg(source_database_id)::uuid AND v.database_oid=sqlc.arg(database_oid)::bigint AND v.verification_id=sqlc.arg(verification_id)::uuid AND v.state='verifying'
 AND EXISTS(SELECT 1 FROM project_environment_clone_operations o WHERE o.id=v.operation_id AND o.status='capturing' AND o.revision=sqlc.arg(expected_revision)::bigint
 AND o.lease_token::text=sqlc.arg(worker_token)::text AND o.lease_until>clock_timestamp()) RETURNING v.*;

-- name: ReadProjectEnvironmentClonePostgresVerificationReadBudget :one
SELECT * FROM project_environment_clone_postgres_verification_read_budgets WHERE operation_id=$1 AND source_database_id=$2 AND database_oid=$3 FOR UPDATE;

-- name: ReadProjectEnvironmentClonePostgresVerificationReadAllocations :many
SELECT * FROM project_environment_clone_postgres_verification_read_debits WHERE operation_id=$1 AND source_database_id=$2 AND database_oid=$3 ORDER BY attempt FOR UPDATE;

-- name: CountProjectEnvironmentClonePostgresVerificationReadBudgets :one
SELECT count(*)::bigint AS count,coalesce(sum(read_bytes),0)::bigint AS bytes FROM project_environment_clone_postgres_verification_read_budgets WHERE account_id=$1;

-- name: InsertProjectEnvironmentClonePostgresVerificationReadBudget :one
INSERT INTO project_environment_clone_postgres_verification_read_budgets(operation_id,source_database_id,database_oid,original_verification_id,account_id,project_id,read_bytes,sort_memory_bytes,sort_disk_bytes)
SELECT sqlc.arg(operation_id)::uuid,sqlc.arg(source_database_id)::uuid,sqlc.arg(database_oid)::bigint,sqlc.arg(original_verification_id)::uuid,sqlc.arg(account_id)::uuid,sqlc.arg(project_id)::uuid,
 sqlc.arg(read_bytes)::bigint,sqlc.arg(sort_memory_bytes)::bigint,sqlc.arg(sort_disk_bytes)::bigint
WHERE EXISTS(SELECT 1 FROM project_environment_clone_operations o WHERE o.id=sqlc.arg(operation_id)::uuid AND o.status='capturing' AND o.account_id=sqlc.arg(account_id)::uuid AND o.project_id=sqlc.arg(project_id)::uuid
 AND o.revision=sqlc.arg(expected_revision)::bigint AND o.lease_token::text=sqlc.arg(worker_token)::text AND o.lease_until>clock_timestamp()) RETURNING *;

-- name: InsertProjectEnvironmentClonePostgresVerificationReadAllocation :one
INSERT INTO project_environment_clone_postgres_verification_read_debits(operation_id,source_database_id,database_oid,original_verification_id,attempt,verification_id,retry_attempt,retry_verification_id,read_bytes,sort_memory_bytes,sort_disk_bytes)
SELECT sqlc.arg(operation_id)::uuid,sqlc.arg(source_database_id)::uuid,sqlc.arg(database_oid)::bigint,sqlc.arg(original_verification_id)::uuid,sqlc.arg(attempt)::smallint,sqlc.arg(verification_id)::uuid,
 sqlc.narg(retry_attempt)::smallint,sqlc.narg(retry_verification_id)::uuid,sqlc.arg(read_bytes)::bigint,sqlc.arg(sort_memory_bytes)::bigint,sqlc.arg(sort_disk_bytes)::bigint
WHERE EXISTS(SELECT 1 FROM project_environment_clone_operations o WHERE o.id=sqlc.arg(operation_id)::uuid AND o.status='capturing'
 AND o.revision=sqlc.arg(expected_revision)::bigint AND o.lease_token::text=sqlc.arg(worker_token)::text AND o.lease_until>clock_timestamp()) RETURNING *;

-- name: CountProjectEnvironmentClonePostgresContents :one
SELECT count(*)::bigint AS count,coalesce(sum(reserved_bytes),0)::bigint AS bytes FROM project_environment_clone_postgres_contents WHERE account_id=$1;

-- name: InsertProjectEnvironmentClonePostgresContents :one
INSERT INTO project_environment_clone_postgres_contents(operation_id,source_database_id,database_oid,account_id,project_id,owner_id,scope,
 inventory_fingerprint,inventory_ciphertext_sha256,archive_owner_id,archive_reservation_sha256,reader_owner_id,reader_identity_sha256,key_id,reserved_bytes)
SELECT sqlc.arg(operation_id)::uuid,sqlc.arg(source_database_id)::uuid,sqlc.arg(database_oid)::bigint,sqlc.arg(account_id)::uuid,sqlc.arg(project_id)::uuid,sqlc.arg(owner_id)::uuid,sqlc.arg(scope)::jsonb,
 sqlc.arg(inventory_fingerprint)::text,sqlc.arg(inventory_ciphertext_sha256)::text,sqlc.arg(archive_owner_id)::uuid,sqlc.arg(archive_reservation_sha256)::text,
 sqlc.arg(reader_owner_id)::uuid,sqlc.arg(reader_identity_sha256)::text,sqlc.arg(key_id)::text,sqlc.arg(reserved_bytes)::bigint
WHERE EXISTS(SELECT 1 FROM project_environment_clone_operations o WHERE o.id=sqlc.arg(operation_id)::uuid
 AND o.account_id=sqlc.arg(account_id)::uuid AND o.project_id=sqlc.arg(project_id)::uuid AND o.status='capturing'
 AND o.revision=sqlc.arg(expected_revision)::bigint AND o.lease_token::text=sqlc.arg(worker_token)::text AND o.lease_until>clock_timestamp()) RETURNING *;

-- name: RecordProjectEnvironmentClonePostgresContents :one
UPDATE project_environment_clone_postgres_contents c SET state='captured',fingerprint=sqlc.arg(fingerprint)::text,
 ciphertext=sqlc.arg(ciphertext)::bytea,ciphertext_sha256=sqlc.arg(ciphertext_sha256)::text,captured_at=clock_timestamp()
WHERE c.operation_id=sqlc.arg(operation_id)::uuid AND c.source_database_id=sqlc.arg(source_database_id)::uuid AND c.database_oid=sqlc.arg(database_oid)::bigint AND c.state='reserved'
 AND EXISTS(SELECT 1 FROM project_environment_clone_operations o WHERE o.id=c.operation_id AND o.status='capturing'
 AND o.revision=sqlc.arg(expected_revision)::bigint AND o.lease_token::text=sqlc.arg(worker_token)::text AND o.lease_until>clock_timestamp()) RETURNING c.*;

-- name: InsertProjectEnvironmentClonePostgresInventory :one
INSERT INTO project_environment_clone_postgres_inventories(operation_id,source_database_id,account_id,project_id,capture_database_id,scope,fingerprint,key_id,ciphertext,ciphertext_sha256)
SELECT sqlc.arg(operation_id)::uuid,sqlc.arg(source_database_id)::uuid,sqlc.arg(account_id)::uuid,sqlc.arg(project_id)::uuid,
    sqlc.arg(capture_database_id)::uuid,sqlc.arg(scope)::jsonb,sqlc.arg(fingerprint)::text,sqlc.arg(key_id)::text,
    sqlc.arg(ciphertext)::bytea,sqlc.arg(ciphertext_sha256)::text
WHERE EXISTS(SELECT 1 FROM project_environment_clone_operations o WHERE o.id=sqlc.arg(operation_id)::uuid
    AND o.account_id=sqlc.arg(account_id)::uuid AND o.project_id=sqlc.arg(project_id)::uuid AND o.status='capturing'
    AND o.revision=sqlc.arg(expected_revision)::bigint AND o.lease_token::text=sqlc.arg(worker_token)::text AND o.lease_until>clock_timestamp())
RETURNING *;

-- name: InsertProjectEnvironmentClonePostgresCopyTarget :one
INSERT INTO project_environment_clone_postgres_copy_targets(operation_id,source_database_id,account_id,capture_database_id,target_database_id)
VALUES($1,$2,$3,$4,$5) RETURNING *;

-- name: ClaimProjectEnvironmentClonePostgresCopyTargetRequest :one
UPDATE project_environment_clone_postgres_copy_targets c SET state='requested',request_started_at=clock_timestamp(),updated_at=clock_timestamp()
WHERE c.operation_id=sqlc.arg(operation_id)::uuid AND c.source_database_id=sqlc.arg(source_database_id)::uuid AND c.state='reserved'
    AND EXISTS(SELECT 1 FROM project_environment_clone_operations o WHERE o.id=c.operation_id AND o.status='capturing'
        AND o.revision=sqlc.arg(expected_revision)::bigint AND o.lease_token::text=sqlc.arg(worker_token)::text AND o.lease_until>clock_timestamp()) RETURNING c.*;

-- name: PinProjectEnvironmentClonePostgresCopyTargetDatabase :one
UPDATE managed_postgres_databases d SET provider_resource_id=sqlc.arg(provider_resource_id)::text,updated_at=clock_timestamp()
FROM project_environment_clone_postgres_copy_targets c,project_environment_clone_operations o
WHERE c.operation_id=sqlc.arg(operation_id)::uuid AND c.source_database_id=sqlc.arg(source_database_id)::uuid AND c.state IN ('requested','preparing','prepared')
    AND d.id=c.target_database_id AND d.account_id=c.account_id AND d.environment_clone_operation_id=c.operation_id AND d.restore_source_database_id=c.source_database_id
    AND d.clone_resource_role='target' AND d.state='provisioning' AND d.desired_generation=1 AND d.observed_generation=0 AND d.data_resource_id IS NULL
    AND d.lease_token IS NULL AND d.lease_until IS NULL AND (d.provider_resource_id IS NULL OR d.provider_resource_id=sqlc.arg(provider_resource_id)::text)
    AND o.id=c.operation_id AND o.status='capturing' AND o.revision=sqlc.arg(expected_revision)::bigint
    AND o.lease_token::text=sqlc.arg(worker_token)::text AND o.lease_until>clock_timestamp() RETURNING d.*;

-- name: RecordProjectEnvironmentClonePostgresCopyTarget :one
UPDATE project_environment_clone_postgres_copy_targets c SET state=CASE WHEN sqlc.arg(prepared)::boolean THEN 'prepared' ELSE 'preparing' END,
    provider_resource_id=sqlc.arg(provider_resource_id)::text,provider_created_at=sqlc.arg(provider_created_at)::timestamptz,
    observed_at=clock_timestamp(),prepared_at=CASE WHEN sqlc.arg(prepared)::boolean THEN coalesce(c.prepared_at,clock_timestamp()) ELSE NULL END,updated_at=clock_timestamp()
WHERE c.operation_id=sqlc.arg(operation_id)::uuid AND c.source_database_id=sqlc.arg(source_database_id)::uuid AND c.state IN ('requested','preparing','prepared')
    AND (c.state<>'prepared' OR sqlc.arg(prepared)::boolean) AND sqlc.arg(provider_created_at)::timestamptz<=clock_timestamp()
    AND (c.provider_resource_id IS NULL OR (c.provider_resource_id=sqlc.arg(provider_resource_id)::text AND c.provider_created_at=sqlc.arg(provider_created_at)::timestamptz))
    AND EXISTS(SELECT 1 FROM project_environment_clone_operations o WHERE o.id=c.operation_id AND o.status='capturing'
        AND o.revision=sqlc.arg(expected_revision)::bigint AND o.lease_token::text=sqlc.arg(worker_token)::text AND o.lease_until>clock_timestamp()) RETURNING c.*;

-- name: RetireUndispatchedProjectEnvironmentClonePostgresCopyTargetDatabase :one
UPDATE managed_postgres_databases d SET state='deleted',deleted_at=clock_timestamp(),updated_at=clock_timestamp()
FROM project_environment_clone_postgres_copy_targets c,project_environment_clone_operations o
WHERE c.operation_id=sqlc.arg(operation_id)::uuid AND c.source_database_id=sqlc.arg(source_database_id)::uuid AND c.state='reserved' AND c.request_started_at IS NULL
    AND d.id=c.target_database_id AND d.account_id=c.account_id AND d.environment_clone_operation_id=c.operation_id AND d.restore_source_database_id=c.source_database_id
    AND d.clone_resource_role='target' AND d.state='provisioning' AND d.desired_generation=1 AND d.observed_generation=0
    AND d.provider_resource_id IS NULL AND d.data_resource_id IS NULL AND d.lease_token IS NULL AND d.lease_until IS NULL
    AND NOT EXISTS(SELECT 1 FROM managed_postgres_bindings b WHERE b.database_id=d.id AND b.state<>'deleted')
    AND NOT EXISTS(SELECT 1 FROM managed_postgres_databases child WHERE child.restore_source_database_id=d.id AND child.state<>'deleted')
    AND o.id=c.operation_id AND o.status='compensating' AND o.revision=sqlc.arg(expected_revision)::bigint
    AND o.lease_token::text=sqlc.arg(worker_token)::text AND o.lease_until>clock_timestamp() RETURNING d.*;

-- name: RetireUndispatchedProjectEnvironmentClonePostgresCopyTarget :one
UPDATE project_environment_clone_postgres_copy_targets c SET state='retired',retired_at=clock_timestamp(),updated_at=clock_timestamp()
WHERE c.operation_id=sqlc.arg(operation_id)::uuid AND c.source_database_id=sqlc.arg(source_database_id)::uuid AND c.state='reserved' AND c.request_started_at IS NULL
    AND EXISTS(SELECT 1 FROM managed_postgres_databases d WHERE d.id=c.target_database_id AND d.state='deleted' AND d.deleted_at IS NOT NULL AND d.provider_resource_id IS NULL)
    AND EXISTS(SELECT 1 FROM project_environment_clone_operations o WHERE o.id=c.operation_id AND o.status='compensating'
        AND o.revision=sqlc.arg(expected_revision)::bigint AND o.lease_token::text=sqlc.arg(worker_token)::text AND o.lease_until>clock_timestamp()) RETURNING c.*;


-- name: BeginProjectEnvironmentClonePostgresCopyTargetCleanup :one
UPDATE project_environment_clone_postgres_copy_targets c SET state='deleting',deletion_started_at=coalesce(c.deletion_started_at,clock_timestamp()),updated_at=clock_timestamp()
WHERE c.operation_id=sqlc.arg(operation_id)::uuid AND c.source_database_id=sqlc.arg(source_database_id)::uuid AND c.state IN ('requested','preparing','prepared','deleting') AND c.request_started_at IS NOT NULL
    AND EXISTS(SELECT 1 FROM project_environment_clone_operations o WHERE o.id=c.operation_id AND o.status='compensating'
        AND o.revision=sqlc.arg(expected_revision)::bigint AND o.lease_token::text=sqlc.arg(worker_token)::text AND o.lease_until>clock_timestamp()) RETURNING c.*;

-- name: PinProjectEnvironmentClonePostgresCopyTargetCleanupDatabase :one
UPDATE managed_postgres_databases d SET provider_resource_id=sqlc.arg(provider_resource_id)::text,updated_at=clock_timestamp()
FROM project_environment_clone_postgres_copy_targets c,project_environment_clone_operations o
WHERE c.operation_id=sqlc.arg(operation_id)::uuid AND c.source_database_id=sqlc.arg(source_database_id)::uuid AND c.state='deleting' AND c.request_started_at IS NOT NULL
    AND d.id=c.target_database_id AND d.account_id=c.account_id AND d.environment_clone_operation_id=c.operation_id AND d.restore_source_database_id=c.source_database_id
    AND d.clone_resource_role='target' AND d.state='provisioning' AND d.desired_generation=1 AND d.observed_generation=0 AND d.data_resource_id IS NULL
    AND d.lease_token IS NULL AND d.lease_until IS NULL AND (d.provider_resource_id IS NULL OR d.provider_resource_id=sqlc.arg(provider_resource_id)::text)
    AND o.id=c.operation_id AND o.status='compensating' AND o.revision=sqlc.arg(expected_revision)::bigint
    AND o.lease_token::text=sqlc.arg(worker_token)::text AND o.lease_until>clock_timestamp() RETURNING d.*;

-- name: RecordProjectEnvironmentClonePostgresCopyTargetCleanupIdentity :one
UPDATE project_environment_clone_postgres_copy_targets c SET provider_resource_id=sqlc.arg(provider_resource_id)::text,provider_created_at=sqlc.arg(provider_created_at)::timestamptz,
    observed_at=coalesce(c.observed_at,clock_timestamp()),updated_at=clock_timestamp()
WHERE c.operation_id=sqlc.arg(operation_id)::uuid AND c.source_database_id=sqlc.arg(source_database_id)::uuid AND c.state='deleting' AND c.request_started_at IS NOT NULL
    AND sqlc.arg(provider_created_at)::timestamptz<=clock_timestamp()
    AND (c.provider_resource_id IS NULL OR (c.provider_resource_id=sqlc.arg(provider_resource_id)::text AND c.provider_created_at=sqlc.arg(provider_created_at)::timestamptz))
    AND EXISTS(SELECT 1 FROM project_environment_clone_operations o WHERE o.id=c.operation_id AND o.status='compensating'
        AND o.revision=sqlc.arg(expected_revision)::bigint AND o.lease_token::text=sqlc.arg(worker_token)::text AND o.lease_until>clock_timestamp()) RETURNING c.*;

-- name: RetireProjectEnvironmentClonePostgresCopyTargetDatabase :one
UPDATE managed_postgres_databases d SET state='deleted',deleted_at=clock_timestamp(),updated_at=clock_timestamp()
FROM project_environment_clone_postgres_copy_targets c,project_environment_clone_operations o
WHERE c.operation_id=sqlc.arg(operation_id)::uuid AND c.source_database_id=sqlc.arg(source_database_id)::uuid AND c.state='deleting' AND c.request_started_at IS NOT NULL
    AND c.provider_resource_id IS NOT NULL AND c.provider_created_at IS NOT NULL AND d.provider_resource_id=c.provider_resource_id
    AND d.id=c.target_database_id AND d.account_id=c.account_id AND d.environment_clone_operation_id=c.operation_id AND d.restore_source_database_id=c.source_database_id
    AND d.clone_resource_role='target' AND d.state='provisioning' AND d.desired_generation=1 AND d.observed_generation=0 AND d.data_resource_id IS NULL
    AND d.lease_token IS NULL AND d.lease_until IS NULL
    AND NOT EXISTS(SELECT 1 FROM managed_postgres_bindings b WHERE b.database_id=d.id AND b.state<>'deleted')
    AND NOT EXISTS(SELECT 1 FROM managed_postgres_databases child WHERE child.restore_source_database_id=d.id AND child.state<>'deleted')
    AND o.id=c.operation_id AND o.status='compensating' AND o.revision=sqlc.arg(expected_revision)::bigint
    AND o.lease_token::text=sqlc.arg(worker_token)::text AND o.lease_until>clock_timestamp() RETURNING d.*;

-- name: FinishProjectEnvironmentClonePostgresCopyTargetCleanup :one
UPDATE project_environment_clone_postgres_copy_targets c SET state='retired',deletion_observed_at=statement_timestamp(),retired_at=statement_timestamp(),updated_at=statement_timestamp()
WHERE c.operation_id=sqlc.arg(operation_id)::uuid AND c.source_database_id=sqlc.arg(source_database_id)::uuid AND c.state='deleting' AND c.request_started_at IS NOT NULL
    AND c.provider_resource_id=sqlc.arg(provider_resource_id)::text AND c.provider_created_at=sqlc.arg(provider_created_at)::timestamptz
    AND EXISTS(SELECT 1 FROM managed_postgres_databases d WHERE d.id=c.target_database_id AND d.state='deleted' AND d.deleted_at IS NOT NULL AND d.provider_resource_id=c.provider_resource_id)
    AND EXISTS(SELECT 1 FROM project_environment_clone_operations o WHERE o.id=c.operation_id AND o.status='compensating'
        AND o.revision=sqlc.arg(expected_revision)::bigint AND o.lease_token::text=sqlc.arg(worker_token)::text AND o.lease_until>clock_timestamp()) RETURNING c.*;


-- name: ReadProjectEnvironmentClonePostgresCopyReader :one
SELECT * FROM project_environment_clone_postgres_copy_readers WHERE operation_id=$1 AND source_database_id=$2 FOR UPDATE;

-- name: CountProjectEnvironmentClonePostgresCopyReaders :one
SELECT count(*) FROM project_environment_clone_postgres_copy_readers WHERE account_id=$1 AND state<>'retired';

-- name: InsertProjectEnvironmentClonePostgresCopyReader :one
INSERT INTO project_environment_clone_postgres_copy_readers(operation_id,source_database_id,account_id,project_id,capture_database_id,scope,owner_id)
SELECT sqlc.arg(operation_id)::uuid,sqlc.arg(source_database_id)::uuid,sqlc.arg(account_id)::uuid,sqlc.arg(project_id)::uuid,
    sqlc.arg(capture_database_id)::uuid,sqlc.arg(scope)::jsonb,sqlc.arg(owner_id)::uuid
WHERE EXISTS(SELECT 1 FROM project_environment_clone_operations o WHERE o.id=sqlc.arg(operation_id)::uuid
    AND o.account_id=sqlc.arg(account_id)::uuid AND o.project_id=sqlc.arg(project_id)::uuid AND o.status='capturing'
    AND o.revision=sqlc.arg(expected_revision)::bigint AND o.lease_token::text=sqlc.arg(worker_token)::text AND o.lease_until>clock_timestamp()) RETURNING *;

-- name: ClaimProjectEnvironmentClonePostgresCopyReaderRequest :one
UPDATE project_environment_clone_postgres_copy_readers c SET state='requested',request_started_at=clock_timestamp(),updated_at=clock_timestamp()
WHERE c.operation_id=sqlc.arg(operation_id)::uuid AND c.source_database_id=sqlc.arg(source_database_id)::uuid AND c.state='reserved'
    AND EXISTS(SELECT 1 FROM project_environment_clone_operations o WHERE o.id=c.operation_id AND o.account_id=c.account_id AND o.project_id=c.project_id
        AND o.status=sqlc.arg(expected_status)::text AND o.revision=sqlc.arg(expected_revision)::bigint
        AND o.lease_token::text=sqlc.arg(worker_token)::text AND o.lease_until>clock_timestamp()) RETURNING c.*;

-- name: RecordProjectEnvironmentClonePostgresCopyReader :one
UPDATE project_environment_clone_postgres_copy_readers c SET endpoint_id=sqlc.arg(endpoint_id)::text,endpoint_created_at=sqlc.arg(endpoint_created_at)::timestamptz,
    observed_at=clock_timestamp(),state=CASE WHEN c.state='deleting' THEN c.state ELSE 'observed' END,
    available=CASE WHEN c.state='deleting' THEN false ELSE sqlc.arg(available)::boolean END,updated_at=clock_timestamp()
WHERE c.operation_id=sqlc.arg(operation_id)::uuid AND c.source_database_id=sqlc.arg(source_database_id)::uuid AND c.state IN ('requested','observed','deleting') AND c.request_started_at IS NOT NULL
    AND sqlc.arg(endpoint_created_at)::timestamptz<=clock_timestamp()
    AND (c.endpoint_id IS NULL OR (c.endpoint_id=sqlc.arg(endpoint_id)::text AND c.endpoint_created_at=sqlc.arg(endpoint_created_at)::timestamptz))
    AND EXISTS(SELECT 1 FROM project_environment_clone_operations o WHERE o.id=c.operation_id AND o.account_id=c.account_id AND o.project_id=c.project_id
        AND o.status=sqlc.arg(expected_status)::text AND o.revision=sqlc.arg(expected_revision)::bigint
        AND o.lease_token::text=sqlc.arg(worker_token)::text AND o.lease_until>clock_timestamp()) RETURNING c.*;

-- name: BeginProjectEnvironmentClonePostgresCopyReaderCleanup :one
UPDATE project_environment_clone_postgres_copy_readers c SET state='deleting',available=false,cleanup_requested_at=clock_timestamp(),updated_at=clock_timestamp()
WHERE c.operation_id=sqlc.arg(operation_id)::uuid AND c.source_database_id=sqlc.arg(source_database_id)::uuid AND c.state NOT IN ('deleting','retired')
    AND EXISTS(SELECT 1 FROM project_environment_clone_operations o WHERE o.id=c.operation_id AND o.account_id=c.account_id AND o.project_id=c.project_id
        AND o.status=sqlc.arg(expected_status)::text AND o.revision=sqlc.arg(expected_revision)::bigint
        AND o.lease_token::text=sqlc.arg(worker_token)::text AND o.lease_until>clock_timestamp()) RETURNING c.*;

-- name: ClaimProjectEnvironmentClonePostgresCopyReaderCleanup :one
UPDATE project_environment_clone_postgres_copy_readers c SET cleanup_dispatched_at=clock_timestamp(),updated_at=clock_timestamp()
WHERE c.operation_id=sqlc.arg(operation_id)::uuid AND c.source_database_id=sqlc.arg(source_database_id)::uuid AND c.state='deleting' AND c.endpoint_id IS NOT NULL AND c.cleanup_dispatched_at IS NULL
    AND EXISTS(SELECT 1 FROM project_environment_clone_operations o WHERE o.id=c.operation_id AND o.account_id=c.account_id AND o.project_id=c.project_id
        AND o.status=sqlc.arg(expected_status)::text AND o.revision=sqlc.arg(expected_revision)::bigint
        AND o.lease_token::text=sqlc.arg(worker_token)::text AND o.lease_until>clock_timestamp()) RETURNING c.*;

-- name: RecordProjectEnvironmentClonePostgresCopyReaderDeletionOperations :one
UPDATE project_environment_clone_postgres_copy_readers c SET delete_operation_ids=sqlc.arg(delete_operation_ids)::jsonb,
    capture_operation_ids=sqlc.arg(capture_operation_ids)::jsonb,updated_at=clock_timestamp()
WHERE c.operation_id=sqlc.arg(operation_id)::uuid AND c.source_database_id=sqlc.arg(source_database_id)::uuid AND c.state='deleting' AND c.endpoint_id=sqlc.arg(endpoint_id)::text AND c.endpoint_created_at=sqlc.arg(endpoint_created_at)::timestamptz
    AND (c.delete_operation_ids='[]' OR c.delete_operation_ids=sqlc.arg(delete_operation_ids)::jsonb)
    AND (c.capture_operation_ids='[]' OR c.capture_operation_ids=sqlc.arg(capture_operation_ids)::jsonb)
    AND EXISTS(SELECT 1 FROM project_environment_clone_operations o WHERE o.id=c.operation_id AND o.account_id=c.account_id AND o.project_id=c.project_id
        AND o.status=sqlc.arg(expected_status)::text AND o.revision=sqlc.arg(expected_revision)::bigint
        AND o.lease_token::text=sqlc.arg(worker_token)::text AND o.lease_until>clock_timestamp()) RETURNING c.*;

-- name: FinishProjectEnvironmentClonePostgresCopyReaderCleanup :one
UPDATE project_environment_clone_postgres_copy_readers c SET state='retired',available=false,retired_at=clock_timestamp(),updated_at=clock_timestamp()
WHERE c.operation_id=sqlc.arg(operation_id)::uuid AND c.source_database_id=sqlc.arg(source_database_id)::uuid AND c.state='deleting'
    AND EXISTS(SELECT 1 FROM project_environment_clone_operations o WHERE o.id=c.operation_id AND o.account_id=c.account_id AND o.project_id=c.project_id
        AND o.status=sqlc.arg(expected_status)::text AND o.revision=sqlc.arg(expected_revision)::bigint
        AND o.lease_token::text=sqlc.arg(worker_token)::text AND o.lease_until>clock_timestamp()) RETURNING c.*;

-- name: ReadProjectEnvironmentClonePostgresArchive :one
SELECT * FROM project_environment_clone_postgres_archives WHERE operation_id=$1 AND source_database_id=$2 AND database_oid=$3 FOR UPDATE;

-- name: CountProjectEnvironmentClonePostgresArchives :one
SELECT count(*)::bigint AS count,COALESCE(sum(reserved_bytes),0)::bigint AS bytes FROM project_environment_clone_postgres_archives WHERE account_id=$1;

-- name: InsertProjectEnvironmentClonePostgresArchive :one
INSERT INTO project_environment_clone_postgres_archives(operation_id,source_database_id,database_oid,account_id,project_id,owner_id,scope,inventory_fingerprint,key_id,storage_id,storage_fingerprint,storage_key,reserved_bytes)
SELECT sqlc.arg(operation_id)::uuid,sqlc.arg(source_database_id)::uuid,sqlc.arg(database_oid)::bigint,sqlc.arg(account_id)::uuid,sqlc.arg(project_id)::uuid,
    sqlc.arg(owner_id)::uuid,sqlc.arg(scope)::jsonb,sqlc.arg(inventory_fingerprint)::text,sqlc.arg(key_id)::text,sqlc.arg(storage_id)::text,sqlc.arg(storage_fingerprint)::text,sqlc.arg(storage_key)::text,sqlc.arg(reserved_bytes)::bigint
WHERE EXISTS(SELECT 1 FROM project_environment_clone_operations o WHERE o.id=sqlc.arg(operation_id)::uuid
    AND o.account_id=sqlc.arg(account_id)::uuid AND o.project_id=sqlc.arg(project_id)::uuid AND o.status='capturing'
    AND o.revision=sqlc.arg(expected_revision)::bigint AND o.lease_token::text=sqlc.arg(worker_token)::text AND o.lease_until>clock_timestamp()) RETURNING *;

-- name: ClaimProjectEnvironmentClonePostgresArchiveUpload :one
UPDATE project_environment_clone_postgres_archives a SET state='uploading',upload_started_at=clock_timestamp()
WHERE a.operation_id=sqlc.arg(operation_id)::uuid AND a.source_database_id=sqlc.arg(source_database_id)::uuid AND a.database_oid=sqlc.arg(database_oid)::bigint AND a.state='reserved'
    AND EXISTS(SELECT 1 FROM project_environment_clone_operations o WHERE o.id=a.operation_id AND o.account_id=a.account_id AND o.project_id=a.project_id AND o.status='capturing'
        AND o.revision=sqlc.arg(expected_revision)::bigint AND o.lease_token::text=sqlc.arg(worker_token)::text AND o.lease_until>clock_timestamp()) RETURNING a.*;

-- name: RecordProjectEnvironmentClonePostgresArchive :one
UPDATE project_environment_clone_postgres_archives a SET state='retained',retained_at=clock_timestamp(),
    plaintext_bytes=sqlc.arg(plaintext_bytes)::bigint,ciphertext_bytes=sqlc.arg(ciphertext_bytes)::bigint,ciphertext_sha256=sqlc.arg(ciphertext_sha256)::text
WHERE a.operation_id=sqlc.arg(operation_id)::uuid AND a.source_database_id=sqlc.arg(source_database_id)::uuid AND a.database_oid=sqlc.arg(database_oid)::bigint AND a.state='uploading'
    AND sqlc.arg(ciphertext_bytes)::bigint<=a.reserved_bytes
    AND EXISTS(SELECT 1 FROM project_environment_clone_operations o WHERE o.id=a.operation_id AND o.account_id=a.account_id AND o.project_id=a.project_id AND o.status='capturing'
        AND o.revision=sqlc.arg(expected_revision)::bigint AND o.lease_token::text=sqlc.arg(worker_token)::text AND o.lease_until>clock_timestamp()) RETURNING a.*;

-- name: ReadProjectEnvironmentClonePostgresImport :one
SELECT * FROM project_environment_clone_postgres_imports WHERE operation_id=$1 AND source_database_id=$2 AND database_oid=$3 FOR UPDATE;

-- name: InsertProjectEnvironmentClonePostgresImport :one
INSERT INTO project_environment_clone_postgres_imports(operation_id,source_database_id,database_oid,account_id,project_id,import_id,archive_owner_id,archive_ciphertext_sha256,target_database_id,target_provider_resource_id,target_provider_created_at,target_fingerprint,database_sql_pins_ciphertext_sha256,database_plan_ciphertext_sha256,archive_reservation_sha256)
SELECT sqlc.arg(operation_id)::uuid,sqlc.arg(source_database_id)::uuid,sqlc.arg(database_oid)::bigint,sqlc.arg(account_id)::uuid,sqlc.arg(project_id)::uuid,
    sqlc.arg(import_id)::uuid,sqlc.arg(archive_owner_id)::uuid,sqlc.arg(archive_ciphertext_sha256)::text,sqlc.arg(target_database_id)::uuid,
    sqlc.arg(target_provider_resource_id)::text,sqlc.arg(target_provider_created_at)::timestamptz,sqlc.arg(target_fingerprint)::text,
    sqlc.narg(database_sql_pins_ciphertext_sha256)::text,sqlc.narg(database_plan_ciphertext_sha256)::text,sqlc.narg(archive_reservation_sha256)::text
WHERE EXISTS(SELECT 1 FROM project_environment_clone_operations o WHERE o.id=sqlc.arg(operation_id)::uuid
    AND o.account_id=sqlc.arg(account_id)::uuid AND o.project_id=sqlc.arg(project_id)::uuid AND o.status='capturing'
    AND o.revision=sqlc.arg(expected_revision)::bigint AND o.lease_token::text=sqlc.arg(worker_token)::text AND o.lease_until>clock_timestamp()) RETURNING *;

-- name: ClaimProjectEnvironmentClonePostgresImport :one
UPDATE project_environment_clone_postgres_imports i SET state='importing',import_started_at=clock_timestamp()
WHERE i.operation_id=sqlc.arg(operation_id)::uuid AND i.source_database_id=sqlc.arg(source_database_id)::uuid AND i.database_oid=sqlc.arg(database_oid)::bigint AND i.state='reserved'
    AND EXISTS(SELECT 1 FROM project_environment_clone_operations o WHERE o.id=i.operation_id AND o.account_id=i.account_id AND o.project_id=i.project_id AND o.status='capturing'
        AND o.revision=sqlc.arg(expected_revision)::bigint AND o.lease_token::text=sqlc.arg(worker_token)::text AND o.lease_until>clock_timestamp()) RETURNING i.*;

-- name: RecordProjectEnvironmentClonePostgresImportExecution :one
UPDATE project_environment_clone_postgres_imports i SET state='executed',executed_at=clock_timestamp()
WHERE i.operation_id=sqlc.arg(operation_id)::uuid AND i.source_database_id=sqlc.arg(source_database_id)::uuid AND i.database_oid=sqlc.arg(database_oid)::bigint AND i.state='importing'
    AND EXISTS(SELECT 1 FROM project_environment_clone_operations o WHERE o.id=i.operation_id AND o.account_id=i.account_id AND o.project_id=i.project_id AND o.status='capturing'
        AND o.revision=sqlc.arg(expected_revision)::bigint AND o.lease_token::text=sqlc.arg(worker_token)::text AND o.lease_until>clock_timestamp()) RETURNING i.*;
-- name: BindEnvironmentGitOpsQueue :execrows
INSERT INTO environment_gitops_queue_bindings(source_id, resource, field_path, binding_id)
VALUES (sqlc.arg(source_id)::uuid, sqlc.arg(resource)::text, sqlc.arg(field_path)::text, sqlc.arg(binding_id)::uuid)
ON CONFLICT (source_id, resource, field_path) DO UPDATE SET binding_id=excluded.binding_id
WHERE environment_gitops_queue_bindings.binding_id=excluded.binding_id;

-- name: LockEnvironmentGitSourceForQueueMutation :many
SELECT s.id FROM active_environment_git_sources s JOIN apps a ON a.project_id=s.project_id AND a.account_id=s.account_id
WHERE a.account_id=sqlc.arg(account_id)::uuid AND a.id=sqlc.arg(app_id)::uuid
AND s.environment_id=coalesce(sqlc.narg(environment_id)::uuid,
    (SELECT b.environment_id FROM queue_bindings b WHERE b.id=sqlc.narg(binding_id)::uuid AND b.app_id=a.id AND b.account_id=a.account_id),
    (SELECT e.id FROM project_environments e WHERE e.project_id=a.project_id AND e.account_id=a.account_id AND e.slug=sqlc.arg(environment)::text))
ORDER BY s.id FOR UPDATE OF s;

-- name: InsertEnvironmentGitRevisionApproval :exec
INSERT INTO environment_git_revision_approvals(source_id,revision_id,approved_generation,definition_digest,evidence,poll_lease_token)
VALUES(sqlc.arg(source_id)::uuid,sqlc.arg(revision_id)::uuid,sqlc.arg(approved_generation)::bigint,
  sqlc.arg(definition_digest)::text,sqlc.arg(evidence)::jsonb,sqlc.arg(poll_lease_token)::uuid)
ON CONFLICT(source_id,revision_id,approved_generation) DO NOTHING;

-- name: GetEnvironmentGitRevisionApproval :one
SELECT a.* FROM environment_git_revision_approvals a
JOIN environment_git_sources s ON s.id=a.source_id
WHERE s.account_id=sqlc.arg(account_id)::uuid AND s.id=sqlc.arg(source_id)::uuid AND a.revision_id=sqlc.arg(revision_id)::uuid
ORDER BY a.approved_generation DESC LIMIT 1;

-- name: SetEnvironmentGitApprovalContext :exec
SELECT set_config('faas.environment_git_approval_id',sqlc.arg(approval_id)::text,true);

-- name: LockEnvironmentGitSourcePoll :one
SELECT source_id FROM environment_git_source_polls
WHERE source_id=sqlc.arg(source_id)::uuid AND lease_token=sqlc.arg(lease_token)::uuid
  AND lease_until > greatest(sqlc.arg(now_at)::timestamptz,clock_timestamp()) FOR UPDATE;

-- Scoped reference reads do not expose values or fall back to another scope.
-- name: GetAppEnvironmentSecretReferences :one
SELECT environment_scoped_secret_refs(a.id,sqlc.arg(scope)::text)::jsonb AS refs FROM apps a
 WHERE a.id=sqlc.arg(app_id)::uuid AND a.account_id=sqlc.arg(account_id)::uuid;

-- name: LockEnvironmentGitSourceForSecretMutation :many
SELECT s.id FROM active_environment_git_sources s JOIN apps a ON a.project_id=s.project_id AND a.account_id=s.account_id
 JOIN project_environments e ON e.id=s.environment_id
 WHERE a.id=sqlc.arg(app_id)::uuid AND a.account_id=sqlc.arg(account_id)::uuid AND e.slug=sqlc.arg(scope)::text
 FOR UPDATE OF s;

-- name: LockAppEnvironmentSecretReferenceScope :one
SELECT e.id,e.project_id FROM apps a JOIN project_environments e ON e.project_id=a.project_id AND e.account_id=a.account_id
 WHERE a.id=sqlc.arg(app_id)::uuid AND a.account_id=sqlc.arg(account_id)::uuid AND a.status<>'deleted'
 AND e.slug=sqlc.arg(scope)::text FOR UPDATE OF a,e;

-- name: PutEnvironmentGitOpsSecretReference :exec
INSERT INTO app_environment_secret_refs(account_id,project_id,environment_id,app_id,scope,key,secret_name)
 VALUES(sqlc.arg(account_id)::uuid,sqlc.arg(project_id)::uuid,sqlc.arg(environment_id)::uuid,sqlc.arg(app_id)::uuid,
 sqlc.arg(scope)::text,sqlc.arg(key)::text,sqlc.arg(secret_name)::text)
 ON CONFLICT(app_id,environment_id,key) DO UPDATE SET secret_name=excluded.secret_name;

-- name: DeleteEnvironmentGitOpsSecretReference :exec
DELETE FROM app_environment_secret_refs WHERE account_id=sqlc.arg(account_id)::uuid AND app_id=sqlc.arg(app_id)::uuid
 AND environment_id=sqlc.arg(environment_id)::uuid AND key=sqlc.arg(key)::text;

-- name: EnvironmentSecretReferenceQuota :one
SELECT ((SELECT count(*) FROM app_envs WHERE app_id=sqlc.arg(app_id)::uuid)
 +(SELECT count(*) FROM app_environment_secret_refs WHERE app_id=sqlc.arg(app_id)::uuid))::bigint AS total,
 EXISTS(SELECT 1 FROM app_envs WHERE app_id=sqlc.arg(app_id)::uuid AND scope=sqlc.arg(scope)::text AND key=sqlc.arg(key)::text) AS variable_exists,
 EXISTS(SELECT 1 FROM app_environment_secret_refs WHERE app_id=sqlc.arg(app_id)::uuid AND environment_id=sqlc.arg(environment_id)::uuid AND key=sqlc.arg(key)::text) AS ref_exists;

-- name: LockEnvironmentGitOpsIntentApps :many
SELECT a.id FROM environment_git_sources s JOIN apps a ON a.project_id=s.project_id AND a.account_id=s.account_id
 WHERE s.id=sqlc.arg(source_id)::uuid AND a.status<>'deleted' ORDER BY a.id FOR UPDATE OF a;

-- References and plaintext variables share the app's environment-key quota.
-- name: CountAppEnvironmentIntent :one
SELECT ((SELECT count(*) FROM app_envs WHERE account_id=sqlc.arg(account_id)::uuid AND app_id=sqlc.arg(app_id)::uuid)
 + (SELECT count(*) FROM app_environment_secret_refs WHERE account_id=sqlc.arg(account_id)::uuid AND app_id=sqlc.arg(app_id)::uuid))::bigint AS count;

-- name: CountAppEnvironmentIntentInScope :one
SELECT ((SELECT count(*) FROM app_envs WHERE account_id=sqlc.arg(account_id)::uuid AND app_id=sqlc.arg(app_id)::uuid AND scope=sqlc.arg(scope)::text)
 + (SELECT count(*) FROM app_environment_secret_refs WHERE account_id=sqlc.arg(account_id)::uuid AND app_id=sqlc.arg(app_id)::uuid AND scope=sqlc.arg(scope)::text))::bigint AS count;

-- Clone locking follows source -> app -> catalog, matching reference writes.
-- name: LockProjectEnvironmentCloneGitSources :many
SELECT s.id FROM active_environment_git_sources s JOIN project_environments e ON e.id=s.environment_id
WHERE s.account_id=sqlc.arg(account_id)::uuid AND s.project_id=sqlc.arg(project_id)::uuid
 AND e.slug IN (sqlc.arg(source_slug)::text,sqlc.arg(target_slug)::text)
ORDER BY s.id FOR UPDATE OF s;

-- Counts include reference intent in the shared environment-key quota.
-- name: ProjectEnvironmentCloneQuota :many
SELECT a.slug,
 (SELECT count(*) FROM app_secrets s WHERE s.app_id=a.id)::bigint AS secret_count,
 (SELECT count(*) FROM app_secrets s WHERE s.app_id=a.id AND s.scope=(sqlc.arg(source_value_scopes)::jsonb ->> a.id::text))::bigint AS source_secrets,
 (SELECT count(*) FROM app_secrets s WHERE s.app_id=a.id AND s.scope=(sqlc.arg(source_value_scopes)::jsonb ->> a.id::text)
  AND (s.managed_postgres_binding_id IS NOT NULL OR s.managed_object_storage_credential_id IS NOT NULL))::bigint AS source_managed,
 ((SELECT count(*) FROM app_envs v WHERE v.app_id=a.id)
  +(SELECT count(*) FROM app_environment_secret_refs r WHERE r.app_id=a.id))::bigint AS env_count,
 ((SELECT count(*) FROM app_envs v WHERE v.app_id=a.id AND v.scope=(sqlc.arg(source_value_scopes)::jsonb ->> a.id::text))
  +(SELECT count(*) FROM app_environment_secret_refs r JOIN project_environments e ON e.id=r.environment_id
    WHERE r.app_id=a.id AND e.slug=sqlc.arg(source_slug)::text))::bigint AS source_env,
 (SELECT count(*) FROM app_environment_secret_ref_suppressions r WHERE r.app_id=a.id)::bigint AS suppression_count,
 (SELECT count(*) FROM app_environment_secret_ref_suppressions r JOIN project_environments e ON e.id=r.environment_id
    WHERE r.app_id=a.id AND e.slug=sqlc.arg(source_slug)::text)::bigint AS source_suppressions
FROM apps a WHERE a.account_id=sqlc.arg(account_id)::uuid AND a.project_id=sqlc.arg(project_id)::uuid AND a.status<>'deleted'
ORDER BY a.slug;

-- Values and source versions stay in app_secrets; references receive a new
-- catalog identity. Ownership and runtime evidence are deliberately absent.
-- name: CopyProjectEnvironmentSecretReferences :one
WITH copied AS (
 INSERT INTO app_environment_secret_refs(account_id,project_id,environment_id,app_id,scope,key,secret_name)
 SELECT r.account_id,r.project_id,target.id,r.app_id,target.slug,r.key,r.secret_name
 FROM app_environment_secret_refs r JOIN apps a ON a.id=r.app_id AND a.account_id=r.account_id AND a.project_id=r.project_id
 JOIN project_environments source ON source.id=r.environment_id AND source.project_id=r.project_id AND source.account_id=r.account_id
 JOIN project_environments target ON target.project_id=r.project_id AND target.account_id=r.account_id AND target.slug=sqlc.arg(target_slug)::text
 WHERE r.account_id=sqlc.arg(account_id)::uuid AND r.project_id=sqlc.arg(project_id)::uuid
  AND source.slug=sqlc.arg(source_slug)::text AND a.status<>'deleted'
 RETURNING 1
)
SELECT count(*) FROM copied;

-- A customer projection includes only names and one catalog/intent snapshot.
-- name: ReadAppEnvironmentSecretReferenceSnapshot :one
SELECT e.id AS environment_id,environment_scoped_secret_refs(a.id,e.slug)::jsonb AS refs,
 environment_scoped_secret_suppressions(a.id,e.slug)::text[] AS suppressed_keys,
 ((SELECT count(*) FROM app_envs v WHERE v.app_id=a.id)
 +(SELECT count(*) FROM app_environment_secret_refs r WHERE r.app_id=a.id))::bigint AS count
FROM apps a JOIN project_environments e ON e.project_id=a.project_id AND e.account_id=a.account_id
WHERE a.id=sqlc.arg(app_id)::uuid AND a.account_id=sqlc.arg(account_id)::uuid AND a.status<>'deleted'
 AND e.slug=sqlc.arg(scope)::text;

-- name: EnvironmentSecretReferenceSourcePresent :one
SELECT EXISTS(SELECT 1 FROM app_secrets WHERE account_id=sqlc.arg(account_id)::uuid AND app_id=sqlc.arg(app_id)::uuid
 AND scope=sqlc.arg(scope)::text AND key=sqlc.arg(key)::text) AS present;

-- Public controls check authority before source/quota checks, including a
-- no-op delete. The storage trigger still checks authority at the write.
-- name: EnvironmentSecretReferenceWriteOwned :one
SELECT EXISTS(SELECT 1 FROM environment_git_sources s JOIN environment_gitops_resources r ON r.source_id=s.id
 JOIN environment_managed_fields f ON f.source_id=s.id AND f.resource=r.logical_name
 WHERE s.account_id=sqlc.arg(account_id)::uuid AND s.environment_id=sqlc.arg(environment_id)::uuid
 AND r.app_id=sqlc.arg(app_id)::uuid AND s.mode='enforce' AND f.field_path='secret_refs/'||sqlc.arg(key)::text
 AND NOT EXISTS(SELECT 1 FROM environment_management_overrides o WHERE o.environment_id=f.environment_id
  AND o.resource=f.resource AND o.field_path=f.field_path AND o.expires_at>clock_timestamp())) AS owned;

-- Read both sides of the intent at one statement snapshot.
-- name: GetAppEnvironmentSecretIntent :one
SELECT jsonb_build_object('references',environment_scoped_secret_refs(a.id,sqlc.arg(scope)::text),
 'suppressed_keys',environment_scoped_secret_suppressions(a.id,sqlc.arg(scope)::text))::jsonb AS intent
FROM apps a WHERE a.id=sqlc.arg(app_id)::uuid AND a.account_id=sqlc.arg(account_id)::uuid AND a.status<>'deleted';

-- name: PutEnvironmentSecretReferenceSuppression :exec
INSERT INTO app_environment_secret_ref_suppressions(account_id,project_id,environment_id,app_id,scope,key)
SELECT sqlc.arg(account_id)::uuid,sqlc.arg(project_id)::uuid,sqlc.arg(environment_id)::uuid,sqlc.arg(app_id)::uuid,
 sqlc.arg(scope)::text,sqlc.arg(key)::text
WHERE NOT EXISTS(SELECT 1 FROM app_environment_secret_ref_suppressions
 WHERE app_id=sqlc.arg(app_id)::uuid AND environment_id=sqlc.arg(environment_id)::uuid AND key=sqlc.arg(key)::text)
ON CONFLICT(app_id,environment_id,key) DO NOTHING;

-- name: DeleteEnvironmentSecretReferenceSuppression :exec
DELETE FROM app_environment_secret_ref_suppressions WHERE account_id=sqlc.arg(account_id)::uuid AND app_id=sqlc.arg(app_id)::uuid
 AND environment_id=sqlc.arg(environment_id)::uuid AND key=sqlc.arg(key)::text;

-- name: CopyProjectEnvironmentSecretSuppressions :exec
INSERT INTO app_environment_secret_ref_suppressions(account_id,project_id,environment_id,app_id,scope,key)
SELECT r.account_id,r.project_id,target.id,r.app_id,target.slug,r.key
FROM app_environment_secret_ref_suppressions r JOIN apps a ON a.id=r.app_id
JOIN project_environments source ON source.id=r.environment_id
JOIN project_environments target ON target.project_id=r.project_id AND target.account_id=r.account_id AND target.slug=sqlc.arg(target_slug)::text
WHERE r.account_id=sqlc.arg(account_id)::uuid AND r.project_id=sqlc.arg(project_id)::uuid
 AND source.slug=sqlc.arg(source_slug)::text AND a.status<>'deleted';

-- name: EnvironmentWorkloadIntent :one
SELECT w.* FROM app_environment_workload_intents w
JOIN apps a ON a.id=w.app_id AND a.account_id=w.account_id AND a.status<>'deleted'
JOIN project_environments e ON e.id=w.environment_id AND e.account_id=a.account_id AND e.project_id=a.project_id
WHERE w.account_id=sqlc.arg(account_id)::uuid AND w.app_id=sqlc.arg(app_id)::uuid AND w.environment_id=sqlc.arg(environment_id)::uuid;

-- name: PutEnvironmentWorkloadIntent :one
INSERT INTO app_environment_workload_intents(account_id,app_id,environment_id,source,runtime,source_revision)
VALUES(sqlc.arg(account_id)::uuid,sqlc.arg(app_id)::uuid,sqlc.arg(environment_id)::uuid,sqlc.narg(source)::jsonb,sqlc.arg(runtime)::jsonb,nullif(sqlc.arg(source_revision)::text,''))
ON CONFLICT(app_id,environment_id) DO UPDATE SET source=excluded.source,runtime=excluded.runtime,source_revision=excluded.source_revision,updated_at=now()
RETURNING *;

-- name: EnvironmentWorkloadIntentLockSource :many
SELECT id FROM active_environment_git_sources WHERE account_id=sqlc.arg(account_id)::uuid AND environment_id=sqlc.arg(environment_id)::uuid FOR UPDATE;

-- name: EnvironmentWorkloadIntentContext :one
SELECT jsonb_build_object('manifest',a.manifest,'workload_class',a.workload_class,'environment',e.slug,'plan',c.plan)::jsonb AS context
FROM apps a JOIN project_environments e ON e.project_id=a.project_id AND e.account_id=a.account_id
JOIN accounts c ON c.id=a.account_id
WHERE a.id=sqlc.arg(app_id)::uuid AND a.account_id=sqlc.arg(account_id)::uuid AND e.id=sqlc.arg(environment_id)::uuid AND a.status<>'deleted'
FOR UPDATE OF a,c;

-- name: EnvironmentGitOpsUnqualifiedWorkloads :many
SELECT r.app_id,r.logical_name FROM environment_gitops_resources r
WHERE r.source_id=sqlc.arg(source_id)::uuid AND EXISTS(SELECT 1 FROM environment_managed_fields f
 WHERE f.source_id=r.source_id AND f.resource=r.logical_name AND (f.field_path='source' OR starts_with(f.field_path,'runtime/')));

-- name: DeleteAccountEnvironmentGitSources :exec
DELETE FROM environment_git_sources s USING accounts a
WHERE s.account_id=a.id AND a.id=sqlc.arg(account_id)::uuid AND a.status='deleted_pending';

-- name: AccountPendingDeletionEmail :one
SELECT coalesce(email::text,'')::text AS email FROM accounts WHERE id=sqlc.arg(account_id)::uuid AND status='deleted_pending' FOR UPDATE;

-- name: LockEnvironmentGitOpsCandidateApps :many
SELECT a.id FROM environment_gitops_resources r JOIN apps a ON a.id=r.app_id
JOIN environment_git_sources s ON s.id=r.source_id
WHERE s.id=sqlc.arg(source_id)::uuid AND a.account_id=s.account_id AND a.project_id=s.project_id AND a.status IN ('active','evicted_cold')
ORDER BY a.id FOR UPDATE OF a;

-- name: EnvironmentGitOpsCandidateByInput :one
SELECT id FROM deployments WHERE environment_workload_runtime->>'source_id'=sqlc.arg(source_id)::text
 AND environment_workload_runtime->>'generation'=sqlc.arg(generation)::text
 AND environment_workload_runtime->>'resource'=sqlc.arg(resource)::text
 AND environment_workload_runtime->>'plan_hash'=sqlc.arg(plan_hash)::text;

-- name: EnvironmentGitOpsImageCandidate :one
SELECT id,app_id,status,coalesce(build_id::text,'')::text AS build_id,coalesce(rootfs_path,'')::text AS rootfs_path,coalesce(rootfs_key,'')::text AS rootfs_key
FROM deployments WHERE id=sqlc.arg(deployment_id)::uuid AND environment_workload_runtime IS NOT NULL;

-- name: CreateEnvironmentWorkloadGraph :exec
INSERT INTO environment_workload_graphs(source_id,environment_id,revision_id,generation,intent_version,plan_hash,definition_digest,members,resource_ids)
VALUES(sqlc.arg(source_id)::uuid,sqlc.arg(environment_id)::uuid,sqlc.arg(revision_id)::uuid,sqlc.arg(generation)::bigint,
 sqlc.arg(intent_version)::bigint,sqlc.arg(plan_hash)::text,sqlc.arg(definition_digest)::text,sqlc.arg(members)::jsonb,sqlc.arg(resource_ids)::jsonb)
ON CONFLICT(source_id,generation,plan_hash) DO NOTHING;

-- name: EnvironmentWorkloadGraphForPreparation :one
SELECT * FROM environment_workload_graphs WHERE source_id=sqlc.arg(source_id)::uuid
 AND generation=sqlc.arg(generation)::bigint AND plan_hash=sqlc.arg(plan_hash)::text FOR UPDATE;

-- name: AdvanceEnvironmentWorkloadGraphPreparation :one
UPDATE environment_workload_graphs SET phase=sqlc.arg(phase)::text,error_code=sqlc.arg(error_code)::text,
 prepared_at=CASE WHEN sqlc.arg(phase)::text='prepared' THEN coalesce(prepared_at,now()) ELSE NULL END
WHERE id=sqlc.arg(id)::uuid AND phase<>'failed'
RETURNING *;

-- name: CreateEnvironmentWorkloadQualification :execrows
INSERT INTO environment_workload_qualification_requests(graph_id,deployment_id,app_id,resource,artifact,frozen_inputs,execution_mode)
SELECT sqlc.arg(graph_id)::uuid,d.id,d.app_id,sqlc.arg(resource)::text,environment_workload_artifact(d),d.environment_workload_runtime,
 CASE WHEN d.environment_workload_runtime->'runtime' ? 'execution_mode'
 THEN coalesce(nullif(d.environment_workload_runtime->'runtime'->>'execution_mode',''),'request')
 ELSE coalesce(nullif(d.environment_workload_runtime->'baseline'->>'execution_mode',''),'request') END
FROM deployments d WHERE d.id=sqlc.arg(deployment_id)::uuid
ON CONFLICT(graph_id,resource) DO NOTHING;

-- name: EnvironmentWorkloadQualificationsByGraph :many
SELECT * FROM environment_workload_qualification_requests WHERE graph_id=sqlc.arg(graph_id)::uuid ORDER BY resource;

-- Called after qualificationCurrentTx locks the source and its mapped apps.
-- name: EnvironmentWorkloadQualificationAppOwner :one
SELECT node_id, status FROM apps WHERE id = $1;

-- Discovery grants no execution authority. Claim rechecks the full observation
-- and cohort under source/app/request locks before issuing a new attempt.
-- name: ListEnvironmentWorkloadQualificationsForDispatch :many
SELECT q.id FROM environment_workload_qualification_requests q
JOIN environment_workload_graphs g ON g.id=q.graph_id
JOIN environment_git_sources s ON s.id=g.source_id
JOIN apps a ON a.id=q.app_id AND a.account_id=s.account_id AND a.project_id=s.project_id
JOIN accounts c ON c.id=s.account_id
WHERE s.mode='enforce' AND NOT s.suspended AND g.phase='prepared'
 AND g.generation=s.generation AND g.intent_version=s.intent_version
 AND g.revision_id=s.approved_revision_id AND g.environment_id=s.environment_id
 AND c.status='active' AND c.abuse_hold_at IS NULL AND a.status IN ('active','evicted_cold')
 AND (a.node_id IS NULL OR a.node_id=sqlc.arg(node_id)::uuid)
 AND (sqlc.arg(after_request_id)::text='' OR q.id>nullif(sqlc.arg(after_request_id)::text,'')::uuid)
 AND q.execution_mode<>'job' AND (q.phase='queued' OR q.phase='claimed' AND q.lease_until<=clock_timestamp())
 AND NOT EXISTS(SELECT 1 FROM instances i WHERE i.id=q.reserved_instance_id AND i.state NOT IN ('parked','stopped','failed'))
 AND NOT EXISTS(SELECT 1 FROM environment_qualification_executions e WHERE e.instance_id=q.reserved_instance_id AND e.retired_at IS NULL)
ORDER BY q.id LIMIT sqlc.arg(page_limit)::integer;

-- name: EnvironmentWorkloadQualificationSourceForUpdate :one
SELECT s.* FROM environment_workload_qualification_requests q
JOIN environment_workload_graphs g ON g.id=q.graph_id JOIN environment_git_sources s ON s.id=g.source_id
WHERE q.id=sqlc.arg(id)::uuid FOR UPDATE OF s;

-- name: EnvironmentWorkloadQualificationForUpdate :one
SELECT * FROM environment_workload_qualification_requests WHERE id=sqlc.arg(id)::uuid FOR UPDATE;

-- name: EnvironmentWorkloadGraphByIDForUpdate :one
SELECT * FROM environment_workload_graphs WHERE id=sqlc.arg(id)::uuid FOR UPDATE;

-- name: EnvironmentWorkloadQualificationArtifactCurrent :one
SELECT NOT EXISTS(SELECT 1 FROM jsonb_array_elements(g.members) m
 LEFT JOIN environment_workload_qualification_requests q ON q.graph_id=g.id AND q.resource=m->>'resource' AND q.deployment_id=(m->>'candidate_deployment_id')::uuid
 LEFT JOIN deployments d ON d.id=q.deployment_id WHERE m ? 'candidate_deployment_id' AND
 (q.id IS NULL OR q.artifact IS DISTINCT FROM environment_workload_artifact(d) OR q.frozen_inputs IS DISTINCT FROM d.environment_workload_runtime
 OR d.status IS DISTINCT FROM 'snapshotting' OR coalesce(d.rootfs_bytes,0)<=0)) AS current
FROM environment_workload_qualification_requests target JOIN environment_workload_graphs g ON g.id=target.graph_id WHERE target.id=sqlc.arg(id)::uuid;

-- name: EnvironmentWorkloadQualificationInputsCurrent :one
SELECT environment_workload_qualification_inputs_current(sqlc.arg(id)::uuid)::boolean;

-- name: SetEnvironmentWorkloadQualificationContext :one
SELECT set_config('gregale.gitops_qualification',sqlc.arg(token)::text,true)::text;

-- name: ClaimEnvironmentWorkloadQualification :one
UPDATE environment_workload_qualification_requests SET phase='claimed',worker_id=sqlc.arg(worker_id)::text,
 lease_token=sqlc.arg(token)::text,lease_until=clock_timestamp()+sqlc.arg(duration_us)::bigint*interval '1 microsecond',attempt=attempt+1,
 reserved_instance_id=sqlc.narg(instance_id)::uuid
WHERE id=sqlc.arg(id)::uuid AND (phase='queued' OR lease_until<=clock_timestamp())
 AND NOT EXISTS(SELECT 1 FROM instances i WHERE i.id=reserved_instance_id AND i.state NOT IN ('parked','stopped','failed'))
 AND NOT EXISTS(SELECT 1 FROM environment_qualification_executions e WHERE e.instance_id=reserved_instance_id AND e.retired_at IS NULL) RETURNING *;

-- name: RenewEnvironmentWorkloadQualification :one
UPDATE environment_workload_qualification_requests SET lease_until=greatest(lease_until,clock_timestamp()+sqlc.arg(duration_us)::bigint*interval '1 microsecond')
WHERE id=sqlc.arg(id)::uuid AND phase='claimed' AND lease_token=sqlc.arg(token)::text AND attempt=sqlc.arg(attempt)::bigint
 AND lease_until>clock_timestamp() RETURNING *;

-- name: EnvironmentQualificationAdmissionInputs :one
SELECT a.ram_mb,n.admission_ceiling_mb FROM apps a JOIN accounts c ON c.id=a.account_id
CROSS JOIN compute_nodes n WHERE a.id=sqlc.arg(app_id)::uuid AND n.id=sqlc.arg(node_id)::uuid
 AND c.status='active' AND c.abuse_hold_at IS NULL AND n.active AND n.lifecycle='active' FOR SHARE OF n;

-- name: EnvironmentQualificationInstance :one
SELECT * FROM instances WHERE id=sqlc.arg(instance_id)::uuid;

-- name: LockEnvironmentQualificationRuntimeInstance :one
SELECT * FROM instances WHERE id=sqlc.arg(id)::uuid FOR UPDATE;

-- name: PublishEnvironmentQualificationRuntime :one
UPDATE instances SET state='running',started_at=clock_timestamp(),netns=sqlc.arg(netns)::text,
 host_ip=sqlc.arg(host_ip)::text::inet,guest_uid=sqlc.arg(guest_uid)::integer
WHERE id=sqlc.arg(id)::uuid AND state='cold_booting' RETURNING *;

-- name: LockEnvironmentQualificationAccount :one
SELECT c.status='active' AND c.abuse_hold_at IS NULL AS may_deploy FROM accounts c JOIN apps a ON a.account_id=c.id
WHERE a.id=sqlc.arg(app_id)::uuid FOR UPDATE OF c;

-- name: LockEnvironmentQualificationNode :exec
SELECT pg_advisory_xact_lock(sqlc.arg(lock_class)::integer,hashtext(sqlc.arg(node_id)::text));

-- name: EnvironmentQualificationNodeUsedMB :one
SELECT coalesce(sum(ram_mb+sqlc.arg(overhead_mb)::integer),0)::bigint FROM instances
WHERE node_id=sqlc.arg(node_id)::uuid AND state IN ('waking','cold_booting','running','draining','warm');

-- name: CreateEnvironmentQualificationInstance :one
INSERT INTO instances(id,app_id,deployment_id,state,ram_mb,node_id,wake_id,started_at,mode)
VALUES(sqlc.arg(id)::uuid,sqlc.arg(app_id)::uuid,sqlc.arg(deployment_id)::uuid,'cold_booting',sqlc.arg(ram_mb)::integer,
 sqlc.arg(node_id)::uuid,sqlc.arg(wake_id)::uuid,clock_timestamp(),sqlc.arg(mode)::text) RETURNING *;

-- name: EnvironmentQualificationExecution :one
SELECT * FROM environment_qualification_executions WHERE instance_id=sqlc.arg(instance_id)::uuid;

-- name: LockEnvironmentQualificationExecution :one
SELECT * FROM environment_qualification_executions WHERE instance_id=sqlc.arg(instance_id)::uuid FOR UPDATE;

-- Discovery does not authorize cleanup. Recheck under the original request
-- and immutable frame locks before any native retirement RPC.
-- name: ListEnvironmentQualificationExecutionsForRecovery :many
SELECT e.* FROM environment_qualification_executions e
WHERE e.frame->>'node_id'=sqlc.arg(node_id)::text AND e.retired_at IS NULL
 AND (sqlc.arg(after_instance_id)::text='' OR e.instance_id>nullif(sqlc.arg(after_instance_id)::text,'')::uuid)
 AND NOT EXISTS(SELECT 1 FROM environment_workload_qualification_requests q WHERE q.id=e.request_id
  AND q.attempt=(e.frame->>'attempt')::bigint AND q.reserved_instance_id=e.instance_id
  AND q.phase='claimed' AND q.lease_until>clock_timestamp())
ORDER BY e.instance_id LIMIT sqlc.arg(page_limit)::integer;

-- Caller already holds the request (when present) and then the frame lock.
-- Use the database clock so host clock skew cannot expire a current lease.
-- name: EnvironmentQualificationExecutionRecoverable :one
SELECT e.retired_at IS NOT NULL OR NOT EXISTS(SELECT 1 FROM environment_workload_qualification_requests q WHERE q.id=e.request_id
 AND q.attempt=(e.frame->>'attempt')::bigint AND q.reserved_instance_id=e.instance_id
 AND q.phase='claimed' AND q.lease_until>clock_timestamp()) AS recoverable
FROM environment_qualification_executions e WHERE e.instance_id=sqlc.arg(instance_id)::uuid;

-- name: MarkEnvironmentQualificationDispatched :execrows
UPDATE environment_qualification_executions SET dispatch_started=true
WHERE instance_id=sqlc.arg(instance_id)::uuid AND NOT dispatch_started AND retired_at IS NULL;

-- name: SetEnvironmentQualificationCleanupContext :one
SELECT set_config('gregale.gitops_qualification_cleanup',sqlc.arg(token)::text,true)::text;

-- name: RetireEnvironmentQualificationExecution :execrows
UPDATE environment_qualification_executions SET retirement=sqlc.arg(retirement)::jsonb,retired_at=clock_timestamp()
WHERE instance_id=sqlc.arg(instance_id)::uuid AND retired_at IS NULL;

-- name: StopEnvironmentQualificationInstance :one
UPDATE instances i SET state=CASE WHEN i.state IN ('parked','stopped','failed','evicting_account_deleting') THEN i.state ELSE 'stopped' END,
terminal_at=e.retired_at FROM environment_qualification_executions e
WHERE i.id=sqlc.arg(instance_id)::uuid AND e.instance_id=i.id AND e.retired_at IS NOT NULL RETURNING i.*;

-- name: CreateEnvironmentGitOpsWorkloadCandidate :one
INSERT INTO deployments(app_id,scope,kind,image_digest,commit_sha,status,traffic_percent,traffic_percent_explicit,
 source_path,source_root,source_sha256,source_bytes,source_url,log_path,revision,environment_workload_runtime,override_entrypoint,override_cmd,override_env,override_env_secrets,override_port,
 override_healthcheck,override_liveness_probe,override_readiness_probe,override_main_depends_on,sidecars,workflows,
 full_rootfs_allow_auto,full_rootfs_override,min_instances,release_command,release_command_shell,disable_startup_cpu_boost,
 rollback_on_5xx)
VALUES(sqlc.arg(app_id)::uuid,sqlc.arg(scope)::text,sqlc.arg(kind)::text,sqlc.arg(image)::text,sqlc.arg(commit_sha)::text,'pending',0,true,
 nullif(sqlc.arg(source_path)::text,''),sqlc.arg(source_root)::text,nullif(sqlc.arg(source_sha256)::text,''),sqlc.arg(source_bytes)::bigint,nullif(sqlc.arg(source_url)::text,''),sqlc.arg(runtime)::jsonb->'source_archive'->>'log_path',
 (SELECT coalesce(max(revision),0)+1 FROM deployments WHERE app_id=sqlc.arg(app_id)::uuid),sqlc.arg(runtime)::jsonb,
 (SELECT coalesce(array_agg(value ORDER BY position),ARRAY[]::text[]) FROM jsonb_array_elements_text(coalesce(sqlc.arg(runtime)::jsonb->'deployment_inputs'->'override_entrypoint','[]'::jsonb)) WITH ORDINALITY AS elements(value,position)),
 (SELECT coalesce(array_agg(value ORDER BY position),ARRAY[]::text[]) FROM jsonb_array_elements_text(coalesce(sqlc.arg(runtime)::jsonb->'deployment_inputs'->'override_cmd','[]'::jsonb)) WITH ORDINALITY AS elements(value,position)),
 sqlc.arg(runtime)::jsonb->'deployment_inputs'->'override_env',sqlc.arg(runtime)::jsonb->'deployment_inputs'->'override_env_secrets',
 (sqlc.arg(runtime)::jsonb->'deployment_inputs'->>'override_port')::integer,
 sqlc.arg(runtime)::jsonb->'deployment_inputs'->'override_healthcheck',sqlc.arg(runtime)::jsonb->'deployment_inputs'->'override_liveness_probe',
 sqlc.arg(runtime)::jsonb->'deployment_inputs'->'override_readiness_probe',coalesce(sqlc.arg(runtime)::jsonb->'deployment_inputs'->'override_main_depends_on','[]'::jsonb),
 coalesce(sqlc.arg(runtime)::jsonb->'deployment_inputs'->'sidecars','[]'::jsonb),coalesce(sqlc.arg(runtime)::jsonb->'deployment_inputs'->'workflows','[]'::jsonb),
 coalesce((sqlc.arg(runtime)::jsonb->'deployment_inputs'->>'full_rootfs_allow_auto')::boolean,false),(sqlc.arg(runtime)::jsonb->'deployment_inputs'->>'full_rootfs_override')::boolean,
 coalesce((sqlc.arg(runtime)::jsonb->'deployment_inputs'->>'min_instances')::integer,0),
 (SELECT coalesce(array_agg(value ORDER BY position),ARRAY[]::text[]) FROM jsonb_array_elements_text(coalesce(sqlc.arg(runtime)::jsonb->'deployment_inputs'->'release_command','[]'::jsonb)) WITH ORDINALITY AS elements(value,position)),
 coalesce((sqlc.arg(runtime)::jsonb->'deployment_inputs'->>'release_command_shell')::boolean,false),
 coalesce((sqlc.arg(runtime)::jsonb->'deployment_inputs'->>'disable_startup_cpu_boost')::boolean,false),
 coalesce((sqlc.arg(runtime)::jsonb->'deployment_inputs'->>'rollback_on_5xx')::boolean,false))
RETURNING id;

-- name: CreateEnvironmentGitOpsSourceBuild :exec
WITH queued AS (
 INSERT INTO builds(id,deployment_id,kind,source_bytes,status,log_path)
 VALUES(sqlc.arg(build_id)::uuid,sqlc.arg(deployment_id)::uuid,'github',sqlc.arg(source_bytes)::bigint,'queued',sqlc.arg(log_path)::text)
 RETURNING id,deployment_id
)
UPDATE deployments d SET status='building',build_id=queued.id FROM queued
WHERE d.id=queued.deployment_id AND d.environment_workload_runtime IS NOT NULL AND d.status='pending';
-- name: CommitSourceForManagedAdmission :one
SELECT c.app_id::text, c.enabled, COALESCE(c.operation_policy,'')::text AS operation_policy,
 c.contract_version, c.allow_tenant_selection, a.platform_tenant_required
FROM commit_sources c JOIN apps a ON a.id=c.app_id AND a.account_id=c.account_id
WHERE c.account_id=sqlc.arg(account_id)::text::uuid AND c.id=sqlc.arg(source_id)::text::uuid
FOR UPDATE OF c FOR SHARE OF a;

-- name: CommitManagedSource :one
INSERT INTO commit_sources(account_id,app_id,name,operation_policy,contract_version,allow_tenant_selection)
SELECT sqlc.arg(account_id)::text::uuid,id,sqlc.arg(name)::text,sqlc.arg(operation_policy)::text,sqlc.arg(contract_version)::integer,sqlc.arg(allow_tenant_selection)::boolean
FROM apps WHERE id=sqlc.arg(app_id)::text::uuid AND account_id=sqlc.arg(account_id)::text::uuid
 AND (NOT platform_tenant_required OR sqlc.arg(allow_tenant_selection)::boolean) AND status<>'deleted'
ON CONFLICT(account_id,name) DO UPDATE SET name=commit_sources.name
WHERE commit_sources.app_id=excluded.app_id AND commit_sources.operation_policy=excluded.operation_policy
 AND commit_sources.contract_version=excluded.contract_version AND commit_sources.allow_tenant_selection=excluded.allow_tenant_selection
RETURNING id::text, enabled;

-- name: CommitManagedReceiptReplay :one
SELECT id::text, source_id::text, event_id::text,
 COALESCE(invocation_id::text,'')::text AS invocation_id,
 COALESCE(operation_id::text,'')::text AS operation_id, accepted_at,
 event_type=sqlc.arg(event_type)::text AND payload=sqlc.arg(payload)::jsonb
 AND routing IS NOT DISTINCT FROM sqlc.narg(routing)::jsonb AS matches
FROM commit_receipts WHERE account_id=sqlc.arg(account_id)::text::uuid
 AND source_id=sqlc.arg(source_id)::text::uuid AND event_id=sqlc.arg(event_id)::text::uuid;

-- name: CommitManagedReceipt :one
INSERT INTO commit_receipts(id,account_id,source_id,event_id,event_type,payload,operation_id,operation_state,completed_at,routing)
VALUES(sqlc.arg(id)::text::uuid,sqlc.arg(account_id)::text::uuid,sqlc.arg(source_id)::text::uuid,
 sqlc.arg(event_id)::text::uuid,sqlc.arg(event_type)::text,sqlc.arg(payload)::jsonb,
 sqlc.arg(operation_id)::text::uuid,sqlc.arg(operation_state)::text,sqlc.narg(completed_at)::timestamptz,sqlc.narg(routing)::jsonb)
RETURNING id::text,source_id::text,event_id::text,operation_id::text,accepted_at;

-- name: CommitReceiptIdentity :one
SELECT id::text,source_id::text,event_id::text,
 COALESCE(invocation_id::text,'')::text AS invocation_id,
 COALESCE(operation_id::text,'')::text AS operation_id,accepted_at
FROM commit_receipts WHERE account_id=sqlc.arg(account_id)::text::uuid
 AND source_id=sqlc.arg(source_id)::text::uuid AND event_id=sqlc.arg(event_id)::text::uuid;

-- name: CommitOperationHistory :one
SELECT COALESCE(operation_id,invocation_id)::text AS operation_id,id::text AS receipt_id,
 source_id::text,event_id::text,operation_state,accepted_at,completed_at
FROM commit_receipts WHERE account_id=sqlc.arg(account_id)::text::uuid
 AND COALESCE(operation_id,invocation_id)=sqlc.arg(operation_id)::text::uuid;

-- name: CommitSourceIdentity :one
SELECT id::text,app_id::text,name,enabled,COALESCE(operation_policy,'')::text AS operation_policy,
 contract_version,allow_tenant_selection,relay_status,last_checked_at,pending_events,blocked_events,oldest_pending_at
FROM commit_sources WHERE account_id=sqlc.arg(account_id)::text::uuid AND id=sqlc.arg(source_id)::text::uuid;

-- name: SetCommitSourceEnabled :exec
UPDATE commit_sources SET
 credential_revision=CASE WHEN sqlc.arg(enabled)::boolean AND NOT enabled THEN credential_revision+1 ELSE credential_revision END,
 relay_status=CASE WHEN sqlc.arg(enabled)::boolean AND NOT enabled THEN 'unconfigured' ELSE relay_status END,
 last_checked_at=CASE WHEN sqlc.arg(enabled)::boolean AND NOT enabled THEN NULL ELSE last_checked_at END,
 pending_events=CASE WHEN sqlc.arg(enabled)::boolean AND NOT enabled THEN NULL ELSE pending_events END,
 blocked_events=CASE WHEN sqlc.arg(enabled)::boolean AND NOT enabled THEN NULL ELSE blocked_events END,
 oldest_pending_at=CASE WHEN sqlc.arg(enabled)::boolean AND NOT enabled THEN NULL ELSE oldest_pending_at END,
 enabled=sqlc.arg(enabled)::boolean
WHERE account_id=sqlc.arg(account_id)::text::uuid AND id=sqlc.arg(source_id)::text::uuid;

-- name: CommitPolicyWouldInvalidateSource :one
SELECT EXISTS(SELECT 1 FROM commit_sources c
 WHERE c.account_id=sqlc.arg(account_id)::text::uuid AND c.operation_policy=sqlc.arg(name)::text AND c.enabled
 AND (sqlc.arg(retired)::boolean OR sqlc.arg(configuration)::jsonb->>'scope'<>CASE WHEN c.allow_tenant_selection THEN 'platform_tenant' ELSE 'account' END
 OR COALESCE(sqlc.arg(configuration)::jsonb->>'environment_id','')<>''
 OR sqlc.arg(configuration)::jsonb->>'contention'<>'queue'
 OR NOT COALESCE(sqlc.arg(configuration)::jsonb->'member_app_ids' ? c.app_id::text,false))) AS incompatible;

-- name: SetCommitSourceConnection :execrows
UPDATE commit_sources SET sealed_connection=sqlc.arg(connection)::bytea,
 credential_revision=credential_revision+1,relay_status='unconfigured',
 last_checked_at=NULL,pending_events=NULL,blocked_events=NULL,oldest_pending_at=NULL
WHERE account_id=sqlc.arg(account_id)::text::uuid AND id=sqlc.arg(source_id)::text::uuid;

-- name: CommitRelayObservationSummary :one
WITH observations AS (
 SELECT relay_status,pending_events,blocked_events,oldest_pending_at,COALESCE(last_checked_at>=sqlc.arg(fresh_after)::timestamptz
   AND last_checked_at<=now()+interval '1 minute',false) AS fresh
 FROM commit_sources WHERE enabled
), snapshots AS (
 SELECT *,fresh AND pending_events IS NOT NULL AND blocked_events IS NOT NULL AS known
 FROM observations
)
SELECT count(*)::bigint AS enabled_sources,
 count(*) FILTER(WHERE NOT known)::bigint AS unknown_sources,
 count(*) FILTER(WHERE fresh AND relay_status NOT IN ('healthy','blocked_events'))::bigint AS failing_sources,
 COALESCE(sum(pending_events) FILTER(WHERE known),0)::bigint AS pending_events,
 COALESCE(sum(blocked_events) FILTER(WHERE known),0)::bigint AS blocked_events,
 COALESCE(extract(epoch FROM min(oldest_pending_at) FILTER(WHERE known AND (pending_events>0 OR blocked_events>0))),0)::double precision AS oldest_pending_timestamp
FROM snapshots;
-- Route policy snapshots, locked batches, and receipts (ADR-438).
-- name: ReadRoutePolicyAccount :one
SELECT jsonb_build_object('ID', id, 'Plan', plan, 'Status', status, 'AbuseHoldAt', abuse_hold_at) AS snapshot FROM accounts WHERE id = sqlc.arg(account_id)::text::uuid;

-- name: LockRoutePolicyAccount :one
SELECT jsonb_build_object('ID', id, 'Plan', plan, 'Status', status, 'AbuseHoldAt', abuse_hold_at) AS snapshot FROM accounts WHERE id = sqlc.arg(account_id)::text::uuid FOR UPDATE;

-- name: ReadRoutePolicyApp :one
SELECT jsonb_build_object('ID', id, 'AccountID', account_id, 'Slug', slug, 'Type', type, 'Status', status, 'RAMMB', ram_mb, 'CPUMillicores', cpu_millicores, 'MaxConcurrency', max_concurrency, 'RequestRateLimitRPS', request_rate_limit_rps, 'RequestRateLimitBurst', request_rate_limit_burst, 'ConsumerAuthMode', consumer_auth_mode, 'MaintenanceMode', maintenance_mode, 'Manifest', manifest, 'ScalingPolicy', scaling_policy) AS snapshot FROM apps WHERE id = sqlc.arg(app_id)::text::uuid AND account_id = sqlc.arg(account_id)::text::uuid AND status <> 'deleted';

-- name: LockRoutePolicyApp :one
SELECT jsonb_build_object('ID', id, 'AccountID', account_id, 'Slug', slug, 'Type', type, 'Status', status, 'RAMMB', ram_mb, 'CPUMillicores', cpu_millicores, 'MaxConcurrency', max_concurrency, 'RequestRateLimitRPS', request_rate_limit_rps, 'RequestRateLimitBurst', request_rate_limit_burst, 'ConsumerAuthMode', consumer_auth_mode, 'MaintenanceMode', maintenance_mode, 'Manifest', manifest, 'ScalingPolicy', scaling_policy) AS snapshot FROM apps WHERE id = sqlc.arg(app_id)::text::uuid AND account_id = sqlc.arg(account_id)::text::uuid AND status <> 'deleted' FOR UPDATE;

-- name: ReadRoutePolicyRules :many
SELECT jsonb_build_object('id', id, 'account_id', account_id, 'app_id', app_id, 'match_host', match_host, 'match_path', match_path, 'match_methods', match_methods, 'match_headers', match_headers, 'priority', priority, 'enabled', enabled, 'kind', kind, 'validate_mode', validate_mode, 'action', action, 'created_at', created_at, 'updated_at', updated_at) AS snapshot FROM edge_rules WHERE app_id = sqlc.arg(app_id)::text::uuid ORDER BY id;

-- name: LockRoutePolicyRules :many
SELECT jsonb_build_object('id', id, 'account_id', account_id, 'app_id', app_id, 'match_host', match_host, 'match_path', match_path, 'match_methods', match_methods, 'match_headers', match_headers, 'priority', priority, 'enabled', enabled, 'kind', kind, 'validate_mode', validate_mode, 'action', action, 'created_at', created_at, 'updated_at', updated_at) AS snapshot FROM edge_rules WHERE app_id = sqlc.arg(app_id)::text::uuid ORDER BY id FOR UPDATE;

-- name: ReadRoutePolicyReceiptByKey :one
SELECT request_sha256, receipt FROM route_policy_receipts
WHERE account_id = sqlc.arg(account_id)::text::uuid AND app_id = sqlc.arg(app_id)::text::uuid AND idempotency_key = sqlc.arg(idempotency_key);

-- name: ReadRoutePolicyReceipt :one
SELECT receipt FROM route_policy_receipts WHERE account_id = sqlc.arg(account_id)::text::uuid AND app_id = sqlc.arg(app_id)::text::uuid AND id = sqlc.arg(id)::text::uuid;

-- name: InsertRoutePolicyReceipt :exec
INSERT INTO route_policy_receipts (id, account_id, app_id, idempotency_key, request_sha256, receipt)
VALUES (sqlc.arg(id)::text::uuid, sqlc.arg(account_id)::text::uuid, sqlc.arg(app_id)::text::uuid, sqlc.arg(idempotency_key), sqlc.arg(request_sha256), sqlc.arg(receipt));

-- name: CreateRoutePolicyRule :exec
INSERT INTO edge_rules (id, account_id, app_id, match_host, match_path, match_methods, priority, enabled, kind, action)
VALUES (sqlc.arg(id)::text::uuid, sqlc.arg(account_id)::text::uuid, sqlc.arg(app_id)::text::uuid, sqlc.arg(match_host), sqlc.arg(match_path), sqlc.arg(match_methods), sqlc.arg(priority), true, sqlc.arg(kind), sqlc.arg(action));

-- name: UpdateRoutePolicyRuleAction :execrows
UPDATE edge_rules SET action = sqlc.arg(action), updated_at = now()
WHERE id = sqlc.arg(id)::text::uuid AND app_id = sqlc.arg(app_id)::text::uuid AND account_id = sqlc.arg(account_id)::text::uuid AND kind = sqlc.arg(kind);

-- Captured contract ownership and stability for group policy plans (ADR-446).
-- name: ReadRoutePolicyDeployment :one
SELECT id::text FROM deployments WHERE id = sqlc.arg(deployment_id)::text::uuid AND app_id = sqlc.arg(app_id)::text::uuid;

-- name: LockRoutePolicyDeployment :one
SELECT id::text FROM deployments WHERE id = sqlc.arg(deployment_id)::text::uuid AND app_id = sqlc.arg(app_id)::text::uuid FOR UPDATE;

-- name: ReadRoutePolicyContract :one
SELECT doc, doc_sha256, truncated FROM deployment_openapi_docs WHERE deployment_id = sqlc.arg(deployment_id)::text::uuid AND account_id = sqlc.arg(account_id)::text::uuid AND app_id = sqlc.arg(app_id)::text::uuid;

-- name: LockRoutePolicyContract :one
SELECT doc, doc_sha256, truncated FROM deployment_openapi_docs WHERE deployment_id = sqlc.arg(deployment_id)::text::uuid AND account_id = sqlc.arg(account_id)::text::uuid AND app_id = sqlc.arg(app_id)::text::uuid FOR UPDATE;

-- Saved route intent is read with the same app ownership filters (ADR-448).
-- name: ReadSavedRouteRequirements :one
SELECT jsonb_build_object('app_id', saved.app_id, 'revision', saved.revision, 'sha256', saved.sha256, 'requirements', saved.requirements, 'updated_at', saved.updated_at) AS saved
FROM saved_route_requirements AS saved JOIN apps AS a ON a.id = saved.app_id
WHERE saved.app_id = sqlc.arg(app_id)::text::uuid AND saved.account_id = sqlc.arg(account_id)::text::uuid AND a.account_id = saved.account_id AND a.status <> 'deleted';

-- Serialized by the owned app row lock, including the first insert.
-- name: WriteSavedRouteRequirements :exec
INSERT INTO saved_route_requirements (app_id, account_id, revision, sha256, requirements)
VALUES (sqlc.arg(app_id)::text::uuid, sqlc.arg(account_id)::text::uuid, sqlc.arg(revision), sqlc.arg(sha256), sqlc.arg(requirements))
ON CONFLICT (app_id) DO UPDATE SET revision = EXCLUDED.revision, sha256 = EXCLUDED.sha256, requirements = EXCLUDED.requirements, updated_at = now()
WHERE saved_route_requirements.account_id = EXCLUDED.account_id;

-- name: QueueAutomaticRouteCheck :exec
SELECT enqueue_automatic_route_check(sqlc.arg(app_id)::text::uuid, sqlc.arg(deployment_id)::text::uuid, false);

-- name: ReadCanaryRouteGate :one
SELECT jsonb_build_object('app_id', a.id, 'mode', coalesce(g.mode, 'report'), 'revision', coalesce(g.revision, 0), 'updated_at', g.updated_at) AS gate
FROM apps a LEFT JOIN canary_route_gates g ON g.app_id = a.id AND g.account_id = a.account_id
WHERE a.id = sqlc.arg(app_id)::text::uuid AND a.account_id = sqlc.arg(account_id)::text::uuid AND a.status <> 'deleted';

-- name: WriteCanaryRouteGate :exec
INSERT INTO canary_route_gates (app_id, account_id, mode, revision)
VALUES (sqlc.arg(app_id)::text::uuid, sqlc.arg(account_id)::text::uuid, sqlc.arg(mode), sqlc.arg(revision))
ON CONFLICT (app_id) DO UPDATE SET mode = EXCLUDED.mode, revision = EXCLUDED.revision, updated_at = now()
WHERE canary_route_gates.account_id = EXCLUDED.account_id;

-- name: ReadCanaryRouteGateOwner :one
SELECT a.id::text AS app_id, a.account_id::text AS account_id FROM apps a
JOIN deployments d ON d.app_id = a.id
WHERE d.id = sqlc.arg(deployment_id)::text::uuid AND a.status <> 'deleted';

-- name: LockCanaryRouteGateApp :one
SELECT account_id::text FROM apps WHERE id = sqlc.arg(app_id)::text::uuid FOR UPDATE;

-- name: ClaimAutomaticRouteCheck :one
WITH candidate AS (
    SELECT j.deployment_id FROM automatic_route_checks j
    JOIN apps a ON a.id = j.app_id AND a.account_id = j.account_id
    JOIN deployments d ON d.id = j.deployment_id AND d.app_id = a.id
    JOIN saved_route_requirements s ON s.app_id = a.id AND s.account_id = a.account_id
    WHERE a.status <> 'deleted' AND j.completed_request_id IS DISTINCT FROM j.request_id
      AND j.next_attempt_at <= now() AND (j.lease_until IS NULL OR j.lease_until <= now())
    ORDER BY j.next_attempt_at, j.queued_at, j.deployment_id
    FOR UPDATE OF j SKIP LOCKED LIMIT 1
), claimed AS (
    UPDATE automatic_route_checks j SET claimed_request_id = j.request_id,
        lease_token = sqlc.arg(lease_token)::text::uuid,
        lease_until = now() + sqlc.arg(lease_ms)::bigint * interval '1 millisecond',
        attempts = LEAST(j.attempts + 1, sqlc.arg(max_attempts)::integer)
    FROM candidate WHERE j.deployment_id = candidate.deployment_id
    RETURNING j.*
)
SELECT jsonb_build_object('deployment_id', deployment_id, 'app_id', app_id, 'account_id', account_id, 'request_id', request_id, 'lease_token', lease_token, 'lease_until', lease_until, 'attempts', attempts) AS claim FROM claimed;

-- name: CompleteAutomaticRouteCheck :execrows
UPDATE automatic_route_checks SET latest_check = sqlc.arg(latest_check)::jsonb,
    latest_changes = sqlc.arg(latest_changes)::jsonb, finding_baseline = sqlc.arg(finding_baseline)::jsonb,
    safety_state = CASE WHEN sqlc.arg(notification_allowed)::boolean
        AND sqlc.arg(latest_check)::jsonb->'report'->>'status' IN ('satisfied', 'violated')
        AND EXISTS (SELECT 1 FROM deployments d JOIN apps a ON a.id = d.app_id
            WHERE d.id = automatic_route_checks.deployment_id AND d.app_id = automatic_route_checks.app_id
                AND d.status = 'live' AND a.account_id = automatic_route_checks.account_id AND a.status <> 'deleted')
        THEN sqlc.arg(latest_check)::jsonb->'report'->>'status' ELSE safety_state END,
    capture_sha256 = sqlc.arg(capture_sha256), capture_truncated = sqlc.arg(capture_truncated),
    checked_at = sqlc.arg(checked_at)::timestamptz, completed_request_id = request_id,
    claimed_request_id = NULL, lease_token = NULL, lease_until = NULL,
    attempts = 0, last_error_code = ''
WHERE deployment_id = sqlc.arg(deployment_id)::text::uuid AND app_id = sqlc.arg(app_id)::text::uuid AND account_id = sqlc.arg(account_id)::text::uuid
    AND request_id = sqlc.arg(request_id)::text::uuid AND claimed_request_id = request_id
    AND lease_token = sqlc.arg(lease_token)::text::uuid AND lease_until > clock_timestamp();

-- Parent locks serialize configuration edits while remaining compatible with
-- the FK key-share locks taken by captures, rules and webhook event inserts.
-- name: LockRouteCheckCompletionAccount :one
SELECT jsonb_build_object('ID', id, 'Plan', plan, 'Status', status, 'AbuseHoldAt', abuse_hold_at) AS snapshot
FROM accounts WHERE id = sqlc.arg(account_id)::text::uuid FOR NO KEY UPDATE;

-- name: LockRouteCheckCompletionApp :one
SELECT id::text FROM apps WHERE id = sqlc.arg(app_id)::text::uuid
    AND account_id = sqlc.arg(account_id)::text::uuid AND status <> 'deleted' FOR NO KEY UPDATE;

-- name: FailAutomaticRouteCheck :execrows
UPDATE automatic_route_checks SET claimed_request_id = NULL, lease_token = NULL, lease_until = NULL,
    last_error_code = 'check_failed', next_attempt_at = now() + sqlc.arg(retry_ms)::bigint * interval '1 millisecond'
WHERE deployment_id = sqlc.arg(deployment_id)::text::uuid AND app_id = sqlc.arg(app_id)::text::uuid AND account_id = sqlc.arg(account_id)::text::uuid
    AND request_id = sqlc.arg(request_id)::text::uuid AND claimed_request_id = request_id
    AND lease_token = sqlc.arg(lease_token)::text::uuid AND lease_until > now();

-- name: ReadAutomaticRouteCheck :one
SELECT jsonb_build_object('version', 1, 'app', a.slug, 'app_id', j.app_id, 'deployment_id', j.deployment_id,
    'state', CASE WHEN j.completed_request_id = j.request_id THEN 'complete' WHEN j.lease_until > now() THEN 'running' WHEN j.last_error_code <> '' THEN 'retrying' ELSE 'pending' END,
    'freshness', 'unavailable', 'stale_reasons', '[]'::jsonb, 'attempts', j.attempts,
    'last_error_code', j.last_error_code, 'queued_at', j.queued_at,
    'next_attempt_at', CASE WHEN j.completed_request_id = j.request_id THEN NULL ELSE j.next_attempt_at END,
    'checked_at', j.checked_at, 'check', j.latest_check, 'check_id', j.completed_request_id, 'changes', j.latest_changes,
    'input_capture_sha256', j.capture_sha256, 'input_capture_truncated', j.capture_truncated) AS result
FROM automatic_route_checks j JOIN apps a ON a.id = j.app_id AND a.account_id = j.account_id
JOIN deployments d ON d.id = j.deployment_id AND d.app_id = a.id
WHERE j.deployment_id = sqlc.arg(deployment_id)::text::uuid AND j.app_id = sqlc.arg(app_id)::text::uuid AND j.account_id = sqlc.arg(account_id)::text::uuid AND a.status <> 'deleted';

-- name: LockRouteFindingBaseline :one
SELECT finding_baseline FROM automatic_route_checks
WHERE deployment_id = sqlc.arg(deployment_id)::text::uuid AND app_id = sqlc.arg(app_id)::text::uuid AND account_id = sqlc.arg(account_id)::text::uuid
    AND request_id = sqlc.arg(request_id)::text::uuid AND claimed_request_id = request_id
    AND lease_token = sqlc.arg(lease_token)::text::uuid AND lease_until > clock_timestamp()
FOR UPDATE;

-- name: InsertRouteCheckHistory :exec
INSERT INTO route_check_history(id, deployment_id, app_id, account_id, checked_at, encoded_bytes, entry)
VALUES (sqlc.arg(id)::text::uuid, sqlc.arg(deployment_id)::text::uuid, sqlc.arg(app_id)::text::uuid, sqlc.arg(account_id)::text::uuid, sqlc.arg(checked_at), sqlc.arg(encoded_bytes), sqlc.arg(entry)::jsonb);

-- name: PruneRouteCheckHistory :exec
DELETE FROM route_check_history WHERE id IN (
    SELECT id FROM (
        SELECT id, row_number() OVER (ORDER BY checked_at DESC, id DESC) AS position,
            sum(encoded_bytes) OVER (ORDER BY checked_at DESC, id DESC ROWS UNBOUNDED PRECEDING) AS total_bytes
        FROM route_check_history WHERE deployment_id = sqlc.arg(deployment_id)::text::uuid
    ) retained WHERE position > sqlc.arg(max_entries)::integer OR total_bytes > sqlc.arg(max_bytes)::bigint
);

-- name: ReadRouteCheckHistoryEntry :one
SELECT entry FROM route_check_history
WHERE id = sqlc.arg(id)::text::uuid AND deployment_id = sqlc.arg(deployment_id)::text::uuid AND app_id = sqlc.arg(app_id)::text::uuid AND account_id = sqlc.arg(account_id)::text::uuid;

-- name: ListRouteCheckHistory :many
SELECT jsonb_build_object('version', 1, 'id', h.id, 'checked_at', h.checked_at,
    'status', entry->'check'->'report'->>'status',
    'requirements_revision', entry->'check'->'requirements_revision',
    'requirements_sha256', entry->'check'->>'requirements_sha256',
    'comparison_status', entry->'changes'->>'status', 'summary', entry->'changes'->'summary')
FROM route_check_history h
WHERE deployment_id = sqlc.arg(deployment_id)::text::uuid AND app_id = sqlc.arg(app_id)::text::uuid AND account_id = sqlc.arg(account_id)::text::uuid
    AND (sqlc.arg(before_id)::text = '' OR (checked_at, id) < (SELECT c.checked_at, c.id FROM route_check_history c WHERE c.id = NULLIF(sqlc.arg(before_id)::text, '')::uuid AND c.deployment_id = h.deployment_id AND c.app_id = h.app_id AND c.account_id = h.account_id))
ORDER BY checked_at DESC, id DESC LIMIT sqlc.arg(page_limit)::integer;

-- name: ReadRouteHealthGate :one
SELECT jsonb_build_object('app_id', a.id, 'mode', coalesce(g.mode, 'report'), 'on_regression', coalesce(g.on_regression, 'hold'), 'revision', coalesce(g.revision, 0), 'routes', coalesce(g.routes, '[]'::jsonb), 'updated_at', g.updated_at) AS gate
FROM apps a LEFT JOIN route_health_gates g ON g.app_id = a.id AND g.account_id = a.account_id
WHERE a.id = sqlc.arg(app_id)::text::uuid AND a.account_id = sqlc.arg(account_id)::text::uuid AND a.status <> 'deleted';

-- name: WriteRouteHealthGate :exec
INSERT INTO route_health_gates (app_id, account_id, mode, on_regression, revision, routes, updated_at)
VALUES (sqlc.arg(app_id)::text::uuid, sqlc.arg(account_id)::text::uuid, sqlc.arg(mode), sqlc.arg(on_regression), sqlc.arg(revision), sqlc.arg(routes)::jsonb, clock_timestamp())
ON CONFLICT (app_id) DO UPDATE SET mode = EXCLUDED.mode, on_regression = EXCLUDED.on_regression, revision = EXCLUDED.revision, routes = EXCLUDED.routes, updated_at = clock_timestamp()
WHERE route_health_gates.account_id = EXCLUDED.account_id;

-- name: ReadRouteHealthDeployment :one
SELECT d.id::text AS id, d.app_id::text AS app_id, d.commit_sha, d.status::text AS status, d.traffic_percent,
 d.canary_step, d.canary_total_steps, d.canary_step_started_at, coalesce(d.scope, '')::text AS scope
FROM deployments d WHERE d.id = sqlc.arg(deployment_id)::text::uuid AND d.app_id = sqlc.arg(app_id)::text::uuid;

-- name: RouteHealthStableIDs :many
SELECT d.id::text FROM deployments d JOIN deployments c ON c.app_id = d.app_id
WHERE c.id = sqlc.arg(deployment_id)::text::uuid AND c.app_id = sqlc.arg(app_id)::text::uuid
 AND d.id <> c.id AND d.status = 'live' AND d.traffic_percent > 0 AND coalesce(nullif(d.scope, ''), 'default') = coalesce(nullif(c.scope, ''), 'default')
ORDER BY d.id LIMIT 2;

-- name: RouteHealthObservation :one
-- Exact selected labels and shared windows. Aggregate publisher weights without
-- expanding requests. Percentile ranks interpolate bucket representatives, like
-- RequestTelemetryBaselineP95ByRoute. Old selectors skip the percentile sort.
WITH selected AS (
 SELECT (value->>'method')::text AS method, (value->>'path')::text AS path,
  (coalesce((value->>'check_latency')::boolean, false) OR coalesce((value->>'max_p95_ms')::bigint, 0) > 0) AS latency_enabled
 FROM jsonb_array_elements(sqlc.arg(routes)::jsonb)
), windows AS (
 SELECT (value->>'start')::timestamptz AS start, (value->>'end')::timestamptz AS "end" FROM jsonb_array_elements(sqlc.arg(windows)::jsonb)
), observed AS MATERIALIZED (
 SELECT s.method, s.path, s.latency_enabled, w.start, w."end", t.deployment_id, t.latency_ms, t.status, t.count
 FROM selected s CROSS JOIN windows w LEFT JOIN request_telemetry t
 ON t.app_id = sqlc.arg(app_id)::text::uuid AND t.account_id = sqlc.arg(account_id)::text::uuid
 AND t.deployment_id IN (sqlc.arg(candidate_id)::text::uuid, sqlc.arg(stable_id)::text::uuid)
 AND t.method = s.method AND t.route = s.method || ' ' || s.path
 AND t.received_at >= w.start AND t.received_at < w."end"
 AND t.received_at >= sqlc.arg(since)::timestamptz AND t.received_at < sqlc.arg(until)::timestamptz
 AND (sqlc.arg(customer_id)::text = ''
  OR (sqlc.arg(customer_group_by)::text = 'tenant' AND t.platform_tenant_id = NULLIF(sqlc.arg(customer_id)::text, '')::uuid)
  OR (sqlc.arg(customer_group_by)::text = 'consumer' AND t.consumer_id = NULLIF(sqlc.arg(customer_id)::text, '')::uuid))
), counts AS (
 SELECT method, path, start, "end",
 coalesce(sum(count::bigint) FILTER (WHERE deployment_id = sqlc.arg(candidate_id)::text::uuid), 0)::bigint AS candidate_requests,
 coalesce(sum(count::bigint) FILTER (WHERE deployment_id = sqlc.arg(candidate_id)::text::uuid AND status BETWEEN 500 AND 599), 0)::bigint AS candidate_errors,
 coalesce(sum(count::bigint) FILTER (WHERE deployment_id = sqlc.arg(stable_id)::text::uuid), 0)::bigint AS stable_requests,
 coalesce(sum(count::bigint) FILTER (WHERE deployment_id = sqlc.arg(stable_id)::text::uuid AND status BETWEEN 500 AND 599), 0)::bigint AS stable_errors
 FROM observed GROUP BY method, path, start, "end"
), weighted AS (
 SELECT method, path, start, deployment_id, latency_ms, sum(count::bigint) AS weight
 FROM observed WHERE latency_enabled AND deployment_id IS NOT NULL
 GROUP BY method, path, start, deployment_id, latency_ms
), ranked AS (
 SELECT method, path, start, deployment_id, latency_ms,
 sum(weight) OVER (PARTITION BY method, path, start, deployment_id ORDER BY latency_ms ROWS UNBOUNDED PRECEDING) AS cumulative,
 sum(weight) OVER (PARTITION BY method, path, start, deployment_id) AS total
 FROM weighted
), targets AS (
 SELECT method, path, start, deployment_id, latency_ms, cumulative,
 (total - 1)::numeric * sqlc.arg(latency_quantile)::double precision::numeric AS rank
 FROM ranked
), values_at_rank AS (
 SELECT method, path, start, deployment_id, rank,
 min(latency_ms) FILTER (WHERE cumulative > floor(rank)) AS low,
 min(latency_ms) FILTER (WHERE cumulative > ceil(rank)) AS high
 FROM targets GROUP BY method, path, start, deployment_id, rank
), percentiles AS (
 SELECT method, path, start, deployment_id,
 (low + (rank - floor(rank)) * (high - low))::double precision AS p95_ms
 FROM values_at_rank
)
SELECT coalesce(jsonb_agg(jsonb_build_object('method', c.method, 'path', c.path, 'start', c.start, 'end', c."end",
 'candidate', jsonb_build_object('requests', c.candidate_requests, 'server_errors', c.candidate_errors, 'p95_latency_ms', candidate.p95_ms),
 'stable', jsonb_build_object('requests', c.stable_requests, 'server_errors', c.stable_errors, 'p95_latency_ms', stable.p95_ms)) ORDER BY c.method, c.path, c.start), '[]'::jsonb)::jsonb AS observations
FROM counts c
LEFT JOIN percentiles candidate ON candidate.method = c.method AND candidate.path = c.path AND candidate.start = c.start AND candidate.deployment_id = sqlc.arg(candidate_id)::text::uuid
LEFT JOIN percentiles stable ON stable.method = c.method AND stable.path = c.path AND stable.start = c.start AND stable.deployment_id = sqlc.arg(stable_id)::text::uuid;

-- name: RouteHealthClock :one
SELECT clock_timestamp()::timestamptz AS checked_at;

-- name: InsertRouteHealthHistory :one
-- AdvanceCanary holds the same app lock for inserts and pruning. Retries do
-- not mutate the original evidence or its checked_at.
WITH inserted AS (
 INSERT INTO route_health_history(id, deployment_id, app_id, account_id, decision_key, checked_at, encoded_bytes, entry)
 VALUES (sqlc.arg(id)::text::uuid, sqlc.arg(deployment_id)::text::uuid, sqlc.arg(app_id)::text::uuid, sqlc.arg(account_id)::text::uuid,
  sqlc.arg(decision_key), sqlc.arg(checked_at), sqlc.arg(encoded_bytes), sqlc.arg(entry)::jsonb)
 ON CONFLICT (deployment_id, decision_key) DO NOTHING RETURNING id
)
SELECT id::text FROM inserted
UNION ALL
SELECT id::text FROM route_health_history
 WHERE deployment_id = sqlc.arg(deployment_id)::text::uuid AND app_id = sqlc.arg(app_id)::text::uuid AND account_id = sqlc.arg(account_id)::text::uuid
 AND decision_key = sqlc.arg(decision_key) AND NOT EXISTS (SELECT 1 FROM inserted)
LIMIT 1;

-- name: PruneRouteHealthHistory :exec
DELETE FROM route_health_history WHERE id IN (
 SELECT id FROM (
  SELECT id, row_number() OVER (ORDER BY checked_at DESC, id DESC) AS position,
   sum(encoded_bytes) OVER (ORDER BY checked_at DESC, id DESC ROWS UNBOUNDED PRECEDING) AS total_bytes
  FROM route_health_history WHERE deployment_id = sqlc.arg(deployment_id)::text::uuid
 ) retained WHERE position > sqlc.arg(max_entries)::integer OR total_bytes > sqlc.arg(max_bytes)::bigint
);

-- name: ReadRouteHealthHistoryEntry :one
SELECT entry FROM route_health_history
 WHERE id = sqlc.arg(id)::text::uuid AND deployment_id = sqlc.arg(deployment_id)::text::uuid
 AND app_id = sqlc.arg(app_id)::text::uuid AND account_id = sqlc.arg(account_id)::text::uuid;

-- name: ListRouteHealthHistory :many
SELECT h.entry FROM route_health_history h
 WHERE deployment_id = sqlc.arg(deployment_id)::text::uuid AND app_id = sqlc.arg(app_id)::text::uuid AND account_id = sqlc.arg(account_id)::text::uuid
 AND (sqlc.arg(before_id)::text = '' OR (checked_at, id) < (SELECT c.checked_at, c.id FROM route_health_history c
  WHERE c.id = NULLIF(sqlc.arg(before_id)::text, '')::uuid AND c.deployment_id = h.deployment_id AND c.app_id = h.app_id AND c.account_id = h.account_id))
 ORDER BY checked_at DESC, id DESC LIMIT sqlc.arg(page_limit)::integer;

-- name: ReadRouteHealthNotificationState :one
SELECT context_key, status, COALESCE(blocked_decision_id::text, '')::text AS blocked_decision_id
FROM route_health_notification_state WHERE deployment_id = sqlc.arg(deployment_id)::text::uuid
 AND app_id = sqlc.arg(app_id)::text::uuid AND account_id = sqlc.arg(account_id)::text::uuid;

-- name: WriteRouteHealthNotificationState :exec
-- Serialized by AdvanceCanary's owned parent app/deployment lock.
INSERT INTO route_health_notification_state(deployment_id, app_id, account_id, context_key, status, blocked_decision_id, updated_at)
VALUES (sqlc.arg(deployment_id)::text::uuid, sqlc.arg(app_id)::text::uuid, sqlc.arg(account_id)::text::uuid,
 sqlc.arg(context_key), sqlc.arg(status), NULLIF(sqlc.arg(blocked_decision_id)::text, '')::uuid, sqlc.arg(updated_at))
ON CONFLICT (deployment_id) DO UPDATE SET app_id = EXCLUDED.app_id, account_id = EXCLUDED.account_id,
 context_key = EXCLUDED.context_key, status = EXCLUDED.status, blocked_decision_id = EXCLUDED.blocked_decision_id, updated_at = EXCLUDED.updated_at;

-- name: EnqueueRouteHealthNotification :exec
WITH recipients AS (
 SELECT array_agg(id ORDER BY id) AS ids FROM app_webhooks
 WHERE app_id = sqlc.arg(app_id)::text::uuid AND account_id = sqlc.arg(account_id)::text::uuid AND scope = 'app' AND enabled
 AND (cardinality(event_filter) = 0 OR sqlc.arg(event)::text = ANY(event_filter))
)
INSERT INTO app_webhook_event_outbox(account_id, app_id, event, source_id, payload, recipient_webhook_ids)
SELECT sqlc.arg(account_id)::text::uuid, sqlc.arg(app_id)::text::uuid, sqlc.arg(event), sqlc.arg(decision_id)::text::uuid, sqlc.arg(payload)::jsonb, ids
FROM recipients WHERE cardinality(ids) > 0 ON CONFLICT (event, source_id) DO NOTHING;


-- name: LockRouteHealthRecoveryLease :one
SELECT expires_at FROM safe_release_worker_lease WHERE singleton = true FOR UPDATE;

-- name: LockRouteHealthRecoveryCandidate :one
SELECT id::text AS id, app_id::text AS app_id, status::text AS status, commit_sha,
       traffic_percent, canary_step, canary_total_steps, canary_step_started_at,
       rollout_state, coalesce(scope, '')::text AS scope, created_at
FROM deployments WHERE id = sqlc.arg(deployment_id)::text::uuid AND app_id = sqlc.arg(app_id)::text::uuid FOR UPDATE;

-- name: LockRouteHealthRecoverySiblings :many
SELECT id::text AS id, created_at, canary_total_steps, rollout_state
FROM deployments WHERE app_id = sqlc.arg(app_id)::text::uuid AND id <> sqlc.arg(deployment_id)::text::uuid
 AND status = 'live' AND traffic_percent > 0
 AND coalesce(nullif(scope, ''), 'default') = coalesce(nullif(sqlc.arg(scope)::text, ''), 'default')
ORDER BY id FOR UPDATE;

-- name: NotifyRouteHealthRecovery :exec
SELECT pg_notify('deployment_changed', sqlc.arg(payload)::text);

-- name: LockImagePreparationDeployment :one
SELECT status, COALESCE(NULLIF(rootfs_path, ''),
       CASE WHEN kind = 'image' THEN COALESCE(NULLIF(image_digest, ''), NULLIF(source_path, '')) END, '')::text AS input_path, rootfs_key,
       COALESCE(rootfs_bytes, 0)::bigint AS input_bytes
FROM deployments WHERE id = sqlc.arg(deployment_id)::uuid FOR UPDATE;

-- name: GetImagePreparation :one
SELECT * FROM deployment_image_preparations
WHERE deployment_id = sqlc.arg(deployment_id)::uuid;

-- name: BeginImagePreparation :one
INSERT INTO deployment_image_preparations
    (deployment_id, node_name, input_path, input_key, input_bytes, claim_token, phase)
VALUES (sqlc.arg(deployment_id)::uuid, sqlc.arg(node_name)::text,
        sqlc.arg(input_path)::text, sqlc.arg(input_key)::text,
        sqlc.arg(input_bytes)::bigint, sqlc.arg(claim_token)::uuid, 'preparing')
ON CONFLICT (deployment_id) DO UPDATE
SET claim_token = EXCLUDED.claim_token, updated_at = now()
RETURNING *;

-- name: PublishImagePreparationLayer :execrows
WITH publication AS (
    UPDATE deployments d
    SET rootfs_path = sqlc.arg(path)::text, rootfs_key = sqlc.arg(key)::text,
        rootfs_bytes = sqlc.arg(bytes)::bigint
    WHERE d.id = sqlc.arg(deployment_id)::uuid AND d.status = 'imaging'
      AND EXISTS (SELECT 1 FROM deployment_image_preparations p
                  WHERE p.deployment_id = d.id AND p.claim_token = sqlc.arg(claim_token)::uuid
                    AND p.phase = 'preparing')
    RETURNING d.id
)
UPDATE deployment_image_preparations p
SET phase = 'layer_published', updated_at = now()
FROM publication WHERE p.deployment_id = publication.id;

-- name: AdvanceImagePreparation :execrows
UPDATE deployment_image_preparations
SET phase = sqlc.arg(next_phase)::text, updated_at = now()
WHERE deployment_id = sqlc.arg(deployment_id)::uuid
  AND claim_token = sqlc.arg(claim_token)::uuid AND phase = sqlc.arg(expected_phase)::text;

-- name: ListResumableImagePreparations :many
SELECT d.app_id, d.id AS deployment_id, p.node_name AS node_id
FROM deployment_image_preparations p JOIN deployments d ON d.id = p.deployment_id
WHERE p.phase <> 'handed_off' AND d.status IN ('pending', 'building', 'imaging', 'snapshotting')
  AND (sqlc.arg(node_id)::text = '' OR p.node_name = '' OR p.node_name = sqlc.arg(node_id)::text)
ORDER BY p.updated_at, p.deployment_id LIMIT sqlc.arg(batch_limit)::int;

-- name: ListBuildsAwaitingImage :many
SELECT d.app_id, d.id AS deployment_id, COALESCE(p.builder_node_id, '')::text AS node_id
FROM deployments d JOIN builds b ON b.deployment_id = d.id
JOIN build_provenance p ON p.build_id = b.id
WHERE d.status IN ('pending', 'building') AND b.status = 'succeeded'
  AND COALESCE(d.rootfs_path, '') <> ''
  AND NOT EXISTS (SELECT 1 FROM deployment_image_preparations i WHERE i.deployment_id = d.id)
  AND (sqlc.arg(node_id)::text = '' OR COALESCE(p.builder_node_id, '') = '' OR p.builder_node_id = sqlc.arg(node_id)::text)
ORDER BY b.finished_at, b.id LIMIT sqlc.arg(batch_limit)::int;

-- name: TransitionImagePreparation :execrows
UPDATE deployments d SET status = sqlc.arg(next_status)::text, error = NULL
FROM deployment_image_preparations p
WHERE d.id = sqlc.arg(deployment_id)::uuid AND p.deployment_id = d.id
  AND p.claim_token = sqlc.arg(claim_token)::uuid
  AND ((sqlc.arg(next_status)::text = 'imaging' AND
        ((p.phase = 'preparing' AND d.status IN ('pending', 'building', 'imaging')) OR
         (p.phase = 'layer_published' AND d.status = 'imaging'))) OR
       (sqlc.arg(next_status)::text = 'snapshotting' AND p.phase = 'scan_complete'
        AND d.status IN ('imaging', 'snapshotting')));
-- name: CommitTenantAppScope :one
SELECT s.id::text FROM tenant_surfaces s
WHERE s.account_id=sqlc.arg(account_id)::text::uuid
 AND s.app_id=sqlc.arg(app_id)::text::uuid
 AND s.platform_tenant_id=sqlc.arg(tenant_id)::text::uuid AND s.status='active'
LIMIT 1 FOR SHARE;


-- name: RouteCustomerHealthObservation :one
-- Advisory identity cohorts use the same exact routes, deployment pair and
-- closed windows as aggregate health. Rank before bounding output and sorting
-- weighted latency. Request-time attribution never follows today's tenant link.
WITH selected AS (
 SELECT value->>'method' AS method, value->>'path' AS path,
  (coalesce((value->>'check_latency')::boolean, false) OR coalesce((value->>'max_p95_ms')::bigint, 0) > 0) AS latency_enabled,
  ARRAY(SELECT code::integer FROM jsonb_array_elements_text(coalesce(value->'watch_statuses', '[]'::jsonb)) code) AS watch_statuses
 FROM jsonb_array_elements(sqlc.arg(routes)::jsonb)
), watched AS (
 SELECT method,path,unnest(watch_statuses) AS status_code FROM selected
), windows AS (
 SELECT (value->>'start')::timestamptz AS start, (value->>'end')::timestamptz AS "end"
 FROM jsonb_array_elements(sqlc.arg(windows)::jsonb)
), observed AS MATERIALIZED (
 SELECT s.method, s.path, s.latency_enabled, w.start, w."end", rt.deployment_id, rt.latency_ms, rt.status, rt.count::bigint AS requests, (rt.status=ANY(s.watch_statuses)) AS watched_status,
  CASE WHEN sqlc.arg(group_by)::text = 'tenant' THEN pt.id ELSE c.id END AS customer_id,
  CASE WHEN sqlc.arg(group_by)::text = 'tenant' THEN rt.platform_tenant_id IS NULL ELSE rt.consumer_id IS NULL END AS unattributed
 FROM selected s CROSS JOIN windows w JOIN request_telemetry rt
 ON rt.app_id = sqlc.arg(app_id)::text::uuid AND rt.account_id = sqlc.arg(account_id)::text::uuid
 AND rt.deployment_id IN (sqlc.arg(candidate_id)::text::uuid, sqlc.arg(stable_id)::text::uuid)
 AND rt.method = s.method AND rt.route = s.method || ' ' || s.path
 AND rt.received_at >= w.start AND rt.received_at < w."end"
 AND rt.received_at >= sqlc.arg(since)::timestamptz AND rt.received_at < sqlc.arg(until)::timestamptz
 LEFT JOIN api_consumers c ON c.id = rt.consumer_id AND c.account_id = rt.account_id AND c.app_id = rt.app_id
 LEFT JOIN platform_tenants pt ON pt.id = rt.platform_tenant_id AND pt.account_id = rt.account_id
), totals AS (
 SELECT method, path,
 count(DISTINCT customer_id)::bigint AS observed_customers,
 coalesce(sum(requests) FILTER (WHERE deployment_id = sqlc.arg(candidate_id)::text::uuid AND customer_id IS NOT NULL), 0)::bigint AS candidate_identified,
 coalesce(sum(requests) FILTER (WHERE deployment_id = sqlc.arg(candidate_id)::text::uuid AND unattributed), 0)::bigint AS candidate_unattributed,
 coalesce(sum(requests) FILTER (WHERE deployment_id = sqlc.arg(candidate_id)::text::uuid AND NOT unattributed AND customer_id IS NULL), 0)::bigint AS candidate_unresolved,
 coalesce(sum(requests) FILTER (WHERE deployment_id = sqlc.arg(stable_id)::text::uuid AND customer_id IS NOT NULL), 0)::bigint AS stable_identified,
 coalesce(sum(requests) FILTER (WHERE deployment_id = sqlc.arg(stable_id)::text::uuid AND unattributed), 0)::bigint AS stable_unattributed,
 coalesce(sum(requests) FILTER (WHERE deployment_id = sqlc.arg(stable_id)::text::uuid AND NOT unattributed AND customer_id IS NULL), 0)::bigint AS stable_unresolved
 FROM observed GROUP BY method, path
), cohort_totals AS (
 SELECT method, path, customer_id, sum(requests)::bigint AS requests,
 coalesce(sum(requests) FILTER (WHERE deployment_id = sqlc.arg(candidate_id)::text::uuid AND status BETWEEN 500 AND 599), 0)::bigint AS candidate_errors,
 coalesce(sum(requests) FILTER (WHERE deployment_id = sqlc.arg(candidate_id)::text::uuid AND watched_status),0)::bigint AS candidate_watched_responses,
 coalesce(sum(requests) FILTER (WHERE deployment_id = sqlc.arg(candidate_id)::text::uuid), 0)::bigint AS candidate_requests,
 coalesce(sum(requests) FILTER (WHERE deployment_id = sqlc.arg(stable_id)::text::uuid), 0)::bigint AS stable_requests
 FROM observed WHERE customer_id IS NOT NULL GROUP BY method, path, customer_id
), ranked_cohorts AS (
 SELECT *, row_number() OVER (PARTITION BY method, path ORDER BY candidate_errors DESC, candidate_watched_responses DESC, requests DESC, customer_id ASC) AS position
 FROM cohort_totals
), bounds AS (
 SELECT method, path,
 coalesce(sum(candidate_requests) FILTER (WHERE position > sqlc.arg(customer_limit)::integer), 0)::bigint AS candidate_other,
 coalesce(sum(stable_requests) FILTER (WHERE position > sqlc.arg(customer_limit)::integer), 0)::bigint AS stable_other
 FROM ranked_cohorts GROUP BY method, path
), bounded AS MATERIALIZED (
 SELECT * FROM ranked_cohorts WHERE position <= sqlc.arg(customer_limit)::integer
), counts AS (
 SELECT b.method, b.path, b.customer_id, b.position, w.start, w."end",
 coalesce(sum(o.requests) FILTER (WHERE o.deployment_id = sqlc.arg(candidate_id)::text::uuid), 0)::bigint AS candidate_requests,
 coalesce(sum(o.requests) FILTER (WHERE o.deployment_id = sqlc.arg(candidate_id)::text::uuid AND o.status BETWEEN 500 AND 599), 0)::bigint AS candidate_errors,
 coalesce(sum(o.requests) FILTER (WHERE o.deployment_id = sqlc.arg(stable_id)::text::uuid), 0)::bigint AS stable_requests,
 coalesce(sum(o.requests) FILTER (WHERE o.deployment_id = sqlc.arg(stable_id)::text::uuid AND o.status BETWEEN 500 AND 599), 0)::bigint AS stable_errors
 FROM bounded b CROSS JOIN windows w LEFT JOIN observed o
 ON o.method = b.method AND o.path = b.path AND o.customer_id = b.customer_id AND o.start = w.start
 GROUP BY b.method, b.path, b.customer_id, b.position, w.start, w."end"
), status_responses AS (
 SELECT o.method,o.path,o.customer_id,o.start,o.status AS status_code,
 coalesce(sum(o.requests) FILTER (WHERE o.deployment_id=sqlc.arg(candidate_id)::text::uuid),0)::bigint AS candidate_responses,
 coalesce(sum(o.requests) FILTER (WHERE o.deployment_id=sqlc.arg(stable_id)::text::uuid),0)::bigint AS stable_responses
 FROM observed o JOIN bounded b USING (method,path,customer_id) WHERE o.watched_status
 GROUP BY o.method,o.path,o.customer_id,o.start,o.status
), status_windows AS (
 SELECT c.method,c.path,c.customer_id,watch.status_code,
 jsonb_agg(jsonb_build_object('start',c.start,'end',c."end",
 'candidate',jsonb_build_object('requests',c.candidate_requests,'responses',coalesce(r.candidate_responses,0)),
 'stable',jsonb_build_object('requests',c.stable_requests,'responses',coalesce(r.stable_responses,0))) ORDER BY c.start) AS windows
 FROM counts c JOIN watched watch USING (method,path)
 LEFT JOIN status_responses r ON r.method=c.method AND r.path=c.path AND r.customer_id=c.customer_id AND r.start=c.start AND r.status_code=watch.status_code
 GROUP BY c.method,c.path,c.customer_id,watch.status_code
), status_findings AS (
 SELECT method,path,customer_id,jsonb_agg(jsonb_build_object('status_code',status_code,'windows',windows) ORDER BY status_code) AS statuses
 FROM status_windows GROUP BY method,path,customer_id
), weighted AS (
 SELECT o.method, o.path, o.customer_id, o.start, o.deployment_id, o.latency_ms, sum(o.requests) AS weight
 FROM observed o JOIN bounded b USING (method, path, customer_id) WHERE o.latency_enabled
 GROUP BY o.method, o.path, o.customer_id, o.start, o.deployment_id, o.latency_ms
), ranked AS (
 SELECT *, sum(weight) OVER (PARTITION BY method, path, customer_id, start, deployment_id ORDER BY latency_ms ROWS UNBOUNDED PRECEDING) AS cumulative,
 sum(weight) OVER (PARTITION BY method, path, customer_id, start, deployment_id) AS total FROM weighted
), targets AS (
 SELECT *, (total - 1)::numeric * sqlc.arg(latency_quantile)::double precision::numeric AS rank FROM ranked
), values_at_rank AS (
 SELECT method, path, customer_id, start, deployment_id, rank,
 min(latency_ms) FILTER (WHERE cumulative > floor(rank)) AS low,
 min(latency_ms) FILTER (WHERE cumulative > ceil(rank)) AS high
 FROM targets GROUP BY method, path, customer_id, start, deployment_id, rank
), percentiles AS (
 SELECT method, path, customer_id, start, deployment_id,
 (low + (rank - floor(rank)) * (high - low))::double precision AS p95_ms FROM values_at_rank
), cohort_windows AS (
 SELECT c.method, c.path, c.customer_id, c.position,
 jsonb_agg(jsonb_build_object('start', c.start, 'end', c."end",
 'candidate', jsonb_build_object('requests', c.candidate_requests, 'server_errors', c.candidate_errors, 'p95_latency_ms', cp.p95_ms),
 'stable', jsonb_build_object('requests', c.stable_requests, 'server_errors', c.stable_errors, 'p95_latency_ms', sp.p95_ms)) ORDER BY c.start) AS windows
 FROM counts c
 LEFT JOIN percentiles cp ON cp.method = c.method AND cp.path = c.path AND cp.customer_id = c.customer_id AND cp.start = c.start AND cp.deployment_id = sqlc.arg(candidate_id)::text::uuid
 LEFT JOIN percentiles sp ON sp.method = c.method AND sp.path = c.path AND sp.customer_id = c.customer_id AND sp.start = c.start AND sp.deployment_id = sqlc.arg(stable_id)::text::uuid
 GROUP BY c.method, c.path, c.customer_id, c.position
), customers AS (
 SELECT cw.method, cw.path, jsonb_agg(jsonb_build_object('customer_id', cw.customer_id,
 'health', jsonb_build_object('method', cw.method, 'path', cw.path, 'windows', cw.windows) || CASE WHEN sf.statuses IS NULL THEN '{}'::jsonb ELSE jsonb_build_object('client_errors',jsonb_build_object('statuses',sf.statuses)) END) ORDER BY cw.position) AS customers
 FROM cohort_windows cw LEFT JOIN status_findings sf USING (method,path,customer_id) GROUP BY cw.method, cw.path
)
SELECT coalesce(jsonb_agg(jsonb_build_object('method', s.method, 'path', s.path,
 'observed_customers', coalesce(t.observed_customers, 0), 'customers_truncated', coalesce(t.observed_customers, 0) > sqlc.arg(customer_limit)::integer,
 'candidate', jsonb_build_object('identified_requests', coalesce(t.candidate_identified, 0), 'unattributed_requests', coalesce(t.candidate_unattributed, 0), 'unresolved_identity_requests', coalesce(t.candidate_unresolved, 0), 'other_customer_requests', coalesce(b.candidate_other, 0)),
 'stable', jsonb_build_object('identified_requests', coalesce(t.stable_identified, 0), 'unattributed_requests', coalesce(t.stable_unattributed, 0), 'unresolved_identity_requests', coalesce(t.stable_unresolved, 0), 'other_customer_requests', coalesce(b.stable_other, 0)),
 'customers', coalesce(c.customers, '[]'::jsonb)) ORDER BY s.method, s.path), '[]'::jsonb)::jsonb AS observations
FROM selected s LEFT JOIN totals t USING (method, path) LEFT JOIN bounds b USING (method, path) LEFT JOIN customers c USING (method, path);

-- name: RouteHealthClientErrorObservation :one
-- Live advisory reads only. Each code has its own weighted numerator and the
-- entire deployment/route/window request count as denominator. No request expansion.
WITH selected AS (
 SELECT value->>'method' AS method, value->>'path' AS path,
  ARRAY(SELECT code::integer FROM jsonb_array_elements_text(coalesce(value->'watch_statuses', '[]'::jsonb)) code) AS watch_statuses
 FROM jsonb_array_elements(sqlc.arg(routes)::jsonb)
), watched AS (
 SELECT method, path, unnest(watch_statuses) AS status_code FROM selected
), windows AS (
 SELECT (value->>'start')::timestamptz AS start, (value->>'end')::timestamptz AS "end" FROM jsonb_array_elements(sqlc.arg(windows)::jsonb)
), counts AS (
 SELECT s.method,s.path,s.status_code,w.start,w."end",
 coalesce(sum(t.count::bigint) FILTER (WHERE t.deployment_id=sqlc.arg(candidate_id)::text::uuid),0)::bigint AS candidate_requests,
 coalesce(sum(t.count::bigint) FILTER (WHERE t.deployment_id=sqlc.arg(candidate_id)::text::uuid AND t.status=s.status_code),0)::bigint AS candidate_responses,
 coalesce(sum(t.count::bigint) FILTER (WHERE t.deployment_id=sqlc.arg(stable_id)::text::uuid),0)::bigint AS stable_requests,
 coalesce(sum(t.count::bigint) FILTER (WHERE t.deployment_id=sqlc.arg(stable_id)::text::uuid AND t.status=s.status_code),0)::bigint AS stable_responses
 FROM watched s CROSS JOIN windows w LEFT JOIN request_telemetry t
 ON t.app_id=sqlc.arg(app_id)::text::uuid AND t.account_id=sqlc.arg(account_id)::text::uuid
 AND t.deployment_id IN (sqlc.arg(candidate_id)::text::uuid,sqlc.arg(stable_id)::text::uuid)
 AND t.method=s.method AND t.route=s.method || ' ' || s.path
 AND t.received_at>=w.start AND t.received_at<w."end"
 AND t.received_at>=sqlc.arg(since)::timestamptz AND t.received_at<sqlc.arg(until)::timestamptz
 AND (sqlc.arg(customer_id)::text = ''
  OR (sqlc.arg(customer_group_by)::text = 'tenant' AND t.platform_tenant_id = NULLIF(sqlc.arg(customer_id)::text, '')::uuid)
  OR (sqlc.arg(customer_group_by)::text = 'consumer' AND t.consumer_id = NULLIF(sqlc.arg(customer_id)::text, '')::uuid))
 GROUP BY s.method,s.path,s.status_code,w.start,w."end"
), signals AS (
 SELECT method,path,status_code,jsonb_agg(jsonb_build_object('start',start,'end',"end",
 'candidate',jsonb_build_object('requests',candidate_requests,'responses',candidate_responses),
 'stable',jsonb_build_object('requests',stable_requests,'responses',stable_responses)) ORDER BY start) AS windows
 FROM counts GROUP BY method,path,status_code
), routes AS (
 SELECT method,path,jsonb_agg(jsonb_build_object('status_code',status_code,'windows',windows) ORDER BY status_code) AS statuses
 FROM signals GROUP BY method,path
)
SELECT coalesce(jsonb_agg(jsonb_build_object('method',method,'path',path,'client_errors',jsonb_build_object('statuses',statuses)) ORDER BY method,path),'[]'::jsonb)::jsonb AS observations
FROM routes;

-- name: RouteHealthInvestigationCustomerExists :one
SELECT CASE WHEN sqlc.arg(customer_group_by)::text = 'tenant' THEN
 EXISTS(SELECT 1 FROM platform_tenants WHERE id = sqlc.arg(customer_id)::text::uuid AND account_id = sqlc.arg(account_id)::text::uuid)
 ELSE EXISTS(SELECT 1 FROM api_consumers WHERE id = sqlc.arg(customer_id)::text::uuid AND account_id = sqlc.arg(account_id)::text::uuid AND app_id = sqlc.arg(app_id)::text::uuid) END::boolean AS owned;

-- name: RouteHealthInvestigationExamples :one
-- Metadata only. Count all matching rows/weights before independently bounding
-- each deployment/window. Error rows prefer trace links; latency rows prefer
-- slowest latency buckets. Both then use newest timestamps and UUID ties.
WITH windows AS (
 SELECT (value->>'start')::timestamptz AS start, (value->>'end')::timestamptz AS "end"
 FROM jsonb_array_elements(sqlc.arg(windows)::jsonb)
), observed AS MATERIALIZED (
 SELECT w.start,w."end",t.id,t.received_at,t.deployment_id,t.status,t.latency_ms,t.count,t.trace_id
 FROM windows w JOIN request_telemetry t
 ON t.app_id = sqlc.arg(app_id)::text::uuid AND t.account_id = sqlc.arg(account_id)::text::uuid
 AND t.method = sqlc.arg(method)::text AND t.route = sqlc.arg(method)::text || ' ' || sqlc.arg(path)::text
 AND t.deployment_id IN (sqlc.arg(candidate_id)::text::uuid,sqlc.arg(stable_id)::text::uuid)
 AND t.received_at >= w.start AND t.received_at < w."end"
 AND (sqlc.arg(latency)::boolean OR t.status BETWEEN sqlc.arg(status_min)::integer AND sqlc.arg(status_max)::integer)
 AND (sqlc.arg(customer_id)::text = ''
  OR (sqlc.arg(customer_group_by)::text = 'tenant' AND t.platform_tenant_id = NULLIF(sqlc.arg(customer_id)::text, '')::uuid)
  OR (sqlc.arg(customer_group_by)::text = 'consumer' AND t.consumer_id = NULLIF(sqlc.arg(customer_id)::text, '')::uuid))
), ranked AS (
 SELECT *,row_number() OVER (PARTITION BY start,deployment_id ORDER BY CASE WHEN sqlc.arg(latency)::boolean THEN latency_ms END DESC NULLS LAST,(nullif(trace_id,'') IS NOT NULL) DESC,received_at DESC,id DESC) AS position
 FROM observed
), totals AS (
 SELECT start,deployment_id,count(*)::bigint AS observed_rows,sum(count::bigint)::bigint AS matching_requests
 FROM observed GROUP BY start,deployment_id
), examples AS (
 SELECT start,deployment_id,jsonb_agg(jsonb_build_object('telemetry_id',id,'received_at',received_at,'status',status,'latency_ms',latency_ms,'represented_requests',count,'trace_id',trace_id) ORDER BY position) AS examples
 FROM ranked WHERE position <= sqlc.arg(example_limit)::integer GROUP BY start,deployment_id
), sides AS (
 SELECT sqlc.arg(candidate_id)::text::uuid AS deployment_id,'candidate'::text AS side
 UNION ALL SELECT sqlc.arg(stable_id)::text::uuid,'stable'::text
), summaries AS (
 SELECT w.start,w."end",jsonb_object_agg(s.side,jsonb_build_object(
 'matching_requests',coalesce(t.matching_requests,0),'observed_rows',coalesce(t.observed_rows,0),
 'examples_truncated',coalesce(t.observed_rows,0) > sqlc.arg(example_limit)::integer,'examples',coalesce(e.examples,'[]'::jsonb))) AS sides
 FROM windows w CROSS JOIN sides s LEFT JOIN totals t ON t.start=w.start AND t.deployment_id=s.deployment_id
 LEFT JOIN examples e ON e.start=w.start AND e.deployment_id=s.deployment_id GROUP BY w.start,w."end"
)
SELECT coalesce(jsonb_agg(jsonb_build_object('start',start,'end',"end",'candidate',sides->'candidate','stable',sides->'stable') ORDER BY start),'[]'::jsonb)::jsonb AS observations FROM summaries;


-- name: RouteHealthLatencyEvidence :many
-- Independently bound the newest rows in each exact deployment/window. Read
-- rows without spans too, so missing coverage is not hidden by selection.
WITH windows AS (
 SELECT (value->>'start')::timestamptz AS start, (value->>'end')::timestamptz AS "end"
 FROM jsonb_array_elements(sqlc.arg(windows)::jsonb)
), ranked AS (
 SELECT w.start,w."end",t.id,t.received_at,t.deployment_id,t.status,t.latency_ms,t.count,t.trace_id,
 t.spans_summary,t.cold_boot,t.guest_duration_ms,t.guest_outcome,t.wake_id,t.instance_id,
 row_number() OVER (PARTITION BY w.start,t.deployment_id ORDER BY t.received_at DESC,t.id DESC) AS position
 FROM windows w JOIN request_telemetry t
 ON t.app_id=sqlc.arg(app_id)::text::uuid AND t.account_id=sqlc.arg(account_id)::text::uuid
 AND t.method=sqlc.arg(method)::text AND t.route=sqlc.arg(method)::text || ' ' || sqlc.arg(path)::text
 AND t.deployment_id IN (sqlc.arg(candidate_id)::text::uuid,sqlc.arg(stable_id)::text::uuid)
 AND t.received_at>=w.start AND t.received_at<w."end"
 AND (sqlc.arg(customer_id)::text=''
  OR (sqlc.arg(customer_group_by)::text='tenant' AND t.platform_tenant_id=NULLIF(sqlc.arg(customer_id)::text,'')::uuid)
  OR (sqlc.arg(customer_group_by)::text='consumer' AND t.consumer_id=NULLIF(sqlc.arg(customer_id)::text,'')::uuid))
), sampled AS MATERIALIZED (
 SELECT * FROM ranked WHERE position<=sqlc.arg(rows_limit)::integer
)
SELECT s.start,s.deployment_id::text AS deployment_id,s.id::text AS telemetry_id,s.received_at,s.status,s.latency_ms,s.count,s.trace_id,
 s.spans_summary,s.cold_boot,s.guest_duration_ms,s.guest_outcome,s.wake_id,
 COALESCE((CASE WHEN wake.started IS NOT NULL AND wake.completed>=wake.started
 AND wake.completed-wake.started<=interval '24 hours'
 THEN (EXTRACT(EPOCH FROM (wake.completed-wake.started))*1000)::bigint END),-1)::bigint AS wake_boot_ms
FROM sampled s LEFT JOIN LATERAL (
 SELECT min(e.at) FILTER (WHERE e.kind='wake.boot_started') AS started,
 min(e.at) FILTER (WHERE e.kind='wake.boot_completed') AS completed
 FROM events e WHERE s.cold_boot AND s.wake_id IS NOT NULL AND nullif(s.instance_id,'') IS NOT NULL AND e.actor='schedd'
 AND e.kind IN ('wake.boot_started','wake.boot_completed')
 AND e.data->>'wake_id'=s.wake_id AND e.data->>'app_id'=sqlc.arg(app_id)::text
 AND e.data->>'instance_id'=s.instance_id
 AND e.at>=s.received_at-interval '24 hours' AND e.at<=s.received_at+interval '30 seconds'
) wake ON true
ORDER BY s.start,s.deployment_id,s.position;

-- name: ReadRouteMonitorConfig :one
SELECT (jsonb_build_object('app_id',a.id,'enabled',coalesce(m.enabled,false),'revision',coalesce(m.revision,0),
	'routes',coalesce(m.routes,'[]'::jsonb),'updated_at',m.updated_at)::jsonb ||
 CASE WHEN coalesce(m.customer_group_by,'')='' THEN '{}'::jsonb ELSE jsonb_build_object('customer_group_by',m.customer_group_by) END)::text AS config
FROM apps a LEFT JOIN route_monitors m ON m.app_id=a.id AND m.account_id=a.account_id
WHERE a.id=sqlc.arg(app_id)::text::uuid AND a.account_id=sqlc.arg(account_id)::text::uuid AND a.status<>'deleted';

-- name: WriteRouteMonitorConfig :exec
INSERT INTO route_monitors(app_id,account_id,enabled,revision,routes,customer_group_by)
VALUES(sqlc.arg(app_id)::text::uuid,sqlc.arg(account_id)::text::uuid,sqlc.arg(enabled),sqlc.arg(revision),sqlc.arg(routes)::jsonb,sqlc.arg(customer_group_by)::text)
ON CONFLICT(app_id) DO UPDATE SET enabled=EXCLUDED.enabled,revision=EXCLUDED.revision,routes=EXCLUDED.routes,customer_group_by=EXCLUDED.customer_group_by,
 updated_at=clock_timestamp(),next_check_at=clock_timestamp(),last_deployment_id=NULL,active_incident_id=NULL,customer_recovery_state='{}'::jsonb;

-- name: ReadRouteMonitorRecoveryCustomers :one
SELECT customer_recovery_state FROM route_monitors WHERE app_id=sqlc.arg(app_id)::text::uuid AND account_id=sqlc.arg(account_id)::text::uuid;

-- name: ListDueRouteMonitors :many
SELECT m.app_id::text AS app_id,m.account_id::text AS account_id,m.revision,m.next_check_at FROM route_monitors m JOIN apps a ON a.id=m.app_id AND a.account_id=m.account_id
WHERE m.enabled AND m.next_check_at<=clock_timestamp() AND a.status<>'deleted'
ORDER BY m.next_check_at,m.app_id LIMIT sqlc.arg(batch_limit)::integer;

-- name: LockRouteMonitor :one
SELECT next_check_at,coalesce(last_deployment_id::text,'')::text AS last_deployment_id,coalesce(active_incident_id::text,'')::text AS active_incident_id
FROM route_monitors WHERE app_id=sqlc.arg(app_id)::text::uuid AND account_id=sqlc.arg(account_id)::text::uuid FOR UPDATE SKIP LOCKED;

-- name: WriteRouteMonitorState :exec
UPDATE route_monitors SET next_check_at=sqlc.arg(next_check_at),last_deployment_id=nullif(sqlc.arg(deployment_id)::text,'')::uuid,
	active_incident_id=nullif(sqlc.arg(incident_id)::text,'')::uuid,customer_recovery_state=sqlc.arg(customer_recovery_state)::jsonb WHERE app_id=sqlc.arg(app_id)::text::uuid AND account_id=sqlc.arg(account_id)::text::uuid;

-- name: RouteMonitorServingDeployments :many
SELECT id::text AS id,commit_sha,created_at,canary_step_started_at,rollout_completed_at,traffic_percent,canary_step,canary_total_steps
FROM deployments WHERE app_id=sqlc.arg(app_id)::text::uuid AND status='live' AND deleted_at IS NULL AND traffic_percent>0
 AND coalesce(nullif(scope,''),'default')='default' ORDER BY id LIMIT 2;

-- name: ReadRouteMonitorIncident :one
SELECT entry FROM route_monitor_incidents WHERE id=sqlc.arg(id)::text::uuid AND app_id=sqlc.arg(app_id)::text::uuid AND account_id=sqlc.arg(account_id)::text::uuid;

-- name: WriteRouteMonitorIncident :exec
INSERT INTO route_monitor_incidents(id,app_id,account_id,deployment_id,revision,status,opened_at,closed_at,encoded_bytes,entry)
VALUES(sqlc.arg(id)::text::uuid,sqlc.arg(app_id)::text::uuid,sqlc.arg(account_id)::text::uuid,sqlc.arg(deployment_id)::text::uuid,sqlc.arg(revision),sqlc.arg(status),sqlc.arg(opened_at),sqlc.narg(closed_at),sqlc.arg(encoded_bytes),sqlc.arg(entry)::jsonb)
ON CONFLICT(id) DO UPDATE SET status=EXCLUDED.status,closed_at=EXCLUDED.closed_at,encoded_bytes=EXCLUDED.encoded_bytes,entry=EXCLUDED.entry;

-- name: ListRouteMonitorIncidents :many
SELECT h.entry FROM route_monitor_incidents h WHERE h.app_id=sqlc.arg(app_id)::text::uuid AND h.account_id=sqlc.arg(account_id)::text::uuid
 AND (sqlc.arg(before_id)::text='' OR (h.opened_at,h.id)<(SELECT c.opened_at,c.id FROM route_monitor_incidents c WHERE c.id=nullif(sqlc.arg(before_id)::text,'')::uuid AND c.app_id=h.app_id AND c.account_id=h.account_id))
ORDER BY h.opened_at DESC,h.id DESC LIMIT sqlc.arg(page_limit)::integer;

-- name: PruneRouteMonitorIncidents :exec
DELETE FROM route_monitor_incidents WHERE id IN (
 SELECT id FROM (SELECT id,row_number() OVER(ORDER BY opened_at DESC,id DESC) AS position,
 sum(encoded_bytes) OVER(ORDER BY opened_at DESC,id DESC ROWS UNBOUNDED PRECEDING) AS total_bytes
 FROM route_monitor_incidents WHERE app_id=sqlc.arg(app_id)::text::uuid AND status<>'open') retained
 WHERE position>sqlc.arg(max_entries)::integer OR total_bytes>sqlc.arg(max_bytes)::bigint
);

-- name: DeferRouteMonitor :exec
-- Fence a failed attempt against a configuration edit or another worker's success.
UPDATE route_monitors SET next_check_at=sqlc.arg(next_check_at)::timestamptz
WHERE app_id=sqlc.arg(app_id)::text::uuid AND account_id=sqlc.arg(account_id)::text::uuid
 AND revision=sqlc.arg(revision)::bigint AND enabled AND next_check_at=sqlc.arg(previous_due_at)::timestamptz;
-- name: RouteMonitorCustomerObservations :one
-- Evaluate the full request-time identity population in bounded route windows.
-- Only five cohorts and one hundred recovery identities per route are returned;
-- full verdict and distinct counts are computed before either output cap.
WITH selected AS (
 SELECT value->>'method' AS method,value->>'path' AS path,
  nullif(value->>'max_5xx_rate_bps','')::bigint AS error_budget,
  coalesce((value->>'max_p95_ms')::bigint,0) AS latency_budget
 FROM jsonb_array_elements(sqlc.arg(routes)::jsonb)
), windows AS (
 SELECT (value->>'start')::timestamptz AS start,(value->>'end')::timestamptz AS "end"
 FROM jsonb_array_elements(sqlc.arg(windows)::jsonb)
), raw AS MATERIALIZED (
 SELECT s.method,s.path,s.error_budget,s.latency_budget,w.start,w."end",rt.status,
  rt.latency_ms,rt.count::bigint AS requests,
  CASE WHEN sqlc.arg(group_by)::text='tenant' THEN pt.id ELSE c.id END AS customer_id,
  CASE WHEN sqlc.arg(group_by)::text='tenant' THEN rt.platform_tenant_id IS NULL ELSE rt.consumer_id IS NULL END AS unattributed,
  CASE WHEN sqlc.arg(group_by)::text='tenant' THEN rt.platform_tenant_id IS NOT NULL AND pt.id IS NULL ELSE rt.consumer_id IS NOT NULL AND c.id IS NULL END AS unresolved
 FROM selected s CROSS JOIN windows w
 JOIN request_telemetry rt ON rt.account_id=sqlc.arg(account_id)::text::uuid AND rt.app_id=sqlc.arg(app_id)::text::uuid
  AND rt.deployment_id=sqlc.arg(deployment_id)::text::uuid AND rt.method=s.method AND rt.route=s.method||' '||s.path
  AND rt.received_at>=w.start AND rt.received_at<w."end"
 LEFT JOIN api_consumers c ON c.id=rt.consumer_id AND c.account_id=rt.account_id AND c.app_id=rt.app_id
 LEFT JOIN platform_tenants pt ON pt.id=rt.platform_tenant_id AND pt.account_id=rt.account_id
), required AS (
 SELECT value->>'method' AS method,value->>'path' AS path,(value->>'customer_id')::uuid AS customer_id
 FROM jsonb_array_elements(sqlc.arg(required_customers)::jsonb)
), identities AS (
 SELECT method,path,customer_id,bool_or(required) AS required FROM (
  SELECT method,path,customer_id,false AS required FROM raw WHERE customer_id IS NOT NULL
  UNION ALL SELECT method,path,customer_id,true AS required FROM required
 ) candidates GROUP BY method,path,customer_id
), cohort_counts AS (
 SELECT i.method,i.path,i.customer_id,i.required,w.start,w."end",
  coalesce(sum(o.requests),0)::bigint AS requests,
  coalesce(sum(o.requests) FILTER(WHERE o.status BETWEEN 500 AND 599),0)::bigint AS errors
 FROM identities i CROSS JOIN windows w LEFT JOIN raw o
  ON o.method=i.method AND o.path=i.path AND o.customer_id=i.customer_id AND o.start=w.start
 GROUP BY i.method,i.path,i.customer_id,i.required,w.start,w."end"
), weighted AS (
 SELECT o.method,o.path,o.customer_id,o.start,o.latency_ms,sum(o.requests)::bigint AS weight
 FROM raw o JOIN identities i USING(method,path,customer_id)
 WHERE o.latency_budget>0 GROUP BY o.method,o.path,o.customer_id,o.start,o.latency_ms
), ranked_latency AS (
 SELECT *,sum(weight) OVER(PARTITION BY method,path,customer_id,start ORDER BY latency_ms ROWS UNBOUNDED PRECEDING) AS cumulative,
  sum(weight) OVER(PARTITION BY method,path,customer_id,start) AS total FROM weighted
), latency_targets AS (
 SELECT *, (total-1)::numeric*sqlc.arg(latency_quantile)::double precision::numeric AS rank FROM ranked_latency
), latency_bounds AS (
 SELECT method,path,customer_id,start,rank,
  min(latency_ms) FILTER(WHERE cumulative>floor(rank)) AS low,
  min(latency_ms) FILTER(WHERE cumulative>ceil(rank)) AS high
 FROM latency_targets GROUP BY method,path,customer_id,start,rank
), percentiles AS (
 SELECT method,path,customer_id,start,
  (low+(rank-floor(rank))*(high-low))::double precision AS p95_ms FROM latency_bounds
), evaluated_windows AS (
 SELECT c.*,
  CASE WHEN s.error_budget IS NULL THEN 'disabled'
   WHEN c.start<sqlc.arg(observation_anchor)::timestamptz OR c.requests<sqlc.arg(minimum_requests)::bigint THEN 'unknown'
   WHEN c.errors::numeric*sqlc.arg(max_rate_bps)::bigint>s.error_budget::numeric*c.requests THEN CASE WHEN c.errors<sqlc.arg(minimum_errors)::bigint THEN 'unknown' ELSE 'violated' END
   ELSE 'healthy' END AS error_status,
  CASE WHEN s.latency_budget=0 THEN 'disabled'
   WHEN c.start<sqlc.arg(observation_anchor)::timestamptz OR c.requests<sqlc.arg(minimum_latency_requests)::bigint OR p.p95_ms IS NULL THEN 'unknown'
   WHEN p.p95_ms>s.latency_budget THEN 'violated' ELSE 'healthy' END AS latency_status,
  p.p95_ms
 FROM cohort_counts c JOIN selected s USING(method,path)
 LEFT JOIN percentiles p USING(method,path,customer_id,start)
), summaries AS (
 SELECT method,path,customer_id,required,
  bool_and(error_status='violated') AS error_violated,
  bool_and(error_status='healthy') AS error_healthy,
  bool_and(error_status='disabled') AS error_disabled,
  bool_and(latency_status='violated') AS latency_violated,
  bool_and(latency_status='healthy') AS latency_healthy,
  bool_and(latency_status='disabled') AS latency_disabled,
  bool_or(requests>0) AS observed,
  jsonb_agg(jsonb_build_object('start',start,'end',"end",'observed',jsonb_build_object('requests',requests,'server_errors',errors,'p95_latency_ms',p95_ms)) ORDER BY start) AS windows
 FROM evaluated_windows GROUP BY method,path,customer_id,required
), classified AS (
 SELECT *,CASE WHEN error_violated OR latency_violated THEN 'violated'
  WHEN NOT(error_healthy OR error_disabled) OR NOT(latency_healthy OR latency_disabled) THEN 'unknown'
  WHEN error_healthy OR latency_healthy THEN 'healthy' ELSE 'disabled' END AS status
 FROM summaries
), population AS (
 SELECT method,path,count(*) FILTER(WHERE observed)::bigint AS observed_customers,
  count(*) FILTER(WHERE observed AND status='violated')::bigint AS violated_customers,
  count(*) FILTER(WHERE observed AND status='unknown')::bigint AS unknown_customers,
  count(*) FILTER(WHERE NOT observed)::bigint AS recovery_missing_customers,
  count(*) FILTER(WHERE required AND status<>'healthy')::bigint AS recovery_remaining_customers,
  coalesce(jsonb_agg(to_jsonb(customer_id) ORDER BY customer_id) FILTER(WHERE observed AND status='violated' AND customer_rank<=sqlc.arg(recovery_limit)::integer),'[]'::jsonb) AS violating_customer_ids,
  count(*) FILTER(WHERE observed AND status='violated')>sqlc.arg(recovery_limit)::integer AS violating_customers_truncated
 FROM (
  SELECT *,row_number() OVER(PARTITION BY method,path ORDER BY customer_id) AS customer_rank FROM classified
 ) q GROUP BY method,path
), display AS (
 SELECT method,path,jsonb_agg(jsonb_build_object('customer_id',customer_id,'observed',observed,'status',status,'windows',windows) ORDER BY CASE status WHEN 'violated' THEN 0 WHEN 'unknown' THEN 1 ELSE 2 END,customer_id) AS customers
 FROM (SELECT *,row_number() OVER(PARTITION BY method,path ORDER BY CASE status WHEN 'violated' THEN 0 WHEN 'unknown' THEN 1 ELSE 2 END,customer_id) AS position FROM classified) q
 WHERE position<=sqlc.arg(customer_limit)::integer GROUP BY method,path
), attribution AS (
 SELECT s.method,s.path,w.start,w."end",
  coalesce(sum(o.requests) FILTER(WHERE o.customer_id IS NOT NULL),0)::bigint AS identified_requests,
  coalesce(sum(o.requests) FILTER(WHERE o.unattributed),0)::bigint AS unattributed_requests,
  coalesce(sum(o.requests) FILTER(WHERE o.unresolved),0)::bigint AS unresolved_identity_requests
 FROM selected s CROSS JOIN windows w LEFT JOIN raw o ON o.method=s.method AND o.path=s.path AND o.start=w.start
 GROUP BY s.method,s.path,w.start,w."end"
), attribution_json AS (
 SELECT method,path,jsonb_agg(jsonb_build_object('start',start,'end',"end",'identified_requests',identified_requests,
  'unattributed_requests',unattributed_requests,'unresolved_identity_requests',unresolved_identity_requests) ORDER BY start) AS windows
 FROM attribution GROUP BY method,path
), global_customers AS (
 SELECT customer_id,bool_or(observed) AS observed,
  bool_or(observed AND status='violated') AS violated,
  bool_or(observed AND status='unknown') AS unknown,
  bool_or(required AND status<>'healthy') AS recovery_remaining
 FROM classified GROUP BY customer_id
), global_population AS (
	 SELECT count(*) FILTER(WHERE observed AND violated)::bigint AS violated_customers,
	  count(*) FILTER(WHERE observed AND NOT violated AND unknown)::bigint AS unknown_customers,
  count(*) FILTER(WHERE recovery_remaining)::bigint AS recovery_remaining_customers
 FROM global_customers
), encoded AS (
 SELECT s.method,s.path,p.observed_customers,p.violated_customers,p.unknown_customers,p.recovery_missing_customers,p.recovery_remaining_customers,
  p.violating_customer_ids,p.violating_customers_truncated,
  coalesce((SELECT count(*) FROM classified x WHERE x.method=s.method AND x.path=s.path AND x.observed AND x.status IN('violated','unknown')),0)::bigint AS nonhealthy_customers,
  coalesce(d.customers,'[]'::jsonb) AS customers,a.windows
 FROM selected s LEFT JOIN population p USING(method,path) LEFT JOIN display d USING(method,path) LEFT JOIN attribution_json a USING(method,path)
)
SELECT jsonb_build_object('group_by',sqlc.arg(group_by)::text,'coverage','observed_only',
 'customers_limit',sqlc.arg(customer_limit)::integer,
 'observed_customers',(SELECT count(DISTINCT customer_id)::bigint FROM raw WHERE customer_id IS NOT NULL),
 'violated_customers',g.violated_customers,'unknown_customers',g.unknown_customers,'recovery_remaining_customers',g.recovery_remaining_customers,
 'routes',coalesce((SELECT jsonb_agg(jsonb_build_object('method',e.method,'path',e.path,
  'observed_customers',e.observed_customers,'violated_customers',e.violated_customers,'unknown_customers',e.unknown_customers,
  'recovery_missing_customers',e.recovery_missing_customers,'recovery_remaining_customers',e.recovery_remaining_customers,
  'customers_truncated',e.observed_customers+e.recovery_missing_customers>jsonb_array_length(e.customers),
  'violating_customers_truncated',e.violating_customers_truncated,
  'violating_customer_ids',e.violating_customer_ids,'windows',e.windows,'customers',e.customers) ORDER BY e.method,e.path) FROM encoded e),'[]'::jsonb)) AS observations
FROM global_population g;

-- name: ReadActiveRouteMonitorIncident :one
SELECT i.entry FROM route_monitors m LEFT JOIN route_monitor_incidents i ON i.id=m.active_incident_id
WHERE m.app_id=sqlc.arg(app_id)::text::uuid AND m.account_id=sqlc.arg(account_id)::text::uuid;
-- name: ReadBindingApplicationAdoption :many
-- One statement reads current managed versions, the complete authorized
-- resident workload roster and independently versioned application receipts.
WITH managed AS (
 SELECT s.account_id, s.app_id, s.scope, s.key, s.delivery_version,
        CASE WHEN s.managed_postgres_binding_id IS NOT NULL THEN 'postgres' ELSE 'object_storage' END::text AS binding_type,
        coalesce(s.managed_postgres_binding_id, s.managed_object_storage_credential_id) AS binding_id
 FROM app_secrets s
 WHERE s.account_id = sqlc.arg(account_id)::uuid AND s.app_id = sqlc.arg(app_id)::uuid
   AND ((s.managed_postgres_binding_id = ANY(sqlc.arg(postgres_ids)::uuid[]) AND s.managed_object_storage_credential_id IS NULL)
     OR (s.managed_object_storage_credential_id = ANY(sqlc.arg(storage_ids)::uuid[]) AND s.managed_postgres_binding_id IS NULL))
), roster AS (
SELECT s.binding_type, s.binding_id, s.delivery_version AS current_version, d.id::text AS deployment_id, s.scope,
       s.key,
       i.id::text AS instance_id,
       ''::text AS workload_name,
       i.state AS runtime_state,
       CASE
         WHEN d.secret_reload_signal IS NULL THEN 'unknown'
         WHEN d.secret_reload_signal = '' THEN 'disabled'
         ELSE 'enabled'
       END AS reload_support,
       o.secret_version,
       o.projection,
       o.signal,
       o.observed_at,
       o.error_code,
       o.application_ack_version,
       o.application_ack_status,
       o.application_ack_at,
       o.application_ack_error_code,
       o.application_ack_generation,
       CASE WHEN process.active THEN process.generation ELSE '' END::text AS process_generation
  FROM instances i
  JOIN deployments d ON d.id = i.deployment_id AND d.app_id = i.app_id
  JOIN managed s ON s.app_id = i.app_id AND s.scope = d.scope
  LEFT JOIN app_secret_runtime_reload_observations o
    ON o.app_id = s.app_id AND o.scope = s.scope AND o.key = s.key AND o.instance_id = i.id
   AND o.workload_name = ''
  LEFT JOIN app_secret_runtime_processes process ON process.instance_id = i.id AND process.app_id = i.app_id AND process.workload_name = ''
 WHERE s.account_id = sqlc.arg(account_id)::uuid
   AND i.app_id = sqlc.arg(app_id)::uuid
   AND i.kind = 'wake' AND i.mode <> 'mirror'
   AND i.state IN ('waking','cold_booting','running','draining','snapshotting','migrating','warm')
   AND ((coalesce(d.override_env_secrets, '{}'::jsonb) = '{}'::jsonb
         AND jsonb_array_length(coalesce(d.sidecars, '[]'::jsonb)) = 0)
        OR d.override_env_secrets ? s.key)
UNION ALL
SELECT s.binding_type, s.binding_id, s.delivery_version AS current_version, d.id::text AS deployment_id, s.scope,
       s.key,
       i.id::text AS instance_id,
       sidecar.value->>'name' AS workload_name,
       i.state AS runtime_state,
       CASE
         WHEN reload.signal IS NULL THEN 'unknown'
         WHEN reload.signal = '' THEN 'disabled'
         ELSE 'enabled'
       END AS reload_support,
       o.secret_version,
       o.projection,
       o.signal,
       o.observed_at,
       o.error_code,
       o.application_ack_version,
       o.application_ack_status,
       o.application_ack_at,
       o.application_ack_error_code,
       o.application_ack_generation,
       CASE WHEN process.active THEN process.generation ELSE '' END::text AS process_generation
  FROM instances i
  JOIN deployments d ON d.id = i.deployment_id AND d.app_id = i.app_id
  JOIN managed s ON s.app_id = i.app_id AND s.scope = d.scope
 CROSS JOIN LATERAL jsonb_array_elements(coalesce(d.sidecars, '[]'::jsonb)) AS sidecar(value)
  LEFT JOIN deployment_sidecar_secret_reload_signals reload
    ON reload.deployment_id = d.id AND reload.sidecar_name = sidecar.value->>'name'
  LEFT JOIN app_secret_runtime_reload_observations o
    ON o.app_id = s.app_id AND o.scope = s.scope AND o.key = s.key AND o.instance_id = i.id
   AND o.workload_name = sidecar.value->>'name'
  LEFT JOIN app_secret_runtime_processes process ON process.instance_id = i.id AND process.app_id = i.app_id AND process.workload_name = sidecar.value->>'name'
 WHERE s.account_id = sqlc.arg(account_id)::uuid
   AND i.app_id = sqlc.arg(app_id)::uuid
   AND i.kind = 'wake' AND i.mode <> 'mirror'
   AND i.state IN ('waking','cold_booting','running','draining','snapshotting','migrating','warm')
   AND sidecar.value->>'type' = 'sidecar'
   AND sidecar.value->'env_secrets'->>s.key = 'secret:' || s.key
)
SELECT m.binding_type, m.binding_id, m.scope, m.key, m.delivery_version AS current_version,
       coalesce(r.deployment_id, '')::text AS deployment_id,
       coalesce(r.instance_id, '')::text AS instance_id,
       coalesce(r.workload_name, '')::text AS workload_name,
       coalesce(r.runtime_state, '')::text AS runtime_state,
       coalesce(r.reload_support, '')::text AS reload_support,
       coalesce(r.secret_version, 0)::bigint AS reload_version,
       coalesce(r.projection, '')::text AS projection,
       coalesce(r.signal, '')::text AS signal, r.observed_at,
       coalesce(r.application_ack_version, 0)::bigint AS application_ack_version,
       coalesce(r.application_ack_status, '')::text AS application_ack_status,
       r.application_ack_at,
       coalesce(r.application_ack_generation, '')::text AS application_ack_generation,
       coalesce(r.process_generation, '')::text AS process_generation
FROM managed m LEFT JOIN roster r ON r.binding_type = m.binding_type AND r.binding_id = m.binding_id AND r.scope = m.scope AND r.key = m.key
ORDER BY m.binding_type, m.binding_id, m.scope, m.key, instance_id, workload_name;
-- name: AppManagedPostgresBindingInventory :many
SELECT b.id AS binding_id, d.name AS database_name, b.scope, b.environment_key, b.access, b.state,
       b.credential_generation, (COALESCE(b.rotation_previous_generation, 0) > 0) AS rotation_pending,
       COALESCE(b.rotation_wake_id::text, '')::text AS rotation_wake_id
FROM managed_postgres_bindings b
JOIN managed_postgres_databases d ON d.id = b.database_id AND d.account_id = b.account_id
WHERE b.account_id = sqlc.arg(account_id) AND b.app_id = sqlc.arg(app_id)
  AND b.state <> 'deleted' AND d.state <> 'deleted'
  AND (sqlc.arg(scope_filter)::text = '' OR b.scope = sqlc.arg(scope_filter)::text)
ORDER BY d.name, b.environment_key, b.scope;
-- name: AppObjectStorageBindingInventory :many
SELECT c.id AS binding_id, b.name AS bucket_name, c.managed_scope AS scope, c.managed_prefix AS prefix,
	       COALESCE((SELECT stage.id::text FROM object_storage_s3_credentials stage
	                 WHERE stage.rotation_parent_id = c.id AND stage.account_id = c.account_id
	                 ORDER BY stage.created_at DESC, stage.id DESC LIMIT 1), '')::text AS rotation_revision_id,
       c.permission, c.status AS state,
       EXISTS (SELECT 1 FROM object_storage_s3_credentials stage
               WHERE stage.rotation_parent_id = c.id AND stage.account_id = c.account_id
                 AND stage.status = 'active') AS rotation_pending,
       COALESCE((SELECT stage.rotation_wake_id::text FROM object_storage_s3_credentials stage
                 WHERE stage.rotation_parent_id = c.id AND stage.account_id = c.account_id
                   AND stage.status = 'active' ORDER BY stage.id LIMIT 1), '')::text AS rotation_wake_id
FROM object_storage_s3_credentials c
JOIN object_buckets b ON b.id = c.bucket_id AND b.account_id = c.account_id
WHERE c.account_id = sqlc.arg(account_id) AND c.managed_app_id = sqlc.arg(app_id)
  AND b.app_id = sqlc.arg(app_id) AND b.state <> 'deleted'
  AND c.status = 'active' AND c.rotation_parent_id IS NULL
  AND (sqlc.arg(scope_filter)::text = '' OR c.managed_scope = sqlc.arg(scope_filter)::text)
ORDER BY b.name, c.managed_prefix, c.managed_scope;
-- name: AppQueueBindingConsumerInventory :many
SELECT b.id AS binding_id, COALESCE(consumer.id::text, '') AS consumer_id,
       COALESCE(consumer.enabled, false) AS consumer_enabled,
       h.last_poll_at, h.last_success_at, h.last_error_at
FROM queue_bindings b
LEFT JOIN LATERAL (
    SELECT t.id, t.enabled FROM triggers t
    WHERE t.app_id = b.app_id AND t.kind = 'queue' AND t.source = 'queue'
      AND t.config->>'queue_binding_id' = b.id::text
    ORDER BY t.created_at, t.id LIMIT 1
) consumer ON true
LEFT JOIN trigger_consumer_health h ON h.trigger_id = consumer.id
WHERE b.account_id = sqlc.arg(account_id) AND b.app_id = sqlc.arg(app_id)
ORDER BY b.created_at, b.id;
-- name: CreateServiceBindingSmokeTask :one
INSERT INTO app_tasks (
 account_id, app_id, deployment_id, kind, command, command_shell,
 deployment_scope, artifact_key, image_digest, timeout_seconds, max_output_bytes,
 created_at, updated_at)
SELECT a.account_id, a.id, d.id, 'manual', sqlc.arg(command)::text[], false,
 COALESCE(NULLIF(d.scope, ''), 'default'), d.rootfs_key, d.image_digest,
 sqlc.arg(timeout_seconds), sqlc.arg(max_output_bytes), sqlc.arg(created_at), sqlc.arg(created_at)
FROM apps a JOIN deployments d ON d.app_id = a.id
WHERE a.id = sqlc.arg(app_id) AND a.account_id = sqlc.arg(account_id)
 AND a.status <> 'deleted' AND d.id = sqlc.arg(deployment_id)
 AND d.status = 'live' AND d.rootfs_key IS NOT NULL AND d.rootfs_key <> '' AND d.image_digest <> ''
FOR SHARE OF a, d
RETURNING app_tasks.*;

-- name: CreateBindingVerificationTask :one
INSERT INTO app_tasks (account_id, app_id, deployment_id, kind, command, command_shell,
 deployment_scope, artifact_key, image_digest, timeout_seconds, max_output_bytes,
 created_at, updated_at, binding_verification)
SELECT a.account_id, a.id, d.id, 'manual', sqlc.arg(command)::text[], false,
 COALESCE(NULLIF(d.scope, ''), 'default'), d.rootfs_key, d.image_digest,
 sqlc.arg(timeout_seconds), sqlc.arg(max_output_bytes), sqlc.arg(created_at), sqlc.arg(created_at),
 sqlc.arg(binding_verification)::jsonb
FROM apps a JOIN deployments d ON d.app_id = a.id
WHERE a.id = sqlc.arg(app_id) AND a.account_id = sqlc.arg(account_id)
 AND a.status <> 'deleted' AND d.id = sqlc.arg(deployment_id)
 AND d.rootfs_key IS NOT NULL AND d.rootfs_key <> '' AND d.image_digest <> ''
 AND d.status IN ('imaging', 'snapshotting', 'live', 'superseded')
 AND (NOT sqlc.arg(require_live_deployment)::boolean OR d.status = 'live')
FOR SHARE OF d
RETURNING id;
-- name: ListBindingVerificationTasks :many
SELECT DISTINCT ON (binding_verification->>'type', binding_verification->>'binding', deployment_scope)
 binding_verification, deployment_id, deployment_scope, status,
 CASE WHEN octet_length(stdout_tail) <= 4096 THEN stdout_tail ELSE '' END AS stdout,
 (output_truncated OR octet_length(stdout_tail) > 4096) AS truncated,
 exit_code, created_at, finished_at
FROM app_tasks
WHERE account_id = sqlc.arg(account_id) AND app_id = sqlc.arg(app_id)
 AND binding_verification IS NOT NULL
 AND binding_verification->>'type' = ANY(sqlc.arg(kinds)::text[])
 AND (NOT sqlc.arg(exact_deployment)::boolean OR deployment_id = sqlc.narg(selected_deployment_id)::uuid)
ORDER BY binding_verification->>'type', binding_verification->>'binding', deployment_scope,
 CASE WHEN deployment_id = sqlc.narg(selected_deployment_id)::uuid THEN 0 ELSE 1 END, created_at DESC, id DESC;
-- name: AppBindingRuntimeInventory :many
WITH resident AS (
 SELECT i.deployment_id, i.state, i.started_at FROM instances i
 JOIN apps a ON a.id = i.app_id AND a.account_id = sqlc.arg(account_id)
 WHERE i.app_id = sqlc.arg(app_id) AND i.kind = 'wake' AND i.mode <> 'mirror'
   AND i.state IN ('running','waking','cold_booting','warm','snapshotting','draining','migrating')
), selected AS (
 SELECT d.id, d.status, COALESCE(NULLIF(d.scope, ''), 'default') AS scope FROM deployments d
 WHERE d.app_id = sqlc.arg(app_id)
   AND (sqlc.arg(scope_filter)::text = '' OR COALESCE(NULLIF(d.scope, ''), 'default') = sqlc.arg(scope_filter)::text)
   AND (d.status = 'live' OR EXISTS (SELECT 1 FROM resident i WHERE i.deployment_id = d.id))
)
SELECT c.changed_at, COALESCE(d.id::text, '')::text AS deployment_id,
       COALESCE(d.scope, '')::text AS scope, COALESCE(d.status, '')::text AS deployment_status,
       COALESCE(i.state, '')::text AS instance_state, i.started_at
FROM apps a
LEFT JOIN app_runtime_config_changes c ON c.app_id = a.id
LEFT JOIN selected d ON true
LEFT JOIN resident i ON i.deployment_id = d.id
WHERE a.id = sqlc.arg(app_id) AND a.account_id = sqlc.arg(account_id)
ORDER BY d.scope, d.id;
-- name: AppBindingRefreshInventory :many
SELECT DISTINCT ON (o.payload::jsonb->>'wake_id')
 (o.payload::jsonb->>'wake_id')::text AS wake_id,
 CASE o.state WHEN 'pending' THEN CASE WHEN o.attempts > 0 THEN 'retrying' ELSE 'queued' END
 WHEN 'processing' THEN 'running' WHEN 'delivered' THEN 'completed' WHEN 'dead_letter' THEN 'failed'
 ELSE 'unknown' END::text AS status, o.attempts,
 CASE WHEN COALESCE(o.last_error, '') = '' THEN ''
 WHEN position('reason=telemetry_missing' in o.last_error) > 0 THEN 'telemetry_missing'
 WHEN position('reason=requests_active' in o.last_error) > 0 THEN 'requests_active'
 WHEN position('reason=quiet_period_not_elapsed' in o.last_error) > 0 THEN 'quiet_period_not_elapsed'
 ELSE 'restart_attempt_failed' END::text AS failure_reason,
 o.created_at AS requested_at, o.delivered_at AS completed_at
FROM notification_outbox o
JOIN apps a ON a.id = sqlc.arg(app_id) AND a.account_id = sqlc.arg(account_id)
WHERE o.channel = 'runtime_config_restart' AND o.payload::jsonb->>'app_id' = a.id::text
 AND o.payload::jsonb->>'wake_id' = ANY(sqlc.arg(wake_ids)::text[])
ORDER BY o.payload::jsonb->>'wake_id', o.id DESC;
-- name: ReadBindingPromotionRevision :one
SELECT (r.epoch::text || ':' || r.revision::text)::text AS revision
FROM app_binding_promotion_revisions r JOIN apps a ON a.id=r.app_id
WHERE r.app_id=sqlc.arg(app_id) AND a.account_id=sqlc.arg(account_id) AND a.status<>'deleted';
-- name: LockBindingPromotionRevision :one
SELECT (r.epoch::text || ':' || r.revision::text)::text AS revision
FROM app_binding_promotion_revisions r JOIN apps a ON a.id=r.app_id
WHERE r.app_id=sqlc.arg(app_id) AND a.account_id=sqlc.arg(account_id) AND a.status<>'deleted'
FOR UPDATE OF r;
-- name: GetOutboundBindingProbePolicy :one
SELECT p.method, p.path, p.expected_status FROM outbound_integration_probe_policies p
JOIN outbound_integrations i ON i.id=p.integration_id AND i.account_id=p.account_id
WHERE p.integration_id=sqlc.arg(integration_id) AND p.account_id=sqlc.arg(account_id) AND i.owner_kind='customer';
-- name: SetOutboundBindingProbePolicy :one
INSERT INTO outbound_integration_probe_policies(integration_id,account_id,method,path,expected_status)
SELECT i.id,i.account_id,sqlc.arg(method),sqlc.arg(path),sqlc.arg(expected_status) FROM outbound_integrations i
WHERE i.id=sqlc.arg(integration_id) AND i.account_id=sqlc.arg(account_id) AND i.owner_kind='customer' AND enabled
ON CONFLICT(integration_id) DO UPDATE SET method=EXCLUDED.method,path=EXCLUDED.path,expected_status=EXCLUDED.expected_status
RETURNING integration_id;
-- name: DeleteOutboundBindingProbePolicy :execrows
DELETE FROM outbound_integration_probe_policies p USING outbound_integrations i
WHERE i.id=sqlc.arg(integration_id) AND i.account_id=sqlc.arg(account_id) AND i.owner_kind='customer' AND p.integration_id=i.id;
-- name: ListOutboundBindingProbeSnapshots :many
SELECT i.id, p.method, p.path, p.expected_status,
 (to_jsonb(i)-'updated_at'-'created_at')::text AS integration_facts,
 to_jsonb(b)::text AS binding_facts, a.plan,
 (encode(sha256(coalesce(c.authorization_sealed,''::bytea)), 'hex') || ':' || coalesce(c.updated_at::text,''))::text AS credential_revision
FROM outbound_app_bindings b
JOIN outbound_integrations i ON i.id=b.integration_id AND i.account_id=b.account_id
JOIN accounts a ON a.id=b.account_id
JOIN apps app ON app.id=b.app_id AND app.account_id=b.account_id AND app.status<>'deleted'
LEFT JOIN outbound_integration_credentials c ON c.integration_id=i.id AND c.account_id=i.account_id
LEFT JOIN outbound_integration_probe_policies p ON p.integration_id=i.id AND p.account_id=i.account_id AND i.credential_source='customer_sealed'
WHERE b.account_id=sqlc.arg(account_id) AND b.app_id=sqlc.arg(app_id)
ORDER BY i.id;
-- name: EnsureAppSecretRuntimeProcess :execrows
INSERT INTO app_secret_runtime_processes(instance_id, app_id, workload_name)
SELECT i.id, i.app_id, sqlc.arg(workload_name)::text
FROM instances i JOIN apps a ON a.id = i.app_id
WHERE i.id = sqlc.arg(instance_id)::uuid AND i.app_id = sqlc.arg(app_id)::uuid
  AND a.account_id = sqlc.arg(account_id)::uuid
ON CONFLICT (instance_id, workload_name) DO NOTHING;
-- name: LockAppSecretRuntimeProcess :one
SELECT p.generation, p.active, p.started_at
FROM app_secret_runtime_processes p JOIN apps a ON a.id = p.app_id
WHERE p.instance_id = sqlc.arg(instance_id)::uuid AND p.app_id = sqlc.arg(app_id)::uuid
  AND p.workload_name = sqlc.arg(workload_name)::text AND a.account_id = sqlc.arg(account_id)::uuid
FOR UPDATE OF p;
-- name: SetAppSecretRuntimeProcess :execrows
UPDATE app_secret_runtime_processes SET generation = sqlc.arg(generation)::text,
  active = sqlc.arg(active)::boolean, started_at = sqlc.arg(started_at)::timestamptz
WHERE instance_id = sqlc.arg(instance_id)::uuid AND app_id = sqlc.arg(app_id)::uuid
  AND workload_name = sqlc.arg(workload_name)::text;
-- name: ClearAppSecretRuntimeProcessAck :execrows
UPDATE app_secret_runtime_reload_observations
SET application_ack_version = NULL, application_ack_status = NULL,
    application_ack_at = NULL, application_ack_error_code = NULL, application_ack_generation = ''
WHERE instance_id = sqlc.arg(instance_id)::uuid AND app_id = sqlc.arg(app_id)::uuid
  AND workload_name = sqlc.arg(workload_name)::text;
-- name: RecordAppSecretRuntimeProcessAck :execrows
UPDATE app_secret_runtime_reload_observations o
SET application_ack_version = sqlc.arg(secret_version)::bigint,
    application_ack_status = sqlc.arg(status)::text, application_ack_at = sqlc.arg(ack_at)::timestamptz,
    application_ack_error_code = nullif(sqlc.arg(error_code)::text, ''),
    application_ack_generation = sqlc.arg(generation)::text
WHERE o.app_id = sqlc.arg(app_id)::uuid AND o.scope = sqlc.arg(scope)::text AND o.key = sqlc.arg(key)::text
  AND o.instance_id = sqlc.arg(instance_id)::uuid AND o.workload_name = sqlc.arg(workload_name)::text
  AND o.secret_version <= sqlc.arg(secret_version)::bigint AND coalesce(o.application_ack_version, 0) <= sqlc.arg(secret_version)::bigint
  AND EXISTS (SELECT 1 FROM app_secrets s JOIN instances i ON i.id = sqlc.arg(instance_id)::uuid AND i.app_id = s.app_id
      WHERE s.account_id = sqlc.arg(account_id)::uuid AND s.app_id = sqlc.arg(app_id)::uuid
        AND s.scope = sqlc.arg(scope)::text AND s.key = sqlc.arg(key)::text AND s.delivery_version = sqlc.arg(secret_version)::bigint);

-- ADR-508: preserve only current execution receipts on projection writes.
-- name: RecordAppSecretRuntimeProjection :execrows
insert into app_secret_runtime_reload_observations
				(app_id, scope, key, instance_id, workload_name, secret_version, projection, signal, observed_at, error_code)
			 select s.app_id, s.scope, s.key, i.id, sqlc.arg(workload_name)::text, sqlc.arg(secret_version)::bigint, sqlc.arg(projection)::text, sqlc.arg(signal)::text, sqlc.arg(observed_at)::timestamptz, nullif(sqlc.arg(error_code)::text, '')
			 from app_secrets s
			 join instances i on i.id = sqlc.arg(instance_id)::uuid and i.app_id = s.app_id
			 where s.account_id = sqlc.arg(account_id)::uuid and s.app_id = sqlc.arg(app_id)::uuid and s.scope = sqlc.arg(scope)::text and s.key = sqlc.arg(key)::text
			   and s.delivery_version = sqlc.arg(secret_version)::bigint
			 on conflict (app_id, scope, key, instance_id, workload_name) do update
			 set secret_version = excluded.secret_version,
				     projection = excluded.projection,
				     signal = excluded.signal,
				     observed_at = excluded.observed_at,
				     error_code = excluded.error_code,
				     application_ack_version = CASE WHEN app_secret_runtime_reload_observations.application_ack_version >= excluded.secret_version THEN app_secret_runtime_reload_observations.application_ack_version END,
				     application_ack_status = CASE WHEN app_secret_runtime_reload_observations.application_ack_version >= excluded.secret_version THEN app_secret_runtime_reload_observations.application_ack_status END,
				     application_ack_at = CASE WHEN app_secret_runtime_reload_observations.application_ack_version >= excluded.secret_version THEN app_secret_runtime_reload_observations.application_ack_at END,
				     application_ack_error_code = CASE WHEN app_secret_runtime_reload_observations.application_ack_version >= excluded.secret_version THEN app_secret_runtime_reload_observations.application_ack_error_code END,
				     application_ack_generation = CASE WHEN app_secret_runtime_reload_observations.application_ack_version >= excluded.secret_version THEN app_secret_runtime_reload_observations.application_ack_generation ELSE '' END
			 where app_secret_runtime_reload_observations.secret_version <= excluded.secret_version;
-- name: ListAppSecretRuntimeProcessObservations :many
select o.scope, o.key, o.instance_id::text AS instance_id, o.workload_name, o.secret_version,
		        o.projection, o.signal, o.observed_at, coalesce(o.error_code, '')::text AS error_code,
	        coalesce(o.application_ack_version, 0)::bigint AS application_ack_version, coalesce(o.application_ack_status, '')::text AS application_ack_status,
	        o.application_ack_at, coalesce(o.application_ack_error_code, '')::text AS application_ack_error_code, o.application_ack_generation
	   from app_secret_runtime_reload_observations o
	   join app_secrets s on s.app_id = o.app_id and s.scope = o.scope and s.key = o.key
	   join instances i on i.id = o.instance_id and i.app_id = o.app_id
	  where s.account_id = sqlc.arg(account_id)::uuid and o.app_id = sqlc.arg(app_id)::uuid and (sqlc.arg(scope)::text = '' or o.scope = sqlc.arg(scope)::text)
	    and i.state in ('waking','cold_booting','running','draining','snapshotting','migrating','warm')
	  order by o.scope asc, o.key asc, o.instance_id asc, o.workload_name asc;

-- name: ValidateBoundQueueInvocationClaim :one
SELECT EXISTS(SELECT 1 FROM invocations i JOIN apps a ON a.id=i.app_id AND a.account_id=i.account_id
 JOIN queue_bindings b ON b.id=i.queue_binding_id AND b.app_id=i.app_id AND b.account_id=i.account_id
 JOIN project_environments e ON e.id=b.environment_id AND e.project_id=a.project_id AND e.account_id=a.account_id
 WHERE i.id=sqlc.arg(invocation_id)::uuid AND i.environment_id IS NULL AND i.source='queue'
 AND i.work_policy_name IS NULL AND i.cron_id IS NULL AND i.on_success_destination_id IS NULL AND i.on_failure_destination_id IS NULL
 AND i.deployment_scope=b.deployment_scope AND b.deployment_scope=e.slug AND b.retired_at IS NULL AND a.status<>'deleted'
 AND (NOT i.headers ? 'X-Gregale-Revision' OR EXISTS(SELECT 1 FROM deployments d WHERE d.id::text=lower(i.headers->>'X-Gregale-Revision') AND d.app_id=i.app_id AND d.scope=e.slug))
 AND (NOT i.headers ? 'X-Gregale-Release' OR EXISTS(SELECT 1 FROM project_release_sets r WHERE r.id::text=lower(i.headers->>'X-Gregale-Release') AND r.project_id=e.project_id AND r.account_id=e.account_id AND r.environment_slug=e.slug)))::boolean AS valid;

-- name: ReadBoundOrProductionQueueTriggerInvocation :one
SELECT i.* FROM invocations i WHERE i.id=sqlc.arg(invocation_id) AND i.app_id=sqlc.arg(app_id) AND i.source='queue' AND i.environment_id IS NULL
 AND NOT EXISTS(SELECT 1 FROM invocation_environment_queue_receipts r WHERE r.invocation_id=i.id)
 AND (exists (select 1 from queue_bindings accepted where accepted.id=i.queue_binding_id
 and accepted.app_id=i.app_id and accepted.account_id=i.account_id and accepted.deployment_scope<>''
 and accepted.deployment_scope=i.deployment_scope) OR (not exists (select 1 from deployments stage
              where stage.app_id=i.app_id and stage.scope not in ('production','default')
                and exists (select 1 from jsonb_each_text(case when jsonb_typeof(i.headers)='object' then i.headers else '{}'::jsonb end) pin
                    where lower(pin.key)='x-gregale-revision' and translate(regexp_replace(lower(pin.value), '[[:space:]]|(^urn:uuid:)|[{}]', '', 'g'), '-', '')=replace(stage.id::text,'-','')))
          and not exists (select 1 from project_release_sets stage join apps owner
              on owner.project_id=stage.project_id and owner.account_id=stage.account_id
              where owner.id=i.app_id and stage.environment_slug not in ('production','default')
                and exists (select 1 from jsonb_each_text(case when jsonb_typeof(i.headers)='object' then i.headers else '{}'::jsonb end) pin
                    where lower(pin.key)='x-gregale-release' and translate(regexp_replace(lower(pin.value), '[[:space:]]|(^urn:uuid:)|[{}]', '', 'g'), '-', '')=replace(stage.id::text,'-','')))));

-- name: LockBoundOrProductionQueueTriggerInvocation :one
SELECT i.id FROM invocations i WHERE i.id=sqlc.arg(invocation_id) AND i.app_id=sqlc.arg(app_id) AND i.source='queue' AND i.state='pending' AND i.environment_id IS NULL
 AND NOT EXISTS(SELECT 1 FROM invocation_environment_queue_receipts r WHERE r.invocation_id=i.id)
 AND (exists (select 1 from queue_bindings accepted where accepted.id=i.queue_binding_id
 and accepted.app_id=i.app_id and accepted.account_id=i.account_id and accepted.deployment_scope<>''
 and accepted.deployment_scope=i.deployment_scope) OR (not exists (select 1 from deployments stage
              where stage.app_id=i.app_id and stage.scope not in ('production','default')
                and exists (select 1 from jsonb_each_text(case when jsonb_typeof(i.headers)='object' then i.headers else '{}'::jsonb end) pin
                    where lower(pin.key)='x-gregale-revision' and translate(regexp_replace(lower(pin.value), '[[:space:]]|(^urn:uuid:)|[{}]', '', 'g'), '-', '')=replace(stage.id::text,'-','')))
          and not exists (select 1 from project_release_sets stage join apps owner
              on owner.project_id=stage.project_id and owner.account_id=stage.account_id
              where owner.id=i.app_id and stage.environment_slug not in ('production','default')
                and exists (select 1 from jsonb_each_text(case when jsonb_typeof(i.headers)='object' then i.headers else '{}'::jsonb end) pin
                    where lower(pin.key)='x-gregale-release' and translate(regexp_replace(lower(pin.value), '[[:space:]]|(^urn:uuid:)|[{}]', '', 'g'), '-', '')=replace(stage.id::text,'-',''))))) FOR UPDATE OF i SKIP LOCKED;

-- name: RetainCustomerOperationIdempotency :exec
UPDATE customer_operation_idempotency SET expires_at=greatest(expires_at,sqlc.arg(expires_at)::timestamptz)
WHERE operation_id=sqlc.arg(operation_id)::uuid;


-- name: LockCustomerOperationCodeApp :one
SELECT id FROM apps WHERE id=sqlc.arg(app_id)::uuid AND account_id=sqlc.arg(account_id)::uuid
AND status<>'deleted' FOR SHARE;


-- name: CustomerOperationReleaseMemberCount :one
SELECT count(*) FROM (
    SELECT 1 FROM project_release_sets rs JOIN project_release_members rm ON rm.release_id=rs.id
    WHERE rs.id=sqlc.arg(release_id)::uuid AND rs.account_id=sqlc.arg(account_id)::uuid
    AND rs.environment_slug=sqlc.arg(scope)::text
    AND EXISTS(SELECT 1 FROM project_release_members source WHERE source.release_id=rs.id
        AND source.app_id=sqlc.arg(app_id)::uuid AND source.deployment_id=sqlc.arg(deployment_id)::uuid)
    LIMIT sqlc.arg(member_limit)::integer
) members;


-- name: LockCustomerOperationReleaseApps :many
SELECT a.id FROM apps a JOIN project_release_sets rs ON rs.project_id=a.project_id AND rs.account_id=a.account_id
WHERE rs.id=sqlc.arg(release_id)::uuid AND rs.account_id=sqlc.arg(account_id)::uuid
AND a.status<>'deleted' AND EXISTS(SELECT 1 FROM project_release_members rm WHERE rm.release_id=rs.id AND rm.app_id=a.id)
ORDER BY a.id LIMIT sqlc.arg(member_limit)::integer FOR SHARE OF a;


-- name: LockCustomerOperationReleaseDeployments :many
SELECT d.id FROM project_release_sets rs JOIN project_release_members rm ON rm.release_id=rs.id
JOIN apps a ON a.id=rm.app_id AND a.account_id=rs.account_id AND a.project_id=rs.project_id AND a.status<>'deleted'
JOIN deployments d ON d.id=rm.deployment_id AND d.app_id=a.id AND d.scope=rs.environment_slug AND d.status='live'
WHERE rs.id=sqlc.arg(release_id)::uuid AND rs.account_id=sqlc.arg(account_id)::uuid
ORDER BY a.id,d.id LIMIT sqlc.arg(member_limit)::integer FOR SHARE OF d;


-- name: PinCustomerOperationReleaseMembers :execrows
INSERT INTO customer_operation_code_pins(deployment_id,app_id,expires_at)
SELECT d.id,d.app_id,sqlc.arg(expires_at)::timestamptz FROM project_release_sets rs
JOIN project_release_members source ON source.release_id=rs.id AND source.app_id=sqlc.arg(app_id)::uuid AND source.deployment_id=sqlc.arg(deployment_id)::uuid
JOIN apps origin ON origin.id=source.app_id AND origin.account_id=rs.account_id AND origin.project_id=rs.project_id AND origin.status<>'deleted'
JOIN deployments origin_dep ON origin_dep.id=source.deployment_id AND origin_dep.app_id=origin.id AND origin_dep.scope=rs.environment_slug
JOIN project_release_members rm ON rm.release_id=rs.id
JOIN apps a ON a.id=rm.app_id AND a.account_id=rs.account_id AND a.project_id=rs.project_id AND a.status<>'deleted'
JOIN deployments d ON d.id=rm.deployment_id AND d.app_id=a.id AND d.scope=rs.environment_slug
WHERE rs.id=sqlc.arg(release_id)::uuid AND rs.account_id=sqlc.arg(account_id)::uuid AND rs.environment_slug=sqlc.arg(scope)::text
ORDER BY a.id,d.id LIMIT sqlc.arg(member_limit)::integer
ON CONFLICT(deployment_id) DO UPDATE SET expires_at=greatest(customer_operation_code_pins.expires_at,excluded.expires_at);


-- name: DeactivateProjectReleaseSets :exec
UPDATE project_release_sets SET active=false,expires_at=now()+(ttl_seconds*interval '1 second')
WHERE project_id=sqlc.arg(project_id)::uuid AND environment_slug=sqlc.arg(environment)::text AND active
AND (sqlc.narg(release_id)::uuid IS NULL OR id=sqlc.narg(release_id)::uuid);


-- name: RetireLiveDeploymentSiblings :execrows
UPDATE deployments d SET status = CASE WHEN
    EXISTS(SELECT 1 FROM deployment_revision_pins p WHERE p.deployment_id=d.id AND p.expires_at>now())
    OR EXISTS(SELECT 1 FROM project_release_members rm JOIN project_release_sets rs ON rs.id=rm.release_id
        WHERE rm.deployment_id=d.id AND (rs.active OR rs.expires_at>now()))
    OR EXISTS(SELECT 1 FROM customer_operation_retained_deployment_refs retained WHERE retained.deployment_id=d.id)
    THEN 'live' ELSE 'superseded' END, traffic_percent=0
WHERE d.app_id=sqlc.arg(app_id)::uuid AND d.scope=sqlc.arg(scope)::text
AND d.status='live' AND d.id<>sqlc.arg(deployment_id)::uuid;


-- name: SetRetainedServiceRolloutSiblingTraffic :execrows
UPDATE deployments d SET status = CASE WHEN sqlc.arg(traffic_percent)::integer>0
    OR EXISTS(SELECT 1 FROM deployment_revision_pins p WHERE p.deployment_id=d.id AND p.expires_at>now())
    OR EXISTS(SELECT 1 FROM project_release_members rm JOIN project_release_sets rs ON rs.id=rm.release_id
        WHERE rm.deployment_id=d.id AND (rs.active OR rs.expires_at>now()))
    OR EXISTS(SELECT 1 FROM customer_operation_retained_deployment_refs retained WHERE retained.deployment_id=d.id)
    THEN 'live' ELSE 'superseded' END, traffic_percent=sqlc.arg(traffic_percent)::integer
WHERE d.id=sqlc.arg(deployment_id)::uuid;


-- name: ClearServiceRolloutPredecessorPin :exec
DELETE FROM deployment_revision_pins WHERE deployment_id=sqlc.arg(deployment_id)::uuid;


-- name: LockExpiredRevisionPinApps :many
SELECT a.id FROM apps a WHERE EXISTS(SELECT 1 FROM deployment_code_pin_deadlines p WHERE p.app_id=a.id AND p.expires_at<=now()
        AND NOT EXISTS(SELECT 1 FROM customer_operation_retained_deployment_refs retained WHERE retained.deployment_id=p.deployment_id)
        AND NOT EXISTS(SELECT 1 FROM project_release_members rm JOIN project_release_sets rs ON rs.id=rm.release_id
            WHERE rm.deployment_id=p.deployment_id AND (rs.active OR rs.expires_at>now())))
ORDER BY a.id LIMIT sqlc.arg(page_limit)::integer FOR UPDATE OF a;

-- Admission locks apps before deployments. A fresh READ COMMITTED snapshot
-- after app-lock acquisition sees references published while waiting. Both
-- receipt kinds must have expired; one page locks at most page_limit deployments.

-- name: ExpireRetainedDeploymentRevisionPins :execrows
WITH locked_deployments AS MATERIALIZED (
    SELECT d.id,
        EXISTS(SELECT 1 FROM deployment_revision_pins public_pin WHERE public_pin.deployment_id=d.id) AS has_public,
        EXISTS(SELECT 1 FROM customer_operation_code_pins private_pin WHERE private_pin.deployment_id=d.id) AS has_private
    FROM deployments d JOIN deployment_code_pin_deadlines p ON p.deployment_id=d.id AND p.app_id=d.app_id
    WHERE d.app_id=ANY(sqlc.arg(app_ids)::uuid[]) AND p.expires_at<=now()
    AND NOT EXISTS(SELECT 1 FROM customer_operation_retained_deployment_refs retained WHERE retained.deployment_id=d.id)
    AND NOT EXISTS(SELECT 1 FROM project_release_members rm JOIN project_release_sets rs ON rs.id=rm.release_id
        WHERE rm.deployment_id=d.id AND (rs.active OR rs.expires_at>now()))
    ORDER BY d.app_id,d.id LIMIT sqlc.arg(page_limit)::integer FOR UPDATE OF d
), expired_public AS (
    DELETE FROM deployment_revision_pins p USING locked_deployments d WHERE p.deployment_id=d.id AND p.expires_at<=now()
    RETURNING p.deployment_id
), expired_private AS (
    DELETE FROM customer_operation_code_pins p USING locked_deployments d WHERE p.deployment_id=d.id AND p.expires_at<=now()
    RETURNING p.deployment_id
), expired AS (
    SELECT d.id AS deployment_id FROM locked_deployments d
    WHERE (NOT d.has_public OR EXISTS(SELECT 1 FROM expired_public p WHERE p.deployment_id=d.id))
    AND (NOT d.has_private OR EXISTS(SELECT 1 FROM expired_private p WHERE p.deployment_id=d.id))
)
UPDATE deployments d SET status='superseded',traffic_percent=0 FROM expired e
WHERE d.id=e.deployment_id AND d.status='live' AND d.traffic_percent=0;


-- name: ResolveRetainedProjectRelease :one
SELECT rs.id::text AS release_id,coalesce(rm.deployment_id::text,'')::text AS deployment_id
FROM apps a JOIN project_release_sets rs ON rs.project_id=a.project_id AND rs.account_id=a.account_id
LEFT JOIN project_release_members rm ON rm.release_id=rs.id AND rm.app_id=a.id
WHERE a.id=sqlc.arg(app_id)::uuid AND a.status<>'deleted' AND rs.environment_slug=sqlc.arg(scope)::text
AND ((sqlc.narg(release_id)::uuid IS NULL AND rs.active)
    OR (rs.id=sqlc.narg(release_id)::uuid AND (rs.active OR rs.expires_at>now()
        OR EXISTS(SELECT 1 FROM customer_operation_retained_release_refs retained WHERE retained.release_id=rs.id))));


-- name: RetainedReleaseTargetUsable :one
SELECT EXISTS(SELECT 1 FROM deployments d WHERE d.id=sqlc.arg(deployment_id)::uuid
AND d.app_id=sqlc.arg(app_id)::uuid AND d.status='live' AND (
    d.traffic_percent>0
    OR EXISTS(SELECT 1 FROM deployment_revision_pins p WHERE p.deployment_id=d.id AND p.expires_at>now())
    OR EXISTS(SELECT 1 FROM project_release_members rm JOIN project_release_sets rs ON rs.id=rm.release_id
        WHERE rm.deployment_id=d.id AND rm.app_id=d.app_id AND (rs.active OR rs.expires_at>now()))
    OR EXISTS(SELECT 1 FROM customer_operation_retained_deployment_refs retained WHERE retained.deployment_id=d.id)));


-- name: ListRetainedServiceReleases :many
SELECT rs.id::text AS release_id,target.deployment_id::text AS deployment_id FROM project_release_sets rs
JOIN project_release_members caller ON caller.release_id=rs.id
JOIN project_release_members target ON target.release_id=rs.id
JOIN apps ca ON ca.id=caller.app_id AND ca.account_id=rs.account_id AND ca.project_id=rs.project_id AND ca.status<>'deleted'
JOIN apps ta ON ta.id=target.app_id AND ta.account_id=rs.account_id AND ta.project_id=rs.project_id AND ta.status<>'deleted'
WHERE caller.app_id=sqlc.arg(caller_app_id)::uuid AND caller.deployment_id=sqlc.arg(caller_deployment_id)::uuid
AND target.app_id=sqlc.arg(target_app_id)::uuid AND (sqlc.narg(release_id)::uuid IS NULL OR rs.id=sqlc.narg(release_id)::uuid)
AND (rs.active OR rs.expires_at>now() OR EXISTS(SELECT 1 FROM customer_operation_retained_release_refs retained WHERE retained.release_id=rs.id))
ORDER BY rs.created_at DESC LIMIT 2;


-- name: RetainedReleaseMemberDeploymentForUpdate :one
SELECT d.id FROM deployments d WHERE d.id=sqlc.arg(deployment_id)::uuid AND d.app_id=sqlc.arg(app_id)::uuid
AND d.scope=sqlc.arg(scope)::text AND d.status='live' AND (
    d.traffic_percent>0 OR d.traffic_percent_explicit
    OR EXISTS(SELECT 1 FROM deployment_revision_pins p WHERE p.deployment_id=d.id AND p.expires_at>now())
    OR EXISTS(SELECT 1 FROM project_release_members rm JOIN project_release_sets rs ON rs.id=rm.release_id
        WHERE rm.deployment_id=d.id AND rm.app_id=d.app_id AND (rs.active OR rs.expires_at>now()))
    OR EXISTS(SELECT 1 FROM customer_operation_retained_deployment_refs retained WHERE retained.deployment_id=d.id))
FOR UPDATE OF d;


-- name: FinalizeRetainedServiceRolloutAbortTarget :one
UPDATE deployments d SET status=CASE WHEN
    EXISTS(SELECT 1 FROM customer_operation_retained_deployment_refs retained WHERE retained.deployment_id=d.id)
    THEN 'live' ELSE 'superseded' END,
    traffic_percent=0,rollout_state='aborted',rollout_completed_at=NULL,
    rollout_aborted_at=sqlc.arg(aborted_at)::timestamptz,
    rollout_aborted_reason=sqlc.arg(reason)::text,
    service_rollout_handoff=sqlc.arg(handoff)::jsonb
WHERE d.id=sqlc.arg(deployment_id)::uuid RETURNING status::text;


-- name: LatestRetainedRollbackDeployment :one
-- Most recently serving first (serving_ended_at, migration
-- 20261004234807528); rows superseded before it fall back to created_at.
-- A live 0% deployment that served before (a release demoted by `traffic
-- promote` or `traffic set`) is a rollback target; one that never served
-- (a dark deploy) needs a retention pin.
SELECT d.id FROM deployments d WHERE d.app_id=sqlc.arg(app_id)::uuid
AND (sqlc.narg(scope)::text IS NULL OR d.scope=sqlc.narg(scope)::text)
AND (sqlc.narg(current_deployment_id)::uuid IS NULL OR d.id<>sqlc.narg(current_deployment_id)::uuid)
AND (d.status='superseded' OR (d.status='live' AND d.traffic_percent=0 AND (
    d.serving_ended_at IS NOT NULL
    OR EXISTS(SELECT 1 FROM deployment_revision_pins p WHERE p.deployment_id=d.id AND p.expires_at>now())
    OR EXISTS(SELECT 1 FROM customer_operation_retained_deployment_refs retained WHERE retained.deployment_id=d.id))))
ORDER BY coalesce(d.serving_ended_at,d.created_at) DESC,d.created_at DESC,d.id DESC LIMIT 1;


-- name: LockRetainedRollbackDeployment :one
SELECT d.id FROM deployments d WHERE d.app_id=sqlc.arg(app_id)::uuid AND d.scope=sqlc.arg(scope)::text
AND d.id<>sqlc.arg(current_deployment_id)::uuid
AND d.environment_workload_runtime IS NULL
AND (d.status='superseded' OR (d.status='live' AND d.traffic_percent=0 AND (
    d.serving_ended_at IS NOT NULL
    OR EXISTS(SELECT 1 FROM deployment_revision_pins p WHERE p.deployment_id=d.id AND p.expires_at>now())
    OR EXISTS(SELECT 1 FROM customer_operation_retained_deployment_refs retained WHERE retained.deployment_id=d.id))))
ORDER BY coalesce(d.serving_ended_at,d.created_at) DESC,d.created_at DESC,d.id DESC LIMIT 1 FOR UPDATE OF d;


-- name: RetireAutoRollbackDeploymentSiblings :exec
UPDATE deployments d SET status=CASE WHEN
    EXISTS(SELECT 1 FROM customer_operation_retained_deployment_refs retained WHERE retained.deployment_id=d.id)
    THEN 'live' ELSE 'superseded' END,
    traffic_percent=0,rollout_state='aborted',rollout_completed_at=NULL,
    rollout_aborted_at=coalesce(rollout_aborted_at,now()),
    rollout_aborted_reason=coalesce(nullif(rollout_aborted_reason,''),'automatic rollback'),
    last_auto_rollback_at=CASE WHEN d.id=sqlc.arg(current_deployment_id)::uuid THEN coalesce(last_auto_rollback_at,now()) ELSE last_auto_rollback_at END,
    last_auto_rollback_reason=CASE WHEN d.id=sqlc.arg(current_deployment_id)::uuid THEN coalesce(last_auto_rollback_reason,'threshold_exceeded') ELSE last_auto_rollback_reason END
WHERE d.app_id=sqlc.arg(app_id)::uuid AND d.scope=sqlc.arg(scope)::text AND d.status='live';


-- name: ActivateRetainedRollbackDeployment :execrows
UPDATE deployments SET status='live',error='',traffic_percent=100,
    canary_step=canary_total_steps,
    canary_step_started_at=CASE WHEN canary_total_steps>0 THEN now() ELSE canary_step_started_at END,
    rollout_state='complete',rollout_started_at=coalesce(rollout_started_at,now()),
    rollout_completed_at=now(),rollout_aborted_at=NULL,rollout_aborted_reason=''
WHERE id=sqlc.arg(deployment_id)::uuid AND app_id=sqlc.arg(app_id)::uuid
AND scope=sqlc.arg(scope)::text AND status IN ('superseded','live');

-- Public release selectors retain their configured access window. Private
-- execution retention is resolved only through an owned operation/workload.
-- name: ResolvePublicProjectRelease :one
SELECT rs.id::text AS release_id,coalesce(rm.deployment_id::text,'')::text AS deployment_id
FROM apps a JOIN project_release_sets rs ON rs.project_id=a.project_id AND rs.account_id=a.account_id
LEFT JOIN project_release_members rm ON rm.release_id=rs.id AND rm.app_id=a.id
WHERE a.id=sqlc.arg(app_id)::uuid AND a.status<>'deleted' AND rs.environment_slug=sqlc.arg(scope)::text
AND ((sqlc.narg(release_id)::uuid IS NULL AND rs.active)
    OR (rs.id=sqlc.narg(release_id)::uuid AND (rs.active OR rs.expires_at>now())));

-- ADR-569: known resources remain accountable through lifecycle shutdown.
-- name: ListManagedPostgresUsageResources :many
SELECT d.* FROM managed_postgres_databases d
WHERE (NULLIF(d.provider_resource_id, '') IS NOT NULL OR d.accounting_required)
  AND (sqlc.narg(after_updated_at)::timestamptz IS NULL
       OR (d.updated_at, d.id) > (sqlc.narg(after_updated_at)::timestamptz, sqlc.arg(after_id)::uuid))
ORDER BY d.updated_at, d.id LIMIT sqlc.arg(page_limit);

-- name: LockManagedPostgresUsageResource :one
SELECT d.* FROM managed_postgres_databases d WHERE d.id = sqlc.arg(id)::uuid FOR UPDATE;

-- name: GetManagedPostgresUsageProgress :one
SELECT c.collected_from, c.collected_until, c.observed_at, COALESCE(c.source_database_id::text, '')::text AS source_database_id,
 (SELECT min(u.observed_at) FROM managed_postgres_usage u
  WHERE u.database_id = d.id AND u.window_to <= c.collected_until
    AND u.window_from >= GREATEST(c.collected_from, c.collected_until - 3 * sqlc.arg(window_seconds)::bigint * interval '1 second'))::timestamptz AS correction_observed_at
FROM managed_postgres_databases d LEFT JOIN managed_postgres_usage_coverage c
ON c.database_id = d.id AND c.window_seconds = sqlc.arg(window_seconds)::bigint
WHERE d.account_id = sqlc.arg(account_id)::uuid AND d.id = sqlc.arg(database_id)::uuid;

-- name: RecordManagedPostgresSharedUsage :execrows
WITH RECURSIVE ancestry AS (
 SELECT restore_source_database_id AS id FROM managed_postgres_databases
 WHERE id = sqlc.arg(database_id)::uuid AND account_id = sqlc.arg(account_id)::uuid
 UNION
 SELECT d.restore_source_database_id FROM managed_postgres_databases d JOIN ancestry a ON d.id = a.id
 WHERE d.account_id = sqlc.arg(account_id)::uuid
)
INSERT INTO managed_postgres_usage_coverage (database_id, window_seconds, source_database_id)
SELECT d.id, sqlc.arg(window_seconds)::bigint, s.id FROM managed_postgres_databases d JOIN managed_postgres_databases s
ON s.id = sqlc.arg(source_id)::uuid AND s.account_id = d.account_id
AND s.backend_id = d.backend_id AND s.backend_fingerprint = d.backend_fingerprint
WHERE d.id = sqlc.arg(database_id)::uuid AND d.account_id = sqlc.arg(account_id)::uuid
AND NULLIF(d.provider_resource_id, '') IS NOT NULL AND NULLIF(s.provider_resource_id, '') IS NOT NULL
AND s.id IN (SELECT id FROM ancestry)
ON CONFLICT (database_id, window_seconds) DO UPDATE SET
source_database_id = EXCLUDED.source_database_id, collected_from = NULL, collected_until = NULL,
observed_at = NULL, updated_at = now();

-- name: ListManagedPostgresAccountingCoverage :many
SELECT d.id AS database_id, d.name, d.state, d.accounting_required,
(NULLIF(d.provider_resource_id, '') IS NOT NULL)::boolean AS identity_known, d.lease_until,
COALESCE(source.id, d.id)::uuid AS accounting_database_id, COALESCE(source.created_at, d.created_at)::timestamptz AS accounting_created_at, (d.accounting_required AND NULLIF(d.provider_resource_id, '') IS NULL)::boolean AS unresolved,
COALESCE(source.state, d.state)::text AS accounting_state,
(CASE WHEN source.id IS NULL THEN d.deleted_at ELSE source.deleted_at END)::timestamptz AS ended_at, COALESCE(c.window_seconds, 0)::bigint AS window_seconds,
COALESCE(s.collected_from, c.collected_from)::timestamptz AS collected_from,
COALESCE(s.collected_until, c.collected_until)::timestamptz AS collected_until,
COALESCE(s.observed_at, c.observed_at)::timestamptz AS observed_at,
(SELECT min(u.observed_at) FROM managed_postgres_usage u
 WHERE u.database_id = COALESCE(c.source_database_id, d.id)
 AND u.window_to <= COALESCE(s.collected_until, c.collected_until)
 AND u.window_from >= GREATEST(COALESCE(s.collected_from, c.collected_from),
 COALESCE(s.collected_until, c.collected_until) - 3 * c.window_seconds * interval '1 second'))::timestamptz AS correction_observed_at, COALESCE(c.source_database_id::text, '')::text AS source_database_id
FROM managed_postgres_databases d
LEFT JOIN LATERAL (SELECT * FROM managed_postgres_usage_coverage WHERE database_id = d.id
 ORDER BY updated_at DESC, window_seconds DESC LIMIT 1) c ON true
LEFT JOIN managed_postgres_databases source ON source.id = c.source_database_id
LEFT JOIN managed_postgres_usage_coverage s ON s.database_id = c.source_database_id AND s.window_seconds = c.window_seconds
WHERE d.account_id = sqlc.arg(account_id)::uuid AND (d.state = 'ready' OR NULLIF(d.provider_resource_id, '') IS NOT NULL OR d.accounting_required)
AND (sqlc.narg(after_id)::uuid IS NULL OR d.id > sqlc.narg(after_id)::uuid)
ORDER BY d.id LIMIT sqlc.narg(page_limit)::integer;

-- name: PruneEnvironmentGitOpsReports :exec
DELETE FROM environment_gitops_runs r USING (
 SELECT id, completed_at, row_number() OVER (ORDER BY completed_at DESC,id DESC) AS position
 FROM environment_gitops_runs WHERE source_id=sqlc.arg(source_id)::uuid AND completed_at IS NOT NULL
) old
WHERE r.id=old.id AND old.position>1
 AND (old.position>sqlc.arg(keep_count)::integer OR old.completed_at<sqlc.arg(before_at)::timestamptz);

-- name: LockEnvironmentGitOpsEnvironment :one
SELECT e.id FROM project_environments e WHERE e.account_id=sqlc.arg(account_id)::uuid
 AND e.project_id=sqlc.arg(project_id)::uuid AND e.slug=sqlc.arg(environment)::text FOR NO KEY UPDATE;

-- name: EnvironmentGitOpsLifecyclePending :one
SELECT EXISTS(SELECT 1 FROM environment_gitops_effects WHERE source_id=sqlc.arg(source_id)::uuid AND completed_at IS NULL)
 OR EXISTS(SELECT 1 FROM environment_gitops_runtime_effects WHERE source_id=sqlc.arg(source_id)::uuid AND completed_at IS NULL)
 OR EXISTS(SELECT 1 FROM environment_workload_graphs WHERE source_id=sqlc.arg(source_id)::uuid AND phase='preparing')
 OR EXISTS(SELECT 1 FROM environment_workload_qualification_requests q JOIN environment_workload_graphs g ON g.id=q.graph_id WHERE g.source_id=sqlc.arg(source_id)::uuid) AS pending;

-- name: DetachEnvironmentGitSource :execrows
UPDATE environment_git_sources SET detached=true,suspended=true,generation=generation+1,intent_version=intent_version+1,updated_at=now()
 WHERE id=sqlc.arg(source_id)::uuid AND NOT detached AND mode='report' AND generation=sqlc.arg(expected_generation)::bigint;

-- name: ReleaseEnvironmentGitSourceOwners :exec
DELETE FROM environment_managed_fields WHERE source_id=sqlc.arg(source_id)::uuid;

-- name: ReleaseEnvironmentGitSourceOverrides :exec
DELETE FROM environment_management_overrides o USING environment_managed_fields f
 WHERE f.source_id=sqlc.arg(source_id)::uuid AND o.environment_id=f.environment_id AND o.resource=f.resource AND o.field_path=f.field_path;

-- name: ResolveEnvironmentFieldOwnershipScope :one
SELECT e.id AS environment_id,e.project_id,p.account_id,
 CASE WHEN sqlc.arg(app)::text='' THEN 'environment' ELSE 'app/'||a.id::text END::text AS resource
FROM project_environments e JOIN projects p ON p.id=e.project_id AND p.account_id=e.account_id
LEFT JOIN apps a ON a.project_id=p.id AND a.account_id=p.account_id AND a.slug=sqlc.arg(app)::text AND a.status<>'deleted'
WHERE p.account_id=sqlc.arg(account_id)::uuid AND e.slug=sqlc.arg(environment)::text
 AND ((sqlc.arg(app)::text<>'' AND a.id IS NOT NULL) OR (sqlc.arg(app)::text='' AND p.slug=sqlc.arg(project)::text));

-- name: LockEnvironmentFieldOwnershipScope :exec
SELECT pg_advisory_xact_lock(hashtextextended(sqlc.arg(environment_id)::text,31));

-- name: EnvironmentFieldGitOwned :one
SELECT EXISTS(SELECT 1 FROM environment_managed_fields f JOIN active_environment_git_sources s ON s.id=f.source_id
 LEFT JOIN environment_gitops_resources r ON r.source_id=s.id AND r.logical_name=f.resource
 WHERE s.environment_id=sqlc.arg(environment_id)::uuid
 AND ((sqlc.arg(resource)::text='environment' AND f.resource='environment') OR sqlc.arg(resource)::text='app/'||r.app_id::text)
 AND (f.field_path=sqlc.arg(field_path)::text OR (starts_with(sqlc.arg(field_path)::text,'variables/') AND f.field_path='secret_refs/'||substring(sqlc.arg(field_path)::text FROM 11)))) AS owned;

-- name: PutEnvironmentExternalFieldOwner :execrows
INSERT INTO environment_external_field_owners(environment_id,resource,field_path,manager_id)
VALUES(sqlc.arg(environment_id)::uuid,sqlc.arg(resource)::text,sqlc.arg(field_path)::text,'terraform') ON CONFLICT DO NOTHING;

-- name: DeleteEnvironmentExternalFieldOwner :execrows
DELETE FROM environment_external_field_owners WHERE environment_id=sqlc.arg(environment_id)::uuid AND resource=sqlc.arg(resource)::text
 AND field_path=sqlc.arg(field_path)::text AND manager_id='terraform';

-- name: EnvironmentFieldOwnershipLegacyApp :one
SELECT EXISTS(SELECT 1 FROM apps WHERE account_id=sqlc.arg(account_id)::uuid AND slug=sqlc.arg(app)::text AND status<>'deleted'
 AND (project_id IS NULL OR sqlc.arg(environment)::text='default')) AS legacy;

-- name: ObjectVersionProtectionGet :one
SELECT * FROM object_version_protection WHERE id=$1;

-- name: ObjectVersionProtectionInsert :exec
INSERT INTO object_version_protection(id,bucket_id,account_id,app_id,object_key,public_version_id,native_version_id,intent)
VALUES($1,$2,$3,$4,$5,$6,$7,$8);

-- name: ObjectVersionProtectionUpdate :exec
UPDATE object_version_protection SET state=$2,lease_token=$3,lease_until=$4,retry_at=$5,dispatched=$6,last_error_code=$7,event_hold_baseline=$8,updated_at=now() WHERE id=$1;

-- name: ObjectVersionProtectionDue :many
SELECT id FROM object_version_protection WHERE state IN ('waiting','applying') AND retry_at<=now() AND (lease_until IS NULL OR lease_until<=now()) ORDER BY retry_at,id LIMIT $1;

-- name: ObjectVersionProtectionActive :one
SELECT id FROM object_version_protection WHERE bucket_id=$1 AND state IN ('waiting','applying');

-- ADR-581: persist an irreversible accounting obligation before provider I/O.
-- name: BeginManagedPostgresAccounting :execrows
UPDATE managed_postgres_databases SET accounting_required = true, updated_at = sqlc.arg(now)::timestamptz
WHERE id = sqlc.arg(id)::uuid AND state = 'provisioning'
  AND lease_token = sqlc.arg(lease_token)::text AND lease_until > sqlc.arg(now)::timestamptz;

-- name: RecordManagedPostgresProviderResource :execrows
UPDATE managed_postgres_databases
SET provider_resource_id = sqlc.arg(provider_resource_id)::text, accounting_required = true, updated_at = sqlc.arg(now)::timestamptz
WHERE id = sqlc.arg(id)::uuid AND state IN ('provisioning', 'deleting')
  AND lease_token = sqlc.arg(lease_token)::text AND lease_until > sqlc.arg(now)::timestamptz
  AND (NULLIF(provider_resource_id, '') IS NULL OR provider_resource_id = sqlc.arg(provider_resource_id)::text);

-- name: RecordManagedPostgresDiscoveredResource :execrows
UPDATE managed_postgres_databases
SET provider_resource_id = sqlc.arg(provider_resource_id)::text, updated_at = sqlc.arg(now)::timestamptz
WHERE id = sqlc.arg(id)::uuid AND account_id = sqlc.arg(account_id)::uuid
  AND backend_id = sqlc.arg(backend_id)::text AND backend_fingerprint = sqlc.arg(backend_fingerprint)::text
  AND accounting_required AND state <> 'deleted'
  AND (lease_until IS NULL OR lease_until <= sqlc.arg(now)::timestamptz)
  AND (NULLIF(provider_resource_id, '') IS NULL OR provider_resource_id = sqlc.arg(provider_resource_id)::text);

-- ADR-581: only a validated new reservation can prove provider I/O has not begun.
-- name: InsertManagedPostgresReservation :one
INSERT INTO managed_postgres_databases
(id, account_id, name, region, postgres_major, service_class, availability, scale_to_zero,
 storage_limit_bytes, restore_window_seconds, backend_id, backend_fingerprint,
 restore_source_database_id, restore_source_resource_id, restore_point_in_time, state,
 desired_generation, observed_generation, retry_at, created_at, updated_at, accounting_required)
VALUES
(sqlc.arg(id), sqlc.arg(account_id), sqlc.arg(name), sqlc.arg(region), sqlc.arg(postgres_major),
 sqlc.arg(service_class), sqlc.arg(availability), sqlc.arg(scale_to_zero), sqlc.arg(storage_limit_bytes),
 sqlc.arg(restore_window_seconds), sqlc.arg(backend_id), sqlc.arg(backend_fingerprint),
 sqlc.narg(restore_source_database_id), sqlc.narg(restore_source_resource_id), sqlc.narg(restore_point_in_time),
 sqlc.arg(state), sqlc.arg(desired_generation), sqlc.arg(observed_generation), sqlc.arg(retry_at),
 sqlc.arg(created_at), sqlc.arg(updated_at), false)
RETURNING *;

-- name: ListWorkflowScheduleCandidates :many
SELECT a.id AS app_id, d.id AS deployment_id, d.workflows
FROM apps a
JOIN accounts ac ON ac.id = a.account_id
JOIN LATERAL (
    SELECT dep.id, app_workflow_definitions(a.id,dep.workflows)::jsonb AS workflows FROM deployments dep
    WHERE dep.app_id = a.id AND dep.status = 'live' AND dep.scope = 'default'
    ORDER BY (dep.traffic_percent > 0) DESC, dep.created_at DESC, dep.id DESC LIMIT 1
) d ON true
WHERE a.status <> 'deleted' AND NOT a.maintenance_mode AND NOT a.platform_tenant_required
  AND ac.status IN ('active', 'past_due') AND ac.abuse_hold_at IS NULL AND ac.plan <> 'free'
  AND (sqlc.narg(after_app_id)::uuid IS NULL OR a.id > sqlc.narg(after_app_id)::uuid)
  AND (sqlc.narg(owner_node_id)::uuid IS NULL OR a.node_id = sqlc.narg(owner_node_id)::uuid)
  AND d.workflows @> '[{"trigger":{"type":"schedule"}}]'::jsonb
ORDER BY a.id LIMIT sqlc.arg(batch_limit);

-- name: LockWorkflowScheduleTarget :one
SELECT d.id AS deployment_id, app_workflow_definitions(a.id,d.workflows)::jsonb AS workflows, ac.plan
FROM apps a JOIN accounts ac ON ac.id = a.account_id
JOIN deployments d ON d.app_id = a.id
WHERE a.id = sqlc.arg(app_id) AND a.status <> 'deleted' AND NOT a.maintenance_mode AND NOT a.platform_tenant_required
  AND ac.status IN ('active', 'past_due') AND ac.abuse_hold_at IS NULL
  AND d.id = (
      SELECT dep.id FROM deployments dep
      WHERE dep.app_id = a.id AND dep.status = 'live' AND dep.scope = 'default'
      ORDER BY (dep.traffic_percent > 0) DESC, dep.created_at DESC, dep.id DESC LIMIT 1
  )
FOR SHARE OF a, ac, d;

-- name: LockWorkflowResumeTarget :one
SELECT d.id AS deployment_id, app_workflow_definitions(a.id,d.workflows)::jsonb AS workflows, ac.plan
FROM apps a JOIN accounts ac ON ac.id = a.account_id
JOIN deployments d ON d.app_id = a.id
WHERE a.id = sqlc.arg(app_id) AND a.status <> 'deleted' AND NOT a.maintenance_mode
  AND ac.status IN ('active', 'past_due') AND ac.abuse_hold_at IS NULL
  AND d.id = (
      SELECT dep.id FROM deployments dep
      WHERE dep.app_id = a.id AND dep.status = 'live' AND dep.scope = 'default'
      ORDER BY (dep.traffic_percent > 0) DESC, dep.created_at DESC, dep.id DESC LIMIT 1
  )
FOR SHARE OF a, ac, d;

-- name: LockWorkflowRunAdmission :exec
SELECT pg_advisory_xact_lock(hashtextextended(sqlc.arg(app_key)::text, 0));

-- name: LockWorkflowActionAdmission :exec
SELECT pg_advisory_xact_lock(hashtextextended(sqlc.arg(workflow_key)::text, 1));

-- name: CountWorkflowRunningActionsForAdmission :one
SELECT count(*) FROM workflow_steps s
JOIN workflow_runs r ON r.id=s.run_id
WHERE r.app_id=sqlc.arg(app_id) AND r.workflow_name=sqlc.arg(workflow_name)
  AND r.status IN ('pending','running','awaiting_event') AND s.status='running';

-- name: CountActiveWorkflowRunsForAdmission :one
SELECT count(*) FROM workflow_runs
WHERE app_id = sqlc.arg(app_id) AND status IN ('pending', 'running', 'awaiting_event')
  AND (sqlc.arg(workflow_name)::text = '' OR workflow_name = sqlc.arg(workflow_name));

-- name: InsertScheduledWorkflowRun :one
INSERT INTO workflow_runs (id, app_id, workflow_name, status, input, definition_snapshot, scheduled_for)
VALUES (sqlc.arg(id), sqlc.arg(app_id), sqlc.arg(workflow_name), 'pending', sqlc.arg(input), sqlc.arg(definition_snapshot), sqlc.arg(scheduled_for))
RETURNING created_at, updated_at;

-- name: GetWorkflowScheduleCursor :one
SELECT * FROM workflow_schedule_cursors WHERE app_id = $1 AND workflow_name = $2;

-- name: ListWorkflowScheduleCursors :many
SELECT * FROM workflow_schedule_cursors WHERE app_id = $1 ORDER BY workflow_name;

-- name: PruneWorkflowScheduleCursors :exec
DELETE FROM workflow_schedule_cursors c WHERE c.app_id = sqlc.arg(app_id)
AND NOT EXISTS (
    SELECT 1 FROM jsonb_array_elements(sqlc.arg(workflows)::jsonb) definition
    WHERE definition->>'name' = c.workflow_name AND definition->'trigger'->>'type' = 'schedule'
);

-- name: UpsertWorkflowScheduleCursor :one
INSERT INTO workflow_schedule_cursors (app_id, workflow_name, deployment_id, trigger_snapshot,
    last_evaluated_at, scheduled_for, status, last_run_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (app_id, workflow_name) DO UPDATE SET
    deployment_id = EXCLUDED.deployment_id, trigger_snapshot = EXCLUDED.trigger_snapshot,
    last_evaluated_at = EXCLUDED.last_evaluated_at, scheduled_for = EXCLUDED.scheduled_for,
    status = EXCLUDED.status, last_run_id = EXCLUDED.last_run_id, updated_at = now()
RETURNING *;

-- Tenant schedule candidates are one row per active tenant/app binding. The
-- composite cursor prevents large tenants from being starved by the page cap.
-- name: ListTenantWorkflowScheduleCandidates :many
SELECT a.id AS app_id, t.id AS platform_tenant_id, d.id AS deployment_id,
       app_workflow_definitions(a.id, d.workflows)::jsonb AS workflows
FROM apps a
JOIN accounts ac ON ac.id = a.account_id
JOIN platform_tenants t ON t.account_id = a.account_id AND t.status = 'active'
JOIN deployments d ON d.app_id = a.id
WHERE a.status <> 'deleted' AND NOT a.maintenance_mode AND a.platform_tenant_required
  AND ac.status IN ('active', 'past_due') AND ac.abuse_hold_at IS NULL AND ac.plan <> 'free'
  AND d.id = (SELECT dep.id FROM deployments dep WHERE dep.app_id = a.id
      AND dep.status = 'live' AND dep.scope = 'default'
      ORDER BY (dep.traffic_percent > 0) DESC, dep.created_at DESC, dep.id DESC LIMIT 1)
  AND (sqlc.narg(owner_node_id)::uuid IS NULL OR a.node_id = sqlc.narg(owner_node_id)::uuid)
  AND (sqlc.narg(after_app_id)::uuid IS NULL OR a.id > sqlc.narg(after_app_id)::uuid
       OR (a.id = sqlc.narg(after_app_id)::uuid AND t.id > sqlc.narg(after_tenant_id)::uuid))
  AND app_workflow_definitions(a.id, d.workflows)::jsonb @> '[{"trigger":{"type":"schedule"}}]'::jsonb
  AND (EXISTS (SELECT 1 FROM api_consumers c WHERE c.account_id = a.account_id AND c.app_id = a.id
       AND c.platform_tenant_id = t.id AND c.status = 'active' AND c.revoked_at IS NULL)
       OR EXISTS (SELECT 1 FROM tenant_surfaces s WHERE s.account_id = a.account_id AND s.app_id = a.id
       AND s.platform_tenant_id = t.id AND s.status = 'active'))
ORDER BY a.id, t.id LIMIT sqlc.arg(batch_limit);

-- name: LockTenantWorkflowScheduleTarget :one
SELECT a.account_id, d.id AS deployment_id, app_workflow_definitions(a.id, d.workflows)::jsonb AS workflows, ac.plan
FROM apps a
JOIN accounts ac ON ac.id = a.account_id
JOIN platform_tenants t ON t.id = sqlc.arg(tenant_id)::uuid AND t.account_id = a.account_id
JOIN deployments d ON d.app_id = a.id
WHERE a.id = sqlc.arg(app_id)::uuid AND a.status <> 'deleted' AND NOT a.maintenance_mode
  AND a.platform_tenant_required AND t.status = 'active'
  AND ac.status IN ('active', 'past_due') AND ac.abuse_hold_at IS NULL
  AND d.id = (SELECT dep.id FROM deployments dep WHERE dep.app_id = a.id
      AND dep.status = 'live' AND dep.scope = 'default'
      ORDER BY (dep.traffic_percent > 0) DESC, dep.created_at DESC, dep.id DESC LIMIT 1)
FOR SHARE OF a, ac, t, d;

-- Link rows are locked separately so unlink/revocation cannot race a schedule
-- admission after the target and tenant have been checked.
-- name: LockTenantWorkflowScheduleConsumerLink :one
SELECT c.id FROM api_consumers c
WHERE c.account_id = sqlc.arg(account_id)::uuid AND c.app_id = sqlc.arg(app_id)::uuid
  AND c.platform_tenant_id = sqlc.arg(tenant_id)::uuid AND c.status = 'active' AND c.revoked_at IS NULL
FOR SHARE;

-- name: LockTenantWorkflowScheduleSurfaceLink :one
SELECT s.id FROM tenant_surfaces s
WHERE s.account_id = sqlc.arg(account_id)::uuid AND s.app_id = sqlc.arg(app_id)::uuid
  AND s.platform_tenant_id = sqlc.arg(tenant_id)::uuid AND s.status = 'active'
FOR SHARE;

-- name: CountActiveTenantWorkflowRunsForAdmission :one
SELECT count(*) FROM workflow_runs
WHERE app_id = sqlc.arg(app_id)::uuid AND platform_tenant_id = sqlc.arg(tenant_id)::uuid
  AND workflow_name = sqlc.arg(workflow_name)::text
  AND status IN ('pending', 'running', 'awaiting_event');

-- name: InsertTenantScheduledWorkflowRun :one
INSERT INTO workflow_runs (id, app_id, platform_tenant_id, workflow_name, status, input, definition_snapshot, scheduled_for)
VALUES (sqlc.arg(id)::uuid, sqlc.arg(app_id)::uuid, sqlc.arg(tenant_id)::uuid, sqlc.arg(workflow_name)::text,
    'pending', sqlc.arg(input)::jsonb, sqlc.arg(definition_snapshot)::jsonb, sqlc.arg(scheduled_for)::timestamptz)
RETURNING created_at, updated_at;

-- name: GetTenantWorkflowScheduleCursor :one
SELECT * FROM platform_tenant_workflow_schedule_cursors
WHERE app_id = sqlc.arg(app_id)::uuid AND platform_tenant_id = sqlc.arg(tenant_id)::uuid
  AND workflow_name = sqlc.arg(workflow_name)::text;

-- name: UpsertTenantWorkflowScheduleCursor :one
INSERT INTO platform_tenant_workflow_schedule_cursors (app_id, platform_tenant_id, workflow_name,
    deployment_id, trigger_snapshot, last_evaluated_at, scheduled_for, status, last_run_id)
VALUES (sqlc.arg(app_id)::uuid, sqlc.arg(tenant_id)::uuid, sqlc.arg(workflow_name)::text,
    sqlc.arg(deployment_id)::uuid, sqlc.arg(trigger_snapshot)::jsonb, sqlc.arg(last_evaluated_at)::timestamptz,
    sqlc.narg(scheduled_for)::timestamptz, sqlc.arg(status)::text, sqlc.narg(last_run_id)::uuid)
ON CONFLICT (app_id, platform_tenant_id, workflow_name) DO UPDATE SET
    deployment_id = EXCLUDED.deployment_id, trigger_snapshot = EXCLUDED.trigger_snapshot,
    last_evaluated_at = EXCLUDED.last_evaluated_at, scheduled_for = EXCLUDED.scheduled_for,
    status = EXCLUDED.status, last_run_id = EXCLUDED.last_run_id, updated_at = now()
RETURNING *;

-- name: ListMatchingEventWorkflows :many
SELECT recipient::jsonb FROM workflow_event_recipients(sqlc.arg(account_id)::uuid, sqlc.arg(source)::text, sqlc.arg(event_type)::text)
WHERE recipient->>'id' > sqlc.arg(after_id)::text
ORDER BY recipient->>'id' LIMIT sqlc.arg(batch_limit)::int;

-- name: LockEventWorkflowRecipient :one
SELECT o.payload, (SELECT r.recipient FROM jsonb_array_elements(o.recipient_snapshot) r(recipient)
    WHERE r.recipient->>'id' = sqlc.arg(recipient_id)::text AND r.recipient ? 'workflow')::jsonb AS recipient
FROM event_fanout_outbox o
WHERE o.id = sqlc.arg(outbox_id) AND o.claim_token = sqlc.arg(claim_token)::uuid AND o.state = 'processing'
FOR UPDATE OF o;

-- name: GetEventWorkflowReceipt :one
SELECT run_id FROM workflow_event_receipts WHERE outbox_id = $1 AND recipient_id = $2;

-- name: LockEventWorkflowTarget :one
SELECT a.account_id, a.status AS app_status, a.maintenance_mode, a.platform_tenant_required,
 ac.plan, ac.status AS account_status, ac.abuse_hold_at
FROM apps a JOIN accounts ac ON ac.id = a.account_id
WHERE a.id = sqlc.arg(app_id) FOR SHARE OF a, ac;

-- name: InsertEventWorkflowRun :exec
INSERT INTO workflow_runs(id, app_id, platform_tenant_id, workflow_name, status, input, definition_snapshot)
VALUES($1, $2, nullif(sqlc.arg(platform_tenant_id)::text, '')::uuid, $3, 'pending', $4, $5);

-- name: InsertEventWorkflowReceipt :exec
INSERT INTO workflow_event_receipts(outbox_id, recipient_id, run_id) VALUES($1, $2, $3);

-- name: ListAutomations :many
SELECT * FROM workflow_automation_definitions WHERE app_id=$1 ORDER BY name;

-- name: ListWorkflowAutomationRevisions :many
SELECT * FROM workflow_automation_revisions
WHERE app_id=$1 AND name=$2
ORDER BY version DESC
LIMIT $3 OFFSET $4;

-- name: CountWorkflowAutomationRevisions :one
SELECT count(*) FROM workflow_automation_revisions WHERE app_id=$1 AND name=$2;

-- name: GetWorkflowAutomationHealthSummary :one
SELECT count(*)::bigint AS run_count,
       count(*) FILTER (WHERE status='pending')::bigint AS pending_runs,
       count(*) FILTER (WHERE status='running')::bigint AS running_runs,
       count(*) FILTER (WHERE status='awaiting_event')::bigint AS awaiting_event_runs,
       count(*) FILTER (WHERE status='succeeded')::bigint AS succeeded_runs,
       count(*) FILTER (WHERE status='failed')::bigint AS failed_runs,
       count(*) FILTER (WHERE status='dead')::bigint AS dead_runs,
       count(*) FILTER (WHERE started_at IS NOT NULL AND finished_at >= started_at AND status IN ('succeeded','failed','dead'))::bigint AS duration_samples,
       COALESCE(round((percentile_cont(0.50) WITHIN GROUP (
           ORDER BY (EXTRACT(EPOCH FROM (finished_at-started_at))*1000)::double precision
       ) FILTER (WHERE started_at IS NOT NULL AND finished_at >= started_at AND status IN ('succeeded','failed','dead')))::numeric)::bigint, 0)::bigint AS p50_duration_ms,
       COALESCE(round((percentile_cont(0.95) WITHIN GROUP (
           ORDER BY (EXTRACT(EPOCH FROM (finished_at-started_at))*1000)::double precision
       ) FILTER (WHERE started_at IS NOT NULL AND finished_at >= started_at AND status IN ('succeeded','failed','dead')))::numeric)::bigint, 0)::bigint AS p95_duration_ms
FROM workflow_runs
WHERE app_id=sqlc.arg(app_id)::uuid AND workflow_name=sqlc.arg(workflow_name)::text
  AND created_at >= sqlc.arg(created_after)::timestamptz
  AND created_at <= sqlc.arg(created_before)::timestamptz;

-- name: ListWorkflowAutomationHealthRecentRuns :many
WITH recent AS (
    (SELECT 'latest'::text AS kind, id, status, created_at, finished_at
     FROM workflow_runs
     WHERE app_id=sqlc.arg(app_id)::uuid AND workflow_name=sqlc.arg(workflow_name)::text
       AND created_at >= sqlc.arg(created_after)::timestamptz
       AND created_at <= sqlc.arg(created_before)::timestamptz
     ORDER BY created_at DESC, id DESC LIMIT 1)
    UNION ALL
    (SELECT 'success'::text AS kind, id, status, created_at, finished_at
     FROM workflow_runs
     WHERE app_id=sqlc.arg(app_id)::uuid AND workflow_name=sqlc.arg(workflow_name)::text
       AND created_at >= sqlc.arg(created_after)::timestamptz
       AND created_at <= sqlc.arg(created_before)::timestamptz AND status='succeeded'
     ORDER BY created_at DESC, id DESC LIMIT 1)
    UNION ALL
    (SELECT 'failure'::text AS kind, id, status, created_at, finished_at
     FROM workflow_runs
     WHERE app_id=sqlc.arg(app_id)::uuid AND workflow_name=sqlc.arg(workflow_name)::text
       AND created_at >= sqlc.arg(created_after)::timestamptz
       AND created_at <= sqlc.arg(created_before)::timestamptz AND status IN ('failed','dead')
     ORDER BY created_at DESC, id DESC LIMIT 1)
)
SELECT kind, id, status, created_at, finished_at FROM recent;

-- name: ListWorkflowAutomationHealthFailedSteps :many
SELECT COALESCE(s.foreach_parent, s.step_name) AS step_name,
       count(DISTINCT r.id)::bigint AS failed_run_count,
       max(COALESCE(r.finished_at, r.created_at))::timestamptz AS last_failed_at
FROM workflow_runs r
JOIN workflow_steps s ON s.run_id=r.id
WHERE r.app_id=sqlc.arg(app_id)::uuid AND r.workflow_name=sqlc.arg(workflow_name)::text
  AND r.created_at >= sqlc.arg(created_after)::timestamptz
  AND r.created_at <= sqlc.arg(created_before)::timestamptz
  AND r.status IN ('failed','dead') AND s.status IN ('failed','dead')
GROUP BY COALESCE(s.foreach_parent, s.step_name)
ORDER BY failed_run_count DESC, step_name
LIMIT sqlc.arg(max_steps)::int;

-- name: GetWorkflowAutomationRevision :one
SELECT * FROM workflow_automation_revisions WHERE app_id=$1 AND name=$2 AND version=$3;

-- name: InsertWorkflowAutomationRevision :exec
INSERT INTO workflow_automation_revisions(
 app_id,name,version,definition,recorded_at,legacy_snapshot,published_by_account_id,published_by_api_key_id
) VALUES($1,$2,$3,$4,$5,false,$6,$7);

-- name: SaveAutomation :exec
INSERT INTO workflow_automation_definitions(app_id,name,version,draft,published,published_version,enabled,updated_at)
VALUES($1,$2,$3,$4,$5,$6,$7,$8)
ON CONFLICT(app_id,name) DO UPDATE SET version=excluded.version,draft=excluded.draft,
 published=excluded.published,published_version=excluded.published_version,enabled=excluded.enabled,updated_at=excluded.updated_at;

-- name: DeleteAutomation :exec
DELETE FROM workflow_automation_definitions WHERE app_id=$1 AND name=$2;

-- name: DeleteAutomationScheduleCursor :exec
DELETE FROM workflow_schedule_cursors WHERE app_id=$1 AND workflow_name=$2;

-- name: AutomationManifest :one
SELECT id,workflows FROM deployments WHERE app_id=$1 AND status='live' AND scope='default'
ORDER BY (traffic_percent>0) DESC,created_at DESC,id DESC LIMIT 1;

-- name: EffectiveWorkflowDefinitions :one
SELECT app_workflow_definitions(sqlc.arg(app_id)::uuid,sqlc.arg(manifest)::jsonb)::jsonb;

-- name: NextAutomationVersion :one
SELECT nextval('automation_definition_versions')::bigint;
-- name: WorkflowOutboundSigningKey :one
SELECT key_id, public_key_pem FROM cluster_signing_keys WHERE id=1 AND retired_at IS NULL;

-- name: WorkflowOutboundAttempt :one
SELECT a.account_id, r.app_id, r.platform_tenant_id, s.outbound_attempt_token
FROM workflow_runs r JOIN workflow_steps s ON s.run_id=r.id
JOIN apps a ON a.id=r.app_id JOIN accounts ac ON ac.id=a.account_id
WHERE r.id=sqlc.arg(run_id) AND s.step_name=sqlc.arg(step_name) AND s.attempt=sqlc.arg(attempt)
AND r.status='running' AND r.lease_until>clock_timestamp() AND s.status='running'
AND s.outbound_attempt_token IS NOT NULL AND a.status<>'deleted'
AND NOT a.maintenance_mode AND (NOT a.platform_tenant_required OR r.platform_tenant_id IS NOT NULL)
AND ac.status IN ('active','past_due') AND ac.abuse_hold_at IS NULL AND ac.plan IN ('hobby','pro','scale')
AND (r.platform_tenant_id IS NULL OR EXISTS (
 SELECT 1 FROM platform_tenants t WHERE t.id=r.platform_tenant_id AND t.account_id=a.account_id AND t.status='active'
 AND (
  EXISTS (SELECT 1 FROM api_consumers c WHERE c.account_id=a.account_id AND c.app_id=a.id AND c.platform_tenant_id=t.id AND c.status='active' AND c.revoked_at IS NULL)
  OR EXISTS (SELECT 1 FROM tenant_surfaces ts WHERE ts.account_id=a.account_id AND ts.app_id=a.id AND ts.platform_tenant_id=t.id AND ts.status='active')
 )
));

-- name: AuthorizeWorkflowOutbound :one
SELECT EXISTS(
 SELECT 1 FROM workflow_runs r JOIN workflow_steps s ON s.run_id=r.id
 JOIN apps a ON a.id=r.app_id JOIN accounts ac ON ac.id=a.account_id
 JOIN outbound_app_bindings b ON b.app_id=a.id AND b.account_id=a.account_id
 JOIN outbound_integrations i ON i.id=b.integration_id AND i.account_id=a.account_id
 JOIN outbound_integration_credentials c ON c.integration_id=i.id AND c.account_id=a.account_id
 WHERE r.id=sqlc.arg(run_id) AND r.app_id=sqlc.arg(app_id) AND a.account_id=sqlc.arg(account_id)
 AND s.step_name=sqlc.arg(step_name) AND s.attempt=sqlc.arg(attempt)
 AND s.outbound_attempt_token=sqlc.arg(attempt_token) AND r.status='running'
 AND r.lease_until>clock_timestamp() AND s.status='running' AND a.status<>'deleted'
 AND NOT a.maintenance_mode AND (NOT a.platform_tenant_required OR r.platform_tenant_id IS NOT NULL)
 AND ac.status IN ('active','past_due') AND ac.abuse_hold_at IS NULL AND ac.plan IN ('hobby','pro','scale')
 AND COALESCE(r.platform_tenant_id::text, '')=COALESCE(sqlc.arg(tenant_id)::text, '')
 AND (r.platform_tenant_id IS NULL OR EXISTS (
  SELECT 1 FROM platform_tenants t WHERE t.id=r.platform_tenant_id AND t.account_id=a.account_id AND t.status='active'
  AND (
   EXISTS (SELECT 1 FROM api_consumers tc WHERE tc.account_id=a.account_id AND tc.app_id=a.id AND tc.platform_tenant_id=t.id AND tc.status='active' AND tc.revoked_at IS NULL)
   OR EXISTS (SELECT 1 FROM tenant_surfaces ts WHERE ts.account_id=a.account_id AND ts.app_id=a.id AND ts.platform_tenant_id=t.id AND ts.status='active')
  )
 ))
 AND i.id=sqlc.arg(integration_id) AND i.enabled AND i.owner_kind='customer'
 AND i.provider_auth_mode='managed' AND i.credential_source='customer_sealed'
 AND workflow_step_definition(r.definition_snapshot,s.step_name,s.foreach_parent,s.foreach_index)#>>'{outbound,integration_id}'=i.id::text
 AND workflow_step_definition(r.definition_snapshot,s.step_name,s.foreach_parent,s.foreach_index)#>>'{outbound,method}'=sqlc.arg(method)::text
 AND workflow_step_definition(r.definition_snapshot,s.step_name,s.foreach_parent,s.foreach_index)#>>'{outbound,path}'=sqlc.arg(path_template)::text
 AND coalesce(workflow_step_definition(r.definition_snapshot,s.step_name,s.foreach_parent,s.foreach_index)->'outbound'->'query','{}'::jsonb)=sqlc.arg(query_template)::jsonb
);

-- name: WorkflowOutboundBinding :one
SELECT i.allowed_methods, i.allowed_path_prefixes,
 b.allowed_methods AS binding_methods, b.allowed_path_prefixes AS binding_paths
FROM outbound_integrations i JOIN outbound_app_bindings b ON b.integration_id=i.id AND b.account_id=i.account_id
JOIN outbound_integration_credentials c ON c.integration_id=i.id AND c.account_id=i.account_id
WHERE i.id=sqlc.arg(integration_id) AND b.app_id=sqlc.arg(app_id) AND i.account_id=sqlc.arg(account_id)
AND i.enabled AND i.owner_kind='customer' AND i.provider_auth_mode='managed' AND i.credential_source='customer_sealed';

-- name: MarkWorkflowOutboundUnknown :exec
UPDATE workflow_steps s SET status='dead', finished_at=now(),
 error='outbound result unknown; unsafe to repeat', outbound_attempt_token=NULL
FROM workflow_runs r
WHERE s.run_id=r.id AND r.id=sqlc.arg(run_id) AND s.status='running'
AND workflow_step_definition(r.definition_snapshot,s.step_name,s.foreach_parent,s.foreach_index)#>>'{outbound,method}' NOT IN ('GET','HEAD')
 AND coalesce(workflow_step_definition(r.definition_snapshot,s.step_name,s.foreach_parent,s.foreach_index)#>>'{outbound,idempotency_supported}','false') <> 'true' ;

-- name: CloseWorkflowOutboundUnknownAttempts :exec
UPDATE workflow_step_attempts t SET status='failed',finished_at=now(),
error=CASE WHEN s.status='dead' THEN s.error
 WHEN s.foreach_parent IS NOT NULL AND jsonb_typeof(workflow_step_definition(r.definition_snapshot,s.step_name,s.foreach_parent,s.foreach_index)->'outbound') IS DISTINCT FROM 'object'
 THEN 'for_each item result unknown after recovery' ELSE 'outbound result unknown after recovery' END
FROM workflow_steps s, workflow_runs r
WHERE t.run_id=sqlc.arg(run_id) AND s.run_id=t.run_id AND r.id=s.run_id
AND s.step_name=t.step_name AND s.attempt=t.attempt AND s.status IN ('running','dead') AND t.status='running'
AND (r.resume_count>0 OR s.foreach_parent IS NOT NULL OR jsonb_typeof(workflow_step_definition(r.definition_snapshot,s.step_name,s.foreach_parent,s.foreach_index)->'outbound')='object');

-- name: LockWorkflowRecovery :one
SELECT status FROM workflow_runs WHERE id=sqlc.arg(run_id) FOR UPDATE;

-- name: ResetWorkflowRunningSteps :exec
UPDATE workflow_steps s
SET status='pending',
attempt=CASE WHEN (s.foreach_parent IS NOT NULL OR jsonb_typeof(workflow_step_definition(r.definition_snapshot,s.step_name,s.foreach_parent,s.foreach_index)->'outbound')='object')
 THEN s.attempt WHEN r.resume_count>0 THEN s.attempt ELSE GREATEST(s.attempt-1,0) END,
finished_at=NULL,error=NULL,next_retry_at=NULL,outbound_attempt_token=NULL
FROM workflow_runs r WHERE s.run_id=r.id AND r.id=sqlc.arg(run_id) AND s.status='running';

-- name: WorkflowOutboundCompletionCurrent :one
SELECT NOT EXISTS(
 SELECT 1 FROM workflow_runs r JOIN workflow_steps s ON s.run_id=r.id
 WHERE r.id=sqlc.arg(run_id) AND s.step_name=sqlc.arg(step_name)
 AND (r.resume_count>0 OR s.foreach_parent IS NOT NULL OR jsonb_typeof(workflow_step_definition(r.definition_snapshot,s.step_name,s.foreach_parent,s.foreach_index)->'outbound')='object')
 AND (r.status<>'running' OR s.status<>'running' OR s.attempt<>sqlc.arg(attempt)
  OR ((r.resume_count>0 OR s.foreach_parent IS NOT NULL) AND (r.lease_until IS NULL OR r.lease_until<=clock_timestamp())))
);

-- name: WorkflowOutboundStartCurrent :one
SELECT NOT EXISTS(
 SELECT 1 FROM workflow_runs r JOIN workflow_steps s ON s.run_id=r.id
 WHERE r.id=sqlc.arg(run_id) AND s.step_name=sqlc.arg(step_name)
 AND (r.resume_count>0 OR s.foreach_parent IS NOT NULL OR jsonb_typeof(workflow_step_definition(r.definition_snapshot,s.step_name,s.foreach_parent,s.foreach_index)->'outbound')='object')
 AND (r.status<>'running' OR r.lease_until IS NULL OR r.lease_until<=clock_timestamp() OR s.status<>'pending' OR s.attempt<>sqlc.arg(attempt)::integer-1)
);

-- name: CancelWorkflowOutboundAttempts :exec
UPDATE workflow_step_attempts t SET status='failed',finished_at=now(),error=sqlc.arg(reason)::text
FROM workflow_runs r
WHERE t.run_id=r.id AND r.id=sqlc.arg(run_id) AND t.status='running'
AND EXISTS(SELECT 1 FROM workflow_steps s WHERE s.run_id=t.run_id AND s.step_name=t.step_name AND (r.resume_count>0 OR s.foreach_parent IS NOT NULL OR jsonb_typeof(workflow_step_definition(r.definition_snapshot,s.step_name,s.foreach_parent,s.foreach_index)->'outbound')='object'));

-- name: LockWorkflowGuardRun :one
SELECT status, input, definition_snapshot FROM workflow_runs
WHERE id=sqlc.arg(run_id) FOR UPDATE;

-- name: LockWorkflowGuardStep :one
SELECT status, when_matched, when_evaluated_at FROM workflow_steps
WHERE run_id=sqlc.arg(run_id) AND step_name=sqlc.arg(step_name) FOR UPDATE;

-- name: WorkflowGuardOutputs :many
SELECT step_name, status, output FROM workflow_steps WHERE run_id=sqlc.arg(run_id);

-- name: RecordWorkflowGuardDecision :exec
UPDATE workflow_steps SET when_matched=sqlc.arg(matched)::boolean, when_evaluated_at=clock_timestamp(),
status=CASE WHEN sqlc.arg(matched)::boolean THEN status ELSE 'skipped' END,
skip_reason=CASE WHEN sqlc.arg(matched)::boolean THEN NULL ELSE 'when_false' END,
finished_at=CASE WHEN sqlc.arg(matched)::boolean THEN finished_at ELSE clock_timestamp() END,
next_retry_at=CASE WHEN sqlc.arg(matched)::boolean THEN next_retry_at ELSE NULL END
WHERE run_id=sqlc.arg(run_id) AND step_name=sqlc.arg(step_name) AND status='pending' AND when_matched IS NULL;

-- name: SkipPendingWorkflowStep :exec
UPDATE workflow_steps SET status='skipped',skip_reason=sqlc.arg(reason)::text,finished_at=clock_timestamp(),next_retry_at=NULL
WHERE run_id=sqlc.arg(run_id) AND step_name=sqlc.arg(step_name) AND status='pending';

-- name: WorkflowGuardStartAllowed :one
SELECT NOT EXISTS(
 SELECT 1 FROM workflow_runs r
 LEFT JOIN workflow_steps s ON s.run_id=r.id AND s.step_name=sqlc.arg(step_name)
 WHERE r.id=sqlc.arg(run_id) AND (
 jsonb_path_exists(r.definition_snapshot, '$.steps[*] ? (@.name == $name && @.join.type() == "object")', jsonb_build_object('name',sqlc.arg(step_name)::text))
 OR (s.when_matched IS DISTINCT FROM TRUE
 AND jsonb_path_exists(r.definition_snapshot, '$.steps[*] ? (@.name == $name && @.when.type() == "object")', jsonb_build_object('name',sqlc.arg(step_name)::text)))
 )
);

-- name: WorkflowJoinSteps :many
SELECT step_name, status, output, when_matched, skip_reason
FROM workflow_steps WHERE run_id=sqlc.arg(run_id);

-- name: CompleteWorkflowJoin :exec
UPDATE workflow_steps SET status=sqlc.arg(status)::text,output=sqlc.narg(output)::jsonb,
skip_reason=sqlc.narg(skip_reason)::text,finished_at=clock_timestamp(),next_retry_at=NULL
WHERE run_id=sqlc.arg(run_id) AND step_name=sqlc.arg(step_name) AND status='pending';

-- name: WorkflowControlSteps :many
SELECT step_name,status,attempt,input,output,when_matched,foreach_parent,foreach_index,foreach_count,next_retry_at,retry_base
FROM workflow_steps WHERE run_id=sqlc.arg(run_id) ORDER BY foreach_index NULLS FIRST,step_name;

-- name: InitializeWorkflowForEach :exec
UPDATE workflow_steps SET foreach_count=sqlc.arg(item_count)::integer,input=sqlc.arg(items)::jsonb,started_at=clock_timestamp()
WHERE run_id=sqlc.arg(run_id) AND step_name=sqlc.arg(step_name) AND status='pending' AND foreach_count IS NULL;

-- name: CreateWorkflowForEachItem :exec
INSERT INTO workflow_steps(run_id,step_name,status,input,foreach_parent,foreach_index)
VALUES(sqlc.arg(run_id),sqlc.arg(step_name),'pending',sqlc.arg(input)::jsonb,sqlc.arg(parent)::text,sqlc.arg(item_index)::integer);

-- name: CompleteWorkflowForEach :exec
UPDATE workflow_steps SET status=sqlc.arg(status)::text,output=sqlc.arg(output)::jsonb,error=sqlc.narg(error)::text,finished_at=clock_timestamp(),next_retry_at=NULL
WHERE run_id=sqlc.arg(run_id) AND step_name=sqlc.arg(step_name) AND status='pending';

-- name: SkipWorkflowForEachRemaining :exec
UPDATE workflow_steps SET status='skipped',skip_reason='dependency_failed',finished_at=clock_timestamp(),next_retry_at=NULL
WHERE run_id=sqlc.arg(run_id) AND foreach_parent=sqlc.arg(parent) AND status='pending';

-- name: WorkflowForEachStartAllowed :one
SELECT NOT EXISTS(SELECT 1 FROM workflow_steps s JOIN workflow_runs r ON r.id=s.run_id
WHERE s.run_id=sqlc.arg(run_id) AND s.step_name=sqlc.arg(step_name) AND (
 jsonb_typeof(workflow_step_definition(r.definition_snapshot,s.step_name,s.foreach_parent,s.foreach_index)->'for_each')='object'
 OR (s.status='skipped' AND s.when_matched IS FALSE)
 OR (s.foreach_parent IS NOT NULL AND (
   NOT EXISTS(SELECT 1 FROM workflow_steps p
    WHERE p.run_id=s.run_id AND p.step_name=s.foreach_parent AND p.status='pending' AND p.foreach_count>s.foreach_index)
   OR s.input<>sqlc.arg(input)::jsonb
   OR (SELECT count(*) FROM workflow_steps active
       WHERE active.run_id=s.run_id AND active.foreach_parent=s.foreach_parent AND active.status='running')>=sqlc.arg(parallel_limit)::integer
   OR s.foreach_index>=COALESCE((SELECT min(frontier.foreach_index)+sqlc.arg(parallel_limit)::integer
       FROM workflow_steps frontier
       WHERE frontier.run_id=s.run_id AND frontier.foreach_parent=s.foreach_parent
        AND frontier.status<>'succeeded'
        AND NOT (frontier.status='skipped' AND frontier.when_matched IS FALSE)
        AND NOT (frontier.status IN ('failed','dead')
         AND (workflow_step_definition(r.definition_snapshot,s.foreach_parent,NULL::text,NULL::integer)#>>'{for_each,on_item_failure}') IS NOT DISTINCT FROM 'continue')),-1)
   OR ((workflow_step_definition(r.definition_snapshot,s.foreach_parent,NULL::text,NULL::integer)#>>'{for_each,on_item_failure}') IS DISTINCT FROM 'continue'
       AND EXISTS(SELECT 1 FROM workflow_steps prev
        WHERE prev.run_id=s.run_id AND prev.foreach_parent=s.foreach_parent AND prev.foreach_index<s.foreach_index
         AND prev.status IN ('failed','dead')))
 ))
));

-- name: LockWorkflowResumeRun :one
SELECT id,app_id,workflow_name,status,current_step,input,output,definition_snapshot,
 scheduled_for,started_at,finished_at,last_error,created_at,updated_at,resume_count,cancelled_at,platform_tenant_id
FROM workflow_runs WHERE id=sqlc.arg(run_id) AND app_id=sqlc.arg(app_id) FOR UPDATE;

-- name: WorkflowResumeSteps :many
SELECT step_name,status,attempt,input,output,skip_reason,foreach_parent,foreach_index,foreach_count,retry_base
FROM workflow_steps WHERE run_id=sqlc.arg(run_id) ORDER BY step_name FOR UPDATE;

-- name: WorkflowResumeHasRunningAttempts :one
SELECT EXISTS(SELECT 1 FROM workflow_step_attempts WHERE run_id=sqlc.arg(run_id) AND status='running');

-- name: ResetWorkflowResumeStep :exec
UPDATE workflow_steps SET status='pending',retry_base=attempt,output=NULL,error=NULL,finished_at=NULL,
 next_retry_at=NULL,next_check_at=NULL,skip_reason=NULL,outbound_attempt_token=NULL
WHERE run_id=sqlc.arg(run_id) AND step_name=sqlc.arg(step_name);

-- name: EnqueueWorkflowResume :exec
UPDATE workflow_runs SET status='pending',resume_count=resume_count+1,scheduled_for=clock_timestamp(),
 finished_at=NULL,last_error=NULL,output=NULL,current_step=NULL,lease_until=NULL,updated_at=clock_timestamp()
WHERE id=sqlc.arg(run_id);

-- name: InsertWorkflowResume :one
INSERT INTO workflow_run_resumes(run_id,resume_number,account_id,previous_status,previous_error,resumed_steps)
VALUES(sqlc.arg(run_id),sqlc.arg(resume_number),sqlc.arg(account_id),sqlc.arg(previous_status),sqlc.narg(previous_error),sqlc.arg(resumed_steps))
RETURNING created_at;

-- name: ListWorkflowResumes :many
SELECT * FROM workflow_run_resumes WHERE run_id=sqlc.arg(run_id) ORDER BY resume_number;

-- name: RecordWorkflowCancellation :exec
UPDATE workflow_runs SET cancelled_at=clock_timestamp() WHERE id=sqlc.arg(run_id) AND status='failed';

-- name: MarkWorkflowRunStatusFenced :execrows
UPDATE workflow_runs SET status=sqlc.arg(status)::text,output=coalesce(sqlc.narg(output)::jsonb,output),
 last_error=coalesce(sqlc.narg(last_error)::text,last_error),
 started_at=CASE WHEN sqlc.arg(status)::text='running' AND started_at IS NULL THEN now() ELSE started_at END,
 finished_at=CASE WHEN sqlc.arg(status)::text IN ('succeeded','failed','dead') THEN now() ELSE finished_at END,
 updated_at=now()
WHERE id=sqlc.arg(run_id) AND (sqlc.narg(generation)::integer IS NULL OR resume_count=sqlc.narg(generation)::integer)
 AND (status NOT IN ('succeeded','failed','dead') OR status=sqlc.arg(status)::text);

-- name: WorkflowGenerationCurrent :one
SELECT EXISTS(SELECT 1 FROM workflow_runs WHERE id=sqlc.arg(run_id)
 AND (sqlc.narg(generation)::integer IS NULL OR resume_count=sqlc.narg(generation)::integer));

-- name: ScheduleWorkflowRunFenced :execrows
UPDATE workflow_runs SET status=sqlc.arg(status)::text,
 scheduled_for=CASE WHEN status='awaiting_event' AND sqlc.arg(status)::text='awaiting_event'
 THEN least(scheduled_for,sqlc.arg(scheduled_for)::timestamptz) ELSE sqlc.arg(scheduled_for)::timestamptz END,
 updated_at=now()
WHERE id=sqlc.arg(run_id) AND (sqlc.narg(generation)::integer IS NULL OR resume_count=sqlc.narg(generation)::integer)
 AND (status NOT IN ('succeeded','failed','dead') OR status=sqlc.arg(status)::text);

-- name: SetWorkflowRunWakeFenced :execrows
UPDATE workflow_runs SET status=sqlc.arg(status)::text,scheduled_for=sqlc.arg(scheduled_for),updated_at=now()
WHERE id=sqlc.arg(run_id) AND (sqlc.narg(generation)::integer IS NULL OR resume_count=sqlc.narg(generation)::integer)
 AND status IN ('running','pending','awaiting_event') AND NOT(status='pending' AND scheduled_for<=now());

-- name: ExtendWorkflowRunLeaseFenced :execrows
UPDATE workflow_runs SET lease_until=now()+(sqlc.arg(timeout_ms)::bigint*interval '1 millisecond')+interval '5 minutes'
WHERE id=sqlc.arg(run_id) AND status='running'
 AND (sqlc.narg(generation)::integer IS NULL OR resume_count=sqlc.narg(generation)::integer);

-- name: LockCustomerOperationDeliveryOwner :one
SELECT record FROM customer_operations WHERE id=sqlc.arg(id)::uuid AND account_id=sqlc.arg(account_id)::uuid FOR UPDATE;

-- name: GetCustomerOperationDeliveryRetry :one
SELECT delivery_id::text, expected_replay_generation, replay_generation, queued_at, expires_at
FROM customer_operation_delivery_retries WHERE operation_id=sqlc.arg(operation_id)::uuid AND retry_id=sqlc.arg(retry_id)::text;

-- name: CountCustomerOperationDeliveryRetries :one
SELECT count(*) FROM customer_operation_delivery_retries WHERE operation_id=sqlc.arg(operation_id)::uuid;

-- name: InsertCustomerOperationDeliveryRetry :exec
INSERT INTO customer_operation_delivery_retries(operation_id,retry_id,delivery_id,expected_replay_generation,replay_generation,queued_at,expires_at)
VALUES(sqlc.arg(operation_id)::uuid,sqlc.arg(retry_id)::text,sqlc.arg(delivery_id)::uuid,sqlc.arg(expected_replay_generation)::integer,sqlc.arg(replay_generation)::integer,sqlc.arg(queued_at)::timestamptz,sqlc.arg(expires_at)::timestamptz);

-- name: GetCustomerOperationDeliveryPlan :one
SELECT plan FROM accounts WHERE id=sqlc.arg(account_id)::uuid;

-- name: GetCustomerOperationDeliveryRow :one
SELECT id::text, webhook_id::text, app_id::text, account_id::text, event, payload,
 attempt,status,last_error,last_response_code,next_attempt_at,delivered_at,replay_generation
FROM app_webhook_deliveries WHERE id=sqlc.arg(id)::uuid AND account_id=sqlc.arg(account_id)::uuid;

-- name: LockCustomerOperationDeliveryRow :one
SELECT id::text, webhook_id::text, app_id::text, account_id::text, event, payload,
 attempt,status,last_error,last_response_code,next_attempt_at,delivered_at,replay_generation
FROM app_webhook_deliveries WHERE id=sqlc.arg(id)::uuid AND account_id=sqlc.arg(account_id)::uuid FOR UPDATE;

-- name: ResetCustomerOperationDelivery :execrows
UPDATE app_webhook_deliveries SET status='pending',attempt=0,replay_generation=replay_generation+1,
 last_error='',last_response_code=0,next_attempt_at=sqlc.arg(now)::timestamptz,updated_at=sqlc.arg(now)::timestamptz
WHERE id=sqlc.arg(id)::uuid AND account_id=sqlc.arg(account_id)::uuid AND status='dead'
 AND replay_generation=sqlc.arg(expected_replay_generation)::integer;

-- name: GetManagedPostgresUsageImport :one
SELECT request_sha256, result FROM managed_postgres_usage_imports
WHERE account_id = $1 AND import_id = $2;

-- name: InsertManagedPostgresUsageImport :exec
INSERT INTO managed_postgres_usage_imports (
 account_id, import_id, database_id, actor_id, reason, evidence_reference, evidence_sha256,
 request_sha256, preview_revision, request, policy, before_records, after_records, result, created_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15);

-- name: ListManagedPostgresImportRecords :many
SELECT * FROM managed_postgres_usage
WHERE database_id = sqlc.arg(database_id) AND window_from < sqlc.arg(window_to) AND window_to > sqlc.arg(window_from)
ORDER BY window_from, meter;

-- name: GetManagedPostgresRawUsageCoverage :one
SELECT * FROM managed_postgres_usage_coverage WHERE database_id = $1 AND window_seconds = $2;

-- name: HasManagedPostgresIncompatibleUsageWindow :one
SELECT EXISTS (SELECT 1 FROM managed_postgres_usage
 WHERE database_id = $1 AND window_to - window_from <> sqlc.arg(window_seconds)::bigint * interval '1 second');

-- name: UpsertManagedPostgresUsageRecord :exec
INSERT INTO managed_postgres_usage (
 account_id, database_id, backend_id, backend_fingerprint, window_from, window_to, observed_at, meter, quantity, cost_millicents
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
ON CONFLICT (database_id, window_from, window_to, meter) DO UPDATE SET
 observed_at = EXCLUDED.observed_at, quantity = EXCLUDED.quantity, cost_millicents = EXCLUDED.cost_millicents
WHERE managed_postgres_usage.observed_at <= EXCLUDED.observed_at;

-- name: UpsertManagedPostgresUsageCoverage :exec
INSERT INTO managed_postgres_usage_coverage (database_id, window_seconds, collected_from, collected_until, observed_at)
VALUES ($1,$2,$3,$4,$5)
ON CONFLICT (database_id, window_seconds) DO UPDATE SET
 collected_from = EXCLUDED.collected_from, collected_until = EXCLUDED.collected_until,
 observed_at = EXCLUDED.observed_at, updated_at = now();

-- name: ObjectWriteProtectionClock :one
SELECT clock_timestamp()::timestamptz;

-- name: ObjectWriteProtectionBucketLock :exec
SELECT id FROM object_buckets WHERE id=$1 FOR NO KEY UPDATE;

-- ADR-591: operator reconciliation only repairs an unresolved deleted catalog row.
-- name: GetManagedPostgresAccountingReconciliation :one
SELECT request_sha256, result FROM managed_postgres_accounting_reconciliations
WHERE account_id = $1 AND reconciliation_id = $2;

-- name: InsertManagedPostgresAccountingReconciliation :exec
INSERT INTO managed_postgres_accounting_reconciliations (
 account_id, reconciliation_id, database_id, backend_id, backend_fingerprint, provider_resource_id,
 actor_id, reason, evidence_reference, evidence_sha256, request_sha256, preview_revision,
 request, policy, before_catalog, after_catalog, coverage_before, result, created_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19);

-- name: LockManagedPostgresReconciliationIdentity :exec
SELECT pg_advisory_xact_lock(hashtextextended(sqlc.arg(identity_scope)::text,587));

-- name: HasManagedPostgresReconciliationIdentity :one
SELECT EXISTS (SELECT 1 FROM managed_postgres_databases
 WHERE backend_id = sqlc.arg(backend_id)::text AND backend_fingerprint = sqlc.arg(backend_fingerprint)::text
 AND provider_resource_id = sqlc.arg(provider_resource_id)::text AND id <> sqlc.arg(database_id)::uuid)
 OR EXISTS (SELECT 1 FROM managed_postgres_accounting_reconciliations
 WHERE backend_id = sqlc.arg(backend_id)::text AND backend_fingerprint = sqlc.arg(backend_fingerprint)::text
 AND provider_resource_id = sqlc.arg(provider_resource_id)::text AND database_id <> sqlc.arg(database_id)::uuid) AS claimed;

-- name: ListManagedPostgresReconciliationCoverage :many
SELECT * FROM managed_postgres_usage_coverage WHERE database_id = $1 ORDER BY window_seconds LIMIT 2;

-- name: GetManagedPostgresReconciliationLedger :one
SELECT count(*)::bigint AS records, min(window_from)::timestamptz AS first_window,
 max(window_to)::timestamptz AS last_window, max(observed_at)::timestamptz AS last_observation,
 COALESCE(bool_or(account_id <> sqlc.arg(account_id)::uuid OR backend_id <> sqlc.arg(backend_id)::text
 OR backend_fingerprint <> sqlc.arg(backend_fingerprint)::text OR meter <> ALL(sqlc.arg(meters)::text[])
 OR window_to - window_from <> sqlc.arg(window_seconds)::bigint * interval '1 second'
 OR mod(extract(epoch FROM window_from),NULLIF(sqlc.arg(window_seconds)::bigint,0)) <> 0),false)::boolean AS invalid
FROM managed_postgres_usage WHERE database_id = sqlc.arg(database_id)::uuid;

-- name: ReconcileManagedPostgresLegacyResource :execrows
UPDATE managed_postgres_databases SET provider_resource_id = sqlc.arg(provider_resource_id)::text,
 deleted_at = sqlc.arg(shutdown_at)::timestamptz, updated_at = sqlc.arg(now)::timestamptz
WHERE id = sqlc.arg(id)::uuid AND account_id = sqlc.arg(account_id)::uuid
 AND backend_id = sqlc.arg(backend_id)::text AND backend_fingerprint = sqlc.arg(backend_fingerprint)::text
 AND state = 'deleted' AND accounting_required AND NULLIF(provider_resource_id,'') IS NULL
 AND (lease_until IS NULL OR lease_until <= sqlc.arg(now)::timestamptz);

-- name: ResetManagedPostgresReconciliationCoverage :exec
DELETE FROM managed_postgres_usage_coverage WHERE database_id = $1;

-- name: GetManagedPostgresResize :one
SELECT * FROM managed_postgres_resizes WHERE account_id=$1 AND id=$2;

-- name: ActiveManagedPostgresResize :one
SELECT * FROM managed_postgres_resizes WHERE account_id=$1 AND database_id=$2 AND state='pending';

-- name: ReadManagedPostgresResizeConflicts :one
SELECT EXISTS(SELECT 1 FROM managed_postgres_bindings WHERE database_id=$1 AND state<>'deleted'
    AND (state<>'ready' OR rotation_previous_generation<>0 OR lease_token IS NOT NULL)) AS unfinished_bindings,
    EXISTS(SELECT 1 FROM managed_postgres_databases WHERE restore_source_database_id=$1 AND state NOT IN ('ready','deleted')) AS unfinished_restores;

-- name: BeginManagedPostgresResizeDatabase :one
UPDATE managed_postgres_databases SET state='updating',desired_generation=desired_generation+1,
    last_error_code=NULL,lease_token=NULL,lease_until=NULL,attempt_count=0,retry_at=sqlc.arg(at)::timestamptz,updated_at=sqlc.arg(at)::timestamptz
WHERE id=sqlc.arg(id)::uuid AND state='ready' AND desired_generation=sqlc.arg(source_generation)::bigint
    AND desired_generation=observed_generation AND clone_resource_role='target' AND environment_clone_operation_id IS NULL
    AND cutover_id IS NULL AND provider_resource_id IS NOT NULL AND data_resource_id IS NOT NULL AND deleted_at IS NULL RETURNING *;

-- name: InsertManagedPostgresResize :one
INSERT INTO managed_postgres_resizes(id,account_id,database_id,backend_id,backend_fingerprint,provider_resource_id,data_resource_id,
    source_spec,target_class,target_scale_to_zero,generation,state,created_at)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,'pending',$12) RETURNING *;

-- name: ClaimManagedPostgresResize :one
UPDATE managed_postgres_databases SET lease_token=sqlc.arg(lease_token)::text,lease_until=sqlc.arg(until)::timestamptz,
    attempt_count=least(attempt_count+1,30),retry_at=sqlc.arg(at)::timestamptz,updated_at=sqlc.arg(at)::timestamptz
WHERE id=sqlc.arg(id)::uuid AND account_id=sqlc.arg(account)::uuid AND state='updating'
    AND (lease_until IS NULL OR lease_until<=sqlc.arg(at)::timestamptz) AND retry_at<=sqlc.arg(at)::timestamptz
    AND EXISTS(SELECT 1 FROM managed_postgres_resizes r WHERE r.database_id=managed_postgres_databases.id AND r.state='pending'
        AND r.generation=managed_postgres_databases.desired_generation) RETURNING *;

-- name: CompleteManagedPostgresResize :execrows
UPDATE managed_postgres_resizes SET state='succeeded',completed_at=sqlc.arg(at)::timestamptz
WHERE id=sqlc.arg(id)::uuid AND database_id=sqlc.arg(database)::uuid AND generation=sqlc.arg(generation)::bigint AND state='pending';

-- name: FinishManagedPostgresResizeDatabase :one
UPDATE managed_postgres_databases SET state='ready',service_class=sqlc.arg(target_class)::text,scale_to_zero=sqlc.arg(target_scale_to_zero)::boolean,observed_generation=desired_generation,
    last_error_code=NULL,lease_token=NULL,lease_until=NULL,attempt_count=0,retry_at=sqlc.arg(at)::timestamptz,updated_at=sqlc.arg(at)::timestamptz
WHERE id=sqlc.arg(id)::uuid AND account_id=sqlc.arg(account)::uuid AND state='updating'
    AND desired_generation=sqlc.arg(generation)::bigint AND observed_generation=desired_generation-1
    AND lease_token=sqlc.arg(token)::text AND lease_until>sqlc.arg(at)::timestamptz AND lease_until>clock_timestamp() RETURNING *;

-- name: ReadServiceBindingRevision :one
SELECT (r.epoch::text || ':' || r.service_revision::text)::text AS revision
FROM app_binding_promotion_revisions r JOIN apps a ON a.id=r.app_id
WHERE r.app_id=sqlc.arg(app_id) AND a.account_id=sqlc.arg(account_id) AND a.status<>'deleted';

-- name: ReadBindingReleasePolicy :one
SELECT COALESCE(to_jsonb(p), '{}'::jsonb)::jsonb AS policy
FROM apps a LEFT JOIN app_binding_release_policies p ON p.app_id=a.id AND p.scope=sqlc.arg(scope)
WHERE a.id=sqlc.arg(app_id) AND a.account_id=sqlc.arg(account_id) AND a.status<>'deleted';

-- name: WriteBindingReleasePolicy :one
INSERT INTO app_binding_release_policies(app_id,scope,mode,revision,max_age_seconds,require_application_ack,reason)
SELECT a.id,sqlc.arg(scope),sqlc.arg(mode),sqlc.arg(expected_revision)::bigint+1,sqlc.arg(max_age_seconds),sqlc.arg(require_application_ack),sqlc.arg(reason)
FROM apps a WHERE a.id=sqlc.arg(app_id) AND a.account_id=sqlc.arg(account_id) AND a.status<>'deleted'
 AND sqlc.arg(expected_revision)::bigint=0
ON CONFLICT(app_id,scope) DO UPDATE SET mode=EXCLUDED.mode,revision=app_binding_release_policies.revision+1,
 max_age_seconds=EXCLUDED.max_age_seconds,require_application_ack=EXCLUDED.require_application_ack,reason=EXCLUDED.reason,updated_at=clock_timestamp()
WHERE app_binding_release_policies.revision=sqlc.arg(expected_revision)::bigint
RETURNING to_jsonb(app_binding_release_policies)::jsonb AS policy;

-- name: UpdateBindingReleasePolicy :one
UPDATE app_binding_release_policies p SET mode=sqlc.arg(mode),revision=p.revision+1,max_age_seconds=sqlc.arg(max_age_seconds),
 require_application_ack=sqlc.arg(require_application_ack),reason=sqlc.arg(reason),updated_at=clock_timestamp()
FROM apps a WHERE p.app_id=a.id AND a.id=sqlc.arg(app_id) AND a.account_id=sqlc.arg(account_id) AND a.status<>'deleted'
 AND p.scope=sqlc.arg(scope) AND p.revision=sqlc.arg(expected_revision)
RETURNING to_jsonb(p)::jsonb AS policy;

-- name: AuthorizeBindingReleaseTraffic :one
SELECT authorize_binding_release_traffic(sqlc.arg(fences)::jsonb)::boolean;

-- name: ServiceRolloutBindingEnforced :one
SELECT EXISTS (SELECT 1 FROM app_binding_release_policies WHERE app_id = sqlc.arg(app_id) AND scope = sqlc.arg(scope) AND mode = 'enforce')::boolean;

-- name: SaveServiceRolloutHandoff :exec
UPDATE deployments SET service_rollout_handoff = sqlc.arg(handoff)::jsonb WHERE id = sqlc.arg(deployment_id);

-- name: ServiceRolloutRecipientTraffic :one
SELECT traffic_percent FROM deployments WHERE id = sqlc.arg(deployment_id) AND status = 'live';

-- name: ServiceRolloutRecipientReady :one
SELECT (count(*) >= CASE WHEN sqlc.arg(action)::text = 'promote' THEN coalesce((SELECT (a.manifest->'service_replicas'->>'desired')::integer FROM apps a WHERE a.id = sqlc.arg(app_id)), 1) ELSE 1 END)::boolean
FROM (SELECT i.id FROM instances i WHERE i.deployment_id = sqlc.arg(deployment_id)::uuid AND i.mode = 'service' AND i.state = 'running' FOR SHARE) ready;

-- name: AppendServiceRolloutBindingAudit :one
INSERT INTO deployment_audit(deployment_id, account_id, kind, actor, at, data)
VALUES (sqlc.arg(deployment_id)::uuid, (SELECT account_id FROM apps WHERE id = sqlc.arg(app_id)::uuid), sqlc.arg(kind), sqlc.arg(actor), clock_timestamp(), sqlc.arg(data)::jsonb)
RETURNING id;

-- name: LockCheckedRollbackApp :one
SELECT id FROM apps WHERE id=sqlc.arg(app_id) AND account_id=sqlc.arg(account_id) AND status IN ('active','evicted_cold') FOR UPDATE;

-- name: CheckedRollbackTargetFacts :one
SELECT jsonb_build_object('id',d.id,'app_id',d.app_id,'scope',d.scope,'status',d.status,'traffic_percent',d.traffic_percent,
 'canary_total_steps',d.canary_total_steps,'canary_step',d.canary_step,'rollout_state',d.rollout_state,'rootfs_key',coalesce(d.rootfs_key,''),
 'image_digest',d.image_digest,'environment_workload_runtime',d.environment_workload_runtime,
 'service_rollout_handoff',d.service_rollout_handoff) FROM deployments d
 WHERE d.id=sqlc.arg(deployment_id) AND d.app_id=sqlc.arg(app_id) FOR UPDATE;

-- name: CheckedRollbackCurrentMatches :one
SELECT EXISTS(SELECT 1 FROM deployments d WHERE d.id=sqlc.arg(current_id) AND d.app_id=sqlc.arg(app_id)
 AND d.scope=sqlc.arg(scope) AND d.status='live' AND d.traffic_percent=100
 AND (d.canary_total_steps=0 OR d.canary_step>=d.canary_total_steps) AND d.rollout_state NOT IN ('pending','rolling_out'))
 AND NOT EXISTS(SELECT 1 FROM deployments d WHERE d.app_id=sqlc.arg(app_id) AND d.scope=sqlc.arg(scope)
 AND d.status='live' AND d.id<>sqlc.arg(current_id) AND d.id<>sqlc.arg(target_id)
 AND (d.traffic_percent>0 OR d.rollout_state IN ('pending','rolling_out')));

-- name: InsertCheckedRollback :exec
INSERT INTO deployment_rollback_operations(id,app_id,scope,target_deployment_id,current_deployment_id,status,receipt)
 VALUES(sqlc.arg(id),sqlc.arg(app_id),sqlc.arg(scope),sqlc.arg(target_id),sqlc.arg(current_id),sqlc.arg(status),sqlc.arg(receipt));

-- name: ReadCheckedRollback :one
SELECT r.receipt FROM deployment_rollback_operations r JOIN apps a ON a.id=r.app_id
 WHERE r.id=sqlc.arg(id) AND r.app_id=sqlc.arg(app_id) AND a.account_id=sqlc.arg(account_id) AND a.status<>'deleted';

-- name: LockCheckedRollback :one
SELECT receipt FROM deployment_rollback_operations WHERE id=sqlc.arg(id) AND app_id=sqlc.arg(app_id) FOR UPDATE;

-- name: CheckedRollbackForTarget :one
SELECT receipt FROM deployment_rollback_operations WHERE target_deployment_id=sqlc.arg(target_id)
 AND status NOT IN ('complete','failed') ORDER BY updated_at,id LIMIT 1;

-- name: ListPendingCheckedRollbacks :many
SELECT receipt FROM deployment_rollback_operations WHERE status NOT IN ('complete','failed') ORDER BY updated_at,id LIMIT sqlc.arg(batch_size);

-- name: SaveCheckedRollback :exec
UPDATE deployment_rollback_operations SET status=sqlc.arg(status),receipt=sqlc.arg(receipt),updated_at=clock_timestamp()
 WHERE id=sqlc.arg(id);

-- name: PrepareCheckedRollbackTarget :exec
UPDATE deployments SET status='snapshotting',error='',error_code='',traffic_percent=0,traffic_percent_explicit=true,
 canary_preset='none',canary_step=0,canary_total_steps=0,canary_stages=null,canary_step_started_at=clock_timestamp(),
 rollout_state=sqlc.arg(rollout_state),rollout_started_at=null,rollout_completed_at=null,rollout_aborted_at=null,rollout_aborted_reason='',
 service_rollout_handoff=sqlc.arg(handoff),api_hosting_receipt='{}'::jsonb,
 stage_state=jsonb_build_object('current','snapshot_prepare','current_started_at',clock_timestamp(),'history',coalesce(stage_state->'history','[]'::jsonb))
 WHERE id=sqlc.arg(target_id);

-- name: MarkCheckedRollbackReady :exec
UPDATE deployments SET status='live',error='',error_code='',traffic_percent=0,
 rollout_started_at=CASE WHEN rollout_state='rolling_out' THEN clock_timestamp() ELSE NULL END WHERE id=sqlc.arg(target_id);

-- name: AuthorizeCheckedRollback :one
SELECT set_config('faas.checked_rollback_request',sqlc.arg(request_id)::text,true);

-- name: CutoverCheckedRollbackTarget :exec
UPDATE deployments SET traffic_percent=100,rollout_state='complete',rollout_completed_at=clock_timestamp() WHERE id=sqlc.arg(target_id);

-- name: RetireCheckedRollbackSiblings :exec
UPDATE deployments d SET traffic_percent=0,status=CASE WHEN EXISTS(SELECT 1 FROM deployment_revision_pins p
 WHERE p.deployment_id=d.id AND p.expires_at>clock_timestamp()) OR EXISTS(SELECT 1 FROM project_release_members rm
 JOIN project_release_sets rs ON rs.id=rm.release_id WHERE rm.deployment_id=d.id AND (rs.active OR rs.expires_at>clock_timestamp()))
 THEN 'live' ELSE 'superseded' END
 WHERE d.app_id=sqlc.arg(app_id) AND d.scope=sqlc.arg(scope) AND d.status='live' AND d.id<>sqlc.arg(target_id);

-- name: RetainCheckedRollbackPredecessor :exec
INSERT INTO deployment_revision_pins(deployment_id,app_id,expires_at)
 SELECT d.id,d.app_id,clock_timestamp()+((a.manifest->>'revision_pin_ttl_seconds')::integer*interval '1 second')
 FROM deployments d JOIN apps a ON a.id=d.app_id WHERE d.id=sqlc.arg(current_id)
 AND coalesce((a.manifest->>'revision_pin_ttl_seconds')::integer,0) BETWEEN 1 AND 604800
 ON CONFLICT(deployment_id) DO NOTHING;

-- name: FailCheckedRollbackTarget :exec
UPDATE deployments SET status='superseded',rollout_state='pending' WHERE id=sqlc.arg(target_id) AND traffic_percent=0;

-- name: FailedCheckedRollbackTarget :one
SELECT EXISTS(SELECT 1 FROM deployment_rollback_operations WHERE target_deployment_id=sqlc.arg(target_id) AND status='failed');

-- name: NotifyCheckedRollbackCutover :exec
SELECT pg_notify('deployment_changed',jsonb_build_object('kind','service_rollout','app_id',sqlc.arg(app_id)::text,'deployment_id',sqlc.arg(target_id)::text,'status','live')::text),
 pg_notify('deployment_changed',jsonb_build_object('app_id',sqlc.arg(app_id)::text,'deployment_id',sqlc.arg(current_id)::text,'status',
 (SELECT status FROM deployments WHERE id=sqlc.arg(current_id)::uuid))::text);

-- name: LockCheckedRollbackScope :many
SELECT id FROM deployments WHERE app_id=sqlc.arg(app_id) AND scope=sqlc.arg(scope) ORDER BY id FOR UPDATE;

-- name: LockAlertRollbackFireApp :many
SELECT a.id FROM apps a JOIN alert_rules r ON r.app_id=a.id AND r.account_id=a.account_id
 WHERE r.id=sqlc.arg(rule_id) AND r.action='rollback' FOR UPDATE OF a;

-- name: ReadAlertRollbackFireFacts :one
SELECT jsonb_build_object('rule_id',r.id,'account_id',r.account_id,'app_id',coalesce(r.app_id::text,''),
 'app_account_id',coalesce(a.account_id::text,''),'enabled',r.enabled,'action',r.action,'metric',r.metric,'name',r.name,
 'comparison',r.comparison,'threshold',r.threshold,'window_spec',r.window_spec,
 'service',coalesce(a.manifest->>'execution_mode'='service',false),'window_seconds',r.post_deploy_rollback_window_seconds,
 'deployments',coalesce((SELECT jsonb_agg(jsonb_build_object('id',d.id,'app_id',d.app_id,'scope',d.scope,'status',d.status,
 'traffic_percent',d.traffic_percent,'canary_total_steps',d.canary_total_steps,'canary_step',d.canary_step,
 'rollout_state',d.rollout_state,'created_at',d.created_at,'completed_at',d.rollout_completed_at,
 'recovery_predecessor_id',coalesce((SELECT predecessor_deployment_id::text FROM deployment_recovery_lineage WHERE deployment_id=d.id),''),
 'recovered',EXISTS(SELECT 1 FROM deployment_rollback_operations WHERE target_deployment_id=d.id) OR EXISTS(SELECT 1 FROM deployment_audit WHERE kind='deploy.rolled_back' AND (deployment_id=d.id OR data->>'predecessor_deployment_id'=d.id::text)) OR EXISTS(SELECT 1 FROM alert_rollback_actions WHERE receipt->>'predecessor_deployment_id'=d.id::text AND status='complete'),
 'predecessor_deployment_id',coalesce(d.service_rollout_handoff->>'predecessor_deployment_id',''))) FROM deployments d WHERE d.app_id=r.app_id AND d.status IN ('live','superseded') AND d.deleted_at IS NULL),'[]'::jsonb))
 FROM alert_rules r LEFT JOIN apps a ON a.id=r.app_id AND a.status<>'deleted' WHERE r.id=sqlc.arg(rule_id) AND r.action='rollback';

-- name: InsertAlertRollback :exec
INSERT INTO alert_rollback_actions(fire_id,app_id,status,receipt)
 VALUES(sqlc.arg(fire_id),sqlc.narg(app_id)::uuid,sqlc.arg(status),sqlc.arg(receipt));

-- name: ReadAlertRollback :one
SELECT receipt FROM alert_rollback_actions WHERE fire_id=sqlc.arg(fire_id);

-- name: LockAlertRollback :one
SELECT receipt FROM alert_rollback_actions WHERE fire_id=sqlc.arg(fire_id) FOR UPDATE;

-- name: GetAlertRollback :one
SELECT r.receipt FROM alert_rollback_actions r JOIN apps a ON a.id=r.app_id
 WHERE r.fire_id=sqlc.arg(fire_id) AND a.id=sqlc.arg(app_id) AND a.account_id=sqlc.arg(account_id) AND r.receipt->>'account_id'=a.account_id::text AND a.status<>'deleted';

-- name: ListAlertRollbacks :many
SELECT r.receipt FROM alert_rollback_actions r JOIN apps a ON a.id=r.app_id
 WHERE a.id=sqlc.arg(app_id) AND a.account_id=sqlc.arg(account_id) AND r.receipt->>'account_id'=a.account_id::text AND a.status<>'deleted'
 ORDER BY r.updated_at DESC,r.fire_id LIMIT sqlc.arg(batch_size);

-- name: ListPendingAlertRollbacks :many
SELECT receipt FROM alert_rollback_actions WHERE status IN ('pending','blocked') ORDER BY updated_at,fire_id LIMIT sqlc.arg(batch_size);

-- name: SaveAlertRollback :exec
UPDATE alert_rollback_actions SET status=sqlc.arg(status),receipt=sqlc.arg(receipt),updated_at=clock_timestamp() WHERE fire_id=sqlc.arg(fire_id);

-- name: AppendRolloutRecoveryAudit :one
INSERT INTO deployment_audit(deployment_id,account_id,alert_rule_id,kind,actor,at,data)
 VALUES(sqlc.arg(deployment_id)::uuid,sqlc.narg(account_id)::uuid,sqlc.narg(alert_rule_id)::uuid,sqlc.arg(kind),sqlc.arg(actor),sqlc.arg(at),sqlc.arg(data)::jsonb) RETURNING id;

-- name: NotifyAlertRollbackTraffic :exec
SELECT pg_notify('deployment_changed',jsonb_build_object('kind','traffic','app_id',sqlc.arg(app_id)::text,'deployment_id',sqlc.arg(candidate_id)::text,'traffic_percent',0)::text);

-- name: LockAlertRollbackRule :one
SELECT id FROM alert_rules WHERE id=sqlc.arg(rule_id) FOR SHARE;

-- name: NotifyAlertServiceRollback :exec
SELECT pg_notify('deployment_changed',jsonb_build_object('kind','service_rollout_abort','app_id',sqlc.arg(app_id)::text,'deployment_id',sqlc.arg(candidate_id)::text,'status','live')::text);

-- name: ClaimHistoricalAlertRollback :execrows
INSERT INTO alert_historical_rollback_claims(deployment_id,fire_id)
 VALUES(sqlc.arg(deployment_id),sqlc.arg(fire_id)) ON CONFLICT(deployment_id) DO NOTHING;

-- name: InsertCustomerAlertRule :one
INSERT INTO alert_rules(account_id,app_id,name,enabled,metric,comparison,threshold,window_spec,failure_source,
 action,webhook_url,webhook_secret_sealed,cooldown_minutes,state,post_deploy_rollback_window_seconds)
VALUES(sqlc.arg(account_id),sqlc.narg(app_id),sqlc.arg(name),sqlc.arg(enabled),sqlc.arg(metric),sqlc.arg(comparison),
 sqlc.arg(threshold),sqlc.arg(window_spec),sqlc.narg(failure_source),sqlc.arg(action),sqlc.arg(webhook_url),
 sqlc.arg(webhook_secret_sealed),sqlc.arg(cooldown_minutes),sqlc.arg(state),sqlc.arg(post_deploy_rollback_window_seconds))
RETURNING *;

-- name: UpdateCustomerAlertRule :one
UPDATE alert_rules SET name=coalesce(sqlc.narg(name)::text,name),enabled=coalesce(sqlc.narg(enabled)::boolean,enabled),
 metric=coalesce(sqlc.narg(metric)::text,metric),comparison=coalesce(sqlc.narg(comparison)::text,comparison),
 threshold=coalesce(sqlc.narg(threshold)::double precision,threshold),window_spec=coalesce(sqlc.narg(window_spec)::text,window_spec),
 action=coalesce(sqlc.narg(action)::text,action),webhook_url=coalesce(sqlc.narg(webhook_url)::text,webhook_url),
 webhook_secret_sealed=coalesce(sqlc.narg(webhook_secret_sealed)::bytea,webhook_secret_sealed),
 cooldown_minutes=coalesce(sqlc.narg(cooldown_minutes)::integer,cooldown_minutes),
 post_deploy_rollback_window_seconds=coalesce(sqlc.narg(post_deploy_rollback_window_seconds)::integer,post_deploy_rollback_window_seconds),updated_at=now()
WHERE id=sqlc.arg(id) RETURNING *;

-- name: ReadCustomerAlertRule :one
SELECT * FROM alert_rules WHERE id=sqlc.arg(id);

-- name: ListCustomerAlertRulesForAccount :many
SELECT * FROM alert_rules WHERE account_id=sqlc.arg(account_id) ORDER BY created_at DESC;

-- name: ListEnabledCustomerAlertRules :many
SELECT * FROM alert_rules WHERE enabled=true ORDER BY account_id;

-- name: ListCustomerAlertRulesByPreset :many
SELECT r.* FROM alert_rules r WHERE r.account_id=sqlc.arg(account_id) AND r.app_id=sqlc.arg(app_id)
 AND r.name LIKE (SELECT p.display_name||' (%' FROM alert_presets p WHERE p.name=sqlc.arg(preset_name))
 ORDER BY r.created_at DESC LIMIT 2;

-- name: ReadHistoricalAlertDeploymentEvidence :one
-- Keep the app alert's 2xx/5xx denominator, weighted by publisher counts.
-- The cutover minute and unfinished ingestion minutes are excluded by callers.
SELECT coalesce(sum(count::bigint),0)::bigint AS requests,
 coalesce(sum(count::bigint) FILTER (WHERE status BETWEEN 500 AND 599),0)::bigint AS server_errors,
 max(received_at)::timestamptz AS last_sample_at
FROM request_telemetry
WHERE account_id=sqlc.arg(account_id)::uuid AND app_id=sqlc.arg(app_id)::uuid
 AND deployment_id=sqlc.arg(deployment_id)::uuid
 AND received_at>=sqlc.arg(window_start)::timestamptz AND received_at<sqlc.arg(window_end)::timestamptz
 AND (status BETWEEN 200 AND 299 OR status BETWEEN 500 AND 599);

-- name: ListSnapshotDeploymentIDs :many
SELECT DISTINCT deployment_id::text FROM snapshots ORDER BY deployment_id::text;

-- name: LockCustomerOperationBackendExecution :one
SELECT o.record,e.generation,e.execution_kind FROM customer_operations o
JOIN customer_operation_executions e ON e.operation_id=o.id
WHERE e.execution_id=sqlc.arg(execution_id)::uuid FOR UPDATE OF o;

-- name: SetCustomerOperationWorkflowIdentity :execrows
UPDATE workflow_runs w SET operation_id=o.id FROM customer_operations o
WHERE w.id=sqlc.arg(run_id)::uuid AND o.id=sqlc.arg(operation_id)::uuid AND w.app_id=o.app_id AND o.execution_kind='workflow'
AND w.platform_tenant_id=o.platform_tenant_id
AND (w.operation_id IS NULL OR w.operation_id=o.id);

-- name: InsertCustomerOperationWorkflowExecution :execrows
INSERT INTO customer_operation_executions(operation_id,generation,workflow_run_id)
SELECT o.id,sqlc.arg(generation)::integer,w.id FROM customer_operations o
JOIN workflow_runs w ON w.id=sqlc.arg(run_id)::uuid AND w.app_id=o.app_id AND w.operation_id=o.id
AND w.platform_tenant_id=o.platform_tenant_id
WHERE o.id=sqlc.arg(operation_id)::uuid AND o.execution_kind='workflow';

-- The same advisory key as CreateWorkflowRunAdmitted, taken before app locks.
-- name: LockNativeWorkflowAdmission :exec
SELECT pg_advisory_xact_lock(hashtextextended(sqlc.arg(app_id)::text,0));

-- name: CountActiveNativeWorkflowRuns :one
SELECT count(*)::bigint FROM workflow_runs WHERE app_id=sqlc.arg(app_id)::uuid
AND status IN ('pending','running','awaiting_event');

-- name: InsertCustomerOperationWorkflowRun :exec
INSERT INTO workflow_runs(id,app_id,platform_tenant_id,operation_id,workflow_name,status,input,definition_snapshot,scheduled_for,created_at,updated_at)
VALUES(sqlc.arg(id)::uuid,sqlc.arg(app_id)::uuid,sqlc.narg(platform_tenant_id)::uuid,sqlc.arg(operation_id)::uuid,sqlc.arg(workflow_name)::text,
 'pending',sqlc.arg(input)::jsonb,sqlc.arg(definition_snapshot)::jsonb,sqlc.arg(created_at)::timestamptz,
 sqlc.arg(created_at)::timestamptz,sqlc.arg(created_at)::timestamptz);

-- name: InsertCustomerOperationWorkflowStep :exec
INSERT INTO workflow_steps(run_id,step_name,status,attempt,input,created_at)
VALUES(sqlc.arg(run_id)::uuid,sqlc.arg(step_name)::text,'pending',0,sqlc.arg(input)::jsonb,sqlc.arg(created_at)::timestamptz);

-- name: GetNativeWorkflowRun :one
SELECT * FROM workflow_runs WHERE id=sqlc.arg(id)::uuid;

-- name: LockNativeWorkflowOwnership :one
SELECT coalesce(operation_id::text,'')::text AS operation_id FROM workflow_runs WHERE id=sqlc.arg(id)::uuid FOR UPDATE;

-- name: ClaimLegacyPendingWorkflowRun :one
UPDATE workflow_runs SET status='running',started_at=coalesce(started_at,now()),updated_at=now(),lease_until=now()+interval '5 minutes'
WHERE id=(SELECT id FROM workflow_runs WHERE operation_id IS NULL AND status='pending' AND scheduled_for<=now()
 ORDER BY scheduled_for,id FOR UPDATE SKIP LOCKED LIMIT 1) AND operation_id IS NULL RETURNING *;

-- name: NextDueLegacyWorkflowRun :one
SELECT id,status FROM workflow_runs WHERE operation_id IS NULL AND
 ((status IN ('pending','awaiting_event') AND scheduled_for<=now())
 OR (status='running' AND coalesce(lease_until,updated_at+(sqlc.arg(stale_ms)::bigint*interval '1 millisecond'))<=now()))
ORDER BY CASE WHEN status='running' THEN coalesce(lease_until,updated_at+(sqlc.arg(stale_ms)::bigint*interval '1 millisecond')) ELSE scheduled_for END,id
FOR UPDATE SKIP LOCKED LIMIT 1;

-- name: ClaimDueLegacyWorkflowRun :one
UPDATE workflow_runs SET status='running',started_at=coalesce(started_at,now()),updated_at=now(),lease_until=now()+interval '5 minutes'
WHERE id=sqlc.arg(id)::uuid AND operation_id IS NULL AND
 ((status IN ('pending','awaiting_event') AND scheduled_for<=now())
 OR (status='running' AND coalesce(lease_until,updated_at+(sqlc.arg(stale_ms)::bigint*interval '1 millisecond'))<=now()))
RETURNING *;

-- Recovery callers must lock an unmarked run and execute unknown-effect marking,
-- attempt closure and running-step reset in that order in one transaction.
-- name: MarkLegacyWorkflowOutboundUnknown :exec
UPDATE workflow_steps s SET status='dead', finished_at=now(),
 error='outbound result unknown; unsafe to repeat', outbound_attempt_token=NULL
FROM workflow_runs r
WHERE s.run_id=r.id AND r.id=sqlc.arg(run_id) AND r.operation_id IS NULL AND s.status='running'
AND workflow_step_definition(r.definition_snapshot,s.step_name,s.foreach_parent,s.foreach_index)#>>'{outbound,method}' NOT IN ('GET','HEAD')
 AND coalesce(workflow_step_definition(r.definition_snapshot,s.step_name,s.foreach_parent,s.foreach_index)#>>'{outbound,idempotency_supported}','false') <> 'true' ;

-- name: CloseLegacyWorkflowOutboundUnknownAttempts :exec
UPDATE workflow_step_attempts t SET status='failed',finished_at=now(),
error=CASE WHEN s.status='dead' THEN s.error
 WHEN s.foreach_parent IS NOT NULL AND jsonb_typeof(workflow_step_definition(r.definition_snapshot,s.step_name,s.foreach_parent,s.foreach_index)->'outbound') IS DISTINCT FROM 'object'
 THEN 'for_each item result unknown after recovery' ELSE 'outbound result unknown after recovery' END
FROM workflow_steps s, workflow_runs r
WHERE t.run_id=sqlc.arg(run_id) AND s.run_id=t.run_id AND r.id=s.run_id AND r.operation_id IS NULL
AND s.step_name=t.step_name AND s.attempt=t.attempt AND s.status IN ('running','dead') AND t.status='running'
AND (r.resume_count>0 OR s.foreach_parent IS NOT NULL OR jsonb_typeof(workflow_step_definition(r.definition_snapshot,s.step_name,s.foreach_parent,s.foreach_index)->'outbound')='object');

-- name: RecoverLegacyWorkflowSteps :exec
UPDATE workflow_steps s
SET status='pending',
attempt=CASE WHEN (s.foreach_parent IS NOT NULL OR jsonb_typeof(workflow_step_definition(r.definition_snapshot,s.step_name,s.foreach_parent,s.foreach_index)->'outbound')='object')
 THEN s.attempt WHEN r.resume_count>0 THEN s.attempt ELSE GREATEST(s.attempt-1,0) END,
finished_at=NULL,error=NULL,next_retry_at=NULL,outbound_attempt_token=NULL
FROM workflow_runs r WHERE s.run_id=r.id AND r.id=sqlc.arg(run_id) AND r.operation_id IS NULL AND s.status='running';

-- name: RecoverLegacyWorkflowRun :exec
UPDATE workflow_runs SET status='pending',scheduled_for=now(),updated_at=now()
WHERE id=sqlc.arg(id)::uuid AND operation_id IS NULL AND status NOT IN ('succeeded','failed','dead');

-- name: SweepUnboundNativeWorkflowRuns :execrows
DELETE FROM workflow_runs w WHERE w.finished_at<now()-(sqlc.arg(age_ms)::bigint*interval '1 millisecond')
AND NOT EXISTS(SELECT 1 FROM customer_operation_executions e WHERE e.workflow_run_id=w.id);

-- name: SetCustomerOperationJobIdentity :execrows
UPDATE job_runs j SET operation_id=o.id FROM customer_operations o
WHERE j.id=sqlc.arg(run_id)::uuid AND o.id=sqlc.arg(operation_id)::uuid AND j.account_id=o.account_id AND o.execution_kind='job'
AND (j.operation_id IS NULL OR j.operation_id=o.id);

-- name: InsertCustomerOperationJobExecution :execrows
INSERT INTO customer_operation_executions(operation_id,generation,job_run_id)
SELECT o.id,sqlc.arg(generation)::integer,j.id FROM customer_operations o
JOIN job_runs j ON j.id=sqlc.arg(run_id)::uuid AND j.account_id=o.account_id AND j.operation_id=o.id
WHERE o.id=sqlc.arg(operation_id)::uuid AND o.execution_kind='job';

-- name: ListRollbackOn5xxCandidates :many
-- ADR-625: completed live releases that opted into first-wake 5xx
-- auto-rollback and have not rolled back. A NULL window means apid has not
-- observed traffic for the release yet; an open window, plus a grace for
-- late telemetry, is still evaluated. Canary rollouts in flight belong to the
-- meterd circuit breaker, so only 100% complete releases qualify.
SELECT d.id, d.app_id, d.scope, d.created_at, d.first_wake_at, d.first_5xx_window_ends_at
  FROM deployments AS d
 WHERE d.status = 'live'
   AND d.rollback_on_5xx
   AND d.last_auto_rollback_reason IS NULL
   AND d.traffic_percent = 100
   AND d.rollout_state = 'complete'
   AND (d.first_5xx_window_ends_at IS NULL
        OR d.first_5xx_window_ends_at > now() - make_interval(secs => sqlc.arg('grace_seconds')::int))
 ORDER BY d.created_at, d.id
 LIMIT sqlc.arg('row_limit')::int;


-- name: RecordManagedPostgresCreationReceipt :one
INSERT INTO managed_postgres_creation_receipts(kind,resource_id,account_id,database_id,backend_id,backend_fingerprint,generation,
    point_in_time,source_resource_id,provider_resource_id,provider_created_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
ON CONFLICT (kind,backend_id,resource_id) DO UPDATE SET resource_id=EXCLUDED.resource_id
WHERE managed_postgres_creation_receipts.account_id=EXCLUDED.account_id
    AND managed_postgres_creation_receipts.database_id IS NOT DISTINCT FROM EXCLUDED.database_id
    AND managed_postgres_creation_receipts.backend_fingerprint=EXCLUDED.backend_fingerprint
    AND managed_postgres_creation_receipts.generation=EXCLUDED.generation
    AND managed_postgres_creation_receipts.point_in_time=EXCLUDED.point_in_time
    AND managed_postgres_creation_receipts.source_resource_id=EXCLUDED.source_resource_id
    AND managed_postgres_creation_receipts.provider_resource_id=EXCLUDED.provider_resource_id
    AND managed_postgres_creation_receipts.provider_created_at=EXCLUDED.provider_created_at RETURNING *;

-- name: ReadManagedPostgresCreationReceipt :one
SELECT * FROM managed_postgres_creation_receipts WHERE kind=$1 AND backend_id=$2 AND resource_id=$3;

-- name: ValidateManagedPostgresSnapshotCreationIntent :one
SELECT o.account_id FROM project_environment_clone_postgres_snapshots s JOIN project_environment_clone_operations o ON o.id=s.operation_id
WHERE ('environment-clone-' || s.operation_id::text || '-' || s.source_database_id::text)=sqlc.arg(resource_id)::text
    AND s.backend_id=sqlc.arg(backend_id)::text AND s.backend_fingerprint=sqlc.arg(backend_fingerprint)::text
    AND s.source_data_resource_id=sqlc.arg(source_resource_id)::text AND s.capture_point=sqlc.arg(point_in_time)::timestamptz
    AND s.request_started_at IS NOT NULL AND s.state IN ('requested','deleting') AND o.status IN ('capturing','compensating')
    AND (NOT sqlc.arg(cleanup)::boolean OR (s.state='deleting' AND o.status='compensating'));


-- name: MarkManagedPostgresCreationCleanup :one
UPDATE managed_postgres_creation_receipts SET cleanup_started_at=coalesce(cleanup_started_at,clock_timestamp())
WHERE kind=$1 AND backend_id=$2 AND resource_id=$3 AND provider_resource_id=$4 AND provider_created_at=$5 RETURNING *;

-- name: PurgeAccountManagedPostgresCreationReceipts :exec
DELETE FROM managed_postgres_creation_receipts c
WHERE c.account_id=sqlc.arg(account_id)::uuid
    AND EXISTS(SELECT 1 FROM accounts a WHERE a.id=c.account_id AND a.status='deleted_pending')
    AND ((c.kind='restore' AND EXISTS(SELECT 1 FROM managed_postgres_databases d
        WHERE d.id=c.database_id AND d.account_id=c.account_id AND d.state='deleted'))
    OR (c.kind='snapshot' AND EXISTS(SELECT 1 FROM project_environment_clone_postgres_snapshots s
        JOIN project_environment_clone_operations o ON o.id=s.operation_id
        WHERE o.account_id=c.account_id AND s.state='deleted'
            AND c.resource_id=('environment-clone-' || s.operation_id::text || '-' || s.source_database_id::text)
            AND c.backend_id=s.backend_id AND c.backend_fingerprint=s.backend_fingerprint
            AND c.source_resource_id=s.source_data_resource_id AND c.point_in_time=s.capture_point)));
