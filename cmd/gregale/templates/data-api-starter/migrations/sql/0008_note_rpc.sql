-- The caller supplies content, never an ownership subject. RLS applies to
-- every statement because the function runs with the restricted login's rights.
CREATE FUNCTION api.create_note_with_tags(note_body text, tag_names text[] DEFAULT '{}')
RETURNS SETOF api.notes
LANGUAGE plpgsql SECURITY INVOKER SET search_path = pg_catalog
AS $$
DECLARE
  caller text := current_setting('request.jwt.claims', true)::jsonb ->> 'sub';
  created api.notes;
  label text;
  tag_id integer;
BEGIN
  INSERT INTO api.notes(subject, body) VALUES (caller, note_body) RETURNING * INTO created;
  FOREACH label IN ARRAY tag_names LOOP
    INSERT INTO api.tags(subject, name) VALUES (caller, label) RETURNING id INTO tag_id;
    INSERT INTO api.note_tags(subject, note_id, tag_id) VALUES (caller, created.id, tag_id);
  END LOOP;
  RETURN NEXT created;
END;
$$;
REVOKE ALL ON FUNCTION api.create_note_with_tags(text, text[]) FROM PUBLIC;
-- The migration owner grants EXECUTE only to this app's data_api binding login
-- after applying this migration. No database role name is baked into source.
COMMENT ON FUNCTION api.create_note_with_tags(text, text[]) IS '@gregale:rpc';
