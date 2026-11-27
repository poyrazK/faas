-- Flag experiments and conversion events (ADR 20261115163113).

-- name: EnsureFlagEventUsage :exec
INSERT INTO flag_event_usage (account_id, month)
VALUES (sqlc.arg(account_id)::uuid, sqlc.arg(month)::date)
ON CONFLICT (account_id, month) DO NOTHING;

-- name: LockFlagEventUsage :one
SELECT accepted, dropped FROM flag_event_usage
WHERE account_id = sqlc.arg(account_id)::uuid AND month = sqlc.arg(month)::date
FOR UPDATE;

-- name: AddFlagEventUsage :one
UPDATE flag_event_usage
SET accepted = accepted + sqlc.arg(accepted)::bigint, dropped = dropped + sqlc.arg(dropped)::bigint
WHERE account_id = sqlc.arg(account_id)::uuid AND month = sqlc.arg(month)::date
RETURNING accepted, dropped;

-- name: AddFlagEventName :execrows
-- Registers an event name while the environment holds fewer than max_names.
INSERT INTO flag_event_names (environment_id, name, account_id)
SELECT sqlc.arg(environment_id)::uuid, sqlc.arg(name)::text, sqlc.arg(account_id)::uuid
WHERE (SELECT count(*) FROM flag_event_names n WHERE n.environment_id = sqlc.arg(environment_id)::uuid) < sqlc.arg(max_names)::bigint
ON CONFLICT (environment_id, name) DO NOTHING;

-- name: FlagEventNamesKnown :many
SELECT name FROM flag_event_names
WHERE environment_id = sqlc.arg(environment_id)::uuid AND name = ANY(sqlc.arg(names)::text[]);

-- name: ActivePlatformTenantIDs :many
SELECT id FROM platform_tenants
WHERE account_id = sqlc.arg(account_id)::uuid AND id = ANY(sqlc.arg(ids)::uuid[]) AND status = 'active';

-- name: AddFlagConversionDaily :exec
INSERT INTO flag_conversion_daily (environment_id, event_name, customer_id, day, account_id, count, value_sum, first_at, last_at)
VALUES (sqlc.arg(environment_id)::uuid, sqlc.arg(event_name)::text, sqlc.arg(customer_id)::uuid, sqlc.arg(day)::date,
        sqlc.arg(account_id)::uuid, sqlc.arg(count)::bigint, sqlc.arg(value_sum)::bigint,
        sqlc.arg(first_at)::timestamptz, sqlc.arg(last_at)::timestamptz)
ON CONFLICT (environment_id, event_name, customer_id, day) DO UPDATE
SET count = flag_conversion_daily.count + EXCLUDED.count,
    value_sum = flag_conversion_daily.value_sum + EXCLUDED.value_sum,
    first_at = LEAST(flag_conversion_daily.first_at, EXCLUDED.first_at),
    last_at = GREATEST(flag_conversion_daily.last_at, EXCLUDED.last_at);

-- name: FlagConversionAccounts :many
-- Accounts holding conversion rows older than cutoff, for the retention sweep.
SELECT DISTINCT d.account_id, a.plan::text AS plan
FROM flag_conversion_daily d JOIN accounts a ON a.id = d.account_id
WHERE d.day < sqlc.arg(cutoff)::date
LIMIT sqlc.arg(max_accounts)::int;

-- name: DeleteFlagConversionsBefore :execrows
DELETE FROM flag_conversion_daily
WHERE account_id = sqlc.arg(account_id)::uuid AND day < sqlc.arg(before)::date;

-- name: CreateFlagExperiment :one
INSERT INTO flag_experiments (id, account_id, project_id, environment_id, key, flag_key, rule_id, control,
    metric_event, metric_kind, mixing_lift, weights, guardrails, created_by, metric_definition, cuped,
    unit, layer, traffic, allocation, denominator_event, percentile, notify_channel_ids)
