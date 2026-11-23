-- name: InsertToolPublication :execrows
INSERT INTO tool_publications (account_id, id, status, payload)
VALUES ($1, $2, 'running', $3)
ON CONFLICT (account_id, id) DO NOTHING;

-- name: InsertTaskToolReleaseIntent :execrows
INSERT INTO task_tool_release_intents(account_id,id,status,payload) VALUES($1,$2,'pending',$3)
ON CONFLICT(account_id,id) DO NOTHING;

-- name: CountTaskToolReleaseIntents :one
SELECT count(*) FROM task_tool_release_intents WHERE account_id=$1;

-- name: GetTaskToolReleaseIntent :one
SELECT payload FROM task_tool_release_intents WHERE account_id=$1 AND id=$2;

-- name: LockTaskToolReleaseIntent :one
SELECT payload FROM task_tool_release_intents WHERE account_id=$1 AND id=$2 FOR UPDATE;

-- name: LockTaskToolReleaseIntentNowait :one
SELECT payload FROM task_tool_release_intents WHERE account_id=$1 AND id=$2 FOR UPDATE NOWAIT;

-- name: UpdateTaskToolReleaseIntent :exec
UPDATE task_tool_release_intents SET status=$3,payload=$4 WHERE account_id=$1 AND id=$2;

-- name: LockTaskToolReleaseApp :one
SELECT a.id FROM apps a WHERE a.account_id=$1 AND a.id=$2 AND a.slug=$3
AND a.status='active' AND (a.org_id IS NULL OR EXISTS(SELECT 1 FROM orgs o WHERE o.id=a.org_id AND o.personal_org AND o.personal_owner_account_id=a.account_id)) FOR SHARE OF a;

-- name: LockTaskToolReleaseKey :one
SELECT k.id FROM api_keys k WHERE k.account_id=$1 AND k.id=$2 AND k.status IN ('active','grace')
AND (k.expires_at IS NULL OR k.expires_at>now())
AND (k.scopes @> ARRAY['deploy:write']::text[] OR k.scopes @> ARRAY['admin']::text[])
AND NOT EXISTS(SELECT 1 FROM api_key_project_limits l WHERE l.key_id=k.id)
AND (k.org_id IS NULL OR EXISTS(SELECT 1 FROM orgs o WHERE o.id=k.org_id AND o.personal_org AND o.personal_owner_account_id=k.account_id)) FOR SHARE OF k;

-- Owner authority is checked again under lock for every adapter transition.
-- name: LockTaskToolExecutionOwnerKey :one
SELECT k.id FROM api_keys k WHERE k.account_id=$1 AND k.id=$2 AND k.status IN ('active','grace')
AND k.revoked_at IS NULL AND (k.expires_at IS NULL OR k.expires_at>now())
AND k.scopes @> ARRAY['admin']::text[] AND NOT k.scopes @> ARRAY['agent']::text[]
AND k.label IS DISTINCT FROM 'mcp-login'
AND NOT EXISTS(SELECT 1 FROM api_key_project_limits l WHERE l.key_id=k.id)
AND (k.org_id IS NULL OR EXISTS(SELECT 1 FROM orgs o WHERE o.id=k.org_id AND o.personal_org AND o.personal_owner_account_id=k.account_id)) FOR SHARE OF k;

-- name: LockPublicationReadinessEvidence :many
SELECT evidence FROM deployment_mcp_readiness_evidence WHERE deployment_id=$1 FOR SHARE;

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

