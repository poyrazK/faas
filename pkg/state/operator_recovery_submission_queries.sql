-- name: LockOperatorRecoverySubmission :exec
SELECT pg_advisory_xact_lock(hashtextextended('operator-recovery-submission:' || sqlc.arg(actor_id)::uuid::text || ':' || sqlc.arg(key_digest)::text, 0));

-- name: ReadOperatorRecoverySubmission :one
SELECT s.*, i.kind, i.target_id FROM operator_recovery_submissions s
JOIN operator_intents i ON i.id = s.intent_id
WHERE s.actor_id = sqlc.arg(actor_id)::uuid AND s.key_digest = sqlc.arg(key_digest)::text;

-- Legacy rows are ambiguous even after the old cache's TTL. Never reclaim them here.
-- name: OperatorRecoveryLegacyKeyExists :one
SELECT EXISTS (SELECT 1 FROM idempotency_keys WHERE account_id = sqlc.arg(actor_id)::uuid
 AND right(key, length(sqlc.arg(request_key)::text) + 1) = E'\n' || sqlc.arg(request_key)::text
 AND split_part(key, E'\n', 1) ~ '^POST /v1/admin/(instances/[^/]+/force-(park|restart)|apps/[^/]+/force-cold-boot)$')::boolean;

-- name: ReadOperatorRecoveryInstance :one
SELECT i.id::text AS instance_id, i.app_id::text AS app_id, i.deployment_id::text AS deployment_id,
       i.state, a.account_id::text AS account_id
FROM instances i JOIN apps a ON a.id = i.app_id
WHERE i.id = sqlc.arg(instance_id)::uuid AND a.status <> 'deleted';

-- name: ReadOperatorRecoveryApp :one
SELECT id::text AS app_id, account_id::text AS account_id FROM apps
WHERE slug = sqlc.arg(slug)::text AND status <> 'deleted';

-- name: ReadOperatorRecoveryDeployment :one
SELECT id::text AS deployment_id FROM deployments WHERE app_id = sqlc.arg(app_id)::uuid
ORDER BY created_at DESC, id DESC LIMIT 1;

-- name: InsertOperatorRecoveryIntent :one
INSERT INTO operator_intents (id, kind, target_id, account_id, actor_id, reason, trace_id)
VALUES (sqlc.arg(id)::uuid, sqlc.arg(kind)::text, sqlc.arg(target_id)::text, sqlc.arg(account_id)::uuid,
        sqlc.arg(actor_id)::uuid, sqlc.arg(reason)::text, sqlc.narg(trace_id)::text)
RETURNING id;

-- name: EnqueueOperatorRecoveryRequestAudit :one
INSERT INTO audit_event_outbox (actor, kind, subject, data, dedupe_key)
VALUES ('apid', sqlc.arg(kind)::text, sqlc.arg(subject)::uuid, sqlc.arg(data)::jsonb, sqlc.arg(dedupe_key)::text)
RETURNING id;

-- name: InsertOperatorRecoverySubmission :one
INSERT INTO operator_recovery_submissions (actor_id, key_digest, request_digest, intent_id, response_body, audit_outbox_id)
VALUES (sqlc.arg(actor_id)::uuid, sqlc.arg(key_digest)::text, sqlc.arg(request_digest)::text,
        sqlc.arg(intent_id)::uuid, sqlc.arg(response_body)::bytea, sqlc.arg(audit_outbox_id)::bigint)
RETURNING intent_id;
