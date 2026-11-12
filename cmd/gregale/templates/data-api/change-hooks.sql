-- Data API change hooks (ADR-20261022121642). Apply as the schema owner in a
-- migration. `gregale data-api hooks sql` prints this with your schema.
-- A trigger on each hooked table records its changes in
-- {{schema}}.gregale_change_events inside the writing transaction. The Data
-- API publishes them as Gregale events, in order, and deletes them.

CREATE TABLE IF NOT EXISTS {{schema}}.gregale_change_events (
  id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  event_key uuid NOT NULL DEFAULT gen_random_uuid() UNIQUE,
  relation_schema text NOT NULL,
  relation_name text NOT NULL,
  op text NOT NULL CHECK (op IN ('insert', 'update', 'delete')),
  row_key jsonb NOT NULL,
  old_row jsonb,
  new_row jsonb,
  changed text[],
  truncated boolean NOT NULL DEFAULT false,
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
-- Waiting changes in order: the relay's claim and capture's backlog bound.
CREATE INDEX IF NOT EXISTS gregale_change_events_pending ON {{schema}}.gregale_change_events (id) WHERE failed_at IS NULL;

-- Only the Data API's relay reads and settles events: it adds the
-- gateway-owned gregale_access = 'hooks' claim, which the gateway never gives
-- a client request. Hooked writes insert as the owner, which RLS skips.
DROP POLICY IF EXISTS gregale_change_relay ON {{schema}}.gregale_change_events;
CREATE POLICY gregale_change_relay ON {{schema}}.gregale_change_events
  USING (coalesce(nullif(current_setting('request.jwt.claims', true), '')::jsonb ->> 'gregale_access', '') = 'hooks')
  WITH CHECK (coalesce(nullif(current_setting('request.jwt.claims', true), '')::jsonb ->> 'gregale_access', '') = 'hooks');

CREATE SCHEMA IF NOT EXISTS gregale_hooks;
REVOKE ALL ON SCHEMA gregale_hooks FROM PUBLIC;

-- enable hooks a table with a primary key, or changes its options.
CREATE OR REPLACE FUNCTION gregale_hooks.enable(relation regclass, payload text DEFAULT 'row', exclude text[] DEFAULT '{}')
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
CREATE OR REPLACE FUNCTION gregale_hooks.disable(relation regclass) RETURNS void
LANGUAGE plpgsql SET search_path = pg_catalog, pg_temp AS $$
BEGIN
  EXECUTE format('DROP TRIGGER IF EXISTS gregale_change_hook ON %s', relation);
END
$$;
REVOKE ALL ON FUNCTION gregale_hooks.disable(regclass) FROM PUBLIC;

-- gregale:upgrade-begin. `gregale data-api hooks export --upgrade` replays
-- everything from here to gregale:upgrade-end on an existing installation, so
-- each statement here can run again.

ALTER TABLE {{schema}}.gregale_change_events ADD COLUMN IF NOT EXISTS schema_version text
  CHECK (schema_version ~ '^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$');

-- The schema owner selects the version for future changes. Capture freezes it
-- on each outbox row, so changing the setting cannot reinterpret a retry.
CREATE TABLE IF NOT EXISTS gregale_hooks.schema_versions (
  relation regclass PRIMARY KEY,
  schema_version text NOT NULL CHECK (schema_version ~ '^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$')
);
REVOKE ALL ON gregale_hooks.schema_versions FROM PUBLIC;

CREATE OR REPLACE FUNCTION gregale_hooks.set_schema_version(target regclass, version text)
RETURNS void LANGUAGE plpgsql SET search_path = pg_catalog, pg_temp AS $$
BEGIN
  IF version IS NOT NULL AND version !~ '^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$' THEN
    RAISE EXCEPTION 'invalid event schema version' USING ERRCODE = '22023';
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgrelid=target AND tgname='gregale_change_hook') THEN
    RAISE EXCEPTION 'table has no change hook' USING ERRCODE = '22023';
  END IF;
  IF version IS NULL THEN
    DELETE FROM gregale_hooks.schema_versions WHERE relation=target;
  ELSE
    INSERT INTO gregale_hooks.schema_versions(relation,schema_version) VALUES(target,version)
    ON CONFLICT(relation) DO UPDATE SET schema_version=excluded.schema_version;
  END IF;
END
$$;
REVOKE ALL ON FUNCTION gregale_hooks.set_schema_version(regclass, text) FROM PUBLIC;


-- gregale_change_drops notes when capture last left a change out at the
-- backlog bound (ADR-20261022121642 amendment 3); the relay reports it, so
-- owners see that changes went unrecorded. Like the event table, only the
-- relay's claim reads it; capture writes it as the owner, which RLS skips.
CREATE TABLE IF NOT EXISTS {{schema}}.gregale_change_drops (
  singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
  last_dropped_at timestamptz
);
ALTER TABLE {{schema}}.gregale_change_drops ENABLE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS gregale_change_relay ON {{schema}}.gregale_change_drops;
CREATE POLICY gregale_change_relay ON {{schema}}.gregale_change_drops FOR SELECT
  USING (coalesce(nullif(current_setting('request.jwt.claims', true), '')::jsonb ->> 'gregale_access', '') = 'hooks');
INSERT INTO {{schema}}.gregale_change_drops (singleton) VALUES (true) ON CONFLICT DO NOTHING;

