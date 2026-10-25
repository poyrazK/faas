-- name: QueueCanarySnapshotView :one
-- ADR-570: the same read without row locks, for read-only snapshot views.
SELECT jsonb_build_object(
 'BindingID',b.id,'Name',b.name,'QueueName',b.queue_name,'Scope',d.scope,
 'EnvironmentID',b.environment_id,'BindingScope',b.deployment_scope,'TriggerID',t.id,
 'RevisionTTLSeconds',coalesce((a.manifest->>'revision_pin_ttl_seconds')::int,0),'MaxAttempts',t.max_attempts,'PayloadMaxBytes',t.payload_max_bytes,'FilterCriteria',t.filter_criteria,'PoisonStrategy',t.broker_poison_strategy,'Config',t.config,'BatchSize',t.batch_size_max,'BatchWindow',t.batch_window_ms,
 'Retry',b.retry_policy,'Concurrency',b.max_concurrency,
 'DeploymentID',d.id,'OverrideEnv',d.override_env,'RootfsKey',d.rootfs_key,'ImageDigest',d.image_digest,
 'ConsumerClass',b.workload_class,'ManagedRuntime',d.environment_workload_runtime
)::jsonb AS snapshot, to_jsonb(d)::jsonb AS deployment
FROM apps a JOIN queue_bindings b ON b.app_id=a.id AND b.account_id=a.account_id
JOIN deployments d ON d.app_id=a.id
JOIN triggers t ON t.queue_binding_id=b.id AND t.app_id=a.id AND t.account_id=a.account_id
WHERE a.id=sqlc.arg(app_id)::uuid AND a.account_id=sqlc.arg(account_id)::uuid AND a.status<>'deleted'
AND d.id=sqlc.arg(deployment_id)::uuid AND d.status='live' AND d.deleted_at IS NULL
AND (d.environment_workload_runtime IS NOT NULL OR (coalesce((a.manifest->>'revision_pin_ttl_seconds')::int,0)>0 AND (d.traffic_percent>0 OR EXISTS(SELECT 1 FROM deployment_revision_pins pins WHERE pins.deployment_id=d.id AND pins.expires_at>clock_timestamp()))))
AND b.name=sqlc.arg(binding)::text AND b.enabled AND b.retired_at IS NULL AND b.mode='push'
AND ((b.deployment_scope='' AND d.scope IN ('production','default')) OR (b.deployment_scope=d.scope AND EXISTS(SELECT 1 FROM project_environments e WHERE e.id=b.environment_id AND e.project_id=a.project_id AND e.account_id=a.account_id AND e.slug=d.scope)))
AND t.enabled AND t.kind='queue' AND t.source='queue'
AND t.queue_binding_scope=b.deployment_scope AND t.queue_binding_environment_id IS NOT DISTINCT FROM b.environment_id
AND NOT EXISTS(SELECT 1 FROM queue_bindings other_binding WHERE other_binding.app_id=b.app_id AND other_binding.name=b.name AND other_binding.id<>b.id AND other_binding.retired_at IS NULL AND (other_binding.deployment_scope='' OR other_binding.deployment_scope=d.scope))
AND NOT EXISTS(SELECT 1 FROM triggers other WHERE other.queue_binding_id=b.id AND other.id<>t.id);

-- name: QueueCanarySnapshot :one
SELECT jsonb_build_object(
 'BindingID',b.id,'Name',b.name,'QueueName',b.queue_name,'Scope',d.scope,
 'EnvironmentID',b.environment_id,'BindingScope',b.deployment_scope,'TriggerID',t.id,
 'RevisionTTLSeconds',coalesce((a.manifest->>'revision_pin_ttl_seconds')::int,0),'MaxAttempts',t.max_attempts,'PayloadMaxBytes',t.payload_max_bytes,'FilterCriteria',t.filter_criteria,'PoisonStrategy',t.broker_poison_strategy,'Config',t.config,'BatchSize',t.batch_size_max,'BatchWindow',t.batch_window_ms,
 'Retry',b.retry_policy,'Concurrency',b.max_concurrency,
 'DeploymentID',d.id,'OverrideEnv',d.override_env,'RootfsKey',d.rootfs_key,'ImageDigest',d.image_digest,
 'ConsumerClass',b.workload_class,'ManagedRuntime',d.environment_workload_runtime
)::jsonb AS snapshot, to_jsonb(d)::jsonb AS deployment
FROM apps a JOIN queue_bindings b ON b.app_id=a.id AND b.account_id=a.account_id
JOIN deployments d ON d.app_id=a.id
JOIN triggers t ON t.queue_binding_id=b.id AND t.app_id=a.id AND t.account_id=a.account_id
WHERE a.id=sqlc.arg(app_id)::uuid AND a.account_id=sqlc.arg(account_id)::uuid AND a.status<>'deleted'
AND d.id=sqlc.arg(deployment_id)::uuid AND d.status='live' AND d.deleted_at IS NULL
AND (d.environment_workload_runtime IS NOT NULL OR (coalesce((a.manifest->>'revision_pin_ttl_seconds')::int,0)>0 AND (d.traffic_percent>0 OR EXISTS(SELECT 1 FROM deployment_revision_pins pins WHERE pins.deployment_id=d.id AND pins.expires_at>clock_timestamp()))))
AND b.name=sqlc.arg(binding)::text AND b.enabled AND b.retired_at IS NULL AND b.mode='push'
AND ((b.deployment_scope='' AND d.scope IN ('production','default')) OR (b.deployment_scope=d.scope AND EXISTS(SELECT 1 FROM project_environments e WHERE e.id=b.environment_id AND e.project_id=a.project_id AND e.account_id=a.account_id AND e.slug=d.scope)))
AND t.enabled AND t.kind='queue' AND t.source='queue'
AND t.queue_binding_scope=b.deployment_scope AND t.queue_binding_environment_id IS NOT DISTINCT FROM b.environment_id
AND NOT EXISTS(SELECT 1 FROM queue_bindings other_binding WHERE other_binding.app_id=b.app_id AND other_binding.name=b.name AND other_binding.id<>b.id AND other_binding.retired_at IS NULL AND (other_binding.deployment_scope='' OR other_binding.deployment_scope=d.scope))
AND NOT EXISTS(SELECT 1 FROM triggers other WHERE other.queue_binding_id=b.id AND other.id<>t.id)
FOR SHARE OF a,b,d,t;

