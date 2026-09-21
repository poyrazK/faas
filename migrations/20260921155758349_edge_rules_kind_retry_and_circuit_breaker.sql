-- +goose Up
-- +goose StatementBegin
-- ADR-201 §1 / §2 — widen the closed edge-rule kind vocabulary with the two
-- traffic-resilience primitives. Both rules only TUNE behaviour that the
-- gateway performs by default; the gateway enforces plan entitlement and the
-- per-kind quotas, so this migration carries no policy of its own.
ALTER TABLE edge_rules DROP CONSTRAINT IF EXISTS edge_rules_kind_check;
ALTER TABLE edge_rules ADD CONSTRAINT edge_rules_kind_check
  CHECK (kind IN ('route', 'rewrite', 'redirect', 'headers',
                  'cors', 'jwt', 'ip', 'validate', 'limit', 'geo',
                  'maintenance', 'throttle', 'budget', 'cache', 'respond',
                  'retry', 'circuit_breaker'));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Down must delete rows of the retired kinds first: the CHECK is re-added
-- narrower, and an existing kind='retry' row would make the constraint
-- un-addable and fail the migration mid-transaction.
DELETE FROM edge_rules WHERE kind IN ('retry', 'circuit_breaker');

ALTER TABLE edge_rules DROP CONSTRAINT IF EXISTS edge_rules_kind_check;
ALTER TABLE edge_rules ADD CONSTRAINT edge_rules_kind_check
  CHECK (kind IN ('route', 'rewrite', 'redirect', 'headers',
                  'cors', 'jwt', 'ip', 'validate', 'limit', 'geo',
                  'maintenance', 'throttle', 'budget', 'cache', 'respond'));
-- +goose StatementEnd
