-- filename: 20261031084812032_custom_domain_tls_admissions.sql

-- +goose Up
-- +goose StatementBegin
-- ADR-520 amendment 1 (bug hunt 32 M7): every new hostname the public edge
-- admitted below a wildcard custom domain for on-demand certificate issuance,
-- kept for the longest admission window whether or not the wildcard still
-- exists. custom_domain_tls_hosts rows cascade away with their wildcard, so
-- counting only them let a tenant reset its budgets by deleting and
-- re-creating a wildcard. The per-registered-domain, per-owner and fleet caps
-- count these rows. gatewayd-public is the only writer and deletes rows older
-- than 7 days. owner_id is the wildcard app's organization, else its account,
-- at admission time.
CREATE TABLE IF NOT EXISTS custom_domain_tls_admissions (
    host citext NOT NULL,
    registered_domain citext NOT NULL,
    owner_id uuid NOT NULL,
    admitted_at timestamptz NOT NULL,
    PRIMARY KEY (host, admitted_at),
    CONSTRAINT custom_domain_tls_admissions_host_chk CHECK (host NOT LIKE '%*%' AND host LIKE '%.' || registered_domain)
);
CREATE INDEX IF NOT EXISTS custom_domain_tls_admissions_admitted_idx
    ON custom_domain_tls_admissions (admitted_at);
CREATE INDEX IF NOT EXISTS custom_domain_tls_admissions_registered_idx
    ON custom_domain_tls_admissions (registered_domain, admitted_at);
CREATE INDEX IF NOT EXISTS custom_domain_tls_admissions_owner_idx
    ON custom_domain_tls_admissions (owner_id, admitted_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS custom_domain_tls_admissions;
-- +goose StatementEnd
