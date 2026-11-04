-- migration-compat: expand

-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
-- ADR-520 amendment 4: exact claims use the same durable host set and ledger
-- as wildcards. Keep the legacy column name for compatibility with old edges.
-- A recovered schema can already contain these checks before its Goose row.
-- Preserve existing definitions, including any later strengthening.
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'custom_domain_tls_hosts'::regclass
                   AND conname = 'custom_domain_tls_hosts_binding_chk') THEN
        ALTER TABLE custom_domain_tls_hosts ADD CONSTRAINT custom_domain_tls_hosts_binding_chk
            CHECK (wildcard_domain LIKE '*.%' OR wildcard_domain = host) NOT VALID;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'custom_domain_tls_hosts'::regclass
                   AND conname = 'custom_domain_tls_hosts_name_chk') THEN
        ALTER TABLE custom_domain_tls_hosts ADD CONSTRAINT custom_domain_tls_hosts_name_chk
            CHECK (host NOT LIKE '%*%' AND (host = wildcard_domain OR host LIKE '%' || substr(wildcard_domain, 2))) NOT VALID;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'custom_domain_tls_admissions'::regclass
                   AND conname = 'custom_domain_tls_admissions_registered_host_chk') THEN
        ALTER TABLE custom_domain_tls_admissions ADD CONSTRAINT custom_domain_tls_admissions_registered_host_chk
            CHECK (host NOT LIKE '%*%' AND (host = registered_domain OR host LIKE '%.' || registered_domain)) NOT VALID;
    END IF;
END
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Additive checks are compatible with the preceding release.
SELECT 1;
-- +goose StatementEnd
