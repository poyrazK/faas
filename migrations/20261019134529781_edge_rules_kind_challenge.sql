-- filename: 20261019134529781_edge_rules_kind_challenge.sql
-- migration-compat: expand
--
-- ADR-831 amendment 7: kind=challenge asks browsers on matched routes to
-- solve a proof of work before the request reaches the app. Runtime and plan
-- policy live above the database; this only widens the closed edge-rule
-- vocabulary, so every existing row still satisfies it.
--
-- The CHECK is replaced online and keeps its name (migration tests and later
-- kind migrations look it up as edge_rules_kind_check): the wider list is
-- added NOT VALID under a temporary name and validated, the old one dropped,
-- then the same list is added back under the old name, validated, and the
-- temporary one dropped. Each step is its own transaction; a VALIDATE takes
-- SHARE UPDATE EXCLUSIVE, so edge_rules keeps taking writes during the scans,
-- and a CHECK always holds. Every step is re-runnable
-- (TestNewMigrationsAreReplaySafe).
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';

-- +goose StatementBegin
DO $challenge$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'edge_rules'::regclass AND conname = 'edge_rules_kind_check_v2') THEN
    ALTER TABLE edge_rules ADD CONSTRAINT edge_rules_kind_check_v2
      CHECK (kind IN ('route', 'rewrite', 'redirect', 'headers',
                      'cors', 'jwt', 'ip', 'validate', 'limit', 'geo',
                      'maintenance', 'throttle', 'budget', 'cache', 'respond',
                      'retry', 'circuit_breaker', 'async', 'waf', 'challenge')) NOT VALID;
  END IF;
END
$challenge$;
-- +goose StatementEnd

ALTER TABLE edge_rules VALIDATE CONSTRAINT edge_rules_kind_check_v2;

ALTER TABLE edge_rules DROP CONSTRAINT IF EXISTS edge_rules_kind_check;

-- +goose StatementBegin
DO $challenge$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'edge_rules'::regclass AND conname = 'edge_rules_kind_check') THEN
    ALTER TABLE edge_rules ADD CONSTRAINT edge_rules_kind_check
      CHECK (kind IN ('route', 'rewrite', 'redirect', 'headers',
                      'cors', 'jwt', 'ip', 'validate', 'limit', 'geo',
                      'maintenance', 'throttle', 'budget', 'cache', 'respond',
                      'retry', 'circuit_breaker', 'async', 'waf', 'challenge')) NOT VALID;
  END IF;
END
$challenge$;
-- +goose StatementEnd

ALTER TABLE edge_rules VALIDATE CONSTRAINT edge_rules_kind_check;

ALTER TABLE edge_rules DROP CONSTRAINT IF EXISTS edge_rules_kind_check_v2;

RESET lock_timeout;

-- +goose Down
SET lock_timeout = '5s';

DELETE FROM edge_rules WHERE kind = 'challenge';

-- +goose StatementBegin
DO $challenge$
BEGIN
  ALTER TABLE edge_rules DROP CONSTRAINT IF EXISTS edge_rules_kind_check_v2;
  ALTER TABLE edge_rules DROP CONSTRAINT IF EXISTS edge_rules_kind_check;
  ALTER TABLE edge_rules ADD CONSTRAINT edge_rules_kind_check
    CHECK (kind IN ('route', 'rewrite', 'redirect', 'headers',
                    'cors', 'jwt', 'ip', 'validate', 'limit', 'geo',
                    'maintenance', 'throttle', 'budget', 'cache', 'respond',
                    'retry', 'circuit_breaker', 'async', 'waf'));
END
$challenge$;
-- +goose StatementEnd

RESET lock_timeout;
