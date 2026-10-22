-- name: FinancialComputeBookGet :one
SELECT payload FROM financial_compute_books WHERE account_id=sqlc.arg(account_id)::uuid AND app_id=sqlc.arg(app_id)::uuid AND period_start=sqlc.arg(period_start)::timestamptz;

-- name: FinancialComputeBookPut :exec
INSERT INTO financial_compute_books(account_id,app_id,period_start,period_end,payload)
VALUES(sqlc.arg(account_id)::uuid,sqlc.arg(app_id)::uuid,sqlc.arg(period_start)::timestamptz,sqlc.arg(period_end)::timestamptz,sqlc.arg(payload)::jsonb)
ON CONFLICT(account_id,app_id,period_start) DO UPDATE SET payload=EXCLUDED.payload;

-- name: FinancialComputeLeaseGet :one
SELECT payload FROM financial_compute_leases WHERE account_id=sqlc.arg(account_id)::uuid AND id=sqlc.arg(id)::uuid;

-- name: FinancialComputeLeasePut :execrows
INSERT INTO financial_compute_leases(id,account_id,app_id,job_id,job_run_id,job_task_index,instance_id,node_id,incarnation,period_start,strict,started_at,funded_until,settled_at,payload)
VALUES(sqlc.arg(id)::uuid,sqlc.arg(account_id)::uuid,sqlc.narg(app_id)::uuid,sqlc.narg(job_id)::uuid,sqlc.narg(job_run_id)::uuid,sqlc.narg(job_task_index)::integer,sqlc.arg(instance_id)::text,sqlc.arg(node_id)::uuid,sqlc.arg(incarnation)::uuid,sqlc.arg(period_start)::timestamptz,sqlc.arg(strict)::boolean,sqlc.arg(started_at)::timestamptz,sqlc.arg(funded_until)::timestamptz,sqlc.narg(settled_at)::timestamptz,sqlc.arg(payload)::jsonb)
ON CONFLICT(id) DO UPDATE SET funded_until=EXCLUDED.funded_until,settled_at=EXCLUDED.settled_at,payload=EXCLUDED.payload
WHERE financial_compute_leases.account_id=EXCLUDED.account_id AND financial_compute_leases.app_id IS NOT DISTINCT FROM EXCLUDED.app_id
 AND financial_compute_leases.job_id IS NOT DISTINCT FROM EXCLUDED.job_id
 AND financial_compute_leases.job_run_id IS NOT DISTINCT FROM EXCLUDED.job_run_id
 AND financial_compute_leases.job_task_index IS NOT DISTINCT FROM EXCLUDED.job_task_index
 AND financial_compute_leases.instance_id=EXCLUDED.instance_id AND financial_compute_leases.node_id=EXCLUDED.node_id
 AND financial_compute_leases.incarnation=EXCLUDED.incarnation AND financial_compute_leases.payload->>'fence'=EXCLUDED.payload->>'fence';

-- name: FinancialComputeAppLeases :many
SELECT payload FROM financial_compute_leases WHERE account_id=sqlc.arg(account_id)::uuid AND app_id=sqlc.arg(app_id)::uuid AND settled_at IS NULL ORDER BY id LIMIT 10001;

-- name: FinancialComputeNodeLeases :many
SELECT payload FROM financial_compute_leases WHERE node_id=sqlc.arg(node_id)::uuid AND settled_at IS NULL ORDER BY id LIMIT 10001;

