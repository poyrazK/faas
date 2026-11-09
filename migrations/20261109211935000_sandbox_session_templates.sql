-- filename: 20261109211935000_sandbox_session_templates.sql
-- migration-compat: expand

-- +goose Up
-- The column and its foreign key lock sandbox_sessions and sandbox_templates
-- briefly.
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
-- ADR-20261027090013 template sessions: a session created from a template
-- records it (image_ref is template:<name>); agentd boots the template's
-- newest finished build, and fails the session as image_unavailable when the
-- template is gone before it starts. Nullable, so older binaries keep working.
ALTER TABLE sandbox_sessions
    ADD COLUMN IF NOT EXISTS template_id uuid REFERENCES sandbox_templates(id) ON DELETE SET NULL;
COMMENT ON COLUMN sandbox_sessions.template_id IS
    'ADR-20261027090013: the sandbox template the session boots; NULL for a preset or image session, or once the template is deleted.';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE sandbox_sessions DROP COLUMN IF EXISTS template_id;
-- +goose StatementEnd
