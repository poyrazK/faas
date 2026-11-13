-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
-- ADR-20261112210001: apid alone writes private publication evidence.
-- Failed and abandoned attempts remain tombstones; no automatic replay.
CREATE TABLE tool_publications (
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    id text NOT NULL CHECK (id ~ '^[0-9a-f]{64}$'),
    status text NOT NULL CHECK (status IN ('running','completed','failed','outcome_unknown')),
    payload jsonb NOT NULL CHECK (jsonb_typeof(payload) = 'object' AND octet_length(payload::text) <= 786432),
    PRIMARY KEY (account_id, id),
    CHECK (payload->>'id' = id AND payload->>'account_id' = account_id::text AND payload->>'status' = status)
);
CREATE INDEX tool_publications_registry_idx ON tool_publications (account_id, id) WHERE status = 'completed';

-- +goose Down
DROP TABLE tool_publications;
