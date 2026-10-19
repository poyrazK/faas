-- name: ClaimRegressionActionReceipt :exec
INSERT INTO idempotency_keys (account_id, key, response_status, response_body, request_digest)
VALUES (sqlc.arg(account_id)::uuid, sqlc.arg(key)::text, 0, ''::bytea, sqlc.arg(request_digest)::bytea)
ON CONFLICT (account_id, key) DO NOTHING;

-- name: LockRegressionActionReceipt :one
SELECT response_status, response_body, request_digest
FROM idempotency_keys
WHERE account_id = sqlc.arg(account_id)::uuid AND key = sqlc.arg(key)::text
FOR UPDATE;

-- name: SaveRegressionActionReceipt :execrows
UPDATE idempotency_keys
SET response_status = 200, response_body = sqlc.arg(response_body)::bytea, created_at = now()
WHERE account_id = sqlc.arg(account_id)::uuid AND key = sqlc.arg(key)::text
  AND request_digest = sqlc.arg(request_digest)::bytea AND response_status = 0;
