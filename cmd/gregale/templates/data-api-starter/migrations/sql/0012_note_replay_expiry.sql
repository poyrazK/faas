-- Opt-in v2 namespace. Fixed limits mirror DataAPIReplayV2* in pkg/api/limits.go
-- (ADR-1670). Changing these values requires a new contract, not editing v2.
CREATE TABLE api.note_create_receipts_v2 (
  subject text NOT NULL,
  request_key uuid NOT NULL CHECK (
    substr(request_key::text, 15, 1) = '7' AND substr(request_key::text, 20, 1) IN ('8','9','a','b')),
  request jsonb NOT NULL CHECK (jsonb_typeof(request) = 'object'),
  response jsonb NOT NULL CHECK (jsonb_typeof(response) = 'object'),
  created_at timestamptz NOT NULL DEFAULT now(),
  expires_at timestamptz GENERATED ALWAYS AS (
    to_timestamp(((('x' || substr(replace(request_key::text, '-', ''), 1, 12))::bit(48)::bigint
      + 86400000)::double precision) / 1000)) STORED,
  PRIMARY KEY (subject, request_key)
);
CREATE INDEX note_create_receipts_v2_expiry_idx ON api.note_create_receipts_v2 (expires_at, subject, request_key);
ALTER TABLE api.note_create_receipts_v2 ENABLE ROW LEVEL SECURITY;
CREATE POLICY read_own_note_receipts_v2 ON api.note_create_receipts_v2 FOR SELECT
  USING (subject = current_setting('request.jwt.claims', true)::jsonb ->> 'sub'
    AND current_setting('gregale.idempotency_rpc', true) IN ('create_note_with_tags_once_v2','read_note_create_receipt_v2'));
CREATE POLICY insert_own_note_receipts_v2 ON api.note_create_receipts_v2 FOR INSERT
  WITH CHECK (subject = current_setting('request.jwt.claims', true)::jsonb ->> 'sub'
    AND current_setting('gregale.idempotency_rpc', true) = 'create_note_with_tags_once_v2');
-- No application UPDATE/DELETE policy. Also guard accidental early owner purges
-- and edits. Owners can change their schema; this is not a fence against owners.
CREATE FUNCTION api.guard_note_receipt_v2() RETURNS trigger
LANGUAGE plpgsql SECURITY INVOKER SET search_path = pg_catalog AS $$
BEGIN
  IF TG_OP = 'UPDATE' THEN
    RAISE SQLSTATE 'PT409' USING MESSAGE = 'idempotency_receipt_immutable';
  END IF;
  IF OLD.expires_at > clock_timestamp() THEN
    RAISE SQLSTATE 'PT409' USING MESSAGE = 'idempotency_replay_window_open';
  END IF;
  RETURN OLD;
END;
$$;
REVOKE ALL ON FUNCTION api.guard_note_receipt_v2() FROM PUBLIC;
CREATE TRIGGER guard_note_receipt_v2 BEFORE UPDATE OR DELETE ON api.note_create_receipts_v2
  FOR EACH ROW EXECUTE FUNCTION api.guard_note_receipt_v2();

CREATE FUNCTION api.create_note_with_tags_once_v2(idempotency_key uuid, note_body text, tag_names text[] DEFAULT '{}')
RETURNS SETOF api.notes
LANGUAGE plpgsql SECURITY INVOKER SET search_path = pg_catalog
SET default_transaction_isolation = 'read committed'
AS $$
DECLARE
  caller text := NULLIF(current_setting('request.jwt.claims', true)::jsonb ->> 'sub', '');
  input jsonb := jsonb_build_object('note_body', note_body, 'tag_names', tag_names);
  previous_scope text := current_setting('gregale.idempotency_rpc', true);
  issued_at timestamptz;
  expires_at timestamptz;
  checked_at timestamptz;
  saved api.note_create_receipts_v2;
  created api.notes;
