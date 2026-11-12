-- Row-change capture for Gregale changes sources (ADR-20261112140000).
-- Installed after schema.sql by the database owner (BYO) or by Gregale as the
-- customer schema owner (managed PostgreSQL). Re-running it is safe.
--
-- The capture function is SECURITY DEFINER so writers of a tracked table need
-- no grant on the outbox: a role created after installation must never have
-- its writes refused by capture. A trigger function cannot be called directly,
-- its argument is fixed by the installer, and search_path is pinned with every
-- customer object schema-qualified.
CREATE OR REPLACE FUNCTION public.gregale_changes_capture() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, pg_temp AS $$
DECLARE
    cfg jsonb := TG_ARGV[0]::jsonb;
    op text := pg_catalog.lower(TG_OP);
    before_row jsonb;
    after_row jsonb;
    present jsonb;
    selected_before jsonb;
    selected_after jsonb;
    row_key jsonb;
    missing jsonb;
    payload jsonb;
BEGIN
    IF TG_OP <> 'INSERT' THEN before_row := pg_catalog.to_jsonb(OLD); END IF;
    IF TG_OP <> 'DELETE' THEN after_row := pg_catalog.to_jsonb(NEW); END IF;
    present := COALESCE(after_row, before_row);
    -- A dropped allowlisted column is reported, never raised: capture must not
    -- refuse the customer's write.
    SELECT COALESCE(pg_catalog.jsonb_object_agg(k, before_row -> k) FILTER (WHERE present ? k), '{}'::jsonb),
           COALESCE(pg_catalog.jsonb_object_agg(k, after_row -> k) FILTER (WHERE present ? k), '{}'::jsonb),
           pg_catalog.jsonb_agg(k) FILTER (WHERE NOT present ? k)
      INTO selected_before, selected_after, missing
      FROM pg_catalog.jsonb_array_elements_text(cfg -> 'columns') AS c(k);
    IF TG_OP = 'UPDATE' AND selected_before IS NOT DISTINCT FROM selected_after THEN
        RETURN NULL;
    END IF;
    SELECT pg_catalog.jsonb_object_agg(k, present -> k) INTO row_key
      FROM pg_catalog.jsonb_array_elements_text(cfg -> 'keyColumns') AS c(k);
    payload := pg_catalog.jsonb_build_object(
        'feed', cfg ->> 'name',
        'table', cfg ->> 'table',
        'operation', op,
        'key', row_key,
        'before', CASE WHEN TG_OP = 'INSERT' THEN NULL ELSE selected_before END,
        'after', CASE WHEN TG_OP = 'DELETE' THEN NULL ELSE selected_after END);
    IF missing IS NOT NULL THEN
        payload := payload || pg_catalog.jsonb_build_object('missing_columns', missing);
    END IF;
    INSERT INTO public.gregale_outbox(event_id, event_type, payload)
    VALUES (pg_catalog.gen_random_uuid(), 'database.' || (cfg ->> 'name') || '.' || op, payload);
    RETURN NULL;
END;
$$;
REVOKE ALL ON FUNCTION public.gregale_changes_capture() FROM PUBLIC;

-- One trigger per feed, named gregale_changes_<feed>. Installing a feed again
-- replaces its configuration.
CREATE OR REPLACE FUNCTION public.gregale_changes_install_feed(target regclass, config jsonb) RETURNS void
LANGUAGE plpgsql SET search_path = pg_catalog, pg_temp AS $$
DECLARE
    feed text := config ->> 'name';
    cols jsonb := config -> 'columns';
    ops jsonb := COALESCE(config -> 'operations', '["insert","update","delete"]'::jsonb);
    keys jsonb;
    col text;
    events text;
    table_schema name;
    table_name name;
