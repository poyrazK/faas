-- name: LockScenarioActionApp :one
SELECT account_id=sqlc.arg(account_id)::text::uuid AND preview_of_slug IS NOT NULL
 AND preview_pr_number=0 AND status<>'deleted' AND preview_pr_state='open'
 AND preview_expires_at>clock_timestamp() AS eligible
FROM apps WHERE id=sqlc.arg(app_id)::text::uuid FOR UPDATE;

-- name: LockScenarioActionRun :exec
SELECT pg_advisory_xact_lock(hashtextextended('scenario-action-run:' || sqlc.arg(account_id)::text::uuid::text || ':' || sqlc.arg(run_id)::text,0));

-- name: ScenarioActionRunCanEnroll :one
SELECT NOT EXISTS(SELECT 1 FROM scenario_test_action_policies WHERE account_id=sqlc.arg(account_id)::text::uuid AND run_id=sqlc.arg(run_id)::text)
 AND (NOT sqlc.arg(capture)::boolean OR NOT EXISTS(SELECT 1 FROM scenario_test_members WHERE account_id=sqlc.arg(account_id)::text::uuid AND run_id=sqlc.arg(run_id)::text)) AS allowed;

-- name: ScenarioActionRunMembersMatch :one
SELECT NOT EXISTS (
 SELECT 1 FROM scenario_test_members m FULL JOIN scenario_test_action_policies p ON p.app_id=m.app_id
 WHERE (m.account_id=sqlc.arg(account_id)::text::uuid AND m.run_id=sqlc.arg(run_id)::text)
 OR (p.account_id=sqlc.arg(account_id)::text::uuid AND p.run_id=sqlc.arg(run_id)::text)
 GROUP BY m.app_id,p.app_id,m.account_id,p.account_id,m.run_id,p.run_id,m.workload_name,p.workload_name
 HAVING m.app_id IS NULL OR p.app_id IS NULL OR m.account_id<>p.account_id OR m.run_id<>p.run_id OR m.workload_name<>p.workload_name
) AS matches;

-- name: ScenarioActionAppHasManagedBindings :one
-- A contained member cannot reach real managed PostgreSQL or object storage;
-- refuse it at enrollment instead of failing later (ADR-20261019180000).
SELECT EXISTS(SELECT 1 FROM app_secrets WHERE app_id=sqlc.arg(app_id)::text::uuid
 AND (managed_postgres_binding_id IS NOT NULL OR managed_object_storage_credential_id IS NOT NULL)) AS bound;

-- name: ScenarioActionAppUnstarted :one
SELECT NOT EXISTS(SELECT 1 FROM deployments WHERE app_id=sqlc.arg(app_id)::text::uuid)
 AND NOT EXISTS(SELECT 1 FROM instances WHERE app_id=sqlc.arg(app_id)::text::uuid)
 AND NOT EXISTS(SELECT 1 FROM exclusive_work_operations WHERE app_id=sqlc.arg(app_id)::text::uuid)
 AND NOT EXISTS(SELECT 1 FROM workflow_runs WHERE app_id=sqlc.arg(app_id)::text::uuid)
 AND NOT EXISTS(SELECT 1 FROM app_webhook_deliveries d LEFT JOIN app_webhooks h ON h.id=d.webhook_id
  WHERE d.app_id=sqlc.arg(app_id)::text::uuid OR h.app_id=sqlc.arg(app_id)::text::uuid)
 AND NOT EXISTS(SELECT 1 FROM scenario_test_action_policies WHERE app_id=sqlc.arg(app_id)::text::uuid) AS unstarted;

-- name: InsertScenarioTestMember :exec
INSERT INTO scenario_test_members(account_id,run_id,workload_name,app_id)
VALUES(sqlc.arg(account_id)::text::uuid,sqlc.arg(run_id)::text,sqlc.arg(workload)::text,sqlc.arg(app_id)::text::uuid);

-- name: ScenarioTestMemberWithPolicy :one
SELECT m.account_id::text AS account_id,m.run_id,m.workload_name,m.app_id::text AS app_id,
 coalesce(p.webhook_mode,'')::text AS webhook_mode, coalesce(p.network_mode,'')::text AS network_mode, p.simulation_plan,p.managed_work_plan,p.cold_start_spec,p.suspend_resume_spec
FROM scenario_test_members m LEFT JOIN scenario_test_action_policies p ON p.app_id=m.app_id
WHERE m.app_id=sqlc.arg(app_id)::text::uuid;