BEGIN
  IF caller IS NULL THEN
    RAISE SQLSTATE '42501' USING MESSAGE = 'application subject required';
  END IF;
  IF idempotency_key IS NULL OR substr(idempotency_key::text, 15, 1) <> '7'
    OR substr(idempotency_key::text, 20, 1) NOT IN ('8','9','a','b') THEN
    RAISE SQLSTATE 'PT400' USING MESSAGE = 'idempotency_key_invalid';
  END IF;
  issued_at := to_timestamp((('x' || substr(replace(idempotency_key::text, '-', ''), 1, 12))::bit(48)::bigint)::double precision / 1000);
  expires_at := issued_at + interval '86400000 milliseconds';
  checked_at := clock_timestamp();
  IF issued_at > checked_at + interval '300000 milliseconds' THEN
    RAISE SQLSTATE 'PT400' USING MESSAGE = 'idempotency_key_in_future';
  END IF;
  IF checked_at >= expires_at THEN
    RAISE SQLSTATE 'PT410' USING MESSAGE = 'idempotency_key_expired';
  END IF;
  PERFORM set_config('gregale.idempotency_rpc', 'create_note_with_tags_once_v2', true);
  PERFORM pg_advisory_xact_lock(hashtextextended(
    jsonb_build_array('gregale.note_create.v2', caller, idempotency_key)::text, 0));
  -- Use wall time after waiting. now()/statement_timestamp() can be stale.
  IF clock_timestamp() >= expires_at THEN
    RAISE SQLSTATE 'PT410' USING MESSAGE = 'idempotency_key_expired';
  END IF;
  SELECT r.* INTO saved FROM api.note_create_receipts_v2 r
    WHERE r.subject = caller AND r.request_key = idempotency_key;
  IF FOUND THEN
    IF saved.request IS DISTINCT FROM input THEN
      RAISE SQLSTATE 'PT409' USING MESSAGE = 'idempotency_payload_conflict';
    END IF;
    RETURN NEXT jsonb_populate_record(NULL::api.notes, saved.response);
  ELSE
    SELECT n.* INTO STRICT created FROM api.create_note_with_tags(note_body, tag_names) n;
    -- Work waiting on business-row locks must not cross the replay deadline.
    IF clock_timestamp() >= expires_at THEN
      RAISE SQLSTATE 'PT410' USING MESSAGE = 'idempotency_key_expired';
    END IF;
    INSERT INTO api.note_create_receipts_v2(subject, request_key, request, response)
      VALUES (caller, idempotency_key, input, to_jsonb(created));
    RETURN NEXT created;
  END IF;
  IF clock_timestamp() >= expires_at THEN
    RAISE SQLSTATE 'PT410' USING MESSAGE = 'idempotency_key_expired';
  END IF;
  PERFORM set_config('gregale.idempotency_rpc', coalesce(previous_scope, ''), true);
  RETURN;
EXCEPTION WHEN OTHERS THEN
  PERFORM set_config('gregale.idempotency_rpc', coalesce(previous_scope, ''), true);
  RAISE;
END;
$$;
REVOKE ALL ON FUNCTION api.create_note_with_tags_once_v2(uuid, text, text[]) FROM PUBLIC;
COMMENT ON FUNCTION api.create_note_with_tags_once_v2(uuid, text, text[]) IS '@gregale:rpc';

CREATE FUNCTION api.read_note_create_receipt_v2(idempotency_key uuid, note_body text, tag_names text[] DEFAULT '{}')
RETURNS SETOF api.notes
LANGUAGE plpgsql STABLE SECURITY INVOKER SET search_path = pg_catalog
AS $$
DECLARE
  caller text := NULLIF(current_setting('request.jwt.claims', true)::jsonb ->> 'sub', '');
  input jsonb := jsonb_build_object('note_body', note_body, 'tag_names', tag_names);
  previous_scope text := current_setting('gregale.idempotency_rpc', true);
  issued_at timestamptz;
  saved api.note_create_receipts_v2;
BEGIN
  IF caller IS NULL THEN
    RAISE SQLSTATE '42501' USING MESSAGE = 'application subject required';
  END IF;
  IF idempotency_key IS NULL OR substr(idempotency_key::text, 15, 1) <> '7'
    OR substr(idempotency_key::text, 20, 1) NOT IN ('8','9','a','b') THEN
    RAISE SQLSTATE 'PT400' USING MESSAGE = 'idempotency_key_invalid';
  END IF;
  issued_at := to_timestamp((('x' || substr(replace(idempotency_key::text, '-', ''), 1, 12))::bit(48)::bigint)::double precision / 1000);
  IF issued_at > clock_timestamp() + interval '300000 milliseconds' THEN
    RAISE SQLSTATE 'PT400' USING MESSAGE = 'idempotency_key_in_future';
  END IF;
  PERFORM set_config('gregale.idempotency_rpc', 'read_note_create_receipt_v2', true);
  SELECT r.* INTO saved FROM api.note_create_receipts_v2 r
    WHERE r.subject = caller AND r.request_key = idempotency_key;
  IF FOUND THEN
    IF saved.request IS DISTINCT FROM input THEN
      RAISE SQLSTATE 'PT409' USING MESSAGE = 'idempotency_payload_conflict';
    END IF;
    -- An expired key may still confirm its retained original response.
    RETURN NEXT jsonb_populate_record(NULL::api.notes, saved.response);
  ELSIF clock_timestamp() >= issued_at + interval '86400000 milliseconds' THEN
    RAISE SQLSTATE 'PT410' USING MESSAGE = 'idempotency_key_expired';
  END IF;
  PERFORM set_config('gregale.idempotency_rpc', coalesce(previous_scope, ''), true);
  RETURN;
EXCEPTION WHEN OTHERS THEN
  PERFORM set_config('gregale.idempotency_rpc', coalesce(previous_scope, ''), true);
  RAISE;
END;
$$;
REVOKE ALL ON FUNCTION api.read_note_create_receipt_v2(uuid, text, text[]) FROM PUBLIC;
COMMENT ON FUNCTION api.read_note_create_receipt_v2(uuid, text, text[]) IS '@gregale:rpc';
