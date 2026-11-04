-- filename: 20261023115254527_disable_full_clone_configuration_fences.sql
-- migration-compat: expand

-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
-- ADR-20261023091049: the configuration write fence serves only full
-- environment clones, which are not admitted. Every write to a fenced table
-- still paid two to_jsonb calls and shared locks on the clone clock and project
-- guard rows. The fence is switched off until admission opens; the guard rows
-- keep being created, so turning it back on needs no backfill.
--
-- set_full_clone_configuration_fences switches every fence trigger in name
-- order, so concurrent callers take the table locks in one order. Enabling
-- takes SHARE ROW EXCLUSIVE on each table, which waits for in-flight writers:
-- once it commits, every later write fires the fence, and a capture's clock
-- update drains the rest. object_version_protection keeps its trigger, which
-- also keeps original Object Lock protection evidence immutable.
CREATE OR REPLACE FUNCTION set_full_clone_configuration_fences(enabled boolean) RETURNS integer
LANGUAGE plpgsql AS $$
DECLARE
    fence record;
    changed integer := 0;
BEGIN
    FOR fence IN
        SELECT c.relname
        FROM pg_trigger t
        JOIN pg_class c ON c.oid = t.tgrelid
        JOIN pg_namespace n ON n.oid = c.relnamespace
        WHERE t.tgname = 'clone_configuration_write_fence' AND NOT t.tgisinternal
          AND n.nspname = 'public' AND c.relname <> 'object_version_protection'
        ORDER BY c.relname
    LOOP
        EXECUTE format('ALTER TABLE public.%I %s TRIGGER clone_configuration_write_fence',
            fence.relname, CASE WHEN enabled THEN 'ENABLE' ELSE 'DISABLE' END);
        changed := changed + 1;
    END LOOP;
    RETURN changed;
END
$$;

SELECT set_full_clone_configuration_fences(false);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SELECT set_full_clone_configuration_fences(true);
DROP FUNCTION IF EXISTS set_full_clone_configuration_fences(boolean);
-- +goose StatementEnd