-- name: LockToolPublicationCandidate :one
SELECT d.id FROM deployments d
JOIN apps a ON a.id=d.app_id
JOIN accounts c ON c.id=a.account_id
JOIN api_keys k ON k.account_id=a.account_id
WHERE a.account_id=sqlc.arg(account_id) AND a.id=sqlc.arg(app_id) AND a.slug=sqlc.arg(app_slug)
AND d.id=sqlc.arg(deployment_id) AND d.source_sha256=sqlc.arg(source_sha256)
AND d.status='live' AND d.traffic_percent=0 AND COALESCE(d.scope,'default') IN ('','default')
AND c.status='active' AND k.id=sqlc.arg(key_id) AND k.status IN ('active','grace')
AND (k.expires_at IS NULL OR k.expires_at>now())
AND (k.scopes @> ARRAY['deploy:write']::text[] OR k.scopes @> ARRAY['admin']::text[])
AND NOT EXISTS(SELECT 1 FROM api_key_project_limits l WHERE l.key_id=k.id)
AND (k.org_id IS NULL OR EXISTS(SELECT 1 FROM orgs o WHERE o.id=k.org_id AND o.personal_org AND o.personal_owner_account_id=a.account_id))
AND (a.org_id IS NULL OR EXISTS(SELECT 1 FROM orgs o WHERE o.id=a.org_id AND o.personal_org AND o.personal_owner_account_id=a.account_id))
AND (sqlc.arg(expected_serving)::text='' OR EXISTS(SELECT 1 FROM deployments b WHERE b.app_id=a.id AND b.id::text=sqlc.arg(expected_serving) AND b.status='live' AND b.traffic_percent=100 AND COALESCE(b.scope,'default') IN ('','default')))
AND NOT EXISTS(SELECT 1 FROM deployments b WHERE b.app_id=a.id AND b.status='live' AND b.traffic_percent>0 AND COALESCE(b.scope,'default') IN ('','default') AND (b.id::text<>sqlc.arg(expected_serving) OR b.traffic_percent<>100))
FOR SHARE OF d,a,k,c;

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

-- name: LockTaskRecoveryInstances :many
SELECT id::text AS id, app_id::text AS app_id, COALESCE(deployment_id::text,'')::text AS deployment_id, state::text AS state
FROM instances WHERE app_id=$1 ORDER BY id FOR SHARE;

-- name: LockTaskRecoveryHeartbeat :one
SELECT value,observed_at FROM app_custom_metrics WHERE app_id=$1 AND name='mcp_tasks_observer_heartbeat' FOR SHARE;

-- name: CompleteTaskRecoveryTraffic :execrows
UPDATE task_tool_release_intents AS intent SET payload=sqlc.arg(payload)
WHERE intent.account_id=sqlc.arg(account_id) AND intent.id=sqlc.arg(id)
AND intent.payload->'execution'->'recovery'->>'decision'='approved'
AND intent.payload->'execution'->'recovery'->'result' IS NULL
AND (intent.payload->'execution'->'recovery'->'qualification'->>'expires_at')::timestamptz>clock_timestamp()
AND EXISTS(SELECT 1 FROM api_keys k WHERE k.id=sqlc.arg(owner_key_id)::uuid AND k.account_id=sqlc.arg(account_id)
 AND k.status IN ('active','grace') AND k.revoked_at IS NULL AND (k.expires_at IS NULL OR k.expires_at>clock_timestamp()))
AND EXISTS(SELECT 1 FROM api_keys k WHERE k.id=sqlc.arg(requester_key_id)::uuid AND k.account_id=sqlc.arg(account_id)
 AND k.status IN ('active','grace') AND k.revoked_at IS NULL AND (k.expires_at IS NULL OR k.expires_at>clock_timestamp()))
AND EXISTS(SELECT 1 FROM app_custom_metrics m WHERE m.app_id=sqlc.arg(metric_app_id)::uuid AND m.name='mcp_tasks_observer_heartbeat'
 AND m.value=1 AND m.observed_at<=clock_timestamp() AND m.observed_at>=clock_timestamp()-sqlc.arg(freshness_seconds)::integer*interval '1 second');

-- Block new instance FK admissions while recovery compares the complete VM set.
-- NOWAIT avoids a lock cycle with native traffic/admission transactions.
-- name: LockTaskRecoveryApp :one
SELECT id FROM apps WHERE account_id=$1 AND id=$2 FOR UPDATE NOWAIT;

-- name: TaskQuarantineClockFresh :one
SELECT (sqlc.arg(expires_at)::timestamptz>clock_timestamp()
AND EXISTS(SELECT 1 FROM api_keys k WHERE k.id=sqlc.arg(owner_key_id)::uuid AND k.account_id=sqlc.arg(account_id) AND k.status IN ('active','grace') AND k.revoked_at IS NULL AND (k.expires_at IS NULL OR k.expires_at>clock_timestamp()))
AND EXISTS(SELECT 1 FROM api_keys k WHERE k.id=sqlc.arg(requester_key_id)::uuid AND k.account_id=sqlc.arg(account_id) AND k.status IN ('active','grace') AND k.revoked_at IS NULL AND (k.expires_at IS NULL OR k.expires_at>clock_timestamp())))::boolean AS fresh;