SELECT sqlc.arg(id)::uuid, sqlc.arg(account_id)::uuid, sqlc.arg(project_id)::uuid, sqlc.arg(environment_id)::uuid,
    sqlc.arg(key)::text, sqlc.arg(flag_key)::text, sqlc.arg(rule_id)::text, sqlc.arg(control)::text,
    sqlc.arg(metric_event)::text, sqlc.arg(metric_kind)::text, sqlc.arg(mixing_lift)::double precision, sqlc.arg(weights)::jsonb,
    sqlc.arg(guardrails)::jsonb, sqlc.arg(created_by)::text, sqlc.arg(metric_definition)::jsonb, sqlc.arg(cuped)::boolean,
    sqlc.arg(unit)::text, sqlc.arg(layer)::text, sqlc.arg(traffic)::int, sqlc.arg(allocation)::text,
    sqlc.arg(denominator_event)::text, sqlc.arg(percentile)::int, sqlc.arg(notify_channel_ids)::uuid[]
WHERE (SELECT count(*) FROM flag_experiments x WHERE x.environment_id = sqlc.arg(environment_id)::uuid) < sqlc.arg(max_experiments)::bigint
RETURNING *;

-- name: GetFlagExperiment :one
SELECT * FROM flag_experiments
WHERE environment_id = sqlc.arg(environment_id)::uuid AND key = sqlc.arg(key)::text;

-- name: ListFlagExperiments :many
SELECT * FROM flag_experiments
WHERE environment_id = sqlc.arg(environment_id)::uuid
ORDER BY created_at DESC, key;

-- name: StartFlagExperiment :one
-- Starts a draft while the environment runs fewer than max_running. The
-- telemetry cursor starts just before the start minute: gateways collapse
-- request telemetry into rows stamped at their minute's start, so a cursor at
-- the start instant would skip every exposure in that minute
-- (FlagExperimentStartCursor).
UPDATE flag_experiments x
SET status = 'running', started_at = sqlc.arg(now)::timestamptz,
    telemetry_cursor = date_trunc('minute', sqlc.arg(now)::timestamptz) - interval '1 microsecond',
    weights = sqlc.arg(weights)::jsonb, layer_from = sqlc.arg(layer_from)::int, layer_to = sqlc.arg(layer_to)::int
WHERE x.environment_id = sqlc.arg(environment_id)::uuid AND x.key = sqlc.arg(key)::text AND x.status = 'draft'
  AND (SELECT count(*) FROM flag_experiments r WHERE r.environment_id = sqlc.arg(environment_id)::uuid AND r.status = 'running') < sqlc.arg(max_running)::bigint
RETURNING *;

-- name: StopFlagExperiment :one
UPDATE flag_experiments
SET status = 'stopped', stopped_at = sqlc.arg(now)::timestamptz, stop_reason = sqlc.arg(stop_reason)::text,
    breach = sqlc.narg(breach)::jsonb
WHERE environment_id = sqlc.arg(environment_id)::uuid AND key = sqlc.arg(key)::text AND status = 'running'
RETURNING *;

-- name: DeleteFlagExperiment :execrows
DELETE FROM flag_experiments
WHERE environment_id = sqlc.arg(environment_id)::uuid AND key = sqlc.arg(key)::text AND status <> 'running';

-- name: ListRunningFlagExperiments :many
SELECT x.*, e.slug AS environment_slug, a.plan::text AS plan
FROM flag_experiments x
JOIN project_environments e ON e.id = x.environment_id
JOIN accounts a ON a.id = x.account_id
WHERE x.status = 'running' AND x.id > sqlc.arg(after_id)::uuid
ORDER BY x.id
LIMIT sqlc.arg(max_rows)::int;

-- name: FlagExperimentExposures :many
-- First exposure and guardrail request counts per verified customer and
-- variant to the experiment's rule in request telemetry received in
-- (from, until]. A request is latency-tested once the experiment has a
-- latency threshold (latency_threshold_ms >= 0); platform refusals are not
-- server errors (ADR 20261116190208 §1).
SELECT t.platform_tenant_id::uuid AS customer_id, (e->>'value')::text AS variant,
       min(t.received_at)::timestamptz AS first_at,
       sum(t.count)::bigint AS requests,
       COALESCE(sum(t.count) FILTER (WHERE t.status >= 500 AND NOT t.platform_refusal), 0)::bigint AS errors,
       COALESCE(sum(t.count) FILTER (WHERE sqlc.arg(latency_threshold_ms)::int >= 0), 0)::bigint AS latency_requests,
       COALESCE(sum(t.count) FILTER (WHERE sqlc.arg(latency_threshold_ms)::int >= 0 AND t.latency_ms > sqlc.arg(latency_threshold_ms)::int), 0)::bigint AS slow_requests
