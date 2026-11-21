-- name: ExecutionAdmissionGet :one
SELECT request_digest, execution_id FROM execution_admissions
WHERE account_id = sqlc.arg(account_id) AND scope_hash = sqlc.arg(scope_hash)
  AND key_hash = sqlc.arg(key_hash) AND expires_at > clock_timestamp();

-- name: ExecutionAdmissionDeleteExpiredKey :exec
DELETE FROM execution_admissions
WHERE account_id = sqlc.arg(account_id) AND scope_hash = sqlc.arg(scope_hash)
  AND key_hash = sqlc.arg(key_hash) AND expires_at <= clock_timestamp();

-- name: ExecutionAdmissionInsert :exec
INSERT INTO execution_admissions
    (account_id, scope_hash, key_hash, request_digest, execution_id, created_at, expires_at)
VALUES (sqlc.arg(account_id), sqlc.arg(scope_hash), sqlc.arg(key_hash), sqlc.arg(request_digest),
    sqlc.arg(execution_id), clock_timestamp(), clock_timestamp() + make_interval(secs => sqlc.arg(retention_seconds)::double precision));

-- name: ExecutionAdmissionLegacyExists :one
SELECT EXISTS (SELECT 1 FROM idempotency_keys
WHERE account_id = sqlc.arg(account_id) AND key = sqlc.arg(legacy_key)
  AND created_at > clock_timestamp() - make_interval(secs => sqlc.arg(retention_seconds)::double precision));

-- name: ExecutionAdmissionSweep :exec
DELETE FROM execution_admissions WHERE (account_id, scope_hash, key_hash) IN (
    SELECT expired.account_id, expired.scope_hash, expired.key_hash FROM execution_admissions expired
    WHERE expired.expires_at <= sqlc.arg(sweep_at) ORDER BY expired.expires_at
    LIMIT sqlc.arg(batch_limit) FOR UPDATE SKIP LOCKED
);

-- name: ExecutionAdmissionArtifactGrant :one
SELECT * FROM execution_artifact_grants
WHERE account_id = sqlc.arg(account_id) AND token_hash = sqlc.arg(token_hash)
  AND revoked_at IS NULL AND redeemed_at IS NULL AND expires_at > sqlc.arg(at);
