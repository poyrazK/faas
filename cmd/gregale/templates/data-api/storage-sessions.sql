-- Optional durable upload sessions. Apply after storage.sql as schema owner.
-- Grant the serving login SELECT/INSERT/UPDATE/DELETE on this table and
-- EXECUTE on these invoker functions. They are internal, never @gregale:rpc.
CREATE TABLE {{schema}}.gregale_storage_uploads (
  id uuid PRIMARY KEY,
  actor text NOT NULL DEFAULT nullif(current_setting('request.jwt.claims', true), '')::jsonb->>'gregale_storage_actor',
  bucket text NOT NULL,
  name text NOT NULL CHECK (octet_length(name) BETWEEN 1 AND 1024),
  size bigint NOT NULL CHECK (size BETWEEN 0 AND 104857600),
  content_type text NOT NULL,
  object_key text NOT NULL UNIQUE CHECK (object_key ~ '^_gregale/objects/[0-9a-f]{32}$'),
  expected_version text CHECK (expected_version = 'absent' OR expected_version ~ '^[0-9a-f]{32}$'),
  parts jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(parts) = 'object'),
  state text NOT NULL DEFAULT 'uploading' CHECK (state IN ('uploading','finishing','complete','aborted')),
  receipt jsonb,
  created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  expires_at timestamptz NOT NULL DEFAULT clock_timestamp() + interval '24 hours',
  CHECK ((state = 'complete') = (receipt IS NOT NULL))
);
ALTER TABLE {{schema}}.gregale_storage_uploads ENABLE ROW LEVEL SECURITY;
CREATE POLICY upload_actor ON {{schema}}.gregale_storage_uploads
 USING (actor = nullif(current_setting('request.jwt.claims', true), '')::jsonb->>'gregale_storage_actor'
   AND nullif(current_setting('request.jwt.claims', true), '')::jsonb->>'gregale_storage' = 'write')
 WITH CHECK (actor = nullif(current_setting('request.jwt.claims', true), '')::jsonb->>'gregale_storage_actor'
   AND nullif(current_setting('request.jwt.claims', true), '')::jsonb->>'gregale_storage' = 'write');

CREATE FUNCTION {{schema}}.gregale_storage_start(p_id uuid, p_bucket text, p_name text, p_size bigint, p_content_type text, p_key text, p_expected text DEFAULT NULL)
RETURNS jsonb LANGUAGE plpgsql SECURITY INVOKER SET search_path=pg_catalog AS $$
DECLARE s {{schema}}.gregale_storage_uploads;
BEGIN
  PERFORM pg_advisory_xact_lock(hashtextextended('storage:' || coalesce(current_setting('request.jwt.claims', true)::jsonb->>'gregale_storage_actor',''), 0));
  SELECT * INTO s FROM {{schema}}.gregale_storage_uploads WHERE id=p_id FOR UPDATE;
  IF FOUND THEN
    IF (s.bucket,s.name,s.size,s.content_type,s.expected_version) IS DISTINCT FROM (p_bucket,p_name,p_size,p_content_type,p_expected) THEN RAISE SQLSTATE 'PT409' USING MESSAGE='storage_session_conflict'; END IF;
    RETURN to_jsonb(s);
  END IF;
  IF (SELECT count(*) FROM {{schema}}.gregale_storage_uploads WHERE state IN ('uploading','finishing') AND expires_at>clock_timestamp()) >= 16 THEN RAISE SQLSTATE 'PT429' USING MESSAGE='storage_session_limit'; END IF;
  INSERT INTO {{schema}}.gregale_storage_uploads(id,bucket,name,size,content_type,object_key,expected_version)
    VALUES(p_id,p_bucket,p_name,p_size,p_content_type,p_key,p_expected) RETURNING * INTO s;
  RETURN to_jsonb(s);
END $$;

CREATE FUNCTION {{schema}}.gregale_storage_part(p_id uuid, p_number integer, p_size integer, p_sha256 text, p_key text)
RETURNS jsonb LANGUAGE plpgsql SECURITY INVOKER SET search_path=pg_catalog AS $$
DECLARE s {{schema}}.gregale_storage_uploads; part jsonb;
BEGIN
  SELECT * INTO s FROM {{schema}}.gregale_storage_uploads WHERE id=p_id FOR UPDATE;
  IF NOT FOUND OR s.state<>'uploading' OR s.expires_at<=clock_timestamp() THEN RAISE SQLSTATE 'PT409' USING MESSAGE='storage_session_unavailable'; END IF;
  IF p_number<1 OR p_number>20 OR p_number>ceil(s.size::numeric/5242880) OR p_size<>least(5242880,s.size-(p_number-1)*5242880)
    OR p_sha256 !~ '^[0-9a-f]{64}$' OR p_key !~ '^_gregale/objects/[0-9a-f]{32}$' THEN RAISE SQLSTATE 'PT400' USING MESSAGE='storage_invalid_part'; END IF;
  part := s.parts->p_number::text;
  IF part IS NOT NULL THEN
    IF part->>'sha256'<>p_sha256 OR (part->>'size')::integer<>p_size THEN RAISE SQLSTATE 'PT409' USING MESSAGE='storage_part_conflict'; END IF;
  ELSE
    UPDATE {{schema}}.gregale_storage_uploads SET parts=parts || jsonb_build_object(p_number::text,jsonb_build_object('size',p_size,'sha256',p_sha256,'key',p_key)) WHERE id=p_id RETURNING * INTO s;
  END IF;
  RETURN to_jsonb(s);
END $$;

