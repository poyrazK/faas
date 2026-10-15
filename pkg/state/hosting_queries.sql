-- name: HostingAccountLock :one
SELECT id FROM accounts WHERE id = sqlc.arg(account_id)::uuid FOR UPDATE;

-- name: HostingProjectEnvironmentOwned :one
SELECT EXISTS(SELECT 1 FROM project_environments
 WHERE account_id = sqlc.arg(account_id)::uuid AND project_id = sqlc.arg(project_id)::uuid
 AND slug = sqlc.arg(environment)::text)::boolean;

-- name: HostingSiteCount :one
SELECT count(*)::bigint FROM hosting_sites WHERE account_id = sqlc.arg(account_id)::uuid;

-- name: HostingSiteCreate :one
INSERT INTO hosting_sites(id, account_id, project_id, environment, name, hostname, origin_id)
VALUES(sqlc.arg(id)::uuid, sqlc.arg(account_id)::uuid, sqlc.arg(project_id)::uuid,
 sqlc.arg(environment)::text, sqlc.arg(name)::text, sqlc.arg(hostname)::text, sqlc.arg(origin_id)::text)
RETURNING *;

-- name: HostingSiteGet :one
SELECT * FROM hosting_sites WHERE account_id = sqlc.arg(account_id)::uuid AND id = sqlc.arg(id)::uuid;

-- name: HostingSiteLock :one
SELECT * FROM hosting_sites WHERE account_id = sqlc.arg(account_id)::uuid AND id = sqlc.arg(id)::uuid FOR UPDATE;

-- name: HostingSiteList :many
SELECT * FROM hosting_sites WHERE account_id = sqlc.arg(account_id)::uuid ORDER BY id;

-- name: HostingRoutePage :many
SELECT * FROM hosting_sites WHERE (sqlc.arg(after_id)::text = '' OR id > NULLIF(sqlc.arg(after_id)::text, '')::uuid)
ORDER BY id LIMIT sqlc.arg(page_size)::integer;

-- name: HostingReleaseCount :one
SELECT count(*)::bigint FROM hosting_releases WHERE site_id = sqlc.arg(site_id)::uuid AND state <> 'deleted';

-- name: HostingReleaseCreate :one
INSERT INTO hosting_releases(id, site_id, manifest)
VALUES(sqlc.arg(id)::uuid, sqlc.arg(site_id)::uuid, sqlc.arg(manifest)::jsonb) RETURNING *;

-- name: HostingReleaseGet :one
SELECT r.* FROM hosting_releases r JOIN hosting_sites s ON s.id = r.site_id
WHERE s.account_id = sqlc.arg(account_id)::uuid AND r.site_id = sqlc.arg(site_id)::uuid AND r.id = sqlc.arg(id)::uuid;

-- name: HostingReleaseList :many
SELECT r.* FROM hosting_releases r JOIN hosting_sites s ON s.id = r.site_id
WHERE s.account_id = sqlc.arg(account_id)::uuid AND r.site_id = sqlc.arg(site_id)::uuid AND r.state <> 'deleted'
ORDER BY r.created_at DESC, r.id DESC;

-- name: HostingReleaseReady :one
UPDATE hosting_releases r SET state = 'ready' FROM hosting_sites s
WHERE s.id = r.site_id AND s.account_id = sqlc.arg(account_id)::uuid
AND r.site_id = sqlc.arg(site_id)::uuid AND r.id = sqlc.arg(id)::uuid AND s.status = 'active' AND r.state IN ('pending', 'ready') RETURNING r.*;

-- name: HostingReleasePromote :one
UPDATE hosting_sites SET active_release_id = sqlc.arg(release_id)::uuid, revision = revision + 1
WHERE account_id = sqlc.arg(account_id)::uuid AND id = sqlc.arg(site_id)::uuid
AND status = 'active' AND revision = sqlc.arg(expected_revision)::bigint
AND EXISTS(SELECT 1 FROM hosting_releases r WHERE r.site_id = hosting_sites.id AND r.id = sqlc.arg(release_id)::uuid AND r.state = 'ready')
RETURNING *;

-- name: HostingReleaseRetire :one
UPDATE hosting_releases r SET state = 'retiring', retired_at = clock_timestamp()
FROM hosting_sites s WHERE s.id = r.site_id AND s.account_id = sqlc.arg(account_id)::uuid
AND r.site_id = sqlc.arg(site_id)::uuid AND r.id = sqlc.arg(id)::uuid AND r.state IN ('pending', 'ready')
AND s.active_release_id IS DISTINCT FROM r.id RETURNING r.*;

-- name: HostingReleaseDeleted :exec
DELETE FROM hosting_releases r USING hosting_sites s
WHERE s.id = r.site_id AND s.account_id = sqlc.arg(account_id)::uuid
AND r.site_id = sqlc.arg(site_id)::uuid AND r.id = sqlc.arg(id)::uuid AND r.state = 'retiring';

-- name: HostingSiteDelete :one
UPDATE hosting_sites SET status = 'deleting', active_release_id = NULL, revision = revision + 1
WHERE account_id = sqlc.arg(account_id)::uuid AND id = sqlc.arg(id)::uuid AND status = 'active' RETURNING *;

-- name: HostingSiteRetireReleases :exec
UPDATE hosting_releases SET state = 'retiring', retired_at = clock_timestamp()
WHERE site_id = sqlc.arg(site_id)::uuid AND state IN ('pending', 'ready');

-- name: HostingSitePurge :exec
DELETE FROM hosting_sites s WHERE s.account_id = sqlc.arg(account_id)::uuid AND s.id = sqlc.arg(id)::uuid AND s.status = 'deleting'
AND NOT EXISTS(SELECT 1 FROM hosting_releases r WHERE r.site_id = s.id AND r.state <> 'deleted');
