-- ADR-566: immutable retained evidence, bounded snapshot paging, and prices.
-- name: DeletedAppIDsInBillingWindow :many
SELECT app.id FROM apps app
WHERE app.status = 'deleted' AND EXISTS (
  SELECT 1 FROM instances instance
  JOIN instance_billing_intervals residency ON residency.instance_id = instance.id
  WHERE instance.app_id = app.id
    AND residency.started_at < sqlc.arg(window_end)::timestamptz
    AND COALESCE(residency.ended_at, sqlc.arg(window_end)::timestamptz) > sqlc.arg(window_start)::timestamptz
)
ORDER BY app.id;

-- name: JobInstancesInBillingWindow :many
SELECT instance.id, instance.state, instance.ram_mb, job.id AS job_id, job.account_id
FROM instances instance
JOIN jobs job ON job.id = instance.job_id
WHERE instance.kind = 'job_task'
  AND EXISTS (
    SELECT 1 FROM instance_billing_intervals residency
    WHERE residency.instance_id = instance.id
      AND residency.started_at < sqlc.arg(window_end)::timestamptz
      AND COALESCE(residency.ended_at, sqlc.arg(window_end)::timestamptz) > sqlc.arg(window_start)::timestamptz
  )
ORDER BY instance.started_at NULLS LAST, instance.id;

-- name: FinancialUsageEvidenceList :many
SELECT id, account_id, instance_id, source_id, meter, unit, quantity,
       source_start, source_end, plan, attribution, observed_at, price_version,
       corrects_source_id, adjustment_actor, adjustment_reason
FROM financial_usage_evidence
WHERE account_id = sqlc.arg(account_id)::uuid
  AND source_start >= sqlc.arg(period_start)::timestamptz
  AND source_start < sqlc.arg(period_end)::timestamptz
  AND id > sqlc.arg(after_id)::bigint
  AND id <= sqlc.arg(through_id)::bigint
ORDER BY id LIMIT sqlc.arg(page_size)::integer;

-- name: FinancialEvidenceHead :one
SELECT COALESCE(MAX(id), 0)::bigint AS through_id
FROM financial_usage_evidence
WHERE account_id = sqlc.arg(account_id)::uuid
  AND source_start >= sqlc.arg(period_start)::timestamptz
  AND source_start < sqlc.arg(period_end)::timestamptz;

-- name: FinancialEvidenceCoverage :one
SELECT retained_from FROM financial_evidence_coverage WHERE singleton;

-- name: FinancialPriceSnapshotPut :one
INSERT INTO financial_price_snapshots(account_id, period_start, period_end, meter, version, price, plan, effective_from, delivery_mode)
VALUES(sqlc.arg(account_id)::uuid, sqlc.arg(period_start)::timestamptz,
       sqlc.arg(period_end)::timestamptz, sqlc.arg(meter)::text, sqlc.arg(version)::text, sqlc.arg(price)::jsonb,
       sqlc.arg(plan)::text, sqlc.arg(effective_from)::timestamptz, sqlc.arg(delivery_mode)::text)
ON CONFLICT(account_id, period_start, meter, version) DO UPDATE
SET version = EXCLUDED.version
WHERE financial_price_snapshots.period_end = EXCLUDED.period_end
  AND financial_price_snapshots.price = EXCLUDED.price
  AND financial_price_snapshots.plan = EXCLUDED.plan
  AND financial_price_snapshots.effective_from = EXCLUDED.effective_from
  AND financial_price_snapshots.delivery_mode = EXCLUDED.delivery_mode
RETURNING recorded_at;

-- name: FinancialPriceSnapshotsList :many
SELECT account_id, period_start, period_end, meter, version, price, recorded_at, plan, effective_from, delivery_mode
FROM financial_price_snapshots
WHERE account_id = sqlc.arg(account_id)::uuid AND period_start = sqlc.arg(period_start)::timestamptz
ORDER BY meter, version;

-- name: FinancialSamplingWindowPut :exec
INSERT INTO financial_sampling_windows(minute, compute_complete, egress_complete)
VALUES (sqlc.arg(minute)::timestamptz, sqlc.arg(compute_complete)::boolean, sqlc.arg(egress_complete)::boolean)
ON CONFLICT(minute) DO UPDATE
SET compute_complete = financial_sampling_windows.compute_complete OR EXCLUDED.compute_complete,
    egress_complete = financial_sampling_windows.egress_complete OR EXCLUDED.egress_complete,
    observed_at = clock_timestamp();

-- name: FinancialSamplingCoverage :one
SELECT count(*) FILTER(WHERE compute_complete)::bigint AS compute_minutes,
       count(*) FILTER(WHERE egress_complete)::bigint AS egress_minutes,
       max(observed_at)::timestamptz AS observed_at
FROM financial_sampling_windows
WHERE minute >= sqlc.arg(period_start)::timestamptz AND minute < sqlc.arg(period_end)::timestamptz;

-- name: FinancialCompletedComputeMinutes :many
SELECT minute
FROM financial_sampling_windows
WHERE minute >= sqlc.arg(period_start)::timestamptz AND minute < sqlc.arg(period_end)::timestamptz
  AND compute_complete
