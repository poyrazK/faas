-- name: FinancialBudgetEvaluationGet :one
SELECT observation FROM financial_budget_evaluations
WHERE account_id = sqlc.arg(account_id)::uuid AND policy_id = sqlc.arg(policy_id)::uuid
  AND revision = sqlc.arg(revision)::bigint AND period_start = sqlc.arg(period_start)::timestamptz;

-- name: FinancialBudgetEvaluationPut :one
INSERT INTO financial_budget_evaluations(account_id, policy_id, revision, period_start, evaluated_at, evidence_through_id, observation)
VALUES(sqlc.arg(account_id)::uuid, sqlc.arg(policy_id)::uuid, sqlc.arg(revision)::bigint, sqlc.arg(period_start)::timestamptz,
  sqlc.arg(evaluated_at)::timestamptz, sqlc.arg(evidence_through_id)::bigint, sqlc.arg(observation)::jsonb)
ON CONFLICT (account_id, policy_id) DO UPDATE SET revision = EXCLUDED.revision,
  period_start = EXCLUDED.period_start, evaluated_at = EXCLUDED.evaluated_at,
  evidence_through_id = EXCLUDED.evidence_through_id, observation = EXCLUDED.observation
WHERE financial_budget_evaluations.revision != EXCLUDED.revision OR
  (financial_budget_evaluations.evaluated_at <= EXCLUDED.evaluated_at AND
    (financial_budget_evaluations.period_start != EXCLUDED.period_start OR sqlc.arg(unavailable)::boolean OR financial_budget_evaluations.evidence_through_id <= EXCLUDED.evidence_through_id))
RETURNING policy_id;

-- name: FinancialBudgetAlertInsert :exec
INSERT INTO financial_budget_alerts(id, account_id, policy_id, revision, period_start, threshold_millicents, payload, next_attempt_at)
VALUES(sqlc.arg(id)::uuid, sqlc.arg(account_id)::uuid, sqlc.arg(policy_id)::uuid, sqlc.arg(revision)::bigint,
  sqlc.arg(period_start)::timestamptz, sqlc.arg(threshold_millicents)::bigint, sqlc.arg(payload)::jsonb, sqlc.arg(next_attempt_at)::timestamptz)
ON CONFLICT (account_id, policy_id, revision, period_start, threshold_millicents) DO NOTHING;

-- name: FinancialBudgetAlertList :many
SELECT * FROM financial_budget_alerts
WHERE account_id = sqlc.arg(account_id)::uuid AND policy_id = sqlc.arg(policy_id)::uuid
  AND revision = sqlc.arg(revision)::bigint AND period_start = sqlc.arg(period_start)::timestamptz
ORDER BY threshold_millicents LIMIT sqlc.arg(page_size)::integer;

-- name: FinancialBudgetAlertClaim :one
WITH candidate AS (
  SELECT a.id FROM financial_budget_alerts a
  JOIN financial_budget_policies p ON p.account_id = a.account_id AND p.id = a.policy_id AND p.revision = a.revision
  WHERE a.state != 'delivered' AND a.next_attempt_at <= sqlc.arg(now_at)::timestamptz
    AND p.deleted_at IS NULL AND p.spec->>'enabled' = 'true'
    AND p.spec->>'mode' = 'monitored' AND p.spec->>'action' IN ('notify', 'stop_previews')
  ORDER BY a.next_attempt_at, a.id FOR UPDATE OF a SKIP LOCKED LIMIT 1
)
UPDATE financial_budget_alerts a SET state = 'sending', attempts = LEAST(a.attempts::bigint + 1, 2147483647)::integer,
  lease_token = sqlc.arg(lease_token)::uuid, next_attempt_at = sqlc.arg(lease_until)::timestamptz
FROM candidate WHERE a.id = candidate.id RETURNING a.*;

-- name: FinancialBudgetAlertSettle :one
UPDATE financial_budget_alerts SET state = sqlc.arg(state)::text, lease_token = NULL,
  delivered_at = sqlc.narg(delivered_at)::timestamptz, last_error = sqlc.arg(last_error)::text,
  next_attempt_at = sqlc.arg(next_attempt_at)::timestamptz
WHERE id = sqlc.arg(id)::uuid AND state = 'sending' AND lease_token = sqlc.arg(lease_token)::uuid
  AND next_attempt_at > sqlc.arg(now_at)::timestamptz
RETURNING id;

