-- All customer-intent writes are serialized by the owned app lock.
-- name: ReadProfileInvestigation :one
SELECT jsonb_build_object('id', p.id, 'app_id', p.app_id, 'revision', p.revision, 'investigation', p.investigation, 'created_at', p.created_at, 'updated_at', p.updated_at, 'assessment', p.assessment) AS saved
FROM profile_investigations p JOIN apps a ON a.id = p.app_id
WHERE p.id = sqlc.arg(id)::text::uuid AND p.app_id = sqlc.arg(app_id)::text::uuid
AND p.account_id = sqlc.arg(account_id)::text::uuid AND a.account_id = p.account_id AND a.status <> 'deleted';

-- name: ListProfileInvestigations :many
SELECT jsonb_build_object('id', p.id, 'app_id', p.app_id, 'revision', p.revision, 'investigation', p.investigation, 'created_at', p.created_at, 'updated_at', p.updated_at, 'assessment', p.assessment) AS saved
FROM profile_investigations p JOIN apps a ON a.id = p.app_id
WHERE p.app_id = sqlc.arg(app_id)::text::uuid AND p.account_id = sqlc.arg(account_id)::text::uuid AND a.account_id = p.account_id AND a.status <> 'deleted'
ORDER BY p.updated_at DESC, p.id LIMIT sqlc.arg(max_rows)::int;

-- name: ProfileInvestigationAppOwned :one
SELECT EXISTS (SELECT 1 FROM apps WHERE id = sqlc.arg(app_id)::text::uuid AND account_id = sqlc.arg(account_id)::text::uuid AND status <> 'deleted');

-- name: ProfileInvestigationDeploymentOwned :one
SELECT EXISTS (SELECT 1 FROM deployments WHERE id = sqlc.arg(id)::text::uuid AND app_id = sqlc.arg(app_id)::text::uuid);

-- name: CountProfileInvestigations :one
SELECT count(*) FROM profile_investigations WHERE app_id = sqlc.arg(app_id)::text::uuid;

-- name: WriteProfileInvestigation :exec
INSERT INTO profile_investigations (id, app_id, account_id, revision, investigation, assessment)
VALUES (sqlc.arg(id)::text::uuid, sqlc.arg(app_id)::text::uuid, sqlc.arg(account_id)::text::uuid, sqlc.arg(revision), sqlc.arg(investigation), sqlc.narg(assessment)::jsonb)
ON CONFLICT (id) DO UPDATE SET revision = EXCLUDED.revision, investigation = EXCLUDED.investigation, assessment = COALESCE(EXCLUDED.assessment,profile_investigations.assessment), updated_at = now()
WHERE profile_investigations.account_id = EXCLUDED.account_id AND profile_investigations.app_id = EXCLUDED.app_id;

-- name: DeleteProfileInvestigation :execrows
DELETE FROM profile_investigations WHERE id = sqlc.arg(id)::text::uuid AND app_id = sqlc.arg(app_id)::text::uuid AND account_id = sqlc.arg(account_id)::text::uuid AND revision = sqlc.arg(revision);

-- name: WriteProfileRegressionAssessment :exec
UPDATE profile_investigations SET assessment = sqlc.arg(assessment)::jsonb, revision = revision + 1, updated_at = now()
WHERE id = sqlc.arg(id)::text::uuid AND app_id = sqlc.arg(app_id)::text::uuid AND account_id = sqlc.arg(account_id)::text::uuid AND revision = sqlc.arg(revision);

-- name: ProfileRequestCount :one
SELECT COALESCE(SUM(count), 0)::bigint AS requests, COUNT(*)::bigint AS observations
FROM request_telemetry
WHERE account_id = sqlc.arg(account_id)::text::uuid AND app_id = sqlc.arg(app_id)::text::uuid
  AND deployment_id = sqlc.arg(deployment_id)::text::uuid
  AND (sqlc.arg(route)::text='' OR route=sqlc.arg(route)::text)
  AND received_at >= sqlc.arg(start_at)::timestamptz AND received_at < sqlc.arg(end_at)::timestamptz;