-- Only overlapping physical attempts plus the last prior attempt are needed
-- to bound stale residency. Historical wake count must not grow this read.
-- name: FinancialComputeBillingLeases :many
WITH relevant AS (
 SELECT payload FROM financial_compute_leases
 WHERE account_id=sqlc.arg(account_id)::uuid AND (app_id=sqlc.narg(app_id)::uuid OR job_id=sqlc.narg(job_id)::uuid) AND instance_id=sqlc.arg(instance_id)::text
 AND started_at<sqlc.arg(period_end)::timestamptz
 AND (settled_at IS NULL OR settled_at>sqlc.arg(period_start)::timestamptz)
 LIMIT 10001
), previous AS (
 SELECT payload FROM financial_compute_leases
 WHERE account_id=sqlc.arg(account_id)::uuid AND (app_id=sqlc.narg(app_id)::uuid OR job_id=sqlc.narg(job_id)::uuid) AND instance_id=sqlc.arg(instance_id)::text
 AND started_at<sqlc.arg(period_start)::timestamptz
 ORDER BY started_at DESC,(payload->>'granted_at')::timestamptz DESC LIMIT 1
)
SELECT payload FROM relevant UNION SELECT payload FROM previous;

-- name: FinancialComputeLatestLease :one
SELECT payload FROM financial_compute_leases
 WHERE account_id=sqlc.arg(account_id)::uuid AND (app_id=sqlc.narg(app_id)::uuid OR job_id=sqlc.narg(job_id)::uuid) AND instance_id=sqlc.arg(instance_id)::text
 ORDER BY started_at DESC,(payload->>'granted_at')::timestamptz DESC LIMIT 1;

-- name: FinancialComputeAppManaged :one
SELECT EXISTS(SELECT 1 FROM financial_compute_books WHERE account_id=sqlc.arg(account_id)::uuid AND app_id=sqlc.arg(app_id)::uuid)
 OR EXISTS(SELECT 1 FROM financial_compute_job_books WHERE account_id=sqlc.arg(account_id)::uuid AND job_id=sqlc.narg(job_id)::uuid)
 OR EXISTS(SELECT 1 FROM financial_compute_account_books WHERE account_id=sqlc.arg(account_id)::uuid) AS managed;

-- name: FinancialComputeRetired :one
SELECT COALESCE((SELECT settled_at IS NOT NULL FROM financial_compute_leases
 WHERE node_id=sqlc.arg(node_id)::uuid AND instance_id=sqlc.arg(instance_id)::text
 ORDER BY started_at DESC,(payload->>'granted_at')::timestamptz DESC LIMIT 1),false)::boolean AS rejected;

-- name: FinancialComputeHolds :many
SELECT policy_id, revision, period_start FROM financial_compute_holds WHERE account_id=sqlc.arg(account_id)::uuid;

-- name: FinancialComputeHoldPut :exec
INSERT INTO financial_compute_holds(account_id,policy_id,revision,period_start) VALUES(sqlc.arg(account_id)::uuid,sqlc.arg(policy_id)::uuid,sqlc.arg(revision)::bigint,sqlc.arg(period_start)::timestamptz) ON CONFLICT DO NOTHING;

-- name: FinancialComputeOwnerPut :exec
INSERT INTO financial_compute_owners(node_id,incarnation,protocol,account_compute_incarnation,job_compute_incarnation,egress_budget_incarnation,scope_compute_incarnation)
SELECT id,sqlc.arg(incarnation)::uuid,1,sqlc.narg(account_compute_incarnation)::uuid,sqlc.narg(job_compute_incarnation)::uuid,sqlc.narg(egress_budget_incarnation)::uuid,sqlc.narg(scope_compute_incarnation)::uuid FROM compute_nodes WHERE id=sqlc.arg(node_id)::uuid AND vmmd_incarnation=sqlc.arg(incarnation)::uuid AND active
ON CONFLICT(node_id) DO UPDATE SET incarnation=EXCLUDED.incarnation,observed_at=clock_timestamp(),
 account_compute_incarnation=COALESCE(EXCLUDED.account_compute_incarnation,financial_compute_owners.account_compute_incarnation),
 job_compute_incarnation=COALESCE(EXCLUDED.job_compute_incarnation,financial_compute_owners.job_compute_incarnation),
 egress_budget_incarnation=COALESCE(EXCLUDED.egress_budget_incarnation,financial_compute_owners.egress_budget_incarnation),
 scope_compute_incarnation=COALESCE(EXCLUDED.scope_compute_incarnation,financial_compute_owners.scope_compute_incarnation);

