-- filename: 20261123084446200_consumer_key_mcp_scopes_validate.sql
-- migration-compat: expand

-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
-- ADR-20261122194324: every existing key already satisfies the closed
-- vocabulary, a subset of the new one. Validation does not block writes.
ALTER TABLE consumer_keys VALIDATE CONSTRAINT consumer_keys_scopes_vocab_v2_chk;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Validation has nothing to undo.
SELECT 1;
-- +goose StatementEnd
