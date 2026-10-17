-- name: DeliveryControlsSchema :exec
SELECT mode,revision FROM internal_mail_delivery_controls LIMIT 0;

-- The sentinel UUID denotes the namespace, not an HTTP-selectable namespace.
-- Shared submission locks and exclusive operator locks also fence the first
-- control insertion, when there is no row yet to lock.
-- name: ShareDeliveryScope :exec
SELECT pg_advisory_xact_lock_shared(hashtextextended(sqlc.arg(namespace)::text||':delivery:'||sqlc.arg(scope_id)::uuid::text,871728));

-- name: LockDeliveryScope :exec
SELECT pg_advisory_xact_lock(hashtextextended(sqlc.arg(namespace)::text||':delivery:'||sqlc.arg(scope_id)::uuid::text,871728));

-- name: RecipientDeliveryDomain :one
SELECT m.domain_id FROM internal_mail_messages m JOIN internal_mail_recipients r ON r.namespace=m.namespace AND r.message_id=m.id
WHERE r.namespace=$1 AND r.id=$2;

-- name: DispatchPermitted :one
SELECT NOT EXISTS (SELECT 1 FROM internal_mail_delivery_controls c WHERE c.namespace=$1
    AND c.scope_id IN ('00000000-0000-0000-0000-000000000000'::uuid,$2::uuid) AND c.mode<>'running');

-- name: DeliveryControl :one
SELECT * FROM internal_mail_delivery_controls WHERE namespace=$1 AND scope_id=$2;

-- name: SetDeliveryControl :exec
INSERT INTO internal_mail_delivery_controls(namespace,scope_id,domain_id,mode,revision,request_id)
VALUES($1,$2,$3,$4,1,$5)
ON CONFLICT(namespace,scope_id) DO UPDATE SET mode=excluded.mode,revision=internal_mail_delivery_controls.revision+1,
    request_id=excluded.request_id,updated_at=now();

