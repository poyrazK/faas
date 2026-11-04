-- filename: 20261103104757466_managed_postgres_usage_waiver_skips_restore_sources.sql
-- migration-compat: expand

-- +goose Up
SET LOCAL lock_timeout = '5s';
-- ADR-20261103091657: a database with undeleted restore copies can now be
-- scheduled for deletion. Its metered usage includes its copies' (a Neon
-- restore is a branch of the source's project), so waiving it during the undo
-- window would also waive the copies', one of which may serve an app. Such a
-- database gets no waiver; closing a window is unchanged.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION record_managed_postgres_usage_waiver() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF OLD.purge_after IS NULL AND NEW.purge_after IS NOT NULL THEN
        IF NOT EXISTS (SELECT 1 FROM managed_postgres_databases d
                       WHERE d.restore_source_database_id = NEW.id AND d.state <> 'deleted') THEN
            INSERT INTO managed_postgres_usage_waivers (database_id, waived_from)
            VALUES (NEW.id, NEW.updated_at)
            ON CONFLICT (database_id, waived_from) DO NOTHING;
        END IF;
    ELSIF OLD.purge_after IS NOT NULL AND NEW.purge_after IS NULL THEN
        UPDATE managed_postgres_usage_waivers
        SET waived_until = greatest(waived_from, NEW.updated_at)
        WHERE database_id = NEW.id AND waived_until IS NULL;
    END IF;
    RETURN NULL;
END
$$;
-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';
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
