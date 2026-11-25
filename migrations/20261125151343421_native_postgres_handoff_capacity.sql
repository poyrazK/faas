-- filename: 20261125151343421_native_postgres_handoff_capacity.sql
-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';

ALTER TABLE database_nodes ADD COLUMN IF NOT EXISTS configured_admission_ceiling_mb integer;
ALTER TABLE native_postgres_base_refreshes ADD COLUMN IF NOT EXISTS handoff_reserved_mb bigint NOT NULL DEFAULT 0;

-- Older schedulers read the original ceiling and omit handoff primaries from
-- usage. Project a reduced ceiling there while retaining the configured value
-- for schedulers that include handoff primaries. Re-registration supplies the
-- configured ceiling to this same trigger.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION project_database_handoff_admission() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    reserved bigint;
BEGIN
    SELECT COALESCE(sum(handoff_reserved_mb), 0) INTO reserved
    FROM native_postgres_base_refreshes
    WHERE node_id = NEW.id AND finished_at IS NULL AND handoff_reserved_mb > 0;

    NEW.configured_admission_ceiling_mb := NULL;
    IF reserved > 0 AND NEW.admission_ceiling_mb > 0 THEN
        NEW.configured_admission_ceiling_mb := NEW.admission_ceiling_mb;
        NEW.admission_ceiling_mb := greatest(1, NEW.admission_ceiling_mb - reserved)::integer;
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgrelid = 'database_nodes'::regclass
        AND tgname = 'database_node_handoff_admission') THEN
        CREATE TRIGGER database_node_handoff_admission
            BEFORE UPDATE OF admission_ceiling_mb ON database_nodes
            FOR EACH ROW EXECUTE FUNCTION project_database_handoff_admission();
    END IF;
END;
$$;
-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';

-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM native_postgres_base_refreshes
        WHERE handoff_reserved_mb > 0 AND finished_at IS NULL) THEN
        RAISE EXCEPTION 'rolling PostgreSQL handoffs must finish before removing admission reservations';
    END IF;
END;
$$;
-- +goose StatementEnd

DROP TRIGGER database_node_handoff_admission ON database_nodes;
UPDATE database_nodes SET admission_ceiling_mb = configured_admission_ceiling_mb
WHERE configured_admission_ceiling_mb IS NOT NULL;
DROP FUNCTION project_database_handoff_admission();
ALTER TABLE native_postgres_base_refreshes DROP COLUMN handoff_reserved_mb;
ALTER TABLE database_nodes DROP COLUMN configured_admission_ceiling_mb;
