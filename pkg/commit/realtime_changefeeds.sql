-- Explicit customer-owner installation after realtime_schema.sql. No relay DDL.
-- Trigger functions use the writing role's privileges (never SECURITY DEFINER).
CREATE FUNCTION public.gregale_realtime_capture_change() RETURNS trigger
LANGUAGE plpgsql SET search_path = pg_catalog AS $$
DECLARE
    cfg jsonb := TG_ARGV[0]::jsonb;
    before_row jsonb;
    after_row jsonb;
    selected_before jsonb;
    selected_after jsonb;
    row_key jsonb;
    route text;
    tenant text;
    payload jsonb;
    col text;
BEGIN
    IF TG_OP <> 'INSERT' THEN before_row := to_jsonb(OLD); END IF;
    IF TG_OP <> 'DELETE' THEN after_row := to_jsonb(NEW); END IF;
    FOR col IN SELECT jsonb_array_elements_text(cfg->'columns') LOOP
        IF NOT (coalesce(after_row,before_row) ? col) THEN
            RAISE EXCEPTION 'configured realtime changefeed column no longer exists: %',col;
        END IF;
    END LOOP;
    SELECT jsonb_object_agg(k, before_row->k), jsonb_object_agg(k, after_row->k)
      INTO selected_before, selected_after
      FROM jsonb_array_elements_text(cfg->'columns') AS keys(k);
    IF TG_OP = 'UPDATE' AND selected_before IS NOT DISTINCT FROM selected_after THEN
        RETURN NULL;
    END IF;
    IF cfg->>'tenantColumn' IS NOT NULL THEN
        IF TG_OP = 'UPDATE' AND before_row->(cfg->>'tenantColumn') IS DISTINCT FROM after_row->(cfg->>'tenantColumn') THEN
            RAISE EXCEPTION 'realtime changefeed tenant cannot change; delete and insert instead';
        END IF;
        tenant := coalesce(after_row,before_row)->>(cfg->>'tenantColumn');
        IF tenant IS NULL OR tenant = '' OR tenant ~ '[[:space:]/?#]' THEN
            RAISE EXCEPTION 'invalid realtime changefeed tenant route';
        END IF;
    END IF;
    route := (cfg->>'channel') || coalesce(tenant,'');
    IF octet_length(route) NOT BETWEEN 1 AND 256 OR route ~ '[[:cntrl:]/?#]' OR btrim(route) <> route THEN
        RAISE EXCEPTION 'invalid realtime changefeed channel';
    END IF;
    SELECT jsonb_object_agg(k, coalesce(after_row,before_row)->k) INTO row_key
      FROM jsonb_array_elements_text(cfg->'keyColumns') AS keys(k);
    payload := jsonb_build_object('type','database.change','feed',cfg->>'name',
      'operation',lower(TG_OP),'key',row_key,
      'before',CASE WHEN TG_OP = 'INSERT' THEN NULL ELSE selected_before END,
      'after',CASE WHEN TG_OP = 'DELETE' THEN NULL ELSE selected_after END);
    INSERT INTO public.gregale_realtime_outbox(event_id,endpoint_id,channel,data)
      VALUES(gen_random_uuid(),cfg->>'endpointId',route,convert_to(payload::text,'UTF8'));
    RETURN NULL;
END;
$$;

-- One named binding per ordinary table. Caller owns transaction and table DDL.
CREATE FUNCTION public.gregale_realtime_install_changefeed(target regclass, config jsonb) RETURNS void
LANGUAGE plpgsql SET search_path = pg_catalog AS $$
DECLARE
    cfg jsonb := config;
    keys jsonb;
    col text;
    table_schema text;
    table_name text;
BEGIN
    IF jsonb_typeof(cfg) IS DISTINCT FROM 'object'
       OR cfg->>'name' IS NULL OR cfg->>'name' !~ '^[a-z][a-z0-9_]{0,39}$'
       OR cfg->>'endpointId' IS NULL OR cfg->>'endpointId' !~ '^[0-9a-f]{32}$'
       OR cfg->>'channel' IS NULL OR octet_length(cfg->>'channel') NOT BETWEEN 1 AND 256
       OR cfg->>'channel' ~ '[[:cntrl:]/?#]' OR btrim(cfg->>'channel') <> cfg->>'channel'
       OR jsonb_typeof(cfg->'columns') IS DISTINCT FROM 'array'
       OR jsonb_array_length(cfg->'columns') = 0 THEN
        RAISE EXCEPTION 'invalid realtime changefeed configuration';
    END IF;
    EXECUTE format('LOCK TABLE %s IN ACCESS EXCLUSIVE MODE',target);
    SELECT n.nspname,c.relname INTO table_schema,table_name
      FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
      WHERE c.oid=target AND c.relkind='r';
    IF table_name IS NULL OR table_schema IN ('pg_catalog','information_schema')
       OR (table_schema='public' AND table_name LIKE 'gregale_%') THEN
        RAISE EXCEPTION 'changefeed requires an ordinary application table';
    END IF;
    SELECT jsonb_agg(a.attname ORDER BY ord.ordinality) INTO keys
      FROM pg_index i CROSS JOIN LATERAL unnest(i.indkey) WITH ORDINALITY AS ord(attnum,ordinality)
      JOIN pg_attribute a ON a.attrelid=i.indrelid AND a.attnum=ord.attnum
      WHERE i.indrelid=target AND i.indisprimary AND ord.ordinality <= i.indnkeyatts;
    IF keys IS NULL THEN RAISE EXCEPTION 'changefeed table requires a primary key'; END IF;
    -- Key and tenant fields must be explicitly allowed; never silently expose them.
    FOR col IN SELECT jsonb_array_elements_text(cfg->'columns' || keys ||
        CASE WHEN cfg->>'tenantColumn' IS NULL THEN '[]'::jsonb ELSE jsonb_build_array(cfg->>'tenantColumn') END)
    LOOP
        IF NOT EXISTS (SELECT 1 FROM pg_attribute WHERE attrelid=target AND attname=col AND attnum>0 AND NOT attisdropped)
           OR NOT (cfg->'columns' ? col) THEN
            RAISE EXCEPTION 'changefeed column missing or not explicitly allowed: %',col;
        END IF;
    END LOOP;
    cfg := cfg || jsonb_build_object('keyColumns',keys);
    -- Identifier/literal quoting is mandatory for customer-selected tables/config.
    EXECUTE format('CREATE TRIGGER %I AFTER INSERT OR UPDATE OR DELETE ON %I.%I FOR EACH ROW EXECUTE FUNCTION public.gregale_realtime_capture_change(%L)',
      'gregale_feed_' || (cfg->>'name'),table_schema,table_name,cfg::text);
END;
$$;

CREATE FUNCTION public.gregale_realtime_remove_changefeed(target regclass, feed_name text) RETURNS void
LANGUAGE plpgsql SET search_path = pg_catalog AS $$
BEGIN
    IF feed_name IS NULL OR feed_name !~ '^[a-z][a-z0-9_]{0,39}$' THEN
        RAISE EXCEPTION 'invalid realtime changefeed name';
    END IF;
    EXECUTE format('DROP TRIGGER %I ON %s','gregale_feed_' || feed_name,target);
END;
$$;
