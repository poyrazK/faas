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
