-- adr: 802
-- name: RegionalRuntimeUsageOutbox :one
SELECT * FROM regional_runtime_usage_outbox WHERE app_id=sqlc.arg(app_id) AND allocation_id=sqlc.arg(allocation_id) AND minute=sqlc.arg(minute);

-- name: InsertRegionalRuntimeUsageOutbox :one
INSERT INTO regional_runtime_usage_outbox(app_id,allocation_id,cell_id,minute,usage_claims,envelope)
VALUES(sqlc.arg(app_id),sqlc.arg(allocation_id),sqlc.arg(cell_id),sqlc.arg(minute),sqlc.arg(usage_claims),sqlc.arg(envelope)) RETURNING *;

-- name: AckRegionalRuntimeUsageOutbox :one
UPDATE regional_runtime_usage_outbox SET ack_claims=sqlc.arg(ack_claims),ack_envelope=sqlc.arg(ack_envelope)
WHERE app_id=sqlc.arg(app_id) AND allocation_id=sqlc.arg(allocation_id) AND minute=sqlc.arg(minute) AND ack_claims IS NULL RETURNING *;

-- name: ListPendingRegionalRuntimeUsage :many
SELECT * FROM regional_runtime_usage_outbox WHERE id>sqlc.arg(after_id) AND ack_claims IS NULL
AND (sqlc.narg(cell_id)::uuid IS NULL OR cell_id=sqlc.narg(cell_id)::uuid)
ORDER BY id LIMIT sqlc.arg(batch_limit)::int;
