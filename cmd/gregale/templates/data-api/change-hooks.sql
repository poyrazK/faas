-- Data API change hooks (ADR-20261022121642). Apply as the schema owner in a
-- migration. `gregale data-api hooks sql` prints this with your schema.
-- A trigger on each hooked table records its changes in
-- {{schema}}.gregale_change_events inside the writing transaction. The Data
-- API publishes them as Gregale events, in order, and deletes them.

CREATE TABLE {{schema}}.gregale_change_events (
  id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  event_key uuid NOT NULL DEFAULT gen_random_uuid() UNIQUE,
  relation_schema text NOT NULL,
  relation_name text NOT NULL,
  op text NOT NULL CHECK (op IN ('insert', 'update', 'delete')),
  row_key jsonb NOT NULL,
  old_row jsonb,
  new_row jsonb,
  changed text[],
  txid bigint NOT NULL DEFAULT txid_current(),
  captured_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  attempts integer NOT NULL DEFAULT 0,
  available_at timestamptz NOT NULL DEFAULT '-infinity',
  claimed_by text,
  claimed_until timestamptz,
  failed_at timestamptz,
  failure_code text CHECK (octet_length(failure_code) <= 64)
);
ALTER TABLE {{schema}}.gregale_change_events ENABLE ROW LEVEL SECURITY;

-- Only the Data API's relay reads and settles events: it adds the
-- gateway-owned gregale_access = 'hooks' claim, which the gateway never gives
-- a client request. Hooked writes insert as the owner, which RLS skips.
CREATE POLICY gregale_change_relay ON {{schema}}.gregale_change_events
  USING (coalesce(nullif(current_setting('request.jwt.claims', true), '')::jsonb ->> 'gregale_access', '') = 'hooks')
  WITH CHECK (coalesce(nullif(current_setting('request.jwt.claims', true), '')::jsonb ->> 'gregale_access', '') = 'hooks');

CREATE SCHEMA gregale_hooks;
REVOKE ALL ON SCHEMA gregale_hooks FROM PUBLIC;

-- capture records one change. Its arguments are the key columns, the payload
-- ('row' or 'keys'), excluded columns and the hooked relation's name.
CREATE FUNCTION gregale_hooks.capture() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, pg_temp AS $$
DECLARE
  key_columns text[] := TG_ARGV[0]::text[];
  excluded text[] := TG_ARGV[2]::text[];
  source jsonb;
  old_row jsonb;
  new_row jsonb;
  changed text[];
  row_key jsonb;
BEGIN
  IF current_setting('gregale_hooks.skip', true) = 'on' THEN
    RETURN NULL;
  END IF;
  IF TG_OP <> 'INSERT' THEN
    old_row := to_jsonb(OLD) - excluded;
  END IF;
  IF TG_OP <> 'DELETE' THEN
    new_row := to_jsonb(NEW) - excluded;
  END IF;
  IF TG_OP = 'UPDATE' THEN
    SELECT coalesce(array_agg(n.key ORDER BY n.key), '{}') INTO changed
      FROM jsonb_each(new_row) n WHERE n.value IS DISTINCT FROM old_row -> n.key;
    IF cardinality(changed) = 0 THEN
      RETURN NULL;
    END IF;
  END IF;
  source := coalesce(new_row, old_row);
  SELECT jsonb_object_agg(k, source -> k) INTO row_key FROM unnest(key_columns) k;
  IF TG_ARGV[1] = 'keys' THEN
    old_row := NULL;
    new_row := NULL;
  END IF;
  INSERT INTO {{schema}}.gregale_change_events (relation_schema, relation_name, op, row_key, old_row, new_row, changed)
  VALUES (TG_ARGV[3], TG_ARGV[4], lower(TG_OP), row_key, old_row, new_row, changed);
  RETURN NULL;
END
$$;
REVOKE ALL ON FUNCTION gregale_hooks.capture() FROM PUBLIC;

-- enable hooks a table with a primary key, or changes its options.
CREATE FUNCTION gregale_hooks.enable(relation regclass, payload text DEFAULT 'row', exclude text[] DEFAULT '{}')
RETURNS void LANGUAGE plpgsql SET search_path = pg_catalog, pg_temp AS $$
DECLARE
  target record;
  key_columns text[];
  unknown text;
