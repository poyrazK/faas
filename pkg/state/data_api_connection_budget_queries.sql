-- name: AppConnectionBudgetDatabaseIDs :many
SELECT DISTINCT b.database_id::text AS database_id
FROM managed_postgres_bindings b
JOIN managed_postgres_databases d ON d.id=b.database_id AND d.account_id=b.account_id
WHERE b.account_id=sqlc.arg(account_id)::text::uuid AND b.app_id=sqlc.arg(app_id)::text::uuid
 AND b.access IN ('data_api','read_only')
 AND (b.state<>'deleted' OR EXISTS(SELECT 1 FROM instances i LEFT JOIN deployments dep ON dep.id=i.deployment_id
   WHERE i.app_id=b.app_id AND i.state NOT IN ('stopped','failed')
    AND (dep.id IS NULL OR coalesce(nullif(dep.scope,''),'default')=b.scope)))
 AND d.clone_resource_role='target'
 AND NOT EXISTS(SELECT 1 FROM project_environment_clone_postgres_copy_targets c WHERE c.target_database_id=d.id AND c.state<>'retired')
 AND (d.environment_clone_operation_id IS NULL OR EXISTS(
   SELECT 1 FROM project_environment_clone_operations o WHERE o.id=d.environment_clone_operation_id AND o.account_id=d.account_id AND o.status='ready'
    AND EXISTS(SELECT 1 FROM project_environments e JOIN projects p ON p.id=e.project_id
      WHERE e.project_id=o.project_id AND e.slug=o.target_environment AND p.account_id=d.account_id)
    AND EXISTS(SELECT 1 FROM jsonb_array_elements(o.resources) r WHERE r->>'kind' IN ('postgres','managed_postgres')
      AND r->>'source_id'=d.restore_source_database_id::text AND r->>'target_id'=d.id::text AND r->>'status'='ready')))
ORDER BY database_id LIMIT sqlc.arg(page_limit);

