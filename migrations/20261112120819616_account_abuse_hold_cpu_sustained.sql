-- migration-compat: expand
-- filename: 20261112120819616_account_abuse_hold_cpu_sustained.sql
--
-- ADR-20261112120603: a Free instance recycled for sustained CPU abuse counts
-- toward the ADR-361 account abuse hold, recorded with its own reason
-- 'cpu_sustained'. The hold CHECK is widened in three steps, each its own
-- transaction: the wider constraint is added NOT VALID under a new name,
-- validated (every existing row passes), and then the narrower one is
-- dropped. The previous release writes only reasons both constraints allow,
-- and never writes 'cpu_sustained'. Every step is re-runnable.
-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
SET lock_timeout = '5s';
DO $widen$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint
    WHERE conrelid = 'accounts'::regclass
      AND conname = 'accounts_abuse_hold_v2_valid'
  ) THEN
    ALTER TABLE accounts
      ADD CONSTRAINT accounts_abuse_hold_v2_valid
      CHECK (
        (abuse_hold_at IS NULL) = (abuse_hold_reason IS NULL)
        AND (abuse_hold_reason IS NULL OR abuse_hold_reason IN ('egress_fanout', 'egress_flood', 'cpu_sustained', 'operator'))
      ) NOT VALID;
  END IF;
END
$widen$;
-- +goose StatementEnd
-- +goose StatementBegin
SET lock_timeout = '5s';
ALTER TABLE accounts VALIDATE CONSTRAINT accounts_abuse_hold_v2_valid;
-- +goose StatementEnd
-- +goose StatementBegin
SET lock_timeout = '5s';
ALTER TABLE accounts DROP CONSTRAINT IF EXISTS accounts_abuse_hold_valid;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET lock_timeout = '5s';
UPDATE accounts SET abuse_hold_reason = 'operator' WHERE abuse_hold_reason = 'cpu_sustained';
-- +goose StatementEnd
-- +goose StatementBegin
SET lock_timeout = '5s';
DO $narrow$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint
    WHERE conrelid = 'accounts'::regclass
      AND conname = 'accounts_abuse_hold_valid'
  ) THEN
    ALTER TABLE accounts
      ADD CONSTRAINT accounts_abuse_hold_valid
      CHECK (
        (abuse_hold_at IS NULL) = (abuse_hold_reason IS NULL)
        AND (abuse_hold_reason IS NULL OR abuse_hold_reason IN ('egress_fanout', 'egress_flood', 'operator'))
      ) NOT VALID;
  END IF;
END
$narrow$;
-- +goose StatementEnd
-- +goose StatementBegin
SET lock_timeout = '5s';
ALTER TABLE accounts DROP CONSTRAINT IF EXISTS accounts_abuse_hold_v2_valid;
-- +goose StatementEnd
