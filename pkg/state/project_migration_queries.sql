-- name: LockProjectMigrationAccount :one
SELECT id FROM accounts WHERE id=$1 FOR UPDATE;
-- name: CreateProjectMigration :exec
INSERT INTO project_migrations(id,account_id,project_slug,created_at,version,lease_until,snapshot) VALUES($1,$2,$3,$4,1,$5,$6);
-- name: GetProjectMigration :one
SELECT snapshot,version FROM project_migrations WHERE account_id=$1 AND id=$2;
-- name: ListProjectMigrations :many
SELECT snapshot,version FROM project_migrations WHERE account_id=$1 AND ($2::text='' OR project_slug=$2) ORDER BY created_at DESC,id DESC LIMIT $3;
-- name: CountProjectMigrations :one
SELECT count(*) FROM project_migrations WHERE account_id=$1;
-- name: PruneProjectMigrations :exec
DELETE FROM project_migrations WHERE account_id=$1 AND created_at<$2 AND (lease_until IS NULL OR lease_until<now());
-- name: ActiveProjectMigrationLease :one
SELECT id FROM project_migrations WHERE account_id=$1 AND project_slug=$2 AND id<>$3 AND lease_until>now() LIMIT 1;
-- name: SaveProjectMigration :execrows
UPDATE project_migrations SET snapshot=$3,version=version+1,lease_until=$5 WHERE account_id=$1 AND id=$2 AND version=$4;
