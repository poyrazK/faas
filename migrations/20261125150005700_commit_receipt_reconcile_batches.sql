-- filename: 20261125150005700_commit_receipt_reconcile_batches.sql
-- migration-compat: expand

-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE commit_receipt_usage
 ADD COLUMN scan_in_progress boolean NOT NULL DEFAULT false,
 ADD COLUMN scan_cursor uuid,
 ADD COLUMN scan_count bigint NOT NULL DEFAULT 0,
 ADD COLUMN scan_payload_json_bytes bigint NOT NULL DEFAULT 0,
 ADD COLUMN reconcile_token uuid,
 ADD COLUMN retry_at timestamptz NOT NULL DEFAULT '-infinity';

-- Writers acquire the same shard lock as a scanner. Only changes at or below
-- its committed cursor affect the partial scan; later rows remain for a batch.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION commit_receipt_usage_insert() RETURNS trigger LANGUAGE plpgsql
SET search_path = pg_catalog, public AS $$
DECLARE r record; v_cursor uuid; v_active boolean; v_count bigint; v_bytes bigint;
BEGIN
 FOR r IN SELECT get_byte(uuid_send(id),0) AS shard, count(*) AS n,
   sum(octet_length(payload::text)) AS bytes,
   array_agg(id ORDER BY id) AS ids, array_agg(octet_length(payload::text) ORDER BY id) AS sizes
   FROM new_receipts GROUP BY 1 ORDER BY 1 LOOP
  SELECT scan_in_progress,scan_cursor INTO v_active,v_cursor
   FROM public.commit_receipt_usage WHERE shard=r.shard FOR UPDATE;
  v_count:=0; v_bytes:=0;
  IF v_active AND v_cursor IS NOT NULL THEN
   SELECT count(*),coalesce(sum(x.bytes),0) INTO v_count,v_bytes
    FROM unnest(r.ids,r.sizes) AS x(id,bytes) WHERE x.id<=v_cursor;
  END IF;
  UPDATE public.commit_receipt_usage SET
   receipt_count=receipt_count + CASE WHEN initialized THEN r.n ELSE 0 END,
   payload_json_bytes=payload_json_bytes + CASE WHEN initialized THEN r.bytes ELSE 0 END,
   scan_count=scan_count + v_count,
   scan_payload_json_bytes=scan_payload_json_bytes + v_bytes
  WHERE shard=r.shard;
 END LOOP;
 RETURN NULL;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION commit_receipt_usage_delete() RETURNS trigger LANGUAGE plpgsql
SET search_path = pg_catalog, public AS $$
DECLARE r record; v_cursor uuid; v_active boolean; v_count bigint; v_bytes bigint;
BEGIN
 FOR r IN SELECT get_byte(uuid_send(id),0) AS shard, count(*) AS n,
   sum(octet_length(payload::text)) AS bytes,
   array_agg(id ORDER BY id) AS ids, array_agg(octet_length(payload::text) ORDER BY id) AS sizes
   FROM old_receipts GROUP BY 1 ORDER BY 1 LOOP
  SELECT scan_in_progress,scan_cursor INTO v_active,v_cursor
   FROM public.commit_receipt_usage WHERE shard=r.shard FOR UPDATE;
  v_count:=0; v_bytes:=0;
  IF v_active AND v_cursor IS NOT NULL THEN
   SELECT count(*),coalesce(sum(x.bytes),0) INTO v_count,v_bytes
    FROM unnest(r.ids,r.sizes) AS x(id,bytes) WHERE x.id<=v_cursor;
  END IF;
  UPDATE public.commit_receipt_usage SET
   receipt_count=receipt_count - CASE WHEN initialized THEN r.n ELSE 0 END,
   payload_json_bytes=payload_json_bytes - CASE WHEN initialized THEN r.bytes ELSE 0 END,
   scan_count=scan_count - v_count,
   scan_payload_json_bytes=scan_payload_json_bytes - v_bytes
  WHERE shard=r.shard;
 END LOOP;
 RETURN NULL;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION commit_receipt_usage_update() RETURNS trigger LANGUAGE plpgsql