ORDER BY minute;

-- name: FinancialUsageAggregate :many
SELECT price_version, plan, meter, unit, attribution, sum(quantity)::bigint AS quantity, count(*)::bigint AS source_count
FROM financial_usage_evidence
WHERE account_id = sqlc.arg(account_id)::uuid
  AND source_start >= sqlc.arg(period_start)::timestamptz
  AND source_end <= sqlc.arg(period_end)::timestamptz
  AND id <= sqlc.arg(through_id)::bigint
GROUP BY price_version, plan, meter, unit, attribution
ORDER BY meter, price_version, plan, unit, attribution::text
LIMIT sqlc.arg(allocation_limit)::integer;

-- name: FinancialEvidenceAccountLock :exec
SELECT pg_advisory_xact_lock(hashtextextended('financial-evidence:' || sqlc.arg(account_id)::uuid::text, 0));

-- name: FinancialEvidenceBySource :one
SELECT id, account_id, instance_id, source_id, meter, unit, quantity,
       source_start, source_end, plan, attribution, observed_at, price_version,
       corrects_source_id, adjustment_actor, adjustment_reason
FROM financial_usage_evidence WHERE account_id = sqlc.arg(account_id)::uuid AND source_id = sqlc.arg(source_id)::text;

-- name: FinancialAdjustmentInsert :exec
INSERT INTO financial_usage_evidence(account_id, instance_id, source_id, meter, unit, quantity,
  source_start, source_end, plan, attribution, price_version, corrects_source_id, adjustment_actor, adjustment_reason)
SELECT account_id, instance_id, sqlc.arg(source_id)::text, meter, unit, sqlc.arg(quantity)::bigint,
  source_start, source_end, plan, attribution, price_version, source_id, sqlc.arg(actor)::text, sqlc.arg(reason)::text
FROM financial_usage_evidence
WHERE account_id = sqlc.arg(account_id)::uuid AND source_id = sqlc.arg(corrects_source_id)::text AND corrects_source_id IS NULL
ON CONFLICT(account_id, source_id) DO NOTHING;

-- ADR-20261019193600. Compute MB-seconds the pusher delivered to one provider for one
-- account in [start, end); the ledger dual run compares it with ledger evidence.
-- name: FinancialLedgerDeliveredComputeMBSeconds :one
SELECT coalesce(sum(mb_seconds), 0)::bigint AS mb_seconds
FROM billing_usage_deliveries
WHERE provider = sqlc.arg(provider)::text AND account_id = sqlc.arg(account_id)::uuid
  AND window_start >= sqlc.arg(start_at)::timestamptz AND window_start < sqlc.arg(end_at)::timestamptz;

-- name: FinancialObjectStorageEstimatePut :exec
-- ADR-20261020160000: apid's newest live object-storage estimate replaces
-- the month's row; an older observation never overwrites a newer one.
INSERT INTO financial_object_storage_estimates(account_id, period_start, observed_at, usage_through, fresh,
  stored_byte_hours, request_count, egress_bytes, currency, storage_millicents_per_gib_month,
  requests_millicents_per_million, egress_millicents_per_gib, included_byte_hours, included_requests, included_egress_bytes)
VALUES(sqlc.arg(account_id)::uuid, sqlc.arg(period_start)::timestamptz, sqlc.arg(observed_at)::timestamptz,
  sqlc.arg(usage_through)::timestamptz, sqlc.arg(fresh)::boolean, sqlc.arg(stored_byte_hours)::bigint,
  sqlc.arg(request_count)::bigint, sqlc.arg(egress_bytes)::bigint, sqlc.arg(currency)::text,
  sqlc.arg(storage_millicents_per_gib_month)::bigint, sqlc.arg(requests_millicents_per_million)::bigint,
  sqlc.arg(egress_millicents_per_gib)::bigint, sqlc.arg(included_byte_hours)::bigint,
  sqlc.arg(included_requests)::bigint, sqlc.arg(included_egress_bytes)::bigint)
ON CONFLICT (account_id, period_start) DO UPDATE SET observed_at = EXCLUDED.observed_at,
  usage_through = EXCLUDED.usage_through, fresh = EXCLUDED.fresh, stored_byte_hours = EXCLUDED.stored_byte_hours,
  request_count = EXCLUDED.request_count, egress_bytes = EXCLUDED.egress_bytes, currency = EXCLUDED.currency,
  storage_millicents_per_gib_month = EXCLUDED.storage_millicents_per_gib_month,
  requests_millicents_per_million = EXCLUDED.requests_millicents_per_million,
  egress_millicents_per_gib = EXCLUDED.egress_millicents_per_gib, included_byte_hours = EXCLUDED.included_byte_hours,
  included_requests = EXCLUDED.included_requests, included_egress_bytes = EXCLUDED.included_egress_bytes
WHERE financial_object_storage_estimates.observed_at <= EXCLUDED.observed_at;

-- name: FinancialObjectStorageEstimateGet :one
SELECT * FROM financial_object_storage_estimates
WHERE account_id = sqlc.arg(account_id)::uuid AND period_start = sqlc.arg(period_start)::timestamptz;
