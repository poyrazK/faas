-- name: FinancialComputeJobBookGet :one
SELECT payload FROM financial_compute_job_books WHERE account_id=sqlc.arg(account_id)::uuid AND job_id=sqlc.arg(job_id)::uuid AND period_start=sqlc.arg(period_start)::timestamptz;

-- name: FinancialComputeJobBookPut :exec
INSERT INTO financial_compute_job_books(account_id,job_id,period_start,period_end,payload)
VALUES(sqlc.arg(account_id)::uuid,sqlc.arg(job_id)::uuid,sqlc.arg(period_start)::timestamptz,sqlc.arg(period_end)::timestamptz,sqlc.arg(payload)::jsonb)
ON CONFLICT(account_id,job_id,period_start) DO UPDATE SET payload=EXCLUDED.payload;

-- name: FinancialComputeJobLeases :many
SELECT payload FROM financial_compute_leases WHERE account_id=sqlc.arg(account_id)::uuid AND job_id=sqlc.arg(job_id)::uuid AND settled_at IS NULL ORDER BY id LIMIT 10001;

-- name: FinancialComputeJobAttemptGet :one
SELECT * FROM financial_compute_job_attempts WHERE instance_id=sqlc.arg(instance_id)::uuid;

-- A denied boot consumed no task execution. The scheduler calls this only
-- after the native owner joins cancellation/absence for this exact instance.
-- name: FinancialComputeJobClaimDefer :execrows
UPDATE job_tasks SET status='queued',instance_id=NULL,lease_token=NULL,lease_expires_at=NULL,
 last_lease_node=NULL,started_at=NULL,next_attempt_at=sqlc.arg(next_attempt_at)::timestamptz,
 error_class=NULL,error_message='Waiting for strict compute budget funding'
WHERE run_id=sqlc.arg(run_id)::uuid AND task_index=sqlc.arg(task_index)::integer
 AND status='claimed' AND instance_id=sqlc.arg(instance_id)::uuid AND lease_token=sqlc.arg(lease_token)::uuid;

-- Current task projection must still name the immutable physical claim. A
-- cancelled run, retry, soft deletion or instance retirement revokes new work.
-- name: FinancialComputeJobAttemptCurrent :one
SELECT EXISTS(SELECT 1 FROM financial_compute_job_attempts a
 JOIN job_tasks t ON t.run_id=a.run_id AND t.task_index=a.task_index
 JOIN job_runs r ON r.id=a.run_id AND r.job_id=a.job_id AND r.account_id=a.account_id
 JOIN jobs j ON j.id=a.job_id AND j.account_id=a.account_id
 JOIN instances i ON i.id=a.instance_id
 WHERE a.instance_id=sqlc.arg(instance_id)::uuid AND a.account_id=sqlc.arg(account_id)::uuid
 AND t.status='claimed' AND t.instance_id=a.instance_id AND t.lease_token=a.lease_token
 AND t.lease_expires_at>clock_timestamp() AND r.aggregate_status IN ('queued','running') AND j.status='active'
 AND i.job_id=a.job_id AND i.node_id=a.node_id AND i.ram_mb=a.ram_mb
 AND upper(i.state) IN ('WAKING','COLD_BOOTING','RUNNING')) AS current;

-- name: FinancialComputeJobActivationSafe :one
SELECT EXISTS(SELECT 1 FROM jobs j WHERE j.account_id=sqlc.arg(account_id)::uuid AND j.id=sqlc.arg(job_id)::uuid AND j.status!='deleted')
 AND NOT EXISTS(SELECT 1 FROM instances WHERE job_id=sqlc.arg(job_id)::uuid AND state NOT IN ('stopped','parked','failed'))
 AND NOT EXISTS(SELECT 1 FROM job_tasks t JOIN job_runs r ON r.id=t.run_id WHERE r.job_id=sqlc.arg(job_id)::uuid AND t.status='claimed')
 AND NOT EXISTS(SELECT 1 FROM financial_compute_leases WHERE job_id=sqlc.arg(job_id)::uuid AND settled_at IS NULL)
 AND NOT EXISTS(SELECT 1 FROM instance_billing_intervals b JOIN instances i ON i.id=b.instance_id
 WHERE i.job_id=sqlc.arg(job_id)::uuid AND (b.ended_at IS NULL OR b.ended_at>date_trunc('minute',clock_timestamp())-interval '1 minute')) AS safe;

-- name: FinancialComputeJobSamplingReady :one
WITH since AS (SELECT greatest(created_at,sqlc.arg(period_start)::timestamptz) AS at FROM jobs
 WHERE id=sqlc.arg(job_id)::uuid AND account_id=sqlc.arg(account_id)::uuid)
SELECT (SELECT retained_from<=since.at FROM financial_evidence_coverage WHERE singleton)
 AND (SELECT count(*) FROM financial_sampling_windows WHERE minute>=date_trunc('minute',since.at)
 AND minute<date_trunc('minute',clock_timestamp()) AND compute_complete)
 =extract(epoch FROM date_trunc('minute',clock_timestamp())-date_trunc('minute',since.at))::bigint/60 AS ready FROM since;

-- name: FinancialComputeJobBaseline :one
SELECT COALESCE(sum(CASE WHEN p.price IS NULL OR (p.price->>'unit_quantity')::numeric<=0 THEN 0 ELSE
 ceil(e.quantity::numeric*(p.price->>'millicents_per_unit')::numeric/(p.price->>'unit_quantity')::numeric)
 + CASE WHEN (p.price->>'millicents_per_unit')::numeric>0 THEN 1 ELSE 0 END END),0)::text AS amount,
 COALESCE(bool_or(p.price IS NULL OR p.price->>'meter'!='compute' OR p.price->>'currency'!='USD'
 OR p.price->>'unit'!='mb_seconds' OR (p.price->>'unit_quantity')::numeric<=0),false)::boolean AS incomplete
FROM financial_usage_evidence e LEFT JOIN financial_price_snapshots p
 ON p.account_id=e.account_id AND p.version=e.price_version AND p.meter=e.meter AND p.period_start=sqlc.arg(period_start)::timestamptz
WHERE e.account_id=sqlc.arg(account_id)::uuid AND e.attribution->>'job_id'=sqlc.arg(job_id)::text AND e.meter='compute'
 AND e.source_start>=sqlc.arg(period_start)::timestamptz AND e.source_start<sqlc.arg(period_end)::timestamptz AND e.quantity>0;