-- name: InsertScenarioActionPolicy :exec
INSERT INTO scenario_test_action_policies(account_id,run_id,app_id,webhook_mode,workload_name,network_mode,simulation_plan,managed_work_plan,cold_start_spec,suspend_resume_spec)
VALUES(sqlc.arg(account_id)::text::uuid,sqlc.arg(run_id)::text,sqlc.arg(app_id)::text::uuid,'capture',sqlc.arg(workload)::text,sqlc.arg(network_mode)::text,sqlc.narg(simulation_plan)::jsonb,sqlc.narg(managed_work_plan)::jsonb,sqlc.narg(cold_start_spec)::jsonb,sqlc.narg(suspend_resume_spec)::jsonb);

-- name: ScenarioWebhookCapture :one
SELECT p.run_id,
 p.account_id=sqlc.arg(account_id)::text::uuid AND a.account_id=p.account_id
 AND a.status<>'deleted' AND a.preview_pr_state='open' AND a.preview_pr_number=0
 AND a.preview_of_slug IS NOT NULL AND a.preview_expires_at>clock_timestamp() AND p.sealed_at IS NULL
 AND EXISTS(SELECT 1 FROM scenario_test_members m WHERE m.app_id=p.app_id
  AND m.account_id=p.account_id AND m.run_id=p.run_id) AS active
FROM scenario_test_action_policies p JOIN apps a ON a.id=p.app_id
WHERE p.app_id=sqlc.arg(app_id)::text::uuid;

-- name: ScenarioWebhookDeliveryBlocked :one
SELECT EXISTS(SELECT 1 FROM scenario_test_action_policies p
 WHERE p.app_id=nullif(sqlc.arg(app_id)::text,'')::uuid
 OR p.app_id=(SELECT app_id FROM app_webhooks WHERE id=sqlc.arg(webhook_id)::text::uuid)) AS blocked;

-- name: ScenarioCaptureReceiver :one
SELECT h.scope,h.target_url,h.delivery_format,h.retry_policy,
 coalesce(t.external_ref,'')::text AS tenant_reference
FROM app_webhooks h LEFT JOIN platform_tenants t ON t.id=h.platform_tenant_id AND t.account_id=h.account_id
WHERE h.id=sqlc.arg(webhook_id)::text::uuid;

-- name: ScenarioActionPoliciesForRun :many
SELECT app_id::text AS app_id,workload_name,sealed_at,sealed_snapshot,simulation_plan,simulation_failed,managed_work_plan,managed_work_failed,cold_start_spec,suspend_resume_spec
FROM scenario_test_action_policies
WHERE account_id=sqlc.arg(account_id)::text::uuid AND run_id=sqlc.arg(run_id)::text
ORDER BY app_id;

-- name: SealScenarioActionPolicies :execrows
UPDATE scenario_test_action_policies SET sealed_at=sqlc.arg(sealed_at)::timestamptz,
 sealed_snapshot=CASE WHEN app_id=sqlc.arg(anchor_app_id)::text::uuid THEN sqlc.arg(snapshot)::jsonb ELSE NULL END
WHERE account_id=sqlc.arg(account_id)::text::uuid AND run_id=sqlc.arg(run_id)::text AND sealed_at IS NULL;

