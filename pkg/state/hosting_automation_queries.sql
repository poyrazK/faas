-- name: HostingCredentialPruneExpired :exec
DELETE FROM hosting_automation_credentials WHERE site_id=$1 AND expires_at<=clock_timestamp();
-- name: HostingCredentialCount :one
SELECT count(*)::bigint FROM hosting_automation_credentials WHERE site_id=$1;
-- name: HostingCredentialCreate :one
INSERT INTO hosting_automation_credentials(id,account_id,site_id,token_hash,label,operations,created_at,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8) RETURNING *;
-- name: HostingCredentialList :many
SELECT * FROM hosting_automation_credentials WHERE account_id=$1 AND site_id=$2 ORDER BY created_at,id;
-- name: HostingCredentialRevoke :execrows
DELETE FROM hosting_automation_credentials WHERE account_id=$1 AND site_id=$2 AND id=$3;
-- name: HostingCredentialAuthenticate :one
SELECT c.* FROM hosting_automation_credentials c JOIN hosting_sites s ON s.id=c.site_id
WHERE c.token_hash=$1 AND c.expires_at>clock_timestamp() AND s.account_id=c.account_id AND s.status='active' AND s.durable_routing;
-- name: HostingCIRunCount :one
SELECT count(*)::bigint FROM hosting_ci_runs WHERE site_id=$1;
-- name: HostingCIRunCreate :one
INSERT INTO hosting_ci_runs(site_id,id,release_id,preview_id,fingerprint,created_at,preview_expires_at) VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING *;
-- name: HostingCIRunGet :one
SELECT r.* FROM hosting_ci_runs r JOIN hosting_sites s ON s.id=r.site_id WHERE s.account_id=$1 AND r.site_id=$2 AND r.id=$3;
-- name: HostingCIRunPreviewCreated :exec
UPDATE hosting_ci_runs SET preview_created=true WHERE site_id=$1 AND id=$2;
