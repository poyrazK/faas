-- name: CreateAPISandboxWebhookPartner :execrows
INSERT INTO api_sandbox_webhook_partners (run_id, source_deployment_id, scenario_revision, invite_hash, expires_at)
VALUES ($1, $2, $3, $4, $5) ON CONFLICT (run_id) DO NOTHING;

-- name: GetAPISandboxWebhookPartner :one
SELECT * FROM api_sandbox_webhook_partners WHERE run_id = $1;

-- name: RedeemAPISandboxWebhookPartner :execrows
UPDATE api_sandbox_webhook_partners
SET target_url = sqlc.arg(target_url), signing_secret_sealed = sqlc.arg(signing_secret_sealed),
    outcome_token_hash = sqlc.arg(outcome_token_hash), invite_hash = NULL
WHERE run_id = sqlc.arg(run_id) AND invite_hash = sqlc.arg(invite_hash) AND expires_at > sqlc.arg(at_time);

-- name: ReadyAPISandboxWebhookPartner :execrows
UPDATE api_sandbox_webhook_partners SET ready_at = coalesce(ready_at, sqlc.arg(at_time))
WHERE run_id = sqlc.arg(run_id) AND outcome_token_hash = sqlc.arg(outcome_token_hash) AND expires_at > sqlc.arg(at_time);

-- name: RecordAPISandboxWebhookPartnerOutcome :execrows
WITH waiting AS (
    SELECT id FROM api_sandbox_runs
    WHERE id = sqlc.arg(run_id) AND status = 'queued' AND partner_webhook
      AND partner_outcome_until > sqlc.arg(at_time)
    FOR UPDATE
)
UPDATE api_sandbox_webhook_partners p
SET outcome_count = sqlc.arg(outcome_count), outcome_at = coalesce(p.outcome_at, sqlc.arg(at_time))
FROM waiting WHERE p.run_id = waiting.id AND p.outcome_token_hash = sqlc.arg(outcome_token_hash)
    AND p.expires_at > sqlc.arg(at_time) AND p.ready_at IS NOT NULL
    AND (p.outcome_count IS NULL OR p.outcome_count = sqlc.arg(outcome_count));

-- name: DeleteExpiredAPISandboxWebhookPartners :exec
DELETE FROM api_sandbox_webhook_partners
WHERE run_id IN (
    SELECT p.run_id FROM api_sandbox_webhook_partners p LEFT JOIN api_sandbox_runs r ON r.id = p.run_id
    WHERE p.expires_at <= sqlc.arg(at_time) AND (r.id IS NULL OR r.finished_at IS NOT NULL)
    ORDER BY p.expires_at LIMIT 100
);

-- name: ParkAPISandboxWebhookPartnerRun :execrows
UPDATE api_sandbox_runs
SET status = 'queued', partner_outcome_until = sqlc.arg(outcome_until), lease_token = NULL, lease_until = NULL
WHERE id = sqlc.arg(run_id) AND lease_token = sqlc.arg(lease_token) AND lease_until > sqlc.arg(at_time)
  AND partner_webhook AND partner_outcome_until IS NULL AND sandbox_deployment_id IS NOT NULL;

-- name: ListAPISandboxWebhookPartnerSetups :many
SELECT p.run_id, p.scenario_revision, p.expires_at, p.ready_at,
       (p.target_url <> '')::boolean AS verified
FROM api_sandbox_webhook_partners p
WHERE p.source_deployment_id = sqlc.arg(source_id) AND p.expires_at > sqlc.arg(at_time)
    AND NOT EXISTS (SELECT 1 FROM api_sandbox_runs r WHERE r.id = p.run_id)
ORDER BY p.expires_at DESC, p.run_id DESC LIMIT 25;
