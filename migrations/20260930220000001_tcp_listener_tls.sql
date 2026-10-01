-- Explicit TLS intent for raw TCP listeners (ADR-390).
-- +goose Up
-- +goose StatementBegin
ALTER TABLE app_tcp_listeners
    ADD COLUMN IF NOT EXISTS tls_mode TEXT NOT NULL DEFAULT 'passthrough',
    ADD COLUMN IF NOT EXISTS tls_hostname TEXT NOT NULL DEFAULT '';

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_catalog.pg_constraint
        WHERE conname = 'app_tcp_listeners_tls_mode_chk'
          AND conrelid = 'app_tcp_listeners'::regclass
    ) THEN
        ALTER TABLE app_tcp_listeners
            ADD CONSTRAINT app_tcp_listeners_tls_mode_chk
                CHECK (tls_mode IN ('passthrough', 'terminate'));
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM pg_catalog.pg_constraint
        WHERE conname = 'app_tcp_listeners_tls_hostname_chk'
          AND conrelid = 'app_tcp_listeners'::regclass
    ) THEN
        ALTER TABLE app_tcp_listeners
            ADD CONSTRAINT app_tcp_listeners_tls_hostname_chk CHECK (
                (tls_mode = 'passthrough' AND tls_hostname = '') OR
                (tls_mode = 'terminate' AND char_length(tls_hostname) BETWEEN 1 AND 253
                    AND tls_hostname ~ '^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)+$'
                    AND tls_hostname !~ '^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$')
            );
    END IF;
END$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE app_tcp_listeners
    DROP CONSTRAINT IF EXISTS app_tcp_listeners_tls_hostname_chk,
    DROP CONSTRAINT IF EXISTS app_tcp_listeners_tls_mode_chk,
    DROP COLUMN IF EXISTS tls_hostname,
    DROP COLUMN IF EXISTS tls_mode;
-- +goose StatementEnd
