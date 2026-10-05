-- filename: 20261003175306632_custom_domain_tls_hosts.sql

-- +goose Up
-- +goose StatementBegin
-- ADR-520: hostnames below a wildcard custom domain that the public edge has
-- admitted for on-demand certificate issuance. gatewayd-public is the only
-- writer. Rows go away with their wildcard so a deleted wildcard stops
-- certificate reloads and renewals for every host below it.
CREATE TABLE IF NOT EXISTS custom_domain_tls_hosts (
    host citext PRIMARY KEY,
    wildcard_domain citext NOT NULL REFERENCES custom_domains(domain) ON DELETE CASCADE,
    admitted_at timestamptz NOT NULL,
    CONSTRAINT custom_domain_tls_hosts_wildcard_chk CHECK (wildcard_domain LIKE '*.%'),
    CONSTRAINT custom_domain_tls_hosts_host_chk CHECK (host NOT LIKE '%*%' AND host LIKE '%' || substr(wildcard_domain, 2))
);
CREATE INDEX IF NOT EXISTS custom_domain_tls_hosts_wildcard_admitted_idx
    ON custom_domain_tls_hosts (wildcard_domain, admitted_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS custom_domain_tls_hosts;
-- +goose StatementEnd
