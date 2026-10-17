-- name: DomainLifecycleSchema :exec
SELECT d.dkim_key_id,d.authorization_state,r.signing_selector,k.first_verified_at
FROM internal_mail_domains d,internal_mail_recipients r,internal_mail_dkim_keys k LIMIT 0;

-- name: EnsureLegacyDKIMKey :exec
INSERT INTO internal_mail_dkim_keys(id,namespace,domain_id,selector,public_key,sealed_key,legacy,state,created_at,activated_at)
SELECT d.id,d.namespace,d.id,d.selector,d.public_key,d.sealed_key,true,'active',d.created_at,d.created_at FROM internal_mail_domains d
WHERE d.namespace=$1 AND d.id=$2 AND d.dkim_key_id IS NULL ON CONFLICT DO NOTHING;

-- name: LockMailDomain :one
SELECT * FROM internal_mail_domains WHERE namespace=$1 AND id=$2 FOR UPDATE;

-- name: LockSubmissionDomain :one
SELECT d.* FROM internal_mail_domains d
JOIN internal_mail_messages m ON m.namespace=d.namespace AND m.domain_id=d.id
JOIN internal_mail_recipients r ON r.namespace=m.namespace AND r.message_id=m.id
WHERE r.namespace=$1 AND r.id=$2 FOR UPDATE OF d;

-- name: ConfirmInitialDomain :execrows
UPDATE internal_mail_domains SET status='verified' WHERE namespace=$1 AND id=$2 AND selector=$3 AND status<>'disabled';

-- name: LiveDKIMKeys :one
SELECT count(*)::bigint FROM internal_mail_dkim_keys WHERE namespace=$1 AND domain_id=$2 AND state<>'retired';

-- name: CreateDKIMKey :one
INSERT INTO internal_mail_dkim_keys(id,namespace,domain_id,selector,public_key,sealed_key,state)
VALUES($1,$2,$3,$4,$5,$6,'staged') RETURNING *;

-- name: DKIMKey :one
SELECT * FROM internal_mail_dkim_keys WHERE namespace=$1 AND domain_id=$2 AND id=$3;

-- name: OperatorDKIMKeys :many
SELECT id,domain_id,selector,public_key,legacy,state,check_result,checked_at,first_verified_at,created_at,activated_at,deactivated_at
FROM internal_mail_dkim_keys WHERE namespace=sqlc.arg(namespace) AND domain_id=sqlc.arg(domain_id) AND id>sqlc.arg(after_id)::uuid
ORDER BY id LIMIT sqlc.arg(page_limit);

-- name: RequiredDKIMRecords :many
SELECT id,domain_id,selector,public_key,legacy,state,check_result,checked_at,first_verified_at,created_at,activated_at,deactivated_at
FROM internal_mail_dkim_keys WHERE namespace=$1 AND domain_id=$2 AND state='retiring' ORDER BY id LIMIT $3;

-- name: RecordDKIMVerification :one
UPDATE internal_mail_dkim_keys SET checked_at=now(),check_result=sqlc.arg(outcome),
    first_verified_at=CASE WHEN sqlc.arg(outcome)='valid' THEN coalesce(first_verified_at,now())
                         WHEN sqlc.arg(outcome)='mismatch' THEN NULL ELSE first_verified_at END
WHERE namespace=sqlc.arg(namespace) AND domain_id=sqlc.arg(domain_id) AND id=sqlc.arg(key_id) AND state='staged' RETURNING *;

-- name: DeactivateDKIMKey :execrows
UPDATE internal_mail_dkim_keys SET state='retiring',deactivated_at=now() WHERE namespace=$1 AND domain_id=$2 AND state='active';

-- name: ActivateDKIMKey :one
UPDATE internal_mail_dkim_keys SET state='active',activated_at=now()
WHERE namespace=sqlc.arg(namespace) AND domain_id=sqlc.arg(domain_id) AND id=sqlc.arg(key_id)
  AND state='staged' AND check_result='valid' AND first_verified_at<=now()-sqlc.arg(propagation_seconds)::integer*interval '1 second'
RETURNING *;

-- name: SetDomainSigner :one
UPDATE internal_mail_domains SET selector=$3,public_key=$4,sealed_key=$5,dkim_key_id=$6,
    dns_revision=dns_revision+1,
    authorization_state='valid',check_result='valid',last_checked_at=now(),last_verified_at=now(),
    verified_sending_ip=$7,failure_count=0,first_failure_at=NULL