-- name: FinancialBudgetAlertDeliveryGet :one
SELECT * FROM financial_budget_alerts
WHERE id = sqlc.arg(id)::uuid AND lease_token = sqlc.arg(lease_token)::uuid AND state = 'sending';

-- ADR-20261019193500: budget -> notification channel bindings and one
-- durable delivery per alert and channel.

-- name: FinancialBudgetChannelsDelete :exec
DELETE FROM financial_budget_channels
WHERE account_id = sqlc.arg(account_id)::uuid AND policy_id = sqlc.arg(policy_id)::uuid;

-- name: FinancialBudgetChannelsInsert :exec
-- Only channels owned by the policy's account are bound.
INSERT INTO financial_budget_channels(account_id, policy_id, channel_id)
SELECT p.account_id, p.id, c.id FROM financial_budget_policies p
JOIN notification_channels c ON c.account_id = p.account_id AND c.id = ANY(sqlc.arg(channel_ids)::uuid[])
WHERE p.account_id = sqlc.arg(account_id)::uuid AND p.id = sqlc.arg(policy_id)::uuid AND p.deleted_at IS NULL
ON CONFLICT DO NOTHING;

-- name: FinancialBudgetChannelsList :many
SELECT c.* FROM financial_budget_channels b
JOIN notification_channels c ON c.id = b.channel_id AND c.account_id = b.account_id
WHERE b.account_id = sqlc.arg(account_id)::uuid AND b.policy_id = sqlc.arg(policy_id)::uuid
ORDER BY c.name, c.id;

-- name: FinancialBudgetAlertChannelsEnqueue :exec
-- A channel bound after an alert was recorded still receives it once if the
-- current month's evaluation crosses that threshold again.
INSERT INTO financial_budget_alert_channel_deliveries(alert_id, kind, channel_id, account_id, policy_id, next_attempt_at)
SELECT a.id, 'threshold', b.channel_id, a.account_id, a.policy_id, sqlc.arg(now_at)::timestamptz
FROM financial_budget_alerts a
JOIN financial_budget_channels b ON b.account_id = a.account_id AND b.policy_id = a.policy_id
WHERE a.id = sqlc.arg(alert_id)::uuid
ON CONFLICT (alert_id, channel_id) DO NOTHING;

-- name: FinancialBudgetProjectionChannelsEnqueue :exec
INSERT INTO financial_budget_alert_channel_deliveries(alert_id, kind, channel_id, account_id, policy_id, next_attempt_at)
SELECT a.id, 'projection', b.channel_id, a.account_id, a.policy_id, sqlc.arg(now_at)::timestamptz
FROM financial_budget_projection_alerts a
JOIN financial_budget_channels b ON b.account_id = a.account_id AND b.policy_id = a.policy_id
WHERE a.id = sqlc.arg(alert_id)::uuid
ON CONFLICT (alert_id, channel_id) DO NOTHING;

-- name: FinancialBudgetAlertChannelClaim :one
WITH candidate AS (
  SELECT d.alert_id, d.channel_id FROM financial_budget_alert_channel_deliveries d
  LEFT JOIN financial_budget_alerts a ON d.kind = 'threshold' AND a.id = d.alert_id
  LEFT JOIN financial_budget_projection_alerts pa ON d.kind = 'projection' AND pa.id = d.alert_id
  JOIN financial_budget_policies p ON p.account_id = d.account_id AND p.id = d.policy_id
    AND p.revision = COALESCE(a.revision, pa.revision)
  JOIN notification_channels c ON c.id = d.channel_id AND c.account_id = d.account_id
  -- Unbinding a channel holds its pending deliveries; rebinding resumes them.
  JOIN financial_budget_channels b ON b.account_id = d.account_id AND b.policy_id = d.policy_id AND b.channel_id = d.channel_id
  WHERE d.state IN ('pending', 'sending', 'retrying') AND d.next_attempt_at <= sqlc.arg(now_at)::timestamptz
    AND p.deleted_at IS NULL AND p.spec->>'enabled' = 'true'
    AND p.spec->>'mode' = 'monitored' AND p.spec->>'action' IN ('notify', 'stop_previews')
  ORDER BY d.next_attempt_at, d.alert_id, d.channel_id FOR UPDATE OF d SKIP LOCKED LIMIT 1
)
UPDATE financial_budget_alert_channel_deliveries d SET state = 'sending',
  attempts = LEAST(d.attempts::bigint + 1, 2147483647)::integer,
  lease_token = sqlc.arg(lease_token)::uuid, next_attempt_at = sqlc.arg(lease_until)::timestamptz