-- name: ReadTaskRetirementApp :one
SELECT status,manifest,COALESCE(workload_class,'')::text AS workload_class,COALESCE(park_transition_id::text,'')::text AS park_transition_id FROM apps WHERE account_id=$1 AND id=$2 FOR SHARE;

-- name: ReadTaskRetirementLatestDeployment :one
SELECT id::text FROM deployments WHERE app_id=$1 ORDER BY created_at DESC LIMIT 1;


-- name: GetHostedTaskSourceIntent :one
SELECT payload,ciphertext FROM hosted_task_source_intents WHERE account_id=$1 AND id=$2;

-- name: LockHostedTaskSourceIntent :one
SELECT payload,ciphertext FROM hosted_task_source_intents WHERE account_id=$1 AND id=$2 FOR UPDATE;

-- name: UpdateHostedTaskSourceIntentReview :exec
UPDATE hosted_task_source_intents SET payload=$3 WHERE account_id=$1 AND id=$2;

-- Account API keys are distinct from app, tenant and hosting-site credentials.
-- name: LockHostedTaskSourceRequesterKey :one
SELECT k.id FROM api_keys k WHERE k.account_id=$1 AND k.id=$2 AND k.status IN ('active','grace')
AND k.revoked_at IS NULL AND (k.expires_at IS NULL OR k.expires_at>clock_timestamp())
AND (k.scopes @> ARRAY['admin']::text[] OR k.scopes @> ARRAY['apps:read','deploy:write']::text[])
AND NOT EXISTS(SELECT 1 FROM api_key_project_limits l WHERE l.key_id=k.id)
AND (k.org_id IS NULL OR EXISTS(SELECT 1 FROM orgs o WHERE o.id=k.org_id AND o.personal_org AND o.personal_owner_account_id=k.account_id)) FOR SHARE OF k;

-- name: LockHostedTaskSourceOwnerKey :one
SELECT k.id FROM api_keys k WHERE k.account_id=$1 AND k.id=$2 AND k.status IN ('active','grace')
AND k.revoked_at IS NULL AND (k.expires_at IS NULL OR k.expires_at>clock_timestamp())
AND k.scopes @> ARRAY['admin']::text[] AND NOT k.scopes @> ARRAY['agent']::text[] AND k.label IS DISTINCT FROM 'mcp-login'
AND NOT EXISTS(SELECT 1 FROM api_key_project_limits l WHERE l.key_id=k.id)
AND (k.org_id IS NULL OR EXISTS(SELECT 1 FROM orgs o WHERE o.id=k.org_id AND o.personal_org AND o.personal_owner_account_id=k.account_id)) FOR SHARE OF k;

-- name: InsertHostedTaskSourceIntent :exec
INSERT INTO hosted_task_source_intents(account_id,id,payload,ciphertext,expires_at) VALUES($1,$2,$3,$4,$5);

-- name: RetireHostedTaskSourceAccount :exec
UPDATE hosted_task_source_intents SET ciphertext=NULL WHERE account_id=$1 AND expires_at<=now() AND ciphertext IS NOT NULL;

-- name: HostedTaskSourceCounts :one
SELECT count(*) AS records,count(*) FILTER(WHERE ciphertext IS NOT NULL) AS active,coalesce(sum(octet_length(ciphertext)),0)::bigint AS cipher_bytes
FROM hosted_task_source_intents WHERE account_id=$1;

-- name: PurgeExpiredHostedTaskSources :execrows
WITH expired AS(SELECT account_id,id FROM hosted_task_source_intents WHERE expires_at<=now() AND ciphertext IS NOT NULL ORDER BY expires_at LIMIT $1 FOR UPDATE SKIP LOCKED)
UPDATE hosted_task_source_intents t SET ciphertext=NULL FROM expired e WHERE t.account_id=e.account_id AND t.id=e.id;

-- name: LockHostedTaskBindingSource :one
SELECT payload,ciphertext,expires_at FROM hosted_task_source_intents WHERE account_id=$1 AND id=$2 FOR UPDATE;

-- name: LockHostedTaskBinding :one
SELECT payload FROM hosted_task_binding_qualifications WHERE account_id=$1 AND id=$2 FOR UPDATE;