-- name: ReadConnectionBudgetSnapshot :one
WITH owned AS (
 SELECT d.* FROM managed_postgres_databases d
 WHERE d.id=sqlc.arg(database_id)::text::uuid AND d.account_id=sqlc.arg(account_id)::text::uuid AND d.clone_resource_role='target'
  AND NOT EXISTS(SELECT 1 FROM project_environment_clone_postgres_copy_targets c WHERE c.target_database_id=d.id AND c.state<>'retired')
  AND (d.environment_clone_operation_id IS NULL OR EXISTS(
    SELECT 1 FROM project_environment_clone_operations o WHERE o.id=d.environment_clone_operation_id AND o.account_id=d.account_id AND o.status='ready'
     AND EXISTS(SELECT 1 FROM project_environments e JOIN projects p ON p.id=e.project_id
       WHERE e.project_id=o.project_id AND e.slug=o.target_environment AND p.account_id=d.account_id)
     AND EXISTS(SELECT 1 FROM jsonb_array_elements(o.resources) r WHERE r->>'kind' IN ('postgres','managed_postgres')
       AND r->>'source_id'=d.restore_source_database_id::text AND r->>'target_id'=d.id::text AND r->>'status'='ready')))
), candidates AS (
 SELECT b.id,b.app_id,b.scope,b.access,b.state,b.rotation_previous_generation,b.credential_generation,b.provider_identity_id,
  d.desired_generation AS database_generation,d.backend_fingerprint,d.data_resource_id,
  (d.state='ready' AND d.desired_generation=d.observed_generation) AS database_ready,a.max_concurrency,acct.plan::text AS plan
 FROM owned d JOIN managed_postgres_bindings b ON b.database_id=d.id AND b.account_id=d.account_id
 JOIN apps a ON a.id=b.app_id AND a.account_id=b.account_id JOIN accounts acct ON acct.id=a.account_id
 WHERE b.state<>'deleted' OR EXISTS(SELECT 1 FROM instances i LEFT JOIN deployments dep ON dep.id=i.deployment_id
  WHERE i.app_id=b.app_id AND i.state NOT IN ('stopped','failed')
   AND (dep.id IS NULL OR coalesce(nullif(dep.scope,''),'default')=b.scope))
), selected AS (
 SELECT * FROM candidates ORDER BY id LIMIT sqlc.arg(page_limit)
), consumers AS (
 SELECT b.id,jsonb_build_object('app_id',b.app_id,'scope',b.scope,'access',b.access,'state',b.state,'plan',b.plan,
  'max_concurrency',b.max_concurrency,'rotation',b.rotation_previous_generation IS NOT NULL,
  'residents',inventory.residents,'parked',inventory.parked,'auxiliary',inventory.auxiliary,
  'observed_residents',inventory.observed_residents,'observed_connections',inventory.observed_connections,
  'observations_checked_at',inventory.observations_checked_at,'observations_expires_at',inventory.observations_expires_at,
  'stale_nodes',inventory.stale_nodes,'deployments',inventory.deployments,'sidecars',inventory.sidecars,'migrating',inventory.migrating,
  'tasks',(SELECT count(*) FROM app_tasks t WHERE t.app_id=b.app_id AND t.account_id=sqlc.arg(account_id)::text::uuid
    AND coalesce(nullif(t.deployment_scope,''),'default')=b.scope AND t.status NOT IN ('succeeded','failed','cancelled','skipped'))) AS item
 FROM selected b CROSS JOIN LATERAL (
  SELECT count(*) FILTER(WHERE i.kind='wake' AND i.mode='normal' AND dep.id IS NOT NULL AND i.state<>'parked') AS residents,
   count(*) FILTER(WHERE i.kind='wake' AND i.mode='normal' AND dep.id IS NOT NULL AND i.state='parked') AS parked,
   count(*) FILTER(WHERE i.kind<>'wake' OR i.mode<>'normal' OR dep.id IS NULL) AS auxiliary,
   count(*) FILTER(WHERE n.id IS NULL OR n.last_heartbeat_at>statement_timestamp()
     OR n.last_heartbeat_at<statement_timestamp()-make_interval(secs=>sqlc.arg(heartbeat_seconds)::integer)) AS stale_nodes,
   count(DISTINCT dep.id) FILTER(WHERE i.state<>'parked' AND i.kind='wake' AND i.mode='normal') AS deployments,
   coalesce(bool_or(jsonb_array_length(coalesce(dep.sidecars,'[]'::jsonb))>0),false) AS sidecars,
   count(*) FILTER(WHERE i.kind='wake' AND i.mode='normal' AND i.state<>'parked' AND o.state='observed') AS observed_residents,
   coalesce(sum(o.engine_connections+o.refresh_connections) FILTER(WHERE i.kind='wake' AND i.mode='normal' AND i.state<>'parked' AND o.state='observed'),0) AS observed_connections,
   min(o.observed_at) FILTER(WHERE i.state<>'parked' AND o.state='observed') AS observations_checked_at,
   min(o.expires_at) FILTER(WHERE i.state<>'parked' AND o.state='observed') AS observations_expires_at,
   coalesce(bool_or(i.state='migrating' OR i.migrated_from_node_id IS NOT NULL),false) AS migrating
  FROM instances i LEFT JOIN deployments dep ON dep.id=i.deployment_id AND dep.app_id=b.app_id
  LEFT JOIN compute_nodes n ON n.id=i.node_id
  LEFT JOIN data_api_runtime_connection_observations o ON o.instance_id=i.id AND o.binding_id=b.id
   AND b.access='data_api' AND b.state='ready' AND b.database_ready AND i.kind='wake' AND i.mode='normal'
   AND o.account_id=sqlc.arg(account_id)::text::uuid AND o.deployment_id=i.deployment_id AND o.node_id=i.node_id AND o.wake_id=i.wake_id
   AND o.database_generation=b.database_generation AND o.backend_fingerprint=b.backend_fingerprint AND o.data_resource_id=b.data_resource_id
   AND o.credential_generation=b.credential_generation AND o.provider_identity_id=b.provider_identity_id
   AND o.observed_at>=i.started_at AND o.observed_at<=statement_timestamp() AND o.expires_at>statement_timestamp()
   AND n.last_heartbeat_at<=statement_timestamp()
   AND n.last_heartbeat_at>statement_timestamp()-make_interval(secs=>sqlc.arg(heartbeat_seconds)::integer)
  WHERE i.app_id=b.app_id AND i.state NOT IN ('stopped','failed')
   AND (dep.id IS NULL OR coalesce(nullif(dep.scope,''),'default')=b.scope)
 ) inventory
)
SELECT d.id::text AS database_id,d.name,d.state,d.backend_fingerprint,d.observed_generation,d.data_resource_id,
 statement_timestamp()::timestamptz AS checked_at,
 (SELECT count(*)>sqlc.arg(page_limit)::integer FROM candidates) AS truncated,
 coalesce((SELECT jsonb_agg(item ORDER BY id) FROM consumers),'[]'::jsonb)::jsonb AS bindings,
 coalesce(c.evidence,'null'::jsonb)::jsonb AS capacity
