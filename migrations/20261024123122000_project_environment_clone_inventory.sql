-- filename: 20261024123122000_project_environment_clone_inventory.sql
-- migration-compat: expand

-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
-- ADR-20261024123122: a full clone names exactly the tables its source holds
-- rows in. The clone schema registry declares, per configuration and
-- customer-data table, how a row belongs to the source: its environment,
-- project, captured workloads or captured deployments, optionally through
-- foreign-key hops. The capture transaction passes those declarations here
-- and gets back the tables with at least one owned row, all read in the
-- capture's repeatable-read snapshot.
--
-- Table and column names come from the compiled registry and are quoted with
-- %I; the function runs with the caller's privileges and only tests whether a
-- row exists, never reading values.
CREATE OR REPLACE FUNCTION project_environment_clone_inventory_present(
    p_project_id uuid, p_environment_id uuid, p_environment_slugs text[],
    p_app_ids uuid[], p_deployment_ids uuid[], p_tables jsonb) RETURNS SETOF text
LANGUAGE plpgsql STABLE AS $$
DECLARE
    probe jsonb;
    hop jsonb;
    alias text;
    query text;
    hops integer;
    present boolean;
BEGIN
    FOR probe IN SELECT value FROM jsonb_array_elements(p_tables) ORDER BY value->>'table' LOOP
        query := format('SELECT EXISTS (SELECT 1 FROM public.%I t0', probe->>'table');
        alias := 't0';
        hops := 0;
        FOR hop IN SELECT value FROM jsonb_array_elements(COALESCE(probe->'via', '[]'::jsonb)) LOOP
            hops := hops + 1;
            query := query || format(' JOIN public.%I t%s ON t%s.%I = %s.%I', hop->>1, hops, hops, hop->>2, alias, hop->>0);
            alias := 't' || hops;
        END LOOP;
        query := query || CASE probe->>'owner'
            WHEN 'environment' THEN CASE WHEN probe->>'slug_column' IS NOT NULL
                THEN format(' WHERE %s.project_id = $1 AND %s.%I = ANY ($3))', alias, alias, probe->>'slug_column')
                ELSE format(' WHERE %s.%I = $2)', alias, probe->>'column') END
            WHEN 'project' THEN format(' WHERE %s.%I = $1)', alias, COALESCE(probe->>'column', 'project_id'))
            WHEN 'app' THEN format(' WHERE %s.%I = ANY ($4))', alias, COALESCE(probe->>'column', 'app_id'))
            WHEN 'deployment' THEN format(' WHERE %s.%I = ANY ($5))', alias, COALESCE(probe->>'column', 'deployment_id'))
        END;
        IF query IS NULL OR probe->>'table' IS NULL THEN
            RAISE EXCEPTION 'clone inventory probe is invalid: %', probe USING ERRCODE = '22023';
        END IF;
        EXECUTE query INTO present USING p_project_id, p_environment_id, p_environment_slugs, p_app_ids, p_deployment_ids;
        IF present THEN
            RETURN NEXT probe->>'table';
        END IF;
    END LOOP;
END
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP FUNCTION IF EXISTS project_environment_clone_inventory_present(uuid, uuid, text[], uuid[], uuid[], jsonb);
-- +goose StatementEnd