-- name: FinancialComputeOwnersReady :one
SELECT EXISTS(SELECT 1 FROM compute_nodes WHERE active) AND NOT EXISTS (
 SELECT 1 FROM compute_nodes n LEFT JOIN financial_compute_owners o ON o.node_id=n.id
 WHERE n.active AND (o.node_id IS NULL OR o.incarnation IS DISTINCT FROM n.vmmd_incarnation OR o.account_compute_incarnation IS DISTINCT FROM n.vmmd_incarnation OR o.job_compute_incarnation IS DISTINCT FROM n.vmmd_incarnation OR o.scope_compute_incarnation IS DISTINCT FROM n.vmmd_incarnation OR o.observed_at < clock_timestamp()-make_interval(secs=>sqlc.arg(max_age_seconds)::double precision))
) AS ready;

-- name: FinancialComputeOwnerValid :one
SELECT EXISTS(SELECT 1 FROM financial_compute_owners o JOIN compute_nodes n ON n.id=o.node_id
 WHERE o.node_id=sqlc.arg(node_id)::uuid AND o.incarnation=sqlc.arg(incarnation)::uuid AND n.vmmd_incarnation=o.incarnation AND o.account_compute_incarnation=o.incarnation AND o.job_compute_incarnation=o.incarnation AND o.scope_compute_incarnation=o.incarnation AND n.active) AS valid;

-- name: FinancialComputeClock :one
SELECT clock_timestamp()::timestamptz AS now;

-- name: FinancialComputeActivationSafe :one
SELECT EXISTS(SELECT 1 FROM apps a WHERE a.id=sqlc.arg(app_id)::uuid AND a.account_id=sqlc.arg(account_id)::uuid
 AND a.status!='deleted'
 AND NOT EXISTS(SELECT 1 FROM instances i WHERE i.app_id=a.id AND i.state NOT IN ('stopped','parked','failed'))
 AND NOT EXISTS(SELECT 1 FROM app_tasks t WHERE t.app_id=a.id AND t.status IN ('restoring','running'))
 AND NOT EXISTS(SELECT 1 FROM financial_compute_leases l WHERE l.app_id=a.id AND l.settled_at IS NULL)
 AND NOT EXISTS(SELECT 1 FROM instances i JOIN instance_billing_intervals b ON b.instance_id=i.id WHERE i.app_id=a.id AND (b.ended_at IS NULL OR b.ended_at > date_trunc('minute',clock_timestamp())-interval '1 minute')))
 AS safe;

-- name: FinancialComputeSamplingReady :one
WITH since AS (SELECT greatest(created_at,sqlc.arg(period_start)::timestamptz) AS at FROM apps
 WHERE id=sqlc.arg(app_id)::uuid AND account_id=sqlc.arg(account_id)::uuid)
SELECT (SELECT retained_from <= since.at FROM financial_evidence_coverage WHERE singleton)
 AND (SELECT count(*) FROM financial_sampling_windows WHERE minute >= date_trunc('minute',since.at)
   AND minute < date_trunc('minute',clock_timestamp()) AND compute_complete)
   = extract(epoch FROM date_trunc('minute',clock_timestamp())-date_trunc('minute',since.at))::bigint/60 AS ready FROM since;

-- name: FinancialComputeAppOwned :one
SELECT EXISTS(SELECT 1 FROM apps WHERE account_id=sqlc.arg(account_id)::uuid AND id=sqlc.arg(app_id)::uuid AND status!='deleted') AS owned;

