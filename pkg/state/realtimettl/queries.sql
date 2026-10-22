-- name: ReadExpirations :many
SELECT sequence, ttl_seconds, expires_at FROM managed_realtime_message_expirations
WHERE endpoint_id = sqlc.arg(endpoint_id)::text::uuid AND channel = sqlc.arg(channel)::text
AND sequence = ANY(sqlc.arg(sequences)::bigint[]);

-- name: SaveExpiration :exec
INSERT INTO managed_realtime_message_expirations(endpoint_id, channel, sequence, ttl_seconds, expires_at)
VALUES(sqlc.arg(endpoint_id)::text::uuid, sqlc.arg(channel)::text, sqlc.arg(sequence)::bigint, sqlc.arg(ttl_seconds)::integer, sqlc.arg(expires_at)::timestamptz);

-- name: SetScheduleTTL :exec
UPDATE managed_realtime_schedules SET ttl_seconds = sqlc.arg(ttl_seconds)::integer
WHERE endpoint_id = sqlc.arg(endpoint_id)::text::uuid AND channel = sqlc.arg(channel)::text AND schedule_id = sqlc.arg(schedule_id)::text;

-- name: LeaseRealtimeCommits :many
WITH candidates AS (
 SELECT event_id FROM public.gregale_realtime_outbox
 WHERE published_at IS NULL AND blocked_code IS NULL AND next_attempt_at<=clock_timestamp()
 AND (lease_until IS NULL OR lease_until<=clock_timestamp())
 AND EXISTS(SELECT 1 FROM public.gregale_commit_binding WHERE singleton AND source_id=sqlc.arg(source_id)::text::uuid)
 ORDER BY next_attempt_at,created_at,event_id FOR UPDATE SKIP LOCKED LIMIT 32
 ) UPDATE public.gregale_realtime_outbox o SET lease_token=sqlc.arg(lease_token)::text::uuid,
 lease_until=clock_timestamp()+interval '1 minute',attempts=attempts+1
 FROM candidates c WHERE o.event_id=c.event_id RETURNING o.event_id::text,o.endpoint_id,o.channel,o.data,o.metadata,coalesce((to_jsonb(o)->>'ttl_seconds')::integer,0)::integer AS ttl_seconds;

-- name: SetPublisherTTLCap :exec
UPDATE managed_realtime_publisher_credentials SET max_message_ttl_seconds = sqlc.arg(max_message_ttl_seconds)::integer WHERE id = sqlc.arg(id)::text::uuid;

-- name: ReadPublisherTTLCap :one
SELECT max_message_ttl_seconds FROM managed_realtime_publisher_credentials WHERE id = sqlc.arg(id)::text::uuid;

-- name: ReadInboxExpirations :many
SELECT sequence, ttl_seconds, expires_at FROM managed_realtime_inbox_message_expirations
WHERE endpoint_id = sqlc.arg(endpoint_id)::text::uuid AND channel = sqlc.arg(channel)::text
AND sequence = ANY(sqlc.arg(sequences)::bigint[]);

-- name: SaveInboxExpiration :exec
INSERT INTO managed_realtime_inbox_message_expirations(endpoint_id, channel, sequence, ttl_seconds, expires_at)
VALUES(sqlc.arg(endpoint_id)::text::uuid, sqlc.arg(channel)::text, sqlc.arg(sequence)::bigint, sqlc.arg(ttl_seconds)::integer, sqlc.arg(expires_at)::timestamptz);
