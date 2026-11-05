-- filename: 20261105141918739_managed_postgres_rejected_resize_outcomes.sql
-- migration-compat: expand

-- Confirmed class, storage and idle-policy rejections can finish on the
-- unchanged source, just as a rejected major upgrade does. N-1 readers
-- already understand failed receipts and skip the ready database.
-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
ALTER TABLE managed_postgres_resizes ADD CONSTRAINT managed_postgres_resize_outcome_check_next
 CHECK ((state='failed')=(error_code IS NOT NULL)
  AND (error_code IS NULL OR error_code ~ '^[a-z_]{1,64}$')) NOT VALID;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
ALTER TABLE managed_postgres_resizes DROP CONSTRAINT IF EXISTS managed_postgres_resize_outcome_check_next;
-- +goose StatementEnd
