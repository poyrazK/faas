-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
-- ADR-20261115082509: review approval never authorizes Task execution.
CREATE TABLE IF NOT EXISTS task_tool_release_intents (
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    id text NOT NULL CHECK (id ~ '^[0-9a-f]{64}$'),
    status text NOT NULL CHECK (status IN ('pending','approved','rejected','revoked','expired','stale')),
    payload jsonb NOT NULL CHECK (jsonb_typeof(payload) = 'object' AND octet_length(payload::text) <= 131072),
    PRIMARY KEY (account_id,id),
    CHECK ((payload->>'id'=id AND payload->>'account_id'=account_id::text AND payload->>'status'=status) IS TRUE),
    CHECK (payload @> '{"ready_to_apply":false,"bindings_verified":false}'::jsonb)
);

-- +goose Down
DROP TABLE task_tool_release_intents;
