-- ADR-20261103222927: counts and offsets see only the immutable app/scope.
-- Every projection is an explicit metadata allowlist. No credential payload,
-- provider identity, physical bucket name or raw provider error is selected.
-- name: AppResourceInventory :one
WITH target AS (
 SELECT id, account_id, org_id FROM apps
 WHERE id=sqlc.arg(app_id)::uuid AND account_id=sqlc.arg(account_id)::uuid
   AND COALESCE(org_id::text,'')=sqlc.arg(org_id)::text AND status<>'deleted'
), selected AS NOT MATERIALIZED (
 SELECT lower(d.domain::text) AS sort_key,
  jsonb_build_object('id',lower(d.domain::text),'app_id',a.id,'scope',sqlc.arg(scope)::text,
   'domain',lower(d.domain::text),'verified',d.verified_at IS NOT NULL,'cert_status',d.cert_status) AS item
 FROM custom_domains d JOIN target a ON a.id=d.app_id
 WHERE sqlc.arg(kind)::text='domains' AND d.environment_id IS NULL
 UNION ALL
 SELECT c.id::text,
  jsonb_build_object('id',c.id,'app_id',a.id,'scope',sqlc.arg(scope)::text,
   'schedule',c.schedule,'path',c.path,'timezone',c.timezone,'enabled',c.enabled)
 FROM crons c JOIN target a ON a.id=c.app_id WHERE sqlc.arg(kind)::text='crons'
 UNION ALL
 SELECT b.name||'/'||b.id::text,
  jsonb_build_object('id',b.id,'app_id',a.id,'scope',b.scope,'name',b.name,'region',b.region,'state',b.state)
 FROM object_buckets b JOIN target a ON a.id=b.app_id AND a.account_id=b.account_id
 WHERE sqlc.arg(kind)::text='buckets' AND b.scope=sqlc.arg(scope)::text AND b.state<>'deleted'
  AND (b.environment_clone_operation_id IS NULL OR EXISTS (
   SELECT 1 FROM project_environment_clone_operations o WHERE o.id=b.environment_clone_operation_id
    AND o.account_id=b.account_id AND o.target_environment=b.scope AND o.status='ready'))
 UNION ALL
 SELECT b.name||'/'||c.id::text,
  jsonb_build_object('id',c.id,'app_id',a.id,'scope',c.managed_scope,'bucket_id',b.id,'bucket',b.name,
   'prefix',COALESCE(c.managed_prefix,''),'rotation_pending',EXISTS (
    SELECT 1 FROM object_storage_s3_credentials stage WHERE stage.rotation_parent_id=c.id
     AND stage.account_id=c.account_id AND stage.status='active'))
 FROM object_storage_s3_credentials c
 JOIN object_buckets b ON b.id=c.bucket_id AND b.account_id=c.account_id
 JOIN target a ON a.id=b.app_id AND a.id=c.managed_app_id AND a.account_id=c.account_id
 WHERE sqlc.arg(kind)::text='storage_bindings' AND b.scope=sqlc.arg(scope)::text
  AND c.managed_scope=sqlc.arg(scope)::text AND b.state<>'deleted'
  AND c.status='active' AND c.rotation_parent_id IS NULL
  AND (b.environment_clone_operation_id IS NULL OR EXISTS (
   SELECT 1 FROM project_environment_clone_operations o WHERE o.id=b.environment_clone_operation_id
    AND o.account_id=b.account_id AND o.target_environment=b.scope AND o.status='ready'))
 UNION ALL
 SELECT d.id::text||'/'||b.id::text,
  jsonb_build_object('id',d.id,'app_id',a.id,'scope',b.scope,'name',d.name,'region',d.region,
   'state',d.state,'postgres_major',d.postgres_major,
   'binding',jsonb_build_object('id',b.id,'database_id',d.id,'app_id',a.id,'scope',b.scope,
    'access',b.access,'state',b.state,'environment_key',b.environment_key,
    'rotation_pending',COALESCE(b.rotation_previous_generation,0)>0))
 FROM managed_postgres_bindings b
 JOIN managed_postgres_databases d ON d.id=b.database_id AND d.account_id=b.database_account_id
 JOIN target a ON a.id=b.app_id AND a.account_id=b.account_id
 WHERE sqlc.arg(kind)::text='databases' AND b.scope=sqlc.arg(scope)::text
  AND b.state<>'deleted' AND d.state<>'deleted'
), paged AS (
 SELECT sort_key,item FROM selected ORDER BY sort_key
 LIMIT sqlc.arg(row_limit)::integer OFFSET sqlc.arg(row_offset)::integer
)
SELECT (SELECT count(*) FROM selected)::bigint AS total,
 COALESCE((SELECT jsonb_agg(item ORDER BY sort_key) FROM paged),'[]'::jsonb)::jsonb AS items;
