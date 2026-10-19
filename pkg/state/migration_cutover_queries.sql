-- name: CreateMigrationCutover :exec
INSERT INTO migration_cutovers(id,account_id,app_id,created_at,version,snapshot,next_attempt_at)
VALUES($1,$2,$3,$4,$5,$6,$7);
-- name: GetMigrationCutover :one
SELECT snapshot,version FROM migration_cutovers WHERE account_id=$1 AND app_id=$2 AND id=$3;
-- name: ListMigrationCutovers :many
SELECT snapshot,version FROM migration_cutovers WHERE account_id=$1 AND app_id=$2 ORDER BY created_at DESC,id DESC LIMIT $3;
-- name: SaveMigrationCutover :execrows
UPDATE migration_cutovers SET snapshot=$4,version=version+1,next_attempt_at=$6 WHERE account_id=$1 AND app_id=$2 AND id=$3 AND version=$5;
-- name: PruneMigrationCutovers :exec
DELETE FROM migration_cutovers WHERE app_id=$1 AND created_at<$2 AND snapshot->'Report'->>'status' IN ('completed','cancelled','restored','blocked');
-- name: CountMigrationCutovers :one
SELECT count(*) FROM migration_cutovers WHERE app_id=$1;
-- name: ListDueMigrationCutovers :many
SELECT snapshot,version FROM migration_cutovers WHERE next_attempt_at<=$1 ORDER BY next_attempt_at,id LIMIT $2;