FROM owned d LEFT JOIN managed_postgres_connection_capacity c ON c.database_id=d.id
 AND c.account_id=d.account_id AND c.state='verified' AND d.desired_generation=d.observed_generation
 AND (c.backend_id,c.backend_fingerprint,c.provider_resource_id,c.data_resource_id,c.generation)
 = (d.backend_id,d.backend_fingerprint,d.provider_resource_id,d.data_resource_id,d.desired_generation);

-- name: ClaimManagedPostgresConnectionCapacity :execrows
WITH target AS (
 SELECT d.* FROM managed_postgres_databases d
 WHERE d.id=sqlc.arg(database_id)::text::uuid AND d.account_id=sqlc.arg(account_id)::text::uuid
  AND d.state='ready' AND d.desired_generation=d.observed_generation
  AND (d.backend_id,d.backend_fingerprint,d.provider_resource_id,d.data_resource_id,d.desired_generation)
   = (sqlc.arg(backend_id)::text,sqlc.arg(backend_fingerprint)::text,sqlc.arg(provider_resource_id)::text,
      sqlc.arg(data_resource_id)::text,sqlc.arg(generation)::bigint) FOR SHARE
)
INSERT INTO managed_postgres_connection_capacity AS c
 (database_id,account_id,backend_id,backend_fingerprint,provider_resource_id,data_resource_id,generation,
  state,evidence,checked_at,expires_at,lease_token,lease_until)
SELECT id,account_id,backend_id,backend_fingerprint,provider_resource_id,data_resource_id,desired_generation,
 'unknown',NULL,sqlc.arg(now)::timestamptz,sqlc.arg(now),sqlc.arg(lease_token)::text,sqlc.arg(lease_until)::timestamptz FROM target
ON CONFLICT(database_id) DO UPDATE SET
 account_id=excluded.account_id,backend_id=excluded.backend_id,backend_fingerprint=excluded.backend_fingerprint,
 provider_resource_id=excluded.provider_resource_id,data_resource_id=excluded.data_resource_id,generation=excluded.generation,
 state='unknown',evidence=NULL,checked_at=excluded.checked_at,expires_at=excluded.expires_at,
 lease_token=excluded.lease_token,lease_until=excluded.lease_until
WHERE c.lease_until IS NULL OR c.lease_until<=sqlc.arg(now)
 OR (c.account_id,c.backend_id,c.backend_fingerprint,c.provider_resource_id,c.data_resource_id,c.generation)
 <> (excluded.account_id,excluded.backend_id,excluded.backend_fingerprint,excluded.provider_resource_id,excluded.data_resource_id,excluded.generation);

-- name: FinishManagedPostgresConnectionCapacity :execrows
WITH target AS (
 SELECT d.* FROM managed_postgres_databases d
 WHERE d.id=sqlc.arg(database_id)::text::uuid AND d.account_id=sqlc.arg(account_id)::text::uuid
  AND d.state='ready' AND d.desired_generation=d.observed_generation FOR SHARE
)
UPDATE managed_postgres_connection_capacity c SET state=sqlc.arg(state)::text,evidence=sqlc.narg(evidence)::jsonb,
 checked_at=sqlc.arg(checked_at)::timestamptz,expires_at=sqlc.arg(expires_at)::timestamptz,lease_token=NULL,lease_until=NULL
FROM target d WHERE c.database_id=d.id AND c.lease_token=sqlc.arg(lease_token)::text AND c.lease_until>sqlc.arg(now)::timestamptz
 AND c.checked_at=sqlc.arg(checked_at)
 AND (c.account_id,c.backend_id,c.backend_fingerprint,c.provider_resource_id,c.data_resource_id,c.generation)
 = (d.account_id,d.backend_id,d.backend_fingerprint,d.provider_resource_id,d.data_resource_id,d.desired_generation);