FROM candidate WHERE d.alert_id = candidate.alert_id AND d.channel_id = candidate.channel_id
RETURNING d.*;

-- name: FinancialBudgetAlertChannelGet :one
SELECT * FROM financial_budget_alert_channel_deliveries
WHERE alert_id = sqlc.arg(alert_id)::uuid AND channel_id = sqlc.arg(channel_id)::uuid;

-- name: FinancialBudgetAlertGetByID :one
SELECT * FROM financial_budget_alerts WHERE id = sqlc.arg(id)::uuid;

-- name: FinancialBudgetProjectionGetByID :one
SELECT * FROM financial_budget_projection_alerts WHERE id = sqlc.arg(id)::uuid;

-- name: FinancialBudgetProjectionInsert :exec
INSERT INTO financial_budget_projection_alerts(id, account_id, policy_id, revision, period_start, limit_millicents, payload, next_attempt_at)
VALUES(sqlc.arg(id)::uuid, sqlc.arg(account_id)::uuid, sqlc.arg(policy_id)::uuid, sqlc.arg(revision)::bigint,
  sqlc.arg(period_start)::timestamptz, sqlc.arg(limit_millicents)::bigint, sqlc.arg(payload)::jsonb, sqlc.arg(next_attempt_at)::timestamptz)
ON CONFLICT (account_id, policy_id, revision, period_start) DO NOTHING;

-- name: FinancialBudgetProjectionGet :one
SELECT * FROM financial_budget_projection_alerts
WHERE account_id = sqlc.arg(account_id)::uuid AND policy_id = sqlc.arg(policy_id)::uuid
  AND revision = sqlc.arg(revision)::bigint AND period_start = sqlc.arg(period_start)::timestamptz;

-- name: FinancialBudgetProjectionClaim :one
WITH candidate AS (
  SELECT a.id FROM financial_budget_projection_alerts a
  JOIN financial_budget_policies p ON p.account_id = a.account_id AND p.id = a.policy_id AND p.revision = a.revision
  WHERE a.state != 'delivered' AND a.next_attempt_at <= sqlc.arg(now_at)::timestamptz
    AND p.deleted_at IS NULL AND p.spec->>'enabled' = 'true'
    AND p.spec->>'mode' = 'monitored' AND p.spec->>'action' IN ('notify', 'stop_previews')
  ORDER BY a.next_attempt_at, a.id FOR UPDATE OF a SKIP LOCKED LIMIT 1
)
UPDATE financial_budget_projection_alerts a SET state = 'sending', attempts = LEAST(a.attempts::bigint + 1, 2147483647)::integer,
  lease_token = sqlc.arg(lease_token)::uuid, next_attempt_at = sqlc.arg(lease_until)::timestamptz
FROM candidate WHERE a.id = candidate.id RETURNING a.*;

-- name: FinancialBudgetProjectionDeliveryGet :one
SELECT * FROM financial_budget_projection_alerts
WHERE id = sqlc.arg(id)::uuid AND lease_token = sqlc.arg(lease_token)::uuid AND state = 'sending';

-- name: FinancialBudgetProjectionSettle :one
UPDATE financial_budget_projection_alerts SET state = sqlc.arg(state)::text, lease_token = NULL,
  delivered_at = sqlc.narg(delivered_at)::timestamptz, last_error = sqlc.arg(last_error)::text,
  next_attempt_at = sqlc.arg(next_attempt_at)::timestamptz
WHERE id = sqlc.arg(id)::uuid AND state = 'sending' AND lease_token = sqlc.arg(lease_token)::uuid
  AND next_attempt_at > sqlc.arg(now_at)::timestamptz
RETURNING id;

-- name: FinancialBudgetAlertChannelSettle :one
UPDATE financial_budget_alert_channel_deliveries SET state = sqlc.arg(state)::text, lease_token = NULL,
  delivered_at = sqlc.narg(delivered_at)::timestamptz, last_error = sqlc.arg(last_error)::text,
  next_attempt_at = sqlc.arg(next_attempt_at)::timestamptz
WHERE alert_id = sqlc.arg(alert_id)::uuid AND channel_id = sqlc.arg(channel_id)::uuid
  AND state = 'sending' AND lease_token = sqlc.arg(lease_token)::uuid
  AND next_attempt_at > sqlc.arg(now_at)::timestamptz
RETURNING alert_id;
