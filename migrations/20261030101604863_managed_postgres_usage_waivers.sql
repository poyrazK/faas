-- filename: 20261030101604863_managed_postgres_usage_waivers.sql
-- migration-compat: expand

-- +goose Up
SET LOCAL lock_timeout = '5s';
-- ADR-20261029174950 decision 6: a database's usage is waived while its
-- delete is scheduled. Each row is one scheduled window: it opens when
-- purge_after is set and closes when it is cleared, by an undelete or an
-- immediate delete (purge). A trigger keeps it, so every apid that schedules
-- a delete records it, whatever its version. Customer usage leaves out the
-- usage windows that fall wholly inside one; the fleet spend breaker does not,
-- because the provider still charges for them. The time is the row's
-- updated_at, the writer's clock that the ledger's windows also use.
CREATE TABLE IF NOT EXISTS managed_postgres_usage_waivers (
    database_id uuid NOT NULL REFERENCES managed_postgres_databases(id) ON DELETE CASCADE,
    waived_from timestamptz NOT NULL,
    waived_until timestamptz,
    PRIMARY KEY (database_id, waived_from),
    CHECK (waived_until IS NULL OR waived_until >= waived_from)
);

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION record_managed_postgres_usage_waiver() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF OLD.purge_after IS NULL AND NEW.purge_after IS NOT NULL THEN
        INSERT INTO managed_postgres_usage_waivers (database_id, waived_from)
        VALUES (NEW.id, NEW.updated_at)
        ON CONFLICT (database_id, waived_from) DO NOTHING;
    ELSIF OLD.purge_after IS NOT NULL AND NEW.purge_after IS NULL THEN
        UPDATE managed_postgres_usage_waivers
        SET waived_until = greatest(waived_from, NEW.updated_at)
        WHERE database_id = NEW.id AND waived_until IS NULL;
    END IF;
    RETURN NULL;
END
$$;
-- +goose StatementEnd

DROP TRIGGER IF EXISTS managed_postgres_databases_usage_waiver ON managed_postgres_databases;
CREATE TRIGGER managed_postgres_databases_usage_waiver
    AFTER UPDATE OF purge_after ON managed_postgres_databases
    FOR EACH ROW
    WHEN (OLD.purge_after IS DISTINCT FROM NEW.purge_after)
    EXECUTE FUNCTION record_managed_postgres_usage_waiver();

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TRIGGER IF EXISTS managed_postgres_databases_usage_waiver ON managed_postgres_databases;
DROP FUNCTION IF EXISTS record_managed_postgres_usage_waiver();
DROP TABLE IF EXISTS managed_postgres_usage_waivers;