SET search_path = pg_catalog, public AS $$
DECLARE r record; v_cursor uuid; v_active boolean; v_count bigint; v_bytes bigint;
BEGIN
 IF OLD.id=NEW.id AND OLD.payload::text=NEW.payload::text THEN RETURN NULL; END IF;
 FOR r IN WITH deltas AS (
   SELECT get_byte(uuid_send(OLD.id),0) AS shard,OLD.id AS id,-1::bigint AS n,-octet_length(OLD.payload::text)::bigint AS bytes
   UNION ALL
   SELECT get_byte(uuid_send(NEW.id),0),NEW.id,1::bigint,octet_length(NEW.payload::text)::bigint
  ) SELECT shard,sum(n) AS n,sum(bytes) AS bytes,array_agg(id ORDER BY id,n) AS ids,
    array_agg(n ORDER BY id,n) AS counts,array_agg(bytes ORDER BY id,n) AS sizes FROM deltas GROUP BY shard ORDER BY shard LOOP
  SELECT scan_in_progress,scan_cursor INTO v_active,v_cursor
   FROM public.commit_receipt_usage WHERE shard=r.shard FOR UPDATE;
  v_count:=0; v_bytes:=0;
  IF v_active AND v_cursor IS NOT NULL THEN
   SELECT coalesce(sum(x.n),0),coalesce(sum(x.bytes),0) INTO v_count,v_bytes
    FROM unnest(r.ids,r.counts,r.sizes) AS x(id,n,bytes) WHERE x.id<=v_cursor;
  END IF;
  UPDATE public.commit_receipt_usage SET
   receipt_count=receipt_count + CASE WHEN initialized THEN r.n ELSE 0 END,
   payload_json_bytes=payload_json_bytes + CASE WHEN initialized THEN r.bytes ELSE 0 END,
   scan_count=scan_count + v_count,
   scan_payload_json_bytes=scan_payload_json_bytes + v_bytes
  WHERE shard=r.shard;
 END LOOP;
 RETURN NULL;
END $$;
-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION commit_receipt_usage_insert() RETURNS trigger LANGUAGE plpgsql
SET search_path = pg_catalog, public AS $$
DECLARE r record;
BEGIN
 FOR r IN SELECT get_byte(uuid_send(id),0) AS shard, count(*) AS n,
   sum(octet_length(payload::text)) AS bytes FROM new_receipts GROUP BY 1 ORDER BY 1 LOOP
  UPDATE public.commit_receipt_usage SET
   receipt_count=receipt_count + CASE WHEN initialized THEN r.n ELSE 0 END,
   payload_json_bytes=payload_json_bytes + CASE WHEN initialized THEN r.bytes ELSE 0 END
  WHERE shard=r.shard;
 END LOOP;
 RETURN NULL;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION commit_receipt_usage_delete() RETURNS trigger LANGUAGE plpgsql
SET search_path = pg_catalog, public AS $$
DECLARE r record;
BEGIN
 FOR r IN SELECT get_byte(uuid_send(id),0) AS shard, count(*) AS n,
   sum(octet_length(payload::text)) AS bytes FROM old_receipts GROUP BY 1 ORDER BY 1 LOOP
  UPDATE public.commit_receipt_usage SET
   receipt_count=receipt_count - CASE WHEN initialized THEN r.n ELSE 0 END,
   payload_json_bytes=payload_json_bytes - CASE WHEN initialized THEN r.bytes ELSE 0 END
  WHERE shard=r.shard;
 END LOOP;
 RETURN NULL;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION commit_receipt_usage_update() RETURNS trigger LANGUAGE plpgsql
SET search_path = pg_catalog, public AS $$
DECLARE r record;
BEGIN
 IF OLD.id=NEW.id AND OLD.payload::text=NEW.payload::text THEN RETURN NULL; END IF;
 FOR r IN WITH deltas AS (
   SELECT get_byte(uuid_send(OLD.id),0) AS shard,-1::bigint AS n,-octet_length(OLD.payload::text)::bigint AS bytes
   UNION ALL
   SELECT get_byte(uuid_send(NEW.id),0),1::bigint,octet_length(NEW.payload::text)::bigint
  ) SELECT shard,sum(n) AS n,sum(bytes) AS bytes FROM deltas GROUP BY shard ORDER BY shard LOOP
  UPDATE public.commit_receipt_usage SET
   receipt_count=receipt_count + CASE WHEN initialized THEN r.n ELSE 0 END,
   payload_json_bytes=payload_json_bytes + CASE WHEN initialized THEN r.bytes ELSE 0 END
  WHERE shard=r.shard;
 END LOOP;
 RETURN NULL;
END $$;
-- +goose StatementEnd

ALTER TABLE commit_receipt_usage
 DROP COLUMN retry_at, DROP COLUMN reconcile_token,
 DROP COLUMN scan_payload_json_bytes, DROP COLUMN scan_count,
 DROP COLUMN scan_cursor, DROP COLUMN scan_in_progress;
