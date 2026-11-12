-- +goose Up
-- ADR-20261112080601: what reverses each agent write. apid writes a record
-- next to the agent's activity row and marks it undone when a person (or an
-- approved agent) undoes it. Expand-only: release N-1 apid writes no records.
SET LOCAL lock_timeout = '5s';

CREATE TABLE IF NOT EXISTS agent_undo_records (
    id                  uuid PRIMARY KEY,
    activity_id         uuid NOT NULL,
    key_id              uuid NOT NULL REFERENCES api_keys(id) ON DELETE CASCADE,
    account_id          uuid NOT NULL,
    org_id              uuid,
    method              text NOT NULL CHECK (method IN ('POST', 'PUT', 'PATCH', 'DELETE')),
    path                text NOT NULL CHECK (char_length(path) BETWEEN 1 AND 512),
    kind                text NOT NULL CHECK (kind IN ('settings', 'deploy', 'create', 'delete', 'other')),
    summary             text NOT NULL CHECK (char_length(summary) BETWEEN 1 AND 512),
    inverse             jsonb,
    guard               jsonb,
    not_undoable_reason text CHECK (not_undoable_reason IS NULL OR char_length(not_undoable_reason) BETWEEN 1 AND 512),
    created_at          timestamptz NOT NULL DEFAULT now(),
    undone_at           timestamptz,
    undone_by           uuid,
    CONSTRAINT agent_undo_records_undoable_check CHECK ((inverse IS NULL) = (not_undoable_reason IS NOT NULL)),
    CONSTRAINT agent_undo_records_undone_check CHECK ((undone_at IS NULL) = (undone_by IS NULL)),
    CONSTRAINT agent_undo_records_undone_inverse_check CHECK (undone_at IS NULL OR inverse IS NOT NULL)
);

CREATE INDEX IF NOT EXISTS agent_undo_records_key_created_idx
    ON agent_undo_records (key_id, created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS agent_undo_records_created_idx
    ON agent_undo_records (created_at);

-- +goose Down
DROP TABLE IF EXISTS agent_undo_records;
