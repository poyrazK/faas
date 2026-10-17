-- name: LockManagedPostgresDrainPlanReservation :one
SELECT a.id::text AS app_id,a.node_id::text AS owner_node_id,a.scaling_policy_revision AS owner_revision,
 a.managed_postgres_admission_fenced_at AS fenced_at
FROM apps a JOIN managed_postgres_cutovers c ON c.app_id=a.id
WHERE c.id=sqlc.arg(cutover_id)::text::uuid AND a.node_id=sqlc.arg(owner_node_id)::text::uuid
 AND a.managed_postgres_admission_cutover_id=c.id AND a.status IN ('active','evicted_cold')
 AND c.state IN ('verified','verifying') FOR UPDATE OF a,c;

-- name: GetManagedPostgresDrainPlanForFence :one
SELECT * FROM managed_postgres_cutover_drain_plans
WHERE cutover_id=sqlc.arg(cutover_id)::text::uuid AND owner_node_id=sqlc.arg(owner_node_id)::text::uuid
 AND owner_revision=sqlc.arg(owner_revision) AND fenced_at=sqlc.arg(fenced_at);

-- name: ListManagedPostgresDrainPlanInventory :many
SELECT i.id,i.deployment_id,i.node_id,i.wake_id,i.kind,i.mode,i.state,i.migrated_from_node_id,
 coalesce(d.scope,'')::text AS deployment_scope,
 EXISTS (SELECT 1 FROM environment_qualification_executions e WHERE e.instance_id=i.id AND e.retired_at IS NULL) AS qualification_live
FROM instances i LEFT JOIN deployments d ON d.id=i.deployment_id
WHERE i.app_id=sqlc.arg(app_id)::text::uuid ORDER BY i.id LIMIT sqlc.arg(page_limit) FOR UPDATE OF i;

-- name: InsertManagedPostgresDrainPlan :one
INSERT INTO managed_postgres_cutover_drain_plans (id,cutover_id,app_id,owner_node_id,owner_revision,fenced_at,inventory_count,blocker_code)
VALUES (sqlc.arg(id)::text::uuid,sqlc.arg(cutover_id)::text::uuid,sqlc.arg(app_id)::text::uuid,
 sqlc.arg(owner_node_id)::text::uuid,sqlc.arg(owner_revision),sqlc.arg(fenced_at),sqlc.arg(inventory_count),sqlc.arg(blocker_code)) RETURNING *;

-- name: InsertManagedPostgresDrainPlanMember :exec
INSERT INTO managed_postgres_cutover_drain_plan_members (plan_id,instance_id,deployment_id,node_id,wake_id,
 instance_kind,instance_mode,observed_state,deployment_scope,migrated_from_node_id,qualification_live,drain_id,blocker_code)
VALUES (sqlc.arg(plan_id)::text::uuid,sqlc.arg(instance_id)::text::uuid,sqlc.narg(deployment_id)::uuid,
 sqlc.arg(node_id)::text::uuid,sqlc.arg(wake_id)::text::uuid,sqlc.arg(instance_kind),sqlc.arg(instance_mode),sqlc.arg(observed_state),
 sqlc.arg(deployment_scope),sqlc.narg(migrated_from_node_id)::uuid,sqlc.arg(qualification_live),sqlc.narg(drain_id)::uuid,sqlc.arg(blocker_code));

-- name: GetManagedPostgresDrainPlan :one
SELECT * FROM managed_postgres_cutover_drain_plans WHERE id=sqlc.arg(id)::text::uuid;

-- name: LockManagedPostgresDrainPlanOwner :one
SELECT a.node_id,a.scaling_policy_revision,a.status,a.managed_postgres_admission_cutover_id,a.managed_postgres_admission_fenced_at,
 coalesce(c.state,'')::text AS cutover_state
FROM apps a LEFT JOIN managed_postgres_cutovers c ON c.id=a.managed_postgres_admission_cutover_id
WHERE a.id=sqlc.arg(app_id)::text::uuid FOR SHARE OF a;

-- name: ReadManagedPostgresDrainPlanOwner :one
SELECT a.node_id,a.scaling_policy_revision,a.status,a.managed_postgres_admission_cutover_id,a.managed_postgres_admission_fenced_at,
 coalesce(c.state,'')::text AS cutover_state
FROM apps a LEFT JOIN managed_postgres_cutovers c ON c.id=a.managed_postgres_admission_cutover_id
WHERE a.id=sqlc.arg(app_id)::text::uuid;

-- name: ListManagedPostgresDrainPlanMembers :many
SELECT m.*, coalesce(i.id::text,'')::text AS current_instance_id,coalesce(i.app_id::text,'')::text AS current_app_id,coalesce(i.deployment_id::text,'')::text AS current_deployment_id,
 coalesce(i.node_id::text,'')::text AS current_node_id,coalesce(i.wake_id::text,'')::text AS current_wake_id,
 coalesce(i.kind,'')::text AS current_kind,coalesce(i.mode,'')::text AS current_mode,coalesce(i.state,'')::text AS current_state,
 coalesce(i.migrated_from_node_id::text,'')::text AS current_migrated_from_node_id,
 EXISTS (SELECT 1 FROM environment_qualification_executions e WHERE e.instance_id=m.instance_id AND e.retired_at IS NULL) AS current_qualification_live
FROM managed_postgres_cutover_drain_plan_members m LEFT JOIN instances i ON i.id=m.instance_id
WHERE m.plan_id=sqlc.arg(plan_id)::text::uuid ORDER BY m.instance_id LIMIT sqlc.arg(page_limit);

-- name: ManagedPostgresDrainPlanHasUnplannedInstances :one
SELECT EXISTS (SELECT 1 FROM instances i WHERE i.app_id=sqlc.arg(app_id)::text::uuid
 AND NOT EXISTS (SELECT 1 FROM managed_postgres_cutover_drain_plan_members m WHERE m.plan_id=sqlc.arg(plan_id)::text::uuid AND m.instance_id=i.id)) AS unplanned;
