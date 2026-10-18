-- Opt-in update/delete contract (ADR-1671). Fixed windows mirror
-- DataAPIReplayV2* in pkg/api/limits.go. Never edit previously applied migrations.
CREATE TABLE api.note_mutation_receipts_v2 (
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
CREATE INDEX note_mutation_receipts_v2_expiry_idx ON api.note_mutation_receipts_v2 (expires_at, subject, request_key);
ALTER TABLE api.note_mutation_receipts_v2 ENABLE ROW LEVEL SECURITY;
CREATE POLICY read_own_note_mutation_receipts_v2 ON api.note_mutation_receipts_v2 FOR SELECT
  USING (subject = current_setting('request.jwt.claims', true)::jsonb ->> 'sub'
    AND current_setting('gregale.idempotency_rpc', true) IN
      ('update_note_once_v2','delete_note_once_v2','read_note_update_receipt_v2','read_note_delete_receipt_v2'));
CREATE POLICY insert_own_note_mutation_receipts_v2 ON api.note_mutation_receipts_v2 FOR INSERT
  WITH CHECK (subject = current_setting('request.jwt.claims', true)::jsonb ->> 'sub'
    AND current_setting('gregale.idempotency_rpc', true) IN ('update_note_once_v2','delete_note_once_v2'));
-- Reuse the owner maintenance guard from 0012. No serving-role trigger-function
-- grant is needed; PostgreSQL checks trigger execution privileges at creation.
CREATE TRIGGER guard_note_mutation_receipt_v2 BEFORE UPDATE OR DELETE ON api.note_mutation_receipts_v2
  FOR EACH ROW EXECUTE FUNCTION api.guard_note_receipt_v2();

CREATE FUNCTION api.update_note_once_v2(idempotency_key uuid, note_id integer, expected_version integer, note_patch jsonb)
RETURNS SETOF api.notes
LANGUAGE plpgsql SECURITY INVOKER SET search_path = pg_catalog
SET default_transaction_isolation = 'read committed'
AS $$
DECLARE
  caller text := NULLIF(current_setting('request.jwt.claims', true)::jsonb ->> 'sub', '');
  input jsonb := jsonb_build_object('operation', 'update', 'note_id', note_id, 'expected_version', expected_version, 'note_patch', note_patch);
  previous_scope text := current_setting('gregale.idempotency_rpc', true);
  issued_at timestamptz;
  expires_at timestamptz;
  saved api.note_mutation_receipts_v2;
  changed api.notes;
BEGIN
  IF caller IS NULL THEN
    RAISE SQLSTATE '42501' USING MESSAGE = 'application subject required';
  END IF;
  IF idempotency_key IS NULL OR substr(idempotency_key::text, 15, 1) <> '7'
    OR substr(idempotency_key::text, 20, 1) NOT IN ('8','9','a','b') THEN
    RAISE SQLSTATE 'PT400' USING MESSAGE = 'idempotency_key_invalid';
  END IF;
  IF note_id IS NULL OR note_id < 1 OR expected_version IS NULL OR expected_version < 1 THEN
    RAISE SQLSTATE 'PT400' USING MESSAGE = 'idempotency_mutation_invalid';
  END IF;
  IF note_patch IS NULL OR jsonb_typeof(note_patch) <> 'object' OR note_patch = '{}'::jsonb THEN
    RAISE SQLSTATE 'PT400' USING MESSAGE = 'idempotency_mutation_invalid';
  END IF;
  IF EXISTS (SELECT 1 FROM jsonb_object_keys(note_patch) AS k(name) WHERE k.name NOT IN ('body','priority')) THEN
    RAISE SQLSTATE 'PT400' USING MESSAGE = 'idempotency_mutation_invalid';
  END IF;
  IF note_patch ? 'body' AND jsonb_typeof(note_patch -> 'body') <> 'string' THEN
    RAISE SQLSTATE 'PT400' USING MESSAGE = 'idempotency_mutation_invalid';
  END IF;
  IF note_patch ? 'priority' THEN
    IF jsonb_typeof(note_patch -> 'priority') <> 'number' THEN
      RAISE SQLSTATE 'PT400' USING MESSAGE = 'idempotency_mutation_invalid';
    END IF;
    IF (note_patch ->> 'priority')::numeric <> trunc((note_patch ->> 'priority')::numeric)
      OR (note_patch ->> 'priority')::numeric < -2147483648
      OR (note_patch ->> 'priority')::numeric > 2147483647 THEN
      RAISE SQLSTATE 'PT400' USING MESSAGE = 'idempotency_mutation_invalid';
    END IF;
  END IF;
  issued_at := to_timestamp((('x' || substr(replace(idempotency_key::text, '-', ''), 1, 12))::bit(48)::bigint)::double precision / 1000);
  IF issued_at > clock_timestamp() + interval '300000 milliseconds' THEN
    RAISE SQLSTATE 'PT400' USING MESSAGE = 'idempotency_key_in_future';
  END IF;
  expires_at := issued_at + interval '86400000 milliseconds';
  IF clock_timestamp() >= expires_at THEN
    RAISE SQLSTATE 'PT410' USING MESSAGE = 'idempotency_key_expired';
  END IF;
  PERFORM set_config('gregale.idempotency_rpc', 'update_note_once_v2', true);
  -- Update and delete share a key namespace. Reusing a key for another action,
  -- target, version or payload conflicts instead of performing another write.
  PERFORM pg_advisory_xact_lock(hashtextextended(
    jsonb_build_array('gregale.note_mutation.v2', caller, idempotency_key)::text, 0));
  IF clock_timestamp() >= expires_at THEN
    RAISE SQLSTATE 'PT410' USING MESSAGE = 'idempotency_key_expired';
  END IF;
  SELECT r.* INTO saved FROM api.note_mutation_receipts_v2 r
    WHERE r.subject = caller AND r.request_key = idempotency_key;
  IF FOUND THEN
    IF saved.request IS DISTINCT FROM input THEN
      RAISE SQLSTATE 'PT409' USING MESSAGE = 'idempotency_payload_conflict';
    END IF;
    changed := jsonb_populate_record(NULL::api.notes, saved.response);
  ELSE
    UPDATE api.notes n SET
      body = CASE WHEN note_patch ? 'body' THEN note_patch ->> 'body' ELSE n.body END,
      priority = CASE WHEN note_patch ? 'priority' THEN (note_patch ->> 'priority')::numeric::integer ELSE n.priority END
      WHERE n.subject = caller AND n.id = note_id AND n.version = expected_version
      RETURNING n.* INTO changed;
    IF clock_timestamp() >= expires_at THEN
      RAISE SQLSTATE 'PT410' USING MESSAGE = 'idempotency_key_expired';
    END IF;
    -- Absent, RLS-hidden and stale rows have the same public result. Failures
    -- roll back and do not reserve a key or create a success receipt.
    IF NOT FOUND THEN
      RAISE SQLSTATE 'PT409' USING MESSAGE = 'idempotency_version_conflict';
    END IF;
    INSERT INTO api.note_mutation_receipts_v2(subject, request_key, request, response)
      VALUES (caller, idempotency_key, input, to_jsonb(changed));
  END IF;
  IF clock_timestamp() >= expires_at THEN
    RAISE SQLSTATE 'PT410' USING MESSAGE = 'idempotency_key_expired';
  END IF;
  PERFORM set_config('gregale.idempotency_rpc', coalesce(previous_scope, ''), true);
  RETURN NEXT changed;
  RETURN;
EXCEPTION WHEN OTHERS THEN
  PERFORM set_config('gregale.idempotency_rpc', coalesce(previous_scope, ''), true);
  RAISE;
END;
$$;
REVOKE ALL ON FUNCTION api.update_note_once_v2(uuid, integer, integer, jsonb) FROM PUBLIC;
COMMENT ON FUNCTION api.update_note_once_v2(uuid, integer, integer, jsonb) IS '@gregale:rpc';

CREATE FUNCTION api.read_note_update_receipt_v2(idempotency_key uuid, note_id integer, expected_version integer, note_patch jsonb)
RETURNS SETOF api.notes
LANGUAGE plpgsql STABLE SECURITY INVOKER SET search_path = pg_catalog
AS $$
DECLARE
  caller text := NULLIF(current_setting('request.jwt.claims', true)::jsonb ->> 'sub', '');
  input jsonb := jsonb_build_object('operation', 'update', 'note_id', note_id, 'expected_version', expected_version, 'note_patch', note_patch);
  previous_scope text := current_setting('gregale.idempotency_rpc', true);
  issued_at timestamptz;
  saved api.note_mutation_receipts_v2;
BEGIN
  IF caller IS NULL THEN
    RAISE SQLSTATE '42501' USING MESSAGE = 'application subject required';
  END IF;
  IF idempotency_key IS NULL OR substr(idempotency_key::text, 15, 1) <> '7'
    OR substr(idempotency_key::text, 20, 1) NOT IN ('8','9','a','b') THEN
    RAISE SQLSTATE 'PT400' USING MESSAGE = 'idempotency_key_invalid';
  END IF;
  IF note_id IS NULL OR note_id < 1 OR expected_version IS NULL OR expected_version < 1 THEN
    RAISE SQLSTATE 'PT400' USING MESSAGE = 'idempotency_mutation_invalid';
  END IF;
  IF note_patch IS NULL OR jsonb_typeof(note_patch) <> 'object' OR note_patch = '{}'::jsonb THEN
    RAISE SQLSTATE 'PT400' USING MESSAGE = 'idempotency_mutation_invalid';
  END IF;
  IF EXISTS (SELECT 1 FROM jsonb_object_keys(note_patch) AS k(name) WHERE k.name NOT IN ('body','priority')) THEN
    RAISE SQLSTATE 'PT400' USING MESSAGE = 'idempotency_mutation_invalid';
  END IF;
  IF note_patch ? 'body' AND jsonb_typeof(note_patch -> 'body') <> 'string' THEN
    RAISE SQLSTATE 'PT400' USING MESSAGE = 'idempotency_mutation_invalid';
  END IF;
  IF note_patch ? 'priority' THEN
    IF jsonb_typeof(note_patch -> 'priority') <> 'number' THEN
      RAISE SQLSTATE 'PT400' USING MESSAGE = 'idempotency_mutation_invalid';
    END IF;
    IF (note_patch ->> 'priority')::numeric <> trunc((note_patch ->> 'priority')::numeric)
      OR (note_patch ->> 'priority')::numeric < -2147483648
      OR (note_patch ->> 'priority')::numeric > 2147483647 THEN
      RAISE SQLSTATE 'PT400' USING MESSAGE = 'idempotency_mutation_invalid';
    END IF;
  END IF;
  issued_at := to_timestamp((('x' || substr(replace(idempotency_key::text, '-', ''), 1, 12))::bit(48)::bigint)::double precision / 1000);
  IF issued_at > clock_timestamp() + interval '300000 milliseconds' THEN
    RAISE SQLSTATE 'PT400' USING MESSAGE = 'idempotency_key_in_future';
  END IF;
  PERFORM set_config('gregale.idempotency_rpc', 'read_note_update_receipt_v2', true);
  SELECT r.* INTO saved FROM api.note_mutation_receipts_v2 r
    WHERE r.subject = caller AND r.request_key = idempotency_key;
  IF FOUND THEN
    IF saved.request IS DISTINCT FROM input THEN
      RAISE SQLSTATE 'PT409' USING MESSAGE = 'idempotency_payload_conflict';
    END IF;
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
REVOKE ALL ON FUNCTION api.read_note_update_receipt_v2(uuid, integer, integer, jsonb) FROM PUBLIC;
COMMENT ON FUNCTION api.read_note_update_receipt_v2(uuid, integer, integer, jsonb) IS '@gregale:rpc';

CREATE FUNCTION api.delete_note_once_v2(idempotency_key uuid, note_id integer, expected_version integer)
RETURNS SETOF api.notes
LANGUAGE plpgsql SECURITY INVOKER SET search_path = pg_catalog
SET default_transaction_isolation = 'read committed'
AS $$
DECLARE
  caller text := NULLIF(current_setting('request.jwt.claims', true)::jsonb ->> 'sub', '');
  input jsonb := jsonb_build_object('operation', 'delete', 'note_id', note_id, 'expected_version', expected_version);
  previous_scope text := current_setting('gregale.idempotency_rpc', true);
  issued_at timestamptz;
  expires_at timestamptz;
  saved api.note_mutation_receipts_v2;
  changed api.notes;
BEGIN
  IF caller IS NULL THEN
    RAISE SQLSTATE '42501' USING MESSAGE = 'application subject required';
  END IF;
  IF idempotency_key IS NULL OR substr(idempotency_key::text, 15, 1) <> '7'
    OR substr(idempotency_key::text, 20, 1) NOT IN ('8','9','a','b') THEN
    RAISE SQLSTATE 'PT400' USING MESSAGE = 'idempotency_key_invalid';
  END IF;
  IF note_id IS NULL OR note_id < 1 OR expected_version IS NULL OR expected_version < 1 THEN
    RAISE SQLSTATE 'PT400' USING MESSAGE = 'idempotency_mutation_invalid';
  END IF;
  issued_at := to_timestamp((('x' || substr(replace(idempotency_key::text, '-', ''), 1, 12))::bit(48)::bigint)::double precision / 1000);
  IF issued_at > clock_timestamp() + interval '300000 milliseconds' THEN
    RAISE SQLSTATE 'PT400' USING MESSAGE = 'idempotency_key_in_future';
  END IF;
  expires_at := issued_at + interval '86400000 milliseconds';
  IF clock_timestamp() >= expires_at THEN
    RAISE SQLSTATE 'PT410' USING MESSAGE = 'idempotency_key_expired';
  END IF;
  PERFORM set_config('gregale.idempotency_rpc', 'delete_note_once_v2', true);
  -- Update and delete share a key namespace. Reusing a key for another action,
  -- target, version or payload conflicts instead of performing another write.
  PERFORM pg_advisory_xact_lock(hashtextextended(
    jsonb_build_array('gregale.note_mutation.v2', caller, idempotency_key)::text, 0));
  IF clock_timestamp() >= expires_at THEN
    RAISE SQLSTATE 'PT410' USING MESSAGE = 'idempotency_key_expired';
  END IF;
  SELECT r.* INTO saved FROM api.note_mutation_receipts_v2 r
    WHERE r.subject = caller AND r.request_key = idempotency_key;
  IF FOUND THEN
    IF saved.request IS DISTINCT FROM input THEN
      RAISE SQLSTATE 'PT409' USING MESSAGE = 'idempotency_payload_conflict';
    END IF;
    changed := jsonb_populate_record(NULL::api.notes, saved.response);
  ELSE
    DELETE FROM api.notes n
      WHERE n.subject = caller AND n.id = note_id AND n.version = expected_version
      RETURNING n.* INTO changed;
    IF clock_timestamp() >= expires_at THEN
      RAISE SQLSTATE 'PT410' USING MESSAGE = 'idempotency_key_expired';
    END IF;
    -- Absent, RLS-hidden and stale rows have the same public result. Failures
    -- roll back and do not reserve a key or create a success receipt.
    IF NOT FOUND THEN
      RAISE SQLSTATE 'PT409' USING MESSAGE = 'idempotency_version_conflict';
    END IF;
    INSERT INTO api.note_mutation_receipts_v2(subject, request_key, request, response)
      VALUES (caller, idempotency_key, input, to_jsonb(changed));
  END IF;
  IF clock_timestamp() >= expires_at THEN
    RAISE SQLSTATE 'PT410' USING MESSAGE = 'idempotency_key_expired';
  END IF;
  PERFORM set_config('gregale.idempotency_rpc', coalesce(previous_scope, ''), true);
  RETURN NEXT changed;
  RETURN;
EXCEPTION WHEN OTHERS THEN
  PERFORM set_config('gregale.idempotency_rpc', coalesce(previous_scope, ''), true);
  RAISE;
END;
$$;
REVOKE ALL ON FUNCTION api.delete_note_once_v2(uuid, integer, integer) FROM PUBLIC;
COMMENT ON FUNCTION api.delete_note_once_v2(uuid, integer, integer) IS '@gregale:rpc';

CREATE FUNCTION api.read_note_delete_receipt_v2(idempotency_key uuid, note_id integer, expected_version integer)
RETURNS SETOF api.notes
LANGUAGE plpgsql STABLE SECURITY INVOKER SET search_path = pg_catalog
AS $$
DECLARE
  caller text := NULLIF(current_setting('request.jwt.claims', true)::jsonb ->> 'sub', '');
  input jsonb := jsonb_build_object('operation', 'delete', 'note_id', note_id, 'expected_version', expected_version);
  previous_scope text := current_setting('gregale.idempotency_rpc', true);
  issued_at timestamptz;
  saved api.note_mutation_receipts_v2;
BEGIN
  IF caller IS NULL THEN
    RAISE SQLSTATE '42501' USING MESSAGE = 'application subject required';
  END IF;
  IF idempotency_key IS NULL OR substr(idempotency_key::text, 15, 1) <> '7'
    OR substr(idempotency_key::text, 20, 1) NOT IN ('8','9','a','b') THEN
    RAISE SQLSTATE 'PT400' USING MESSAGE = 'idempotency_key_invalid';
  END IF;
  IF note_id IS NULL OR note_id < 1 OR expected_version IS NULL OR expected_version < 1 THEN
    RAISE SQLSTATE 'PT400' USING MESSAGE = 'idempotency_mutation_invalid';
  END IF;
  issued_at := to_timestamp((('x' || substr(replace(idempotency_key::text, '-', ''), 1, 12))::bit(48)::bigint)::double precision / 1000);
  IF issued_at > clock_timestamp() + interval '300000 milliseconds' THEN
    RAISE SQLSTATE 'PT400' USING MESSAGE = 'idempotency_key_in_future';
  END IF;
  PERFORM set_config('gregale.idempotency_rpc', 'read_note_delete_receipt_v2', true);
  SELECT r.* INTO saved FROM api.note_mutation_receipts_v2 r
    WHERE r.subject = caller AND r.request_key = idempotency_key;
  IF FOUND THEN
    IF saved.request IS DISTINCT FROM input THEN
      RAISE SQLSTATE 'PT409' USING MESSAGE = 'idempotency_payload_conflict';
    END IF;
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
REVOKE ALL ON FUNCTION api.read_note_delete_receipt_v2(uuid, integer, integer) FROM PUBLIC;
COMMENT ON FUNCTION api.read_note_delete_receipt_v2(uuid, integer, integer) IS '@gregale:rpc';
