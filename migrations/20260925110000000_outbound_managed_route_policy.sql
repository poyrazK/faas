-- +goose Up
ALTER TABLE outbound_integrations
    ADD COLUMN allowed_methods text[] NOT NULL DEFAULT ARRAY[]::text[],
    ADD COLUMN allowed_path_prefixes text[] NOT NULL DEFAULT ARRAY[]::text[],
    ADD CONSTRAINT outbound_integrations_allowed_methods_count_chk
        CHECK (cardinality(allowed_methods) <= 6),
    ADD CONSTRAINT outbound_integrations_allowed_path_prefixes_count_chk
        CHECK (cardinality(allowed_path_prefixes) <= 32);

-- Existing managed rows have empty permissions after migration and are
-- intentionally denied by the resolver until outboundd provisions an explicit
-- policy. This keeps the migration compatible without granting broad access.

-- +goose Down
ALTER TABLE outbound_integrations
    DROP CONSTRAINT outbound_integrations_allowed_path_prefixes_count_chk,
    DROP CONSTRAINT outbound_integrations_allowed_methods_count_chk,
    DROP COLUMN allowed_path_prefixes,
    DROP COLUMN allowed_methods;
