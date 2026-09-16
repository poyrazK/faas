-- filename: 20260915192829195_execution_events.sql

-- +goose Up
-- +goose StatementBegin
-- Resumable control-plane event log for disposable executions.
-- This stores bounded lifecycle/output events only; it is not guest storage.
CREATE TABLE IF NOT EXISTS execution_events (
    id bigserial PRIMARY KEY,
    execution_id uuid NOT NULL REFERENCES executions(id) ON DELETE CASCADE,
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    event_type text NOT NULL,
    payload jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT execution_events_type_check CHECK (event_type IN ('status', 'stdout', 'stderr', 'terminal')),
    CONSTRAINT execution_events_payload_check CHECK (octet_length(payload::text) BETWEEN 2 AND 65536)
);

CREATE INDEX IF NOT EXISTS execution_events_execution_id_idx
    ON execution_events (execution_id, id);
CREATE INDEX IF NOT EXISTS execution_events_account_id_idx
    ON execution_events (account_id, id);
-- Keep replay bounded. Terminal rows remain available for the normal
-- execution retention window; this index supports the janitor follow-up.
CREATE INDEX IF NOT EXISTS execution_events_created_at_idx
    ON execution_events (created_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Write the rollback here.
-- +goose StatementEnd
