-- filename: 20261017140827519_outbound_ai_tag_alerts.sql
-- ADR-1438: AI gateway phase 16. A tag with a daily cost limit alerts at
-- 80% and 100% like the other budgets: alert rows gain the tag, the budget
-- check gains tag_daily_cost, and the once-per-period index includes the
-- tag. Replay-safe: the check and the index are dropped and created again.

-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
ALTER TABLE outbound_ai_budget_alerts ADD COLUMN IF NOT EXISTS tag text;

ALTER TABLE outbound_ai_budget_alerts DROP CONSTRAINT IF EXISTS outbound_ai_budget_alerts_budget_chk;
ALTER TABLE outbound_ai_budget_alerts ADD CONSTRAINT outbound_ai_budget_alerts_budget_chk
    CHECK (budget IN ('daily_tokens', 'app_daily_tokens', 'daily_cost', 'monthly_cost', 'user_daily_tokens', 'user_daily_cost',
                      'tag_daily_cost'));

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_catalog.pg_constraint
                   WHERE conname = 'outbound_ai_budget_alerts_tag_chk' AND conrelid = 'outbound_ai_budget_alerts'::regclass) THEN
        ALTER TABLE outbound_ai_budget_alerts ADD CONSTRAINT outbound_ai_budget_alerts_tag_chk
            CHECK ((budget = 'tag_daily_cost') = (tag IS NOT NULL)
                   AND (tag IS NULL OR tag ~ '^[a-z][a-z0-9_.-]{0,31}=[A-Za-z0-9._:@+/-]{1,64}$'));
    END IF;
END
$$;

-- migration-lint: allow index-build outbound_ai_budget_alerts: one row per budget threshold crossed per period, and no prod
-- account has the AI gateway release gate on, so the table is empty there and the build is instant.
DROP INDEX IF EXISTS outbound_ai_budget_alerts_once_idx;
CREATE UNIQUE INDEX outbound_ai_budget_alerts_once_idx ON outbound_ai_budget_alerts
    (integration_id, budget, COALESCE(app_id, '00000000-0000-0000-0000-000000000000'::uuid), COALESCE(end_user, ''),
     COALESCE(tag, ''), period_start, threshold_percent);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM outbound_ai_budget_alerts WHERE budget = 'tag_daily_cost';
DROP INDEX IF EXISTS outbound_ai_budget_alerts_once_idx;
CREATE UNIQUE INDEX outbound_ai_budget_alerts_once_idx ON outbound_ai_budget_alerts
    (integration_id, budget, COALESCE(app_id, '00000000-0000-0000-0000-000000000000'::uuid), COALESCE(end_user, ''),
     period_start, threshold_percent);
ALTER TABLE outbound_ai_budget_alerts DROP CONSTRAINT IF EXISTS outbound_ai_budget_alerts_tag_chk;
ALTER TABLE outbound_ai_budget_alerts DROP CONSTRAINT IF EXISTS outbound_ai_budget_alerts_budget_chk;
ALTER TABLE outbound_ai_budget_alerts ADD CONSTRAINT outbound_ai_budget_alerts_budget_chk
    CHECK (budget IN ('daily_tokens', 'app_daily_tokens', 'daily_cost', 'monthly_cost', 'user_daily_tokens', 'user_daily_cost'));
ALTER TABLE outbound_ai_budget_alerts DROP COLUMN IF EXISTS tag;
-- +goose StatementEnd
