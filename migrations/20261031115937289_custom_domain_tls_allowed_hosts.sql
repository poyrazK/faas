-- filename: 20261031115937289_custom_domain_tls_allowed_hosts.sql
-- migration-compat: expand

-- +goose Up
-- +goose StatementBegin
-- ADR-520 amendment 2 (bug hunt 32 M7): an optional allowlist of the names
-- below a wildcard custom domain that may hold an on-demand certificate.
-- name is the part of the host before the wildcard's suffix (`app` for
-- app.example.com under *.example.com, `eu.shop` for eu.shop.example.com).
-- A wildcard with no rows admits every name, as before; with rows, only the
-- listed names. apid is the only writer (customer intent); gatewayd-public
-- reads it before admitting a host. Rows go with their wildcard.
CREATE TABLE IF NOT EXISTS custom_domain_tls_allowed_hosts (
    wildcard_domain citext NOT NULL REFERENCES custom_domains(domain) ON DELETE CASCADE,
    name citext NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (wildcard_domain, name),
    CONSTRAINT custom_domain_tls_allowed_hosts_wildcard_chk CHECK (wildcard_domain LIKE '*.%'),
    CONSTRAINT custom_domain_tls_allowed_hosts_name_chk CHECK (name ~ '^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)*$')
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS custom_domain_tls_allowed_hosts;
-- +goose StatementEnd
