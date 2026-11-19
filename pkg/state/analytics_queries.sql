-- Product analytics (ADR 20261117084505).

-- name: AnalyticsPropertyKeysKnown :many
SELECT key FROM analytics_property_keys
WHERE environment_id = sqlc.arg(environment_id)::uuid AND key = ANY(sqlc.arg(keys)::text[]);

-- name: AddAnalyticsPropertyKey :execrows
-- Registers a property key while the environment holds fewer than max_keys.
INSERT INTO analytics_property_keys (environment_id, key)
SELECT sqlc.arg(environment_id)::uuid, sqlc.arg(key)::text
WHERE (SELECT count(*) FROM analytics_property_keys k WHERE k.environment_id = sqlc.arg(environment_id)::uuid) < sqlc.arg(max_keys)::bigint
ON CONFLICT (environment_id, key) DO NOTHING;

-- name: InsertAnalyticsEvents :exec
-- The arrays are parallel; an empty subject hash is no subject.
INSERT INTO analytics_events (account_id, environment_id, customer_id, subject_hash, name, at, value, properties, flags)
SELECT sqlc.arg(account_id)::uuid, sqlc.arg(environment_id)::uuid, u.customer_id, NULLIF(u.subject_hash, ''::bytea),
       u.name, u.at, u.value, u.properties::jsonb, u.flags::jsonb
FROM (
    SELECT unnest(sqlc.arg(customer_ids)::uuid[]) AS customer_id, unnest(sqlc.arg(subject_hashes)::bytea[]) AS subject_hash,
           unnest(sqlc.arg(names)::text[]) AS name, unnest(sqlc.arg(ats)::timestamptz[]) AS at,
           unnest(sqlc.arg(event_values)::bigint[]) AS value, unnest(sqlc.arg(properties)::text[]) AS properties,
           unnest(sqlc.arg(flags)::text[]) AS flags
) u;

-- name: AnalyticsEventSeries :many
-- Events, units and value per time bucket and breakdown value. by_kind is
-- property, flag or empty; a series breaks down by the event's own value.
SELECT date_bin(make_interval(secs => sqlc.arg(bucket_seconds)::float8), e.at, sqlc.arg(origin)::timestamptz)::timestamptz AS bucket_start,
       (CASE sqlc.arg(by_kind)::text
            WHEN 'property' THEN COALESCE(e.properties->>sqlc.arg(by_key)::text, '(none)')
            WHEN 'flag' THEN COALESCE(e.flags->>sqlc.arg(by_key)::text, '(none)')
            ELSE '' END)::text AS breakdown,
       count(*)::bigint AS events,
       count(DISTINCT CASE WHEN sqlc.arg(unit)::text = 'subject' THEN encode(e.subject_hash, 'hex') ELSE e.customer_id::text END)::bigint AS units,
       COALESCE(sum(e.value), 0)::bigint AS value_sum
FROM analytics_events e
WHERE e.environment_id = sqlc.arg(environment_id)::uuid
  AND e.name = sqlc.arg(name)::text
  AND e.at >= sqlc.arg(range_from)::timestamptz AND e.at < sqlc.arg(range_until)::timestamptz
  AND e.properties @> sqlc.arg(filter)::jsonb
  AND (sqlc.arg(unit)::text <> 'subject' OR e.subject_hash IS NOT NULL)
GROUP BY 1, 2
ORDER BY 1, 2;

-- name: AnalyticsEntriesByCustomer :many
-- Each customer's first matching event in the range.
SELECT DISTINCT ON (e.customer_id) e.customer_id, e.at, e.properties, e.flags
FROM analytics_events e
WHERE e.environment_id = sqlc.arg(environment_id)::uuid
  AND e.name = sqlc.arg(name)::text
  AND e.at >= sqlc.arg(range_from)::timestamptz AND e.at < sqlc.arg(range_until)::timestamptz
  AND e.properties @> sqlc.arg(filter)::jsonb
ORDER BY e.customer_id, e.at
LIMIT sqlc.arg(max_rows)::int;

-- name: AnalyticsEntriesBySubject :many
-- Each subject's first matching event in the range.
SELECT DISTINCT ON (e.subject_hash) e.subject_hash, e.at, e.properties, e.flags
FROM analytics_events e
WHERE e.environment_id = sqlc.arg(environment_id)::uuid
  AND e.subject_hash IS NOT NULL
  AND e.name = sqlc.arg(name)::text
  AND e.at >= sqlc.arg(range_from)::timestamptz AND e.at < sqlc.arg(range_until)::timestamptz
  AND e.properties @> sqlc.arg(filter)::jsonb
ORDER BY e.subject_hash, e.at
LIMIT sqlc.arg(max_rows)::int;