-- name: ScenarioActionSnapshotSummary :one
WITH members AS (
 SELECT app_id FROM scenario_test_action_policies
 WHERE account_id=sqlc.arg(account_id)::text::uuid AND run_id=sqlc.arg(run_id)::text
), effects AS (
 SELECT e.id,e.payload,e.rehearsal_run_id,e.receiver_sha256 FROM exclusive_work_effects e
 JOIN exclusive_work_operations o ON o.id=e.operation_id JOIN members m ON m.app_id=o.app_id
 WHERE o.account_id=sqlc.arg(account_id)::text::uuid AND e.webhook_id IS NOT NULL
 UNION ALL
 SELECT e.id,e.payload,e.rehearsal_run_id,e.receiver_sha256 FROM workflow_operation_effects e
 JOIN members m ON m.app_id=e.app_id WHERE e.account_id=sqlc.arg(account_id)::text::uuid
), deliveries AS (
 SELECT d.id,d.event,d.last_error FROM app_webhook_deliveries d
 LEFT JOIN app_webhooks h ON h.id=d.webhook_id
 WHERE d.account_id=sqlc.arg(account_id)::text::uuid
 AND EXISTS(SELECT 1 FROM members m WHERE m.app_id=d.app_id OR m.app_id=h.app_id)
)
SELECT (SELECT count(*) FROM effects WHERE rehearsal_run_id=sqlc.arg(run_id)::text)::bigint AS captured_count,
 (SELECT coalesce(sum(octet_length(payload::text)),0) FROM effects WHERE rehearsal_run_id=sqlc.arg(run_id)::text)::bigint AS payload_bytes,
 ((SELECT count(*) FROM exclusive_work_operations o JOIN members m ON m.app_id=o.app_id WHERE o.state IN ('pending','running'))+
  (SELECT count(*) FROM workflow_runs w JOIN members m ON m.app_id=w.app_id WHERE w.status IN ('pending','running','awaiting_event'))+
  (SELECT count(*) FROM invocations i JOIN members m ON m.app_id=i.app_id WHERE i.state IN ('pending','dispatching'))+
 (SELECT count(*) FROM app_tasks t JOIN members m ON m.app_id=t.app_id WHERE t.command[1]='__gregale_rehearsal_command_v1__' AND t.status IN ('queued','restoring','running')))::bigint AS pending_managed_work,
 ((SELECT count(*) FROM effects WHERE rehearsal_run_id<>sqlc.arg(run_id)::text OR receiver_sha256='')+
  (SELECT count(*) FROM deliveries d WHERE d.event='operation.effect' AND NOT EXISTS(SELECT 1 FROM effects e WHERE e.id=d.id)))::bigint AS incomplete_managed_effects,
 (SELECT count(*) FROM deliveries WHERE last_error='rehearsal: unsupported webhook delivery suppressed')::bigint AS suppressed_deliveries;

-- name: ScenarioCapturedActions :many
SELECT e.id::text AS id,p.workload_name AS workload,'operation'::text AS source,
 o.id::text AS operation_id,''::text AS workflow,''::text AS step,e.name,
 e.webhook_id::text AS webhook_id,e.receiver_sha256,e.event_type::text AS type,e.payload
FROM exclusive_work_effects e JOIN exclusive_work_operations o ON o.id=e.operation_id
JOIN scenario_test_action_policies p ON p.app_id=o.app_id AND p.account_id=o.account_id
WHERE p.account_id=sqlc.arg(account_id)::text::uuid AND p.run_id=sqlc.arg(run_id)::text AND e.rehearsal_run_id=p.run_id
UNION ALL
SELECT e.id::text,p.workload_name,'workflow'::text,e.operation_id::text,w.workflow_name,e.step_name,e.name,
 e.webhook_id::text,e.receiver_sha256,e.event_type,e.payload
FROM workflow_operation_effects e JOIN workflow_runs w ON w.id=e.run_id
JOIN scenario_test_action_policies p ON p.app_id=e.app_id AND p.account_id=e.account_id
WHERE p.account_id=sqlc.arg(account_id)::text::uuid AND p.run_id=sqlc.arg(run_id)::text AND e.rehearsal_run_id=p.run_id
ORDER BY workload,source,id LIMIT 257;

-- name: ScenarioEventCapturePolicy :one
-- Any enrolled app, contained or capture-only, publishes into its run's
-- evidence instead of the account's event bus (ADR-20261020090000).
SELECT account_id::text AS account_id,run_id,workload_name FROM scenario_test_action_policies
WHERE app_id=sqlc.arg(app_id)::text::uuid;

-- name: ScenarioEventCaptureState :one
SELECT EXISTS(SELECT 1 FROM scenario_test_action_policies WHERE account_id=sqlc.arg(account_id)::text::uuid
  AND run_id=sqlc.arg(run_id)::text AND sealed_at IS NOT NULL)::boolean AS sealed,
 (SELECT count(*) FROM scenario_captured_events WHERE account_id=sqlc.arg(account_id)::text::uuid AND run_id=sqlc.arg(run_id)::text)::bigint AS captured;

-- name: InsertScenarioCapturedEvent :exec
INSERT INTO scenario_captured_events(account_id,run_id,app_id,source,type,schema_version,data,oversized,event_id)
VALUES(sqlc.arg(account_id)::text::uuid,sqlc.arg(run_id)::text,sqlc.arg(app_id)::text::uuid,sqlc.arg(source)::text,
 sqlc.arg(type)::text,sqlc.arg(schema_version)::text,sqlc.narg(data)::jsonb,sqlc.arg(oversized)::boolean,nullif(sqlc.arg(event_id)::text,''));