FROM request_telemetry t
JOIN apps a ON a.id = t.app_id
JOIN deployments d ON d.id = t.deployment_id
CROSS JOIN LATERAL jsonb_array_elements(t.flag_evidence) AS decision(e)
WHERE t.account_id = sqlc.arg(account_id)::uuid
  AND a.project_id = sqlc.arg(project_id)::uuid
  AND (d.scope = sqlc.arg(environment_slug)::text OR (d.scope = 'default' AND sqlc.arg(environment_slug)::text = 'production'))
  AND t.received_at > sqlc.arg(received_from)::timestamptz
  AND t.received_at <= sqlc.arg(received_until)::timestamptz
  AND t.platform_tenant_id IS NOT NULL
  AND e->>'flag' = sqlc.arg(flag_key)::text
  AND e->>'rule_id' = sqlc.arg(rule_id)::text
  AND e->>'reason' IS DISTINCT FROM 'holdout'
  AND jsonb_typeof(e->'value') = 'string'
GROUP BY 1, 2
ORDER BY 3
LIMIT sqlc.arg(max_rows)::int;

-- name: AddFlagExperimentUnits :exec
-- A customer seen with another variant than its first is marked crossed and
-- left out of results.
-- The arrays are parallel: the select list unnests them in step.
INSERT INTO flag_experiment_units (experiment_id, customer_id, variant, first_exposed_at, crossed,
    requests, errors, latency_requests, slow_requests)
SELECT sqlc.arg(experiment_id)::uuid, unnest(sqlc.arg(customer_ids)::uuid[]), unnest(sqlc.arg(variants)::text[]),
       unnest(sqlc.arg(first_ats)::timestamptz[]), unnest(sqlc.arg(crossed)::boolean[]),
       unnest(sqlc.arg(requests)::bigint[]), unnest(sqlc.arg(errors)::bigint[]),
       unnest(sqlc.arg(latency_requests)::bigint[]), unnest(sqlc.arg(slow_requests)::bigint[])
ON CONFLICT (experiment_id, customer_id) DO UPDATE
SET crossed = flag_experiment_units.crossed OR EXCLUDED.crossed OR flag_experiment_units.variant <> EXCLUDED.variant,
    requests = flag_experiment_units.requests + EXCLUDED.requests,
    errors = flag_experiment_units.errors + EXCLUDED.errors,
    latency_requests = flag_experiment_units.latency_requests + EXCLUDED.latency_requests,
    slow_requests = flag_experiment_units.slow_requests + EXCLUDED.slow_requests;

-- name: MarkFlagExperimentConversions :execrows
-- A unit converts when the metric event was sent on or after its first
-- exposure.
UPDATE flag_experiment_units u
SET converted_at = GREATEST(c.first_at, u.first_exposed_at)
FROM flag_conversion_daily c
WHERE u.experiment_id = sqlc.arg(experiment_id)::uuid
  AND u.converted_at IS NULL
  AND c.environment_id = sqlc.arg(environment_id)::uuid
  AND c.event_name = sqlc.arg(event_name)::text
  AND c.customer_id = u.customer_id
  AND c.day >= sqlc.arg(since_day)::date
  AND c.last_at >= u.first_exposed_at;

-- name: FlagExperimentArms :many
SELECT variant, count(*)::bigint AS exposed, count(converted_at)::bigint AS converted
FROM flag_experiment_units
WHERE experiment_id = sqlc.arg(experiment_id)::uuid AND NOT crossed
GROUP BY variant
ORDER BY variant;

-- name: FlagExperimentCrossedUnits :one
SELECT count(*)::bigint FROM flag_experiment_units
WHERE experiment_id = sqlc.arg(experiment_id)::uuid AND crossed;

-- name: AdvanceFlagExperiment :exec
UPDATE flag_experiments
SET telemetry_cursor = sqlc.arg(telemetry_cursor)::timestamptz, min_p_values = sqlc.arg(min_p_values)::jsonb,
    min_cuped_p_values = sqlc.arg(min_cuped_p_values)::jsonb
WHERE id = sqlc.arg(id)::uuid AND status = 'running';

