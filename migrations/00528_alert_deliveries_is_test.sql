-- filename: 00528_alert_deliveries_is_test.sql
-- +goose Up
-- +goose StatementBegin

-- Issue #1233 / ADR-123 PR-D: the `alert_deliveries` ledger gains
-- an `is_test boolean NOT NULL DEFAULT false` column so the
-- operator's "recent deliveries" pane can be filtered to exclude
-- rows written by Dispatcher.DispatchTest.
--
-- Why this is its own column (not a payload discriminator):
--   - The customer-facing `?include_anonymous` precedent at
--     cmd/apid/handlers_audit.go:106-114 settled on closed-set
--     columns over payload discriminator probes (a payload-only
--     filter would force a payload ->> 'test' = 'true' comparison
--     with no index support, defeating the partial-index plan
--     below).
--   - The default-of-false stamps every pre-PR-D row
--     (PG11+ fast-default) so no backfill is required and the
--     column can be NOT NULL on day one.
--   - The partial index lets the production-traffic SELECT
--     (`WHERE rule_id = $1 AND is_test = false ORDER BY fired_at
--     DESC LIMIT $2`) stay index-only even as the test row count
--     grows unbounded (every "send test alert" click writes one).
--
-- Roll-forward plan: the handler's `?include_test=true` toggle
-- (cmd/apid/handlers_alert_rules.go — new) flips the WHERE
-- clause to `is_test = true OR is_test = false` (effectively
-- unconditional), so the operator can reach test rows on demand
-- without an index-only path. The pre-existing
-- `alert_deliveries_rule_fired_idx` on `(rule_id, fired_at DESC)`
-- remains the production hot path.
--
-- Slot risk: Mega-PR #2 (worktree-m2-lifecycle) claims slots
-- 528-531 in a sibling worktree. If that lands first, this
-- migration + the two fences below renumber to 00533+. Standard
-- mega-PR coordination pattern.

ALTER TABLE alert_deliveries
    ADD COLUMN IF NOT EXISTS is_test boolean NOT NULL DEFAULT false;

-- Partial index for the production hot path: ListAlertDeliveriesForRule
-- with include_test=false (the customer-facing default) hits
-- `WHERE rule_id = $1 AND is_test = false ORDER BY fired_at DESC LIMIT $2`.
-- Pre-PR-D this was index-only via alert_deliveries_rule_fired_idx
-- (rule_id, fired_at desc); the new column is appended to the
-- WHERE clause so the partial index must include is_test to keep
-- the predicate sargable.
CREATE INDEX IF NOT EXISTS alert_deliveries_rule_fired_production_idx
    ON alert_deliveries (rule_id, fired_at DESC)
    WHERE is_test = false;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS alert_deliveries_rule_fired_production_idx;
ALTER TABLE alert_deliveries DROP COLUMN IF EXISTS is_test;

-- +goose StatementEnd
