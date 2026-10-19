-- Fixed opt-in batch contract (ADR-1672). Bounds mirror pkg/api/limits.go.
-- Previously applied migrations and single-record namespaces remain unchanged.
CREATE TABLE api.note_batch_receipts_v2 (
  subject text NOT NULL,
  request_key uuid NOT NULL CHECK (
    substr(request_key::text, 15, 1) = '7' AND substr(request_key::text, 20, 1) IN ('8','9','a','b')),
  request jsonb NOT NULL CHECK (jsonb_typeof(request) = 'array'),
  response jsonb NOT NULL CHECK (jsonb_typeof(response) = 'array'),
  created_at timestamptz NOT NULL DEFAULT now(),
  expires_at timestamptz GENERATED ALWAYS AS (
    to_timestamp(((('x' || substr(replace(request_key::text, '-', ''), 1, 12))::bit(48)::bigint
      + 86400000)::double precision) / 1000)) STORED,
  PRIMARY KEY (subject, request_key)
);
CREATE INDEX note_batch_receipts_v2_expiry_idx ON api.note_batch_receipts_v2 (expires_at, subject, request_key);
ALTER TABLE api.note_batch_receipts_v2 ENABLE ROW LEVEL SECURITY;
CREATE POLICY read_own_note_batch_receipts_v2 ON api.note_batch_receipts_v2 FOR SELECT
  USING (subject = current_setting('request.jwt.claims', true)::jsonb ->> 'sub'
    AND current_setting('gregale.idempotency_rpc', true) IN ('apply_note_batch_once_v2','read_note_batch_receipt_v2'));
CREATE POLICY insert_own_note_batch_receipts_v2 ON api.note_batch_receipts_v2 FOR INSERT
  WITH CHECK (subject = current_setting('request.jwt.claims', true)::jsonb ->> 'sub'
    AND current_setting('gregale.idempotency_rpc', true) = 'apply_note_batch_once_v2');
CREATE TRIGGER guard_note_batch_receipt_v2 BEFORE UPDATE OR DELETE ON api.note_batch_receipts_v2
  FOR EACH ROW EXECUTE FUNCTION api.guard_note_receipt_v2();

-- One public envelope keeps acknowledgement indivisible even when the batch
-- changes several notes. JSON results preserve input order explicitly by index.
CREATE TYPE api.note_batch_result_v2 AS (request_key uuid, subject text, results jsonb);

CREATE FUNCTION api.apply_note_batch_once_v2(idempotency_key uuid, operations jsonb)
RETURNS SETOF api.note_batch_result_v2
LANGUAGE plpgsql SECURITY INVOKER SET search_path = pg_catalog
SET default_transaction_isolation = 'read committed'
AS $$
DECLARE
  caller text := NULLIF(current_setting('request.jwt.claims', true)::jsonb ->> 'sub', '');
  previous_scope text := current_setting('gregale.idempotency_rpc', true);
  item jsonb;
  patch jsonb;
  field text;
  issued_at timestamptz;
  expires_at timestamptz;
  saved api.note_batch_receipts_v2;
  changed api.notes;
  locked_count integer;
  snapshots jsonb := '[]'::jsonb;
  position integer := 0;
