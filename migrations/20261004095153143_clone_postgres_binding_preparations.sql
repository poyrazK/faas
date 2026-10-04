-- ADR-531: reserve identities before provider IO; atomically retain sealed proof.
-- +goose Up
CREATE TABLE project_environment_clone_postgres_bindings (
    operation_id uuid NOT NULL REFERENCES project_environment_clone_operations(id) ON DELETE CASCADE,
    source_binding_id uuid NOT NULL,
    target_binding_id uuid NOT NULL REFERENCES managed_postgres_bindings(id),
    reservation_hash text NOT NULL CHECK (reservation_hash ~ '^[a-f0-9]{64}$'),
    preparation_hash text CHECK (preparation_hash ~ '^[a-f0-9]{64}$'),
    preparation jsonb CHECK (jsonb_typeof(preparation) = 'object'),
    PRIMARY KEY(operation_id, source_binding_id),
    UNIQUE(target_binding_id),
    CHECK(source_binding_id <> target_binding_id),
    CHECK((preparation_hash IS NULL) = (preparation IS NULL))
);

-- +goose Down
DROP TABLE project_environment_clone_postgres_bindings;
