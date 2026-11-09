-- filename: 20261109202124000_sandbox_templates.sql
-- migration-compat: expand

-- +goose Up
-- The apps column and the new table's foreign keys lock apps and accounts
-- briefly.
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
-- ADR-20261027090013 sandbox templates: a template is a Dockerfile built in a
-- builder microVM through a platform-managed app that only builds
-- (execution_mode job, never woken). apps.sandbox_template marks those apps:
-- they count against SandboxTemplatesMax instead of DeployedApps and stay out
-- of the customer's app listings. A constant default, so the column add does
-- not rewrite apps and older binaries keep writing false.
ALTER TABLE apps ADD COLUMN IF NOT EXISTS sandbox_template boolean NOT NULL DEFAULT false;

-- One row per named template. apid is the only writer; the build state is the
-- app's deployments.
CREATE TABLE IF NOT EXISTS sandbox_templates (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id      uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    name            text NOT NULL CHECK (name ~ '^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$'),
    app_id          uuid NOT NULL UNIQUE REFERENCES apps(id) ON DELETE CASCADE,
    -- e2b.toml's start command, run in the background at session boot, and
    -- the ready command polled until it exits 0 before the session is ready.
    start_cmd       text CHECK (start_cmd IS NULL OR char_length(start_cmd) BETWEEN 1 AND 4096),
    ready_cmd       text CHECK (ready_cmd IS NULL OR char_length(ready_cmd) BETWEEN 1 AND 4096),
    ready_timeout_s integer NOT NULL DEFAULT 60 CHECK (ready_timeout_s BETWEEN 1 AND 600),
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (account_id, name)
);
COMMENT ON TABLE sandbox_templates IS
    'ADR-20261027090013: named sandbox templates; each owns one apps row with sandbox_template = true that builds its image.';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS sandbox_templates;
ALTER TABLE apps DROP COLUMN IF EXISTS sandbox_template;
-- +goose StatementEnd