BEGIN
  IF caller IS NULL THEN
    RAISE SQLSTATE '42501' USING MESSAGE = 'application subject required';
  END IF;
  IF idempotency_key IS NULL OR substr(idempotency_key::text, 15, 1) <> '7'
    OR substr(idempotency_key::text, 20, 1) NOT IN ('8','9','a','b') THEN
    RAISE SQLSTATE 'PT400' USING MESSAGE = 'idempotency_key_invalid';
  END IF;
  IF operations IS NULL OR jsonb_typeof(operations) <> 'array' THEN
    RAISE SQLSTATE 'PT400' USING MESSAGE = 'idempotency_batch_invalid';
  END IF;
  IF jsonb_array_length(operations) NOT BETWEEN 1 AND 100 THEN
    RAISE SQLSTATE 'PT400' USING MESSAGE = 'idempotency_batch_invalid';
  END IF;
  FOR item IN SELECT value FROM jsonb_array_elements(operations) LOOP
    IF jsonb_typeof(item) <> 'object' THEN
      RAISE SQLSTATE 'PT400' USING MESSAGE = 'idempotency_batch_invalid';
    END IF;
    IF jsonb_typeof(item -> 'operation') IS DISTINCT FROM 'string'
      OR (item ->> 'operation') NOT IN ('update','delete')
      OR EXISTS (SELECT 1 FROM jsonb_object_keys(item) AS k(name)
        WHERE k.name NOT IN ('operation','note_id','expected_version','patch'))
      OR jsonb_typeof(item -> 'note_id') IS DISTINCT FROM 'number'
      OR jsonb_typeof(item -> 'expected_version') IS DISTINCT FROM 'number' THEN
      RAISE SQLSTATE 'PT400' USING MESSAGE = 'idempotency_batch_invalid';
    END IF;
    FOR field IN SELECT unnest(ARRAY['note_id','expected_version']) LOOP
      IF (item ->> field)::numeric <> trunc((item ->> field)::numeric)
        OR (item ->> field)::numeric NOT BETWEEN 1 AND 2147483647 THEN
        RAISE SQLSTATE 'PT400' USING MESSAGE = 'idempotency_batch_invalid';
      END IF;
    END LOOP;
    IF item ->> 'operation' = 'delete' THEN
      IF item ? 'patch' THEN
        RAISE SQLSTATE 'PT400' USING MESSAGE = 'idempotency_batch_invalid';
      END IF;
    ELSE
      patch := item -> 'patch';
      IF patch IS NULL OR jsonb_typeof(patch) <> 'object' OR patch = '{}'::jsonb THEN
        RAISE SQLSTATE 'PT400' USING MESSAGE = 'idempotency_batch_invalid';
      END IF;
      IF EXISTS (SELECT 1 FROM jsonb_object_keys(patch) AS k(name) WHERE k.name NOT IN ('body','priority'))
        OR (patch ? 'body' AND jsonb_typeof(patch -> 'body') <> 'string') THEN
        RAISE SQLSTATE 'PT400' USING MESSAGE = 'idempotency_batch_invalid';
      END IF;
      IF patch ? 'priority' THEN
        IF jsonb_typeof(patch -> 'priority') <> 'number' THEN
          RAISE SQLSTATE 'PT400' USING MESSAGE = 'idempotency_batch_invalid';
        END IF;
        IF (patch ->> 'priority')::numeric <> trunc((patch ->> 'priority')::numeric)
          OR (patch ->> 'priority')::numeric NOT BETWEEN -2147483648 AND 2147483647 THEN
          RAISE SQLSTATE 'PT400' USING MESSAGE = 'idempotency_batch_invalid';
        END IF;
      END IF;
    END IF;
  END LOOP;
  IF (SELECT count(DISTINCT (value ->> 'note_id')::numeric) FROM jsonb_array_elements(operations)) <> jsonb_array_length(operations) THEN
    RAISE SQLSTATE 'PT400' USING MESSAGE = 'idempotency_batch_invalid';
  END IF;
  issued_at := to_timestamp((('x' || substr(replace(idempotency_key::text, '-', ''), 1, 12))::bit(48)::bigint)::double precision / 1000);
  IF issued_at > clock_timestamp() + interval '300000 milliseconds' THEN
    RAISE SQLSTATE 'PT400' USING MESSAGE = 'idempotency_key_in_future';
  END IF;
  expires_at := issued_at + interval '86400000 milliseconds';
  IF clock_timestamp() >= expires_at THEN
    RAISE SQLSTATE 'PT410' USING MESSAGE = 'idempotency_key_expired';
  END IF;
  PERFORM set_config('gregale.idempotency_rpc', 'apply_note_batch_once_v2', true);
  PERFORM pg_advisory_xact_lock(hashtextextended(
    jsonb_build_array('gregale.note_batch.v2', caller, idempotency_key)::text, 0));
  IF clock_timestamp() >= expires_at THEN
    RAISE SQLSTATE 'PT410' USING MESSAGE = 'idempotency_key_expired';
  END IF;
  SELECT r.* INTO saved FROM api.note_batch_receipts_v2 r
    WHERE r.subject = caller AND r.request_key = idempotency_key;
  IF FOUND THEN
    IF saved.request IS DISTINCT FROM operations THEN
      RAISE SQLSTATE 'PT409' USING MESSAGE = 'idempotency_payload_conflict';
    END IF;
    snapshots := saved.response;
  ELSE
    -- Both overlapping batches lock matching rows in the same ID order,
    -- independently of the caller's submitted operation order.
    PERFORM n.id FROM api.notes n JOIN jsonb_array_elements(operations) AS x(item)
      ON n.id = (x.item ->> 'note_id')::numeric::integer
        AND n.version = (x.item ->> 'expected_version')::numeric::integer
      WHERE n.subject = caller
      ORDER BY n.id FOR UPDATE OF n;
    GET DIAGNOSTICS locked_count = ROW_COUNT;
    IF clock_timestamp() >= expires_at THEN
      RAISE SQLSTATE 'PT410' USING MESSAGE = 'idempotency_key_expired';
    END IF;
    IF locked_count <> jsonb_array_length(operations) THEN
      RAISE SQLSTATE 'PT409' USING MESSAGE = 'idempotency_version_conflict';
    END IF;
    FOR item IN SELECT value FROM jsonb_array_elements(operations) LOOP
      IF item ->> 'operation' = 'update' THEN
        patch := item -> 'patch';
        UPDATE api.notes n SET
          body = CASE WHEN patch ? 'body' THEN patch ->> 'body' ELSE n.body END,
          priority = CASE WHEN patch ? 'priority' THEN (patch ->> 'priority')::numeric::integer ELSE n.priority END
          WHERE n.subject = caller AND n.id = (item ->> 'note_id')::numeric::integer
            AND n.version = (item ->> 'expected_version')::numeric::integer
          RETURNING n.* INTO changed;
      ELSE
        DELETE FROM api.notes n
          WHERE n.subject = caller AND n.id = (item ->> 'note_id')::numeric::integer
            AND n.version = (item ->> 'expected_version')::numeric::integer
          RETURNING n.* INTO changed;
      END IF;
      IF clock_timestamp() >= expires_at THEN
        RAISE SQLSTATE 'PT410' USING MESSAGE = 'idempotency_key_expired';
      END IF;
      IF NOT FOUND THEN
        RAISE SQLSTATE 'PT409' USING MESSAGE = 'idempotency_version_conflict';
      END IF;
      snapshots := snapshots || jsonb_build_array(jsonb_build_object(
        'index', position, 'operation', item ->> 'operation', 'note', to_jsonb(changed)));
      -- Fail within the same transaction, including every preceding mutation.
      IF octet_length(snapshots::text) > 1048576 THEN
        RAISE SQLSTATE 'PT413' USING MESSAGE = 'idempotency_batch_result_too_large';
      END IF;
      position := position + 1;
    END LOOP;
    INSERT INTO api.note_batch_receipts_v2(subject, request_key, request, response)
      VALUES (caller, idempotency_key, operations, snapshots);
  END IF;
  IF clock_timestamp() >= expires_at THEN
    RAISE SQLSTATE 'PT410' USING MESSAGE = 'idempotency_key_expired';
  END IF;
  PERFORM set_config('gregale.idempotency_rpc', coalesce(previous_scope, ''), true);
  RETURN NEXT ROW(idempotency_key, caller, snapshots)::api.note_batch_result_v2;
  RETURN;
