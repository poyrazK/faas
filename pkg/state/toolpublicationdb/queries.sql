-- name: InsertToolPublication :execrows
INSERT INTO tool_publications (account_id, id, status, payload)
VALUES ($1, $2, 'running', $3)
ON CONFLICT (account_id, id) DO NOTHING;

-- name: LockToolPublicationAccount :one
SELECT id FROM accounts WHERE id = $1 FOR UPDATE;

-- name: CountToolPublications :one
SELECT count(*) FROM tool_publications WHERE account_id = $1;

-- name: GetToolPublication :one
SELECT payload FROM tool_publications WHERE account_id = $1 AND id = $2;

-- name: LockToolPublication :one
SELECT payload FROM tool_publications WHERE account_id = $1 AND id = $2 FOR UPDATE;

-- name: FinishToolPublication :exec
UPDATE tool_publications SET status = $3, payload = $4 WHERE account_id = $1 AND id = $2;

-- name: SearchToolRegistry :many
SELECT payload FROM tool_publications
WHERE account_id = $1 AND status = 'completed' AND id > $2
AND (strpos(lower(payload->'intent'->>'app'), lower(sqlc.arg(query)::text)) > 0
     OR EXISTS (SELECT 1 FROM jsonb_object_keys(payload->'intent'->'tool_scopes') AS tool(name)
                WHERE strpos(lower(tool.name), lower(sqlc.arg(query)::text)) > 0))
ORDER BY id LIMIT $3;

-- name: InsertToolInvocation :exec
INSERT INTO tool_invocations (account_id,id,status,deadline,payload) VALUES ($1,$2,'queued',$3,$4);

-- name: GetToolInvocation :one
SELECT payload FROM tool_invocations WHERE account_id=$1 AND id=$2;

-- name: LockToolInvocation :one
SELECT payload FROM tool_invocations WHERE account_id=$1 AND id=$2 FOR UPDATE;

-- name: CountToolInvocations :one
SELECT count(*) AS total, count(*) FILTER (WHERE status IN ('queued','running') AND deadline>now()) AS active
FROM tool_invocations WHERE account_id=$1;

-- name: ChangeToolInvocation :exec
UPDATE tool_invocations SET status=$3,payload=$4 WHERE account_id=$1 AND id=$2;

-- name: GetToolGrant :one
SELECT payload FROM tool_grants WHERE account_id=$1 AND id=$2;

-- name: CountToolGrants :one
SELECT count(*) AS total, count(*) FILTER (WHERE key_id=$2) AS per_key FROM tool_grants WHERE account_id=$1;

-- name: InsertToolGrant :execrows
INSERT INTO tool_grants (account_id,id,key_id,version,expires_at,payload)
SELECT sqlc.arg(account_id), sqlc.arg(id), sqlc.arg(key_id), sqlc.arg(version), sqlc.arg(expires_at), sqlc.arg(payload)
FROM api_keys WHERE api_keys.id=sqlc.arg(key_id) AND api_keys.account_id=sqlc.arg(account_id)
AND api_keys.status IN ('active','grace') AND (api_keys.expires_at IS NULL OR api_keys.expires_at>now());

-- name: ListToolGrants :many
SELECT payload FROM tool_grants WHERE account_id=$1 AND id>sqlc.arg(cursor)::uuid ORDER BY id LIMIT $2;

-- name: RevokeToolGrant :one
UPDATE tool_grants SET revoked_at=COALESCE(revoked_at,now()),
payload=jsonb_set(payload,'{revoked_at}',to_jsonb(COALESCE(revoked_at,now())))
WHERE account_id=$1 AND id=$2 RETURNING payload;

-- name: ToolGrantEntries :many
SELECT g.payload AS grant_payload,p.payload AS publication_payload
FROM tool_grants g JOIN tool_publications p ON p.account_id=g.account_id AND p.id=g.version
WHERE g.account_id=$1 AND g.key_id=$2 AND g.revoked_at IS NULL AND g.expires_at>now() AND p.status='completed'
ORDER BY g.version,g.id;

