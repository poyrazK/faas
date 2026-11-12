-- migration-compat: expand
-- filename: 20261111150000312_managed_postgres_plan_excess_delete_at.sql
--
-- ADR-20261110184700 amendment: a database past its plan is deleted only on
-- the date its queued suspension email named. The mail outbox writes that date
-- in the same transaction that links the email to the notice, so a database
-- whose owner was never told a date is never deleted. The previous release's
-- statements do not name the column, so its rows keep NULL. The CHECK is
-- added NOT VALID: new writes are checked at once. Every step is re-runnable.

-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
ALTER TABLE managed_postgres_plan_excess_notices
    ADD COLUMN IF NOT EXISTS delete_at timestamptz;
-- +goose StatementEnd
-- +goose StatementBegin
DO $check$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint
    WHERE conrelid = 'managed_postgres_plan_excess_notices'::regclass
      AND conname = 'managed_postgres_plan_excess_notices_delete_at_check'
  ) THEN
    ALTER TABLE managed_postgres_plan_excess_notices
      ADD CONSTRAINT managed_postgres_plan_excess_notices_delete_at_check
      CHECK (delete_at IS NULL OR (isfinite(delete_at) AND mail_intent_id IS NOT NULL)) NOT VALID;
  END IF;
END
$check$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
ALTER TABLE managed_postgres_plan_excess_notices
    DROP CONSTRAINT IF EXISTS managed_postgres_plan_excess_notices_delete_at_check;
ALTER TABLE managed_postgres_plan_excess_notices
    DROP COLUMN IF EXISTS delete_at;
-- +goose StatementEnd