WHERE namespace=$1 AND id=$2 AND status='verified' RETURNING *;

-- name: RetireDKIMKey :one
UPDATE internal_mail_dkim_keys k SET state='retired',sealed_key=''::bytea,deactivated_at=coalesce(k.deactivated_at,now())
WHERE k.namespace=sqlc.arg(namespace) AND k.domain_id=sqlc.arg(domain_id) AND k.id=sqlc.arg(key_id)
  AND (k.state='staged' OR (k.state='retiring' AND k.deactivated_at<=now()-sqlc.arg(overlap_seconds)::integer*interval '1 second'
  AND NOT EXISTS (SELECT 1 FROM internal_mail_recipients r JOIN internal_mail_messages m ON m.namespace=r.namespace AND m.id=r.message_id
                  WHERE r.namespace=k.namespace AND m.domain_id=k.domain_id AND r.status IN ('submitting','submitted','uncertain')
                    AND (r.signing_selector=k.selector OR r.signing_selector=''))
  )) RETURNING *;

-- name: BumpDomainDNSRevision :exec
UPDATE internal_mail_domains SET dns_revision=dns_revision+1 WHERE namespace=$1 AND id=$2;

-- name: DomainCheckCandidates :many
SELECT id,name,status,selector,verification_token,public_key,created_at,dkim_key_id,authorization_state,last_checked_at,last_verified_at,verified_sending_ip,check_result,failure_count,first_failure_at,dns_revision
FROM internal_mail_domains
WHERE namespace=sqlc.arg(namespace) AND status='verified'
  AND (last_checked_at IS NULL OR last_checked_at<=now()-
      CASE WHEN authorization_state='failing' THEN sqlc.arg(failure_separation_seconds)::integer
           WHEN verified_sending_ip<>sqlc.arg(sending_ip) THEN sqlc.arg(poll_seconds)::integer
           ELSE sqlc.arg(interval_seconds)::integer END*interval '1 second')
ORDER BY last_checked_at NULLS FIRST,id LIMIT sqlc.arg(batch_size);

-- name: RecordDomainCheck :one
UPDATE internal_mail_domains d SET authorization_state=sqlc.arg(authorization_state),check_result=sqlc.arg(outcome),last_checked_at=now(),
    last_verified_at=CASE WHEN sqlc.arg(outcome)='valid' THEN now() ELSE last_verified_at END,
    verified_sending_ip=CASE WHEN sqlc.arg(outcome)='valid' THEN sqlc.arg(sending_ip) ELSE verified_sending_ip END,
    failure_count=sqlc.arg(failure_count),first_failure_at=sqlc.narg(first_failure_at),
    status=CASE WHEN sqlc.arg(initial_verification)::boolean THEN 'verified' ELSE status END
WHERE d.namespace=sqlc.arg(namespace) AND d.id=sqlc.arg(domain_id) AND d.status<>'disabled' RETURNING d.*;

-- name: DomainLifecycleStatistics :one
SELECT
  (SELECT count(*) FROM internal_mail_domains d WHERE d.namespace=sqlc.arg(namespace) AND d.status='verified' AND d.authorization_state='paused')::bigint AS paused,
  (SELECT count(*) FROM internal_mail_domains d WHERE d.namespace=sqlc.arg(namespace) AND d.status='verified' AND
    (d.last_verified_at IS NULL OR d.last_verified_at<now()-sqlc.arg(max_age_seconds)::integer*interval '1 second' OR d.verified_sending_ip<>sqlc.arg(sending_ip)))::bigint AS stale,
  (SELECT count(*) FROM internal_mail_domains d WHERE d.namespace=sqlc.arg(namespace) AND d.status='verified' AND d.check_result='unavailable')::bigint AS unavailable,
  (SELECT count(*) FROM internal_mail_dkim_keys k WHERE k.namespace=sqlc.arg(namespace) AND k.state='staged')::bigint AS staged,
  (SELECT count(*) FROM internal_mail_dkim_keys k WHERE k.namespace=sqlc.arg(namespace) AND k.state='staged' AND k.check_result IN ('mismatch','unavailable'))::bigint AS failed_keys,
  (SELECT coalesce(greatest(0,extract(epoch FROM now()-min(k.created_at))),0)::float8 FROM internal_mail_dkim_keys k WHERE k.namespace=sqlc.arg(namespace) AND k.state='staged') AS oldest_staged_seconds;