-- Positive retained charges seed admission conservatively. Corrections cannot
-- grant execution authority, and missing contracts must fail activation.
-- name: FinancialComputeBaseline :one
SELECT COALESCE(sum(CASE WHEN p.price IS NULL OR (p.price->>'unit_quantity')::numeric<=0 THEN 0 ELSE
 ceil(e.quantity::numeric*(p.price->>'millicents_per_unit')::numeric/(p.price->>'unit_quantity')::numeric)
 + CASE WHEN (p.price->>'millicents_per_unit')::numeric>0 THEN 1 ELSE 0 END END),0)::text AS amount,
 COALESCE(bool_or(p.price IS NULL OR p.price->>'meter'!='compute' OR p.price->>'currency'!='USD'
 OR p.price->>'unit'!='mb_seconds' OR (p.price->>'unit_quantity')::numeric<=0),false)::boolean AS incomplete
FROM financial_usage_evidence e LEFT JOIN financial_price_snapshots p
 ON p.account_id=e.account_id AND p.version=e.price_version AND p.meter=e.meter AND p.period_start=sqlc.arg(period_start)::timestamptz
WHERE e.account_id=sqlc.arg(account_id)::uuid AND e.attribution->>'app_id'=sqlc.arg(app_id)::text AND e.meter='compute'
 AND e.source_start>=sqlc.arg(period_start)::timestamptz AND e.source_start<sqlc.arg(period_end)::timestamptz AND e.quantity>0;

-- name: FinancialComputeAccountBookGet :one
SELECT payload FROM financial_compute_account_books WHERE account_id=sqlc.arg(account_id)::uuid AND period_start=sqlc.arg(period_start)::timestamptz;

-- name: FinancialComputeAccountBookPut :exec
INSERT INTO financial_compute_account_books(account_id,period_start,period_end,payload)
VALUES(sqlc.arg(account_id)::uuid,sqlc.arg(period_start)::timestamptz,sqlc.arg(period_end)::timestamptz,sqlc.arg(payload)::jsonb)
ON CONFLICT(account_id,period_start) DO UPDATE SET payload=EXCLUDED.payload;

-- name: FinancialComputeAccountLeases :many
SELECT payload FROM financial_compute_leases WHERE account_id=sqlc.arg(account_id)::uuid AND settled_at IS NULL ORDER BY id LIMIT 10001;

-- All account compute, including deleted apps and former jobs, consumes the
-- same quantity allowance. Missing or above-ceiling contracts fail closed.
-- name: FinancialComputeAccountBaseline :one
SELECT COALESCE(sum(e.quantity::numeric),0)::text AS quantity,
 COALESCE(bool_or(p.price IS NULL OR p.price->>'meter'!='compute' OR p.price->>'currency'!='USD'
 OR p.price->>'unit'!='mb_seconds' OR (p.price->>'unit_quantity')::numeric<=0
 OR (p.price->>'millicents_per_unit')::numeric<0
 OR (p.price->>'millicents_per_unit')::numeric*sqlc.arg(upper_quantity)::bigint::numeric > sqlc.arg(upper_rate)::bigint::numeric*(p.price->>'unit_quantity')::numeric),false)::boolean AS incomplete
FROM financial_usage_evidence e LEFT JOIN financial_price_snapshots p
 ON p.account_id=e.account_id AND p.version=e.price_version AND p.meter=e.meter AND p.period_start=sqlc.arg(period_start)::timestamptz
WHERE e.account_id=sqlc.arg(account_id)::uuid AND e.meter='compute' AND e.quantity>0
 AND e.source_start>=sqlc.arg(period_start)::timestamptz AND e.source_start<sqlc.arg(period_end)::timestamptz;

