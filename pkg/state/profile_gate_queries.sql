-- name: ReadProfileGateContext :one
SELECT jsonb_build_object(
 'service',a.manifest->>'execution_mode'='service',
 'policy',CASE WHEN p.app_id IS NOT NULL THEN jsonb_build_object('app_id',p.app_id,'revision',p.revision,'config',p.config,'updated_at',p.updated_at) END,
 'signal',CASE WHEN j.deployment_id IS NOT NULL THEN j.data || jsonb_build_object('status',j.status,'reason',j.reason,'attempts',j.attempts,'next_attempt_at',j.next_attempt_at,'completed_at',j.completed_at) END,
 'stable_id',stable.stable_id,'stable_count',stable.stable_count,
 'active_count',(SELECT count(*) FROM deployments active WHERE active.app_id=d.app_id AND active.scope=d.scope AND active.status='live' AND active.deleted_at IS NULL AND active.traffic_percent>0)) AS context
FROM deployments d JOIN apps a ON a.id=d.app_id
LEFT JOIN profile_deployment_policies p ON p.app_id=d.app_id AND p.account_id=a.account_id
LEFT JOIN profile_canary_checks j ON j.deployment_id=d.id AND j.app_id=d.app_id AND j.account_id=a.account_id
 AND j.canary_step=d.canary_step AND j.canary_step_started_at=d.canary_step_started_at AND j.policy_revision=p.revision
LEFT JOIN LATERAL (
 SELECT count(b.id)::int AS stable_count,CASE WHEN count(b.id)=1 THEN min(b.id::text) END AS stable_id
 FROM deployments b WHERE b.app_id=d.app_id AND b.scope=d.scope AND b.id<>d.id AND b.status='live' AND b.traffic_percent>0 AND b.deleted_at IS NULL
 AND (b.canary_total_steps=0 OR b.canary_step>=b.canary_total_steps)
) stable ON true
WHERE d.id=sqlc.arg(deployment_id)::text::uuid AND d.app_id=sqlc.arg(app_id)::text::uuid AND a.account_id=sqlc.arg(account_id)::text::uuid AND a.status<>'deleted';

-- name: AbortProfileGatedCanary :execrows
UPDATE deployments SET traffic_percent=0,rollout_state='aborted',rollout_aborted_at=sqlc.arg(observed_at)::timestamptz,
 rollout_aborted_reason=sqlc.arg(reason)::text
WHERE id=sqlc.arg(deployment_id)::text::uuid AND app_id=sqlc.arg(app_id)::text::uuid AND status='live'
 AND canary_step=sqlc.arg(canary_step)::int AND canary_step_started_at=sqlc.arg(canary_step_started_at)::timestamptz
 AND rollout_state IN ('pending','rolling_out');

-- name: RestoreProfileGateStableTraffic :execrows
UPDATE deployments SET traffic_percent=100
WHERE id=sqlc.arg(deployment_id)::text::uuid AND app_id=sqlc.arg(app_id)::text::uuid AND scope=sqlc.arg(scope)::text
 AND status='live' AND deleted_at IS NULL AND traffic_percent>0;

-- name: AppendProfileGateRollbackAudit :one
INSERT INTO deployment_audit(deployment_id,account_id,kind,actor,at,data)
VALUES(sqlc.arg(deployment_id)::text::uuid,sqlc.arg(account_id)::text::uuid,'deploy.rollout_aborted',sqlc.arg(actor)::text,sqlc.arg(observed_at)::timestamptz,sqlc.arg(data)::jsonb)
RETURNING id;
