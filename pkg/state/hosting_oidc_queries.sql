-- name: HostingOIDCTrustCount :one
SELECT count(*)::bigint FROM hosting_oidc_trusts WHERE site_id=$1;
-- name: HostingOIDCTrustCreate :one
INSERT INTO hosting_oidc_trusts(id,account_id,site_id,repository_owner_id,repository_id,repository,ref,workflow_ref,event_name,environment,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) RETURNING *;
-- name: HostingOIDCTrustList :many
SELECT * FROM hosting_oidc_trusts WHERE account_id=$1 AND site_id=$2 ORDER BY created_at,id;
-- name: HostingOIDCTrustGet :one
SELECT * FROM hosting_oidc_trusts WHERE site_id=$1 AND id=$2;
-- name: HostingOIDCTrustLock :one
SELECT * FROM hosting_oidc_trusts WHERE site_id=$1 AND id=$2 FOR UPDATE;
-- name: HostingOIDCTrustRevoke :execrows
DELETE FROM hosting_oidc_trusts WHERE account_id=$1 AND site_id=$2 AND id=$3;
-- name: HostingOIDCSessionCount :one
SELECT count(*)::bigint FROM hosting_automation_credentials WHERE site_id=$1 AND oidc_trust_id IS NOT NULL;
-- name: HostingOIDCSessionCreate :one
INSERT INTO hosting_automation_credentials(id,account_id,site_id,token_hash,label,operations,created_at,expires_at,oidc_trust_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING *;