-- name: FinancialComputeAccountActivationSafe :one
SELECT NOT EXISTS(SELECT 1 FROM financial_compute_leases WHERE account_id=sqlc.arg(account_id)::uuid AND settled_at IS NULL)
 AND NOT EXISTS(SELECT 1 FROM job_tasks t JOIN job_runs r ON r.id=t.run_id WHERE r.account_id=sqlc.arg(account_id)::uuid AND t.status='claimed')
 AND NOT EXISTS(SELECT 1 FROM instances i LEFT JOIN apps a ON a.id=i.app_id LEFT JOIN jobs j ON j.id=i.job_id
 WHERE (a.account_id=sqlc.arg(account_id)::uuid OR j.account_id=sqlc.arg(account_id)::uuid)
 AND i.state NOT IN ('stopped','parked','failed'))
 AND NOT EXISTS(SELECT 1 FROM app_tasks t JOIN apps a ON a.id=t.app_id WHERE a.account_id=sqlc.arg(account_id)::uuid AND t.status IN ('restoring','running'))
 AND NOT EXISTS(SELECT 1 FROM instance_billing_intervals b JOIN instances i ON i.id=b.instance_id
 LEFT JOIN apps a ON a.id=i.app_id LEFT JOIN jobs j ON j.id=i.job_id
 WHERE (a.account_id=sqlc.arg(account_id)::uuid OR j.account_id=sqlc.arg(account_id)::uuid)
 AND (b.ended_at IS NULL OR b.ended_at>date_trunc('minute',clock_timestamp())-interval '1 minute')) AS safe;

-- name: FinancialComputeAccountSamplingReady :one
WITH since AS (SELECT greatest(created_at,sqlc.arg(period_start)::timestamptz) AS at FROM accounts WHERE id=sqlc.arg(account_id)::uuid)
SELECT (SELECT retained_from <= since.at FROM financial_evidence_coverage WHERE singleton)
 AND (SELECT count(*) FROM financial_sampling_windows WHERE minute>=date_trunc('minute',since.at)
 AND minute<date_trunc('minute',clock_timestamp()) AND compute_complete)
 =extract(epoch FROM date_trunc('minute',clock_timestamp())-date_trunc('minute',since.at))::bigint/60 AS ready FROM since;

-- name: FinancialComputeAccountStrictEnabled :one
SELECT EXISTS(SELECT 1 FROM financial_budget_policies WHERE account_id=sqlc.arg(account_id)::uuid
 AND deleted_at IS NULL AND spec->>'enabled'='true' AND spec->>'mode'='strict' AND spec->'scope'->>'kind'='account') AS enabled;

-- ADR-1750, amendments 1 and 2: strict project and environment caps.

-- name: FinancialComputeScopeBookGet :one
SELECT payload FROM financial_compute_scope_books WHERE account_id=sqlc.arg(account_id)::uuid AND scope_kind=sqlc.arg(scope_kind)::text
 AND scope_id=sqlc.arg(scope_id)::uuid AND period_start=sqlc.arg(period_start)::timestamptz;

-- name: FinancialComputeScopeBookPut :exec
INSERT INTO financial_compute_scope_books(account_id,scope_kind,scope_id,period_start,period_end,payload)
VALUES(sqlc.arg(account_id)::uuid,sqlc.arg(scope_kind)::text,sqlc.arg(scope_id)::uuid,sqlc.arg(period_start)::timestamptz,sqlc.arg(period_end)::timestamptz,sqlc.arg(payload)::jsonb)
ON CONFLICT(account_id,scope_kind,scope_id,period_start) DO UPDATE SET payload=EXCLUDED.payload;

-- An unsettled attempt belongs to a project's book when it funds it, or
-- when its app is now in the project.
-- name: FinancialComputeProjectLeases :many
SELECT l.payload FROM financial_compute_leases l LEFT JOIN apps a ON a.id=l.app_id
WHERE l.account_id=sqlc.arg(account_id)::uuid AND l.settled_at IS NULL
 AND (l.payload->>'project_id'=sqlc.arg(project_id)::uuid::text OR a.project_id=sqlc.arg(project_id)::uuid)
ORDER BY l.id LIMIT 10001;

