-- Receipts use the binding's api schema, but RLS permits access only inside
-- this function. Their lifetime defines the safe replay window.
CREATE TABLE api.note_create_receipts (
  subject text NOT NULL,
  request_key uuid NOT NULL,
  request jsonb NOT NULL CHECK (jsonb_typeof(request) = 'object'),
  response jsonb NOT NULL CHECK (jsonb_typeof(response) = 'object'),
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (subject, request_key)
);
ALTER TABLE api.note_create_receipts ENABLE ROW LEVEL SECURITY;
CREATE POLICY own_note_create_receipts ON api.note_create_receipts
  USING (subject = current_setting('request.jwt.claims', true)::jsonb ->> 'sub'
    AND current_setting('gregale.idempotency_rpc', true) = 'create_note_with_tags_once')
  WITH CHECK (subject = current_setting('request.jwt.claims', true)::jsonb ->> 'sub'
    AND current_setting('gregale.idempotency_rpc', true) = 'create_note_with_tags_once');

CREATE FUNCTION api.create_note_with_tags_once(idempotency_key uuid, note_body text, tag_names text[] DEFAULT '{}')
RETURNS SETOF api.notes
LANGUAGE plpgsql SECURITY INVOKER SET search_path = pg_catalog
SET default_transaction_isolation = 'read committed'
AS $$
DECLARE
  caller text := current_setting('request.jwt.claims', true)::jsonb ->> 'sub';
  input jsonb := jsonb_build_object('note_body', note_body, 'tag_names', tag_names);
  previous_scope text := current_setting('gregale.idempotency_rpc', true);
  saved api.note_create_receipts;
  created api.notes;
BEGIN
  IF caller IS NULL OR caller = '' THEN
    RAISE EXCEPTION 'application subject required' USING ERRCODE = '42501';
  END IF;
  IF idempotency_key IS NULL THEN
    RAISE EXCEPTION 'idempotency key required' USING ERRCODE = '22023';
  END IF;
  PERFORM set_config('gregale.idempotency_rpc', 'create_note_with_tags_once', true);
  -- Transaction-scoped serialization. Hash collisions only serialize unrelated
  -- requests; the subject/key primary key remains the identity authority.
  PERFORM pg_advisory_xact_lock(hashtextextended(
    jsonb_build_array('gregale.note_create', caller, idempotency_key)::text, 0));
  SELECT r.* INTO saved FROM api.note_create_receipts r
    WHERE r.subject = caller AND r.request_key = idempotency_key;
  IF FOUND THEN
    IF saved.request IS DISTINCT FROM input THEN
      RAISE EXCEPTION 'idempotency_conflict' USING ERRCODE = 'PT409';
    END IF;
    PERFORM set_config('gregale.idempotency_rpc', coalesce(previous_scope, ''), true);
    RETURN NEXT jsonb_populate_record(NULL::api.notes, saved.response);
    RETURN;
  END IF;
  SELECT n.* INTO STRICT created FROM api.create_note_with_tags(note_body, tag_names) n;
  INSERT INTO api.note_create_receipts(subject, request_key, request, response)
    VALUES (caller, idempotency_key, input, to_jsonb(created));
  PERFORM set_config('gregale.idempotency_rpc', coalesce(previous_scope, ''), true);
  RETURN NEXT created;
EXCEPTION WHEN OTHERS THEN
  PERFORM set_config('gregale.idempotency_rpc', coalesce(previous_scope, ''), true);
  RAISE;
END;
$$;
REVOKE ALL ON FUNCTION api.create_note_with_tags_once(uuid, text, text[]) FROM PUBLIC;
COMMENT ON FUNCTION api.create_note_with_tags_once(uuid, text, text[]) IS '@gregale:rpc';
-- The migration owner grants EXECUTE on this function and the original
-- create_note_with_tags to the binding login before refresh/type export.
-- The existing api table grants apply; RLS denies direct receipt access.
-- The function restores its scope on return; errors roll back local settings.
