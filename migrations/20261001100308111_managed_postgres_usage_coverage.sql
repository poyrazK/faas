-- filename: 20261001100308111_managed_postgres_usage_coverage.sql

-- +goose Up
-- +goose StatementBegin
CREATE TABLE managed_postgres_usage_coverage (
    database_id uuid NOT NULL REFERENCES managed_postgres_databases(id) ON DELETE CASCADE,
    window_seconds bigint NOT NULL CHECK (window_seconds BETWEEN 3600 AND 86400),
    collected_from timestamptz,
    collected_until timestamptz,
    observed_at timestamptz,
    source_database_id uuid REFERENCES managed_postgres_databases(id),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (database_id, window_seconds),
    CHECK (source_database_id IS DISTINCT FROM database_id),
    CHECK (
        (source_database_id IS NULL AND collected_from IS NOT NULL
         AND collected_until IS NOT NULL AND collected_until > collected_from AND observed_at IS NOT NULL)
        OR
        (source_database_id IS NOT NULL AND collected_from IS NULL
         AND collected_until IS NULL AND observed_at IS NULL)
    )
);
-- Existing ledger rows are deliberately not promoted to coverage: older
-- collectors could leave gaps. The next sweep verifies them from creation.
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE managed_postgres_usage_coverage;
-- +goose StatementEnd
