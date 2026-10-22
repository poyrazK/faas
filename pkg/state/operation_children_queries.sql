-- ADR-20261018183000 Child Operations.

-- name: GetOperationChildByKey :one
SELECT child_operation_id FROM customer_operation_children
WHERE parent_operation_id = sqlc.arg(parent_operation_id) AND child_key = sqlc.arg(child_key);

-- name: IsOperationChild :one
SELECT EXISTS (SELECT 1 FROM customer_operation_children WHERE child_operation_id = sqlc.arg(operation_id))::bool;

-- name: CountOperationChildren :one
SELECT count(*)::bigint FROM customer_operation_children WHERE parent_operation_id = sqlc.arg(parent_operation_id);

-- name: InsertOperationChild :exec
INSERT INTO customer_operation_children(parent_operation_id, child_key, child_operation_id, account_id, created_at)
VALUES (sqlc.arg(parent_operation_id), sqlc.arg(child_key), sqlc.arg(child_operation_id), sqlc.arg(account_id), sqlc.arg(created_at));

-- name: ListOperationChildren :many
SELECT c.child_key, o.record FROM customer_operation_children c
JOIN customer_operations o ON o.id = c.child_operation_id
WHERE c.parent_operation_id = sqlc.arg(parent_operation_id) AND c.account_id = sqlc.arg(account_id)
ORDER BY c.created_at, c.child_key
LIMIT sqlc.arg(row_limit);

-- A parent waiting on the children-settled event whose children have all
-- settled; a parent that started none qualifies at once.
-- name: ListSettledChildParents :many
SELECT o.id, o.account_id,
    count(co.id)::bigint AS total,
    count(*) FILTER (WHERE co.state = 'succeeded')::bigint AS succeeded,
    count(*) FILTER (WHERE co.state = 'failed')::bigint AS failed,
    count(*) FILTER (WHERE co.state = 'cancelled')::bigint AS cancelled,
    greatest((o.record ->> 'updated_at')::timestamptz, max((co.record ->> 'updated_at')::timestamptz))::timestamptz AS ready_since
FROM customer_operations o
LEFT JOIN customer_operation_children c ON c.parent_operation_id = o.id
LEFT JOIN customer_operations co ON co.id = c.child_operation_id
WHERE o.state = 'waiting' AND (o.record -> 'waiting' ->> 'kind') = 'event'
  AND (o.record -> 'waiting' ->> 'event') = sqlc.arg(event)::text
  AND NOT EXISTS (SELECT 1 FROM workflow_events e WHERE e.run_id = o.workflow_run_id AND e.event_name = sqlc.arg(event)::text)
GROUP BY o.id, o.account_id
HAVING count(co.id) = count(*) FILTER (WHERE co.state IN ('succeeded', 'failed', 'cancelled'))
ORDER BY ready_since, o.id
LIMIT sqlc.arg(row_limit);
