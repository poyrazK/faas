-- name: LockMCPAccessPolicyAccount :exec
SELECT pg_advisory_xact_lock(hashtext('mcp_access_policy'), hashtext(sqlc.arg(account_id)::text));

-- name: ReadMCPAccessPolicyRules :many
SELECT id, account_id, app_id, match_host, match_path, match_methods,
       match_headers, priority, enabled, kind, action, expires_at, match_expr, mode
FROM edge_rules WHERE account_id = sqlc.arg(account_id)::uuid AND kind = 'jwt'
ORDER BY id;

-- name: ReadMCPAccessPolicyAppAccount :one
SELECT account_id FROM apps WHERE id = sqlc.arg(app_id)::uuid;

-- name: ReadMCPGatewayPromotionEvidence :one
WITH sampled AS (SELECT clock_timestamp()::timestamptz AS captured_at), position AS (
 SELECT GREATEST(
   COALESCE((SELECT MAX(id) FROM edge_rule_change_log WHERE account_ids @> ARRAY[sqlc.arg(account_id)::uuid]), 0),
   COALESCE((SELECT MAX(id) FROM edge_rule_change_log WHERE account_ids IS NULL), 0)
 )::bigint AS revision
), gateways AS (
 SELECT n.name, w.boot_id, w.last_change_id, w.observed_at
 FROM compute_nodes n LEFT JOIN gateway_edge_rule_watermarks w ON w.node_name = n.name
 WHERE n.active AND n.role IN ('compute-only', 'compute-node')
   AND n.gateway_target_url IS NOT NULL AND btrim(n.gateway_target_url) <> ''
)
SELECT position.revision, sampled.captured_at,
 COALESCE((SELECT jsonb_agg(jsonb_build_object('node_name', name, 'boot_id', boot_id, 'revision', last_change_id, 'observed_at', observed_at) ORDER BY name) FROM gateways), '[]'::jsonb)::jsonb AS gateways,
 (SELECT COUNT(*) FROM gateways)::bigint AS serving,
 (SELECT COUNT(*) FROM gateways WHERE observed_at IS NULL OR observed_at < sampled.captured_at - interval '10 seconds' OR observed_at > sampled.captured_at)::bigint AS stale,
 (SELECT COUNT(*) FROM gateways WHERE COALESCE(last_change_id, 0) < position.revision)::bigint AS pending
FROM position CROSS JOIN sampled;

-- name: AppendMCPAdmissionEvidence :execrows
INSERT INTO deployment_audit (deployment_id, account_id, kind, actor, at, data)
SELECT d.id, a.account_id, 'deploy.mcp_admission', 'apid:mcp_traffic_admission', clock_timestamp(), sqlc.arg(data)::jsonb
FROM deployments d JOIN apps a ON a.id=d.app_id
WHERE d.id=sqlc.arg(deployment_id)::uuid AND a.id=sqlc.arg(app_id)::uuid AND a.account_id=sqlc.arg(account_id)::uuid
ON CONFLICT DO NOTHING;

-- name: MCPAdmissionEvidenceExists :one
SELECT EXISTS(SELECT 1 FROM deployment_audit WHERE kind='deploy.mcp_admission'
 AND account_id=sqlc.arg(account_id)::uuid AND data->'mcp_admission'->>'id'=sqlc.arg(evidence_id)::text);