BEGIN
  IF payload IS NULL OR payload NOT IN ('row', 'keys') THEN
    RAISE EXCEPTION 'payload must be row or keys' USING ERRCODE = '22023';
  END IF;
  exclude := coalesce(exclude, '{}');
  SELECT n.nspname AS schema, c.relname AS name, c.relkind AS kind INTO target
    FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace WHERE c.oid = relation;
  IF target.kind NOT IN ('r', 'p') THEN
    RAISE EXCEPTION '% is not a table', relation USING ERRCODE = '42809';
  END IF;
  IF relation = '{{schema}}.gregale_change_events'::regclass THEN
    RAISE EXCEPTION 'the change event table cannot be hooked' USING ERRCODE = '22023';
  END IF;
  SELECT array_agg(a.attname::text ORDER BY array_position(i.indkey::int2[], a.attnum)) INTO key_columns
    FROM pg_index i JOIN pg_attribute a ON a.attrelid = i.indrelid AND a.attnum = ANY (i.indkey::int2[])
    WHERE i.indrelid = relation AND i.indisprimary;
  IF key_columns IS NULL THEN
    RAISE EXCEPTION '% needs a primary key to be hooked', relation USING ERRCODE = '55000';
  END IF;
  IF key_columns && exclude THEN
    RAISE EXCEPTION 'primary key columns cannot be excluded' USING ERRCODE = '22023';
  END IF;
  SELECT e INTO unknown FROM unnest(exclude) e
    WHERE NOT EXISTS (SELECT 1 FROM pg_attribute WHERE attrelid = relation AND attname = e AND attnum > 0 AND NOT attisdropped) LIMIT 1;
  IF unknown IS NOT NULL THEN
    RAISE EXCEPTION 'column % of % does not exist', unknown, relation USING ERRCODE = '42703';
  END IF;
  EXECUTE format('DROP TRIGGER IF EXISTS gregale_change_hook ON %s', relation);
  EXECUTE format('CREATE TRIGGER gregale_change_hook AFTER INSERT OR UPDATE OR DELETE ON %s FOR EACH ROW EXECUTE FUNCTION gregale_hooks.capture(%L, %L, %L, %L, %L)',
    relation, key_columns::text, payload, exclude::text, target.schema, target.name);
END
$$;
REVOKE ALL ON FUNCTION gregale_hooks.enable(regclass, text, text[]) FROM PUBLIC;

-- disable stops recording a table's changes; recorded events still publish.
CREATE FUNCTION gregale_hooks.disable(relation regclass) RETURNS void
LANGUAGE plpgsql SET search_path = pg_catalog, pg_temp AS $$
BEGIN
  EXECUTE format('DROP TRIGGER IF EXISTS gregale_change_hook ON %s', relation);
END
$$;
REVOKE ALL ON FUNCTION gregale_hooks.disable(regclass) FROM PUBLIC;

-- retry_failed queues events the platform rejected again. They publish after
-- newer events of the same rows.
CREATE FUNCTION gregale_hooks.retry_failed() RETURNS bigint
LANGUAGE plpgsql SET search_path = pg_catalog, pg_temp AS $$
DECLARE
  n bigint;
BEGIN
  UPDATE {{schema}}.gregale_change_events SET failed_at = NULL, failure_code = NULL, attempts = 0,
    available_at = '-infinity', claimed_by = NULL, claimed_until = NULL
  WHERE failed_at IS NOT NULL;
  GET DIAGNOSTICS n = ROW_COUNT;
  RETURN n;
END
$$;
REVOKE ALL ON FUNCTION gregale_hooks.retry_failed() FROM PUBLIC;

-- discard_failed deletes events the platform rejected.
CREATE FUNCTION gregale_hooks.discard_failed() RETURNS bigint
LANGUAGE plpgsql SET search_path = pg_catalog, pg_temp AS $$
DECLARE
  n bigint;
BEGIN
  DELETE FROM {{schema}}.gregale_change_events WHERE failed_at IS NOT NULL;
  GET DIAGNOSTICS n = ROW_COUNT;
  RETURN n;
END
$$;
REVOKE ALL ON FUNCTION gregale_hooks.discard_failed() FROM PUBLIC;

-- The relay's claim and settlement run as the Data API login and RLS decides
-- what they touch. They carry no @gregale:rpc marker, so the gateway never
-- routes them, and they refuse callers without the relay's claim.
CREATE FUNCTION {{schema}}.gregale_change_claim(relay text, max_events integer, lease_ms integer)
RETURNS SETOF {{schema}}.gregale_change_events
LANGUAGE plpgsql SET search_path = pg_catalog, pg_temp AS $$
DECLARE
  blocked bigint;
  claimed bigint[];