-- An unsettled app attempt belongs to an environment's book when it funds
-- it, or when its deployment now runs in the environment. Attempts acquired
-- before deployments were recorded resolve through their instance.
-- name: FinancialComputeEnvironmentLeases :many
SELECT l.payload FROM financial_compute_leases l
LEFT JOIN instances i ON i.id=CASE WHEN l.instance_id ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$' THEN l.instance_id::uuid END
WHERE l.account_id=sqlc.arg(account_id)::uuid AND l.settled_at IS NULL AND l.app_id IS NOT NULL
 AND (l.payload->>'environment_id'=sqlc.arg(environment_id)::uuid::text
  OR financial_compute_deployment_environment(COALESCE(i.deployment_id,NULLIF(l.payload->>'deployment_id','')::uuid))=sqlc.arg(environment_id)::uuid)
ORDER BY l.id LIMIT 10001;

-- The scopes an app attempt funds: its current project, and the environment
-- of its deployment. The instance's recorded deployment wins over the
-- request's; an app outside any project has neither.
-- name: FinancialComputeAppScopes :one
SELECT COALESCE(a.project_id::text,'')::text AS project_id, COALESCE(d.id::text,'')::text AS deployment_id,
 COALESCE(financial_compute_deployment_environment(d.id)::text,'')::text AS environment_id
FROM apps a
LEFT JOIN instances i ON i.id=sqlc.narg(instance_id)::uuid AND i.app_id=a.id
LEFT JOIN deployments d ON d.app_id=a.id AND d.id=COALESCE(i.deployment_id,sqlc.narg(deployment_id)::uuid)
WHERE a.account_id=sqlc.arg(account_id)::uuid AND a.id=sqlc.arg(app_id)::uuid;

-- name: FinancialComputeProjectEnvironments :many
SELECT id::text AS id FROM project_environments WHERE project_id=sqlc.arg(project_id)::uuid AND account_id=sqlc.arg(account_id)::uuid ORDER BY id;

-- name: FinancialComputeDeploymentEnvironment :one
SELECT COALESCE(financial_compute_deployment_environment(d.id)::text,'')::text AS environment_id
FROM deployments d JOIN apps a ON a.id=d.app_id
WHERE a.account_id=sqlc.arg(account_id)::uuid AND a.id=sqlc.arg(app_id)::uuid AND d.id=sqlc.arg(deployment_id)::uuid;

-- An app is managed while its project, or an environment of its project,
-- has a strict book: the meter then bills its residency through funding.
-- Floor waivers are FinancialComputeFloorCovered's, which ignores
-- environments since a floor has none.
-- name: FinancialComputeProjectManaged :one
SELECT EXISTS(SELECT 1 FROM apps a JOIN financial_compute_scope_books b ON b.account_id=a.account_id
 AND (b.scope_kind='project' AND b.scope_id=a.project_id
  OR b.scope_kind='environment' AND b.scope_id IN (SELECT e.id FROM project_environments e WHERE e.project_id=a.project_id))
 WHERE a.account_id=sqlc.arg(account_id)::uuid AND a.id=sqlc.arg(app_id)::uuid) AS managed;

-- name: FinancialComputeProjectActivationSafe :one
SELECT EXISTS(SELECT 1 FROM projects WHERE id=sqlc.arg(project_id)::uuid AND account_id=sqlc.arg(account_id)::uuid)
 AND NOT EXISTS(SELECT 1 FROM instances i JOIN apps a ON a.id=i.app_id WHERE a.project_id=sqlc.arg(project_id)::uuid AND i.state NOT IN ('stopped','parked','failed'))
 AND NOT EXISTS(SELECT 1 FROM app_tasks t JOIN apps a ON a.id=t.app_id WHERE a.project_id=sqlc.arg(project_id)::uuid AND t.status IN ('restoring','running'))
 AND NOT EXISTS(SELECT 1 FROM financial_compute_leases l LEFT JOIN apps a ON a.id=l.app_id WHERE l.account_id=sqlc.arg(account_id)::uuid AND l.settled_at IS NULL
   AND (a.project_id=sqlc.arg(project_id)::uuid OR l.payload->>'project_id'=sqlc.arg(project_id)::uuid::text))
 AND NOT EXISTS(SELECT 1 FROM instances i JOIN apps a ON a.id=i.app_id JOIN instance_billing_intervals b ON b.instance_id=i.id
   WHERE a.project_id=sqlc.arg(project_id)::uuid AND (b.ended_at IS NULL OR b.ended_at > date_trunc('minute',clock_timestamp())-interval '1 minute'))
 AS safe;

