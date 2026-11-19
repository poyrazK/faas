-- filename: 20261119101645200_sandbox_sessions_placed_node.sql
-- migration-compat: expand
-- ADR-20261117190000 slice 3: a compute node reads a sandbox session's image
-- and snapshot through grants secretd signs only for sessions placed on it.
-- node_id is recorded when the VM is up (it routes reads to the session);
-- placed_node_id is recorded when schedd places the session, before the node
-- boots it, and is what secretd checks.

-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE sandbox_sessions ADD COLUMN IF NOT EXISTS placed_node_id text;
-- +goose StatementBegin
DO $migration$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'sandbox_sessions_placed_node_id_check' AND conrelid = 'sandbox_sessions'::regclass) THEN
        ALTER TABLE sandbox_sessions ADD CONSTRAINT sandbox_sessions_placed_node_id_check
            CHECK (placed_node_id IS NULL OR (char_length(placed_node_id) >= 1 AND char_length(placed_node_id) <= 128)) NOT VALID;
    END IF;
END
$migration$;
-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';
ALTER TABLE sandbox_sessions DROP CONSTRAINT IF EXISTS sandbox_sessions_placed_node_id_check;
ALTER TABLE sandbox_sessions DROP COLUMN IF EXISTS placed_node_id;
