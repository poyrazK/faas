-- filename: 20261015085640049_edge_rules_kind_challenge.sql
--
-- ADR-831 amendment 7: kind=challenge asks browsers on matched routes to
-- solve a proof of work before the request reaches the app. Runtime and plan
-- policy live above the database; this CHECK only widens the closed
-- edge-rule vocabulary. Every statement is re-runnable
-- (TestNewMigrationsAreReplaySafe).

-- +goose Up
-- +goose StatementBegin
ALTER TABLE edge_rules DROP CONSTRAINT IF EXISTS edge_rules_kind_check;
ALTER TABLE edge_rules ADD CONSTRAINT edge_rules_kind_check
  CHECK (kind IN ('route', 'rewrite', 'redirect', 'headers',
                  'cors', 'jwt', 'ip', 'validate', 'limit', 'geo',
                  'maintenance', 'throttle', 'budget', 'cache', 'respond',
                  'retry', 'circuit_breaker', 'async', 'waf', 'challenge'));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM edge_rules WHERE kind = 'challenge';

ALTER TABLE edge_rules DROP CONSTRAINT IF EXISTS edge_rules_kind_check;
ALTER TABLE edge_rules ADD CONSTRAINT edge_rules_kind_check
  CHECK (kind IN ('route', 'rewrite', 'redirect', 'headers',
                  'cors', 'jwt', 'ip', 'validate', 'limit', 'geo',
                  'maintenance', 'throttle', 'budget', 'cache', 'respond',
                  'retry', 'circuit_breaker', 'async', 'waf'));
-- +goose StatementEnd
