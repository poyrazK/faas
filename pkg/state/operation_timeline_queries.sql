-- ADR-1195: each timeline source is read after its own durable position.

-- name: OperationTimelineAttempts :many
-- Operation invocations never replay, so replay generation zero is the
-- complete attempt history of an HTTP execution.
SELECT e.generation, e.invocation_id::text AS execution_id, h.attempt, h.started_at, h.finished_at,
  h.outcome, h.error_detail, h.next_attempt_at, coalesce(p.instance_id::text,'')::text AS instance_id
FROM customer_operation_executions e
JOIN invocation_attempt_history h ON h.invocation_id=e.invocation_id AND h.replay_generation=0
LEFT JOIN customer_operation_attempt_instances p
  ON p.operation_id=e.operation_id AND p.generation=e.generation AND p.attempt=h.attempt
WHERE e.operation_id=sqlc.arg(operation_id)::uuid AND e.invocation_id IS NOT NULL
  AND (e.generation,h.attempt)>(sqlc.arg(after_generation)::integer,sqlc.arg(after_attempt)::integer)
ORDER BY e.generation,h.attempt LIMIT sqlc.arg(page_limit)::integer;

-- name: OperationTimelineStepAttempts :many
-- Recovery resumes the same run and moves its one execution row to the new
-- generation, so step attempts carry no generation. Operation workflows are
-- one linear chain: a run starts one step attempt at a time.
SELECT e.workflow_run_id::text AS execution_id, a.step_name, a.attempt, a.status,
  a.http_status, a.started_at, a.finished_at, a.next_attempt_at, coalesce(a.error,'')::text AS error
FROM customer_operation_executions e
JOIN workflow_step_attempts a ON a.run_id=e.workflow_run_id
WHERE e.operation_id=sqlc.arg(operation_id)::uuid AND e.workflow_run_id IS NOT NULL
  AND (a.started_at,a.step_name,a.attempt)>(sqlc.arg(after_started_at)::timestamptz,
    sqlc.arg(after_step)::text,sqlc.arg(after_attempt)::integer)
ORDER BY a.started_at,a.step_name,a.attempt LIMIT sqlc.arg(page_limit)::integer;

-- name: OperationTimelineDeliveryAttempts :many
-- The caller resolves the delivery through the owned delivery snapshot.
SELECT a.replay_generation, a.attempt_number, a.outcome, a.response_code, a.error,
  a.started_at, a.finished_at, a.next_attempt_at
FROM app_webhook_delivery_attempts a
JOIN app_webhook_deliveries d ON d.id=a.delivery_id
WHERE d.id=sqlc.arg(delivery_id)::uuid AND d.webhook_id=sqlc.arg(webhook_id)::uuid
  AND d.account_id=sqlc.arg(account_id)::uuid
  AND (a.replay_generation,a.attempt_number)>(sqlc.arg(after_generation)::integer,sqlc.arg(after_attempt)::integer)
ORDER BY a.replay_generation,a.attempt_number LIMIT sqlc.arg(page_limit)::integer;