-- name: ReadMCPPolicyConvergenceSnapshot :one
WITH sampled AS (SELECT clock_timestamp()::timestamptz AS captured_at), position AS (
 SELECT GREATEST(
   COALESCE((SELECT MAX(id) FROM edge_rule_change_log WHERE account_ids @> ARRAY[sqlc.arg(account_id)::uuid]), 0),
   COALESCE((SELECT MAX(id) FROM edge_rule_change_log WHERE account_ids IS NULL), 0)
 )::bigint AS revision
), gateways AS (
 SELECT n.name, w.boot_id, w.last_change_id, w.observed_at
 FROM compute_nodes n LEFT JOIN gateway_edge_rule_watermarks w ON w.node_name = n.name
 WHERE n.active AND n.role IN ('compute-only', 'compute-node')
   AND n.gateway_target_url IS NOT NULL AND btrim(n.gateway_target_url) <> ''
)
SELECT a.slug, target.id AS deployment_id, COALESCE(receipt.admission_id, '')::text AS admission_id, position.revision, sampled.captured_at,
 COALESCE((SELECT jsonb_agg(jsonb_build_object('node_name', name, 'boot_id', boot_id, 'revision', last_change_id, 'observed_at', observed_at) ORDER BY name) FROM gateways), '[]'::jsonb)::jsonb AS gateways,
 (SELECT COUNT(*) FROM gateways WHERE observed_at IS NULL OR observed_at < sampled.captured_at - interval '10 seconds' OR observed_at > sampled.captured_at OR COALESCE(last_change_id,0)<position.revision)::bigint AS unconverged,
 (SELECT COUNT(*) FROM gateways)::bigint AS serving,
 (SELECT COUNT(*) FROM gateways WHERE observed_at IS NULL OR observed_at < sampled.captured_at - interval '10 seconds' OR observed_at > sampled.captured_at)::bigint AS stale,
 (SELECT COUNT(*) FROM gateways WHERE COALESCE(last_change_id, 0) < position.revision)::bigint AS pending
FROM position CROSS JOIN sampled
JOIN apps a ON a.id=sqlc.arg(app_id)::uuid AND a.account_id=sqlc.arg(account_id)::uuid
LEFT JOIN LATERAL (
 SELECT d.id FROM deployments d
 WHERE d.app_id=a.id AND d.status='live' AND d.traffic_percent>0 AND a.workload_class='http' AND a.status IN ('active','evicted_cold')
 ORDER BY d.traffic_percent DESC,d.created_at DESC,d.id DESC LIMIT 1
) target ON true
LEFT JOIN LATERAL (
 SELECT data->'mcp_admission'->>'id' AS admission_id FROM deployment_audit
 WHERE deployment_id=target.id AND account_id=a.account_id AND kind='deploy.mcp_admission'
 AND data->'mcp_admission'->>'outcome'='accepted'
 ORDER BY at DESC,id DESC LIMIT 1
) receipt ON true;

-- name: ListMCPAdmissionHistory :many
SELECT da.id, da.data AS evidence
FROM deployment_audit da JOIN deployments d ON d.id=da.deployment_id
JOIN apps a ON a.id=d.app_id
WHERE da.kind='deploy.mcp_admission' AND da.account_id=sqlc.arg(account_id)::uuid
AND a.account_id=sqlc.arg(account_id)::uuid AND da.deployment_id=sqlc.arg(deployment_id)::uuid
AND (sqlc.arg(before_id)::bigint=0 OR da.id<sqlc.arg(before_id)::bigint)
AND (sqlc.arg(outcome)::text='' OR da.data->'mcp_admission'->>'outcome'=sqlc.arg(outcome)::text)
ORDER BY da.id DESC LIMIT sqlc.arg(page_limit)::int;

-- name: GetMCPAdmissionReceipt :one
SELECT da.data AS evidence
FROM deployment_audit da JOIN deployments d ON d.id=da.deployment_id
JOIN apps a ON a.id=d.app_id
WHERE da.kind='deploy.mcp_admission' AND da.account_id=sqlc.arg(account_id)::uuid
AND a.account_id=sqlc.arg(account_id)::uuid
AND da.data->'mcp_admission'->>'id'=sqlc.arg(evidence_id)::text;

-- name: GetMCPCatalogMonitor :one
SELECT m.payload FROM mcp_catalog_monitors m JOIN apps a ON a.id=m.app_id
WHERE m.app_id=sqlc.arg(app_id)::uuid AND m.account_id=sqlc.arg(account_id)::uuid AND a.account_id=m.account_id;
-- name: LockMCPCatalogMonitor :one
SELECT m.payload FROM mcp_catalog_monitors m JOIN apps a ON a.id=m.app_id
WHERE m.app_id=sqlc.arg(app_id)::uuid AND m.account_id=sqlc.arg(account_id)::uuid AND a.account_id=m.account_id FOR UPDATE OF m;
-- name: PutMCPCatalogMonitor :execrows
INSERT INTO mcp_catalog_monitors(app_id,account_id,payload)
SELECT a.id,a.account_id,sqlc.arg(payload)::jsonb FROM apps a
WHERE a.id=sqlc.arg(app_id)::uuid AND a.account_id=sqlc.arg(account_id)::uuid
ON CONFLICT(app_id) DO UPDATE SET account_id=EXCLUDED.account_id,payload=EXCLUDED.payload;

