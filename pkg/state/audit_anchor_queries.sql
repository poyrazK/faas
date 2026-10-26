-- name: LatestAuditLogAnchor :many
-- ADR-20261026163015: the newest anchor, or no row before the first.
SELECT seq, window_start, window_end, row_count, rows_digest, prev_mac, mac, key_id, created_at
FROM audit_log_anchors
ORDER BY seq DESC
LIMIT 1;

-- name: ListAuditLogAnchors :many
SELECT seq, window_start, window_end, row_count, rows_digest, prev_mac, mac, key_id, created_at
FROM audit_log_anchors
WHERE seq >= sqlc.arg(from_seq)::bigint
ORDER BY seq
LIMIT sqlc.arg(page_limit)::integer;

-- name: FirstAuditLogAnchorEndingAfter :many
-- The first anchor whose window ends after a time: where a verification of
-- recent history starts.
SELECT seq
FROM audit_log_anchors
WHERE window_end > sqlc.arg(after)::timestamptz
ORDER BY seq
LIMIT 1;

-- name: InsertAuditLogAnchor :execrows
-- A second anchorer racing for the same seq inserts nothing.
INSERT INTO audit_log_anchors (seq, window_start, window_end, row_count, rows_digest, prev_mac, mac, key_id)
VALUES (sqlc.arg(seq)::bigint, sqlc.arg(window_start)::timestamptz, sqlc.arg(window_end)::timestamptz,
        sqlc.arg(row_count)::bigint, sqlc.arg(rows_digest)::text, sqlc.arg(prev_mac)::text,
        sqlc.arg(mac)::text, sqlc.arg(key_id)::text)
ON CONFLICT (seq) DO NOTHING;

-- name: AuditLogWindowPage :many
-- One page of the audit rows received in [window_start, window_end), in the
-- order anchors digest them, after the (received_at, id) cursor. data is
-- jsonb's canonical text, so the digest does not depend on how a row was
-- written.
SELECT id, kind, account_id, coalesce(account_email, '')::text AS account_email,
       coalesce(actor, '')::text AS actor, received_at, coalesce(data::text, '')::text AS data
FROM audit_log
WHERE received_at >= sqlc.arg(window_start)::timestamptz
  AND received_at < sqlc.arg(window_end)::timestamptz
  AND (received_at, id) > (sqlc.arg(after_at)::timestamptz, sqlc.arg(after_id)::uuid)
ORDER BY received_at, id
LIMIT sqlc.arg(page_limit)::integer;
