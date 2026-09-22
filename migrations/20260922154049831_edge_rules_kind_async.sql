-- +goose Up
-- +goose StatementBegin
-- ADR-211 — kind=async converts a matched public request into the existing
-- durable async invocation lifecycle. Runtime and plan policy live above the
-- database; this CHECK only widens the closed edge-rule vocabulary.
ALTER TABLE edge_rules DROP CONSTRAINT IF EXISTS edge_rules_kind_check;
ALTER TABLE edge_rules ADD CONSTRAINT edge_rules_kind_check
  CHECK (kind IN ('route', 'rewrite', 'redirect', 'headers',
                  'cors', 'jwt', 'ip', 'validate', 'limit', 'geo',
                  'maintenance', 'throttle', 'budget', 'cache', 'respond',
                  'retry', 'circuit_breaker', 'async'));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM edge_rules WHERE kind = 'async';

ALTER TABLE edge_rules DROP CONSTRAINT IF EXISTS edge_rules_kind_check;
ALTER TABLE edge_rules ADD CONSTRAINT edge_rules_kind_check
  CHECK (kind IN ('route', 'rewrite', 'redirect', 'headers',
                  'cors', 'jwt', 'ip', 'validate', 'limit', 'geo',
                  'maintenance', 'throttle', 'budget', 'cache', 'respond',
                  'retry', 'circuit_breaker'));
-- +goose StatementEnd
