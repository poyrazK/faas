-- ADR-20261018210500 Per-customer Operation concurrency.

-- name: GetOperationConcurrencyPolicyByKey :one
SELECT * FROM customer_operation_concurrency_policies
WHERE app_id = sqlc.arg(app_id) AND scope = sqlc.arg(scope) AND operation = sqlc.arg(operation)
  AND (CASE WHEN sqlc.arg(tenant_id)::text = '' THEN platform_tenant_id IS NULL ELSE platform_tenant_id::text = sqlc.arg(tenant_id)::text END)
FOR UPDATE;

-- name: InsertOperationConcurrencyPolicy :one
INSERT INTO customer_operation_concurrency_policies(id, account_id, app_id, scope, operation, platform_tenant_id, max_running, created_at, updated_at)
VALUES (sqlc.arg(id), sqlc.arg(account_id), sqlc.arg(app_id), sqlc.arg(scope), sqlc.arg(operation), sqlc.narg(platform_tenant_id),
    sqlc.arg(max_running), sqlc.arg(now), sqlc.arg(now))
RETURNING *;

-- name: UpdateOperationConcurrencyPolicy :one
UPDATE customer_operation_concurrency_policies SET max_running = sqlc.arg(max_running), updated_at = sqlc.arg(now)
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: CountOperationConcurrencyPolicies :one
SELECT count(*)::bigint FROM customer_operation_concurrency_policies WHERE app_id = sqlc.arg(app_id);

-- name: ListOperationConcurrencyPolicies :many
SELECT * FROM customer_operation_concurrency_policies
WHERE account_id = sqlc.arg(account_id) AND app_id = sqlc.arg(app_id)
ORDER BY scope, operation, platform_tenant_id NULLS FIRST, id;

-- name: DeleteOperationConcurrencyPolicy :execrows
DELETE FROM customer_operation_concurrency_policies
WHERE id = sqlc.arg(id) AND account_id = sqlc.arg(account_id) AND app_id = sqlc.arg(app_id);

-- The customer's override wins over the default.
-- name: EffectiveOperationConcurrencyLimit :one
SELECT max_running FROM customer_operation_concurrency_policies
WHERE app_id = sqlc.arg(app_id) AND scope = sqlc.arg(scope) AND operation = sqlc.arg(operation)
  AND (platform_tenant_id = sqlc.arg(platform_tenant_id) OR platform_tenant_id IS NULL)
ORDER BY platform_tenant_id NULLS LAST
LIMIT 1;

-- name: CountOperationsInFlight :one
SELECT count(*)::bigint FROM customer_operations o
JOIN customer_operation_definitions d ON d.id = o.definition_id
WHERE o.platform_tenant_id = sqlc.arg(platform_tenant_id) AND o.app_id = sqlc.arg(app_id)
  AND o.state IN ('accepted', 'running', 'requires_reconciliation')
  AND d.scope = sqlc.arg(scope) AND d.name = sqlc.arg(operation)
  AND NOT EXISTS (SELECT 1 FROM customer_operation_queue q WHERE q.operation_id = o.id);

-- name: CountQueuedOperations :one
SELECT count(*)::bigint FROM customer_operation_queue
WHERE app_id = sqlc.arg(app_id) AND scope = sqlc.arg(scope) AND operation = sqlc.arg(operation)
  AND platform_tenant_id = sqlc.arg(platform_tenant_id);

-- name: InsertQueuedOperation :exec
INSERT INTO customer_operation_queue(operation_id, account_id, app_id, scope, operation, platform_tenant_id, queued_at)
VALUES (sqlc.arg(operation_id), sqlc.arg(account_id), sqlc.arg(app_id), sqlc.arg(scope), sqlc.arg(operation), sqlc.arg(platform_tenant_id), sqlc.arg(queued_at));

-- name: ListOperationQueueGroups :many
SELECT DISTINCT account_id, app_id, scope, operation, platform_tenant_id FROM customer_operation_queue
ORDER BY account_id, app_id, scope, operation, platform_tenant_id
LIMIT sqlc.arg(row_limit);

-- name: ListQueuedOperationsForGroup :many
SELECT q.operation_id, o.state, o.execution_kind, o.current_invocation_id, o.workflow_run_id FROM customer_operation_queue q
JOIN customer_operations o ON o.id = q.operation_id
WHERE q.app_id = sqlc.arg(app_id) AND q.scope = sqlc.arg(scope) AND q.operation = sqlc.arg(operation)
  AND q.platform_tenant_id = sqlc.arg(platform_tenant_id)
ORDER BY q.queued_at, q.operation_id
LIMIT sqlc.arg(row_limit);

-- name: DeleteQueuedOperation :exec
DELETE FROM customer_operation_queue WHERE operation_id = sqlc.arg(operation_id);

-- name: ReleaseQueuedInvocation :exec
UPDATE invocations SET due_at = sqlc.arg(now) WHERE id = sqlc.arg(id) AND state = 'pending' AND due_at > sqlc.arg(now);

-- name: ReleaseQueuedWorkflowRun :exec
UPDATE workflow_runs SET scheduled_for = sqlc.arg(now), updated_at = sqlc.arg(now)
WHERE id = sqlc.arg(id) AND status = 'pending' AND scheduled_for > sqlc.arg(now);

-- name: ListCustomerQueuedOperations :many
SELECT operation_id, app_id, scope, operation, queued_at, position FROM (
    SELECT q.*, row_number() OVER (PARTITION BY q.app_id, q.scope, q.operation ORDER BY q.queued_at, q.operation_id)::bigint AS position
    FROM customer_operation_queue q
    WHERE q.account_id = sqlc.arg(account_id) AND q.platform_tenant_id = sqlc.arg(platform_tenant_id)
) ranked
ORDER BY queued_at, operation_id
LIMIT sqlc.arg(row_limit);

-- name: ListAppOperationQueueCounts :many
SELECT scope, operation, platform_tenant_id, count(*)::bigint AS queued FROM customer_operation_queue
WHERE account_id = sqlc.arg(account_id) AND app_id = sqlc.arg(app_id)
GROUP BY scope, operation, platform_tenant_id
ORDER BY scope, operation, platform_tenant_id
LIMIT sqlc.arg(row_limit);

-- name: OperationQueueStats :one
SELECT count(*)::bigint AS queued, min(queued_at)::timestamptz AS oldest FROM customer_operation_queue;
