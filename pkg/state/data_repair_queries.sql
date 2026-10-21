-- name: LockDataRepairOperation :one
SELECT record FROM customer_operations
WHERE id=sqlc.arg(id)::uuid AND account_id=sqlc.arg(account_id)::uuid FOR UPDATE;

-- name: InsertDataRepairOperation :exec
INSERT INTO customer_operations(id,account_id,app_id,platform_tenant_id,data_repair_id,state,record,expires_at,created_at)
VALUES(sqlc.arg(id)::uuid,sqlc.arg(account_id)::uuid,sqlc.arg(app_id)::uuid,sqlc.arg(tenant_id)::uuid,
 sqlc.arg(id)::uuid,sqlc.arg(state)::text,sqlc.arg(record)::jsonb,sqlc.arg(expires_at)::timestamptz,sqlc.arg(created_at)::timestamptz);

-- name: InsertDataRepairExecution :exec
INSERT INTO customer_operation_executions(operation_id,generation,data_repair_id)
VALUES(sqlc.arg(id)::uuid,1,sqlc.arg(id)::uuid);

-- name: UpdateDataRepairOperation :exec
UPDATE customer_operations SET state=sqlc.arg(state)::text,record=sqlc.arg(record)::jsonb,
 expires_at=sqlc.arg(expires_at)::timestamptz
WHERE id=sqlc.arg(id)::uuid AND data_repair_id=sqlc.arg(id)::uuid;
