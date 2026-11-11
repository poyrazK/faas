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

-- name: TriggerConfig :one
-- A trigger of the named app only (ADR-20261110210000 slice 3g).
SELECT kind, config FROM triggers
 WHERE id = sqlc.arg(id)::uuid AND app_id = sqlc.arg(app_id)::uuid;