-- name: LockMCPCatalogMonitorApp :one
SELECT id FROM apps WHERE id=sqlc.arg(app_id)::uuid AND account_id=sqlc.arg(account_id)::uuid FOR UPDATE;

-- name: ListDueMCPCatalogObservers :many
SELECT m.app_id,m.account_id,a.slug,views.key::text AS role,views.value::jsonb AS view
FROM mcp_catalog_monitors m JOIN apps a ON a.id=m.app_id AND a.account_id=m.account_id
JOIN app_resource_accounts ac ON ac.app_id = a.id
CROSS JOIN LATERAL jsonb_each(jsonb_build_object('default',m.payload)||COALESCE(m.payload->'roles','{}'::jsonb)) views
WHERE a.workload_class='http' AND a.status IN ('active','evicted_cold') AND ac.plan IN ('hobby','pro','scale')
AND a.public_auth_mode<>'internal_only'
AND views.value->'config'->>'enabled'='true'
AND views.value->'config'->'observer'->>'enabled'='true'
AND COALESCE(views.value->'config'->'observer'->>'paused','false')='false'
AND (views.value->'observer_status'->>'lease_until' IS NULL OR (views.value->'observer_status'->>'lease_until')::timestamptz<=now())
AND (views.value->'observer_status'->>'next_check_at' IS NULL OR (views.value->'observer_status'->>'next_check_at')::timestamptz<=now())
ORDER BY COALESCE((views.value->'observer_status'->>'next_check_at')::timestamptz,'epoch'::timestamptz),m.app_id,role
LIMIT sqlc.arg(batch_limit)::int;

-- name: ListDueMCPCatalogOAuthRevocations :many
SELECT m.app_id,m.account_id,a.slug,views.key::text AS role,views.value::jsonb AS view
FROM mcp_catalog_monitors m JOIN apps a ON a.id=m.app_id AND a.account_id=m.account_id
CROSS JOIN LATERAL jsonb_each(jsonb_build_object('default',m.payload)||COALESCE(m.payload->'roles','{}'::jsonb)) views
WHERE views.value->'oauth_lifecycle'->'revocation'->>'state'='pending'
AND (views.value->'oauth_lifecycle'->'revocation'->>'private_lease_until' IS NULL OR (views.value->'oauth_lifecycle'->'revocation'->>'private_lease_until')::timestamptz<=now())
AND (views.value->'oauth_lifecycle'->'revocation'->>'next_attempt_at')::timestamptz<=now()
ORDER BY (views.value->'oauth_lifecycle'->'revocation'->>'next_attempt_at')::timestamptz,m.app_id,role
LIMIT sqlc.arg(batch_limit)::int;

