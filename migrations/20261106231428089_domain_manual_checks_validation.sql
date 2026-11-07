-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE custom_domains VALIDATE CONSTRAINT custom_domains_manual_check_at_check;

-- +goose Down
SELECT 1;
