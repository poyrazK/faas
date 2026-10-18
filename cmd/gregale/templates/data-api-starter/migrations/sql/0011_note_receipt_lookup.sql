-- The lookup has SELECT access only, while the existing writer's policy stays
-- unchanged. Scope is transaction-local and restored on success or error.
CREATE POLICY read_own_note_create_receipts ON api.note_create_receipts FOR SELECT
  USING (subject = current_setting('request.jwt.claims', true)::jsonb ->> 'sub'
    AND current_setting('gregale.idempotency_rpc', true) = 'read_note_create_receipt');

CREATE FUNCTION api.read_note_create_receipt(idempotency_key uuid, note_body text, tag_names text[] DEFAULT '{}')
RETURNS SETOF api.notes
LANGUAGE plpgsql STABLE SECURITY INVOKER SET search_path = pg_catalog
AS $$
DECLARE
  caller text := NULLIF(current_setting('request.jwt.claims', true)::jsonb ->> 'sub', '');
  input jsonb := jsonb_build_object('note_body', note_body, 'tag_names', tag_names);
  previous_scope text := current_setting('gregale.idempotency_rpc', true);
  saved api.note_create_receipts;
BEGIN
  IF caller IS NULL THEN
    RAISE SQLSTATE '42501' USING MESSAGE = 'application subject required';
  END IF;
  IF idempotency_key IS NULL THEN
    RAISE SQLSTATE '22023' USING MESSAGE = 'idempotency key required';
  END IF;
  PERFORM set_config('gregale.idempotency_rpc', 'read_note_create_receipt', true);
  SELECT r.* INTO saved FROM api.note_create_receipts r
    WHERE r.subject = caller AND r.request_key = idempotency_key;
  IF FOUND THEN
    IF saved.request IS DISTINCT FROM input THEN
      RAISE SQLSTATE 'PT409' USING MESSAGE = 'idempotency_payload_conflict';
    END IF;
    RETURN NEXT jsonb_populate_record(NULL::api.notes, saved.response);
  END IF;
  PERFORM set_config('gregale.idempotency_rpc', coalesce(previous_scope, ''), true);
  RETURN;
EXCEPTION WHEN OTHERS THEN
  PERFORM set_config('gregale.idempotency_rpc', coalesce(previous_scope, ''), true);
  RAISE;
END;
$$;
REVOKE ALL ON FUNCTION api.read_note_create_receipt(uuid, text, text[]) FROM PUBLIC;
COMMENT ON FUNCTION api.read_note_create_receipt(uuid, text, text[]) IS '@gregale:rpc';
