CREATE TABLE IF NOT EXISTS api.load_notes (
  key text PRIMARY KEY, subject text NOT NULL, tenant text NOT NULL DEFAULT 'canary',
  body text NOT NULL, invocations integer NOT NULL DEFAULT 1
);
ALTER TABLE api.load_notes ENABLE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS own_load_notes ON api.load_notes;
CREATE POLICY own_load_notes ON api.load_notes
  USING (subject = current_setting('request.jwt.claims',true)::jsonb->>'sub' AND tenant = current_setting('request.jwt.claims',true)::jsonb->>'tenant')
  WITH CHECK (subject = current_setting('request.jwt.claims',true)::jsonb->>'sub' AND tenant = current_setting('request.jwt.claims',true)::jsonb->>'tenant');
CREATE OR REPLACE FUNCTION api.load_write(note_key text, payload text) RETURNS SETOF api.load_notes
LANGUAGE plpgsql SECURITY INVOKER SET search_path=pg_catalog AS $$
BEGIN
  INSERT INTO api.load_notes(key,subject,tenant,body)
    VALUES(note_key,current_setting('request.jwt.claims',true)::jsonb->>'sub',current_setting('request.jwt.claims',true)::jsonb->>'tenant',payload)
    ON CONFLICT(key) DO UPDATE SET invocations=api.load_notes.invocations+1
      WHERE api.load_notes.body=excluded.body;
  IF NOT FOUND THEN RAISE EXCEPTION USING ERRCODE='23505', MESSAGE='Receipt mismatch'; END IF;
  RETURN QUERY SELECT * FROM api.load_notes WHERE key=note_key;
END $$;
CREATE OR REPLACE FUNCTION api.load_visible() RETURNS SETOF api.load_notes
LANGUAGE sql STABLE SECURITY INVOKER AS $$ SELECT * FROM api.load_notes $$;
CREATE OR REPLACE FUNCTION api.load_ping() RETURNS jsonb LANGUAGE sql STABLE SECURITY INVOKER AS $$
  SELECT jsonb_build_object('subject',current_setting('request.jwt.claims',true)::jsonb->>'sub','tenant',current_setting('request.jwt.claims',true)::jsonb->>'tenant')
$$;
CREATE OR REPLACE FUNCTION api.load_pause() RETURNS jsonb LANGUAGE plpgsql SECURITY INVOKER AS $$
BEGIN PERFORM pg_sleep(3); RETURN api.load_ping(); END $$;
REVOKE ALL ON FUNCTION api.load_write(text,text),api.load_visible(),api.load_ping(),api.load_pause() FROM PUBLIC;
COMMENT ON FUNCTION api.load_write(text,text) IS '@gregale:rpc';
COMMENT ON FUNCTION api.load_visible() IS '@gregale:rpc';
COMMENT ON FUNCTION api.load_ping() IS '@gregale:rpc';
COMMENT ON FUNCTION api.load_pause() IS '@gregale:rpc';