-- name: ReadProfileDeploymentPolicy :one
SELECT jsonb_build_object('app_id', p.app_id, 'revision', p.revision, 'config', p.config, 'updated_at', p.updated_at) AS policy
FROM profile_deployment_policies p JOIN apps a ON a.id=p.app_id
WHERE p.app_id=sqlc.arg(app_id)::text::uuid AND p.account_id=sqlc.arg(account_id)::text::uuid AND a.account_id=p.account_id AND a.status<>'deleted';

-- name: WriteProfileDeploymentPolicy :exec
INSERT INTO profile_deployment_policies(app_id,account_id,revision,enabled,config)
VALUES(sqlc.arg(app_id)::text::uuid,sqlc.arg(account_id)::text::uuid,sqlc.arg(revision),sqlc.arg(enabled),sqlc.arg(config)::jsonb)
ON CONFLICT(app_id) DO UPDATE SET revision=EXCLUDED.revision,enabled=EXCLUDED.enabled,config=EXCLUDED.config,updated_at=now();

-- name: CancelProfileDeploymentChecks :exec
UPDATE profile_deployment_checks SET status='cancelled',reason='Automatic profiling policy changed or was disabled.',completed_at=now(),next_attempt_at=NULL,lease_token=NULL,lease_until=NULL
WHERE app_id=sqlc.arg(app_id)::text::uuid AND status IN ('queued','running');

-- name: DiscoverProfileDeploymentCandidates :many
SELECT jsonb_build_object('deployment_id', d.id, 'app_id', d.app_id, 'account_id', p.account_id, 'scope', d.scope, 'created_at', d.created_at, 'completed_at', d.rollout_completed_at,
 'policy',jsonb_build_object('app_id',p.app_id,'revision',p.revision,'config',p.config,'updated_at',p.updated_at),
 'baseline_id',(SELECT b.id FROM deployments b WHERE b.app_id=d.app_id AND b.scope=d.scope AND b.id<>d.id AND b.status IN ('live','superseded') AND b.rollout_state='complete' AND b.deleted_at IS NULL AND b.rollout_completed_at<d.created_at ORDER BY b.rollout_completed_at DESC,b.id DESC LIMIT 1)) AS candidate
FROM deployments d JOIN profile_deployment_policies p ON p.app_id=d.app_id JOIN apps a ON a.id=d.app_id JOIN accounts ac ON ac.id=p.account_id
WHERE p.enabled AND a.account_id=p.account_id AND a.status<>'deleted' AND ac.plan IN ('hobby','pro','scale')
 AND d.status IN ('live','superseded') AND d.rollout_state='complete' AND d.deleted_at IS NULL
 AND d.rollout_completed_at>=p.updated_at AND d.rollout_completed_at>=sqlc.arg(earliest)::timestamptz AND d.rollout_completed_at<=sqlc.arg(observed_at)::timestamptz
 AND NOT EXISTS(SELECT 1 FROM profile_deployment_checks j WHERE j.deployment_id=d.id)
ORDER BY d.rollout_completed_at,d.id LIMIT sqlc.arg(batch_limit)::int;

-- name: EnqueueProfileDeploymentCheck :execrows
INSERT INTO profile_deployment_checks(deployment_id,app_id,account_id,policy_revision,data,next_attempt_at,created_at,reason)
SELECT sqlc.arg(deployment_id)::text::uuid,p.app_id,p.account_id,p.revision,sqlc.arg(data)::jsonb,sqlc.arg(due)::timestamptz,sqlc.arg(observed_at)::timestamptz,'Waiting for the capture window and profile ingestion.'
FROM profile_deployment_policies p JOIN apps a ON a.id=p.app_id
WHERE p.app_id=sqlc.arg(app_id)::text::uuid AND p.account_id=sqlc.arg(account_id)::text::uuid AND p.enabled AND p.revision=sqlc.arg(policy_revision) AND a.status<>'deleted' AND a.account_id=p.account_id
ON CONFLICT(deployment_id) DO NOTHING;