-- name: FlagExperimentHorizonArms :many
-- Exposed and converted units of a funnel or retention metric whose
-- deadline (first exposure plus horizon) is at or before until
-- (ADR 20261119102741 §1).
SELECT variant, count(*)::bigint AS exposed, count(converted_at)::bigint AS converted
FROM flag_experiment_units
WHERE experiment_id = sqlc.arg(experiment_id)::uuid AND NOT crossed
  AND first_exposed_at + make_interval(secs => sqlc.arg(horizon_seconds)::double precision) <= sqlc.arg(until)::timestamptz
GROUP BY variant
ORDER BY variant;

-- name: FlagExperimentConversionCandidates :many
-- Unconverted units of a funnel or retention metric whose window has
-- started (first exposure plus start offset is at or before now) and whose
-- deadline is after not_before, in customer order after after_customer.
-- Subject units also return their customer and subject hash.
SELECT customer_id, first_exposed_at, unit_customer_id, subject_hash
FROM flag_experiment_units
WHERE experiment_id = sqlc.arg(experiment_id)::uuid AND NOT crossed AND converted_at IS NULL
  AND customer_id > sqlc.arg(after_customer)::uuid
  AND first_exposed_at + make_interval(secs => sqlc.arg(start_seconds)::double precision) <= sqlc.arg(now)::timestamptz
  AND first_exposed_at + make_interval(secs => sqlc.arg(horizon_seconds)::double precision) > sqlc.arg(not_before)::timestamptz
ORDER BY customer_id
LIMIT sqlc.arg(max_rows)::int;

-- name: MarkFlagExperimentUnitConversions :execrows
-- The arrays are parallel.
UPDATE flag_experiment_units u
SET converted_at = c.at
FROM (SELECT unnest(sqlc.arg(customer_ids)::uuid[]) AS customer_id, unnest(sqlc.arg(ats)::timestamptz[]) AS at) c
WHERE u.experiment_id = sqlc.arg(experiment_id)::uuid AND u.customer_id = c.customer_id
  AND u.converted_at IS NULL AND c.at >= u.first_exposed_at;

-- name: FlagExperimentUnitsWithoutPreValue :many
SELECT customer_id, unit_customer_id, subject_hash
FROM flag_experiment_units
WHERE experiment_id = sqlc.arg(experiment_id)::uuid AND pre_value IS NULL
ORDER BY customer_id
LIMIT sqlc.arg(max_rows)::int;

-- name: SetFlagExperimentPreValues :execrows
-- The arrays are parallel.
UPDATE flag_experiment_units u
SET pre_value = v.pre_value
FROM (SELECT unnest(sqlc.arg(customer_ids)::uuid[]) AS customer_id, unnest(sqlc.arg(pre_values)::double precision[]) AS pre_value) v
WHERE u.experiment_id = sqlc.arg(experiment_id)::uuid AND u.customer_id = v.customer_id AND u.pre_value IS NULL;

-- name: FlagExperimentBinaryCupedArms :many
-- Per-variant CUPED sums of a binary metric (Y is converted) over counted
-- units, and how many counted units still lack a pre-period value. A
-- horizon of 0 counts every unit.
SELECT variant, count(*)::bigint AS n,
       count(converted_at)::float8 AS sum_y,
       COALESCE(sum(pre_value), 0)::float8 AS sum_x,
       COALESCE(sum(pre_value * pre_value), 0)::float8 AS sum_x2,
       COALESCE(sum(pre_value) FILTER (WHERE converted_at IS NOT NULL), 0)::float8 AS sum_xy,
       count(*) FILTER (WHERE pre_value IS NULL)::bigint AS pending
FROM flag_experiment_units
WHERE experiment_id = sqlc.arg(experiment_id)::uuid AND NOT crossed
  AND first_exposed_at + make_interval(secs => sqlc.arg(horizon_seconds)::double precision) <= sqlc.arg(until)::timestamptz
GROUP BY variant
ORDER BY variant;