BEGIN
  IF coalesce(nullif(current_setting('request.jwt.claims', true), '')::jsonb ->> 'gregale_access', '') <> 'hooks' THEN
    RAISE EXCEPTION 'change events are read only by the Data API relay' USING ERRCODE = '42501';
  END IF;
  IF relay IS NULL OR relay !~ '^[A-Za-z0-9._:-]{1,128}$' OR max_events IS NULL OR max_events NOT BETWEEN 1 AND 100
     OR lease_ms IS NULL OR lease_ms NOT BETWEEN 1000 AND 300000 THEN
    RAISE EXCEPTION 'invalid claim' USING ERRCODE = '22023';
  END IF;
  -- One relay publishes at a time, in event order.
  PERFORM pg_advisory_xact_lock(hashtextextended('gregale_change_events:{{schema}}', 0));
  IF EXISTS (SELECT 1 FROM {{schema}}.gregale_change_events
             WHERE claimed_by <> relay AND claimed_until > clock_timestamp() AND failed_at IS NULL) THEN
    RETURN;
  END IF;
  -- An event waiting to be retried holds back every later event.
  SELECT min(id) INTO blocked FROM (
    SELECT id, available_at FROM {{schema}}.gregale_change_events WHERE failed_at IS NULL ORDER BY id LIMIT max_events
  ) head WHERE available_at > clock_timestamp();
  -- Lock first, then update: under RLS a locking IN subquery runs per row and
  -- would skip rows this statement already updated, claiming past the limit.
  SELECT array_agg(id ORDER BY id) INTO claimed FROM (
    SELECT id FROM {{schema}}.gregale_change_events
     WHERE failed_at IS NULL AND (blocked IS NULL OR id < blocked)
     ORDER BY id LIMIT max_events FOR UPDATE
  ) head;
  RETURN QUERY
  UPDATE {{schema}}.gregale_change_events e
     SET claimed_by = relay, claimed_until = clock_timestamp() + lease_ms * interval '1 millisecond', attempts = e.attempts + 1
   WHERE e.id = ANY (coalesce(claimed, '{}'))
  RETURNING e.*;
END
$$;
GRANT EXECUTE ON FUNCTION {{schema}}.gregale_change_claim(text, integer, integer) TO PUBLIC;

-- settle deletes published events, marks rejected ones failed and returns the
-- rest of the relay's claim, to wait retry_after_ms when it is positive.
CREATE FUNCTION {{schema}}.gregale_change_settle(relay text, published bigint[], failed jsonb, retry_after_ms integer)
RETURNS void LANGUAGE plpgsql SET search_path = pg_catalog, pg_temp AS $$
BEGIN
  IF coalesce(nullif(current_setting('request.jwt.claims', true), '')::jsonb ->> 'gregale_access', '') <> 'hooks' THEN
    RAISE EXCEPTION 'change events are settled only by the Data API relay' USING ERRCODE = '42501';
  END IF;
  IF relay IS NULL OR relay !~ '^[A-Za-z0-9._:-]{1,128}$' OR retry_after_ms IS NULL OR retry_after_ms NOT BETWEEN 0 AND 3600000
     OR jsonb_typeof(coalesce(failed, '[]')) <> 'array' THEN
    RAISE EXCEPTION 'invalid settlement' USING ERRCODE = '22023';
  END IF;
  DELETE FROM {{schema}}.gregale_change_events WHERE claimed_by = relay AND id = ANY (coalesce(published, '{}'));
  UPDATE {{schema}}.gregale_change_events e
     SET failed_at = clock_timestamp(), failure_code = left(f.code, 64), claimed_by = NULL, claimed_until = NULL
    FROM jsonb_to_recordset(coalesce(failed, '[]')) AS f(id bigint, code text)
   WHERE e.id = f.id AND e.claimed_by = relay;
  UPDATE {{schema}}.gregale_change_events
     SET claimed_by = NULL, claimed_until = NULL,
         available_at = CASE WHEN retry_after_ms > 0 THEN clock_timestamp() + retry_after_ms * interval '1 millisecond' ELSE available_at END
   WHERE claimed_by = relay;
END
$$;
GRANT EXECUTE ON FUNCTION {{schema}}.gregale_change_settle(text, bigint[], jsonb, integer) TO PUBLIC;

-- Hook your tables in this or a later migration. For example:
--
-- SELECT gregale_hooks.enable('{{schema}}.orders');
-- SELECT gregale_hooks.enable('{{schema}}.users', exclude => ARRAY['password_hash']);
-- SELECT gregale_hooks.enable('{{schema}}.audit_log', payload => 'keys');
--
-- Skip recording in one transaction, such as a backfill:
-- SET LOCAL gregale_hooks.skip = 'on';
