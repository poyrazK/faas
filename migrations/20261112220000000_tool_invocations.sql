-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
-- ADR-20261112220000: apid owns single-dispatch tool invocation receipts.
CREATE TABLE tool_invocations (
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    id uuid NOT NULL CHECK (id <> '00000000-0000-0000-0000-000000000000'),
    status text NOT NULL CHECK (status IN ('queued','running','completed','tool_error','not_started','cancelled','outcome_unknown')),
    deadline timestamptz NOT NULL CHECK (isfinite(deadline)),
    payload jsonb NOT NULL CHECK (jsonb_typeof(payload)='object' AND octet_length(payload::text)<=196608),
    PRIMARY KEY (account_id,id),
    CHECK (payload->>'id'=id::text AND payload->>'account_id'=account_id::text AND payload->>'status'=status)
);
CREATE INDEX tool_invocations_active_idx ON tool_invocations (account_id,deadline) WHERE status IN ('queued','running');

-- +goose Down
DROP TABLE tool_invocations;