EXCEPTION WHEN OTHERS THEN
  PERFORM set_config('gregale.idempotency_rpc', coalesce(previous_scope, ''), true);
  RAISE;
END;
$$;
REVOKE ALL ON FUNCTION api.apply_note_batch_once_v2(uuid, jsonb) FROM PUBLIC;
COMMENT ON FUNCTION api.apply_note_batch_once_v2(uuid, jsonb) IS '@gregale:rpc';

CREATE FUNCTION api.read_note_batch_receipt_v2(idempotency_key uuid, operations jsonb)
RETURNS SETOF api.note_batch_result_v2
LANGUAGE plpgsql STABLE SECURITY INVOKER SET search_path = pg_catalog
AS $$
DECLARE
  caller text := NULLIF(current_setting('request.jwt.claims', true)::jsonb ->> 'sub', '');
  previous_scope text := current_setting('gregale.idempotency_rpc', true);
  item jsonb;
  patch jsonb;
  field text;
  issued_at timestamptz;
  saved api.note_batch_receipts_v2;
BEGIN
  IF caller IS NULL THEN
    RAISE SQLSTATE '42501' USING MESSAGE = 'application subject required';
  END IF;
  IF idempotency_key IS NULL OR substr(idempotency_key::text, 15, 1) <> '7'
    OR substr(idempotency_key::text, 20, 1) NOT IN ('8','9','a','b') THEN
    RAISE SQLSTATE 'PT400' USING MESSAGE = 'idempotency_key_invalid';
  END IF;
  IF operations IS NULL OR jsonb_typeof(operations) <> 'array' THEN
    RAISE SQLSTATE 'PT400' USING MESSAGE = 'idempotency_batch_invalid';
  END IF;
  IF jsonb_array_length(operations) NOT BETWEEN 1 AND 100 THEN
    RAISE SQLSTATE 'PT400' USING MESSAGE = 'idempotency_batch_invalid';
  END IF;
  FOR item IN SELECT value FROM jsonb_array_elements(operations) LOOP
    IF jsonb_typeof(item) <> 'object' THEN
      RAISE SQLSTATE 'PT400' USING MESSAGE = 'idempotency_batch_invalid';
    END IF;
    IF jsonb_typeof(item -> 'operation') IS DISTINCT FROM 'string'
      OR (item ->> 'operation') NOT IN ('update','delete')
      OR EXISTS (SELECT 1 FROM jsonb_object_keys(item) AS k(name)
        WHERE k.name NOT IN ('operation','note_id','expected_version','patch'))
      OR jsonb_typeof(item -> 'note_id') IS DISTINCT FROM 'number'
      OR jsonb_typeof(item -> 'expected_version') IS DISTINCT FROM 'number' THEN
      RAISE SQLSTATE 'PT400' USING MESSAGE = 'idempotency_batch_invalid';
    END IF;
    FOR field IN SELECT unnest(ARRAY['note_id','expected_version']) LOOP
      IF (item ->> field)::numeric <> trunc((item ->> field)::numeric)
        OR (item ->> field)::numeric NOT BETWEEN 1 AND 2147483647 THEN
        RAISE SQLSTATE 'PT400' USING MESSAGE = 'idempotency_batch_invalid';
      END IF;
    END LOOP;
    IF item ->> 'operation' = 'delete' THEN
      IF item ? 'patch' THEN
        RAISE SQLSTATE 'PT400' USING MESSAGE = 'idempotency_batch_invalid';
      END IF;
    ELSE
      patch := item -> 'patch';
      IF patch IS NULL OR jsonb_typeof(patch) <> 'object' OR patch = '{}'::jsonb THEN
        RAISE SQLSTATE 'PT400' USING MESSAGE = 'idempotency_batch_invalid';
      END IF;
      IF EXISTS (SELECT 1 FROM jsonb_object_keys(patch) AS k(name) WHERE k.name NOT IN ('body','priority'))
        OR (patch ? 'body' AND jsonb_typeof(patch -> 'body') <> 'string') THEN
        RAISE SQLSTATE 'PT400' USING MESSAGE = 'idempotency_batch_invalid';
      END IF;
      IF patch ? 'priority' THEN
        IF jsonb_typeof(patch -> 'priority') <> 'number' THEN
          RAISE SQLSTATE 'PT400' USING MESSAGE = 'idempotency_batch_invalid';
        END IF;
        IF (patch ->> 'priority')::numeric <> trunc((patch ->> 'priority')::numeric)
          OR (patch ->> 'priority')::numeric NOT BETWEEN -2147483648 AND 2147483647 THEN
          RAISE SQLSTATE 'PT400' USING MESSAGE = 'idempotency_batch_invalid';
        END IF;
      END IF;
    END IF;
  END LOOP;
  IF (SELECT count(DISTINCT (value ->> 'note_id')::numeric) FROM jsonb_array_elements(operations)) <> jsonb_array_length(operations) THEN
    RAISE SQLSTATE 'PT400' USING MESSAGE = 'idempotency_batch_invalid';
  END IF;
  issued_at := to_timestamp((('x' || substr(replace(idempotency_key::text, '-', ''), 1, 12))::bit(48)::bigint)::double precision / 1000);
  IF issued_at > clock_timestamp() + interval '300000 milliseconds' THEN
    RAISE SQLSTATE 'PT400' USING MESSAGE = 'idempotency_key_in_future';
  END IF;
  PERFORM set_config('gregale.idempotency_rpc', 'read_note_batch_receipt_v2', true);
  SELECT r.* INTO saved FROM api.note_batch_receipts_v2 r
    WHERE r.subject = caller AND r.request_key = idempotency_key;
  IF FOUND THEN
    IF saved.request IS DISTINCT FROM operations THEN
      RAISE SQLSTATE 'PT409' USING MESSAGE = 'idempotency_payload_conflict';
    END IF;
    RETURN NEXT ROW(idempotency_key, caller, saved.response)::api.note_batch_result_v2;
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
REVOKE ALL ON FUNCTION api.read_note_batch_receipt_v2(uuid, jsonb) FROM PUBLIC;
COMMENT ON FUNCTION api.read_note_batch_receipt_v2(uuid, jsonb) IS '@gregale:rpc';
