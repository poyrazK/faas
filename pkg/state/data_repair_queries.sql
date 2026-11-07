-- name: LockDataRepairOperation :one
SELECT record FROM customer_operations
WHERE id=sqlc.arg(id)::uuid AND account_id=sqlc.arg(account_id)::uuid FOR UPDATE;

-- Only unapproved drafts without a physical task can settle on local expiry.
-- Share the admission predicate and skip lifecycle rows locked by foreground work.
-- name: LockExpiredDataRepairDrafts :many
SELECT o.record FROM customer_operations o
WHERE o.data_repair_id IS NOT NULL AND o.state IN ('accepted','running','waiting','requires_reconciliation')
 AND COALESCE(o.record->'data_repair'->>'phase','')='prepared'
 AND COALESCE(o.record->'data_repair'->'approval','null'::jsonb)='null'::jsonb
 AND COALESCE(o.record->'data_repair'->>'rehearsal_status','') NOT IN ('queued','running','cleanup','publishing')
 AND (o.record->'data_repair'->>'plan_expires_at')::timestamptz<=sqlc.arg(observed_at)::timestamptz
 AND NOT EXISTS(SELECT 1 FROM data_repair_rehearsals r WHERE r.operation_id=o.id AND r.phase IN ('queued','running','cleanup','publishing'))
ORDER BY (o.record->'data_repair'->>'plan_expires_at')::timestamptz,o.id
LIMIT sqlc.arg(page_limit)::integer FOR UPDATE OF o SKIP LOCKED;

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
