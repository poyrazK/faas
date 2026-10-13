-- filename: 20261012101044312_outbound_ai_user_alerts.sql
-- ADR-995: per-end-user budget alerts. An end user's UTC-day token and cost
-- limits (ADR-994) alert at 80% and 100% like the other AI budgets; the
-- alert names the end user, and the once-per-period key gains it. Replay-safe.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE outbound_ai_budget_alerts
    ADD COLUMN IF NOT EXISTS end_user text;

ALTER TABLE outbound_ai_budget_alerts
    DROP CONSTRAINT IF EXISTS outbound_ai_budget_alerts_budget_chk;
ALTER TABLE outbound_ai_budget_alerts
    ADD CONSTRAINT outbound_ai_budget_alerts_budget_chk
        CHECK (budget IN ('daily_tokens', 'app_daily_tokens', 'daily_cost', 'monthly_cost',
                          'user_daily_tokens', 'user_daily_cost'));

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_catalog.pg_constraint
                   WHERE conname = 'outbound_ai_budget_alerts_end_user_chk' AND conrelid = 'outbound_ai_budget_alerts'::regclass) THEN
        ALTER TABLE outbound_ai_budget_alerts ADD CONSTRAINT outbound_ai_budget_alerts_end_user_chk
            CHECK ((budget IN ('user_daily_tokens', 'user_daily_cost')) = (end_user IS NOT NULL)
                   AND (end_user IS NULL OR end_user ~ '^[A-Za-z0-9._:@+=/|-]{1,64}$'));
    END IF;
END$$;

-- One alert per budget, app or end user, period and threshold.
DROP INDEX IF EXISTS outbound_ai_budget_alerts_once_idx;
CREATE UNIQUE INDEX outbound_ai_budget_alerts_once_idx
    ON outbound_ai_budget_alerts (integration_id, budget, COALESCE(app_id, '00000000-0000-0000-0000-000000000000'::uuid),
                                  COALESCE(end_user, ''), period_start, threshold_percent);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM outbound_ai_budget_alerts WHERE end_user IS NOT NULL;
DROP INDEX IF EXISTS outbound_ai_budget_alerts_once_idx;
CREATE UNIQUE INDEX outbound_ai_budget_alerts_once_idx
    ON outbound_ai_budget_alerts (integration_id, budget, COALESCE(app_id, '00000000-0000-0000-0000-000000000000'::uuid),
                                  period_start, threshold_percent);
ALTER TABLE outbound_ai_budget_alerts
    DROP CONSTRAINT IF EXISTS outbound_ai_budget_alerts_end_user_chk,
    DROP CONSTRAINT IF EXISTS outbound_ai_budget_alerts_budget_chk,
    DROP COLUMN IF EXISTS end_user;
ALTER TABLE outbound_ai_budget_alerts
    ADD CONSTRAINT outbound_ai_budget_alerts_budget_chk
        CHECK (budget IN ('daily_tokens', 'app_daily_tokens', 'daily_cost', 'monthly_cost'));
-- +goose StatementEnd