-- name: SaveHostedTaskBinding :execrows
INSERT INTO hosted_task_binding_qualifications(account_id,id,payload,expires_at)
SELECT sqlc.arg(account_id),sqlc.arg(id),sqlc.arg(payload),sqlc.arg(expires_at)
WHERE (NOT sqlc.arg(check_owner)::boolean OR sqlc.arg(owner_session)::boolean OR EXISTS(
 SELECT 1 FROM api_keys k WHERE k.account_id=sqlc.arg(account_id) AND k.id::text=sqlc.arg(owner_key_id)::text
 AND k.status IN ('active','grace') AND k.revoked_at IS NULL AND (k.expires_at IS NULL OR k.expires_at>clock_timestamp())
 AND k.scopes @> ARRAY['admin']::text[] AND NOT k.scopes @> ARRAY['agent']::text[] AND k.label IS DISTINCT FROM 'mcp-login'
 AND NOT EXISTS(SELECT 1 FROM api_key_project_limits l WHERE l.key_id=k.id)
 AND (k.org_id IS NULL OR EXISTS(SELECT 1 FROM orgs o WHERE o.id=k.org_id AND o.personal_org AND o.personal_owner_account_id=k.account_id))))
AND (NOT sqlc.arg(require_fresh)::boolean OR (
 sqlc.arg(expires_at)::timestamptz>clock_timestamp()
 AND EXISTS(SELECT 1 FROM hosted_task_source_intents t WHERE t.account_id=sqlc.arg(account_id) AND t.id=sqlc.arg(id)
 AND t.ciphertext IS NOT NULL AND t.expires_at>clock_timestamp() AND t.payload->>'review_status'='approved')
 AND EXISTS(SELECT 1 FROM api_keys k WHERE k.account_id=sqlc.arg(account_id) AND k.id::text=sqlc.arg(requester_key_id)::text
 AND k.status IN ('active','grace') AND k.revoked_at IS NULL AND (k.expires_at IS NULL OR k.expires_at>clock_timestamp())
 AND (k.scopes @> ARRAY['admin']::text[] OR k.scopes @> ARRAY['apps:read','deploy:write']::text[])
 AND NOT EXISTS(SELECT 1 FROM api_key_project_limits l WHERE l.key_id=k.id)
 AND (k.org_id IS NULL OR EXISTS(SELECT 1 FROM orgs o WHERE o.id=k.org_id AND o.personal_org AND o.personal_owner_account_id=k.account_id)))
 AND (sqlc.arg(original_owner_key_id)::text='' OR EXISTS(SELECT 1 FROM api_keys k
 WHERE k.account_id=sqlc.arg(account_id) AND k.id::text=sqlc.arg(original_owner_key_id)::text
 AND k.status IN ('active','grace') AND k.revoked_at IS NULL AND (k.expires_at IS NULL OR k.expires_at>clock_timestamp())
 AND k.scopes @> ARRAY['admin']::text[] AND NOT k.scopes @> ARRAY['agent']::text[] AND k.label IS DISTINCT FROM 'mcp-login'
 AND NOT EXISTS(SELECT 1 FROM api_key_project_limits l WHERE l.key_id=k.id)
 AND (k.org_id IS NULL OR EXISTS(SELECT 1 FROM orgs o WHERE o.id=k.org_id AND o.personal_org AND o.personal_owner_account_id=k.account_id))))))
ON CONFLICT(account_id,id) DO UPDATE SET payload=EXCLUDED.payload
WHERE hosted_task_binding_qualifications.expires_at=EXCLUDED.expires_at;

-- name: HostedTaskBindingClock :one
SELECT clock_timestamp()::timestamptz AS observed_at;

-- NOWAIT keeps a catalog/credential rotation from creating an inverted lock wait.
-- name: LockHostedTaskBindingSecret :one
SELECT account_id::text AS account_id,app_id::text AS app_id,scope,key,ciphertext,
 COALESCE(secret_version,0)::bigint AS secret_version,
 COALESCE(managed_postgres_binding_id::text,'')::text AS binding_id,
 COALESCE(managed_credential_ref,'')::text AS credential_ref,
 COALESCE(managed_credential_generation,0)::bigint AS credential_generation,
 COALESCE(managed_object_storage_credential_id::text,'')::text AS object_storage_id
FROM app_secrets WHERE account_id=$1 AND app_id=$2 AND scope='default' AND key=$3 FOR SHARE NOWAIT;

