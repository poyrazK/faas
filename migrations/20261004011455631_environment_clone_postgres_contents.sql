-- +goose Up
-- ADR-375: reserve and retain original independently captured contents evidence.
-- These owners never grant import execution, writer release or stage readiness.
-- +goose StatementBegin
DO $contents$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='project_environment_clone_postgres_inventories'::regclass AND conname='postgres_inventory_contents_identity') THEN
  ALTER TABLE project_environment_clone_postgres_inventories ADD CONSTRAINT postgres_inventory_contents_identity UNIQUE (operation_id,source_database_id,ciphertext_sha256);
 END IF;
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='project_environment_clone_postgres_archives'::regclass AND conname='postgres_archive_contents_identity') THEN
  ALTER TABLE project_environment_clone_postgres_archives ADD CONSTRAINT postgres_archive_contents_identity UNIQUE (operation_id,source_database_id,database_oid,owner_id);
 END IF;
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='project_environment_clone_postgres_copy_readers'::regclass AND conname='postgres_reader_contents_identity') THEN
  ALTER TABLE project_environment_clone_postgres_copy_readers ADD CONSTRAINT postgres_reader_contents_identity UNIQUE (operation_id,source_database_id,owner_id);
 END IF;
END $contents$;
-- +goose StatementEnd

CREATE TABLE IF NOT EXISTS project_environment_clone_postgres_contents (
 operation_id uuid NOT NULL,
 source_database_id uuid NOT NULL,
 database_oid bigint NOT NULL CHECK (database_oid BETWEEN 1 AND 4294967295),
 account_id uuid NOT NULL REFERENCES accounts(id),
 project_id uuid NOT NULL REFERENCES projects(id),
 owner_id uuid NOT NULL UNIQUE,
 scope jsonb NOT NULL CHECK (jsonb_typeof(scope)='object'),
 inventory_fingerprint text NOT NULL CHECK (inventory_fingerprint ~ '^[0-9a-f]{64}$'),
 inventory_ciphertext_sha256 text NOT NULL CHECK (inventory_ciphertext_sha256 ~ '^[0-9a-f]{64}$'),
 archive_owner_id uuid NOT NULL,
 archive_reservation_sha256 text NOT NULL CHECK (archive_reservation_sha256 ~ '^[0-9a-f]{64}$'),
 reader_owner_id uuid NOT NULL,
 reader_identity_sha256 text NOT NULL CHECK (reader_identity_sha256 ~ '^[0-9a-f]{64}$'),
 key_id text NOT NULL CHECK (key_id ~ '^age1[0-9a-z]{58}$'),
 reserved_bytes bigint NOT NULL CHECK (reserved_bytes>0),
 state text NOT NULL DEFAULT 'reserved' CHECK (state IN ('reserved','captured')),
 fingerprint text CHECK (fingerprint ~ '^[0-9a-f]{64}$'),
 ciphertext bytea CHECK (octet_length(ciphertext)>0 AND octet_length(ciphertext)<=reserved_bytes),
 ciphertext_sha256 text CHECK (ciphertext_sha256 ~ '^[0-9a-f]{64}$'),
 captured_at timestamptz CHECK (isfinite(captured_at)),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK (isfinite(created_at)),
 PRIMARY KEY (operation_id,source_database_id,database_oid),
 FOREIGN KEY (operation_id,source_database_id,inventory_ciphertext_sha256)
  REFERENCES project_environment_clone_postgres_inventories(operation_id,source_database_id,ciphertext_sha256),
 FOREIGN KEY (operation_id,source_database_id,database_oid,archive_owner_id)
  REFERENCES project_environment_clone_postgres_archives(operation_id,source_database_id,database_oid,owner_id),
 FOREIGN KEY (operation_id,source_database_id,reader_owner_id)
  REFERENCES project_environment_clone_postgres_copy_readers(operation_id,source_database_id,owner_id),
 CHECK (owner_id<>operation_id AND owner_id<>source_database_id AND owner_id<>archive_owner_id AND owner_id<>reader_owner_id),
 CHECK (captured_at IS NULL OR captured_at>=created_at),
 CHECK ((state='reserved' AND fingerprint IS NULL AND ciphertext IS NULL AND ciphertext_sha256 IS NULL AND captured_at IS NULL)
  OR (state='captured' AND fingerprint IS NOT NULL AND ciphertext IS NOT NULL AND ciphertext_sha256 IS NOT NULL AND captured_at IS NOT NULL))
);
CREATE INDEX IF NOT EXISTS postgres_contents_account_holds ON project_environment_clone_postgres_contents(account_id);

-- +goose Down
-- Even an undispatched contents reservation retains its charged ownership.
ALTER TABLE project_environment_clone_postgres_contents
 ADD CONSTRAINT postgres_contents_down_no_ownership CHECK (false);
DROP TABLE project_environment_clone_postgres_contents;
ALTER TABLE project_environment_clone_postgres_copy_readers DROP CONSTRAINT postgres_reader_contents_identity;
ALTER TABLE project_environment_clone_postgres_archives DROP CONSTRAINT postgres_archive_contents_identity;
ALTER TABLE project_environment_clone_postgres_inventories DROP CONSTRAINT postgres_inventory_contents_identity;
