-- name: InsertDurableAuditEventWithTrace :exec
INSERT INTO events (actor, kind, subject, data, outbox_id, trace_id)
VALUES (sqlc.arg(actor)::text, sqlc.arg(kind)::text, sqlc.narg(subject)::uuid, sqlc.arg(data)::jsonb, sqlc.arg(outbox_id)::bigint,
 CASE WHEN (sqlc.arg(data)::jsonb->>'trace_id') ~ '^[0-9a-f]{32}$' THEN sqlc.arg(data)::jsonb->>'trace_id' ELSE NULL END)
ON CONFLICT (outbox_id) WHERE outbox_id IS NOT NULL DO NOTHING;