-- name: DeliveryControlStatus :one
SELECT
  coalesce((SELECT c.mode FROM internal_mail_delivery_controls c WHERE c.namespace=sqlc.arg(namespace) AND c.scope_id=sqlc.arg(scope_id)),'running')::text AS mode,
  coalesce((SELECT c.revision FROM internal_mail_delivery_controls c WHERE c.namespace=sqlc.arg(namespace) AND c.scope_id=sqlc.arg(scope_id)),0)::bigint AS revision,
  (SELECT c.request_id FROM internal_mail_delivery_controls c WHERE c.namespace=sqlc.arg(namespace) AND c.scope_id=sqlc.arg(scope_id)) AS request_id,
  (SELECT c.updated_at FROM internal_mail_delivery_controls c WHERE c.namespace=sqlc.arg(namespace) AND c.scope_id=sqlc.arg(scope_id)) AS updated_at,
  coalesce((SELECT c.mode FROM internal_mail_delivery_controls c WHERE c.namespace=sqlc.arg(namespace) AND c.scope_id='00000000-0000-0000-0000-000000000000'::uuid),'running')::text AS namespace_mode,
  (SELECT count(*) FROM internal_mail_recipients r JOIN internal_mail_messages m ON m.namespace=r.namespace AND m.id=r.message_id
      WHERE r.namespace=sqlc.arg(namespace) AND (sqlc.arg(scope_id)::uuid='00000000-0000-0000-0000-000000000000'::uuid OR m.domain_id=sqlc.arg(scope_id)) AND r.status IN ('queued','deferred'))::bigint AS queued,
  (SELECT count(*) FROM internal_mail_recipients r JOIN internal_mail_messages m ON m.namespace=r.namespace AND m.id=r.message_id
      WHERE r.namespace=sqlc.arg(namespace) AND (sqlc.arg(scope_id)::uuid='00000000-0000-0000-0000-000000000000'::uuid OR m.domain_id=sqlc.arg(scope_id)) AND r.status='claimed' AND r.lease_until>now())::bigint AS active_claims,
  (SELECT count(*) FROM internal_mail_recipients r JOIN internal_mail_messages m ON m.namespace=r.namespace AND m.id=r.message_id
      WHERE r.namespace=sqlc.arg(namespace) AND (sqlc.arg(scope_id)::uuid='00000000-0000-0000-0000-000000000000'::uuid OR m.domain_id=sqlc.arg(scope_id)) AND r.status='claimed' AND r.lease_until<=now())::bigint AS expired_claims,
  (SELECT count(*) FROM internal_mail_recipients r JOIN internal_mail_messages m ON m.namespace=r.namespace AND m.id=r.message_id
      WHERE r.namespace=sqlc.arg(namespace) AND (sqlc.arg(scope_id)::uuid='00000000-0000-0000-0000-000000000000'::uuid OR m.domain_id=sqlc.arg(scope_id)) AND r.status='submitting' AND r.lease_until>now())::bigint AS active_submissions,
  (SELECT count(*) FROM internal_mail_recipients r JOIN internal_mail_messages m ON m.namespace=r.namespace AND m.id=r.message_id
      WHERE r.namespace=sqlc.arg(namespace) AND (sqlc.arg(scope_id)::uuid='00000000-0000-0000-0000-000000000000'::uuid OR m.domain_id=sqlc.arg(scope_id)) AND (r.status='uncertain' OR (r.status='submitting' AND r.lease_until<=now())))::bigint AS uncertain,
  (SELECT count(*) FROM internal_mail_recipients r JOIN internal_mail_messages m ON m.namespace=r.namespace AND m.id=r.message_id
      WHERE r.namespace=sqlc.arg(namespace) AND (sqlc.arg(scope_id)::uuid='00000000-0000-0000-0000-000000000000'::uuid OR m.domain_id=sqlc.arg(scope_id)) AND r.status='submitted')::bigint AS relay_owned,
  (SELECT count(*) FROM internal_mail_recipients r JOIN internal_mail_messages m ON m.namespace=r.namespace AND m.id=r.message_id
      WHERE r.namespace=sqlc.arg(namespace) AND (sqlc.arg(scope_id)::uuid='00000000-0000-0000-0000-000000000000'::uuid OR m.domain_id=sqlc.arg(scope_id)) AND r.status='cancelled')::bigint AS cancelled,
  (SELECT count(*) FROM internal_mail_recipients r JOIN internal_mail_messages m ON m.namespace=r.namespace AND m.id=r.message_id
      WHERE r.namespace=sqlc.arg(namespace) AND (sqlc.arg(scope_id)::uuid='00000000-0000-0000-0000-000000000000'::uuid OR m.domain_id=sqlc.arg(scope_id)) AND r.status='expired')::bigint AS expired;

-- name: CancellationMessage :one
SELECT id FROM internal_mail_messages WHERE namespace=$1 AND id=$2;

-- name: LockMessageCancellation :many
SELECT id,status FROM internal_mail_recipients WHERE namespace=$1 AND message_id=$2 ORDER BY id LIMIT $3 FOR UPDATE;

-- name: LockRecipientCancellation :one
SELECT id,status FROM internal_mail_recipients WHERE namespace=$1 AND id=$2 FOR UPDATE;

-- name: CancelRecipient :execrows
UPDATE internal_mail_recipients SET status='cancelled',code='operator_cancelled',lease_id=NULL,lease_until=NULL,updated_at=now()
WHERE namespace=$1 AND id=$2 AND status IN ('queued','deferred','claimed');

-- name: RecipientStopped :one
SELECT EXISTS(SELECT 1 FROM internal_mail_recipients WHERE namespace=$1 AND id=$2 AND status IN ('cancelled','expired'));

-- name: DeliveryControlsStatistics :many
SELECT mode,count(*)::bigint AS total FROM internal_mail_delivery_controls WHERE namespace=$1 GROUP BY mode;
