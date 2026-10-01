-- filename: 20261001105914375_managed_postgres_migration_credentials.sql

-- +goose Up
-- +goose StatementBegin
ALTER TABLE managed_postgres_bindings DROP CONSTRAINT managed_postgres_bindings_access_check;
ALTER TABLE managed_postgres_bindings ADD CONSTRAINT managed_postgres_bindings_access_check
    CHECK (access IN ('read_write', 'read_only', 'migration'));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Refuse a downgrade while migration bindings exist; never rewrite their access.
ALTER TABLE managed_postgres_bindings DROP CONSTRAINT managed_postgres_bindings_access_check;
ALTER TABLE managed_postgres_bindings ADD CONSTRAINT managed_postgres_bindings_access_check
    CHECK (access IN ('read_write', 'read_only'));
-- +goose StatementEnd
