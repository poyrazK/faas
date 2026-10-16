-- name: HostingAccountLock :one
SELECT id FROM accounts WHERE id = sqlc.arg(account_id)::uuid FOR UPDATE;

-- name: HostingProjectEnvironmentOwned :one
SELECT EXISTS(SELECT 1 FROM project_environments
 WHERE account_id = sqlc.arg(account_id)::uuid AND project_id = sqlc.arg(project_id)::uuid
 AND slug = sqlc.arg(environment)::text)::boolean;

-- name: HostingSiteCount :one
SELECT count(*)::bigint FROM hosting_sites WHERE account_id = sqlc.arg(account_id)::uuid;

-- name: HostingSiteCreate :one
INSERT INTO hosting_sites(id, account_id, project_id, environment, name, hostname, origin_id, durable_routing)
VALUES(sqlc.arg(id)::uuid, sqlc.arg(account_id)::uuid, sqlc.arg(project_id)::uuid,
 sqlc.arg(environment)::text, sqlc.arg(name)::text, sqlc.arg(hostname)::text, sqlc.arg(origin_id)::text, sqlc.arg(durable_routing)::boolean)
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
INSERT INTO hosting_releases(id, site_id, manifest, artifact_bytes)
VALUES(sqlc.arg(id)::uuid, sqlc.arg(site_id)::uuid, sqlc.arg(manifest)::jsonb, sqlc.arg(artifact_bytes)::bigint) RETURNING *;

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
UPDATE hosting_releases r SET state = 'retiring', retired_at = clock_timestamp(), retirement_generation = sqlc.arg(generation)::bigint
FROM hosting_sites s WHERE s.id = r.site_id AND s.account_id = sqlc.arg(account_id)::uuid
AND r.site_id = sqlc.arg(site_id)::uuid AND r.id = sqlc.arg(id)::uuid AND r.state IN ('pending', 'ready')
AND s.active_release_id IS DISTINCT FROM r.id
AND NOT EXISTS(SELECT 1 FROM hosting_previews p WHERE p.release_id=r.id) RETURNING r.*;

-- name: HostingReleaseDeleted :execrows
DELETE FROM hosting_releases r USING hosting_sites s
WHERE s.id = r.site_id AND s.account_id = sqlc.arg(account_id)::uuid
AND r.site_id = sqlc.arg(site_id)::uuid AND r.id = sqlc.arg(id)::uuid AND r.state = 'retiring'
AND NOT EXISTS(SELECT 1 FROM hosting_previews p WHERE p.release_id=r.id)
AND (NOT s.durable_routing OR r.retirement_published_at <= sqlc.arg(safe_before)::timestamptz);

-- name: HostingSiteDelete :one
UPDATE hosting_sites SET status = 'deleting', active_release_id = NULL, revision = revision + 1
WHERE account_id = sqlc.arg(account_id)::uuid AND id = sqlc.arg(id)::uuid AND status = 'active' RETURNING *;

-- name: HostingSiteRetireReleases :exec
UPDATE hosting_releases SET state = 'retiring', retired_at = clock_timestamp(), retirement_generation = sqlc.arg(generation)::bigint
WHERE site_id = sqlc.arg(site_id)::uuid AND state IN ('pending', 'ready');

-- name: HostingSitePurge :execrows
DELETE FROM hosting_sites s WHERE s.account_id = sqlc.arg(account_id)::uuid AND s.id = sqlc.arg(id)::uuid AND s.status = 'deleting'
AND NOT EXISTS(SELECT 1 FROM hosting_releases r WHERE r.site_id = s.id AND r.state <> 'deleted')
AND NOT EXISTS(SELECT 1 FROM hosting_domains d WHERE d.site_id = s.id)
AND NOT EXISTS(SELECT 1 FROM hosting_previews p WHERE p.site_id = s.id)
AND (NOT s.durable_routing OR EXISTS(SELECT 1 FROM hosting_route_publications p WHERE p.site_id = s.id
 AND p.published_generation = p.generation AND p.published_at <= sqlc.arg(safe_before)::timestamptz));

-- name: HostingSiteSuspend :one
UPDATE hosting_sites SET suspended = sqlc.arg(suspended)::boolean, revision = revision + 1
WHERE account_id = sqlc.arg(account_id)::uuid AND id = sqlc.arg(site_id)::uuid
AND status = 'active' AND durable_routing AND revision = sqlc.arg(expected_revision)::bigint RETURNING *;

-- name: HostingRouteChanged :one
INSERT INTO hosting_route_publications(site_id)
SELECT id FROM hosting_sites WHERE id = sqlc.arg(site_id)::uuid AND durable_routing
ON CONFLICT (site_id) DO UPDATE SET generation = hosting_route_publications.generation + 1,
 next_attempt_at = clock_timestamp(), pending_since = COALESCE(hosting_route_publications.pending_since, clock_timestamp()),
 last_attempt_at = NULL, retry_count = 0, last_error_code = '' RETURNING generation;

