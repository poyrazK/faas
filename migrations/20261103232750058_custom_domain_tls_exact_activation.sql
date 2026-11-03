-- migration-compat: expand

-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
-- The broader checks are already validated. Remove the wildcard-only checks
-- in a separate transaction; old edges keep using the same table and columns.
ALTER TABLE custom_domain_tls_hosts
    DROP CONSTRAINT custom_domain_tls_hosts_wildcard_chk,
    DROP CONSTRAINT custom_domain_tls_hosts_host_chk;
ALTER TABLE custom_domain_tls_admissions DROP CONSTRAINT custom_domain_tls_admissions_host_chk;
-- +goose StatementEnd

-- +goose Down
-- Retain the expanded checks and runtime admissions when rolling the edge back.
SELECT 1;
