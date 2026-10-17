-- ADR-1545: retain source ownership until pool recovery or retirement is confirmed.
-- +goose Up
CREATE TABLE IF NOT EXISTS function_migration_sources (
    instance_id uuid PRIMARY KEY REFERENCES migration_leases(instance_id) ON DELETE CASCADE,
    lease_token text NOT NULL UNIQUE REFERENCES migration_leases(lease_token) ON DELETE CASCADE,
    source_node_id text NOT NULL CHECK (source_node_id <> ''),
    source_wake_id uuid NOT NULL,
    phase text NOT NULL CHECK (phase IN ('preparing', 'prepared', 'recovering', 'recovered', 'retired'))
);

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM function_migration_sources) THEN
        RAISE EXCEPTION 'cannot remove function migration authority while source leases remain';
    END IF;
END $$;
-- +goose StatementEnd
DROP TABLE function_migration_sources;
