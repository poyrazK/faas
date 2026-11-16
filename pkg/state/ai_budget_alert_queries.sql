-- name: AIBudgetAlertsSince :many
-- ADR 20261027132936 amendment 2: the AI budget threshold crossings (80% and
-- 100%) at or after since of the integrations bound to an app, newest first,
-- for the ai_budget_alerts alert metric. Another app's own budget is left
-- out. total counts every match, not only the rows returned.
SELECT a.id::text AS id, i.name AS integration_name, a.budget,
       a.threshold_percent::int AS threshold_percent, a.limit_value, a.used_value,
       COALESCE(a.end_user, '')::text AS end_user, COALESCE(a.tag, '')::text AS tag,
       a.crossed_at, count(*) OVER ()::bigint AS total
FROM outbound_ai_budget_alerts a
JOIN outbound_app_bindings b ON b.integration_id = a.integration_id AND b.account_id = a.account_id
JOIN outbound_integrations i ON i.id = a.integration_id AND i.account_id = a.account_id
WHERE b.app_id = sqlc.arg(app_id)::text::uuid
  AND a.account_id = sqlc.arg(account_id)::text::uuid
  AND a.crossed_at >= sqlc.arg(since)
  AND (a.app_id IS NULL OR a.app_id = b.app_id)
ORDER BY a.crossed_at DESC, a.id DESC
LIMIT sqlc.arg(row_limit)::int;