-- name: ScenarioCapturedEventIdentity :one
-- Whether a captured event with this source and ID carries the same type,
-- schema version and data. A run's events are few and read under the run
-- lock, so the run index bounds this lookup (ADR-20261021120000).
SELECT (type=sqlc.arg(type)::text AND schema_version=sqlc.arg(schema_version)::text
  AND data IS NOT DISTINCT FROM sqlc.narg(data)::jsonb AND oversized=sqlc.arg(oversized)::boolean)::boolean AS same
FROM scenario_captured_events
WHERE account_id=sqlc.arg(account_id)::text::uuid AND run_id=sqlc.arg(run_id)::text
 AND source=sqlc.arg(source)::text AND event_id=sqlc.arg(event_id)::text
ORDER BY created_at,id LIMIT 1;

-- name: ScenarioRehearsalNetworkPolicy :one
SELECT run_id,account_id::text AS account_id FROM scenario_test_action_policies
WHERE app_id=sqlc.arg(app_id)::text::uuid AND network_mode='contained';

-- name: ScenarioCapturedEvents :many
SELECT e.id::text AS id,p.workload_name AS workload,e.source,e.type,e.schema_version,e.data,e.oversized
FROM scenario_captured_events e JOIN scenario_test_action_policies p ON p.app_id=e.app_id AND p.account_id=e.account_id AND p.run_id=e.run_id
WHERE e.account_id=sqlc.arg(account_id)::text::uuid AND e.run_id=sqlc.arg(run_id)::text
ORDER BY e.created_at,e.id LIMIT 257;

-- name: ScenarioQueueCaptureState :one
SELECT EXISTS(SELECT 1 FROM scenario_test_action_policies WHERE account_id=sqlc.arg(account_id)::text::uuid
  AND run_id=sqlc.arg(run_id)::text AND sealed_at IS NOT NULL)::boolean AS sealed,
 (SELECT count(*) FROM scenario_captured_queue_messages WHERE account_id=sqlc.arg(account_id)::text::uuid AND run_id=sqlc.arg(run_id)::text)::bigint AS captured;

-- name: ScenarioQueueCaptureTarget :one
-- A member of the same run is named by its workload, so sibling runs compare;
-- any other app must exist in the caller's account, as for a real send.
SELECT coalesce((SELECT 'workload:' || p.workload_name FROM scenario_test_action_policies p JOIN apps a ON a.id=p.app_id
  WHERE p.account_id=sqlc.arg(account_id)::text::uuid AND p.run_id=sqlc.arg(run_id)::text AND p.workload_name<>''
  AND (p.workload_name=sqlc.arg(slug)::text OR a.slug=sqlc.arg(slug)::text) ORDER BY p.workload_name LIMIT 1),
 (SELECT 'app:' || a.slug FROM apps a WHERE a.slug=sqlc.arg(slug)::text AND a.account_id=sqlc.arg(account_id)::text::uuid),
 '')::text AS target;

-- name: InsertScenarioCapturedQueueMessage :one
INSERT INTO scenario_captured_queue_messages(account_id,run_id,app_id,kind,target,queue_name,schedule,message,oversized)
VALUES(sqlc.arg(account_id)::text::uuid,sqlc.arg(run_id)::text,sqlc.arg(app_id)::text::uuid,sqlc.arg(kind)::text,
 sqlc.arg(target)::text,sqlc.arg(queue_name)::text,sqlc.arg(schedule)::text,sqlc.narg(message)::jsonb,sqlc.arg(oversized)::boolean)
RETURNING id::text AS id;

-- name: ScenarioCapturedQueueMessages :many
SELECT m.id::text AS id,p.workload_name AS workload,m.kind,m.target,m.queue_name,m.schedule,m.message,m.oversized
FROM scenario_captured_queue_messages m JOIN scenario_test_action_policies p ON p.app_id=m.app_id AND p.account_id=m.account_id AND p.run_id=m.run_id
WHERE m.account_id=sqlc.arg(account_id)::text::uuid AND m.run_id=sqlc.arg(run_id)::text
ORDER BY m.created_at,m.id LIMIT 257;

-- name: ScenarioPlatformCaptureState :one
SELECT EXISTS(SELECT 1 FROM scenario_test_action_policies WHERE account_id=sqlc.arg(account_id)::text::uuid
  AND run_id=sqlc.arg(run_id)::text AND sealed_at IS NOT NULL)::boolean AS sealed,
 (SELECT count(*) FROM scenario_captured_platform_calls WHERE account_id=sqlc.arg(account_id)::text::uuid AND run_id=sqlc.arg(run_id)::text)::bigint AS captured;

