-- name: FinancialBudgetStopInsert :exec
INSERT INTO financial_budget_stops(id, account_id, policy_id, revision, period_start, period_end, requested_at, drain_deadline, resume_rule, payload)
SELECT sqlc.arg(id)::uuid, sqlc.arg(account_id)::uuid, sqlc.arg(policy_id)::uuid, sqlc.arg(revision)::bigint,
  sqlc.arg(period_start)::timestamptz, sqlc.arg(period_end)::timestamptz, sqlc.arg(requested_at)::timestamptz,
  sqlc.arg(drain_deadline)::timestamptz, sqlc.arg(resume_rule)::text, sqlc.arg(payload)::jsonb
WHERE sqlc.arg(resume_rule)::text != 'manual' OR NOT EXISTS (
  SELECT 1 FROM financial_budget_stops WHERE account_id = sqlc.arg(account_id)::uuid
    AND policy_id = sqlc.arg(policy_id)::uuid AND revision = sqlc.arg(revision)::bigint
)
ON CONFLICT (account_id, policy_id, revision, period_start) DO NOTHING;

-- name: FinancialBudgetStopActive :many
SELECT s.*, p.spec FROM financial_budget_stops s
JOIN financial_budget_policies p ON p.account_id = s.account_id AND p.id = s.policy_id AND p.revision = s.revision
WHERE s.account_id = sqlc.arg(account_id)::uuid AND p.deleted_at IS NULL
  AND p.spec->>'enabled' = 'true' AND p.spec->>'mode' = 'monitored' AND p.spec->>'action' = 'stop_previews'
  AND s.requested_at <= sqlc.arg(now_at)::timestamptz
  AND (s.resume_rule = 'manual' OR s.period_end > sqlc.arg(now_at)::timestamptz)
ORDER BY s.policy_id, s.period_start LIMIT sqlc.arg(page_size)::integer;

-- name: FinancialBudgetStopTargetList :many
SELECT t.* FROM financial_budget_stop_targets t
JOIN financial_budget_stops s ON s.id = t.stop_id
WHERE t.stop_id = sqlc.arg(stop_id)::uuid AND s.account_id = sqlc.arg(account_id)::uuid
ORDER BY t.app_id, t.environment_id LIMIT sqlc.arg(page_size)::integer;

-- name: FinancialBudgetStopTargetPut :execrows
INSERT INTO financial_budget_stop_targets(stop_id, app_id, environment_id, state, observed_at, resident_count, last_error)
SELECT s.id, a.id, sqlc.arg(environment_id)::text, sqlc.arg(state)::text, sqlc.arg(observed_at)::timestamptz,
  sqlc.arg(resident_count)::integer, sqlc.arg(last_error)::text
FROM financial_budget_stops s
JOIN financial_budget_policies p ON p.id = s.policy_id AND p.account_id = s.account_id AND p.revision = s.revision
JOIN apps a ON a.id = sqlc.arg(app_id)::uuid AND a.account_id = s.account_id
WHERE s.id = sqlc.arg(stop_id)::uuid AND s.account_id = sqlc.arg(account_id)::uuid
  AND p.deleted_at IS NULL AND p.spec->>'enabled' = 'true'
  AND a.preview_of_slug IS NOT NULL AND a.preview_of_slug != ''
  AND s.requested_at <= sqlc.arg(observed_at)::timestamptz
  AND (s.resume_rule = 'manual' OR s.period_end > sqlc.arg(observed_at)::timestamptz)
  AND CASE p.spec->'scope'->>'kind'
    WHEN 'account' THEN true
    WHEN 'app' THEN (p.spec->'scope'->>'id')::uuid = a.id
    WHEN 'project' THEN (p.spec->'scope'->>'id')::uuid = a.project_id
    WHEN 'environment' THEN (p.spec->'scope'->>'id')::uuid = nullif(sqlc.arg(environment_id)::text, '')::uuid
      AND EXISTS(SELECT 1 FROM project_environments e WHERE e.id=(p.spec->'scope'->>'id')::uuid AND e.account_id=a.account_id AND e.project_id=a.project_id)
    ELSE false END
ON CONFLICT (stop_id, app_id, environment_id) DO UPDATE SET state = EXCLUDED.state,
  observed_at = EXCLUDED.observed_at, resident_count = EXCLUDED.resident_count, last_error = EXCLUDED.last_error
WHERE financial_budget_stop_targets.observed_at <= EXCLUDED.observed_at;
