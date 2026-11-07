-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE accounts VALIDATE CONSTRAINT accounts_display_name_check;

-- +goose Down
SELECT 1;
