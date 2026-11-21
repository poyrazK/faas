-- filename: 20261117075028100_sandbox_sessions_browser.sql
-- migration-compat: expand

-- +goose Up
-- The column locks sandbox_sessions briefly; a constant default rewrites no
-- rows.
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
-- ADR-20261116182615 §1: a session created with browser: true boots with
-- the sandbox browser running and restores the browser shape's warm
-- snapshot. Defaults to false, so older binaries keep working.
ALTER TABLE sandbox_sessions
    ADD COLUMN IF NOT EXISTS browser boolean NOT NULL DEFAULT false;
COMMENT ON COLUMN sandbox_sessions.browser IS
    'ADR-20261116182615: the session boots with the sandbox browser running (gregale-browser) and CDP on port 9222.';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE sandbox_sessions DROP COLUMN IF EXISTS browser;
-- +goose StatementEnd
