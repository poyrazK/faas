-- migration-compat: expand

-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
-- Seed separately from constraint activation, without holding DDL locks.
-- Existing verified exact claims were eligible under the preceding policy.
INSERT INTO custom_domain_tls_hosts (host, wildcard_domain, admitted_at)
SELECT domain, domain, verified_at FROM custom_domains
WHERE domain NOT LIKE '*.%' AND verified_at IS NOT NULL
ON CONFLICT (host) DO UPDATE
SET wildcard_domain = excluded.wildcard_domain, admitted_at = excluded.admitted_at;
-- +goose StatementEnd

-- +goose Down
-- Retain runtime admissions for certificate reloads after an edge rollback.
SELECT 1;
