-- name: FinancialEgressOwnerValid :one
SELECT EXISTS(SELECT 1 FROM financial_compute_owners o JOIN compute_nodes n ON n.id=o.node_id
WHERE o.node_id=sqlc.arg(node_id)::uuid AND o.incarnation=sqlc.arg(incarnation)::uuid
AND n.vmmd_incarnation=o.incarnation AND o.egress_budget_incarnation=o.incarnation AND n.active)::boolean AS valid;

-- name: FinancialEgressOwnersReady :one
SELECT (EXISTS(SELECT 1 FROM compute_nodes WHERE active) AND NOT EXISTS(
 SELECT 1 FROM compute_nodes n LEFT JOIN financial_compute_owners o ON o.node_id=n.id
 WHERE n.active AND (o.egress_budget_incarnation IS DISTINCT FROM n.vmmd_incarnation
 OR o.observed_at<clock_timestamp()-make_interval(secs=>sqlc.arg(max_age_seconds)::double precision))))::boolean AS ready;

-- name: FinancialEgressSamplingReady :one
WITH since AS (SELECT greatest(created_at,sqlc.arg(period_start)::timestamptz) AS at FROM accounts WHERE id=sqlc.arg(account_id)::uuid)
SELECT (SELECT retained_from<=since.at FROM financial_evidence_coverage WHERE singleton)
 AND (SELECT count(*) FROM financial_sampling_windows WHERE minute>=date_trunc('minute',since.at)
 AND minute<date_trunc('minute',clock_timestamp()) AND egress_complete)
 =extract(epoch FROM date_trunc('minute',clock_timestamp())-date_trunc('minute',since.at))::bigint/60 AS ready FROM since;

-- name: FinancialEgressBaseline :one
SELECT COALESCE(sum(e.quantity::numeric),0)::text AS quantity,
 COALESCE(bool_or(p.price IS NULL OR p.price->>'meter'!='egress' OR p.price->>'currency'!='USD'
 OR p.price->>'unit'!='interface_bytes' OR (p.price->>'unit_quantity')::numeric<=0
 OR (p.price->>'millicents_per_unit')::numeric<0
 OR (p.price->>'millicents_per_unit')::numeric*sqlc.arg(upper_quantity)::bigint::numeric > sqlc.arg(upper_rate)::bigint::numeric*(p.price->>'unit_quantity')::numeric),false)::boolean AS incomplete
FROM financial_usage_evidence e LEFT JOIN financial_price_snapshots p
 ON p.account_id=e.account_id AND p.version=e.price_version AND p.meter=e.meter AND p.period_start=sqlc.arg(period_start)::timestamptz
WHERE e.account_id=sqlc.arg(account_id)::uuid AND e.meter='egress' AND e.quantity>0
 AND e.source_start>=sqlc.arg(period_start)::timestamptz AND e.source_start<sqlc.arg(period_end)::timestamptz;
