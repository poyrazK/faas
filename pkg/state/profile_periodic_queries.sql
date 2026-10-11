-- name: DiscoverProfilePeriodicCandidates :many
SELECT jsonb_build_object('deployment_id',d.id,'app_id',d.app_id,'account_id',p.account_id,'scope',d.scope,'completed_at',d.rollout_completed_at,'route',r.route,
 'policy',jsonb_build_object('app_id',p.app_id,'revision',p.revision,'config',p.config,'updated_at',p.updated_at)) AS candidate
FROM deployments d JOIN apps a ON a.id=d.app_id JOIN profile_deployment_policies p ON p.app_id=a.id JOIN accounts ac ON ac.id=p.account_id
CROSS JOIN LATERAL jsonb_array_elements_text(p.config->'options'->'routes') r(route)
WHERE p.enabled AND jsonb_typeof(p.config->'periodic')='object' AND a.account_id=p.account_id AND a.status<>'deleted'
 AND ac.plan IN ('hobby','pro','scale') AND ac.status IN ('active','past_due') AND ac.abuse_hold_at IS NULL
 AND d.status='live' AND d.rollout_state='complete' AND d.deleted_at IS NULL AND d.rollout_completed_at IS NOT NULL
 AND NOT EXISTS(SELECT 1 FROM profile_periodic_monitors j WHERE j.deployment_id=d.id AND j.policy_revision=p.revision AND j.route=r.route)
ORDER BY d.id,r.route LIMIT sqlc.arg(max_rows)::int;

-- name: EnqueueProfilePeriodicMonitor :exec
INSERT INTO profile_periodic_monitors(id,app_id,account_id,deployment_id,policy_revision,route,data,next_attempt_at,updated_at)
SELECT sqlc.arg(id)::text::uuid,a.id,a.account_id,d.id,p.revision,sqlc.arg(route)::text,sqlc.arg(data)::jsonb,sqlc.arg(due)::timestamptz,sqlc.arg(observed_at)::timestamptz
FROM deployments d JOIN apps a ON a.id=d.app_id JOIN profile_deployment_policies p ON p.app_id=a.id
WHERE d.id=sqlc.arg(deployment_id)::text::uuid AND a.id=sqlc.arg(app_id)::text::uuid AND a.account_id=sqlc.arg(account_id)::text::uuid AND a.status<>'deleted'
 AND d.status='live' AND d.rollout_state='complete' AND d.deleted_at IS NULL AND p.revision=sqlc.arg(policy_revision) AND p.enabled AND jsonb_typeof(p.config->'periodic')='object'
 AND p.config->'options'->'routes' ? sqlc.arg(route)::text
ON CONFLICT(deployment_id,policy_revision,route) DO NOTHING;

-- name: ClaimProfilePeriodicMonitor :one
WITH picked AS (
 SELECT j.id FROM profile_periodic_monitors j JOIN deployments d ON d.id=j.deployment_id JOIN apps a ON a.id=j.app_id
 JOIN profile_deployment_policies p ON p.app_id=j.app_id JOIN accounts ac ON ac.id=j.account_id
 WHERE j.next_attempt_at<=sqlc.arg(observed_at)::timestamptz AND (j.lease_until IS NULL OR j.lease_until<=sqlc.arg(observed_at)::timestamptz)
 AND p.enabled AND jsonb_typeof(p.config->'periodic')='object' AND p.revision=j.policy_revision AND p.config->'options'->'routes' ? j.route
 AND a.account_id=j.account_id AND a.status<>'deleted' AND d.app_id=a.id AND d.status='live' AND d.rollout_state='complete' AND d.deleted_at IS NULL
 AND ac.plan IN ('hobby','pro','scale') AND ac.status IN ('active','past_due') AND ac.abuse_hold_at IS NULL
 ORDER BY j.next_attempt_at,j.id FOR UPDATE OF j SKIP LOCKED LIMIT 1
)
UPDATE profile_periodic_monitors j SET lease_token=sqlc.arg(token)::text::uuid,lease_until=sqlc.arg(lease_until)::timestamptz,attempts=LEAST(j.attempts+1,6)
FROM picked WHERE j.id=picked.id
RETURNING jsonb_build_object('monitor',j.data || jsonb_build_object('attempts',j.attempts,'next_attempt_at',j.next_attempt_at),'account_id',j.account_id) AS work;

