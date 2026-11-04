-- ADR-20261103131742 amendment 2: the monthly build-minute quota.

-- Build time a billing subject's source builds ran in [period_start,
-- period_end): each started build counts from its start to its finish, or to
-- now while it runs. A company app's builds count for its organization when
-- the organization owns its resources, as its usage does. A build deferred
-- behind an identical build has its claim's started_at cleared, so only the
-- time it really runs counts.
-- name: BillingSubjectBuildSeconds :one
SELECT COALESCE(sum(GREATEST(0, extract(epoch FROM (
    COALESCE(b.finished_at, LEAST(now(), sqlc.arg(period_end)::timestamptz)) - b.started_at)))), 0)::bigint AS build_seconds
FROM apps a
JOIN deployments d ON d.app_id = a.id
JOIN builds b ON b.deployment_id = d.id
WHERE (a.account_id = sqlc.arg(subject_id)::uuid OR a.org_id = sqlc.arg(subject_id)::uuid)
  AND COALESCE(managed_postgres_usage_subject(a.org_id, a.account_id), a.account_id) = sqlc.arg(subject_id)::uuid
  AND b.started_at >= sqlc.arg(period_start)::timestamptz
  AND b.started_at < sqlc.arg(period_end)::timestamptz;
