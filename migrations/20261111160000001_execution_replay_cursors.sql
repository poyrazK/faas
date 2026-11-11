-- filename: 20261111160000001_execution_replay_cursors.sql
-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
CREATE TABLE execution_event_replay_cursors (
    execution_id uuid PRIMARY KEY REFERENCES executions(id) ON DELETE CASCADE,
    pruned_through bigint NOT NULL CHECK (pruned_through > 0),
    updated_at timestamptz NOT NULL CHECK (isfinite(updated_at))
);
-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'faas_runtime') THEN
        GRANT SELECT, INSERT, UPDATE, DELETE ON execution_event_replay_cursors TO faas_runtime;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TABLE execution_event_replay_cursors;