-- name: ListProfileDeploymentChecks :many
SELECT (j.data || jsonb_build_object('status',j.status,'reason',j.reason,'attempts',j.attempts,'next_attempt_at',j.next_attempt_at,'completed_at',j.completed_at,'investigation_id',coalesce(j.investigation_id::text,'')))::jsonb AS receipt
FROM profile_deployment_checks j JOIN apps a ON a.id=j.app_id
WHERE j.app_id=sqlc.arg(app_id)::text::uuid AND j.account_id=sqlc.arg(account_id)::text::uuid AND a.account_id=j.account_id AND a.status<>'deleted'
ORDER BY j.created_at DESC,j.deployment_id LIMIT sqlc.arg(max_rows)::int;

-- name: ReadProfileDeploymentCheck :one
SELECT (j.data || jsonb_build_object('status',j.status,'reason',j.reason,'attempts',j.attempts,'next_attempt_at',j.next_attempt_at,'completed_at',j.completed_at,'investigation_id',coalesce(j.investigation_id::text,'')))::jsonb AS receipt
FROM profile_deployment_checks j JOIN apps a ON a.id=j.app_id
WHERE j.deployment_id=sqlc.arg(deployment_id)::text::uuid AND j.app_id=sqlc.arg(app_id)::text::uuid AND j.account_id=sqlc.arg(account_id)::text::uuid AND a.account_id=j.account_id AND a.status<>'deleted';

-- name: ClaimProfileDeploymentCheck :one
WITH next_job AS (
 SELECT j.deployment_id FROM profile_deployment_checks j JOIN profile_deployment_policies p ON p.app_id=j.app_id JOIN apps a ON a.id=j.app_id
 WHERE j.status IN ('queued','running') AND j.next_attempt_at<=sqlc.arg(observed_at)::timestamptz
 AND (j.lease_until IS NULL OR j.lease_until<=sqlc.arg(observed_at)::timestamptz)
 AND j.attempts<sqlc.arg(max_attempts)::int AND p.enabled AND p.revision=j.policy_revision AND a.status<>'deleted' AND a.account_id=j.account_id
 ORDER BY j.next_attempt_at,j.deployment_id FOR UPDATE OF j SKIP LOCKED LIMIT 1
)
UPDATE profile_deployment_checks j SET status='running',attempts=attempts+1,lease_token=sqlc.arg(token)::text::uuid,lease_until=sqlc.arg(lease_until)::timestamptz
FROM next_job n WHERE j.deployment_id=n.deployment_id
RETURNING (j.data || jsonb_build_object('status',j.status,'reason',j.reason,'attempts',j.attempts,'next_attempt_at',j.next_attempt_at))::jsonb AS receipt,j.account_id::text AS account_id;

-- name: LockClaimedProfileDeploymentCheck :one
SELECT (j.data || jsonb_build_object('status',j.status,'reason',j.reason,'attempts',j.attempts,'next_attempt_at',j.next_attempt_at))::jsonb AS receipt
FROM profile_deployment_checks j WHERE j.deployment_id=sqlc.arg(deployment_id)::text::uuid AND j.app_id=sqlc.arg(app_id)::text::uuid AND j.account_id=sqlc.arg(account_id)::text::uuid
 AND j.status='running' AND j.lease_token=sqlc.arg(token)::text::uuid AND j.lease_until>sqlc.arg(observed_at)::timestamptz FOR UPDATE;

-- name: FinishProfileDeploymentCheck :exec
UPDATE profile_deployment_checks SET status=sqlc.arg(status),reason=sqlc.arg(reason),next_attempt_at=sqlc.narg(next_attempt_at)::timestamptz,completed_at=sqlc.narg(completed_at)::timestamptz,
 investigation_id=sqlc.narg(investigation_id)::text::uuid,lease_token=NULL,lease_until=NULL
WHERE deployment_id=sqlc.arg(deployment_id)::text::uuid AND lease_token=sqlc.arg(token)::text::uuid;

