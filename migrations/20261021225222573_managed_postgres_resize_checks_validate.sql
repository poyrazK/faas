-- filename: 20261021225222573_managed_postgres_resize_checks_validate.sql
-- migration-compat: contract

-- ADR-1027: the second step of the resize journal's check changes
-- (20261021225216283 storage targets, 20261021225221946 major upgrades).
-- It validates the checks those added NOT VALID without blocking writes,
-- drops the narrow state and completion checks, and gives the wider ones
-- their names. Every step is skipped once done, so a replay keeps the
-- checks. Dropping and renaming checks is a full release's to apply.
-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
ALTER TABLE managed_postgres_resizes VALIDATE CONSTRAINT managed_postgres_storage_resize_target_check;
ALTER TABLE managed_postgres_resizes VALIDATE CONSTRAINT managed_postgres_resize_outcome_check;
ALTER TABLE managed_postgres_resizes VALIDATE CONSTRAINT managed_postgres_major_upgrade_target_check;
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'managed_postgres_resizes'::regclass AND conname = 'managed_postgres_resizes_state_check_next') THEN
  ALTER TABLE managed_postgres_resizes VALIDATE CONSTRAINT managed_postgres_resizes_state_check_next;
  ALTER TABLE managed_postgres_resizes DROP CONSTRAINT IF EXISTS managed_postgres_resizes_state_check;
  ALTER TABLE managed_postgres_resizes RENAME CONSTRAINT managed_postgres_resizes_state_check_next TO managed_postgres_resizes_state_check;
 END IF;
 IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'managed_postgres_resizes'::regclass AND conname = 'managed_postgres_resizes_check_next') THEN
  ALTER TABLE managed_postgres_resizes VALIDATE CONSTRAINT managed_postgres_resizes_check_next;
  ALTER TABLE managed_postgres_resizes DROP CONSTRAINT IF EXISTS managed_postgres_resizes_check;
  ALTER TABLE managed_postgres_resizes RENAME CONSTRAINT managed_postgres_resizes_check_next TO managed_postgres_resizes_check;
 END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM managed_postgres_resizes WHERE state = 'failed') THEN
  RAISE EXCEPTION 'retain failed major upgrades before rollback' USING ERRCODE = '23514', CONSTRAINT = 'managed_postgres_resize_conflict';
 END IF;
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'managed_postgres_resizes'::regclass AND conname = 'managed_postgres_resizes_state_check_next') THEN
  ALTER TABLE managed_postgres_resizes RENAME CONSTRAINT managed_postgres_resizes_state_check TO managed_postgres_resizes_state_check_next;
  ALTER TABLE managed_postgres_resizes ADD CONSTRAINT managed_postgres_resizes_state_check CHECK (state IN ('pending', 'succeeded'));
 END IF;
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'managed_postgres_resizes'::regclass AND conname = 'managed_postgres_resizes_check_next') THEN
  ALTER TABLE managed_postgres_resizes RENAME CONSTRAINT managed_postgres_resizes_check TO managed_postgres_resizes_check_next;
  ALTER TABLE managed_postgres_resizes ADD CONSTRAINT managed_postgres_resizes_check CHECK ((state = 'pending' AND completed_at IS NULL)
   OR (state = 'succeeded' AND completed_at IS NOT NULL AND completed_at >= created_at));
 END IF;
END $$;
-- +goose StatementEnd
