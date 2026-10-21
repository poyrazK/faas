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
INSERT INTO financial_compute_owners(node_id,incarnation,protocol,account_compute_incarnation,job_compute_incarnation,egress_budget_incarnation)
SELECT id,sqlc.arg(incarnation)::uuid,1,sqlc.narg(account_compute_incarnation)::uuid,sqlc.narg(job_compute_incarnation)::uuid,sqlc.narg(egress_budget_incarnation)::uuid FROM compute_nodes WHERE id=sqlc.arg(node_id)::uuid AND vmmd_incarnation=sqlc.arg(incarnation)::uuid AND active
ON CONFLICT(node_id) DO UPDATE SET incarnation=EXCLUDED.incarnation,observed_at=clock_timestamp(),
 account_compute_incarnation=COALESCE(EXCLUDED.account_compute_incarnation,financial_compute_owners.account_compute_incarnation),
 job_compute_incarnation=COALESCE(EXCLUDED.job_compute_incarnation,financial_compute_owners.job_compute_incarnation),
 egress_budget_incarnation=COALESCE(EXCLUDED.egress_budget_incarnation,financial_compute_owners.egress_budget_incarnation);

-- name: FinancialComputeOwnersReady :one
SELECT EXISTS(SELECT 1 FROM compute_nodes WHERE active) AND NOT EXISTS (
 SELECT 1 FROM compute_nodes n LEFT JOIN financial_compute_owners o ON o.node_id=n.id
 WHERE n.active AND (o.node_id IS NULL OR o.incarnation IS DISTINCT FROM n.vmmd_incarnation OR o.account_compute_incarnation IS DISTINCT FROM n.vmmd_incarnation OR o.job_compute_incarnation IS DISTINCT FROM n.vmmd_incarnation OR o.observed_at < clock_timestamp()-make_interval(secs=>sqlc.arg(max_age_seconds)::double precision))
) AS ready;

-- name: FinancialComputeOwnerValid :one
SELECT EXISTS(SELECT 1 FROM financial_compute_owners o JOIN compute_nodes n ON n.id=o.node_id
 WHERE o.node_id=sqlc.arg(node_id)::uuid AND o.incarnation=sqlc.arg(incarnation)::uuid AND n.vmmd_incarnation=o.incarnation AND o.account_compute_incarnation=o.incarnation AND o.job_compute_incarnation=o.incarnation AND n.active) AS valid;

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

-- ADR-1750, amendment 1: strict project caps.

-- name: FinancialComputeProjectBookGet :one
SELECT payload FROM financial_compute_project_books WHERE account_id=sqlc.arg(account_id)::uuid AND project_id=sqlc.arg(project_id)::uuid AND period_start=sqlc.arg(period_start)::timestamptz;

-- name: FinancialComputeProjectBookPut :exec
INSERT INTO financial_compute_project_books(account_id,project_id,period_start,period_end,payload)
VALUES(sqlc.arg(account_id)::uuid,sqlc.arg(project_id)::uuid,sqlc.arg(period_start)::timestamptz,sqlc.arg(period_end)::timestamptz,sqlc.arg(payload)::jsonb)
ON CONFLICT(account_id,project_id,period_start) DO UPDATE SET payload=EXCLUDED.payload;

-- An unsettled attempt belongs to a project's book when it funds it, or
-- when its app is now in the project.
-- name: FinancialComputeProjectLeases :many
SELECT l.payload FROM financial_compute_leases l LEFT JOIN apps a ON a.id=l.app_id
WHERE l.account_id=sqlc.arg(account_id)::uuid AND l.settled_at IS NULL
 AND (l.payload->>'project_id'=sqlc.arg(project_id)::uuid::text OR a.project_id=sqlc.arg(project_id)::uuid)
ORDER BY l.id LIMIT 10001;

-- name: FinancialComputeAppProject :one
SELECT COALESCE(project_id::text,'')::text AS project_id FROM apps WHERE account_id=sqlc.arg(account_id)::uuid AND id=sqlc.arg(app_id)::uuid;

-- name: FinancialComputeProjectManaged :one
SELECT EXISTS(SELECT 1 FROM apps a JOIN financial_compute_project_books b ON b.account_id=a.account_id AND b.project_id=a.project_id
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

-- name: FinancialComputeProjectSamplingReady :one
WITH since AS (SELECT greatest(created_at,sqlc.arg(period_start)::timestamptz) AS at FROM projects
 WHERE id=sqlc.arg(project_id)::uuid AND account_id=sqlc.arg(account_id)::uuid)
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
