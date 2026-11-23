-- filename: 20261123084446100_consumer_key_mcp_scopes.sql
-- migration-compat: expand

-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
-- ADR-20261122194324: consumer keys may also carry namespaced MCP scopes
-- ("mcp:<scope>"), which grant access only to a consumer-key MCP server. The
-- wider vocabulary is added NOT VALID beside the closed one; the next
-- migration validates it and a contract migration drops the closed one.
CREATE OR REPLACE FUNCTION consumer_key_scopes_valid(scopes text[]) RETURNS boolean
LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
  SELECT cardinality(scopes) > 0 AND NOT EXISTS (
    SELECT 1 FROM unnest(scopes) AS s(scope)
    WHERE NOT (s.scope IN ('read', 'write', 'admin', 'billing')
      OR (char_length(s.scope) <= 128 AND s.scope ~ '^mcp:[!#-\[\]-~]+$')))
$$;
DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'consumer_keys'::regclass AND conname = 'consumer_keys_scopes_vocab_v2_chk') THEN
    ALTER TABLE consumer_keys ADD CONSTRAINT consumer_keys_scopes_vocab_v2_chk
      CHECK (consumer_key_scopes_valid(scopes)) NOT VALID;
  END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
ALTER TABLE consumer_keys DROP CONSTRAINT IF EXISTS consumer_keys_scopes_vocab_v2_chk;
DROP FUNCTION IF EXISTS consumer_key_scopes_valid(text[]);
-- +goose StatementEnd
