-- Analytics cohorts (ADR 20261119102741 §3–4).

-- name: CreateAnalyticsCohort :one
-- Creates a cohort while the environment holds fewer than max_cohorts.
INSERT INTO analytics_cohorts (account_id, environment_id, key, definition, created_by)
SELECT sqlc.arg(account_id)::uuid, sqlc.arg(environment_id)::uuid, sqlc.arg(key)::text,
       sqlc.arg(definition)::jsonb, sqlc.arg(created_by)::text
WHERE (SELECT count(*) FROM analytics_cohorts c WHERE c.environment_id = sqlc.arg(environment_id)::uuid) < sqlc.arg(max_cohorts)::bigint
RETURNING *;

-- name: GetAnalyticsCohort :one
SELECT * FROM analytics_cohorts
WHERE environment_id = sqlc.arg(environment_id)::uuid AND key = sqlc.arg(key)::text;

-- name: ListAnalyticsCohorts :many
SELECT * FROM analytics_cohorts
WHERE environment_id = sqlc.arg(environment_id)::uuid
ORDER BY key;

-- name: DeleteAnalyticsCohort :execrows
DELETE FROM analytics_cohorts
WHERE environment_id = sqlc.arg(environment_id)::uuid AND key = sqlc.arg(key)::text;

-- name: RequestAnalyticsCohortRefresh :one
UPDATE analytics_cohorts SET refresh_requested_at = sqlc.arg(now)::timestamptz
WHERE environment_id = sqlc.arg(environment_id)::uuid AND key = sqlc.arg(key)::text
RETURNING *;

-- name: DueAnalyticsCohorts :many
-- Cohorts whose refresh was requested or whose last refresh is older than
-- stale_before, oldest request first, with the account's plan.
SELECT c.*, a.plan::text AS plan
FROM analytics_cohorts c
JOIN accounts a ON a.id = c.account_id
WHERE c.refreshed_at IS NULL OR c.refresh_requested_at > c.refreshed_at
   OR c.refreshed_at < sqlc.arg(stale_before)::timestamptz
ORDER BY c.refresh_requested_at, c.id
LIMIT sqlc.arg(max_rows)::int;

-- name: DeleteAnalyticsCohortMembers :exec
DELETE FROM analytics_cohort_members WHERE cohort_id = sqlc.arg(cohort_id)::uuid;

-- name: InsertAnalyticsCohortMembers :exec
-- Members must be platform tenants of the cohort's account.
INSERT INTO analytics_cohort_members (cohort_id, customer_id)
SELECT sqlc.arg(cohort_id)::uuid, t.id
FROM platform_tenants t
WHERE t.id = ANY(sqlc.arg(customer_ids)::uuid[]) AND t.account_id = sqlc.arg(account_id)::uuid
ON CONFLICT DO NOTHING;

-- name: FinishAnalyticsCohortRefresh :exec
UPDATE analytics_cohorts
SET members = (SELECT count(*) FROM analytics_cohort_members m WHERE m.cohort_id = sqlc.arg(id)::uuid),
    truncated = sqlc.arg(truncated)::boolean,
    -- At least the request this refresh served, so clock skew between apid
    -- and Postgres cannot leave the cohort due; a later request stays due.
    refreshed_at = GREATEST(sqlc.arg(now)::timestamptz, sqlc.arg(served_request)::timestamptz),
    refresh_error = sqlc.arg(refresh_error)::text
WHERE id = sqlc.arg(id)::uuid;

-- name: FailAnalyticsCohortRefresh :exec
UPDATE analytics_cohorts
-- A failed refresh waits for the hourly retry, not the next pass.
SET refreshed_at = GREATEST(sqlc.arg(now)::timestamptz, refresh_requested_at), refresh_error = sqlc.arg(refresh_error)::text
WHERE id = sqlc.arg(id)::uuid;

-- name: AnalyticsCohortsForDeploymentCustomer :many
-- The keys of the cohorts of a deployment's project environment (scope ""
-- or "default" reads production) that contain the customer.
SELECT c.key
FROM deployments d
JOIN apps a ON a.id = d.app_id AND a.project_id IS NOT NULL
JOIN project_environments e ON e.project_id = a.project_id
 AND e.slug = CASE WHEN d.scope IN ('', 'default') THEN 'production' ELSE d.scope END
JOIN analytics_cohorts c ON c.environment_id = e.id
JOIN analytics_cohort_members m ON m.cohort_id = c.id AND m.customer_id = sqlc.arg(customer_id)::uuid
WHERE d.id = sqlc.arg(deployment_id)::uuid
ORDER BY c.key;

-- name: NotifyAnalyticsCohortsChanged :exec
SELECT pg_notify('analytics_cohorts_changed', sqlc.arg(environment_id)::text);
