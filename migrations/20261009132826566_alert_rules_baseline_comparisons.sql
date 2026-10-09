-- filename: 20261009132826566_alert_rules_baseline_comparisons.sql

-- +goose Up
-- +goose StatementBegin
-- ADR-744: baseline comparisons, where threshold is a multiplier of the app's
-- usual value. apid restricts them to app-scoped request-metric rules.
ALTER TABLE alert_rules DROP CONSTRAINT IF EXISTS alert_rules_comparison_chk;
ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_comparison_chk
  CHECK (comparison = ANY (ARRAY['gt'::text, 'gte'::text, 'lt'::text, 'lte'::text, 'above_baseline'::text, 'below_baseline'::text]));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Baseline rules have no absolute-threshold equivalent; rolling back
-- removes them rather than reinterpreting their multipliers.
DELETE FROM alert_rules WHERE comparison IN ('above_baseline', 'below_baseline');
ALTER TABLE alert_rules DROP CONSTRAINT IF EXISTS alert_rules_comparison_chk;
ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_comparison_chk
  CHECK (comparison = ANY (ARRAY['gt'::text, 'gte'::text, 'lt'::text, 'lte'::text]));
-- +goose StatementEnd
