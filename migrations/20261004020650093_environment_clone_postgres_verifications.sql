-- filename: 20261004020650093_environment_clone_postgres_verifications.sql

-- +goose Up
-- +goose StatementBegin
DO $verification$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='project_environment_clone_postgres_contents'::regclass AND conname='postgres_contents_verification_identity') THEN
  ALTER TABLE project_environment_clone_postgres_contents ADD CONSTRAINT postgres_contents_verification_identity UNIQUE(operation_id,source_database_id,database_oid,owner_id,ciphertext_sha256);
 END IF;
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='project_environment_clone_postgres_imports'::regclass AND conname='postgres_import_verification_identity') THEN
  ALTER TABLE project_environment_clone_postgres_imports ADD CONSTRAINT postgres_import_verification_identity UNIQUE(operation_id,source_database_id,database_oid,import_id);
 END IF;
END $verification$;
-- +goose StatementEnd

-- ADR-375: one private verification owner per already charged contents owner.
-- Compared evidence is persisted while native access is still open. Verified
-- additionally requires authenticated closure; neither grants stage readiness.
CREATE TABLE IF NOT EXISTS project_environment_clone_postgres_verifications (
 operation_id uuid NOT NULL,
 source_database_id uuid NOT NULL,
 database_oid bigint NOT NULL CHECK (database_oid BETWEEN 1 AND 4294967295),
 account_id uuid NOT NULL REFERENCES accounts(id),
 project_id uuid NOT NULL REFERENCES projects(id),
 verification_id uuid NOT NULL UNIQUE,
 scope jsonb NOT NULL CHECK (jsonb_typeof(scope)='object'),
 contents_owner_id uuid NOT NULL,
 contents_ciphertext_sha256 text NOT NULL CHECK (contents_ciphertext_sha256 ~ '^[0-9a-f]{64}$'),
 manifest_fingerprint text NOT NULL CHECK (manifest_fingerprint ~ '^[0-9a-f]{64}$'),
 import_id uuid NOT NULL,
 import_started_at timestamptz NOT NULL CHECK (isfinite(import_started_at)),
 database_sql_pins_ciphertext_sha256 text NOT NULL CHECK (database_sql_pins_ciphertext_sha256 ~ '^[0-9a-f]{64}$'),
 database_plan_ciphertext_sha256 text NOT NULL CHECK (database_plan_ciphertext_sha256 ~ '^[0-9a-f]{64}$'),
 archive_reservation_sha256 text NOT NULL CHECK (archive_reservation_sha256 ~ '^[0-9a-f]{64}$'),
 target_fingerprint text NOT NULL CHECK (target_fingerprint ~ '^[0-9a-f]{64}$'),
 key_id text NOT NULL CHECK (key_id ~ '^age1[0-9a-z]{58}$'),
 reserved_bytes bigint NOT NULL CHECK (reserved_bytes BETWEEN 1 AND 16384),
 state text NOT NULL DEFAULT 'reserved' CHECK (state IN ('reserved','verifying','compared','verified')),
 request_started_at timestamptz CHECK (isfinite(request_started_at)),
 window_opened_at timestamptz CHECK (isfinite(window_opened_at)),
 target_database_oid bigint CHECK (target_database_oid BETWEEN 1 AND 4294967295),
 fingerprint text CHECK (fingerprint ~ '^[0-9a-f]{64}$'),
 ciphertext bytea CHECK (octet_length(ciphertext)>0 AND octet_length(ciphertext)<=reserved_bytes),
 ciphertext_sha256 text CHECK (ciphertext_sha256 ~ '^[0-9a-f]{64}$'),
 compared_at timestamptz CHECK (isfinite(compared_at)),
 native_closed_at timestamptz CHECK (isfinite(native_closed_at)),
 verified_at timestamptz CHECK (isfinite(verified_at)),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK (isfinite(created_at)),
 PRIMARY KEY (operation_id,source_database_id,database_oid),
 FOREIGN KEY (operation_id,source_database_id,database_oid,contents_owner_id,contents_ciphertext_sha256)
  REFERENCES project_environment_clone_postgres_contents(operation_id,source_database_id,database_oid,owner_id,ciphertext_sha256),
 FOREIGN KEY (operation_id,source_database_id,database_oid,import_id)
  REFERENCES project_environment_clone_postgres_imports(operation_id,source_database_id,database_oid,import_id),
 FOREIGN KEY (operation_id,source_database_id,database_oid,database_sql_pins_ciphertext_sha256)
  REFERENCES project_environment_clone_postgres_database_sql_pins(operation_id,source_database_id,database_oid,ciphertext_sha256),
 CHECK (verification_id<>operation_id AND verification_id<>source_database_id AND verification_id<>contents_owner_id AND verification_id<>import_id),
 CHECK (request_started_at IS NULL OR request_started_at>=created_at),
 CHECK (compared_at IS NULL OR compared_at>=request_started_at),
 CHECK (verified_at IS NULL OR verified_at>=compared_at),
 CHECK (native_closed_at IS NULL OR native_closed_at>=window_opened_at),
 CHECK ((state='reserved' AND request_started_at IS NULL AND ciphertext IS NULL)
  OR (state='verifying' AND request_started_at IS NOT NULL AND ciphertext IS NULL)
  OR (state IN ('compared','verified') AND request_started_at IS NOT NULL AND window_opened_at IS NOT NULL AND target_database_oid IS NOT NULL
   AND fingerprint IS NOT NULL AND ciphertext IS NOT NULL AND ciphertext_sha256 IS NOT NULL AND compared_at IS NOT NULL)),
 CHECK ((ciphertext IS NULL AND fingerprint IS NULL AND ciphertext_sha256 IS NULL AND window_opened_at IS NULL AND target_database_oid IS NULL AND compared_at IS NULL)
  OR ciphertext IS NOT NULL),
 CHECK ((state='verified' AND native_closed_at IS NOT NULL AND verified_at IS NOT NULL)
  OR (state<>'verified' AND native_closed_at IS NULL AND verified_at IS NULL))
);

-- +goose Down
-- +goose StatementBegin
ALTER TABLE project_environment_clone_postgres_verifications ADD CONSTRAINT postgres_verifications_down_no_ownership CHECK (false);
DROP TABLE project_environment_clone_postgres_verifications;
ALTER TABLE project_environment_clone_postgres_imports DROP CONSTRAINT postgres_import_verification_identity;
ALTER TABLE project_environment_clone_postgres_contents DROP CONSTRAINT postgres_contents_verification_identity;
-- +goose StatementEnd
