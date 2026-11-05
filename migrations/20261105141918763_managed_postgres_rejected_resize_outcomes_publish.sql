-- filename: 20261105141918763_managed_postgres_rejected_resize_outcomes_publish.sql
-- migration-compat: contract

-- The replacement was added NOT VALID and validated in earlier transactions.
-- Dropping/renaming checks uses the full-release data step. N-1 readers
-- already understand failed receipts; per-unit releases retain the old check.
-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
ALTER TABLE managed_postgres_resizes DROP CONSTRAINT managed_postgres_resize_outcome_check;
ALTER TABLE managed_postgres_resizes RENAME CONSTRAINT managed_postgres_resize_outcome_check_next TO managed_postgres_resize_outcome_check;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM managed_postgres_resizes WHERE state='failed' AND target_postgres_major IS NULL) THEN
  RAISE EXCEPTION 'retain rejected resize history before rollback'
   USING ERRCODE='23514', CONSTRAINT='managed_postgres_resize_conflict';
 END IF;
END $$;
ALTER TABLE managed_postgres_resizes RENAME CONSTRAINT managed_postgres_resize_outcome_check TO managed_postgres_resize_outcome_check_next;
ALTER TABLE managed_postgres_resizes ADD CONSTRAINT managed_postgres_resize_outcome_check
 CHECK ((state='failed')=(error_code IS NOT NULL)
  AND (error_code IS NULL OR error_code ~ '^[a-z_]{1,64}$')
  AND (state<>'failed' OR target_postgres_major IS NOT NULL)) NOT VALID;
-- +goose StatementEnd