-- name: LockMCPOAuthRetirementAccount :exec
SELECT pg_advisory_xact_lock(hashtext('mcp_oauth_retirement'),hashtext(sqlc.arg(account_id)::text));
-- name: GetMCPOAuthRetirementApp :one
SELECT slug FROM apps WHERE id=sqlc.arg(app_id)::uuid AND account_id=sqlc.arg(account_id)::uuid AND status<>'deleted';
-- name: CountMCPOAuthRetirements :one
SELECT count(*) FROM mcp_oauth_retirements WHERE account_id=sqlc.arg(account_id)::uuid AND state IN ('awaiting_issuance','pending','held','manual_required');
-- name: InsertMCPOAuthRetirement :execrows
INSERT INTO mcp_oauth_retirements(id,account_id,app_id,binding_sha256,created_at,retain_until,next_attempt_at,payload)
VALUES(sqlc.arg(id)::uuid,sqlc.arg(account_id)::uuid,sqlc.arg(app_id)::uuid,sqlc.arg(binding_sha256)::text,sqlc.arg(created_at)::timestamptz,sqlc.arg(retain_until)::timestamptz,sqlc.arg(next_attempt_at)::timestamptz,sqlc.arg(payload)::jsonb)
ON CONFLICT(id) DO NOTHING;
-- name: ReadMCPOAuthRetirement :one
SELECT payload FROM mcp_oauth_retirements WHERE id=sqlc.arg(id)::uuid AND account_id=sqlc.arg(account_id)::uuid;
-- name: LockMCPOAuthRetirement :one
SELECT payload FROM mcp_oauth_retirements WHERE id=sqlc.arg(id)::uuid AND account_id=sqlc.arg(account_id)::uuid FOR UPDATE;
-- name: PutMCPOAuthRetirement :execrows
UPDATE mcp_oauth_retirements SET payload=sqlc.arg(payload)::jsonb,next_attempt_at=sqlc.arg(next_attempt_at)::timestamptz
WHERE id=sqlc.arg(id)::uuid AND account_id=sqlc.arg(account_id)::uuid;
-- name: ListMCPOAuthRetirements :many
SELECT payload FROM mcp_oauth_retirements WHERE account_id=sqlc.arg(account_id)::uuid
ORDER BY created_at DESC,id DESC LIMIT 50 OFFSET sqlc.arg(page_offset)::int;
-- name: ListDueMCPOAuthRetirements :many
SELECT account_id,payload FROM mcp_oauth_retirements
WHERE (state IN ('pending','held','awaiting_issuance') AND next_attempt_at<=now() AND (payload->'revocation'->>'private_lease_until' IS NULL OR (payload->'revocation'->>'private_lease_until')::timestamptz<=now()))
OR (state IN ('pending','held','awaiting_issuance','manual_required') AND retain_until<=now())
ORDER BY next_attempt_at,id LIMIT sqlc.arg(batch_limit)::int;
-- name: MCPOAuthRetirementActiveBinding :one
SELECT EXISTS (
SELECT 1 FROM mcp_catalog_monitors m JOIN apps a ON a.id=m.app_id AND a.account_id=m.account_id AND a.status<>'deleted'
CROSS JOIN LATERAL jsonb_each(jsonb_build_object('default',m.payload)||COALESCE(m.payload->'roles','{}'::jsonb)) views
WHERE m.account_id=sqlc.arg(account_id)::uuid AND (views.value->'observer_status'->'oauth'->>'ciphertext' IS NOT NULL OR views.value->'observer_status'->'oauth_renewal'->>'state' IN ('pending','saved') OR views.value->'oauth_lifecycle'->>'credential_state' IN ('credential_changed','retirement_held'))
AND views.value->'config'->'observer'->'oauth'->>'token_endpoint'=sqlc.arg(token_endpoint)::text
AND views.value->'config'->'observer'->'oauth'->>'client_id'=sqlc.arg(client_id)::text
) OR EXISTS (
 SELECT 1 FROM mcp_oauth_retirements WHERE account_id=sqlc.arg(account_id)::uuid AND binding_sha256=sqlc.arg(binding_sha256)::text
 AND id<>sqlc.arg(id)::uuid AND state='awaiting_issuance' AND next_attempt_at>now()
) AS active;
-- name: MCPOAuthRetirementBindingBusy :one
SELECT EXISTS(SELECT 1 FROM mcp_oauth_retirements WHERE account_id=sqlc.arg(account_id)::uuid AND binding_sha256=sqlc.arg(binding_sha256)::text
AND state='pending') AS busy;
-- name: MCPOAuthRetirementClientCiphertext :one
SELECT ciphertext FROM app_secrets WHERE account_id=sqlc.arg(account_id)::uuid AND app_id=sqlc.arg(app_id)::uuid AND key=sqlc.arg(key)::text
AND scope='default' AND managed_postgres_binding_id IS NULL AND managed_object_storage_credential_id IS NULL AND managed_credential_ref IS NULL;
-- name: InstallMCPOAuthRetirement :execrows
UPDATE mcp_oauth_retirements SET payload=jsonb_set(jsonb_set(payload,'{state}','"installed"'),'{revocation}',sqlc.arg(public_phase)::jsonb)
WHERE id=sqlc.arg(id)::uuid AND account_id=sqlc.arg(account_id)::uuid AND app_id=sqlc.arg(app_id)::uuid
AND payload->>'role'=sqlc.arg(role)::text AND binding_sha256=sqlc.arg(binding_sha256)::text
AND state IN ('pending','held','manual_required') AND payload->>'reason'='authorization_exchange' AND payload->'revocation'->>'private_ciphertext'=sqlc.arg(ciphertext_base64)::text
AND (payload->'revocation'->>'private_lease_until' IS NULL OR (payload->'revocation'->>'private_lease_until')::timestamptz<=now());
-- name: PruneMCPOAuthRetirementMetadata :exec
DELETE FROM mcp_oauth_retirements WHERE state IN ('installed','provider_acknowledged','manually_acknowledged','expired') AND created_at<now()-interval '30 days';
-- name: MCPOAuthRetirementFlowCurrent :one
SELECT EXISTS(SELECT 1 FROM mcp_catalog_monitors m JOIN apps a ON a.id=m.app_id AND a.account_id=m.account_id AND a.status<>'deleted'
CROSS JOIN LATERAL jsonb_each(jsonb_build_object('default',m.payload)||COALESCE(m.payload->'roles','{}'::jsonb)) views
WHERE m.account_id=sqlc.arg(account_id)::uuid AND m.app_id=sqlc.arg(app_id)::uuid AND views.key=sqlc.arg(role)::text
AND views.value->'config'->>'revision'=sqlc.arg(revision)::text AND views.value->'config'->'observer'->'oauth'=sqlc.arg(config)::jsonb
AND views.value->'observer_status'->'oauth_flow'->>'consumed'='true'
AND (views.value->'observer_status'->'oauth_flow'->>'expires_at')::timestamptz>now()) AS current;