-- name: ExpireProfileDeploymentChecks :exec
UPDATE profile_deployment_checks j SET status=CASE WHEN NOT p.enabled OR p.revision<>j.policy_revision OR a.status='deleted' THEN 'cancelled' ELSE 'inconclusive' END,
 reason=CASE WHEN NOT p.enabled OR p.revision<>j.policy_revision OR a.status='deleted' THEN 'Automatic profiling policy changed or was disabled.' ELSE 'The worker could not finish within its attempt limit.' END,
 completed_at=sqlc.arg(observed_at)::timestamptz,next_attempt_at=NULL,lease_token=NULL,lease_until=NULL
FROM profile_deployment_policies p,apps a WHERE p.app_id=j.app_id AND a.id=j.app_id AND j.status IN ('queued','running')
 AND (NOT p.enabled OR p.revision<>j.policy_revision OR a.status='deleted' OR (j.attempts>=sqlc.arg(max_attempts)::int AND (j.lease_until IS NULL OR j.lease_until<=sqlc.arg(observed_at)::timestamptz)));

-- name: PruneProfileDeploymentChecks :exec
DELETE FROM profile_deployment_checks WHERE deployment_id IN (SELECT deployment_id FROM profile_deployment_checks WHERE completed_at<sqlc.arg(cutoff)::timestamptz ORDER BY completed_at,deployment_id LIMIT sqlc.arg(batch_limit)::int);

-- Profile canary assessments are deduplicated for one deployment stage
-- instance and one automatic-profile policy revision.
-- name: ReadProfileCanaryHistoryTarget :one
SELECT true AS available
FROM apps a JOIN deployments d ON d.app_id=a.id
WHERE a.id=sqlc.arg(app_id)::text::uuid AND a.account_id=sqlc.arg(account_id)::text::uuid AND a.status<>'deleted'
  AND d.id=sqlc.arg(deployment_id)::text::uuid AND d.deleted_at IS NULL AND d.canary_total_steps>0;

-- name: ReadProfileCanaryHistoryCursor :one
SELECT true AS available
FROM profile_canary_checks j JOIN apps a ON a.id=j.app_id
WHERE j.deployment_id=sqlc.arg(deployment_id)::text::uuid AND j.app_id=sqlc.arg(app_id)::text::uuid AND j.account_id=sqlc.arg(account_id)::text::uuid
  AND j.canary_step=sqlc.arg(canary_step)::int AND j.canary_step_started_at=sqlc.arg(canary_step_started_at)::timestamptz AND j.policy_revision=sqlc.arg(policy_revision)::bigint
  AND a.account_id=j.account_id AND a.status<>'deleted';

-- name: ListProfileCanaryChecks :many
SELECT (j.data || jsonb_build_object('status',j.status,'reason',j.reason,'attempts',j.attempts,'next_attempt_at',j.next_attempt_at,'created_at',j.created_at,'completed_at',j.completed_at))::jsonb AS signal
FROM profile_canary_checks j JOIN apps a ON a.id=j.app_id
WHERE j.deployment_id=sqlc.arg(deployment_id)::text::uuid AND j.app_id=sqlc.arg(app_id)::text::uuid AND j.account_id=sqlc.arg(account_id)::text::uuid
  AND a.account_id=j.account_id AND a.status<>'deleted'
  AND (sqlc.narg(before_started_at)::timestamptz IS NULL OR (j.canary_step_started_at,j.canary_step,j.policy_revision) < (sqlc.narg(before_started_at)::timestamptz,sqlc.narg(before_step)::int,sqlc.narg(before_policy_revision)::bigint))
ORDER BY j.canary_step_started_at DESC,j.canary_step DESC,j.policy_revision DESC LIMIT sqlc.arg(page_limit)::int;

-- name: ReadLatestProfileCanaryCheck :one
SELECT (j.data || jsonb_build_object('status',j.status,'reason',j.reason,'attempts',j.attempts,'next_attempt_at',j.next_attempt_at,'created_at',j.created_at,'completed_at',j.completed_at))::jsonb AS signal
FROM profile_canary_checks j JOIN apps a ON a.id=j.app_id
WHERE j.deployment_id=sqlc.arg(deployment_id)::text::uuid AND j.app_id=sqlc.arg(app_id)::text::uuid AND j.account_id=sqlc.arg(account_id)::text::uuid
  AND j.policy_revision=sqlc.arg(policy_revision)::bigint AND a.account_id=j.account_id AND a.status<>'deleted'