-- name: LockDelegatedToolGrant :one
SELECT g.payload FROM tool_grants g
JOIN api_keys k ON k.id=g.key_id AND k.account_id=g.account_id
JOIN apps a ON a.id=(g.payload->>'app_id')::uuid AND a.account_id=g.account_id
LEFT JOIN api_key_project_limits l ON l.key_id=k.id
WHERE g.account_id=sqlc.arg(account_id) AND g.id=sqlc.arg(grant_id) AND g.key_id=sqlc.arg(key_id)
AND g.revoked_at IS NULL AND g.expires_at>now() AND (g.payload->'intent'->>'allow_invocation')::boolean IS TRUE
AND k.status IN ('active','grace') AND (k.expires_at IS NULL OR k.expires_at>now())
AND k.scopes @> ARRAY['apps:read']::text[] AND NOT (k.scopes @> ARRAY['admin']::text[])
AND (k.label='mcp-login' OR k.scopes @> ARRAY['agent']::text[])
AND (k.org_id IS NULL OR EXISTS (SELECT 1 FROM orgs o WHERE o.id=k.org_id AND o.personal_org AND o.personal_owner_account_id=g.account_id))
AND (a.org_id IS NULL OR EXISTS (SELECT 1 FROM orgs o WHERE o.id=a.org_id AND o.personal_org AND o.personal_owner_account_id=g.account_id))
AND a.project_id::text IS NOT DISTINCT FROM NULLIF(g.payload->>'project_id','')
AND (l.key_id IS NULL OR a.project_id=ANY(l.project_ids))
FOR SHARE OF g,k,a;

-- name: LockToolGrantCredential :one
SELECT ciphertext FROM app_secrets
WHERE account_id=$1 AND app_id=$2 AND scope=$3 AND key=$4
AND managed_postgres_binding_id IS NULL AND managed_object_storage_credential_id IS NULL
AND COALESCE(managed_credential_ref,'')=''
FOR SHARE;

-- name: InsertToolOAuthConnection :exec
INSERT INTO tool_oauth_connections(account_id,id,app_id,payload,state) VALUES($1,$2,$3,$4,$5);

-- name: CountToolOAuthConnections :one
SELECT count(*) AS total,count(*) FILTER(WHERE app_id=$2) AS per_app FROM tool_oauth_connections WHERE account_id=$1;

-- name: GetToolOAuthConnection :one
SELECT * FROM tool_oauth_connections WHERE account_id=$1 AND id=$2;

-- name: LockToolOAuthConnection :one
SELECT * FROM tool_oauth_connections WHERE account_id=$1 AND id=$2 FOR UPDATE;

-- name: ShareToolOAuthConnection :one
SELECT * FROM tool_oauth_connections WHERE account_id=$1 AND id=$2 FOR SHARE;

-- name: ListToolOAuthConnections :many
SELECT * FROM tool_oauth_connections WHERE account_id=$1 AND id>sqlc.arg(cursor)::uuid ORDER BY id LIMIT $2;

-- name: UpdateToolOAuthConnection :exec
UPDATE tool_oauth_connections SET state=$3,generation=$4,credentials_ciphertext=$5,expires_at=$6,lease_id=$7,lease_until=$8,revoked_at=$9 WHERE account_id=$1 AND id=$2;

-- name: LockToolOAuthApp :one
SELECT a.id FROM apps a WHERE a.account_id=$1 AND a.id=$2 AND a.slug=$3
AND a.project_id::text IS NOT DISTINCT FROM NULLIF(sqlc.arg(project_id)::text,'')
AND (a.org_id IS NULL OR EXISTS(SELECT 1 FROM orgs o WHERE o.id=a.org_id AND o.personal_org AND o.personal_owner_account_id=a.account_id)) FOR SHARE OF a;

-- name: GetToolOAuthAuthorization :one
SELECT * FROM tool_oauth_authorizations WHERE account_id=$1 AND connection_id=$2;

-- name: StoreToolOAuthAuthorization :exec
INSERT INTO tool_oauth_authorizations(account_id,connection_id,state_sha256,phase,expires_at,flow_ciphertext) VALUES($1,$2,$3,$4,$5,$6)
ON CONFLICT(account_id,connection_id) DO UPDATE SET phase=EXCLUDED.phase,flow_ciphertext=EXCLUDED.flow_ciphertext;

-- name: CancelToolOAuthAuthorization :exec
UPDATE tool_oauth_authorizations SET phase='failed',flow_ciphertext=NULL WHERE account_id=$1 AND connection_id=$2;
