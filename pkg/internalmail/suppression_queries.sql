-- name: SuppressionControlsSchema :exec
SELECT s.last_block_revision,e.source_kind,r.suppression_revision FROM internal_mail_suppression_state s,internal_mail_suppression_events e,internal_mail_recipients r LIMIT 0;

-- Both first insertion and later changes use the address fence (seed 871738).
-- name: ShareSuppressionAddress :exec
SELECT pg_advisory_xact_lock_shared(hashtextextended(sqlc.arg(namespace)::text||':'||lower(sqlc.arg(address)::text),871738));

-- name: LockSuppressionAddress :exec
SELECT pg_advisory_xact_lock(hashtextextended(sqlc.arg(namespace)::text||':'||lower(sqlc.arg(address)::text),871738));

-- name: RecipientSuppressionAddress :one
SELECT address FROM internal_mail_recipients WHERE namespace=$1 AND id=$2;

-- name: RecipientSuppressed :one
SELECT EXISTS(SELECT 1 FROM internal_mail_suppressions s WHERE s.namespace=r.namespace AND s.address=lower(r.address))
    OR EXISTS(SELECT 1 FROM internal_mail_suppression_state s WHERE s.namespace=r.namespace AND s.address=lower(r.address)
        AND s.last_block_revision>r.suppression_revision) AS suppressed
FROM internal_mail_recipients r WHERE r.namespace=$1 AND r.id=$2;

-- name: SuppressionState :one
SELECT * FROM internal_mail_suppression_state WHERE namespace=$1 AND address=$2;

-- name: SuppressionProjection :one
SELECT * FROM internal_mail_suppressions WHERE namespace=$1 AND address=$2;

-- A conservative bootstrap for legacy projection rows (including an upgraded
-- fixture). New binaries always update state and projection together.
-- name: EnsureSuppressionState :exec
INSERT INTO internal_mail_suppression_state(namespace,address,revision,last_block_revision,blocked,reason,cause,source_kind,created_at,updated_at)
SELECT p.namespace,p.address,1,1,true,p.reason,'legacy','legacy',p.created_at,p.created_at FROM internal_mail_suppressions p
WHERE p.namespace=$1 AND p.address=$2 ON CONFLICT DO NOTHING;

-- name: EnsureSuppressionLegacyEvent :exec
INSERT INTO internal_mail_suppression_events(namespace,address,revision,blocked,reason,cause,source_kind,created_at)
SELECT s.namespace,s.address,s.revision,s.blocked,s.reason,s.cause,s.source_kind,s.updated_at FROM internal_mail_suppression_state s
WHERE s.namespace=$1 AND s.address=$2 AND s.cause='legacy' AND s.revision=1 ON CONFLICT DO NOTHING;

-- name: BlockSuppressionState :one
INSERT INTO internal_mail_suppression_state(namespace,address,revision,last_block_revision,blocked,reason,cause,source_kind,source_id,recipient_id,event_ref,request_id)
VALUES(sqlc.arg(namespace),sqlc.arg(address),1,1,true,sqlc.arg(reason),sqlc.arg(cause),sqlc.arg(source_kind),sqlc.arg(source_id),sqlc.narg(recipient_id),sqlc.arg(event_ref),sqlc.narg(request_id))
ON CONFLICT(namespace,address) DO UPDATE SET
    revision=internal_mail_suppression_state.revision+1,last_block_revision=internal_mail_suppression_state.revision+1,
    blocked=true,reason=CASE WHEN internal_mail_suppression_state.reason='complained' THEN 'complained' ELSE excluded.reason END,
    cause=excluded.cause,source_kind=excluded.source_kind,source_id=excluded.source_id,recipient_id=excluded.recipient_id,
    event_ref=excluded.event_ref,request_id=excluded.request_id,updated_at=clock_timestamp()
RETURNING *;

-- name: ReleaseSuppressionState :one
UPDATE internal_mail_suppression_state SET revision=revision+1,blocked=false,cause='release',source_kind='operator',source_id='',
    recipient_id=NULL,event_ref='',request_id=sqlc.arg(request_id),updated_at=clock_timestamp()
WHERE namespace=sqlc.arg(namespace) AND address=sqlc.arg(address) AND revision=sqlc.arg(expected_revision) AND blocked AND reason<>'complained'
RETURNING *;

-- name: DeleteSuppressionProjection :exec
DELETE FROM internal_mail_suppressions WHERE namespace=$1 AND address=$2;

-- name: RecordSuppressionEvent :exec
INSERT INTO internal_mail_suppression_events(namespace,address,revision,blocked,reason,cause,source_kind,source_id,recipient_id,event_ref,request_id,created_at)
SELECT s.namespace,s.address,s.revision,s.blocked,s.reason,s.cause,s.source_kind,s.source_id,s.recipient_id,s.event_ref,s.request_id,s.updated_at
FROM internal_mail_suppression_state s WHERE s.namespace=$1 AND s.address=$2;

-- name: SuppressionHistory :many
SELECT * FROM internal_mail_suppression_events WHERE namespace=sqlc.arg(namespace) AND address=sqlc.arg(address) AND revision>sqlc.arg(after_revision)::bigint
ORDER BY revision LIMIT sqlc.arg(page_limit)::integer;

-- name: SuppressionControlStatistics :one
SELECT
    (SELECT count(*) FROM internal_mail_suppressions m WHERE m.namespace=sqlc.arg(namespace) AND m.reason='manual')::bigint AS manual_blocks,
    (SELECT count(*) FROM internal_mail_suppressions b WHERE b.namespace=sqlc.arg(namespace) AND b.reason='bounced')::bigint AS bounce_blocks,
    (SELECT count(*) FROM internal_mail_suppressions c WHERE c.namespace=sqlc.arg(namespace) AND c.reason='complained')::bigint AS complaint_blocks,
    (SELECT count(*) FROM internal_mail_suppression_state s WHERE s.namespace=sqlc.arg(namespace) AND NOT s.blocked)::bigint AS released_addresses;
