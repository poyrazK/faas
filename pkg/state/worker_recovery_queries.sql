-- name: ListWorkerRecoveryApps :many
SELECT a.id FROM apps a JOIN accounts ac ON ac.id = a.account_id
LEFT JOIN worker_recovery r ON r.app_id = a.id
WHERE a.status = 'active'
  AND (a.manifest->>'execution_mode' = 'worker' OR a.workload_class = 'worker'
    OR EXISTS (SELECT 1 FROM deployments d
      JOIN project_environment_workload_deployment_specs p ON p.deployment_id = d.id
      JOIN project_environment_workload_specs s ON s.id = p.spec_id AND s.app_id = a.id
      WHERE d.app_id = a.id AND d.status = 'live'
        AND (s.settings->'manifest'->>'execution_mode' = 'worker' OR s.settings->>'workload_class' = 'worker'))
    OR EXISTS (SELECT 1 FROM instances i WHERE i.app_id = a.id AND i.mode = 'worker'
      AND i.state IN ('running', 'waking', 'cold_booting', 'draining', 'migrating', 'snapshotting', 'warm', 'frozen')))
  AND ac.status IN ('active', 'past_due') AND ac.abuse_hold_at IS NULL
  AND (sqlc.arg(owner_node_id)::text = '' OR a.node_id IS NULL OR a.node_id::text = sqlc.arg(owner_node_id)::text)
  AND coalesce(r.lease_until, '1970-01-01'::timestamptz) <= sqlc.arg(sampled_at)::timestamptz
  AND coalesce(r.next_attempt_at, '1970-01-01'::timestamptz) <= sqlc.arg(sampled_at)::timestamptz
ORDER BY coalesce(r.next_attempt_at, '1970-01-01'::timestamptz), a.id
LIMIT sqlc.arg(batch_limit)::integer;

-- Claims always allow an event to inspect demand and drain surplus workers.
-- The returned failure deadline gates new admission inside the reconciler.
-- name: ClaimWorkerRecovery :one
INSERT INTO worker_recovery(app_id, revision, claim_token, lease_until, status, next_attempt_at, updated_at)
SELECT a.id, sqlc.arg(revision)::text, sqlc.arg(claim_token)::uuid, sqlc.arg(lease_until)::timestamptz,
       'reconciling', sqlc.arg(sampled_at)::timestamptz, sqlc.arg(sampled_at)::timestamptz
FROM apps a JOIN accounts ac ON ac.id = a.account_id
WHERE a.id = sqlc.arg(app_id)::uuid AND a.status = 'active'
  AND ac.status IN ('active', 'past_due') AND ac.abuse_hold_at IS NULL
  AND (sqlc.arg(owner_node_id)::text = '' OR a.node_id IS NULL OR a.node_id::text = sqlc.arg(owner_node_id)::text)
ON CONFLICT (app_id) DO UPDATE SET
 revision = EXCLUDED.revision, claim_token = EXCLUDED.claim_token, lease_until = EXCLUDED.lease_until,
 updated_at = EXCLUDED.updated_at,
 status = CASE WHEN worker_recovery.revision <> EXCLUDED.revision THEN 'reconciling' ELSE worker_recovery.status END,
 failures = CASE WHEN worker_recovery.revision <> EXCLUDED.revision THEN 0 ELSE worker_recovery.failures END,
 pools = CASE WHEN worker_recovery.revision <> EXCLUDED.revision THEN '[]'::jsonb ELSE worker_recovery.pools END,
 next_attempt_at = CASE WHEN worker_recovery.revision <> EXCLUDED.revision THEN EXCLUDED.next_attempt_at ELSE worker_recovery.next_attempt_at END
WHERE worker_recovery.lease_until <= EXCLUDED.updated_at
RETURNING *;

-- name: CompleteWorkerRecovery :execrows
UPDATE worker_recovery SET status = sqlc.arg(status)::text, failures = sqlc.arg(failures)::integer,
 pools = sqlc.arg(pools)::jsonb,
 next_attempt_at = sqlc.arg(next_attempt_at)::timestamptz, updated_at = sqlc.arg(updated_at)::timestamptz,
 claim_token = NULL, lease_until = '1970-01-01'::timestamptz
WHERE app_id = sqlc.arg(app_id)::uuid AND claim_token = sqlc.arg(claim_token)::uuid
  AND revision = sqlc.arg(revision)::text
  AND lease_until > sqlc.arg(updated_at)::timestamptz;

-- name: WorkerRecoveryByApp :one
SELECT * FROM worker_recovery WHERE app_id = $1;

-- Aggregate only eligible apps owned by this scheduler, including candidates
-- that have never acquired a lease. Live leases and persisted retry deadlines
-- suppress overdue detection until they expire plus the discovery grace.
-- name: WorkerRecoverySummary :one
WITH eligible AS (
 SELECT a.id, r.revision, r.pools,
        greatest(coalesce(r.next_attempt_at, a.created_at), coalesce(r.lease_until, '1970-01-01'::timestamptz)) AS due_at
 FROM apps a JOIN accounts ac ON ac.id = a.account_id
 LEFT JOIN worker_recovery r ON r.app_id = a.id
 WHERE a.status = 'active'
  AND (a.manifest->>'execution_mode' = 'worker' OR a.workload_class = 'worker'
    OR EXISTS (SELECT 1 FROM deployments d
      JOIN project_environment_workload_deployment_specs p ON p.deployment_id = d.id
      JOIN project_environment_workload_specs s ON s.id = p.spec_id AND s.app_id = a.id
      WHERE d.app_id = a.id AND d.status = 'live'
        AND (s.settings->'manifest'->>'execution_mode' = 'worker' OR s.settings->>'workload_class' = 'worker'))
    OR EXISTS (SELECT 1 FROM instances i WHERE i.app_id = a.id AND i.mode = 'worker'
      AND i.state IN ('running', 'waking', 'cold_booting', 'draining', 'migrating', 'snapshotting', 'warm', 'frozen')))
  AND ac.status IN ('active', 'past_due') AND ac.abuse_hold_at IS NULL
  AND (sqlc.arg(owner_node_id)::text = '' OR a.node_id IS NULL OR a.node_id::text = sqlc.arg(owner_node_id)::text)

), pools AS (
 SELECT e.id, (p->>'desired')::bigint AS desired,
        (p->>'running')::bigint AS running, (p->>'starting')::bigint AS starting,
        (p->>'draining')::bigint AS draining
 FROM eligible e CROSS JOIN LATERAL jsonb_array_elements(e.pools) p
 WHERE p->>'revision' = e.revision
   AND (p->>'observed_at')::timestamptz >= sqlc.arg(fresh_after)::timestamptz
)
SELECT (SELECT count(*) FROM eligible)::bigint AS apps,
       (SELECT count(*) FROM eligible e
        WHERE coalesce(jsonb_array_length(e.pools), 0) = 0
           OR EXISTS (SELECT 1 FROM jsonb_array_elements(e.pools) p
                      WHERE p->>'revision' IS DISTINCT FROM e.revision
                         OR (p->>'observed_at')::timestamptz < sqlc.arg(fresh_after)::timestamptz))::bigint AS unobserved,
       (SELECT count(*) FROM eligible WHERE due_at < sqlc.arg(overdue_before)::timestamptz)::bigint AS overdue,
       coalesce(sum(desired), 0)::bigint AS desired,
       coalesce(sum(running), 0)::bigint AS running,
       coalesce(sum(starting), 0)::bigint AS starting,
       coalesce(sum(draining), 0)::bigint AS draining,
       coalesce(sum(greatest(desired - running, 0)), 0)::bigint AS missing
FROM pools;
