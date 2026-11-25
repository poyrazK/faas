-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE account_billing_connections VALIDATE CONSTRAINT account_billing_connections_stripe_account_chk;

-- +goose Down
-- The check still applies to new writes. The original migration drops it with the column.
SELECT 1;