-- A quiet environment: no running instance, task, unsettled attempt or open
-- billing interval of a deployment in it, nor of a project app instance whose
-- deployment is unknown.
-- name: FinancialComputeEnvironmentActivationSafe :one
WITH env AS (SELECT id, project_id FROM project_environments WHERE id=sqlc.arg(environment_id)::uuid AND account_id=sqlc.arg(account_id)::uuid)
SELECT EXISTS(SELECT 1 FROM env)
 AND NOT EXISTS(SELECT 1 FROM instances i JOIN apps a ON a.id=i.app_id JOIN env ON env.project_id=a.project_id
   WHERE i.state NOT IN ('stopped','parked','failed') AND (i.deployment_id IS NULL OR financial_compute_deployment_environment(i.deployment_id)=env.id))
 AND NOT EXISTS(SELECT 1 FROM app_tasks t JOIN apps a ON a.id=t.app_id JOIN env ON env.project_id=a.project_id
   WHERE t.status IN ('restoring','running') AND financial_compute_deployment_environment(t.deployment_id)=env.id)
 AND NOT EXISTS(SELECT 1 FROM financial_compute_leases l
   LEFT JOIN instances i ON i.id=CASE WHEN l.instance_id ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$' THEN l.instance_id::uuid END
   WHERE l.account_id=sqlc.arg(account_id)::uuid AND l.settled_at IS NULL AND l.app_id IS NOT NULL
   AND (l.payload->>'environment_id'=sqlc.arg(environment_id)::uuid::text
    OR financial_compute_deployment_environment(COALESCE(i.deployment_id,NULLIF(l.payload->>'deployment_id','')::uuid))=sqlc.arg(environment_id)::uuid))
 AND NOT EXISTS(SELECT 1 FROM instances i JOIN apps a ON a.id=i.app_id JOIN env ON env.project_id=a.project_id JOIN instance_billing_intervals b ON b.instance_id=i.id
   WHERE (i.deployment_id IS NULL OR financial_compute_deployment_environment(i.deployment_id)=env.id)
   AND (b.ended_at IS NULL OR b.ended_at > date_trunc('minute',clock_timestamp())-interval '1 minute'))
 AS safe;

-- name: FinancialComputeProjectSamplingReady :one
WITH since AS (SELECT greatest(created_at,sqlc.arg(period_start)::timestamptz) AS at FROM projects
 WHERE id=sqlc.arg(project_id)::uuid AND account_id=sqlc.arg(account_id)::uuid)
SELECT (SELECT retained_from <= since.at FROM financial_evidence_coverage WHERE singleton)
 AND (SELECT count(*) FROM financial_sampling_windows WHERE minute >= date_trunc('minute',since.at)
   AND minute < date_trunc('minute',clock_timestamp()) AND compute_complete)
   = extract(epoch FROM date_trunc('minute',clock_timestamp())-date_trunc('minute',since.at))::bigint/60 AS ready FROM since;

