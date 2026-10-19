-- filename: 20261013215208298_consumer_key_billing_scope.sql

-- ADR-1304: a consumer key may carry the `billing` scope, which lets the
-- app's customer read its own usage, spend and invoices. The gateway's
-- method check ignores it, so a billing-only key cannot call the app.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE consumer_keys DROP CONSTRAINT IF EXISTS consumer_keys_scopes_vocab_chk;
ALTER TABLE consumer_keys ADD CONSTRAINT consumer_keys_scopes_vocab_chk
  CHECK (scopes <@ ARRAY['read', 'write', 'admin', 'billing']::text[] AND cardinality(scopes) > 0);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE consumer_keys DROP CONSTRAINT IF EXISTS consumer_keys_scopes_vocab_chk;
ALTER TABLE consumer_keys ADD CONSTRAINT consumer_keys_scopes_vocab_chk
  CHECK (scopes <@ ARRAY['read', 'write', 'admin']::text[] AND cardinality(scopes) > 0);
-- +goose StatementEnd
