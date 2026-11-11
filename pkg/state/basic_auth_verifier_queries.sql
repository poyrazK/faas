-- ADR-20261110210000 slice 3h: verify-only basic auth, keyed by the SHA-256
-- of the sealed credential in apps.public_auth_basic.

-- name: PutAppBasicAuthVerifier :exec
-- A key names one sealed blob, so its verifier never changes: a repeat
-- write is a no-op.
INSERT INTO app_basic_auth_verifiers (sealed_sha256, verifier)
VALUES (sqlc.arg(sealed_sha256)::bytea, sqlc.arg(verifier)::text)
ON CONFLICT (sealed_sha256) DO NOTHING;

-- name: AppBasicAuthVerifier :one
SELECT verifier FROM app_basic_auth_verifiers
 WHERE sealed_sha256 = sqlc.arg(sealed_sha256)::bytea;
