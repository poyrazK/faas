-- filename: 20261019103813603_paddle_send_collection.sql

-- ADR-20261019100333: a Paddle connection can send exported invoices; Paddle
-- still cannot charge one to a saved payment method.

-- migration-compat: expand

-- +goose Up
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
ALTER TABLE account_billing_connections DROP CONSTRAINT IF EXISTS account_billing_connections_collection_chk;
ALTER TABLE account_billing_connections ADD CONSTRAINT account_billing_connections_collection_chk CHECK (
  (provider = 'stripe' AND collection IN ('none', 'send', 'charge'))
  OR (provider = 'paddle' AND collection IN ('none', 'send'))
  OR (provider = 'lago' AND collection IN ('none', 'charge')));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE account_billing_connections DROP CONSTRAINT IF EXISTS account_billing_connections_collection_chk;
ALTER TABLE account_billing_connections ADD CONSTRAINT account_billing_connections_collection_chk CHECK (
  (provider = 'stripe' AND collection IN ('none', 'send', 'charge'))
  OR (provider = 'paddle' AND collection = 'none')
  OR (provider = 'lago' AND collection IN ('none', 'charge')));
-- +goose StatementEnd
