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
