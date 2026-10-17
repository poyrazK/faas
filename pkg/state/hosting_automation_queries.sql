-- name: HostingCredentialPruneExpired :exec
DELETE FROM hosting_automation_credentials WHERE site_id=$1 AND expires_at<=clock_timestamp();
-- name: HostingCredentialCount :one
SELECT count(*)::bigint FROM hosting_automation_credentials WHERE site_id=$1 AND oidc_trust_id IS NULL;
-- name: HostingCredentialCreate :one
INSERT INTO hosting_automation_credentials(id,account_id,site_id,token_hash,label,operations,created_at,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8) RETURNING *;
-- name: HostingCredentialList :many
SELECT * FROM hosting_automation_credentials WHERE account_id=$1 AND site_id=$2 AND oidc_trust_id IS NULL ORDER BY created_at,id;
-- name: HostingCredentialRevoke :execrows
DELETE FROM hosting_automation_credentials WHERE account_id=$1 AND site_id=$2 AND id=$3 AND oidc_trust_id IS NULL;
-- name: HostingCredentialAuthenticate :one
SELECT c.* FROM hosting_automation_credentials c JOIN hosting_sites s ON s.id=c.site_id
WHERE c.token_hash=$1 AND c.expires_at>clock_timestamp() AND s.account_id=c.account_id AND s.status='active' AND s.durable_routing;
-- name: HostingCIRunCount :one
SELECT count(*)::bigint FROM hosting_ci_runs WHERE site_id=$1;
-- name: HostingCIRunCreate :one
INSERT INTO hosting_ci_runs(site_id,id,release_id,preview_id,fingerprint,created_at,preview_expires_at,preview_reserved) VALUES($1,$2,$3,$4,$5,$6,$7,$8) RETURNING *;
-- name: HostingCIRunGet :one
SELECT r.* FROM hosting_ci_runs r JOIN hosting_sites s ON s.id=r.site_id WHERE s.account_id=$1 AND r.site_id=$2 AND r.id=$3;
-- name: HostingCIRunPreviewCreated :exec
UPDATE hosting_ci_runs SET preview_created=true WHERE site_id=$1 AND id=$2;

-- name: HostingActivityList :many
SELECT r.site_id,r.id,r.release_id,r.preview_id,r.preview_created,r.canceled,r.created_at,r.preview_expires_at,
 rel.id AS release_record_id,rel.state AS release_state,rel.provenance AS release_provenance,rel.first_promoted_at AS release_first_promoted_at,
 EXISTS(SELECT 1 FROM hosting_previews other WHERE other.site_id=r.site_id AND other.release_id=r.release_id AND other.id<>r.preview_id) AS other_previews,
 p.id AS preview_record_id,p.release_id AS preview_release_id,p.origin_id AS preview_origin_id,
 p.expires_at AS preview_expiry,p.revoked AS preview_revoked,p.generation AS preview_generation,
 p.published_generation AS preview_published_generation,p.published_at AS preview_published_at,
 p.next_attempt_at AS preview_next_attempt_at,p.last_error_code AS preview_last_error_code
FROM hosting_ci_runs r
JOIN hosting_sites s ON s.id=r.site_id
LEFT JOIN hosting_releases rel ON rel.id=r.release_id AND rel.site_id=r.site_id
LEFT JOIN hosting_previews p ON p.id=r.preview_id AND p.site_id=r.site_id AND p.account_id=s.account_id
WHERE s.account_id=sqlc.arg(account_id) AND r.site_id=sqlc.arg(site_id)
 AND (NOT sqlc.arg(has_cursor)::boolean OR (r.created_at,r.id)<(sqlc.arg(before_created_at)::timestamptz,sqlc.arg(before_id)::uuid))
ORDER BY r.created_at DESC,r.id DESC LIMIT sqlc.arg(page_size);

-- name: HostingPreviewCapacity :one
SELECT s.id,
 (SELECT count(*)::bigint FROM hosting_previews p WHERE p.site_id=s.id) AS previews,
 (SELECT count(*)::bigint FROM hosting_ci_runs r JOIN hosting_releases rel ON rel.id=r.release_id AND rel.site_id=r.site_id
  WHERE r.site_id=s.id AND r.preview_reserved AND NOT r.canceled AND NOT r.preview_created
   AND (NOT sqlc.arg(has_excluded_run)::boolean OR r.id<>sqlc.arg(exclude_run_id)::uuid) AND r.preview_expires_at>sqlc.arg(observed_at)::timestamptz
   AND rel.state IN ('pending','ready')) AS reservations
FROM hosting_sites s WHERE s.account_id=sqlc.arg(account_id) AND s.id=sqlc.arg(site_id);

-- name: HostingCIRunCancel :one
UPDATE hosting_ci_runs SET canceled=true WHERE site_id=$1 AND id=$2 RETURNING *;

-- name: HostingCICancelRetire :execrows
UPDATE hosting_releases SET state='retiring',retired_at=clock_timestamp(),retirement_generation=sqlc.arg(generation)::bigint
WHERE site_id=sqlc.arg(site_id)::uuid AND id=sqlc.arg(release_id)::uuid AND state IN ('pending','ready') AND first_promoted_at IS NULL;

-- name: HostingCICancelPreview :exec
UPDATE hosting_previews SET revoked=true,generation=2,next_attempt_at=clock_timestamp(),last_error_code=''
WHERE site_id=$1 AND id=$2 AND NOT revoked;