-- name: ScenarioPlatformCallsByIdempotencyKey :many
-- Earlier captures under the same key; each kind applies the production
-- replay rule to them (ADR-20261021150000).
SELECT id::text AS id,name,(message IS NOT DISTINCT FROM sqlc.narg(message)::jsonb AND oversized=sqlc.arg(oversized)::boolean)::boolean AS same
FROM scenario_captured_platform_calls
WHERE account_id=sqlc.arg(account_id)::text::uuid AND run_id=sqlc.arg(run_id)::text AND app_id=sqlc.arg(app_id)::text::uuid
 AND kind=sqlc.arg(kind)::text AND target=sqlc.arg(target)::text AND idempotency_key=sqlc.arg(idempotency_key)::text
ORDER BY created_at,id LIMIT 257;

-- name: ScenarioCapturedWorkflowRunReference :one
-- A captured workflow run is named by its starter, target, workflow and
-- start ordinal, so a later event to it compares across sibling runs.
SELECT ('run:' || p.workload_name || '/' || c.target || '/' || c.name || '#' ||
 (SELECT count(*) FROM scenario_captured_platform_calls x WHERE x.account_id=c.account_id AND x.run_id=c.run_id AND x.app_id=c.app_id
   AND x.kind='workflow_run' AND x.target=c.target AND x.name=c.name AND (x.created_at,x.id)<=(c.created_at,c.id))::text)::text AS reference
FROM scenario_captured_platform_calls c JOIN scenario_test_action_policies p ON p.app_id=c.app_id
WHERE c.id=sqlc.arg(id)::text::uuid AND c.account_id=sqlc.arg(account_id)::text::uuid AND c.run_id=sqlc.arg(run_id)::text AND c.kind='workflow_run';

-- name: ScenarioExternalWorkflowRunInAccount :one
SELECT EXISTS(SELECT 1 FROM workflow_runs wr JOIN apps a ON a.id=wr.app_id
 WHERE wr.id=sqlc.arg(id)::text::uuid AND a.account_id=sqlc.arg(account_id)::text::uuid)::boolean AS present;

-- name: InsertScenarioCapturedPlatformCall :one
INSERT INTO scenario_captured_platform_calls(account_id,run_id,app_id,kind,target,name,idempotency_key,message,oversized)
VALUES(sqlc.arg(account_id)::text::uuid,sqlc.arg(run_id)::text,sqlc.arg(app_id)::text::uuid,sqlc.arg(kind)::text,sqlc.arg(target)::text,
 sqlc.arg(name)::text,nullif(sqlc.arg(idempotency_key)::text,''),sqlc.narg(message)::jsonb,sqlc.arg(oversized)::boolean)
RETURNING id::text AS id;

-- name: ScenarioCapturedPlatformCalls :many
SELECT c.id::text AS id,p.workload_name AS workload,c.kind,c.target,c.name,c.message,c.oversized
FROM scenario_captured_platform_calls c JOIN scenario_test_action_policies p ON p.app_id=c.app_id AND p.account_id=c.account_id AND p.run_id=c.run_id
WHERE c.account_id=sqlc.arg(account_id)::text::uuid AND c.run_id=sqlc.arg(run_id)::text
ORDER BY c.created_at,c.id LIMIT 257;

-- name: ScenarioSuppressedWebhookEvents :many
-- Ordinary webhook deliveries the capture fence suppressed, attributed like
-- the snapshot summary: the delivery's app first, then the receiver's app.
-- Managed operation effects are accounted separately as captured intent.
SELECT p.workload_name AS workload,d.event,count(*)::bigint AS count
FROM app_webhook_deliveries d LEFT JOIN app_webhooks h ON h.id=d.webhook_id
JOIN scenario_test_action_policies p ON p.account_id=d.account_id AND p.run_id=sqlc.arg(run_id)::text
 AND p.app_id=CASE WHEN EXISTS(SELECT 1 FROM scenario_test_action_policies x WHERE x.app_id=d.app_id
  AND x.account_id=d.account_id AND x.run_id=sqlc.arg(run_id)::text) THEN d.app_id ELSE h.app_id END
WHERE d.account_id=sqlc.arg(account_id)::text::uuid
 AND d.last_error='rehearsal: unsupported webhook delivery suppressed' AND d.event<>'operation.effect'
GROUP BY p.workload_name,d.event ORDER BY p.workload_name,d.event LIMIT 257;

-- name: ScenarioRehearsalNetworkRun :one
SELECT run_id FROM scenario_test_action_policies
WHERE app_id=sqlc.arg(app_id)::text::uuid AND network_mode='contained';
