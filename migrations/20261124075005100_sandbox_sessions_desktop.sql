-- filename: 20261124075005100_sandbox_sessions_desktop.sql
-- migration-compat: expand
-- ADR-20261124075005 sandbox desktops. Both columns are nullable with no
-- default, so no row is rewritten and older binaries keep working. Each
-- CHECK is added NOT VALID and validated after, so the table is locked only
-- for the catalog change. Every step is re-runnable.
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';
-- §2: a session created with preset desktop records its screen; agentd
-- starts the desktop at that resolution. §3a: the platform desktop drive a
-- desktop boots, instead of a per-account image-run job's copy.
ALTER TABLE sandbox_sessions
    ADD COLUMN IF NOT EXISTS desktop_resolution text,
    ADD COLUMN IF NOT EXISTS desktop_image_key text;
-- +goose StatementBegin
DO $migration$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'sandbox_sessions_desktop_resolution_check' AND conrelid = 'sandbox_sessions'::regclass) THEN
        ALTER TABLE sandbox_sessions ADD CONSTRAINT sandbox_sessions_desktop_resolution_check
            CHECK (desktop_resolution IS NULL OR desktop_resolution ~ '^[1-9][0-9]{2,3}x[1-9][0-9]{2,3}$') NOT VALID;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'sandbox_sessions_desktop_image_key_check' AND conrelid = 'sandbox_sessions'::regclass) THEN
        ALTER TABLE sandbox_sessions ADD CONSTRAINT sandbox_sessions_desktop_image_key_check
            CHECK (desktop_image_key IS NULL OR (desktop_resolution IS NOT NULL
                AND desktop_image_key ~ '^base/sandbox-desktop-[0-9a-f]{64}\.ext4$')) NOT VALID;
    END IF;
END
$migration$;
-- +goose StatementEnd
ALTER TABLE sandbox_sessions VALIDATE CONSTRAINT sandbox_sessions_desktop_resolution_check;
ALTER TABLE sandbox_sessions VALIDATE CONSTRAINT sandbox_sessions_desktop_image_key_check;
COMMENT ON COLUMN sandbox_sessions.desktop_resolution IS
    'ADR-20261124075005: a desktop session''s screen as WIDTHxHEIGHT; agentd starts gregale-desktop at it. NULL: not a desktop.';
COMMENT ON COLUMN sandbox_sessions.desktop_image_key IS
    'ADR-20261124075005 §3a: the platform desktop drive (base/sandbox-desktop-<digest>.ext4) a desktop session boots. NULL: not a desktop.';

-- +goose Down
SET lock_timeout = '5s';
ALTER TABLE sandbox_sessions DROP CONSTRAINT IF EXISTS sandbox_sessions_desktop_image_key_check;
ALTER TABLE sandbox_sessions DROP CONSTRAINT IF EXISTS sandbox_sessions_desktop_resolution_check;
ALTER TABLE sandbox_sessions
    DROP COLUMN IF EXISTS desktop_image_key,
    DROP COLUMN IF EXISTS desktop_resolution;
