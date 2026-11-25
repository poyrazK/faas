-- filename: 20261125150005600_commit_receipt_usage.sql
-- migration-compat: expand

-- +goose Up
SET LOCAL lock_timeout = '5s';
CREATE TABLE IF NOT EXISTS commit_receipt_usage (
    shard smallint PRIMARY KEY CHECK (shard BETWEEN 0 AND 255),
    receipt_count bigint NOT NULL DEFAULT 0,
    payload_json_bytes bigint NOT NULL DEFAULT 0,
    initialized boolean NOT NULL DEFAULT false,
    reconciled_at timestamptz
);
INSERT INTO commit_receipt_usage(shard) SELECT n FROM generate_series(0,255) n ON CONFLICT DO NOTHING;

-- A shard is locked even before its initial scan. The scanner takes the same
-- lock before reading receipts, so its snapshot and later deltas cannot overlap.
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

DROP TRIGGER IF EXISTS commit_receipt_usage_insert ON commit_receipts;
CREATE TRIGGER commit_receipt_usage_insert AFTER INSERT ON commit_receipts
 REFERENCING NEW TABLE AS new_receipts FOR EACH STATEMENT EXECUTE FUNCTION commit_receipt_usage_insert();
DROP TRIGGER IF EXISTS commit_receipt_usage_delete ON commit_receipts;
CREATE TRIGGER commit_receipt_usage_delete AFTER DELETE ON commit_receipts
 REFERENCING OLD TABLE AS old_receipts FOR EACH STATEMENT EXECUTE FUNCTION commit_receipt_usage_delete();
DROP TRIGGER IF EXISTS commit_receipt_usage_update ON commit_receipts;
CREATE TRIGGER commit_receipt_usage_update AFTER UPDATE OF id, payload ON commit_receipts
 FOR EACH ROW EXECUTE FUNCTION commit_receipt_usage_update();

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TRIGGER IF EXISTS commit_receipt_usage_update ON commit_receipts;
DROP TRIGGER IF EXISTS commit_receipt_usage_delete ON commit_receipts;
DROP TRIGGER IF EXISTS commit_receipt_usage_insert ON commit_receipts;
DROP FUNCTION IF EXISTS commit_receipt_usage_update();
DROP FUNCTION IF EXISTS commit_receipt_usage_delete();
DROP FUNCTION IF EXISTS commit_receipt_usage_insert();
DROP TABLE IF EXISTS commit_receipt_usage;
