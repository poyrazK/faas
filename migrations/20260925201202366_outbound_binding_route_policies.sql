-- +goose Up
ALTER TABLE outbound_app_bindings
    ADD COLUMN allowed_methods text[],
    ADD COLUMN allowed_path_prefixes text[],
    ADD CONSTRAINT outbound_app_binding_route_pair_chk CHECK (
        (allowed_methods IS NULL AND allowed_path_prefixes IS NULL)
        OR (allowed_methods IS NOT NULL AND allowed_path_prefixes IS NOT NULL
            AND cardinality(allowed_methods) BETWEEN 1 AND 6
            AND cardinality(allowed_path_prefixes) BETWEEN 1 AND 32)
    );

-- NULL means the original full operator ceiling. The API only writes explicit,
-- validated subsets; the gateway intersects them with the live ceiling.

-- +goose Down
ALTER TABLE outbound_app_bindings
    DROP CONSTRAINT outbound_app_binding_route_pair_chk,
    DROP COLUMN allowed_methods,
    DROP COLUMN allowed_path_prefixes;
