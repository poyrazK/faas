-- name: CreateMigrationVerification :exec
INSERT INTO migration_verifications(id,account_id,app_id,created_at,version,snapshot,next_attempt_at)
VALUES($1,$2,$3,$4,$5,$6,$7);
-- name: GetMigrationVerification :one
SELECT snapshot,version FROM migration_verifications WHERE account_id=$1 AND app_id=$2 AND id=$3;
-- name: LockMigrationVerification :one
SELECT snapshot,version FROM migration_verifications WHERE account_id=$1 AND app_id=$2 AND id=$3 FOR UPDATE;
-- name: ListMigrationVerifications :many
SELECT snapshot,version FROM migration_verifications WHERE account_id=$1 AND app_id=$2 ORDER BY created_at DESC,id DESC LIMIT $3;
-- name: SaveMigrationVerification :execrows
UPDATE migration_verifications SET snapshot=$4,version=version+1,next_attempt_at=$6 WHERE account_id=$1 AND app_id=$2 AND id=$3 AND version=$5;
-- name: PruneMigrationVerifications :exec
DELETE FROM migration_verifications WHERE app_id=$1 AND created_at<$2 AND snapshot->'Report'->>'status'<>'pending';
-- name: LockMigrationVerificationApp :one
SELECT id FROM apps WHERE account_id=$1 AND id=$2 AND status<>'deleted' FOR UPDATE;
-- name: CountActiveMigrationVerifications :one
SELECT count(*) FROM migration_verifications WHERE app_id=$1 AND snapshot->'Report'->>'status'='pending';
-- name: ListDueMigrationVerifications :many
SELECT snapshot,version FROM migration_verifications WHERE next_attempt_at<=$1 ORDER BY next_attempt_at,id LIMIT $2;
