-- migration-compat: expand

-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
-- ADR-520 amendment 4: exact claims use the same durable host set and ledger
-- as wildcards. Keep the legacy column name for compatibility with old edges.
ALTER TABLE custom_domain_tls_hosts
    ADD CONSTRAINT custom_domain_tls_hosts_binding_chk CHECK (wildcard_domain LIKE '*.%' OR wildcard_domain = host) NOT VALID,
    ADD CONSTRAINT custom_domain_tls_hosts_name_chk CHECK (host NOT LIKE '%*%' AND
        (host = wildcard_domain OR host LIKE '%' || substr(wildcard_domain, 2))) NOT VALID;
ALTER TABLE custom_domain_tls_admissions
    ADD CONSTRAINT custom_domain_tls_admissions_registered_host_chk CHECK (host NOT LIKE '%*%' AND
        (host = registered_domain OR host LIKE '%.' || registered_domain)) NOT VALID;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Additive checks are compatible with the preceding release.
SELECT 1;
-- +goose StatementEnd
