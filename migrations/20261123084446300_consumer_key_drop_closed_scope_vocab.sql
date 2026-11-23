-- filename: 20261123084446300_consumer_key_drop_closed_scope_vocab.sql
-- migration-compat: contract

-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
-- ADR-20261122194324: consumer_keys_scopes_vocab_v2_chk now enforces the
-- vocabulary, including "mcp:<scope>". The closed check refused those.
ALTER TABLE consumer_keys DROP CONSTRAINT IF EXISTS consumer_keys_scopes_vocab_chk;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
-- Rows written with MCP scopes would violate the closed check, so it returns
-- NOT VALID: new writes are checked, existing rows are kept.
ALTER TABLE consumer_keys ADD CONSTRAINT consumer_keys_scopes_vocab_chk
  CHECK ((scopes <@ ARRAY['read'::text, 'write'::text, 'admin'::text, 'billing'::text]) AND (cardinality(scopes) > 0)) NOT VALID;
-- +goose StatementEnd