-- capture records one change. Its arguments are the key columns, the payload
-- ('row' or 'keys'), excluded columns and the hooked relation's name. Rows
-- larger than 64 KiB together are left out and the event marked truncated;
-- consumers read the row by its key.
--
-- gregale:backlog-bound (amendment 2). While {{backlog_max}} changes wait to
-- publish, because the platform does not take them (a stage, an installation
-- without workload identity) or publishing keeps failing, new changes are not
-- recorded: the table cannot grow until it fills the database. The bound
-- counts identifiers from the oldest waiting change, so it errs early.
--
-- gregale:drop-tracking (amendment 3). A change left out is noted in
-- {{schema}}.gregale_change_drops, at most once a second and never waiting for another
-- writer's note.
CREATE OR REPLACE FUNCTION gregale_hooks.capture() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, pg_temp AS $$
DECLARE
  key_columns text[] := TG_ARGV[0]::text[];
  excluded text[] := TG_ARGV[2]::text[];
  source jsonb;
  old_row jsonb;
  new_row jsonb;
  changed text[];
  row_key jsonb;
  truncated boolean := false;
  newest bigint;
  oldest bigint;
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
  ELSIF coalesce(octet_length(old_row::text), 0) + coalesce(octet_length(new_row::text), 0) > 65536 THEN
    old_row := NULL;
    new_row := NULL;
    truncated := true;
  END IF;
  -- gregale:backlog-bound
  newest := pg_sequence_last_value(pg_get_serial_sequence('{{schema}}.gregale_change_events', 'id')::regclass);
  SELECT id INTO oldest FROM {{schema}}.gregale_change_events WHERE failed_at IS NULL ORDER BY id LIMIT 1;
  IF newest - oldest + 1 >= {{backlog_max}} THEN
    -- gregale:drop-tracking
    PERFORM 1 FROM {{schema}}.gregale_change_drops
      WHERE singleton AND (last_dropped_at IS NULL OR last_dropped_at < clock_timestamp() - interval '1 second')
      FOR UPDATE SKIP LOCKED;
    IF FOUND THEN
      UPDATE {{schema}}.gregale_change_drops SET last_dropped_at = clock_timestamp() WHERE singleton;
    END IF;
    RETURN NULL;
  END IF;
  -- Partition trigger arguments identify the configured parent source.
  INSERT INTO {{schema}}.gregale_change_events (relation_schema, relation_name, op, row_key, old_row, new_row, changed, truncated, schema_version)
  VALUES (TG_ARGV[3], TG_ARGV[4], lower(TG_OP), row_key, old_row, new_row, changed, truncated,
    (SELECT schema_version FROM gregale_hooks.schema_versions
      WHERE relation=coalesce(to_regclass(format('%I.%I', TG_ARGV[3], TG_ARGV[4])), TG_RELID::regclass)));
  RETURN NULL;
END
$$;
REVOKE ALL ON FUNCTION gregale_hooks.capture() FROM PUBLIC;

-- retry_failed queues events the platform rejected again, under new
-- identifiers in their old order: they publish after newer events of the same
-- rows (each keeps its event id, time and transaction; data.sequence is new), and
-- the backlog bound, which counts identifiers from the oldest waiting event,
-- does not count every change recorded since they failed.
CREATE OR REPLACE FUNCTION gregale_hooks.retry_failed() RETURNS bigint
LANGUAGE plpgsql SET search_path = pg_catalog, pg_temp AS $$
DECLARE
  n bigint;
BEGIN
  WITH moved AS (
    DELETE FROM {{schema}}.gregale_change_events WHERE failed_at IS NOT NULL
    RETURNING id, event_key, relation_schema, relation_name, op, row_key, old_row, new_row, changed, truncated, txid, captured_at, schema_version
  )
  INSERT INTO {{schema}}.gregale_change_events (event_key, relation_schema, relation_name, op, row_key, old_row, new_row, changed, truncated, txid, captured_at, schema_version)
  SELECT event_key, relation_schema, relation_name, op, row_key, old_row, new_row, changed, truncated, txid, captured_at, schema_version FROM moved ORDER BY id;
  GET DIAGNOSTICS n = ROW_COUNT;
  RETURN n;
END
$$;
REVOKE ALL ON FUNCTION gregale_hooks.retry_failed() FROM PUBLIC;

-- discard_failed deletes events the platform rejected.
CREATE OR REPLACE FUNCTION gregale_hooks.discard_failed() RETURNS bigint
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

-- discard_waiting deletes every change still waiting to publish, such as a
-- backlog at the bound that will never publish, and clears the drop note.
-- Recording resumes with the next change.
CREATE OR REPLACE FUNCTION gregale_hooks.discard_waiting() RETURNS bigint
LANGUAGE plpgsql SET search_path = pg_catalog, pg_temp AS $$
DECLARE
  n bigint;
BEGIN
  DELETE FROM {{schema}}.gregale_change_events WHERE failed_at IS NULL;
  GET DIAGNOSTICS n = ROW_COUNT;
  UPDATE {{schema}}.gregale_change_drops SET last_dropped_at = NULL WHERE singleton;
  RETURN n;
END
$$;
REVOKE ALL ON FUNCTION gregale_hooks.discard_waiting() FROM PUBLIC;

-- gregale:upgrade-end

-- The relay's claim and settlement run as the Data API login and RLS decides
-- what they touch. They carry no @gregale:rpc marker, so the gateway never
-- routes them, and they refuse callers without the relay's claim.
CREATE OR REPLACE FUNCTION {{schema}}.gregale_change_claim(relay text, max_events integer, lease_ms integer)
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
CREATE OR REPLACE FUNCTION {{schema}}.gregale_change_settle(relay text, published bigint[], failed jsonb, retry_after_ms integer)
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