-- name: FlagExperimentMeanCupedArms :many
-- FlagExperimentMeanArms' winsorized unit values with each unit's
-- pre-period value, summed per variant for CUPED.
WITH v AS (
    SELECT u.variant, (u.value_sum + COALESCE(o.total, 0))::float8 AS val, u.pre_value
    FROM flag_experiment_units u
    LEFT JOIN LATERAL (
        SELECT sum(c.value_sum)::float8 AS total FROM flag_conversion_daily c
        WHERE c.environment_id = sqlc.arg(environment_id)::uuid AND c.event_name = sqlc.arg(event_name)::text
          AND c.customer_id = u.customer_id AND c.day >= sqlc.arg(open_from_day)::date
          AND c.last_at >= u.first_exposed_at
    ) o ON true
    WHERE u.experiment_id = sqlc.arg(experiment_id)::uuid AND NOT u.crossed
), caps AS (
    SELECT percentile_cont(0.01) WITHIN GROUP (ORDER BY val) AS lo,
           percentile_cont(0.99) WITHIN GROUP (ORDER BY val) AS hi, count(*) AS n
    FROM v
), w AS (
    SELECT v.variant, v.pre_value,
           CASE WHEN caps.n >= sqlc.arg(min_units)::bigint THEN LEAST(GREATEST(v.val, caps.lo), caps.hi) ELSE v.val END AS y
    FROM v CROSS JOIN caps
)
SELECT variant, count(*)::bigint AS n,
       COALESCE(sum(y), 0)::float8 AS sum_y, COALESCE(sum(y * y), 0)::float8 AS sum_y2,
       COALESCE(sum(pre_value), 0)::float8 AS sum_x, COALESCE(sum(pre_value * pre_value), 0)::float8 AS sum_x2,
       COALESCE(sum(pre_value * y), 0)::float8 AS sum_xy,
       count(*) FILTER (WHERE pre_value IS NULL)::bigint AS pending
FROM w
GROUP BY variant
ORDER BY variant;

-- name: AddFlagExperimentLatency :exec
-- Adds the experiment rule's requests in (from, until] to each variant's
-- latency histogram (5% log buckets, flags.LatencyBucket).
INSERT INTO flag_experiment_latency (experiment_id, variant, bucket, requests)
SELECT sqlc.arg(experiment_id)::uuid, (e->>'value')::text,
       (CASE WHEN t.latency_ms <= 0 THEN 0 ELSE ceil(ln(t.latency_ms + 1) / ln(1.05)) END)::smallint,
       sum(t.count)::bigint
FROM request_telemetry t
JOIN apps a ON a.id = t.app_id
JOIN deployments d ON d.id = t.deployment_id
CROSS JOIN LATERAL jsonb_array_elements(t.flag_evidence) AS decision(e)
WHERE t.account_id = sqlc.arg(account_id)::uuid
  AND a.project_id = sqlc.arg(project_id)::uuid
  AND (d.scope = sqlc.arg(environment_slug)::text OR (d.scope = 'default' AND sqlc.arg(environment_slug)::text = 'production'))
  AND t.received_at > sqlc.arg(received_from)::timestamptz
  AND t.received_at <= sqlc.arg(received_until)::timestamptz
  AND t.platform_tenant_id IS NOT NULL
  AND e->>'flag' = sqlc.arg(flag_key)::text
  AND e->>'rule_id' = sqlc.arg(rule_id)::text
  AND e->>'reason' IS DISTINCT FROM 'holdout'
  AND jsonb_typeof(e->'value') = 'string'
  AND length(e->>'value') BETWEEN 1 AND 128
GROUP BY 2, 3
ON CONFLICT (experiment_id, variant, bucket) DO UPDATE
SET requests = flag_experiment_latency.requests + EXCLUDED.requests;

-- name: FlagExperimentLatency :many
SELECT variant, bucket::int AS bucket, requests FROM flag_experiment_latency
WHERE experiment_id = sqlc.arg(experiment_id)::uuid
ORDER BY variant, bucket;

-- name: SetFlagExperimentLatencyThreshold :exec
UPDATE flag_experiments SET latency_threshold_ms = sqlc.arg(latency_threshold_ms)::int
WHERE id = sqlc.arg(id)::uuid AND status = 'running' AND latency_threshold_ms IS NULL;