ORDER BY j.canary_step_started_at DESC,j.canary_step DESC LIMIT 1;

-- name: ReadProfileCanaryCheck :one
SELECT (j.data || jsonb_build_object('status',j.status,'reason',j.reason,'attempts',j.attempts,'next_attempt_at',j.next_attempt_at,'created_at',j.created_at,'completed_at',j.completed_at))::jsonb AS signal
FROM profile_canary_checks j JOIN apps a ON a.id=j.app_id
WHERE j.deployment_id=sqlc.arg(deployment_id)::text::uuid AND j.app_id=sqlc.arg(app_id)::text::uuid AND j.account_id=sqlc.arg(account_id)::text::uuid
  AND j.canary_step=sqlc.arg(canary_step)::int AND j.canary_step_started_at=sqlc.arg(canary_step_started_at)::timestamptz AND j.policy_revision=sqlc.arg(policy_revision)::bigint
  AND a.account_id=j.account_id AND a.status<>'deleted';

-- name: DiscoverProfileCanaryCandidates :many
SELECT jsonb_build_object('deployment_id',d.id,'app_id',d.app_id,'account_id',p.account_id,'scope',d.scope,'canary_step',d.canary_step,'canary_step_started_at',d.canary_step_started_at,
 'policy',jsonb_build_object('app_id',p.app_id,'revision',p.revision,'config',p.config,'updated_at',p.updated_at),
 'stable_count',COALESCE(stable.stable_count,0),'stable_id',stable.stable_id) AS candidate
FROM deployments d JOIN profile_deployment_policies p ON p.app_id=d.app_id JOIN apps a ON a.id=d.app_id JOIN accounts ac ON ac.id=p.account_id
LEFT JOIN LATERAL (
 SELECT count(b.id)::int AS stable_count, CASE WHEN count(b.id)=1 THEN min(b.id::text) END AS stable_id
 FROM deployments b WHERE b.app_id=d.app_id AND b.scope=d.scope AND b.id<>d.id AND b.status='live' AND b.traffic_percent>0 AND b.deleted_at IS NULL
  AND (b.canary_total_steps=0 OR b.canary_step>=b.canary_total_steps)
) stable ON true
WHERE p.enabled AND a.account_id=p.account_id AND a.status<>'deleted' AND ac.plan IN ('hobby','pro','scale')
 AND a.manifest #>> '{profiling,enabled}'='true'
 AND d.status='live' AND d.traffic_percent>0 AND d.canary_total_steps>0 AND d.canary_step<d.canary_total_steps
 AND COALESCE(NULLIF(d.rollout_state,''),'pending') IN ('pending','rolling_out')
 AND NOT EXISTS(SELECT 1 FROM profile_canary_checks j WHERE j.deployment_id=d.id AND j.canary_step=d.canary_step AND j.canary_step_started_at=d.canary_step_started_at AND j.policy_revision=p.revision)
ORDER BY d.canary_step_started_at,d.id LIMIT sqlc.arg(batch_limit)::int;

-- name: EnqueueProfileCanaryCheck :execrows
INSERT INTO profile_canary_checks(deployment_id,app_id,account_id,canary_step,canary_step_started_at,policy_revision,data,status,reason,next_attempt_at,created_at,completed_at)
SELECT sqlc.arg(deployment_id)::text::uuid,d.app_id,p.account_id,d.canary_step,d.canary_step_started_at,p.revision,sqlc.arg(data)::jsonb,
 sqlc.arg(status)::text,sqlc.arg(reason)::text,sqlc.narg(next_attempt_at)::timestamptz,sqlc.arg(created_at)::timestamptz,sqlc.narg(completed_at)::timestamptz
