REVOKE ALL ON SCHEMA public FROM PUBLIC;
CREATE SCHEMA api;
CREATE TABLE api.notes(key text PRIMARY KEY, subject text NOT NULL, body text NOT NULL);
ALTER TABLE api.notes ENABLE ROW LEVEL SECURITY;
CREATE POLICY own_notes ON api.notes
  USING (subject = current_setting('request.jwt.claims', true)::jsonb->>'sub')
  WITH CHECK (subject = current_setting('request.jwt.claims', true)::jsonb->>'sub');
CREATE TABLE api.large_payload(subject text NOT NULL, body text NOT NULL);
ALTER TABLE api.large_payload ENABLE ROW LEVEL SECURITY;
CREATE POLICY own_payload ON api.large_payload USING (subject = current_setting('request.jwt.claims', true)::jsonb->>'sub');
CREATE SEQUENCE api.invocations;
GRANT USAGE ON SCHEMA api TO __ROLE__;
GRANT SELECT, INSERT ON api.notes TO __ROLE__;
GRANT SELECT ON api.large_payload TO __ROLE__;
GRANT USAGE, SELECT ON SEQUENCE api.invocations TO __ROLE__;

CREATE FUNCTION api.write_note(note_key text, payload text) RETURNS jsonb LANGUAGE plpgsql AS $$
DECLARE saved api.notes;
BEGIN
  PERFORM nextval('api.invocations');
  INSERT INTO api.notes VALUES(note_key, current_setting('request.jwt.claims', true)::jsonb->>'sub', payload)
    ON CONFLICT(key) DO NOTHING;
  SELECT * INTO saved FROM api.notes WHERE key = note_key;
  IF saved.key IS NULL OR saved.body <> payload THEN RAISE EXCEPTION USING ERRCODE = '23505', MESSAGE = 'Receipt mismatch'; END IF;
  RETURN to_jsonb(saved);
END $$;
CREATE FUNCTION api.visible_notes() RETURNS SETOF api.notes LANGUAGE sql STABLE AS $$ SELECT * FROM api.notes $$;
CREATE FUNCTION api.whoami() RETURNS jsonb LANGUAGE sql STABLE AS $$ SELECT jsonb_build_object('subject', current_setting('request.jwt.claims', true)::jsonb->>'sub') $$;
CREATE FUNCTION api.pause_ping() RETURNS jsonb LANGUAGE plpgsql AS $$
BEGIN PERFORM pg_sleep(0.5); RETURN jsonb_build_object('subject', current_setting('request.jwt.claims', true)::jsonb->>'sub'); END $$;
CREATE FUNCTION api.commit_large(note_key text) RETURNS jsonb LANGUAGE plpgsql AS $$
BEGIN
  PERFORM api.write_note(note_key, 'large-receipt');
  RETURN jsonb_build_object('payload', repeat('x', __RESPONSE_BYTES__ + 1));
END $$;
REVOKE ALL ON FUNCTION api.write_note(text,text), api.visible_notes(), api.whoami(), api.pause_ping(), api.commit_large(text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION api.write_note(text,text), api.visible_notes(), api.whoami(), api.pause_ping(), api.commit_large(text) TO __ROLE__;
COMMENT ON FUNCTION api.write_note(text,text) IS '@gregale:rpc';
COMMENT ON FUNCTION api.visible_notes() IS '@gregale:rpc';
COMMENT ON FUNCTION api.whoami() IS '@gregale:rpc';
COMMENT ON FUNCTION api.pause_ping() IS '@gregale:rpc';
COMMENT ON FUNCTION api.commit_large(text) IS '@gregale:rpc';
