-- name: InsertEntityOutboxAcceptance :execrows
INSERT INTO entity_outbox_acceptances(message_id, account_id, app_id, fingerprint)
VALUES (sqlc.arg(message_id)::text::uuid, sqlc.arg(account_id)::text::uuid, sqlc.arg(app_id)::text::uuid, sqlc.arg(fingerprint)::text)
ON CONFLICT (message_id) DO NOTHING;

-- name: GetEntityOutboxAcceptanceFingerprint :one
SELECT fingerprint FROM entity_outbox_acceptances
WHERE message_id = sqlc.arg(message_id)::text::uuid;

-- name: InsertEntityOutboxWebhookDelivery :execrows
WITH destination AS MATERIALIZED (
    SELECT w.id FROM app_webhooks w
    JOIN apps a ON a.id = w.app_id AND a.account_id = w.account_id
    JOIN accounts ac ON ac.id = w.account_id
    WHERE w.id = sqlc.arg(webhook_id)::text::uuid AND w.app_id = sqlc.arg(app_id)::text::uuid
      AND w.account_id = sqlc.arg(account_id)::text::uuid AND w.scope = 'app' AND w.enabled
      AND a.status <> 'deleted' AND a.deleted_at IS NULL
      AND a.workload_class NOT IN ('job', 'worker')
      AND coalesce(a.manifest->>'execution_mode', '') NOT IN ('job', 'worker')
      AND ac.status IN ('active', 'past_due') AND ac.plan IN ('hobby', 'pro', 'scale')
      AND ac.deletion_requested_at IS NULL AND ac.abuse_hold_at IS NULL
    FOR SHARE OF w, a, ac
)
INSERT INTO app_webhook_deliveries(id, webhook_id, app_id, account_id, event, payload)
SELECT sqlc.arg(message_id)::text::uuid, id, sqlc.arg(app_id)::text::uuid,
       sqlc.arg(account_id)::text::uuid, sqlc.arg(event)::text, sqlc.arg(payload)::jsonb
FROM destination;