-- name: FlagExperimentGuardrailArms :many
-- Per-variant sums the delta method needs for the server error rate
-- (errors over requests) and the slow request share (slow over
-- latency-tested requests), with customers as units.
SELECT variant,
       count(*) FILTER (WHERE requests > 0)::bigint AS units,
       sum(errors)::float8 AS errors, sum(requests)::float8 AS requests,
       sum(errors::float8 * errors)::float8 AS errors_sq, sum(requests::float8 * requests)::float8 AS requests_sq,
       sum(errors::float8 * requests)::float8 AS errors_requests,
       count(*) FILTER (WHERE latency_requests > 0)::bigint AS latency_units,
       sum(slow_requests)::float8 AS slow, sum(latency_requests)::float8 AS latency_requests,
       sum(slow_requests::float8 * slow_requests)::float8 AS slow_sq,
       sum(latency_requests::float8 * latency_requests)::float8 AS latency_requests_sq,
       sum(slow_requests::float8 * latency_requests)::float8 AS slow_latency_requests
FROM flag_experiment_units
WHERE experiment_id = sqlc.arg(experiment_id)::uuid AND NOT crossed
GROUP BY variant
ORDER BY variant;

-- name: FlagExperimentMeanArms :many
-- Per-variant count, non-zero count, sum and sum of squares of unit values
-- (folded closed days plus open days from open_from_day), clamped to the
-- 1st and 99th percentile once there are min_units units
-- (ADR 20261116190208 §2).
WITH v AS (
    SELECT u.variant, (u.value_sum + COALESCE(o.total, 0))::float8 AS val
    FROM flag_experiment_units u
    LEFT JOIN LATERAL (
        SELECT sum(c.value_sum)::float8 AS total FROM flag_conversion_daily c
        WHERE c.environment_id = sqlc.arg(environment_id)::uuid AND c.event_name = sqlc.arg(event_name)::text
          AND c.customer_id = u.customer_id AND c.day >= sqlc.arg(open_from_day)::date
          AND c.last_at >= u.first_exposed_at
    ) o ON true
    WHERE u.experiment_id = sqlc.arg(experiment_id)::uuid AND NOT u.crossed
), caps AS (
    SELECT percentile_cont(0.01) WITHIN GROUP (ORDER BY val) AS lo,
           percentile_cont(0.99) WITHIN GROUP (ORDER BY val) AS hi, count(*) AS n
    FROM v
), w AS (
    SELECT v.variant, v.val,
           CASE WHEN caps.n >= sqlc.arg(min_units)::bigint THEN LEAST(GREATEST(v.val, caps.lo), caps.hi) ELSE v.val END AS capped
    FROM v CROSS JOIN caps
)
SELECT variant, count(*)::bigint AS exposed, (count(*) FILTER (WHERE val <> 0))::bigint AS nonzero,
       COALESCE(sum(capped), 0)::float8 AS total, COALESCE(sum(capped * capped), 0)::float8 AS total_sq
FROM w
GROUP BY variant
ORDER BY variant;

-- name: FoldFlagExperimentValues :execrows
-- Adds the metric event's values of closed days (from_day..through_day) to
-- each unit, counting a day when it ends at or after the unit's first
-- exposure.
UPDATE flag_experiment_units u
SET value_sum = u.value_sum + s.total
FROM (
    SELECT u2.customer_id, sum(c.value_sum)::bigint AS total
    FROM flag_experiment_units u2
    JOIN flag_conversion_daily c ON c.customer_id = u2.customer_id
    WHERE u2.experiment_id = sqlc.arg(experiment_id)::uuid
      AND c.environment_id = sqlc.arg(environment_id)::uuid AND c.event_name = sqlc.arg(event_name)::text
      AND c.day BETWEEN sqlc.arg(from_day)::date AND sqlc.arg(through_day)::date
      AND c.last_at >= u2.first_exposed_at
    GROUP BY u2.customer_id
) s
WHERE u.experiment_id = sqlc.arg(experiment_id)::uuid AND u.customer_id = s.customer_id;

-- name: SetFlagExperimentValueThrough :exec
UPDATE flag_experiments SET value_through_day = sqlc.arg(through_day)::date
WHERE id = sqlc.arg(id)::uuid AND status = 'running';

-- name: RunningSubjectExperiments :many
-- The running subject experiments of an environment, which take SDK
-- exposures (ADR 20261119173702 §1).
SELECT id, flag_key, rule_id FROM flag_experiments
WHERE environment_id = sqlc.arg(environment_id)::uuid AND status = 'running' AND unit = 'subject';

