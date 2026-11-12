SET LOCAL lock_timeout = '5s';

-- Optional owner maintenance. Apply after storage.sql and optional sessions.
-- No serving-login grants: inventory, leases and receipts stay private.
CREATE SCHEMA IF NOT EXISTS gregale_storage_maintenance;
REVOKE ALL ON SCHEMA gregale_storage_maintenance FROM PUBLIC;
CREATE TABLE IF NOT EXISTS gregale_storage_maintenance.objects (
 relation_schema text NOT NULL, bucket text NOT NULL, object_key text NOT NULL,
 etag text NOT NULL, modified_at timestamptz NOT NULL,
 state text NOT NULL DEFAULT 'waiting' CHECK(state IN ('waiting','deleting','deleted','referenced')),
 attempts integer NOT NULL DEFAULT 0, next_attempt_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 lease uuid, leased_until timestamptz, deleted_at timestamptz, last_error text,
 PRIMARY KEY(relation_schema,bucket,object_key),
 CHECK(object_key ~ '^_gregale/objects/[0-9a-f]{32}$')
);
CREATE INDEX IF NOT EXISTS objects_cleanup_due ON gregale_storage_maintenance.objects(relation_schema,next_attempt_at) WHERE state IN ('waiting','deleting');
CREATE TABLE IF NOT EXISTS gregale_storage_maintenance.scans (
 relation_schema text NOT NULL,bucket text NOT NULL,continuation text,
 PRIMARY KEY(relation_schema,bucket)
);
REVOKE ALL ON ALL TABLES IN SCHEMA gregale_storage_maintenance FROM PUBLIC;

-- Serialize reference creation with the collector's final reference check.
-- A durable deleting fence remains after the SQL transaction commits and while
-- an external object DELETE is pending or its response has been lost.
CREATE OR REPLACE FUNCTION gregale_storage_maintenance.guard_reference() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
DECLARE k text; keys text[];
BEGIN
 IF TG_TABLE_NAME='gregale_storage_uploads' THEN
  IF NEW.state NOT IN ('uploading','finishing') OR NEW.expires_at<=clock_timestamp() THEN RETURN NEW; END IF;
  SELECT array_agg(key ORDER BY key) INTO keys FROM (
   SELECT NEW.object_key AS key UNION SELECT value->>'key' FROM jsonb_each(NEW.parts)
  ) s WHERE key IS NOT NULL;
 ELSE
  keys:=ARRAY[NEW.object_key];
 END IF;
 FOREACH k IN ARRAY coalesce(keys,'{}') LOOP
  IF k IS NULL THEN CONTINUE; END IF;
  PERFORM pg_advisory_xact_lock(hashtextextended('gregale:storage-key:'||TG_TABLE_SCHEMA||':'||NEW.bucket||':'||k,0));
  IF EXISTS(SELECT 1 FROM gregale_storage_maintenance.objects WHERE relation_schema=TG_TABLE_SCHEMA AND bucket=NEW.bucket AND object_key=k AND state IN ('deleting','deleted')) THEN
   RAISE SQLSTATE 'PT409' USING MESSAGE='storage_object_retired';
  END IF;
 END LOOP;
 RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION gregale_storage_maintenance.guard_reference() FROM PUBLIC;
DROP TRIGGER IF EXISTS gregale_cleanup_reference_guard ON {{schema}}.storage_objects;
CREATE TRIGGER gregale_cleanup_reference_guard BEFORE INSERT OR UPDATE ON {{schema}}.storage_objects
 FOR EACH ROW EXECUTE FUNCTION gregale_storage_maintenance.guard_reference();
DO $$ BEGIN
 IF to_regclass('{{schema}}.gregale_storage_uploads') IS NOT NULL THEN
  EXECUTE 'DROP TRIGGER IF EXISTS gregale_cleanup_reference_guard ON {{schema}}.gregale_storage_uploads';
  EXECUTE 'CREATE TRIGGER gregale_cleanup_reference_guard BEFORE INSERT OR UPDATE ON {{schema}}.gregale_storage_uploads FOR EACH ROW EXECUTE FUNCTION gregale_storage_maintenance.guard_reference()';
 END IF;
END $$;