-- Sampling must be complete from the earliest of the environment's and its
-- project apps' creation, or from the month start.
-- name: FinancialComputeEnvironmentSamplingReady :one
WITH since AS (SELECT greatest(LEAST(e.created_at,COALESCE((SELECT min(a.created_at) FROM apps a WHERE a.project_id=e.project_id),e.created_at)),
  sqlc.arg(period_start)::timestamptz) AS at FROM project_environments e
 WHERE e.id=sqlc.arg(environment_id)::uuid AND e.account_id=sqlc.arg(account_id)::uuid)
SELECT (SELECT retained_from <= since.at FROM financial_evidence_coverage WHERE singleton)
 AND (SELECT count(*) FROM financial_sampling_windows WHERE minute >= date_trunc('minute',since.at)
   AND minute < date_trunc('minute',clock_timestamp()) AND compute_complete)
   = extract(epoch FROM date_trunc('minute',clock_timestamp())-date_trunc('minute',since.at))::bigint/60 AS ready FROM since;

-- Positive retained charges of the project's apps seed the project book,
-- exactly as FinancialComputeBaseline seeds one app's.
-- name: FinancialComputeProjectBaseline :one
SELECT COALESCE(sum(CASE WHEN p.price IS NULL OR (p.price->>'unit_quantity')::numeric<=0 THEN 0 ELSE
 ceil(e.quantity::numeric*(p.price->>'millicents_per_unit')::numeric/(p.price->>'unit_quantity')::numeric)
 + CASE WHEN (p.price->>'millicents_per_unit')::numeric>0 THEN 1 ELSE 0 END END),0)::text AS amount,
 COALESCE(bool_or(p.price IS NULL OR p.price->>'meter'!='compute' OR p.price->>'currency'!='USD'
 OR p.price->>'unit'!='mb_seconds' OR (p.price->>'unit_quantity')::numeric<=0),false)::boolean AS incomplete
FROM financial_usage_evidence e LEFT JOIN financial_price_snapshots p
 ON p.account_id=e.account_id AND p.version=e.price_version AND p.meter=e.meter AND p.period_start=sqlc.arg(period_start)::timestamptz
WHERE e.account_id=sqlc.arg(account_id)::uuid AND e.attribution->>'project_id'=sqlc.arg(project_id)::text AND e.meter='compute'
 AND e.source_start>=sqlc.arg(period_start)::timestamptz AND e.source_start<sqlc.arg(period_end)::timestamptz AND e.quantity>0;

-- Charges recorded in the environment, or of a deployment now in it, seed
-- the environment book. Synthetic capacity floors carry no deployment.
-- name: FinancialComputeEnvironmentBaseline :one
SELECT COALESCE(sum(CASE WHEN p.price IS NULL OR (p.price->>'unit_quantity')::numeric<=0 THEN 0 ELSE
 ceil(e.quantity::numeric*(p.price->>'millicents_per_unit')::numeric/(p.price->>'unit_quantity')::numeric)
 + CASE WHEN (p.price->>'millicents_per_unit')::numeric>0 THEN 1 ELSE 0 END END),0)::text AS amount,
 COALESCE(bool_or(p.price IS NULL OR p.price->>'meter'!='compute' OR p.price->>'currency'!='USD'
 OR p.price->>'unit'!='mb_seconds' OR (p.price->>'unit_quantity')::numeric<=0),false)::boolean AS incomplete
FROM financial_usage_evidence e LEFT JOIN financial_price_snapshots p
 ON p.account_id=e.account_id AND p.version=e.price_version AND p.meter=e.meter AND p.period_start=sqlc.arg(period_start)::timestamptz
WHERE e.account_id=sqlc.arg(account_id)::uuid AND e.meter='compute'
 AND (e.attribution->>'environment_id'=sqlc.arg(environment_id)::uuid::text
  OR financial_compute_deployment_environment(NULLIF(e.attribution->>'deployment_id','')::uuid)=sqlc.arg(environment_id)::uuid)
 AND e.source_start>=sqlc.arg(period_start)::timestamptz AND e.source_start<sqlc.arg(period_end)::timestamptz AND e.quantity>0;
