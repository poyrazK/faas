-- ADR-431: immutable retained evidence, bounded snapshot paging, and prices.
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
