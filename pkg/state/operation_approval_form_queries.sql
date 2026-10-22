-- ADR-20261019001500 Approval input forms.

-- name: LockOperationApprovalFormByKey :one
SELECT * FROM customer_operation_approval_forms
WHERE app_id = sqlc.arg(app_id) AND scope = sqlc.arg(scope) AND operation = sqlc.arg(operation) AND step = sqlc.arg(step)
FOR UPDATE;

-- name: GetOperationApprovalFormByKey :one
SELECT * FROM customer_operation_approval_forms
WHERE app_id = sqlc.arg(app_id) AND scope = sqlc.arg(scope) AND operation = sqlc.arg(operation) AND step = sqlc.arg(step);

-- name: InsertOperationApprovalForm :one
INSERT INTO customer_operation_approval_forms(id, account_id, app_id, scope, operation, step, input_schema, required, created_at, updated_at)
VALUES (sqlc.arg(id), sqlc.arg(account_id), sqlc.arg(app_id), sqlc.arg(scope), sqlc.arg(operation), sqlc.arg(step), sqlc.arg(input_schema),
    sqlc.arg(required), sqlc.arg(now), sqlc.arg(now))
RETURNING *;

-- name: UpdateOperationApprovalForm :one
UPDATE customer_operation_approval_forms SET input_schema = sqlc.arg(input_schema), required = sqlc.arg(required), updated_at = sqlc.arg(now)
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: CountOperationApprovalForms :one
SELECT count(*)::bigint FROM customer_operation_approval_forms WHERE app_id = sqlc.arg(app_id);

-- name: ListOperationApprovalForms :many
SELECT * FROM customer_operation_approval_forms
WHERE account_id = sqlc.arg(account_id) AND app_id = sqlc.arg(app_id)
ORDER BY scope, operation, step;

-- name: DeleteOperationApprovalForm :execrows
DELETE FROM customer_operation_approval_forms
WHERE id = sqlc.arg(id) AND account_id = sqlc.arg(account_id) AND app_id = sqlc.arg(app_id);

-- name: GetWorkflowCallbackPayload :one
SELECT payload FROM workflow_events WHERE id = sqlc.arg(id);
