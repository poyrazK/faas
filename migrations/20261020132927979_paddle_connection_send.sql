-- filename: 20261020132927979_paddle_connection_send.sql

-- ADR-20261020120000: a Paddle billing connection may use collection send,
-- which bills a statement's transaction when the Paddle customer has an
-- address.
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
UPDATE account_billing_connections SET collection = 'none' WHERE provider = 'paddle' AND collection = 'send';
ALTER TABLE account_billing_connections DROP CONSTRAINT IF EXISTS account_billing_connections_collection_chk;
ALTER TABLE account_billing_connections ADD CONSTRAINT account_billing_connections_collection_chk CHECK (
  (provider = 'stripe' AND collection IN ('none', 'send', 'charge'))
  OR (provider = 'paddle' AND collection = 'none')
  OR (provider = 'lago' AND collection IN ('none', 'charge')));
-- +goose StatementEnd
