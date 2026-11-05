-- filename: 20261104225604001_managed_postgres_rejected_resize_outcomes_validate.sql
-- migration-compat: expand

-- Validate separately from the constraint replacement's exclusive lock.
-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
ALTER TABLE managed_postgres_resizes VALIDATE CONSTRAINT managed_postgres_resize_outcome_check_next;
-- +goose StatementEnd

-- +goose Down
SELECT 1;
