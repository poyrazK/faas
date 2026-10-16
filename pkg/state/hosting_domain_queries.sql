-- name: HostingDomainReserve :exec
INSERT INTO hosting_domain_claims (hostname, domain_id) VALUES ($1, $2);

-- name: HostingDomainCreate :one
INSERT INTO hosting_domains (id, account_id, site_id, hostname, provider_origin_id, challenge, details)
VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING *;

-- name: HostingDomainCount :one
SELECT count(*) FROM hosting_domains WHERE site_id = $1;

-- name: HostingDomainList :many
SELECT d.* FROM hosting_domains d JOIN hosting_sites s ON s.id = d.site_id
WHERE s.account_id = $1 AND d.site_id = $2 ORDER BY d.created_at, d.id;

-- name: HostingDomainDelete :one
UPDATE hosting_domains SET state = 'deleting', revision = revision + 1,
generation = generation + 1, next_attempt_at = clock_timestamp(), last_error_code = ''
WHERE account_id = $1 AND site_id = $2 AND id = $3 AND revision = $4 AND state <> 'deleting'
RETURNING *;

-- name: HostingDomainDeleteSite :exec
UPDATE hosting_domains SET state = 'deleting', revision = revision + 1,
generation = generation + 1, next_attempt_at = clock_timestamp(), last_error_code = ''
WHERE site_id = $1 AND state <> 'deleting';

-- name: HostingDomainClaim :many
WITH due AS (
 SELECT h.id FROM hosting_domains h
 WHERE h.next_attempt_at <= sqlc.arg(now_at) AND (h.lease_until IS NULL OR h.lease_until <= sqlc.arg(now_at))
 ORDER BY h.next_attempt_at, h.id LIMIT sqlc.arg(page_size) FOR UPDATE SKIP LOCKED
)
UPDATE hosting_domains d SET lease_token = sqlc.arg(token), lease_until = sqlc.arg(until_at)
FROM due WHERE d.id = due.id RETURNING d.*;

-- name: HostingDomainSave :one
UPDATE hosting_domains SET state = sqlc.arg(state), details = sqlc.arg(details),
generation = generation + CASE WHEN state <> sqlc.arg(state) OR (details->>'access_aud') IS DISTINCT FROM (sqlc.arg(details)::jsonb->>'access_aud') THEN 1 ELSE 0 END
WHERE id = sqlc.arg(id) AND revision = sqlc.arg(revision) AND generation = sqlc.arg(generation)
AND lease_token = sqlc.arg(token) AND (state <> 'deleting' OR sqlc.arg(state) = 'deleting')
RETURNING *;

-- name: HostingDomainAck :execrows
UPDATE hosting_domains SET published_generation = generation,
published_at = CASE WHEN published_generation < generation THEN sqlc.arg(published_at)::timestamptz ELSE published_at END
WHERE id = sqlc.arg(id) AND generation = sqlc.arg(generation) AND lease_token = sqlc.arg(token);

-- name: HostingDomainFinish :execrows
UPDATE hosting_domains SET next_attempt_at = sqlc.arg(next_at), attempts = attempts + 1,
last_error_code = sqlc.arg(error_code), lease_token = NULL, lease_until = NULL
WHERE id = sqlc.arg(id) AND revision = sqlc.arg(revision) AND generation = sqlc.arg(generation) AND lease_token = sqlc.arg(token);

-- name: HostingDomainReleaseBatch :exec
UPDATE hosting_domains SET lease_token = NULL, lease_until = NULL WHERE lease_token = $1;

-- name: HostingDomainPurge :execrows
DELETE FROM hosting_domains WHERE id = sqlc.arg(id) AND lease_token = sqlc.arg(token)
AND state = 'deleting' AND generation = sqlc.arg(generation)
AND published_generation = generation AND published_at <= sqlc.arg(before_at);

-- name: HostingDomainRecover :one
UPDATE hosting_domains SET details = jsonb_set(details, '{provider_id}', to_jsonb(sqlc.arg(provider_id)::text)),
revision = revision + 1, next_attempt_at = clock_timestamp(), last_error_code = '',
lease_token = NULL, lease_until = NULL
WHERE account_id = sqlc.arg(account_id) AND site_id = sqlc.arg(site_id) AND id = sqlc.arg(id)
AND revision = sqlc.arg(revision) AND hostname = sqlc.arg(hostname)
AND COALESCE(details->>'provider_id', '') = '' AND details->>'verified_at' IS NOT NULL
AND (details->>'create_started_at')::timestamptz <= sqlc.arg(before_at)::timestamptz
RETURNING *;
