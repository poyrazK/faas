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
    metric_event, mixing_lift, weights, created_by)
SELECT sqlc.arg(id)::uuid, sqlc.arg(account_id)::uuid, sqlc.arg(project_id)::uuid, sqlc.arg(environment_id)::uuid,
    sqlc.arg(key)::text, sqlc.arg(flag_key)::text, sqlc.arg(rule_id)::text, sqlc.arg(control)::text,
    sqlc.arg(metric_event)::text, sqlc.arg(mixing_lift)::double precision, sqlc.arg(weights)::jsonb, sqlc.arg(created_by)::text
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
-- Starts a draft while the environment runs fewer than max_running.
UPDATE flag_experiments x
SET status = 'running', started_at = sqlc.arg(now)::timestamptz, telemetry_cursor = sqlc.arg(now)::timestamptz,
    weights = sqlc.arg(weights)::jsonb
WHERE x.environment_id = sqlc.arg(environment_id)::uuid AND x.key = sqlc.arg(key)::text AND x.status = 'draft'
  AND (SELECT count(*) FROM flag_experiments r WHERE r.environment_id = sqlc.arg(environment_id)::uuid AND r.status = 'running') < sqlc.arg(max_running)::bigint
RETURNING *;

-- name: StopFlagExperiment :one
UPDATE flag_experiments
SET status = 'stopped', stopped_at = sqlc.arg(now)::timestamptz
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
-- First and last exposure per verified customer and variant to the
-- experiment's rule in request telemetry received in (from, until].
SELECT t.platform_tenant_id::uuid AS customer_id, (e->>'value')::text AS variant,
       min(t.received_at)::timestamptz AS first_at
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
  AND jsonb_typeof(e->'value') = 'string'
GROUP BY 1, 2
ORDER BY 3
LIMIT sqlc.arg(max_rows)::int;

-- name: AddFlagExperimentUnits :exec
-- A customer seen with another variant than its first is marked crossed and
-- left out of results.
-- The arrays are parallel: the select list unnests them in step.
INSERT INTO flag_experiment_units (experiment_id, customer_id, variant, first_exposed_at, crossed)
SELECT sqlc.arg(experiment_id)::uuid, unnest(sqlc.arg(customer_ids)::uuid[]), unnest(sqlc.arg(variants)::text[]),
       unnest(sqlc.arg(first_ats)::timestamptz[]), unnest(sqlc.arg(crossed)::boolean[])
ON CONFLICT (experiment_id, customer_id) DO UPDATE
SET crossed = flag_experiment_units.crossed OR EXCLUDED.crossed OR flag_experiment_units.variant <> EXCLUDED.variant;

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
SET telemetry_cursor = sqlc.arg(telemetry_cursor)::timestamptz, min_p_values = sqlc.arg(min_p_values)::jsonb
WHERE id = sqlc.arg(id)::uuid AND status = 'running';