BEGIN
    IF pg_catalog.jsonb_typeof(config) IS DISTINCT FROM 'object'
       OR feed IS NULL OR feed !~ '^[a-z][a-z0-9_]{0,39}$'
       OR pg_catalog.jsonb_typeof(cols) IS DISTINCT FROM 'array'
       OR pg_catalog.jsonb_array_length(cols) NOT BETWEEN 1 AND 100
       OR pg_catalog.jsonb_typeof(ops) IS DISTINCT FROM 'array'
       OR pg_catalog.jsonb_array_length(ops) NOT BETWEEN 1 AND 3
       OR EXISTS (SELECT 1 FROM pg_catalog.jsonb_array_elements(cols) AS c(v) WHERE pg_catalog.jsonb_typeof(v) <> 'string')
       OR EXISTS (SELECT 1 FROM pg_catalog.jsonb_array_elements(ops) AS o(v)
                   WHERE pg_catalog.jsonb_typeof(v) <> 'string' OR v #>> '{}' NOT IN ('insert','update','delete')) THEN
        RAISE EXCEPTION 'invalid Gregale changes feed configuration';
    END IF;
    SELECT n.nspname, c.relname INTO table_schema, table_name
      FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
     WHERE c.oid = target AND c.relkind = 'r';
    IF table_name IS NULL OR table_schema IN ('pg_catalog', 'information_schema', 'pg_toast')
       OR (table_schema = 'public' AND table_name LIKE 'gregale\_%') THEN
        RAISE EXCEPTION 'Gregale changes feed requires an ordinary application table';
    END IF;
    SELECT pg_catalog.jsonb_agg(a.attname ORDER BY ord.ordinality) INTO keys
      FROM pg_catalog.pg_index i
      CROSS JOIN LATERAL pg_catalog.unnest(i.indkey) WITH ORDINALITY AS ord(attnum, ordinality)
      JOIN pg_catalog.pg_attribute a ON a.attrelid = i.indrelid AND a.attnum = ord.attnum
     WHERE i.indrelid = target AND i.indisprimary AND ord.ordinality <= i.indnkeyatts;
    IF keys IS NULL THEN
        RAISE EXCEPTION 'Gregale changes feed table requires a primary key';
    END IF;
    -- Every emitted field, primary key included, must be explicitly allowed.
    FOR col IN SELECT pg_catalog.jsonb_array_elements_text(cols || keys) LOOP
        IF NOT EXISTS (SELECT 1 FROM pg_catalog.pg_attribute
                        WHERE attrelid = target AND attname = col AND attnum > 0 AND NOT attisdropped)
           OR NOT (cols ? col) THEN
            RAISE EXCEPTION 'Gregale changes feed column missing or not allowlisted: %', col;
        END IF;
    END LOOP;
    -- Built only from the validated vocabulary above.
    SELECT pg_catalog.string_agg(pg_catalog.upper(v), ' OR ' ORDER BY v) INTO events
      FROM (SELECT DISTINCT o #>> '{}' AS v FROM pg_catalog.jsonb_array_elements(ops) AS e(o)) AS d;
    EXECUTE pg_catalog.format('DROP TRIGGER IF EXISTS %I ON %I.%I', 'gregale_changes_' || feed, table_schema, table_name);
    EXECUTE pg_catalog.format(
        'CREATE TRIGGER %I AFTER %s ON %I.%I FOR EACH ROW EXECUTE FUNCTION public.gregale_changes_capture(%L)',
        'gregale_changes_' || feed, events, table_schema, table_name,
        pg_catalog.jsonb_build_object('name', feed,
            'table', pg_catalog.format('%I.%I', table_schema, table_name),
            'columns', cols, 'keyColumns', keys)::text);
END;
$$;
REVOKE ALL ON FUNCTION public.gregale_changes_install_feed(regclass, jsonb) FROM PUBLIC;

CREATE OR REPLACE FUNCTION public.gregale_changes_remove_feed(target regclass, feed text) RETURNS void
LANGUAGE plpgsql SET search_path = pg_catalog, pg_temp AS $$
BEGIN
    IF feed IS NULL OR feed !~ '^[a-z][a-z0-9_]{0,39}$' THEN
        RAISE EXCEPTION 'invalid Gregale changes feed name';
    END IF;
    EXECUTE pg_catalog.format('DROP TRIGGER IF EXISTS %I ON %s', 'gregale_changes_' || feed, target);
END;
$$;
REVOKE ALL ON FUNCTION public.gregale_changes_remove_feed(regclass, text) FROM PUBLIC;
