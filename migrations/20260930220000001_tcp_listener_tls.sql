-- Explicit TLS intent for raw TCP listeners (ADR-393).
-- +goose Up
-- +goose StatementBegin
ALTER TABLE app_tcp_listeners
    ADD COLUMN tls_mode TEXT NOT NULL DEFAULT 'passthrough',
    ADD COLUMN tls_hostname TEXT NOT NULL DEFAULT '',
    ADD CONSTRAINT app_tcp_listeners_tls_mode_chk CHECK (tls_mode IN ('passthrough', 'terminate')),
    ADD CONSTRAINT app_tcp_listeners_tls_hostname_chk CHECK (
        (tls_mode = 'passthrough' AND tls_hostname = '') OR
        (tls_mode = 'terminate' AND char_length(tls_hostname) BETWEEN 1 AND 253
            AND tls_hostname ~ '^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)+$'
            AND tls_hostname !~ '^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$')
    );
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE app_tcp_listeners DROP CONSTRAINT app_tcp_listeners_tls_hostname_chk,
    DROP CONSTRAINT app_tcp_listeners_tls_mode_chk, DROP COLUMN tls_hostname, DROP COLUMN tls_mode;
-- +goose StatementEnd
