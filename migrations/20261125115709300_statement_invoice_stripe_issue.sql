-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE usage_statement_invoice_exports ADD COLUMN stripe_issue jsonb;

-- +goose Down
SET LOCAL lock_timeout = '5s';
ALTER TABLE usage_statement_invoice_exports DROP COLUMN stripe_issue;
