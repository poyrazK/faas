-- name: HostingPreviewCount :one
SELECT count(*)::bigint FROM hosting_previews WHERE site_id = $1;

-- name: HostingPreviewCreate :one
INSERT INTO hosting_previews(id,account_id,site_id,release_id,hostname,origin_id,expires_at,created_at)
VALUES($1,$2,$3,$4,$5,$6,$7,$8) RETURNING *;

-- name: HostingPreviewList :many
SELECT * FROM hosting_previews WHERE account_id = $1 AND site_id = $2 ORDER BY id;

-- name: HostingPreviewRevoke :one
UPDATE hosting_previews SET revoked=true,generation=2,next_attempt_at=clock_timestamp(),last_error_code=''
WHERE account_id=$1 AND site_id=$2 AND id=$3 AND generation=$4 AND NOT revoked RETURNING *;

-- name: HostingPreviewDue :many
SELECT id FROM hosting_previews WHERE next_attempt_at <= sqlc.arg(now_at)::timestamptz ORDER BY next_attempt_at,id LIMIT sqlc.arg(page_size)::integer;

-- name: HostingPreviewPrepare :one
UPDATE hosting_previews p SET
 revoked=p.revoked OR p.expires_at <= sqlc.arg(now_at)::timestamptz OR s.status <> 'active',
 generation=CASE WHEN p.revoked OR p.expires_at <= sqlc.arg(now_at)::timestamptz OR s.status <> 'active' THEN 2 ELSE 1 END
FROM hosting_sites s WHERE p.id=sqlc.arg(id)::uuid AND s.id=p.site_id RETURNING p.*;

-- name: HostingPreviewFinish :execrows
UPDATE hosting_previews SET
 published_at=CASE WHEN sqlc.arg(error_code)::text='' AND published_generation < generation THEN sqlc.arg(now_at)::timestamptz ELSE published_at END,
 published_generation=CASE WHEN sqlc.arg(error_code)::text='' THEN generation ELSE published_generation END,
 next_attempt_at=sqlc.arg(next_at)::timestamptz,last_error_code=sqlc.arg(error_code)::text
WHERE id=sqlc.arg(id)::uuid AND generation=sqlc.arg(generation)::bigint;

-- name: HostingPreviewPurge :execrows
DELETE FROM hosting_previews WHERE id=sqlc.arg(id)::uuid AND generation=sqlc.arg(generation)::bigint AND revoked
AND published_generation=generation AND published_at <= sqlc.arg(before_at)::timestamptz;

-- name: HostingPreviewDeleteSite :exec
UPDATE hosting_previews SET revoked=true,generation=2,next_attempt_at=clock_timestamp(),last_error_code='' WHERE site_id=$1 AND NOT revoked;
