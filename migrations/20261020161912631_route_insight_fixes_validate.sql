-- migration-compat: expand
-- filename: 20261020161912631_route_insight_fixes_validate.sql

-- ADR-20261020161310: validate the route insight fix checks that
-- 20261020161645839 added NOT VALID. In its own transaction VALIDATE takes a
-- lock that lets the evaluator keep writing.

-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE route_insights VALIDATE CONSTRAINT route_insights_fix_action_check;
ALTER TABLE route_insights VALIDATE CONSTRAINT route_insights_fix_pair_check;
ALTER TABLE route_insights VALIDATE CONSTRAINT route_insights_fixed_resolved_check;

-- +goose Down
SELECT 1;