-- name: InsertQueueCanary :exec
INSERT INTO queue_binding_canaries(id,account_id,app_id,binding_id,trigger_id,deployment_id,snapshot,published_at,expires_at)
VALUES(sqlc.arg(id),sqlc.arg(account_id),sqlc.arg(app_id),sqlc.arg(binding_id),sqlc.arg(trigger_id),sqlc.arg(deployment_id),sqlc.arg(snapshot),sqlc.arg(published_at),sqlc.arg(expires_at));

-- name: ReadQueueCanary :one
SELECT c.*, i.state AS invocation_state,i.attempts,i.replay_generation,i.received_at,i.completed_at
FROM queue_binding_canaries c JOIN invocations i ON i.id=c.id
WHERE c.account_id=sqlc.arg(account_id) AND c.app_id=sqlc.arg(app_id) AND c.id=sqlc.arg(id);

-- name: LatestQueueCanaryID :one
SELECT id::text FROM queue_binding_canaries WHERE account_id=sqlc.arg(account_id) AND app_id=sqlc.arg(app_id)
AND deployment_id=sqlc.arg(deployment_id) AND snapshot->>'Name'=sqlc.arg(binding)::text ORDER BY published_at DESC LIMIT 1;

-- name: CancelQueueCanary :exec
UPDATE queue_binding_canaries SET cancelled=true WHERE account_id=sqlc.arg(account_id) AND app_id=sqlc.arg(app_id) AND id=sqlc.arg(id);

-- name: FinishQueueCanary :exec
UPDATE queue_binding_canaries c SET completed_deployment_id=sqlc.arg(deployment_id)::uuid,completed_attempt=i.attempts,completed_generation=i.replay_generation
FROM invocations i WHERE c.id=i.id AND c.id=sqlc.arg(id)::uuid AND c.app_id=sqlc.arg(app_id)::uuid
AND c.binding_id=sqlc.arg(binding_id)::uuid AND c.trigger_id=sqlc.arg(trigger_id)::uuid AND i.state='completed' AND i.outcome='success'
AND i.attempts=1 AND i.replay_generation=0 AND NOT c.cancelled AND c.expires_at>clock_timestamp();

-- name: LockQueueCanaryApp :one
SELECT id FROM apps WHERE id=sqlc.arg(app_id) AND account_id=sqlc.arg(account_id) AND status<>'deleted' FOR UPDATE;

-- name: ActiveQueueCanaryExists :one
SELECT EXISTS(SELECT 1 FROM queue_binding_canaries c JOIN invocations i ON i.id=c.id WHERE c.app_id=sqlc.arg(app_id) AND c.binding_id=sqlc.arg(binding_id) AND NOT c.cancelled AND c.expires_at>clock_timestamp() AND i.state IN ('pending','dispatching'))::boolean;

-- name: ReadInvocationDispatchAuthority :one
SELECT coalesce(i.environment_id::text,'')::text AS environment_id, i.app_id, i.account_id,
 coalesce(i.queue_binding_id::text,'')::text AS queue_binding_id,i.queue_name,i.deployment_scope,
 EXISTS(SELECT 1 FROM queue_binding_canaries c WHERE c.id=i.id)::boolean AS queue_canary
FROM invocations i WHERE i.id=sqlc.arg(invocation_id)::uuid;

-- name: QueueCanaryManagedAuthority :one
SELECT release.id::text AS release_id, source.generation
FROM deployments d JOIN apps a ON a.id=d.app_id
JOIN project_release_members member ON member.app_id=a.id AND member.deployment_id=d.id
JOIN project_release_sets release ON release.id=member.release_id
JOIN active_environment_git_sources source ON source.id::text=d.environment_workload_runtime->>'source_id'
JOIN project_environments environment ON environment.id=source.environment_id
WHERE d.id=sqlc.arg(deployment_id)::uuid AND a.id=sqlc.arg(app_id)::uuid AND a.account_id=sqlc.arg(account_id)::uuid
 AND coalesce(a.preview_of_slug,'')='' AND NOT d.environment_workload_held
 AND release.active AND release.account_id=a.account_id AND release.project_id=a.project_id AND release.environment_slug=d.scope
 AND source.account_id=a.account_id AND source.project_id=a.project_id AND source.mode='enforce' AND NOT source.suspended
 AND source.environment_id::text=d.environment_workload_runtime->>'environment_id'
 AND environment.account_id=a.account_id AND environment.project_id=a.project_id AND environment.slug=d.scope
-- Admission holds the parent app lock, also required by release publication.
-- Read committed authority without reversing the publisher's release/app lock order.
;