-- name: AddFlagExperimentSubjectUnits :exec
-- Subject units keyed by a UUID derived from the customer and subject hash;
-- a unit seen with another variant than its first is crossed. The arrays
-- are parallel.
INSERT INTO flag_experiment_units (experiment_id, customer_id, unit_customer_id, subject_hash, variant, first_exposed_at, crossed)
SELECT sqlc.arg(experiment_id)::uuid, unnest(sqlc.arg(unit_ids)::uuid[]), unnest(sqlc.arg(customer_ids)::uuid[]),
       unnest(sqlc.arg(subject_hashes)::bytea[]), unnest(sqlc.arg(variants)::text[]), unnest(sqlc.arg(first_ats)::timestamptz[]),
       unnest(sqlc.arg(crossed)::boolean[])
ON CONFLICT (experiment_id, customer_id) DO UPDATE
SET crossed = flag_experiment_units.crossed OR EXCLUDED.crossed OR flag_experiment_units.variant <> EXCLUDED.variant,
    first_exposed_at = LEAST(flag_experiment_units.first_exposed_at, EXCLUDED.first_exposed_at);

-- name: FoldFlagExperimentDenominators :execrows
-- A ratio metric's denominator: like FoldFlagExperimentValues into den_sum.
UPDATE flag_experiment_units u
SET den_sum = u.den_sum + s.total
FROM (
    SELECT u2.customer_id, sum(c.value_sum)::bigint AS total
    FROM flag_experiment_units u2
    JOIN flag_conversion_daily c ON c.customer_id = u2.customer_id
    WHERE u2.experiment_id = sqlc.arg(experiment_id)::uuid
      AND c.environment_id = sqlc.arg(environment_id)::uuid AND c.event_name = sqlc.arg(event_name)::text
      AND c.day BETWEEN sqlc.arg(from_day)::date AND sqlc.arg(through_day)::date
      AND c.last_at >= u2.first_exposed_at
    GROUP BY u2.customer_id
) s
WHERE u.experiment_id = sqlc.arg(experiment_id)::uuid AND u.customer_id = s.customer_id;

-- name: FlagExperimentRatioArms :many
-- Per-variant sums a ratio metric's delta method needs: the numerator and
-- denominator value of each unit (folded plus open days), with their
-- squares and cross products (ADR 20261119173702 §3).
WITH v AS (
    SELECT u.variant,
           (u.value_sum + COALESCE(n.total, 0))::float8 AS num,
           (u.den_sum + COALESCE(d.total, 0))::float8 AS den
    FROM flag_experiment_units u
    LEFT JOIN LATERAL (
        SELECT sum(c.value_sum)::float8 AS total FROM flag_conversion_daily c
        WHERE c.environment_id = sqlc.arg(environment_id)::uuid AND c.event_name = sqlc.arg(event_name)::text
          AND c.customer_id = u.customer_id AND c.day >= sqlc.arg(open_from_day)::date AND c.last_at >= u.first_exposed_at
    ) n ON true
    LEFT JOIN LATERAL (
        SELECT sum(c.value_sum)::float8 AS total FROM flag_conversion_daily c
        WHERE c.environment_id = sqlc.arg(environment_id)::uuid AND c.event_name = sqlc.arg(denominator_event)::text
          AND c.customer_id = u.customer_id AND c.day >= sqlc.arg(open_from_day)::date AND c.last_at >= u.first_exposed_at
    ) d ON true
    WHERE u.experiment_id = sqlc.arg(experiment_id)::uuid AND NOT u.crossed
)
SELECT variant, count(*)::bigint AS units, COALESCE(sum(num), 0)::float8 AS num, COALESCE(sum(den), 0)::float8 AS den,
       COALESCE(sum(num * num), 0)::float8 AS num_sq, COALESCE(sum(den * den), 0)::float8 AS den_sq,
       COALESCE(sum(num * den), 0)::float8 AS num_den
FROM v
GROUP BY variant
ORDER BY variant;

