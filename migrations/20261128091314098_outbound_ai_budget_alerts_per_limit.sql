-- filename: 20261128091314098_outbound_ai_budget_alerts_per_limit.sql
-- AI gateway budget alerts fire once per budget, period and threshold *per
-- limit*. The once-per-period index did not include the limit, so raising a
-- cap during the day silenced its 80% and 100% alerts for the rest of the
-- period (prod hunt 2026-11-28: a USD 0.33 cap overshot to USD 2.72 with no
-- alert, because 0.20's 100% alert had been recorded). Replay-safe: the
-- index is dropped and created again.

-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
-- migration-lint: allow index-build outbound_ai_budget_alerts: one row per budget threshold crossed per period, kept 30 days;
-- production holds a handful of rows (one integration on one account), so the build is instant.
DROP INDEX IF EXISTS outbound_ai_budget_alerts_once_idx;
CREATE UNIQUE INDEX outbound_ai_budget_alerts_once_idx ON outbound_ai_budget_alerts
    (integration_id, budget, COALESCE(app_id, '00000000-0000-0000-0000-000000000000'::uuid), COALESCE(end_user, ''),
     COALESCE(tag, ''), period_start, threshold_percent, limit_value);
-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
-- Keep the newest alert of each budget threshold so the narrower index fits.
DELETE FROM outbound_ai_budget_alerts alert
 USING outbound_ai_budget_alerts newer
 WHERE alert.integration_id = newer.integration_id AND alert.budget = newer.budget
   AND alert.app_id IS NOT DISTINCT FROM newer.app_id AND alert.end_user IS NOT DISTINCT FROM newer.end_user
   AND alert.tag IS NOT DISTINCT FROM newer.tag AND alert.period_start = newer.period_start
   AND alert.threshold_percent = newer.threshold_percent
   AND (alert.crossed_at, alert.id) < (newer.crossed_at, newer.id);
-- migration-lint: allow index-build outbound_ai_budget_alerts: rollback of the index above.
DROP INDEX IF EXISTS outbound_ai_budget_alerts_once_idx;
CREATE UNIQUE INDEX outbound_ai_budget_alerts_once_idx ON outbound_ai_budget_alerts
    (integration_id, budget, COALESCE(app_id, '00000000-0000-0000-0000-000000000000'::uuid), COALESCE(end_user, ''),
     COALESCE(tag, ''), period_start, threshold_percent);
-- +goose StatementEnd
