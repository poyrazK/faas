-- filename: 20261021085211109_edge_rule_events_challenge_outcomes.sql
-- migration-compat: expand
--
-- ADR-831 amendment 7 follow-up: kind=challenge outcomes are ADR-964
-- security events. New outcomes: challenged (challenge page served),
-- rejected (non-browser request answered 403), solved (pass issued) and
-- failed (solution refused). Gateways that do not record them yet keep
-- writing rows the wider CHECK accepts.
--
-- The CHECK is replaced online and keeps its name: the wider list is added
-- NOT VALID under a temporary name and validated, the old one dropped, then
-- the same list is added back under the old name, validated, and the
-- temporary one dropped. Each step is its own transaction; a VALIDATE takes
-- SHARE UPDATE EXCLUSIVE, so gateways keep flushing events during the scans,
-- and a CHECK always holds. Every step is re-runnable
-- (TestNewMigrationsAreReplaySafe).
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';

-- +goose StatementBegin
DO $challenge_events$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'edge_rule_events'::regclass AND conname = 'edge_rule_events_outcome_check_v2') THEN
    ALTER TABLE edge_rule_events ADD CONSTRAINT edge_rule_events_outcome_check_v2
      CHECK (outcome IN ('matched', 'logged', 'detected', 'warned', 'blocked',
                         'challenged', 'rejected', 'solved', 'failed')) NOT VALID;
  END IF;
END
$challenge_events$;
-- +goose StatementEnd

ALTER TABLE edge_rule_events VALIDATE CONSTRAINT edge_rule_events_outcome_check_v2;

ALTER TABLE edge_rule_events DROP CONSTRAINT IF EXISTS edge_rule_events_outcome_check;

-- +goose StatementBegin
DO $challenge_events$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'edge_rule_events'::regclass AND conname = 'edge_rule_events_outcome_check') THEN
    ALTER TABLE edge_rule_events ADD CONSTRAINT edge_rule_events_outcome_check
      CHECK (outcome IN ('matched', 'logged', 'detected', 'warned', 'blocked',
                         'challenged', 'rejected', 'solved', 'failed')) NOT VALID;
  END IF;
END
$challenge_events$;
-- +goose StatementEnd

ALTER TABLE edge_rule_events VALIDATE CONSTRAINT edge_rule_events_outcome_check;

ALTER TABLE edge_rule_events DROP CONSTRAINT IF EXISTS edge_rule_events_outcome_check_v2;

RESET lock_timeout;

-- +goose Down
SET lock_timeout = '5s';

DELETE FROM edge_rule_events WHERE outcome IN ('challenged', 'rejected', 'solved', 'failed');

-- +goose StatementBegin
DO $challenge_events$
BEGIN
  ALTER TABLE edge_rule_events DROP CONSTRAINT IF EXISTS edge_rule_events_outcome_check_v2;
  ALTER TABLE edge_rule_events DROP CONSTRAINT IF EXISTS edge_rule_events_outcome_check;
  ALTER TABLE edge_rule_events ADD CONSTRAINT edge_rule_events_outcome_check
    CHECK (outcome IN ('matched', 'logged', 'detected', 'warned', 'blocked'));
END
$challenge_events$;
-- +goose StatementEnd

RESET lock_timeout;