CREATE FUNCTION {{schema}}.gregale_storage_prepare(p_id uuid) RETURNS jsonb
LANGUAGE plpgsql SECURITY INVOKER SET search_path=pg_catalog AS $$
DECLARE s {{schema}}.gregale_storage_uploads;
BEGIN
  SELECT * INTO s FROM {{schema}}.gregale_storage_uploads WHERE id=p_id FOR UPDATE;
  IF NOT FOUND OR s.state='aborted' OR (s.state<>'complete' AND s.expires_at<=clock_timestamp()) THEN RAISE SQLSTATE 'PT409' USING MESSAGE='storage_session_unavailable'; END IF;
  IF s.state='uploading' THEN
    IF (SELECT count(*) FROM jsonb_object_keys(s.parts))<>ceil(s.size::numeric/5242880) THEN RAISE SQLSTATE 'PT409' USING MESSAGE='storage_parts_missing'; END IF;
    UPDATE {{schema}}.gregale_storage_uploads SET state='finishing' WHERE id=p_id RETURNING * INTO s;
  END IF;
  RETURN to_jsonb(s);
END $$;

-- Compare-and-swap runs under PostgreSQL row locks, together with normal RLS.
CREATE FUNCTION {{schema}}.gregale_storage_put(p_row jsonb, p_expected text DEFAULT NULL) RETURNS jsonb
LANGUAGE plpgsql SECURITY INVOKER SET search_path=pg_catalog AS $$
DECLARE current_row {{schema}}.storage_objects; result {{schema}}.storage_objects;
BEGIN
  IF p_expected IS NOT NULL THEN
    IF p_expected<>'absent' AND p_expected !~ '^[0-9a-f]{32}$' THEN RAISE SQLSTATE 'PT400' USING MESSAGE='storage_invalid_version'; END IF;
    SELECT * INTO current_row FROM {{schema}}.storage_objects WHERE bucket=p_row->>'bucket' AND name=p_row->>'name' FOR UPDATE;
    IF p_expected='absent' THEN
      INSERT INTO {{schema}}.storage_objects(bucket,name,size,content_type,object_key)
        VALUES(p_row->>'bucket',p_row->>'name',(p_row->>'size')::bigint,p_row->>'content_type',p_row->>'object_key')
        ON CONFLICT DO NOTHING RETURNING * INTO result;
      IF result.bucket IS NULL THEN RAISE SQLSTATE 'PT412' USING MESSAGE='storage_version_conflict'; END IF;
    ELSE
      IF current_row.object_key IS DISTINCT FROM '_gregale/objects/' || p_expected THEN RAISE SQLSTATE 'PT412' USING MESSAGE='storage_version_conflict'; END IF;
      UPDATE {{schema}}.storage_objects SET size=(p_row->>'size')::bigint,content_type=p_row->>'content_type',object_key=p_row->>'object_key'
        WHERE bucket=current_row.bucket AND name=current_row.name RETURNING * INTO result;
      IF result.bucket IS NULL THEN RAISE SQLSTATE '42501' USING MESSAGE='storage_forbidden'; END IF;
    END IF;
  ELSE
    INSERT INTO {{schema}}.storage_objects(bucket,name,size,content_type,object_key)
      VALUES(p_row->>'bucket',p_row->>'name',(p_row->>'size')::bigint,p_row->>'content_type',p_row->>'object_key')
      ON CONFLICT(bucket,name) DO UPDATE SET size=EXCLUDED.size,content_type=EXCLUDED.content_type,object_key=EXCLUDED.object_key RETURNING * INTO result;
  END IF;
  RETURN to_jsonb(result);
END $$;

CREATE FUNCTION {{schema}}.gregale_storage_finish(p_id uuid) RETURNS jsonb
LANGUAGE plpgsql SECURITY INVOKER SET search_path=pg_catalog AS $$
DECLARE s {{schema}}.gregale_storage_uploads; result jsonb;
BEGIN
  SELECT * INTO s FROM {{schema}}.gregale_storage_uploads WHERE id=p_id FOR UPDATE;
  IF NOT FOUND OR s.state NOT IN ('finishing','complete') OR (s.state<>'complete' AND s.expires_at<=clock_timestamp()) THEN RAISE SQLSTATE 'PT409' USING MESSAGE='storage_session_unavailable'; END IF;
  IF s.state='complete' THEN RETURN to_jsonb(s); END IF;
  result := {{schema}}.gregale_storage_put(jsonb_build_object('bucket',s.bucket,'name',s.name,'size',s.size,'content_type',s.content_type,'object_key',s.object_key),s.expected_version);
  UPDATE {{schema}}.gregale_storage_uploads SET state='complete',receipt=result WHERE id=p_id RETURNING * INTO s;
  RETURN to_jsonb(s);
END $$;

CREATE FUNCTION {{schema}}.gregale_storage_abort(p_id uuid) RETURNS jsonb
LANGUAGE plpgsql SECURITY INVOKER SET search_path=pg_catalog AS $$
DECLARE s {{schema}}.gregale_storage_uploads;
BEGIN
  SELECT * INTO s FROM {{schema}}.gregale_storage_uploads WHERE id=p_id FOR UPDATE;
  IF NOT FOUND OR s.state='complete' THEN RAISE SQLSTATE 'PT409' USING MESSAGE='storage_session_unavailable'; END IF;
  UPDATE {{schema}}.gregale_storage_uploads SET state='aborted' WHERE id=p_id RETURNING * INTO s;
  RETURN to_jsonb(s);
END $$;
REVOKE ALL ON FUNCTION {{schema}}.gregale_storage_start(uuid,text,text,bigint,text,text,text), {{schema}}.gregale_storage_part(uuid,integer,integer,text,text), {{schema}}.gregale_storage_prepare(uuid), {{schema}}.gregale_storage_put(jsonb,text), {{schema}}.gregale_storage_finish(uuid), {{schema}}.gregale_storage_abort(uuid) FROM PUBLIC;
