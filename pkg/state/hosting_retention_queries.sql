-- name: HostingRetentionGet :one
SELECT * FROM hosting_retention_policies WHERE site_id=$1;
-- name: HostingRetentionPut :one
INSERT INTO hosting_retention_policies(site_id,revision,enabled,pending_age_seconds,staged_age_seconds)
VALUES($1,1,$2,$3,$4) ON CONFLICT(site_id) DO UPDATE SET revision=hosting_retention_policies.revision+1,
 enabled=EXCLUDED.enabled,pending_age_seconds=EXCLUDED.pending_age_seconds,staged_age_seconds=EXCLUDED.staged_age_seconds,updated_at=clock_timestamp() RETURNING *;
-- name: HostingRetentionSitePage :many
SELECT s.* FROM hosting_sites s JOIN hosting_retention_policies p ON p.site_id=s.id
WHERE p.enabled AND s.status='active' AND s.durable_routing AND (sqlc.arg(after_id)::text='' OR s.id>NULLIF(sqlc.arg(after_id)::text,'')::uuid)
ORDER BY s.id LIMIT sqlc.arg(page_size)::integer;
-- name: HostingRetentionCIExpiry :many
SELECT c.release_id,max(c.preview_expires_at)::timestamptz AS expires_at FROM hosting_ci_runs c
JOIN hosting_releases r ON r.id=c.release_id AND r.site_id=c.site_id
WHERE c.site_id=$1 GROUP BY c.release_id;
-- name: HostingReleaseTouch :one
UPDATE hosting_releases r SET last_activity_at=GREATEST(r.last_activity_at,clock_timestamp()) FROM hosting_sites s
WHERE s.id=r.site_id AND s.account_id=$1 AND r.site_id=$2 AND r.id=$3 AND s.status='active' AND r.state IN ('pending','ready') RETURNING r.*;
