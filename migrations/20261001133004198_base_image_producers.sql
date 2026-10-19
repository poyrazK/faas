-- +goose Up
-- adr: 393. Private immutable shared-base producer evidence, not runtime grants.
CREATE TABLE base_image_producers (
 id uuid PRIMARY KEY,
 storage_key text NOT NULL CHECK (storage_key ~ '^base/[^/]+[.]ext4$' AND length(storage_key)<=512),
 parent_producer_id uuid REFERENCES base_image_producers(id),
 input_snapshot jsonb NOT NULL CHECK (jsonb_typeof(input_snapshot)='object'),
 input_hash text NOT NULL CHECK (input_hash ~ '^[a-f0-9]{64}$'),
 published_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(id,storage_key),
 CHECK (input_snapshot->'artifact'->>'storage_key'=storage_key),
 CHECK (COALESCE(input_snapshot->>'parent_producer_id','')=COALESCE(parent_producer_id::text,'')),
 CHECK (parent_producer_id IS NULL OR parent_producer_id<>id)
);
CREATE TABLE base_image_producer_current (
 storage_key text PRIMARY KEY CHECK (storage_key ~ '^base/[^/]+[.]ext4$' AND length(storage_key)<=512),
 producer_id uuid NOT NULL,
 FOREIGN KEY(producer_id,storage_key) REFERENCES base_image_producers(id,storage_key)
);
-- +goose StatementBegin
CREATE FUNCTION base_image_producer_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='INSERT' AND current_setting('gregale.base_producer_insert',true)=NEW.id::text THEN RETURN NEW; END IF;
 RAISE EXCEPTION 'base producer evidence is immutable/private' USING ERRCODE='23514',CONSTRAINT='base_image_producer_immutable';
END;
$$;
CREATE FUNCTION base_image_producer_current_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP<>'DELETE' AND current_setting('gregale.base_producer_insert',true)=NEW.producer_id::text
   AND (TG_OP='INSERT' OR NEW.storage_key=OLD.storage_key) THEN RETURN NEW; END IF;
 RAISE EXCEPTION 'base producer selection is private' USING ERRCODE='23514',CONSTRAINT='base_image_producer_immutable';
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER base_producer_private_guard BEFORE INSERT OR UPDATE OR DELETE ON base_image_producers
 FOR EACH ROW EXECUTE FUNCTION base_image_producer_guard();
CREATE TRIGGER base_producer_current_private_guard BEFORE INSERT OR UPDATE OR DELETE ON base_image_producer_current
 FOR EACH ROW EXECUTE FUNCTION base_image_producer_current_guard();

-- +goose Down
DROP TABLE base_image_producer_current;
DROP TABLE base_image_producers;
DROP FUNCTION base_image_producer_current_guard();
DROP FUNCTION base_image_producer_guard();