-- name: AnalyticsNextStepCustomers :many
-- Each customer's first matching event after its previous step and by its
-- deadline. The arrays are parallel.
SELECT u.customer_id::uuid AS customer_id, min(e.at)::timestamptz AS at
FROM (
    SELECT unnest(sqlc.arg(customer_ids)::uuid[]) AS customer_id, unnest(sqlc.arg(after_ats)::timestamptz[]) AS after_at,
           unnest(sqlc.arg(deadlines)::timestamptz[]) AS deadline
) u
JOIN analytics_events e ON e.customer_id = u.customer_id AND e.at > u.after_at AND e.at <= u.deadline
WHERE e.environment_id = sqlc.arg(environment_id)::uuid
  AND e.name = sqlc.arg(name)::text
  AND e.properties @> sqlc.arg(filter)::jsonb
GROUP BY u.customer_id;

-- name: AnalyticsNextStepSubjects :many
SELECT u.subject_hash::bytea AS subject_hash, min(e.at)::timestamptz AS at
FROM (
    SELECT unnest(sqlc.arg(subject_hashes)::bytea[]) AS subject_hash, unnest(sqlc.arg(after_ats)::timestamptz[]) AS after_at,
           unnest(sqlc.arg(deadlines)::timestamptz[]) AS deadline
) u
JOIN analytics_events e ON e.environment_id = sqlc.arg(environment_id)::uuid AND e.subject_hash = u.subject_hash
     AND e.at > u.after_at AND e.at <= u.deadline
WHERE e.name = sqlc.arg(name)::text
  AND e.properties @> sqlc.arg(filter)::jsonb
GROUP BY u.subject_hash;

-- name: AnalyticsUnitFlagCustomers :many
-- Each customer's value for a flag on its earliest event in the range that
-- carries it.
SELECT DISTINCT ON (e.customer_id) e.customer_id, (e.flags->>sqlc.arg(flag)::text)::text AS value
FROM analytics_events e
WHERE e.customer_id = ANY(sqlc.arg(customer_ids)::uuid[])
  AND e.environment_id = sqlc.arg(environment_id)::uuid
  AND e.at >= sqlc.arg(range_from)::timestamptz AND e.at < sqlc.arg(range_until)::timestamptz
  AND e.flags->>sqlc.arg(flag)::text IS NOT NULL
ORDER BY e.customer_id, e.at;

-- name: AnalyticsUnitFlagSubjects :many
SELECT DISTINCT ON (e.subject_hash) e.subject_hash, (e.flags->>sqlc.arg(flag)::text)::text AS value
FROM analytics_events e
WHERE e.environment_id = sqlc.arg(environment_id)::uuid
  AND e.subject_hash = ANY(sqlc.arg(subject_hashes)::bytea[])
  AND e.at >= sqlc.arg(range_from)::timestamptz AND e.at < sqlc.arg(range_until)::timestamptz
  AND e.flags->>sqlc.arg(flag)::text IS NOT NULL
ORDER BY e.subject_hash, e.at;

-- name: AnalyticsReturnsCustomers :many
-- Customers of each cohort and breakdown value that sent the return event in
-- period k (1..periods) after their cohort's start. The arrays are parallel.
SELECT u.cohort::timestamptz AS cohort, u.breakdown::text AS breakdown, p.k::int AS period,
       count(DISTINCT u.customer_id)::bigint AS units
FROM (
    SELECT unnest(sqlc.arg(customer_ids)::uuid[]) AS customer_id, unnest(sqlc.arg(first_ats)::timestamptz[]) AS first_at,
           unnest(sqlc.arg(cohorts)::timestamptz[]) AS cohort, unnest(sqlc.arg(breakdowns)::text[]) AS breakdown
) u
JOIN analytics_events e ON e.customer_id = u.customer_id AND e.at > u.first_at
     AND e.at < u.cohort + make_interval(secs => sqlc.arg(period_seconds)::float8 * (sqlc.arg(periods)::int + 1))
CROSS JOIN LATERAL (SELECT floor(extract(epoch FROM e.at - u.cohort) / sqlc.arg(period_seconds)::float8)::int AS k) p
WHERE e.environment_id = sqlc.arg(environment_id)::uuid
  AND e.name = sqlc.arg(name)::text
  AND e.properties @> sqlc.arg(filter)::jsonb
  AND p.k BETWEEN 1 AND sqlc.arg(periods)::int
GROUP BY 1, 2, 3
ORDER BY 1, 2, 3;

-- name: AnalyticsReturnsSubjects :many
SELECT u.cohort::timestamptz AS cohort, u.breakdown::text AS breakdown, p.k::int AS period,
       count(DISTINCT u.subject_hash)::bigint AS units
FROM (
    SELECT unnest(sqlc.arg(subject_hashes)::bytea[]) AS subject_hash, unnest(sqlc.arg(first_ats)::timestamptz[]) AS first_at,
           unnest(sqlc.arg(cohorts)::timestamptz[]) AS cohort, unnest(sqlc.arg(breakdowns)::text[]) AS breakdown
) u
JOIN analytics_events e ON e.environment_id = sqlc.arg(environment_id)::uuid AND e.subject_hash = u.subject_hash
     AND e.at > u.first_at
     AND e.at < u.cohort + make_interval(secs => sqlc.arg(period_seconds)::float8 * (sqlc.arg(periods)::int + 1))
