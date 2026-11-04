-- migration-compat: expand

-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
-- Validate separately from ADD CONSTRAINT so the scan does not block writers.
ALTER TABLE custom_domain_tls_hosts VALIDATE CONSTRAINT custom_domain_tls_hosts_binding_chk;
ALTER TABLE custom_domain_tls_hosts VALIDATE CONSTRAINT custom_domain_tls_hosts_name_chk;
ALTER TABLE custom_domain_tls_admissions VALIDATE CONSTRAINT custom_domain_tls_admissions_registered_host_chk;
-- +goose StatementEnd

-- +goose Down
SELECT 1;