-- name: MCPConnectionHealthBindings :many
SELECT jsonb_build_object('app_id',a.id,'app_slug',a.slug,'role',v.key,
'observer_state',v.value->'observer_status'->>'state','oauth_state',v.value->'observer_status'->'oauth'->>'state','credential_state',v.value->'oauth_lifecycle'->>'credential_state',
'guided',COALESCE((v.value->'config'->'observer'->'oauth'->>'guided')::boolean,false),
'enabled',COALESCE((v.value->'config'->>'enabled')::boolean,false) AND COALESCE((v.value->'config'->'observer'->>'enabled')::boolean,false),
'paused',COALESCE((v.value->'config'->'observer'->>'paused')::boolean,false),
'journal_state',v.value->'observer_status'->'oauth_renewal'->>'state','journal_outcome',v.value->'observer_status'->'oauth_renewal'->>'outcome',
'expires_at',v.value->'observer_status'->'oauth'->>'expires_at','lease_until',v.value->'observer_status'->>'lease_until',
'flow_expires_at',v.value->'observer_status'->'oauth_flow'->>'expires_at','failure_reason',v.value->'observer_status'->>'failure_reason') AS health
FROM mcp_catalog_monitors m JOIN apps a ON a.id=m.app_id AND a.account_id=m.account_id
CROSS JOIN LATERAL jsonb_each(jsonb_build_object('default',m.payload)||COALESCE(m.payload->'roles','{}'::jsonb)) v
WHERE m.account_id=sqlc.arg(account_id)::uuid AND a.status<>'deleted' AND (sqlc.arg(app_filter)::text='' OR a.id::text=sqlc.arg(app_filter)::text)
AND v.value->'config'->'observer'->'oauth' IS NOT NULL AND v.value->'config'->'observer'->'oauth'<>'null'::jsonb
ORDER BY a.id,health LIMIT sqlc.arg(source_limit)::int;

-- name: MCPConnectionHealthRetirements :many
SELECT jsonb_build_object('app_id',r.app_id,'app_slug',r.payload->>'app_slug','role',r.payload->>'role',
'retirement_id',r.id,'retirement_state',r.state,'retirement_reason',r.payload->>'reason','retain_until',r.retain_until,
'next_attempt_at',r.next_attempt_at,'app_deleted',a.id IS NULL OR a.status='deleted') AS health
FROM mcp_oauth_retirements r LEFT JOIN apps a ON a.id=r.app_id AND a.account_id=r.account_id
WHERE r.account_id=sqlc.arg(account_id)::uuid AND (sqlc.arg(app_filter)::text='' OR r.app_id::text=sqlc.arg(app_filter)::text)
AND r.state IN ('awaiting_issuance','pending','held','manual_required','expired')
ORDER BY r.app_id,r.id LIMIT sqlc.arg(source_limit)::int;