CROSS JOIN LATERAL (SELECT floor(extract(epoch FROM e.at - u.cohort) / sqlc.arg(period_seconds)::float8)::int AS k) p
WHERE e.name = sqlc.arg(name)::text
  AND e.properties @> sqlc.arg(filter)::jsonb
  AND p.k BETWEEN 1 AND sqlc.arg(periods)::int
GROUP BY 1, 2, 3
ORDER BY 1, 2, 3;

-- name: AnalyticsCustomerTotals :many
-- Each listed customer's matching events in [from, until): how many and
-- the sum of their values (CUPED pre-period, ADR 20261119102741 §2).
SELECT customer_id::uuid AS customer_id, count(*)::bigint AS events, COALESCE(sum(value), 0)::float8 AS total
FROM analytics_events
WHERE environment_id = sqlc.arg(environment_id)::uuid
  AND name = sqlc.arg(name)::text
  AND properties @> sqlc.arg(filter)::jsonb
  AND at >= sqlc.arg(from_at)::timestamptz AND at < sqlc.arg(until_at)::timestamptz
  AND customer_id = ANY(sqlc.arg(customer_ids)::uuid[])
GROUP BY customer_id;

-- name: AnalyticsCohortDid :many
-- Customers with at least min_count matching events since since_at, in
-- customer order, at most max_rows (ADR 20261119102741 §3).
SELECT customer_id::uuid AS customer_id
FROM analytics_events
WHERE environment_id = sqlc.arg(environment_id)::uuid
  AND name = sqlc.arg(name)::text
  AND properties @> sqlc.arg(filter)::jsonb
  AND at >= sqlc.arg(since_at)::timestamptz
GROUP BY customer_id
HAVING count(*) >= sqlc.arg(min_count)::bigint
ORDER BY customer_id
LIMIT sqlc.arg(max_rows)::int;

-- name: AnalyticsCohortDidNot :many
-- Customers with any event since active_since that sent no matching event
-- since since_at, in customer order, at most max_rows.
SELECT DISTINCT e.customer_id::uuid AS customer_id
FROM analytics_events e
WHERE e.environment_id = sqlc.arg(environment_id)::uuid
  AND e.at >= sqlc.arg(active_since)::timestamptz
  AND NOT EXISTS (
    SELECT 1 FROM analytics_events x
    WHERE x.environment_id = sqlc.arg(environment_id)::uuid AND x.customer_id = e.customer_id
      AND x.name = sqlc.arg(name)::text AND x.properties @> sqlc.arg(filter)::jsonb
      AND x.at >= sqlc.arg(since_at)::timestamptz)
ORDER BY 1
LIMIT sqlc.arg(max_rows)::int;

-- name: AnalyticsRequestsByFlag :many
-- Requests, server errors and latency histogram buckets (5% log buckets,
-- flags.LatencyBucket) per value of one flag, from request telemetry
-- received in [from, until) for the project environment's apps.
SELECT (decision.e->>'value')::text AS value,
       (CASE WHEN t.latency_ms <= 0 THEN 0 ELSE ceil(ln(t.latency_ms + 1) / ln(1.05)) END)::int AS bucket,
       sum(t.count)::bigint AS requests,
       COALESCE(sum(t.count) FILTER (WHERE t.status >= 500 AND NOT t.platform_refusal), 0)::bigint AS errors
FROM request_telemetry t
JOIN apps a ON a.id = t.app_id
JOIN deployments d ON d.id = t.deployment_id
CROSS JOIN LATERAL jsonb_array_elements(t.flag_evidence) AS decision(e)
WHERE t.account_id = sqlc.arg(account_id)::uuid
  AND a.project_id = sqlc.arg(project_id)::uuid
  AND (d.scope = sqlc.arg(environment_slug)::text OR (d.scope = 'default' AND sqlc.arg(environment_slug)::text = 'production'))
  AND t.received_at >= sqlc.arg(received_from)::timestamptz
  AND t.received_at < sqlc.arg(received_until)::timestamptz
  AND decision.e->>'flag' = sqlc.arg(flag)::text
  AND decision.e->>'value' IS NOT NULL
GROUP BY 1, 2
ORDER BY 1, 2;

-- name: DeleteExpiredAnalyticsEventsForPlan :execrows
-- Deletes up to max_rows of the oldest events of accounts on plan that are
-- older than cutoff.
DELETE FROM analytics_events
WHERE id IN (
    SELECT e.id FROM analytics_events e
    JOIN accounts a ON a.id = e.account_id
    WHERE e.at < sqlc.arg(cutoff)::timestamptz AND a.plan::text = sqlc.arg(plan)::text
    ORDER BY e.at
    LIMIT sqlc.arg(max_rows)::int
);

-- name: DeleteAnalyticsEventsOutsidePlans :execrows
-- Deletes up to max_rows events of accounts on a plan without analytics
-- retention in known_plans.
DELETE FROM analytics_events
WHERE id IN (
    SELECT e.id FROM analytics_events e
    JOIN accounts a ON a.id = e.account_id
    WHERE NOT (a.plan::text = ANY(sqlc.arg(known_plans)::text[]))
    ORDER BY e.at
    LIMIT sqlc.arg(max_rows)::int
);
