-- On-demand profile captures (ADR-967). apid queues and reads captures;
-- schedd claims, finishes and expires them. Reads join the owning app so a
-- deleted app's captures are invisible before the cascade removes them.
-- Lifecycle columns are merged into the public capture document on read.

-- name: LockProfileCaptureApp :one
SELECT id FROM apps WHERE id = sqlc.arg(app_id)::text::uuid AND account_id = sqlc.arg(account_id)::text::uuid AND status <> 'deleted' FOR UPDATE;

-- name: CountRecentProfileCaptures :one
SELECT count(*) FROM profile_captures
WHERE account_id = sqlc.arg(account_id)::text::uuid AND created_at >= sqlc.arg(since)::timestamptz;

-- name: CountActiveProfileCaptures :one
SELECT count(*) FROM profile_captures
WHERE app_id = sqlc.arg(app_id)::text::uuid AND status IN ('queued', 'capturing');

-- name: InsertProfileCapture :exec
INSERT INTO profile_captures (id, app_id, account_id, status, capture, created_at, expires_at)
VALUES (sqlc.arg(id)::text::uuid, sqlc.arg(app_id)::text::uuid, sqlc.arg(account_id)::text::uuid, 'queued', sqlc.arg(capture)::jsonb, sqlc.arg(created_at)::timestamptz, sqlc.arg(expires_at)::timestamptz);

-- name: ReadProfileCapture :one
SELECT (p.capture || jsonb_build_object('id', p.id, 'app_id', p.app_id, 'status', p.status, 'created_at', p.created_at, 'completed_at', p.completed_at, 'expires_at', p.expires_at))::jsonb AS capture
FROM profile_captures p JOIN apps a ON a.id = p.app_id
WHERE p.id = sqlc.arg(id)::text::uuid AND p.app_id = sqlc.arg(app_id)::text::uuid
  AND p.account_id = sqlc.arg(account_id)::text::uuid AND a.account_id = p.account_id AND a.status <> 'deleted';

-- name: ListProfileCaptures :many
SELECT (p.capture || jsonb_build_object('id', p.id, 'app_id', p.app_id, 'status', p.status, 'created_at', p.created_at, 'completed_at', p.completed_at, 'expires_at', p.expires_at))::jsonb AS capture
FROM profile_captures p JOIN apps a ON a.id = p.app_id
WHERE p.app_id = sqlc.arg(app_id)::text::uuid AND p.account_id = sqlc.arg(account_id)::text::uuid
  AND a.account_id = p.account_id AND a.status <> 'deleted'
ORDER BY p.created_at DESC, p.id LIMIT sqlc.arg(max_rows)::int;

-- name: ListProfileCaptureData :many
SELECT d.kind, d.process_id, d.profile FROM profile_capture_data d
JOIN profile_captures p ON p.id = d.capture_id JOIN apps a ON a.id = p.app_id
WHERE d.capture_id = sqlc.arg(id)::text::uuid AND p.app_id = sqlc.arg(app_id)::text::uuid
  AND p.account_id = sqlc.arg(account_id)::text::uuid AND a.account_id = p.account_id AND a.status <> 'deleted'
ORDER BY d.seq;

-- name: ClaimQueuedProfileCapture :one
UPDATE profile_captures SET status = 'capturing', claimed_at = sqlc.arg(now)::timestamptz
WHERE id = (SELECT q.id FROM profile_captures q WHERE q.status = 'queued' ORDER BY q.created_at, q.id FOR UPDATE SKIP LOCKED LIMIT 1)
RETURNING id::text AS id, app_id::text AS app_id, account_id::text AS account_id, capture;

-- name: FinishProfileCapture :execrows
UPDATE profile_captures SET status = sqlc.arg(status)::text, capture = capture || sqlc.arg(result)::jsonb, completed_at = sqlc.arg(now)::timestamptz
WHERE id = sqlc.arg(id)::text::uuid AND status = 'capturing';

-- name: InsertProfileCaptureData :exec
INSERT INTO profile_capture_data (capture_id, seq, kind, process_id, profile)
VALUES (sqlc.arg(capture_id)::text::uuid, sqlc.arg(seq)::smallint, sqlc.arg(kind)::text, sqlc.arg(process_id)::text, sqlc.arg(profile)::bytea);

-- name: FailStaleProfileCaptures :execrows
UPDATE profile_captures SET status = 'failed', completed_at = sqlc.arg(now)::timestamptz,
  capture = capture || jsonb_build_object('reason', sqlc.arg(reason)::text)
WHERE (status = 'capturing' AND claimed_at < sqlc.arg(claimed_before)::timestamptz)
   OR (status = 'queued' AND created_at < sqlc.arg(queued_before)::timestamptz);

-- name: DeleteExpiredProfileCaptures :execrows
DELETE FROM profile_captures WHERE expires_at < sqlc.arg(now)::timestamptz;