-- name: HostingRouteDue :many
SELECT site_id FROM hosting_route_publications
WHERE next_attempt_at <= sqlc.arg(now_at)::timestamptz ORDER BY next_attempt_at, site_id LIMIT sqlc.arg(page_size)::integer;

-- name: HostingRoutePublicationGet :one
SELECT * FROM hosting_route_publications WHERE site_id = sqlc.arg(site_id)::uuid;

-- name: HostingRouteSiteGet :one
SELECT * FROM hosting_sites WHERE id = sqlc.arg(site_id)::uuid AND durable_routing;

-- name: HostingRoutingReleaseList :many
SELECT id, site_id, state, first_promoted_at FROM hosting_releases WHERE site_id = sqlc.arg(site_id)::uuid AND state <> 'deleted' ORDER BY id;

-- name: HostingRouteSiteLock :one
SELECT * FROM hosting_sites WHERE id = sqlc.arg(site_id)::uuid AND durable_routing FOR UPDATE;

-- name: HostingRoutePublicationLock :one
SELECT * FROM hosting_route_publications WHERE site_id = sqlc.arg(site_id)::uuid FOR UPDATE;

-- name: HostingRouteAcknowledged :exec
UPDATE hosting_route_publications SET
 published_at = CASE WHEN published_generation < sqlc.arg(generation)::bigint THEN sqlc.arg(published_at)::timestamptz ELSE published_at END,
 published_generation = GREATEST(published_generation, sqlc.arg(generation)::bigint),
 next_attempt_at = CASE WHEN generation = sqlc.arg(generation)::bigint THEN sqlc.arg(next_attempt_at)::timestamptz ELSE next_attempt_at END,
 pending_since = CASE WHEN generation = sqlc.arg(generation)::bigint THEN NULL ELSE pending_since END,
 last_attempt_at = CASE WHEN generation = sqlc.arg(generation)::bigint THEN sqlc.arg(published_at)::timestamptz ELSE last_attempt_at END,
 retry_count = CASE WHEN generation = sqlc.arg(generation)::bigint THEN 0 ELSE retry_count END,
 last_error_code = CASE WHEN generation = sqlc.arg(generation)::bigint THEN '' ELSE last_error_code END
WHERE site_id = sqlc.arg(site_id)::uuid AND published_generation <= sqlc.arg(generation)::bigint;

-- name: HostingRetirementAcknowledged :exec
UPDATE hosting_releases SET retirement_published_at = sqlc.arg(published_at)::timestamptz
WHERE site_id = sqlc.arg(site_id)::uuid AND state = 'retiring' AND retirement_generation > 0
AND retirement_generation <= sqlc.arg(generation)::bigint AND retirement_published_at IS NULL;

-- name: HostingRouteRetry :exec
UPDATE hosting_route_publications SET next_attempt_at = sqlc.arg(next_attempt_at)::timestamptz,
 last_attempt_at = sqlc.arg(attempted_at)::timestamptz, retry_count = retry_count + 1, last_error_code = sqlc.arg(error_code)::text
WHERE site_id = sqlc.arg(site_id)::uuid AND generation = sqlc.arg(generation)::bigint;

-- name: HostingRouteBacklog :one
SELECT count(*)::bigint AS pending, min(pending_since)::timestamptz AS oldest_pending_at
FROM hosting_route_publications WHERE generation > published_generation;

-- name: HostingAccountUsage :one
SELECT COALESCE(sum(r.artifact_bytes), 0)::bigint AS payload_bytes,
 COALESCE(sum(r.artifact_bytes) FILTER (WHERE r.state = 'ready'), 0)::bigint AS ready_payload_bytes,
 count(*)::bigint AS releases,
 count(*) FILTER (WHERE r.state = 'pending')::bigint AS pending_uploads,
 COALESCE(sum(sqlc.arg(copies)::bigint * r.artifact_bytes + sqlc.arg(manifest_reservation)::bigint), 0)::bigint AS reserved_bytes,
 COALESCE(sum(sqlc.arg(copies)::bigint * r.artifact_bytes + sqlc.arg(manifest_reservation)::bigint) FILTER (WHERE r.state = 'retiring'), 0)::bigint AS retiring_reserved_bytes
FROM hosting_releases r JOIN hosting_sites s ON s.id = r.site_id
WHERE s.account_id = sqlc.arg(account_id)::uuid AND r.state <> 'deleted';

-- name: HostingReleaseMarkPromoted :execrows
UPDATE hosting_releases SET first_promoted_at = COALESCE(first_promoted_at, clock_timestamp())
WHERE site_id = sqlc.arg(site_id)::uuid AND id = sqlc.arg(id)::uuid AND state = 'ready';