FROM deployments d JOIN profile_deployment_policies p ON p.app_id=d.app_id JOIN apps a ON a.id=d.app_id
WHERE d.id=sqlc.arg(deployment_id)::text::uuid AND d.app_id=sqlc.arg(app_id)::text::uuid AND p.account_id=sqlc.arg(account_id)::text::uuid
 AND d.canary_step=sqlc.arg(canary_step)::int AND d.canary_step_started_at=sqlc.arg(canary_step_started_at)::timestamptz AND p.revision=sqlc.arg(policy_revision)::bigint AND p.enabled
 AND a.account_id=p.account_id AND a.status<>'deleted' AND a.manifest #>> '{profiling,enabled}'='true'
 AND d.status='live' AND d.traffic_percent>0 AND d.canary_total_steps>0 AND d.canary_step<d.canary_total_steps AND COALESCE(NULLIF(d.rollout_state,''),'pending') IN ('pending','rolling_out')
ON CONFLICT(deployment_id,canary_step,canary_step_started_at,policy_revision) DO NOTHING;

-- name: CancelProfileCanaryChecks :exec
UPDATE profile_canary_checks SET status='cancelled',reason='Automatic profiling policy changed or was disabled.',completed_at=now(),next_attempt_at=NULL,lease_token=NULL,lease_until=NULL
WHERE app_id=sqlc.arg(app_id)::text::uuid AND status IN ('queued','running');

-- name: ClaimProfileCanaryCheck :one
WITH next_job AS (
 SELECT j.deployment_id,j.canary_step,j.canary_step_started_at,j.policy_revision FROM profile_canary_checks j
 JOIN profile_deployment_policies p ON p.app_id=j.app_id JOIN apps a ON a.id=j.app_id
 WHERE j.status IN ('queued','running') AND j.next_attempt_at<=sqlc.arg(observed_at)::timestamptz
  AND (j.lease_until IS NULL OR j.lease_until<=sqlc.arg(observed_at)::timestamptz) AND j.attempts<sqlc.arg(max_attempts)::int
  AND p.enabled AND p.revision=j.policy_revision AND a.status<>'deleted' AND a.account_id=j.account_id
 ORDER BY j.next_attempt_at,j.created_at,j.deployment_id,j.canary_step FOR UPDATE OF j SKIP LOCKED LIMIT 1
)
UPDATE profile_canary_checks j SET status='running',attempts=attempts+1,lease_token=sqlc.arg(token)::text::uuid,lease_until=sqlc.arg(lease_until)::timestamptz
FROM next_job n WHERE j.deployment_id=n.deployment_id AND j.canary_step=n.canary_step AND j.canary_step_started_at=n.canary_step_started_at AND j.policy_revision=n.policy_revision
RETURNING (j.data || jsonb_build_object('status',j.status,'reason',j.reason,'attempts',j.attempts,'next_attempt_at',j.next_attempt_at,'created_at',j.created_at,'completed_at',j.completed_at))::jsonb AS signal,j.app_id::text AS app_id,j.account_id::text AS account_id;

-- name: LockClaimedProfileCanaryCheck :one
SELECT (j.data || jsonb_build_object('status',j.status,'reason',j.reason,'attempts',j.attempts,'next_attempt_at',j.next_attempt_at,'created_at',j.created_at,'completed_at',j.completed_at))::jsonb AS signal
FROM profile_canary_checks j WHERE j.deployment_id=sqlc.arg(deployment_id)::text::uuid AND j.canary_step=sqlc.arg(canary_step)::int
 AND j.canary_step_started_at=sqlc.arg(canary_step_started_at)::timestamptz AND j.policy_revision=sqlc.arg(policy_revision)::bigint
 AND j.app_id=sqlc.arg(app_id)::text::uuid AND j.account_id=sqlc.arg(account_id)::text::uuid AND j.status='running'
 AND j.lease_token=sqlc.arg(token)::text::uuid AND j.lease_until>sqlc.arg(observed_at)::timestamptz
 AND (j.data->>'mode'<>'gate' OR EXISTS (SELECT 1 FROM deployments d JOIN profile_deployment_policies p ON p.app_id=d.app_id
 WHERE d.id=j.deployment_id AND d.canary_step=j.canary_step AND d.canary_step_started_at=j.canary_step_started_at
 AND d.status='live' AND d.traffic_percent>0 AND d.rollout_state IN ('pending','rolling_out') AND p.enabled AND p.revision=j.policy_revision)) FOR UPDATE;