-- name: FlagExperimentUnitValues :many
-- Each unit's metric value (folded plus open days) for a percentile
-- metric, at most max_rows per variant.
WITH v AS (
    SELECT u.variant, (u.value_sum + COALESCE(o.total, 0))::float8 AS val,
           row_number() OVER (PARTITION BY u.variant ORDER BY u.customer_id) AS n
    FROM flag_experiment_units u
    LEFT JOIN LATERAL (
        SELECT sum(c.value_sum)::float8 AS total FROM flag_conversion_daily c
        WHERE c.environment_id = sqlc.arg(environment_id)::uuid AND c.event_name = sqlc.arg(event_name)::text
          AND c.customer_id = u.customer_id AND c.day >= sqlc.arg(open_from_day)::date AND c.last_at >= u.first_exposed_at
    ) o ON true
    WHERE u.experiment_id = sqlc.arg(experiment_id)::uuid AND NOT u.crossed
)
SELECT variant, val FROM v WHERE n <= sqlc.arg(max_rows)::bigint ORDER BY variant, val;

-- name: SetFlagExperimentBandit :exec
-- A bandit pass: its posterior, and the weights it published when it
-- re-weighted (weighted_at set).
UPDATE flag_experiments
SET bandit = sqlc.arg(bandit)::jsonb,
    weights = CASE WHEN sqlc.narg(weighted_at)::timestamptz IS NULL THEN weights ELSE sqlc.arg(weights)::jsonb END,
    bandit_weighted_at = COALESCE(sqlc.narg(weighted_at)::timestamptz, bandit_weighted_at)
WHERE id = sqlc.arg(id)::uuid AND status = 'running';

-- name: FlagExperimentSegmentArms :many
-- Exposed and converted counted units by variant and cohort membership
-- (the unit's customer is a current member). A horizon of 0 counts every
-- unit; otherwise units whose deadline is at or before until.
SELECT u.variant,
       EXISTS (SELECT 1 FROM analytics_cohort_members m
               WHERE m.cohort_id = sqlc.arg(cohort_id)::uuid AND m.customer_id = COALESCE(u.unit_customer_id, u.customer_id)) AS member,
       count(*)::bigint AS exposed, count(u.converted_at)::bigint AS converted
FROM flag_experiment_units u
WHERE u.experiment_id = sqlc.arg(experiment_id)::uuid AND NOT u.crossed
  AND (sqlc.arg(horizon_seconds)::double precision = 0
       OR u.first_exposed_at + make_interval(secs => sqlc.arg(horizon_seconds)::double precision) <= sqlc.arg(until)::timestamptz)
GROUP BY 1, 2
ORDER BY 1, 2;

-- name: FeatureFlagRolloutMetricArms :many
-- A metric-driven rollout stage (ADR 20261119173702 §5): customers whose
-- requests since since evaluated the flag, treated when any decision
-- matched the rule, and how many of each group sent the metric event at or
-- after their first exposure.
WITH exposures AS (
    SELECT t.platform_tenant_id::uuid AS customer_id,
           bool_or(e->>'rule_id' = sqlc.arg(rule_id)::text AND e->>'reason' = 'rule_match') AS treated,
           min(t.received_at) AS first_at
    FROM request_telemetry t
    JOIN apps a ON a.id = t.app_id
    JOIN deployments d ON d.id = t.deployment_id
    CROSS JOIN LATERAL jsonb_array_elements(t.flag_evidence) AS decision(e)
    WHERE t.account_id = sqlc.arg(account_id)::uuid
      AND a.project_id = sqlc.arg(project_id)::uuid
      AND (d.scope = sqlc.arg(environment_slug)::text OR (d.scope = 'default' AND sqlc.arg(environment_slug)::text = 'production'))
      AND t.received_at > sqlc.arg(since)::timestamptz
      AND t.platform_tenant_id IS NOT NULL
      AND e->>'flag' = sqlc.arg(flag_key)::text
    GROUP BY 1
    LIMIT sqlc.arg(max_units)::int
)
SELECT x.treated, count(*)::bigint AS exposed,
       (count(*) FILTER (WHERE EXISTS (
           SELECT 1 FROM flag_conversion_daily c
           WHERE c.environment_id = sqlc.arg(environment_id)::uuid AND c.event_name = sqlc.arg(event_name)::text
             AND c.customer_id = x.customer_id AND c.day >= sqlc.arg(since)::timestamptz::date AND c.last_at >= x.first_at)))::bigint AS converted
FROM exposures x
GROUP BY x.treated
ORDER BY x.treated;
