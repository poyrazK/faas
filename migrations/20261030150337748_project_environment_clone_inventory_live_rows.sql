-- filename: 20261030150337748_project_environment_clone_inventory_live_rows.sql
-- migration-compat: expand

-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
-- ADR-20261024123122 amendment 2026-10-30: a probe may name the conditions a
-- row must meet to count. A deleted bucket, a retired queue binding or a
-- revoked credential holds nothing a clone would copy, and a row scoped to
-- another environment is not the source's, so neither blocks admission.
-- Conditions apply to the probed table (t0): {"column": c, "null": true}
-- needs c IS NULL, {"column": c, "not": v} needs c IS DISTINCT FROM v, and
-- {"column": c, "source_scope": true} needs c (empty read as default) to be
-- one of the source environment's slugs.
CREATE OR REPLACE FUNCTION project_environment_clone_inventory_present(
    p_project_id uuid, p_environment_id uuid, p_environment_slugs text[],
    p_app_ids uuid[], p_deployment_ids uuid[], p_tables jsonb) RETURNS SETOF text
LANGUAGE plpgsql STABLE AS $$
DECLARE
    probe jsonb;
    hop jsonb;
    live jsonb;
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
                THEN format(' WHERE %s.project_id = $1 AND %s.%I = ANY ($3)', alias, alias, probe->>'slug_column')
                ELSE format(' WHERE %s.%I = $2', alias, probe->>'column') END
            WHEN 'project' THEN format(' WHERE %s.%I = $1', alias, COALESCE(probe->>'column', 'project_id'))
            WHEN 'app' THEN format(' WHERE %s.%I = ANY ($4)', alias, COALESCE(probe->>'column', 'app_id'))
            WHEN 'deployment' THEN format(' WHERE %s.%I = ANY ($5)', alias, COALESCE(probe->>'column', 'deployment_id'))
        END;
        IF query IS NULL OR probe->>'table' IS NULL THEN
            RAISE EXCEPTION 'clone inventory probe is invalid: %', probe USING ERRCODE = '22023';
        END IF;
        FOR live IN SELECT value FROM jsonb_array_elements(COALESCE(probe->'live', '[]'::jsonb)) LOOP
            IF live->>'column' IS NULL THEN
                RAISE EXCEPTION 'clone inventory live condition is invalid: %', live USING ERRCODE = '22023';
            ELSIF (live->>'null')::boolean IS TRUE THEN
                query := query || format(' AND t0.%I IS NULL', live->>'column');
            ELSIF live->>'not' IS NOT NULL THEN
                query := query || format(' AND t0.%I IS DISTINCT FROM %L', live->>'column', live->>'not');
            ELSIF (live->>'source_scope')::boolean IS TRUE THEN
                query := query || format(' AND coalesce(nullif(t0.%I::text, ''''), ''default'') = ANY ($3)', live->>'column');
            ELSE
                RAISE EXCEPTION 'clone inventory live condition is invalid: %', live USING ERRCODE = '22023';
            END IF;
        END LOOP;
        query := query || ')';
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
