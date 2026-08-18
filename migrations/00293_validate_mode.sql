-- filename: 00293_validate_mode.sql
-- +goose Up
-- +goose StatementBegin

-- Edge rule kind=validate mode enum (issue #975 item #3 / new
-- Mega-Foundation #979-a). The validate-kind rule has shipped as
-- block-only since 00214; the audit identified that operators
-- asking "which endpoints receive invalid payloads?" cannot answer
-- without breaking their customers. This migration adds a
-- validate_mode column so the gateway can opt into three behaviors:
--
--   observe — count mismatch, never reject.
--   warn    — count mismatch, stamp X-Validation-Warning: <rule_id>,
--             never reject.
--   block   — count mismatch, reject with 422 (existing behavior).
--
-- Forward-compat: NOT NULL DEFAULT 'block' means existing rules
-- created before this migration land unchanged. The default is the
-- strictest mode, so no behavior changes for any pre-existing rule.
--
-- Replay-safe: ADD COLUMN IF NOT EXISTS. The column is a metadata
-- string (no rewrite), so a second MigrateUp is a no-op and the
-- replay_safety_test harness stays green. The CHECK is added in a
-- pinned constraint name so the Down block can DROP it without
-- needing its auto-generated name. (PG 15 doesn't have
-- ADD CONSTRAINT IF NOT EXISTS.)

ALTER TABLE edge_rules
  ADD COLUMN IF NOT EXISTS validate_mode TEXT NOT NULL DEFAULT 'block';

ALTER TABLE edge_rules
  DROP CONSTRAINT IF EXISTS edge_rules_validate_mode_check;

ALTER TABLE edge_rules
  ADD CONSTRAINT edge_rules_validate_mode_check
  CHECK (validate_mode IN ('observe', 'warn', 'block'));

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- Reverse the widening. As with 00214, the kind=validate rules
-- created between this migration's apply and a downgrade become
-- violators of the schema default and force the downgrade to fail
-- with 23514. The DROP below goes first so the constraint is gone
-- before the column is dropped — the column drop itself is the
-- preimage restoration.

ALTER TABLE edge_rules
  DROP CONSTRAINT IF EXISTS edge_rules_validate_mode_check;

ALTER TABLE edge_rules
  DROP COLUMN IF EXISTS validate_mode;

-- +goose StatementEnd