-- name: LockHostedTaskBindingCatalog :one
SELECT id::text,account_id::text,database_account_id::text,database_id::text,app_id::text,
 scope,environment_key,COALESCE(credential_ref,'')::text AS credential_ref,credential_generation,state,access
FROM managed_postgres_bindings WHERE account_id=$1 AND id=$2 AND deleted_at IS NULL FOR SHARE NOWAIT;

-- name: ReadHostedTaskBindingNamespace :one
SELECT COALESCE(a.manifest->'env'->>sqlc.arg(env_key)::text,'')::text AS manifest_value,
 COALESCE(a.manifest->'env' ? sqlc.arg(env_key)::text,false)::boolean AS manifest_has,
 COALESCE((SELECT e.value FROM app_envs e WHERE e.account_id=a.account_id AND e.app_id=a.id AND e.scope='default' AND e.key=sqlc.arg(env_key)), '')::text AS env_value,
 EXISTS(SELECT 1 FROM app_envs e WHERE e.account_id=a.account_id AND e.app_id=a.id AND e.scope='default' AND e.key=sqlc.arg(env_key))::boolean AS env_has,
 EXISTS(SELECT 1 FROM app_secrets s WHERE s.account_id=a.account_id AND s.app_id=a.id AND s.scope='default' AND s.key=sqlc.arg(env_key))::boolean AS secret
FROM apps a WHERE a.id=sqlc.arg(app_id);

-- name: LockHostedTaskBindingDatabase :one
SELECT id::text,account_id::text,state FROM managed_postgres_databases
WHERE account_id=$1 AND id=$2 FOR SHARE NOWAIT;

-- name: LockHostedTaskRehearsalPlan :one
SELECT payload, expires_at FROM hosted_task_rehearsal_plans
WHERE account_id = $1 AND id = $2 FOR UPDATE;

-- The owner, requester and binding keys have already been validated under row
-- locks. Real-clock checks fence their expiration and the observation deadline
-- even if processing lasted past either one.
-- name: SaveHostedTaskRehearsalPlan :execrows
INSERT INTO hosted_task_rehearsal_plans (account_id, id, payload, expires_at)
SELECT sqlc.arg(account_id), sqlc.arg(id), sqlc.arg(payload), sqlc.arg(expires_at)
WHERE (NOT sqlc.arg(require_fresh)::boolean OR sqlc.arg(expires_at)::timestamptz > clock_timestamp())
AND NOT EXISTS (
    SELECT 1 FROM api_keys AS k
    WHERE k.account_id = sqlc.arg(account_id) AND k.id = ANY(sqlc.arg(authority_keys)::uuid[])
      AND k.expires_at <= clock_timestamp()
)
ON CONFLICT (account_id, id) DO UPDATE SET payload = excluded.payload
WHERE hosted_task_rehearsal_plans.expires_at = excluded.expires_at;

-- name: InsertHostedTaskRehearsalRun :execrows
INSERT INTO hosted_task_rehearsal_runs (account_id, id, run_id, plan_sha256, reserved_at, approval_expires_at)
SELECT sqlc.arg(account_id), sqlc.arg(id), sqlc.arg(run_id), sqlc.arg(plan_sha256),
       sqlc.arg(reserved_at), sqlc.arg(approval_expires_at)
WHERE sqlc.arg(approval_expires_at)::timestamptz > clock_timestamp()
AND NOT EXISTS (
    SELECT 1 FROM api_keys AS k
    WHERE k.account_id = sqlc.arg(account_id) AND k.id = ANY(sqlc.arg(authority_keys)::uuid[])
      AND k.expires_at <= clock_timestamp()
);

-- name: ReadHostedTaskRehearsalRun :one
SELECT run_id, plan_sha256, reserved_at, approval_expires_at FROM hosted_task_rehearsal_runs
WHERE account_id = $1 AND id = $2;


-- name: MatchHostedTaskRehearsalReservation :one
SELECT EXISTS (
  SELECT 1 FROM hosted_task_rehearsal_runs
  WHERE account_id = sqlc.arg(account_id)::uuid AND run_id = sqlc.arg(run_id)::uuid
    AND plan_sha256 = sqlc.arg(plan_sha256)::text AND reserved_at = sqlc.arg(reserved_at)::timestamptz
    AND reserved_at <= clock_timestamp()
);