-- name: LockProfilePeriodicMonitor :one
SELECT (j.data || jsonb_build_object('attempts',j.attempts,'next_attempt_at',j.next_attempt_at))::jsonb AS monitor
FROM profile_periodic_monitors j JOIN deployments d ON d.id=j.deployment_id JOIN profile_deployment_policies p ON p.app_id=j.app_id JOIN accounts ac ON ac.id=j.account_id
WHERE j.id=sqlc.arg(id)::text::uuid AND j.app_id=sqlc.arg(app_id)::text::uuid AND j.account_id=sqlc.arg(account_id)::text::uuid
 AND j.lease_token=sqlc.arg(token)::text::uuid AND j.lease_until>sqlc.arg(observed_at)::timestamptz
 AND d.status='live' AND d.rollout_state='complete' AND d.deleted_at IS NULL AND p.enabled AND p.revision=j.policy_revision AND jsonb_typeof(p.config->'periodic')='object'
 AND ac.plan IN ('hobby','pro','scale') AND ac.status IN ('active','past_due') AND ac.abuse_hold_at IS NULL
FOR UPDATE OF j;

-- name: FinishProfilePeriodicMonitor :exec
UPDATE profile_periodic_monitors SET data=sqlc.arg(data)::jsonb,next_attempt_at=sqlc.arg(due)::timestamptz,attempts=sqlc.arg(attempts)::int,
 lease_token=NULL,lease_until=NULL,updated_at=sqlc.arg(observed_at)::timestamptz
WHERE id=sqlc.arg(id)::text::uuid AND lease_token=sqlc.arg(token)::text::uuid;

-- name: ListProfilePeriodicMonitors :many
SELECT (j.data || jsonb_build_object('attempts',j.attempts,'next_attempt_at',j.next_attempt_at,'active',coalesce(p.enabled AND p.revision=j.policy_revision AND jsonb_typeof(p.config->'periodic')='object' AND d.status='live' AND d.rollout_state='complete' AND d.deleted_at IS NULL AND ac.plan IN ('hobby','pro','scale') AND ac.status IN ('active','past_due') AND ac.abuse_hold_at IS NULL,false)))::jsonb AS monitor
FROM profile_periodic_monitors j JOIN apps a ON a.id=j.app_id JOIN deployments d ON d.id=j.deployment_id JOIN accounts ac ON ac.id=j.account_id LEFT JOIN profile_deployment_policies p ON p.app_id=j.app_id
WHERE j.app_id=sqlc.arg(app_id)::text::uuid AND j.account_id=sqlc.arg(account_id)::text::uuid AND a.account_id=j.account_id AND a.status<>'deleted'
ORDER BY j.updated_at DESC,j.id LIMIT sqlc.arg(max_rows)::int;

-- name: PruneProfilePeriodicMonitors :exec
DELETE FROM profile_periodic_monitors WHERE id IN (
 SELECT j.id FROM profile_periodic_monitors j WHERE j.updated_at<sqlc.arg(cutoff)::timestamptz
 AND NOT EXISTS(SELECT 1 FROM profile_deployment_policies p JOIN deployments d ON d.app_id=p.app_id JOIN accounts ac ON ac.id=p.account_id
 WHERE d.id=j.deployment_id AND d.status='live' AND d.rollout_state='complete' AND d.deleted_at IS NULL AND ac.plan IN ('hobby','pro','scale') AND ac.status IN ('active','past_due') AND ac.abuse_hold_at IS NULL AND p.enabled AND p.revision=j.policy_revision AND jsonb_typeof(p.config->'periodic')='object' AND p.config->'options'->'routes' ? j.route)
 ORDER BY j.updated_at,j.id LIMIT sqlc.arg(max_rows)::int
);
