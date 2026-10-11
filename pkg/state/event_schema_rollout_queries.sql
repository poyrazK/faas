-- name: EventSchemaRolloutCandidates :many
-- Scan a fixed number of recent account receipts, including unrelated types.
SELECT id,source,event_type,created_at FROM event_fanout_outbox
WHERE account_id=sqlc.arg(account_id)::uuid
 AND created_at>=sqlc.arg(from_at)::timestamptz AND created_at<sqlc.arg(cutoff_at)::timestamptz
ORDER BY created_at DESC,id DESC LIMIT sqlc.arg(scan_limit)::integer;

-- name: EventSchemaRolloutPayloads :many
SELECT id,event_id,created_at,substring(convert_to(payload::text,'UTF8') FROM 1 FOR sqlc.arg(payload_limit)::integer)::bytea AS payload
FROM event_fanout_outbox WHERE account_id=sqlc.arg(account_id)::uuid AND id=ANY(sqlc.arg(ids)::bigint[])
ORDER BY created_at DESC,id DESC;
