-- filename: 20261124075005100_sandbox_sessions_desktop.sql
-- migration-compat: expand

-- +goose Up
-- The column locks sandbox_sessions briefly; it is nullable with no default,
-- so no row is rewritten, and the check reads a few thousand NULLs.
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
-- ADR-20261124075005 §2: a session created with preset desktop records its
-- screen; agentd starts the desktop at that resolution. NULL for every
-- other session, so older binaries keep working.
ALTER TABLE sandbox_sessions
    ADD COLUMN IF NOT EXISTS desktop_resolution text
        CONSTRAINT sandbox_sessions_desktop_resolution_check
        CHECK (desktop_resolution IS NULL OR desktop_resolution ~ '^[1-9][0-9]{2,3}x[1-9][0-9]{2,3}$');
COMMENT ON COLUMN sandbox_sessions.desktop_resolution IS
    'ADR-20261124075005: a desktop session''s screen as WIDTHxHEIGHT; agentd starts gregale-desktop at it. NULL: not a desktop.';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE sandbox_sessions DROP COLUMN IF EXISTS desktop_resolution;
-- +goose StatementEnd