-- name: FinishProfileCanaryCheck :exec
UPDATE profile_canary_checks SET data=sqlc.arg(data)::jsonb,status=sqlc.arg(status)::text,reason=sqlc.arg(reason)::text,attempts=sqlc.arg(attempts)::int,
 next_attempt_at=sqlc.narg(next_attempt_at)::timestamptz,completed_at=sqlc.narg(completed_at)::timestamptz,lease_token=NULL,lease_until=NULL
WHERE deployment_id=sqlc.arg(deployment_id)::text::uuid AND canary_step=sqlc.arg(canary_step)::int AND canary_step_started_at=sqlc.arg(canary_step_started_at)::timestamptz
 AND policy_revision=sqlc.arg(policy_revision)::bigint AND app_id=sqlc.arg(app_id)::text::uuid AND account_id=sqlc.arg(account_id)::text::uuid AND lease_token=sqlc.arg(token)::text::uuid;

-- name: ExpireProfileCanaryChecks :exec
UPDATE profile_canary_checks j SET status=CASE WHEN NOT p.enabled OR p.revision<>j.policy_revision OR a.status='deleted' THEN 'cancelled' ELSE 'inconclusive' END,
 reason=CASE WHEN NOT p.enabled OR p.revision<>j.policy_revision OR a.status='deleted' THEN 'Automatic profiling policy changed or was disabled.' ELSE 'The worker could not finish within its attempt limit.' END,
 completed_at=sqlc.arg(observed_at)::timestamptz,next_attempt_at=NULL,lease_token=NULL,lease_until=NULL
FROM profile_deployment_policies p,apps a WHERE p.app_id=j.app_id AND a.id=j.app_id AND j.status IN ('queued','running')
 AND (NOT p.enabled OR p.revision<>j.policy_revision OR a.status='deleted' OR (j.attempts>=sqlc.arg(max_attempts)::int AND (j.lease_until IS NULL OR j.lease_until<=sqlc.arg(observed_at)::timestamptz)));

-- name: PruneProfileCanaryChecks :exec
DELETE FROM profile_canary_checks WHERE (deployment_id,canary_step,canary_step_started_at,policy_revision) IN (
 SELECT deployment_id,canary_step,canary_step_started_at,policy_revision FROM profile_canary_checks WHERE completed_at<sqlc.arg(cutoff)::timestamptz ORDER BY completed_at,deployment_id,canary_step LIMIT sqlc.arg(batch_limit)::int);

-- name: ProfileRequestMix :many
WITH filtered AS (
 SELECT t.route, t.method, t.status, t.count::bigint AS weight
 FROM request_telemetry t
 JOIN apps a ON a.id=t.app_id AND a.account_id=t.account_id AND a.status<>'deleted'
 JOIN deployments d ON d.id=t.deployment_id AND d.app_id=t.app_id
 WHERE t.account_id=sqlc.arg(account_id)::text::uuid AND t.app_id=sqlc.arg(app_id)::text::uuid
 AND t.deployment_id=sqlc.arg(deployment_id)::text::uuid
 AND (sqlc.arg(route)::text='' OR t.route=sqlc.arg(route)::text)
 AND t.received_at>=sqlc.arg(start_at)::timestamptz AND t.received_at<sqlc.arg(end_at)::timestamptz
), grouped AS (
 SELECT 'route'::text AS dimension, route::text AS label, method::text AS method, SUM(weight)::bigint AS requests
 FROM filtered GROUP BY route, method
 UNION ALL
 SELECT 'status'::text, ((status / 100)::text || 'xx')::text, ''::text, SUM(weight)::bigint
 FROM filtered GROUP BY status / 100
), ranked AS (
 SELECT dimension,label,method,requests,
 SUM(requests) OVER (PARTITION BY dimension)::bigint AS total,
 ROW_NUMBER() OVER (PARTITION BY dimension ORDER BY requests DESC,label,method) AS position
 FROM grouped
)
SELECT dimension,label,method,requests,total FROM ranked
WHERE dimension='status' OR position<=sqlc.arg(max_routes)::int
ORDER BY dimension,requests DESC,label,method;
